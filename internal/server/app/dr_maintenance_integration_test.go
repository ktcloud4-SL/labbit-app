//go:build integration

package app

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/maintenance"
	"github.com/ktcloud4-SL/labbit-app/internal/server/provision"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

// These are two application stacks with separate local gates and process
// bindings, not Kubernetes Pods. Only PostgreSQL and loopback HTTP/WSS are real;
// the Connector uses MockProvider. No operational approval or Fencing is proved.
type drMaintenancePair struct {
	a       *workerMetricsEnv
	b       *controlStack
	path    string
	gates   [2]*maintenance.Gate
	journal *postgres.WorkerControlStore
}

func newDRMaintenancePair(t *testing.T, durable bool) *drMaintenancePair {
	t.Helper()
	p := &drMaintenancePair{path: filepath.Join(t.TempDir(), "mode")}
	p.mode(t, "open")
	p.gates = [2]*maintenance.Gate{maintenance.New(p.path), maintenance.New(p.path)}
	var options stackOptions
	p.a = newWorkerMetricsEnv(t, 5*time.Second, func(o *stackOptions, dsn string) {
		o.Maintenance = p.gates[0]
		o.ProvisionLeaseDuration = time.Minute
		if durable {
			pool, err := postgres.OpenPool(t.Context(), dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(pool.Close)
			p.journal = postgres.NewWorkerControlStore(pool)
			scope := wc.Scope{ID: "dr-integration", Region: "region-fixture", Cluster: "cluster-fixture"}
			// Existing ACTIVE Writer fixture, not an operational resume shortcut.
			if _, err := pool.Exec(t.Context(), `INSERT INTO worker_control_states (control_id,region,cluster,revision,epoch,mode) VALUES ($1,$2,$3,1,1,'ACTIVE')`, scope.ID, scope.Region, scope.Cluster); err != nil {
				t.Fatal(err)
			}
			o.WorkerControlSettings = wc.Settings{Backend: "postgres", Scope: scope, Revision: 1, Epoch: 1}
			o.WorkerControlStore = p.journal
			o.WorkerControlPodID = "stack-a"
		}
		options = *o
	})
	options.Maintenance = p.gates[1]
	options.OperationSink = nil
	options.WorkerMetrics = nil
	if durable {
		options.WorkerControlPodID = "stack-b"
	}
	var err error
	p.b, err = newControlStack(p.a.store, options)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(applicationHandler(p.b.routes()))
	t.Cleanup(func() {
		p.b.close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := p.b.shutdown(ctx); err != nil {
			t.Error(err)
		}
		server.Close()
	})
	return p
}

func (p *drMaintenancePair) mode(t *testing.T, mode string) {
	t.Helper()
	if err := os.WriteFile(p.path, []byte(mode), 0600); err != nil {
		t.Fatal(err)
	}
}

// Whole business rows are compared: status alone would miss a stolen lease or
// changed dispatch correlation. Only the disposable database is inspected.
func (p *drMaintenancePair) items(t *testing.T) string {
	t.Helper()
	var rows string
	if err := p.a.db.QueryRow(t.Context(), `SELECT COALESCE(jsonb_agg(to_jsonb(i) ORDER BY i.id),'[]'::jsonb)::text FROM operation_items i`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func drRunOne(t *testing.T, e *provision.Executor) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	if claimed, err := e.RunOne(ctx); !claimed || err != nil {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
}

type drWorkerRun struct {
	done    chan struct{}
	claimed bool
	err     error
}

// Join on both the happy path and t.Fatal cleanup before the stack/database
// cleanups run. The worker goroutine never calls testing.Fatal itself.
func drStartWorker(t *testing.T, e *provision.Executor, unblock func()) *drWorkerRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	r := &drWorkerRun{done: make(chan struct{})}
	go func() {
		defer close(r.done)
		r.claimed, r.err = e.RunOne(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		unblock()
		select {
		case <-r.done:
		case <-time.After(10 * time.Second):
			t.Error("worker did not exit before stack/database cleanup")
		}
	})
	return r
}

func (r *drWorkerRun) wait(t *testing.T) (bool, error) {
	t.Helper()
	select {
	case <-r.done:
		return r.claimed, r.err
	case <-time.After(9 * time.Second):
		t.Fatal("worker timed out")
		return false, context.DeadlineExceeded
	}
}

// Baseline #194 semantics must be exercised with WorkerControl ACTIVE: adding
// #196 Pause here would mask a broken maintenance claim gate and stop drain.
func TestDRMaintenanceBacklogAndDrainAgainstPostgres(t *testing.T) {
	for _, durable := range []bool{false, true} {
		for _, kind := range []string{"PROVISION", "RESET", "CLEANUP"} {
			t.Run(fmt.Sprintf("durable=%v/%s", durable, kind), func(t *testing.T) {
				p := newDRMaintenancePair(t, durable)
				var calls atomic.Int32
				var hold atomic.Bool
				entered, release := make(chan string, 1), make(chan struct{})
				var once sync.Once
				unblock := func() { once.Do(func() { close(release) }) }
				wait := func(ctx context.Context, instanceID string) error {
					calls.Add(1)
					if !hold.Load() {
						return nil
					}
					select {
					case entered <- instanceID:
					default:
					}
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				p.a.startMock(&provider.MockProvider{
					ProvisionFunc: func(ctx context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
						if err := wait(ctx, r.LabInstanceID); err != nil {
							return provider.OperationResult{}, err
						}
						return mockProvisionResult(r, provider.OutcomeSucceeded), nil
					},
					ResetFunc: func(ctx context.Context, r provider.ResetRequest) (provider.OperationResult, error) {
						if err := wait(ctx, r.LabInstanceID); err != nil {
							return provider.OperationResult{}, err
						}
						v := mockProvisionResult(provider.ProvisionRequest{Correlation: r.Correlation, CreationSnapshot: r.CreationSnapshot}, provider.OutcomeSucceeded)
						for i := range v.ProviderResources {
							v.ProviderResources[i].ProviderID += "/reset"
						}
						v.ProviderResources = append(deletedRefs(r.ProviderResources), v.ProviderResources...)
						return v, nil
					},
					CleanupFunc: func(ctx context.Context, r provider.CleanupRequest) (provider.OperationResult, error) {
						if err := wait(ctx, r.LabInstanceID); err != nil {
							return provider.OperationResult{}, err
						}
						return provider.OperationResult{Outcome: provider.OutcomeSucceeded, ProviderResources: deletedRefs(r.ProviderResources)}, nil
					},
				})
				t.Cleanup(unblock)
				if kind != "PROVISION" {
					id := p.a.seedMutation()
					if kind == "RESET" {
						p.a.reset(id, "reset-dr-integration")
					} else {
						p.a.cleanup("cleanup-dr-integration")
					}
				}
				// The queue is nonempty before closed. Neither stack may claim it.
				var pending int
				if err := p.a.db.QueryRow(t.Context(), `SELECT count(*) FROM operation_items WHERE operation_id=$1 AND status='PENDING'`, p.a.opID).Scan(&pending); err != nil || pending == 0 {
					t.Fatalf("missing backlog: %d/%v", pending, err)
				}
				p.mode(t, "closed")
				before, n := p.items(t), calls.Load()
				for range 3 {
					for _, e := range []*provision.Executor{p.a.stack.Provisions, p.b.Provisions} {
						if claimed, err := e.RunOne(t.Context()); claimed || err != nil {
							t.Fatalf("closed claim=%v/%v", claimed, err)
						}
					}
				}
				if p.items(t) != before || calls.Load() != n {
					t.Fatal("closed backlog mutated or dispatched")
				}
				for _, g := range p.gates {
					closed, counts := g.Snapshot()
					if !closed || counts["worker"] != 0 {
						t.Fatalf("gate=%v/%v", closed, counts)
					}
				}
				// Positive control: same queued work reaches a connected Provider.
				p.mode(t, "open")
				hold.Store(true)
				run := drStartWorker(t, p.a.stack.Provisions, unblock)
				var inFlight string
				select {
				case inFlight = <-entered:
				case <-time.After(6 * time.Second):
					t.Fatal("open positive control never dispatched")
				}
				p.mode(t, "closed")
				if _, counts := p.gates[0].Snapshot(); counts["worker"] != 1 {
					t.Fatalf("preaccepted lifetime not tracked: %v", counts)
				}
				if p.gates[0].LocalResumeReady() {
					t.Fatal("in-flight work reported drained")
				}
				// ACK is sent before Provider entry but persisted asynchronously.
				// Wait for this exact item before comparing otherwise stable rows.
				stage := map[string]string{"PROVISION": "PROVISIONING", "RESET": "RESETTING", "CLEANUP": "CLEANING_UP"}[kind]
				eventually(t, "in-flight ACK persisted", 3*time.Second, func() bool {
					var count int
					err := p.a.db.QueryRow(t.Context(), `SELECT count(*) FROM operation_items WHERE operation_id=$1 AND lab_instance_id=$2 AND status='RUNNING' AND stage=$3 AND finished_at IS NULL`, p.a.opID, inFlight, stage).Scan(&count)
					return err == nil && count == 1
				})
				before, n = p.items(t), calls.Load()
				for _, e := range []*provision.Executor{p.a.stack.Provisions, p.b.Provisions} {
					if claimed, err := e.RunOne(t.Context()); claimed || err != nil {
						t.Fatalf("new claim while draining=%v/%v", claimed, err)
					}
				}
				if p.items(t) != before || calls.Load() != n {
					t.Fatal("new work entered during drain")
				}
				unblock()
				if claimed, err := run.wait(t); !claimed || err != nil {
					t.Fatalf("preaccepted drain=%v/%v", claimed, err)
				}
				var succeeded int
				if err := p.a.db.QueryRow(t.Context(), `SELECT count(*) FROM operation_items WHERE operation_id=$1 AND status='SUCCEEDED'`, p.a.opID).Scan(&succeeded); err != nil || succeeded != 1 {
					t.Fatalf("result not committed: %d/%v", succeeded, err)
				}
				if !p.gates[0].LocalResumeReady() || !p.gates[1].LocalResumeReady() {
					t.Fatal("drain lifetime leaked")
				}
				// Explicit reopen processes only remaining pending items; no replay.
				p.mode(t, "open")
				hold.Store(false)
				for range pending - 1 {
					drRunOne(t, p.a.stack.Provisions)
				}
				if calls.Load() != n+int32(pending-1) {
					t.Fatal("duplicate dispatch or lost pending work")
				}
				var status string
				if err := p.a.db.QueryRow(t.Context(), `SELECT status FROM operations WHERE id=$1`, p.a.opID).Scan(&status); err != nil || status != "SUCCEEDED" {
					t.Fatalf("final operation=%s/%v", status, err)
				}
			})
		}
	}
}

type drClaimWindow struct {
	*postgres.Store
	committed chan repository.OperationItemClaim
	release   <-chan struct{}
}

func (s drClaimWindow) ClaimOperationItem(ctx context.Context, owner uuid.UUID, d time.Duration) (repository.OperationItemClaim, error) {
	c, err := s.Store.ClaimOperationItem(ctx, owner, d)
	if err != nil {
		return c, err
	}
	select {
	case s.committed <- c:
	case <-ctx.Done():
		return c, ctx.Err()
	}
	select {
	case <-s.release:
		return c, nil
	case <-ctx.Done():
		return c, ctx.Err()
	}
}

// #196's hard pause is a different phase from #194 graceful drain. A real DB
// claim has committed, but its response has not reached the Executor yet.
func TestDRMaintenancePeerPauseAfterCommittedClaimAgainstPostgres(t *testing.T) {
	p := newDRMaintenancePair(t, true)
	var calls atomic.Int32
	p.a.startMock(&provider.MockProvider{ProvisionFunc: func(_ context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
		calls.Add(1)
		return mockProvisionResult(r, provider.OutcomeSucceeded), nil
	}})
	scope := wc.Scope{ID: "dr-integration", Region: "region-fixture", Cluster: "cluster-fixture"}
	control, err := wc.NewDurable(wc.Binding{Scope: scope, Revision: 1, Epoch: 1, Mode: wc.Active, PodID: "claim-stack", BootID: uuid.New()}, p.journal)
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	window := drClaimWindow{Store: p.a.store, committed: make(chan repository.OperationItemClaim, 1), release: release}
	executor, err := provision.NewExecutor(provision.Options{Store: p.a.store, Mutations: window, Router: p.a.stack.Router, WorkerControl: control, Admission: p.gates[0].Enter, LeaseDuration: time.Minute, ResultTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	run := drStartWorker(t, executor, unblock)
	var claim repository.OperationItemClaim
	select {
	case claim = <-window.committed:
	case <-time.After(5 * time.Second):
		t.Fatal("claim missing")
	}
	p.mode(t, "closed")
	if err := p.b.Provisions.Pause(t.Context()); !errors.Is(err, wc.ErrHold) {
		t.Fatalf("in-flight peer pause=%v", err)
	}
	// Release is after peer PAUSE_NEW commits, so no timing-dependent sleep.
	unblock()
	if _, err := run.wait(t); !errors.Is(err, wc.ErrStale) {
		t.Fatalf("late claim dispatch result=%v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("paused claim reached Provider")
	}
	var status string
	var owner uuid.UUID
	if err := p.a.db.QueryRow(t.Context(), `SELECT status,lease_owner FROM operation_items WHERE id=$1`, claim.Item.ID).Scan(&status, &owner); err != nil || status != "RUNNING" || owner != claim.Lease.Owner {
		t.Fatalf("claim lost or auto-requeued: %s/%v/%v", status, owner, err)
	}
	p.mode(t, "open")
	before := p.items(t)
	for _, e := range []*provision.Executor{p.a.stack.Provisions, p.b.Provisions} {
		if claimed, err := e.RunOne(t.Context()); claimed || err == nil {
			t.Fatal("opening revived stale binding")
		}
	}
	if p.items(t) != before || calls.Load() != 0 {
		t.Fatal("stale worker changed queue")
	}
}

func TestDRMaintenanceSentUnknownSurvivesPeerPauseAgainstPostgres(t *testing.T) {
	p := newDRMaintenancePair(t, true)
	entered, release := make(chan struct{}, 1), make(chan struct{})
	var calls atomic.Int32
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	p.a.startMock(&provider.MockProvider{ProvisionFunc: func(ctx context.Context, _ provider.ProvisionRequest) (provider.OperationResult, error) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			return provider.OperationResult{Outcome: provider.OutcomeUnknown}, nil
		case <-ctx.Done():
			return provider.OperationResult{}, ctx.Err()
		}
	}})
	t.Cleanup(unblock)
	run := drStartWorker(t, p.a.stack.Provisions, unblock)
	select {
	case <-entered:
	case <-time.After(6 * time.Second):
		t.Fatal("Provider never entered")
	}
	p.mode(t, "closed")
	if err := p.b.Provisions.Pause(t.Context()); err != nil && !errors.Is(err, wc.ErrHold) {
		t.Fatal(err)
	}
	scope := wc.Scope{ID: "dr-integration", Region: "region-fixture", Cluster: "cluster-fixture"}
	eventually(t, "short boundaries drained", 3*time.Second, func() bool { r, err := p.journal.Report(t.Context(), scope); return err == nil && r.Drained() })
	if _, counts := p.gates[0].Snapshot(); counts["worker"] != 1 || p.gates[0].LocalResumeReady() {
		t.Fatal("boundary0 erased sent work lifetime")
	}
	unblock()
	if claimed, err := run.wait(t); !claimed || err != nil {
		t.Fatalf("sent result after pause=%v/%v", claimed, err)
	}
	var unknown int
	if err := p.a.db.QueryRow(t.Context(), `SELECT count(*) FROM operation_items WHERE operation_id=$1 AND status='RECONCILING'`, p.a.opID).Scan(&unknown); err != nil || unknown != 1 {
		t.Fatalf("UNKNOWN not preserved: %d/%v", unknown, err)
	}
	p.mode(t, "open")
	before := p.items(t)
	for _, e := range []*provision.Executor{p.a.stack.Provisions, p.b.Provisions} {
		if claimed, err := e.RunOne(t.Context()); claimed || err == nil {
			t.Fatal("paused worker replayed UNKNOWN")
		}
	}
	if before != p.items(t) || calls.Load() != 1 {
		t.Fatal("UNKNOWN retried or altered")
	}
}

// Test-only trusted ports: they bind synthetic proof to one exact request.
// This is not an implementation of operational authentication or Fencing.
type drResumeFixture struct{ proof wc.ResumeProof }

func (f drResumeFixture) AuthorizeResume(_ context.Context, r wc.ResumeRequest) (wc.ResumeApproval, error) {
	if r.SHA256() != f.proof.Approval.RequestSHA256 {
		return wc.ResumeApproval{}, wc.ErrApproval
	}
	return f.proof.Approval, nil
}
func (f drResumeFixture) RecheckResume(_ context.Context, r wc.ResumeRequest) (wc.ResumeChecks, error) {
	if r.SHA256() != f.proof.Checks.RequestSHA256 {
		return wc.ResumeChecks{}, wc.ErrUnavailable
	}
	return f.proof.Checks, nil
}

func TestDRMaintenanceResumeRequiresNewBindingAndExplicitOpenAgainstPostgres(t *testing.T) {
	testDRMaintenanceResumeAdmissionPolicy(t, "MAINTENANCE")
}

func TestDRMaintenanceApprovedControlProcessesBacklogWhileHTTPClosedAgainstPostgres(t *testing.T) {
	testDRMaintenanceResumeAdmissionPolicy(t, "APPROVED_CONTROL")
}

func testDRMaintenanceResumeAdmissionPolicy(t *testing.T, policy string) {
	p := newDRMaintenancePair(t, true)
	p.mode(t, "closed")
	if err := p.b.Provisions.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
	scope := wc.Scope{ID: "dr-integration", Region: "region-fixture", Cluster: "cluster-fixture"}
	report, err := p.journal.Report(t.Context(), scope)
	if err != nil || !report.Drained() {
		t.Fatalf("paused report=%v/%v", report, err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	r := wc.ResumeRequest{CommandID: uuid.New(), Scope: scope, ExpectedRevision: report.State.Revision, ExpectedEpoch: report.State.Epoch, Desired: wc.Active, TrialID: "dr-integration", RunID: "resume-new-run", Phase: "trial", Principal: "operator-fixture", ApprovalRef: "synthetic-approval", PlanSHA256: strings.Repeat("a", 64), PreconditionsSHA256: strings.Repeat("b", 64), RequestedAt: now.Add(-time.Second), ValidUntil: now.Add(time.Minute)}
	proof := wc.ResumeProof{Approval: wc.ResumeApproval{RequestSHA256: r.SHA256(), ID: uuid.New(), Principal: r.Principal, NotBefore: r.RequestedAt, ExpiresAt: r.ValidUntil}, Checks: wc.ResumeChecks{RequestSHA256: r.SHA256(), EvidenceSHA256: r.PreconditionsSHA256, ObservedAt: now, ValidUntil: r.ValidUntil, AdmissionBlocked: "PASS", SingleWriter: "PASS", Fencing: "PASS", WorkInventory: "PASS", PodCoverage: "PASS", WorkReconciled: "PASS"}}
	f := drResumeFixture{proof}
	resumer, err := wc.NewMaintenanceResumer(p.journal, f, f, p.gates[0], time.Now)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := resumer.Resume(t.Context(), r)
	if err != nil || receipt.Status != wc.ResumeApplied {
		t.Fatalf("synthetic resume=%v/%v", receipt, err)
	}
	// Recreate a production stack with the exact new revision/epoch and boot.
	next := r.NextState()
	gate := maintenance.New(p.path)
	stack, err := newControlStack(p.a.store, stackOptions{Worker: true, PublicOrigin: publicOrigin, Maintenance: gate, WorkerControlSettings: wc.Settings{Backend: "postgres", AdmissionPolicy: policy, Scope: scope, Revision: next.Revision, Epoch: next.Epoch}, WorkerControlStore: p.journal, WorkerControlMode: wc.Active, WorkerControlPodID: "new-approved-stack", ProvisionLeaseDuration: time.Minute, ProvisionResultTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(applicationHandler(stack.routes()))
	t.Cleanup(func() {
		stack.close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := stack.shutdown(ctx); err != nil {
			t.Error(err)
		}
		server.Close()
	})
	var calls atomic.Int32
	startRoutingConnector(t, &lifecycleEnv{url: server.URL, registry: stack.Registry, router: stack.Router, connectorID: p.a.f.ConnectorID}, &provider.MockProvider{ProvisionFunc: func(_ context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
		calls.Add(1)
		return mockProvisionResult(r, provider.OutcomeSucceeded), nil
	}})
	before := p.items(t)
	if policy == "MAINTENANCE" {
		if claimed, err := stack.Provisions.RunOne(t.Context()); claimed || err != nil {
			t.Fatal("APPLIED opened maintenance file")
		}
		if p.items(t) != before || calls.Load() != 0 {
			t.Fatal("closed new binding changed backlog")
		}
		p.mode(t, "open")
		for _, e := range []*provision.Executor{p.a.stack.Provisions, p.b.Provisions} {
			if claimed, err := e.RunOne(t.Context()); claimed || err == nil {
				t.Fatal("APPLIED revived old binding")
			}
		}
		if p.items(t) != before {
			t.Fatal("old binding changed work")
		}
	} else {
		if _, ok := gate.Enter("http"); ok {
			t.Fatal("new HTTP admission opened")
		}
		if _, err := p.a.stack.Router.SendOperationCommand(t.Context(), connector.OperationCommand{}); err == nil {
			t.Fatal("old Router binding accepted")
		}
	}
	for range 2 {
		drRunOne(t, stack.Provisions)
	}
	if calls.Load() != 2 {
		t.Fatalf("new binding dispatch=%d", calls.Load())
	}
	// Repeating the command is historical readback, not another activation.
	again, err := resumer.Resume(t.Context(), r)
	if err != nil || again.Status != wc.ResumeApplied || !again.AppliedAt.Equal(receipt.AppliedAt) {
		t.Fatalf("receipt replay=%s err=%v applied=%v original=%v", again.Status, err, again.AppliedAt, receipt.AppliedAt)
	}
	if calls.Load() != 2 {
		t.Fatal("receipt replay dispatched")
	}
}
