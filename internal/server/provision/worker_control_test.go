package provision

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
	"github.com/ktcloud4-SL/labbit-app/internal/server/valkeyowner"
	"github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
	"github.com/ktcloud4-SL/labbit-app/internal/testsupport/workercontrolfake"
)

func controlFor(t *testing.T, mode workercontrol.Mode) *workercontrol.Control {
	t.Helper()
	c, err := workercontrol.New(mode)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func waitForLocalPause(t *testing.T, c *workercontrol.Control) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	for c.Snapshot().Mode != workercontrol.PauseNew {
		if ctx.Err() != nil {
			t.Fatal("pause not latched")
		}
		runtime.Gosched()
	}
}

func controlledExecutor(t *testing.T, kind string, c *workercontrol.Control, base provisionStoreFake, router Router, configure ...func(*Options)) *Executor {
	t.Helper()
	claim := base.claim
	base.claim = func(ctx context.Context, owner uuid.UUID, duration time.Duration) (repository.OperationItemClaim, error) {
		c, err := claim(ctx, owner, duration)
		c.Operation.Type = kind
		return c, err
	}
	opts := Options{Store: base, Router: router, WorkerControl: c, ResultTimeout: 30 * time.Millisecond}
	if kind != "PROVISION" {
		opts.Mutations = mutationStoreFake{base, kind}
	}
	for _, f := range configure {
		f(&opts)
	}
	p, err := NewExecutor(opts)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWorkerControlPauseCreatesNoClaimsOrSideEffects(t *testing.T) {
	for _, kind := range []string{"PROVISION", "RESET", "CLEANUP"} {
		t.Run(kind, func(t *testing.T) {
			writes := make(chan repository.ProvisionUpdate, 10)
			base := provisionTestStore(t, writes)
			base.claim = func(context.Context, uuid.UUID, time.Duration) (repository.OperationItemClaim, error) {
				t.Error("DB claim entered")
				return repository.OperationItemClaim{}, repository.ErrNotFound
			}
			base.prepare = func(context.Context, repository.OperationItemLease) (repository.ProvisionDispatch, error) {
				t.Error("prepare entered")
				return repository.ProvisionDispatch{}, nil
			}
			p := controlledExecutor(t, kind, controlFor(t, workercontrol.PauseNew), base, provisionRouterFake{send: func(context.Context, connector.OperationCommand) (connector.SentMessage, error) {
				t.Error("dispatch entered")
				return connector.SentMessage{}, nil
			}})
			for range 2 {
				if claimed, err := p.RunOne(t.Context()); claimed || !errors.Is(err, workercontrol.ErrPaused) {
					t.Fatalf("run=%v/%v", claimed, err)
				}
			}
			if len(writes) != 0 || !p.ControlState().Quiesced {
				t.Fatal("pause changed durable state")
			}
			// Reconstructing from the same explicit startup mode remains paused.
			restarted := controlledExecutor(t, kind, controlFor(t, workercontrol.PauseNew), base, p.opts.Router)
			if claimed, err := restarted.RunOne(t.Context()); claimed || !errors.Is(err, workercontrol.ErrPaused) {
				t.Fatal("restart resumed mutation")
			}
		})
	}
}

func TestWorkerControlDelayedClaimCannotDispatchAfterPause(t *testing.T) {
	for _, kind := range []string{"PROVISION", "RESET", "CLEANUP"} {
		t.Run(kind, func(t *testing.T) {
			writes := make(chan repository.ProvisionUpdate, 10)
			base := provisionTestStore(t, writes)
			claim := base.claim
			committed, releaseResponse := make(chan struct{}), make(chan struct{})
			base.claim = func(ctx context.Context, owner uuid.UUID, d time.Duration) (repository.OperationItemClaim, error) {
				c, err := claim(ctx, owner, d)
				c.Operation.Type = kind
				close(committed)
				<-releaseResponse
				return c, err
			}
			base.prepare = func(context.Context, repository.OperationItemLease) (repository.ProvisionDispatch, error) {
				t.Error("prepared after pause")
				return repository.ProvisionDispatch{}, nil
			}
			c := controlFor(t, workercontrol.Active)
			p := controlledExecutor(t, kind, c, base, provisionRouterFake{send: func(context.Context, connector.OperationCommand) (connector.SentMessage, error) {
				t.Error("sent after pause")
				return connector.SentMessage{}, nil
			}})
			done := make(chan error, 1)
			go func() {
				claimed, err := p.RunOne(t.Context())
				if !claimed {
					t.Error("committed claim hidden")
				}
				done <- err
			}()
			<-committed
			paused := make(chan error, 1)
			go func() { paused <- p.Pause(t.Context()) }()
			waitForLocalPause(t, c)
			if c.Snapshot().Quiesced {
				t.Fatal("pause ignored commit response in flight")
			}
			close(releaseResponse)
			if err := <-paused; err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, workercontrol.ErrPaused) {
				t.Fatalf("execute=%v", err)
			}
			if len(writes) != 0 || !c.Snapshot().Quiesced {
				t.Fatal("claim was requeued/canceled")
			}
		})
	}
}

