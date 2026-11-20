package provision

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/maintenance"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
	"github.com/ktcloud4-SL/labbit-app/internal/testsupport/workercontrolfake"
)

func trialMaintenance(t *testing.T) (string, *maintenance.Gate) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mode")
	trialMode(t, path, "closed")
	return path, maintenance.New(path)
}
func trialMode(t *testing.T, path, mode string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(mode), 0600); err != nil {
		t.Fatal(err)
	}
}
func withMaintenance(g *maintenance.Gate) func(*Options) {
	return func(o *Options) { o.Admission = g.Enter; o.ObserveReconciliation = g.ObserveReconciliation }
}

func approvedTrialResume(t *testing.T, mode wc.Mode, g *maintenance.Gate) (wc.Binding, *workercontrolfake.Store) {
	t.Helper()
	scope := wc.Scope{ID: "trial", Region: "region-fixture", Cluster: "cluster-fixture"}
	s := workercontrolfake.New(wc.State{Scope: scope, Revision: 1, Epoch: 1, Mode: wc.PauseNew})
	now := time.Now().UTC().Truncate(time.Microsecond)
	s.SetClock(func() time.Time { return now })
	r := wc.ResumeRequest{CommandID: uuid.New(), Scope: scope, ExpectedRevision: 1, ExpectedEpoch: 1, Desired: mode, TrialID: "trial", RunID: "run-new", Phase: "failover", Principal: "operator-fixture", ApprovalRef: "approval-fixture", PlanSHA256: strings.Repeat("a", 64), PreconditionsSHA256: strings.Repeat("b", 64), RequestedAt: now.Add(-time.Second), ValidUntil: now.Add(time.Minute)}
	p := wc.ResumeProof{Approval: wc.ResumeApproval{RequestSHA256: r.SHA256(), ID: uuid.New(), Principal: r.Principal, NotBefore: r.RequestedAt, ExpiresAt: r.ValidUntil}, Checks: wc.ResumeChecks{RequestSHA256: r.SHA256(), EvidenceSHA256: r.PreconditionsSHA256, ObservedAt: now, ValidUntil: r.ValidUntil, AdmissionBlocked: "PASS", SingleWriter: "PASS", Fencing: "PASS", WorkInventory: "PASS", PodCoverage: "PASS", WorkReconciled: "PASS"}}
	a := executorResumeAuth{p}
	bridge, err := wc.NewMaintenanceResumer(s, a, a, g, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if rec, err := bridge.Resume(t.Context(), r); err != nil || rec.Status != wc.ResumeApplied {
		t.Fatal(rec, err)
	}
	return wc.Binding{Scope: scope, Revision: 2, Epoch: 2, Mode: mode, PodID: "approved-pod", BootID: uuid.New()}, s
}

func TestMaintenanceApprovedResumeNeedsBothFileOpenAndNewBinding(t *testing.T) {
	for _, kind := range []string{"PROVISION", "RESET", "CLEANUP"} {
		t.Run(kind, func(t *testing.T) {
			path, g := trialMaintenance(t)
			b, s := approvedTrialResume(t, wc.Active, g)
			base := provisionTestStore(t, make(chan repository.ProvisionUpdate, 20))
			var sends atomic.Int32
			oldBinding := b
			oldBinding.Revision = 1
			oldBinding.Epoch = 1
			oldBinding.Mode = wc.PauseNew
			oldGate, _ := wc.NewDurable(oldBinding, s)
			old := controlledExecutor(t, kind, oldGate, base, provisionRouterFake{}, withMaintenance(g))
			gate, _ := wc.NewDurable(b, s)
			blocked := controlledExecutor(t, kind, gate, base, provisionRouterFake{}, withMaintenance(g))
			if claimed, err := blocked.RunOne(t.Context()); claimed || err != nil {
				t.Fatal("closed file admitted", err)
			}
			trialMode(t, path, "open") // A simulated external approved operator.
			if claimed, err := old.RunOne(t.Context()); claimed || !errors.Is(err, wc.ErrPaused) {
				t.Fatal("OPEN revived old worker", err)
			}
			var wg sync.WaitGroup
			for range 2 {
				b.BootID = uuid.New()
				c, err := wc.NewDurable(b, s)
				if err != nil {
					t.Fatal(err)
				}
				var e *Executor
				e = controlledExecutor(t, kind, c, base, provisionRouterFake{send: func(_ context.Context, cmd connector.OperationCommand) (connector.SentMessage, error) {
					sends.Add(1)
					e.HandleEvent(connector.OperationResultEvent{Routed: routedFor(cmd), Payload: protocol.OperationResultPayload{Outcome: "SUCCEEDED"}})
					return connector.SentMessage{MessageID: "outbound"}, nil
				}}, withMaintenance(g))
				wg.Go(func() {
					if _, err := e.RunOne(t.Context()); err != nil {
						t.Error(err)
					}
				})
			}
			wg.Wait()
			if sends.Load() != 1 {
				t.Fatalf("dispatch=%d", sends.Load())
			}
			_, counts := g.Snapshot()
			if counts["worker"] != 0 {
				t.Fatal("worker lifetime leaked")
			}
		})
	}
}

func TestMaintenanceDrainAndExplicitDispatchPauseAreSeparate(t *testing.T) {
	for _, hardPause := range []bool{false, true} {
		t.Run(map[bool]string{false: "finish-preclaim", true: "hold-before-dispatch"}[hardPause], func(t *testing.T) {
			path, g := trialMaintenance(t)
			trialMode(t, path, "open")
			base := provisionTestStore(t, make(chan repository.ProvisionUpdate, 20))
			claim := base.claim
			claimed, continueClaim := make(chan struct{}), make(chan struct{})
			base.claim = func(ctx context.Context, o uuid.UUID, d time.Duration) (repository.OperationItemClaim, error) {
				c, err := claim(ctx, o, d)
				close(claimed)
				<-continueClaim
				return c, err
			}
			control := controlFor(t, wc.Active)
			var sends int
			var e *Executor
			e = controlledExecutor(t, "PROVISION", control, base, provisionRouterFake{send: func(_ context.Context, c connector.OperationCommand) (connector.SentMessage, error) {
				sends++
				e.HandleEvent(connector.OperationResultEvent{Routed: routedFor(c), Payload: protocol.OperationResultPayload{Outcome: "SUCCEEDED"}})
				return connector.SentMessage{MessageID: "sent"}, nil
			}}, withMaintenance(g))
			done := make(chan error, 1)
			go func() { _, err := e.RunOne(t.Context()); done <- err }()
			<-claimed
			trialMode(t, path, "closed")
			_, counts := g.Snapshot()
			if counts["worker"] != 1 || g.LocalResumeReady() {
				t.Fatal("inflight preclaim lost")
			}
			var paused chan error
			if hardPause {
				paused = make(chan error, 1)
				go func() { paused <- e.Pause(t.Context()) }()
				waitForLocalPause(t, control)
			}
			close(continueClaim)
			err := <-done
			if hardPause {
				if !errors.Is(err, wc.ErrPaused) || sends != 0 {
					t.Fatalf("pause=%v sends=%d", err, sends)
				}
				if err := <-paused; err != nil {
					t.Fatal(err)
				}
			} else if err != nil || sends != 1 {
				t.Fatalf("drain=%v sends=%d", err, sends)
			}
			if !g.LocalResumeReady() {
				t.Fatal("lifetime not released")
			}
		})
	}
}

func TestMaintenanceClosedReconciliationUsesOnlyApprovedPersistentBinding(t *testing.T) {
	_, g := trialMaintenance(t)
	b, s := approvedTrialResume(t, wc.ReconcileOnly, g)
	c, err := wc.NewDurable(b, s)
	if err != nil {
		t.Fatal(err)
	}
	base := provisionTestStore(t, make(chan repository.ProvisionUpdate, 10))
	base.claim = func(context.Context, uuid.UUID, time.Duration) (repository.OperationItemClaim, error) {
		t.Error("mutation queue claimed")
		return repository.OperationItemClaim{}, repository.ErrNotFound
	}
	base.renew = func(context.Context, repository.OperationItemLease, time.Duration) (time.Time, error) {
		return time.Time{}, repository.ErrOperationLeaseLost
	}
	var queries, records int
	var e *Executor
	recon := controlledReconciliationStore{claim: func(_ context.Context, owner uuid.UUID, _ time.Duration) (repository.OperationItemClaim, error) {
		return repository.OperationItemClaim{Operation: repository.Operation{Type: "PROVISION"}, Item: repository.OperationItem{Status: "RECONCILING"}, Lease: repository.OperationItemLease{ItemID: uuid.New(), Owner: owner}}, nil
	}, prepare: base.prepare, write: func(context.Context, repository.ReconciliationUpdate) error {
		records++
		_, counts := g.Snapshot()
		if counts["reconciliation"] != 1 || g.LocalResumeReady() {
			t.Error("result write not tracked")
		}
		return nil
	}}
	opts := Options{Store: base, Reconciliation: recon, WorkerControl: c, Admission: g.Enter, ObserveReconciliation: g.ObserveReconciliation, Router: controlledReconciliationRouter{provisionRouterFake: provisionRouterFake{send: func(context.Context, connector.OperationCommand) (connector.SentMessage, error) {
		t.Error("mutation sent")
		return connector.SentMessage{}, nil
	}}, query: func(_ context.Context, r connector.ReconcileRequest) (connector.SentMessage, error) {
		queries++
		e.HandleEvent(connector.ReconcileResultEvent{Routed: connector.Routed{ConnectorID: r.ConnectorID, Correlation: r.Correlation, MessageID: "response", RequestMessageID: "query"}, Payload: protocol.ReconcileResultPayload{Error: &protocol.SafeError{Code: "uncertain", Message: "not verified"}}})
		return connector.SentMessage{MessageID: "query"}, nil
	}}}
	e, err = NewExecutor(opts)
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := e.RunOne(t.Context()); !claimed || err != nil {
		t.Fatal("approved query blocked", err)
	}
	if queries != 1 || records != 1 || !g.LocalResumeReady() {
		t.Fatalf("queries=%d records=%d", queries, records)
	}
	if _, ok := g.Enter("http"); ok {
		t.Fatal("query permission opened API")
	}
	if _, err := c.AcquireMutation(t.Context()); !errors.Is(err, wc.ErrPaused) {
		t.Fatal("query permission mutated", err)
	}
	opts.WorkerControl = controlFor(t, wc.ReconcileOnly)
	if _, err := NewExecutor(opts); !errors.Is(err, wc.ErrInvalidBinding) {
		t.Fatal("local mode bypassed file", err)
	}
	stale := b
	stale.Revision--
	staleControl, _ := wc.NewDurable(stale, s)
	opts.WorkerControl = staleControl
	e, err = NewExecutor(opts)
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := e.RunOne(t.Context()); claimed || err == nil {
		t.Fatal("stale query claimed")
	}
	if queries != 1 || records != 1 {
		t.Fatal("stale query side effect")
	}
}