func TestWorkerControlPauseDuringPreparationPreventsDispatchAndRequeue(t *testing.T) {
	for _, kind := range []string{"PROVISION", "RESET", "CLEANUP"} {
		t.Run(kind, func(t *testing.T) {
			writes := make(chan repository.ProvisionUpdate, 10)
			base := provisionTestStore(t, writes)
			prepare := base.prepare
			entered, releasePrepare := make(chan struct{}), make(chan struct{})
			base.prepare = func(ctx context.Context, l repository.OperationItemLease) (repository.ProvisionDispatch, error) {
				close(entered)
				<-releasePrepare
				return prepare(ctx, l)
			}
			base.deferUnsent = func(context.Context, repository.ProvisionDispatch, time.Duration) error {
				t.Error("pause auto-requeued mutation")
				return nil
			}
			c := controlFor(t, workercontrol.Active)
			p := controlledExecutor(t, kind, c, base, provisionRouterFake{send: func(context.Context, connector.OperationCommand) (connector.SentMessage, error) {
				t.Error("send after preparation pause")
				return connector.SentMessage{}, nil
			}})
			done := make(chan error, 1)
			go func() { _, err := p.RunOne(t.Context()); done <- err }()
			<-entered
			ctx, cancel := context.WithCancel(t.Context())
			paused := make(chan error, 1)
			go func() { paused <- p.Pause(ctx) }()
			waitForLocalPause(t, c)
			cancel()
			if err := <-paused; !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			close(releasePrepare)
			if err := <-done; !errors.Is(err, workercontrol.ErrPaused) {
				t.Fatalf("run=%v", err)
			}
			if len(writes) != 0 || !c.Snapshot().Quiesced {
				t.Fatal("pause lost durable work")
			}
		})
	}
}

func TestWorkerControlPausePreservesAlreadySentResultsAndUnknown(t *testing.T) {
	for _, outcome := range []string{protocol.OutcomeSucceeded, protocol.OutcomeUnknown} {
		t.Run(outcome, func(t *testing.T) {
			writes := make(chan repository.ProvisionUpdate, 10)
			base := provisionTestStore(t, writes)
			sent := make(chan connector.OperationCommand, 1)
			var sends atomic.Int32
			c := controlFor(t, workercontrol.Active)
			p := controlledExecutor(t, "PROVISION", c, base, provisionRouterFake{send: func(_ context.Context, cmd connector.OperationCommand) (connector.SentMessage, error) {
				sends.Add(1)
				sent <- cmd
				return connector.SentMessage{MessageID: "outbound-message"}, nil
			}})
			p.opts.ResultTimeout = 2 * time.Second
			done := make(chan error, 1)
			go func() { _, err := p.RunOne(t.Context()); done <- err }()
			cmd := <-sent
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if err := p.Pause(ctx); err != nil || !c.Snapshot().Quiesced {
				t.Fatalf("pause waited for Provider result: %v", err)
			}
			p.HandleEvent(connector.OperationResultEvent{Routed: routedFor(cmd), Payload: protocol.OperationResultPayload{Outcome: outcome}})
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			u := <-writes
			want := "SUCCEEDED"
			if outcome == protocol.OutcomeUnknown {
				want = "RECONCILING"
			}
			if u.Status != want || sends.Load() != 1 {
				t.Fatalf("result=%+v sends=%d", u, sends.Load())
			}
			if claimed, err := p.RunOne(t.Context()); claimed || !errors.Is(err, workercontrol.ErrPaused) {
				t.Fatal("pause auto-retried")
			}
		})
	}
}

type controlledReconciliationStore struct {
	claim   func(context.Context, uuid.UUID, time.Duration) (repository.OperationItemClaim, error)
	prepare func(context.Context, repository.OperationItemLease) (repository.ProvisionDispatch, error)
	write   func(context.Context, repository.ReconciliationUpdate) error
}

func (f controlledReconciliationStore) ClaimReconciliation(ctx context.Context, o uuid.UUID, d time.Duration) (repository.OperationItemClaim, error) {
	return f.claim(ctx, o, d)
}
func (f controlledReconciliationStore) RenewReconciliation(context.Context, repository.OperationItemLease, time.Duration) (time.Time, error) {
	return time.Now().Add(time.Minute), nil
}
func (f controlledReconciliationStore) PrepareReconciliation(ctx context.Context, l repository.OperationItemLease) (repository.ProvisionDispatch, error) {
	return f.prepare(ctx, l)
}
func (f controlledReconciliationStore) WriteReconciliation(ctx context.Context, u repository.ReconciliationUpdate) error {
	return f.write(ctx, u)
}

type controlledReconciliationRouter struct {
	provisionRouterFake
	query func(context.Context, connector.ReconcileRequest) (connector.SentMessage, error)
}

func (f controlledReconciliationRouter) SendReconcileRequest(ctx context.Context, r connector.ReconcileRequest) (connector.SentMessage, error) {
	return f.query(ctx, r)
}
func (f controlledReconciliationRouter) ForgetReconcile(uuid.UUID, string) bool { return true }

func TestWorkerControlReconcileOnlySkipsPendingMutationQueue(t *testing.T) {
	for _, kind := range []string{"PROVISION", "RESET", "CLEANUP"} {
		t.Run(kind, func(t *testing.T) {
			base := provisionTestStore(t, make(chan repository.ProvisionUpdate, 10))
			base.claim = func(context.Context, uuid.UUID, time.Duration) (repository.OperationItemClaim, error) {
				t.Error("mutation-first claim in reconcile-only")
				return repository.OperationItemClaim{}, nil
			}
			base.renew = func(context.Context, repository.OperationItemLease, time.Duration) (time.Time, error) {
				return time.Time{}, repository.ErrOperationLeaseLost
			}
			base.write = func(context.Context, repository.ProvisionUpdate) error { t.Error("mutation writer called"); return nil }
			var queries, records int
			var p *Executor
			var err error
			reconcile := controlledReconciliationStore{
				claim: func(_ context.Context, owner uuid.UUID, _ time.Duration) (repository.OperationItemClaim, error) {
					return repository.OperationItemClaim{Operation: repository.Operation{Type: kind}, Item: repository.OperationItem{Status: "RECONCILING"}, Lease: repository.OperationItemLease{ItemID: uuid.New(), Owner: owner}}, nil
				},
				prepare: base.prepare,
				write: func(_ context.Context, u repository.ReconciliationUpdate) error {
					records++
					if !u.QueryFailed {
						t.Error("uncertain query lost HOLD")
					}
					return nil
				},
			}
			p, err = NewExecutor(Options{Store: base, Reconciliation: reconcile, WorkerControl: controlFor(t, workercontrol.ReconcileOnly), Router: controlledReconciliationRouter{
				provisionRouterFake: provisionRouterFake{send: func(context.Context, connector.OperationCommand) (connector.SentMessage, error) {
					t.Error("mutation dispatched")
					return connector.SentMessage{}, nil
				}},
				query: func(_ context.Context, r connector.ReconcileRequest) (connector.SentMessage, error) {
					queries++
					p.HandleEvent(connector.ReconcileResultEvent{Routed: connector.Routed{ConnectorID: r.ConnectorID, Correlation: r.Correlation, MessageID: "response", RequestMessageID: "query"}, Payload: protocol.ReconcileResultPayload{Error: &protocol.SafeError{Code: "uncertain", Message: "not verified"}}})
					return connector.SentMessage{MessageID: "query"}, nil
				},
			}})
			if err != nil {
				t.Fatal(err)
			}
			if claimed, err := p.RunOne(t.Context()); !claimed || err != nil {
				t.Fatalf("run=%v/%v", claimed, err)
			}
			if queries != 1 || records != 1 {
				t.Fatalf("queries=%d records=%d", queries, records)
			}
			if err := p.Pause(t.Context()); err != nil {
				t.Fatal(err)
			}
			if claimed, err := p.RunOne(t.Context()); claimed || !errors.Is(err, workercontrol.ErrPaused) {
				t.Fatal("paused reconcile claimed")
			}
		})
	}
}

func TestWorkerControlClaimFailureIsNotReconciliationPermission(t *testing.T) {
	base := provisionTestStore(t, make(chan repository.ProvisionUpdate, 10))
	failure := errors.New("private claim uncertainty")
	base.claim = func(context.Context, uuid.UUID, time.Duration) (repository.OperationItemClaim, error) {
		return repository.OperationItemClaim{}, failure
	}
	claims := reconciliationClaims{OperationWorkerStore: base, control: controlFor(t, workercontrol.Active), store: controlledReconciliationStore{claim: func(context.Context, uuid.UUID, time.Duration) (repository.OperationItemClaim, error) {
		t.Error("failure fell through to reconciliation")
		return repository.OperationItemClaim{}, nil
	}}}
	if _, err := claims.ClaimOperationItem(t.Context(), uuid.New(), time.Minute); !errors.Is(err, failure) {
		t.Fatal(err)
	}
}

func TestWorkerControlWrappedPauseSendFailureKeepsUnknownOrConfirmedResult(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "immediate result"}[confirmed], func(t *testing.T) {
			writes := make(chan repository.ProvisionUpdate, 10)
			base := provisionTestStore(t, writes)
			base.deferUnsent = func(context.Context, repository.ProvisionDispatch, time.Duration) error {
				t.Error("ambiguous send requeued")
				return nil
			}
			var p *Executor
			p = controlledExecutor(t, "PROVISION", controlFor(t, workercontrol.Active), base, provisionRouterFake{send: func(_ context.Context, c connector.OperationCommand) (connector.SentMessage, error) {
				if confirmed {
					p.HandleEvent(connector.OperationResultEvent{Routed: routedFor(c), Payload: protocol.OperationResultPayload{Outcome: protocol.OutcomeSucceeded}})
				}
				return connector.SentMessage{}, errors.Join(connector.ErrSendFailed, workercontrol.ErrPaused)
			}})
			if claimed, err := p.RunOne(t.Context()); !claimed || err != nil {
				t.Fatalf("run=%v/%v", claimed, err)
			}
			u := <-writes
			want := "RECONCILING"
			if confirmed {
				want = "SUCCEEDED"
			}
			if u.Status != want {
				t.Fatalf("result=%s want=%s", u.Status, want)
			}
		})
	}
}

func TestWorkerControlPauseDuringLifecycleClosePreventsDispatch(t *testing.T) {
	for _, kind := range []string{"RESET", "CLEANUP"} {
		t.Run(kind, func(t *testing.T) {
			writes := make(chan repository.ProvisionUpdate, 10)
			base := provisionTestStore(t, writes)
			c := controlFor(t, workercontrol.Active)
			p := controlledExecutor(t, kind, c, base, provisionRouterFake{send: func(context.Context, connector.OperationCommand) (connector.SentMessage, error) {
				t.Error("sent after lifecycle pause")
				return connector.SentMessage{}, nil
			}})
			entered, releaseClose := make(chan struct{}), make(chan struct{})
			p.opts.CloseForMutation = func(context.Context, repository.ProvisionDispatch) error { close(entered); <-releaseClose; return nil }
			done := make(chan error, 1)
			go func() { _, err := p.RunOne(t.Context()); done <- err }()
			<-entered
			paused := make(chan error, 1)
			go func() { paused <- p.Pause(t.Context()) }()
			waitForLocalPause(t, c)
			close(releaseClose)
			if err := <-paused; err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, workercontrol.ErrPaused) {
				t.Fatalf("run=%v", err)
			}
			if len(writes) != 0 {
				t.Fatal("paused lifecycle auto-finalized mutation")
			}
		})
	}
}

func TestWorkerControlReconcileOnlyRequiresSupportedStore(t *testing.T) {
	base := provisionTestStore(t, make(chan repository.ProvisionUpdate, 10))
	if p, err := NewExecutor(Options{Store: base, Router: provisionRouterFake{}, WorkerControl: controlFor(t, workercontrol.ReconcileOnly)}); p != nil || err == nil {
		t.Fatal("unsupported reconciliation silently accepted")
	}
}

type controlledOwnerLookup func(context.Context, uuid.UUID) (*valkeyowner.ConnectorOwnerRecord, error)

func (f controlledOwnerLookup) LookupConnector(ctx context.Context, id uuid.UUID) (*valkeyowner.ConnectorOwnerRecord, error) {
	return f(ctx, id)
}

func TestWorkerControlPauseBlocksOwnershipAndUnsentRequeue(t *testing.T) {
	for _, kind := range []string{"PROVISION", "RESET", "CLEANUP"} {
		for _, boundary := range []string{"ownership", "send-not-connected"} {
			t.Run(kind+"/"+boundary, func(t *testing.T) {
				writes := make(chan repository.ProvisionUpdate, 10)
				base := provisionTestStore(t, writes)
				base.deferUnsent = func(context.Context, repository.ProvisionDispatch, time.Duration) error {
					t.Error("paused work requeued")
					return nil
				}
				c := controlFor(t, workercontrol.Active)
				paused := make(chan error, 1)
				var p *Executor
				latch := func() { go func() { paused <- p.Pause(t.Context()) }(); waitForLocalPause(t, c) }
				p = controlledExecutor(t, kind, c, base, provisionRouterFake{send: func(context.Context, connector.OperationCommand) (connector.SentMessage, error) {
					if boundary != "send-not-connected" {
						t.Error("ownership failure dispatched")
					}
					latch()
					return connector.SentMessage{}, connector.ErrNotConnected
				}})
				if boundary == "ownership" {
					p.opts.PodID = "local"
					p.opts.OwnerLocator = controlledOwnerLookup(func(context.Context, uuid.UUID) (*valkeyowner.ConnectorOwnerRecord, error) {
						latch()
						return &valkeyowner.ConnectorOwnerRecord{PodID: "other"}, nil
					})
				}
				if claimed, err := p.RunOne(t.Context()); !claimed || !errors.Is(err, workercontrol.ErrPaused) {
					t.Fatalf("run=%v/%v", claimed, err)
				}
				if err := <-paused; err != nil {
					t.Fatal(err)
				}
				if len(writes) != 0 || !c.Snapshot().Quiesced {
					t.Fatal("pause lost claim")
				}
			})
		}
	}
}

func TestWorkerControlPauseDuringReconciliationPreparationPreservesClaim(t *testing.T) {
	base := provisionTestStore(t, make(chan repository.ProvisionUpdate, 10))
	base.renew = func(context.Context, repository.OperationItemLease, time.Duration) (time.Time, error) {
		return time.Time{}, repository.ErrOperationLeaseLost
	}
	c := controlFor(t, workercontrol.ReconcileOnly)
	var p *Executor
	paused := make(chan error, 1)
	store := controlledReconciliationStore{
		claim: func(_ context.Context, o uuid.UUID, _ time.Duration) (repository.OperationItemClaim, error) {
			return repository.OperationItemClaim{Item: repository.OperationItem{Status: "RECONCILING"}, Lease: repository.OperationItemLease{ItemID: uuid.New(), Owner: o}}, nil
		},
		prepare: func(ctx context.Context, l repository.OperationItemLease) (repository.ProvisionDispatch, error) {
			go func() { paused <- p.Pause(t.Context()) }()
			waitForLocalPause(t, c)
			return base.prepare(ctx, l)
		},
		write: func(context.Context, repository.ReconciliationUpdate) error {
			t.Error("paused query claimed an outcome")
			return nil
		},
	}
	var err error
	p, err = NewExecutor(Options{Store: base, Reconciliation: store, WorkerControl: c, Router: controlledReconciliationRouter{query: func(context.Context, connector.ReconcileRequest) (connector.SentMessage, error) {
		t.Error("query sent after pause")
		return connector.SentMessage{}, nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := p.RunOne(t.Context()); !claimed || !errors.Is(err, workercontrol.ErrPaused) {
		t.Fatalf("run=%v/%v", claimed, err)
	}
	if err := <-paused; err != nil || !c.Snapshot().Quiesced {
		t.Fatal("reconcile pause did not drain")
	}
}

func TestWorkerControlPauseWaitsForAlreadyEnteredUnsentRequeue(t *testing.T) {
	writes := make(chan repository.ProvisionUpdate, 10)
	base := provisionTestStore(t, writes)
	entered, commit := make(chan struct{}), make(chan struct{})
	var requeues atomic.Int32
	base.deferUnsent = func(context.Context, repository.ProvisionDispatch, time.Duration) error {
		close(entered)
		<-commit
		requeues.Add(1)
		return nil
	}
	c := controlFor(t, workercontrol.Active)
	p := controlledExecutor(t, "PROVISION", c, base, provisionRouterFake{send: func(context.Context, connector.OperationCommand) (connector.SentMessage, error) {
		return connector.SentMessage{}, connector.ErrNotConnected
	}})
	done := make(chan error, 1)
	go func() { _, err := p.RunOne(t.Context()); done <- err }()
	<-entered
	paused := make(chan error, 1)
	go func() { paused <- p.Pause(t.Context()) }()
	waitForLocalPause(t, c)
	if c.Snapshot().Quiesced {
		t.Fatal("pause ignored already entered DB requeue")
	}
	close(commit)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-paused; err != nil {
		t.Fatal(err)
	}
	if requeues.Load() != 1 || !c.Snapshot().Quiesced || len(writes) != 0 {
		t.Fatal("existing unsent requeue lost")
	}
	if claimed, err := p.RunOne(t.Context()); claimed || !errors.Is(err, workercontrol.ErrPaused) {
		t.Fatal("pause reclaimed deferred work")
	}
}

// Two real Executor instances share a fake authoritative journal, not a DB.
func TestDurableWorkerControlPeerPauseBetweenClaimAndDispatch(t *testing.T) {
	for _, kind := range []string{"PROVISION", "RESET", "CLEANUP"} {
		t.Run(kind, func(t *testing.T) {
			b := workercontrol.Binding{Scope: workercontrol.Scope{ID: "trial", Region: "region-fixture", Cluster: "cluster-fixture"}, Revision: 11, Epoch: 4, Mode: workercontrol.Active, PodID: "pod-a", BootID: uuid.New()}
			journal := workercontrolfake.New(workercontrol.State{Scope: b.Scope, Revision: b.Revision, Epoch: b.Epoch, Mode: b.Mode})
			a, err := workercontrol.NewDurable(b, journal)
			if err != nil {
				t.Fatal(err)
			}
			peer := b
			peer.PodID = "pod-b"
			peer.BootID = uuid.New()
			c, err := workercontrol.NewDurable(peer, journal)
			if err != nil {
				t.Fatal(err)
			}
			writes := make(chan repository.ProvisionUpdate, 10)
			base := provisionTestStore(t, writes)
			claim := base.claim
			committed, returnClaim := make(chan struct{}), make(chan struct{})
			base.claim = func(ctx context.Context, o uuid.UUID, d time.Duration) (repository.OperationItemClaim, error) {
				v, e := claim(ctx, o, d)
				close(committed)
				<-returnClaim
				return v, e
			}
			base.prepare = func(context.Context, repository.OperationItemLease) (repository.ProvisionDispatch, error) {
				t.Error("prepare entered after peer pause")
				return repository.ProvisionDispatch{}, nil
			}
			router := provisionRouterFake{send: func(context.Context, connector.OperationCommand) (connector.SentMessage, error) {
				t.Error("sent after global pause")
				return connector.SentMessage{}, nil
			}}
			p := controlledExecutor(t, kind, c, base, router)
			done := make(chan error, 1)
			go func() {
				claimed, e := p.RunOne(t.Context())
				if !claimed {
					t.Error("committed claim lost")
				}
				done <- e
			}()
			<-committed
			if err = a.Pause(t.Context()); !errors.Is(err, workercontrol.ErrHold) {
				t.Fatalf("in-flight peer not held: %v", err)
			}
			close(returnClaim)
			if err = <-done; !errors.Is(err, workercontrol.ErrStale) {
				t.Fatalf("late execute=%v", err)
			}
			if len(writes) != 0 {
				t.Fatal("claim automatically requeued")
			}
			if err = a.Pause(t.Context()); err != nil {
				t.Fatal(err)
			}
			// A different Pod restart cannot silently adopt a new authoritative revision.
			peer.BootID = uuid.New()
			restart, err := workercontrol.NewDurable(peer, journal)
			if err != nil {
				t.Fatal(err)
			}
			neverClaim := base
			neverClaim.claim = func(context.Context, uuid.UUID, time.Duration) (repository.OperationItemClaim, error) {
				t.Error("restart called DB claim")
				return repository.OperationItemClaim{}, repository.ErrNotFound
			}
			next := controlledExecutor(t, kind, restart, neverClaim, router)
			if claimed, e := next.RunOne(t.Context()); claimed || !errors.Is(e, workercontrol.ErrStale) {
				t.Fatalf("restart=%v/%v", claimed, e)
			}
		})
	}
}

func TestDurableWorkerControlPeerPauseDuringPrepareBlocksFinalRouterAndRequeue(t *testing.T) {
	for _, kind := range []string{"PROVISION", "RESET", "CLEANUP"} {
		t.Run(kind, func(t *testing.T) {
			b := workercontrol.Binding{Scope: workercontrol.Scope{ID: "trial", Region: "region-fixture", Cluster: "cluster-fixture"}, Revision: 1, Epoch: 1, Mode: workercontrol.Active, PodID: "worker", BootID: uuid.New()}
			journal := workercontrolfake.New(workercontrol.State{Scope: b.Scope, Revision: 1, Epoch: 1, Mode: b.Mode})
			c, err := workercontrol.NewDurable(b, journal)
			if err != nil {
				t.Fatal(err)
			}
			writes := make(chan repository.ProvisionUpdate, 10)
			base := provisionTestStore(t, writes)
			prepare := base.prepare
			base.prepare = func(ctx context.Context, l repository.OperationItemLease) (repository.ProvisionDispatch, error) {
				if err := journal.Pause(ctx, b, uuid.New()); err != nil {
					t.Fatal(err)
				}
				return prepare(ctx, l)
			}
			p := controlledExecutor(t, kind, c, base, provisionRouterFake{send: func(context.Context, connector.OperationCommand) (connector.SentMessage, error) {
				t.Error("send entered after prepare pause")
				return connector.SentMessage{}, nil
			}})
			if claimed, e := p.RunOne(t.Context()); !claimed || !errors.Is(e, workercontrol.ErrStale) {
				t.Fatalf("run=%v/%v", claimed, e)
			}
			if len(writes) != 0 {
				t.Fatal("paused claim altered")
			}
		})
	}
}
