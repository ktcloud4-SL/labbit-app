package workercontrol_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
	"github.com/ktcloud4-SL/labbit-app/internal/testsupport/workercontrolfake"
)

func fixture(mode wc.Mode) (wc.Binding, *workercontrolfake.Store) {
	b := wc.Binding{Scope: wc.Scope{ID: "dr-fixture", Region: "region-fixture", Cluster: "cluster-fixture"}, Revision: 7, Epoch: 3, Mode: mode, PodID: "pod-a", BootID: uuid.New()}
	return b, workercontrolfake.New(wc.State{Scope: b.Scope, Revision: b.Revision, Epoch: b.Epoch, Mode: mode})
}
func control(t *testing.T, b wc.Binding, s wc.DurableStore) *wc.Control {
	t.Helper()
	c, e := wc.NewDurable(b, s)
	if e != nil {
		t.Fatal(e)
	}
	return c
}

func TestDurableControlPeerPauseBlocksClaimsAcrossInstancesAndRestart(t *testing.T) {
	b, s := fixture(wc.Active)
	a := control(t, b, s)
	peer := b
	peer.PodID = "pod-b"
	peer.BootID = uuid.New()
	c := control(t, peer, s)
	release, err := c.AcquireMutationFor(t.Context(), wc.MutationClaim)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Pause(t.Context()); !errors.Is(err, wc.ErrHold) {
		t.Fatalf("peer boundary ignored: %v", err)
	}
	if _, err = c.AcquireMutation(t.Context()); !errors.Is(err, wc.ErrStale) {
		t.Fatalf("stale peer allowed: %v", err)
	}
	r, err := a.DurableReport(t.Context())
	if err != nil || r.Drained() || r.OpenBoundaries != 1 {
		t.Fatalf("report=%+v/%v", r, err)
	}
	release() // Exact old entry revision can close after a global Pause revision bump.
	if err = a.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, _ = a.DurableReport(t.Context())
	if !r.Drained() {
		t.Fatal("closed peer boundary not observed")
	}
	peer.BootID = uuid.New()
	restart := control(t, peer, s)
	if _, err = restart.AcquireMutation(t.Context()); !errors.Is(err, wc.ErrStale) {
		t.Fatal("restart reopened stale epoch/revision")
	}
	peer.Revision++
	restart = control(t, peer, s) // Merely knowing new revision never grants ACTIVE.
	if _, err = restart.AcquireMutation(t.Context()); !errors.Is(err, wc.ErrStale) {
		t.Fatal("mode mismatch reopened")
	}
}

func TestDurableControlAmbiguousEntryAndCloseNeverAutoResolve(t *testing.T) {
	for _, kind := range []string{"enter-before", "enter-after", "leave-before", "leave-after"} {
		t.Run(kind, func(t *testing.T) {
			b, s := fixture(wc.Active)
			c := control(t, b, s)
			faults := workercontrolfake.Faults{}
			switch kind {
			case "enter-before":
				faults.Enter = true
			case "enter-after":
				faults.EnterCommitted = true
			case "leave-before":
				faults.Leave = true
			case "leave-after":
				faults.LeaveCommitted = true
			}
			s.SetFaults(faults)
			release, err := c.AcquireMutation(t.Context())
			if kind == "enter-before" || kind == "enter-after" {
				if !errors.Is(err, wc.ErrUnavailable) || release != nil {
					t.Fatal("ambiguous entry ran work")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				release()
				release()
			}
			if !c.Snapshot().Unverified {
				t.Fatal("uncertainty not latched")
			}
			s.SetFaults(workercontrolfake.Faults{})
			if _, err = c.AcquireMutation(t.Context()); !errors.Is(err, wc.ErrUnavailable) {
				t.Fatal("transient recovery implicitly reopened")
			}
			r, err := s.Report(t.Context(), b.Scope)
			want := int64(0)
			if kind == "enter-after" || kind == "leave-before" {
				want = 1
			}
			if err != nil || r.OpenBoundaries != want {
				t.Fatalf("intent lost=%+v/%v", r, err)
			}
			// A vanished boot does not expire an unresolved intent. Other Pods still
			// see HOLD; only exact-ticket investigation can resolve it later.
			peer := b
			peer.PodID = "new-pod"
			peer.BootID = uuid.New()
			p := control(t, peer, s)
			err = p.Pause(t.Context())
			if want == 1 && !errors.Is(err, wc.ErrHold) {
				t.Fatalf("uncertainty erased: %v", err)
			}
		})
	}
}

func TestDurableControlTicketOwnershipAndPauseRequestBinding(t *testing.T) {
	b, s := fixture(wc.Active)
	ticket := wc.Ticket{Binding: b, ID: uuid.New(), Kind: wc.MutationBoundary, Phase: wc.MutationDispatch}
	if err := s.Enter(t.Context(), ticket); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*wc.Ticket){func(v *wc.Ticket) { v.Binding.BootID = uuid.New() }, func(v *wc.Ticket) { v.Binding.PodID = "other" }, func(v *wc.Ticket) { v.Binding.Epoch++ }, func(v *wc.Ticket) { v.Binding.Scope.Cluster = "other" }, func(v *wc.Ticket) { v.Phase = wc.MutationClaim }} {
		wrong := ticket
		change(&wrong)
		if err := s.Leave(t.Context(), wrong); !errors.Is(err, wc.ErrStale) {
			t.Fatal("foreign close")
		}
	}
	id := uuid.New()
	if err := s.Pause(t.Context(), b, id); err != nil {
		t.Fatal(err)
	}
	if err := s.Pause(t.Context(), b, id); err != nil {
		t.Fatal("same pause not idempotent")
	}
	wrong := b
	wrong.Epoch++
	if err := s.Pause(t.Context(), wrong, id); !errors.Is(err, wc.ErrStale) {
		t.Fatal("request ID reused with different epoch")
	}
	if err := s.Leave(t.Context(), ticket); err != nil {
		t.Fatal(err)
	}
	if err := s.Leave(t.Context(), ticket); err != nil {
		t.Fatal("exact duplicate close failed")
	}
}

func TestDurableControlPauseFailureAndReportFailureNeverGlobalPass(t *testing.T) {
	b, s := fixture(wc.Active)
	c := control(t, b, s)
	s.SetFaults(workercontrolfake.Faults{Pause: true})
	if err := c.Pause(t.Context()); !errors.Is(err, wc.ErrUnavailable) {
		t.Fatal(err)
	}
	if c.Snapshot().Mode != wc.PauseNew {
		t.Fatal("failed durable pause reopened local control")
	}
	r, _ := s.Report(t.Context(), b.Scope)
	if r.Drained() {
		t.Fatal("CAS failure claimed global stop")
	}
	s.SetFaults(workercontrolfake.Faults{Report: true})
	if _, err := c.DurableReport(t.Context()); !errors.Is(err, wc.ErrUnavailable) {
		t.Fatal("cached report used")
	}
	s.SetFaults(workercontrolfake.Faults{})
	if err := c.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestDurableControlReconcileOnlyAndMissingStateFailClosed(t *testing.T) {
	b, s := fixture(wc.ReconcileOnly)
	c := control(t, b, s)
	if _, err := c.AcquireMutation(t.Context()); !errors.Is(err, wc.ErrPaused) {
		t.Fatal("mutation allowed")
	}
	release, err := c.AcquireReconciliation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	release()
	b.Scope.ID = "missing"
	c = control(t, b, s)
	if _, err = c.AcquireReconciliation(t.Context()); !errors.Is(err, wc.ErrStale) {
		t.Fatal("missing/other target allowed")
	}
}

func TestDurableControlConcurrentInstancesPauseAndLateRelease(t *testing.T) {
	b, s := fixture(wc.Active)
	a := control(t, b, s)
	var wg sync.WaitGroup
	var held atomic.Int64
	start := make(chan struct{})
	entered := make(chan struct{}, 48)
	releaseAll := make(chan struct{})
	for range 48 {
		wg.Go(func() {
			peer := b
			peer.PodID = "peer"
			peer.BootID = uuid.New()
			c := control(t, peer, s)
			<-start
			release, err := c.AcquireMutation(t.Context())
			if err == nil {
				held.Add(1)
				entered <- struct{}{}
				<-releaseAll
				release()
			} else {
				entered <- struct{}{}
			}
		})
	}
	close(start)
	for range 48 {
		<-entered
	}
	if held.Load() != 48 {
		t.Fatal("fixture did not enter all boundaries")
	}
	if err := a.Pause(t.Context()); !errors.Is(err, wc.ErrHold) {
		t.Fatal("lost concurrent boundaries")
	}
	close(releaseAll)
	wg.Wait()
	if err := a.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestDurableSettingsValidationAndCanceledEntry(t *testing.T) {
	b, s := fixture(wc.Active)
	for _, change := range []func(*wc.Binding){func(v *wc.Binding) { v.Revision = 0 }, func(v *wc.Binding) { v.Epoch = -1 }, func(v *wc.Binding) { v.BootID = uuid.Nil }, func(v *wc.Binding) { v.Scope.Region = "" }, func(v *wc.Binding) { v.Mode = 255 }} {
		wrong := b
		change(&wrong)
		if _, err := wc.NewDurable(wrong, s); !errors.Is(err, wc.ErrInvalidBinding) {
			t.Fatal("invalid binding accepted")
		}
	}
	if _, err := wc.NewDurable(b, nil); err == nil {
		t.Fatal("nil fallback")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	c := control(t, b, s)
	if _, err := c.AcquireMutation(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(s.OpenTickets()) != 0 {
		t.Fatal("canceled entry persisted")
	}
	if !wc.GuaranteedNoDispatch(wc.ErrUnavailable) || wc.GuaranteedNoDispatch(errors.Join(wc.ErrUnavailable, errors.New("send failed"))) {
		t.Fatal("wrapped send misclassified")
	}
	for _, settings := range []wc.Settings{{Backend: "unknown"}, {Backend: "local", Revision: 1}, {Backend: "postgres"}} {
		if settings.Validate("pod") == nil {
			t.Fatal("partial config accepted")
		}
	}
	if _, err := wc.LoadSettings(func(k string) string {
		if k == "LABBIT_WORKER_CONTROL_EPOCH" {
			return "private-invalid"
		}
		return ""
	}); !errors.Is(err, wc.ErrInvalidBinding) {
		t.Fatal(err)
	}
}

type observationStore struct {
	wc.DurableStore
	observe func(context.Context, wc.Scope) (wc.Report, error)
	enter   func(context.Context, wc.Ticket) error
}

func (s observationStore) Report(ctx context.Context, scope wc.Scope) (wc.Report, error) {
	return s.observe(ctx, scope)
}
func (s observationStore) Enter(ctx context.Context, ticket wc.Ticket) error {
	if s.enter != nil {
		return s.enter(ctx, ticket)
	}
	return s.DurableStore.Enter(ctx, ticket)
}

func TestDurableControlRejectsUntrustedReportAndBoundsQueries(t *testing.T) {
	b, s := fixture(wc.Active)
	for _, kind := range []string{"target", "epoch", "rollback", "negative-count", "negative-resumes", "missing-observation", "invalid-mode"} {
		t.Run(kind, func(t *testing.T) {
			store := observationStore{DurableStore: s, observe: func(ctx context.Context, scope wc.Scope) (wc.Report, error) {
				if until, ok := ctx.Deadline(); !ok || time.Until(until) > 2*time.Second {
					t.Error("unbounded durable report")
				}
				r, _ := s.Report(ctx, scope)
				switch kind {
				case "target":
					r.State.Scope.Cluster = "other"
				case "epoch":
					r.State.Epoch++
				case "rollback":
					r.State.Revision--
				case "negative-count":
					r.OpenBoundaries = -1
				case "negative-resumes":
					r.PendingResumes = -1
				case "missing-observation":
					r.ObservedAt = time.Time{}
				case "invalid-mode":
					r.State.Mode = 255
				}
				return r, nil
			}}
			c := control(t, b, store)
			if _, err := c.DurableReport(t.Context()); err == nil {
				t.Fatal("invalid report accepted")
			}
		})
	}
	store := observationStore{DurableStore: s, enter: func(ctx context.Context, _ wc.Ticket) error {
		if until, ok := ctx.Deadline(); !ok || time.Until(until) > 2*time.Second {
			t.Error("unbounded durable entry")
		}
		return context.DeadlineExceeded
	}}
	c := control(t, b, store)
	if release, err := c.AcquireMutation(t.Context()); release != nil || !errors.Is(err, wc.ErrUnavailable) {
		t.Fatal("timeout permitted work")
	}
	if !c.Snapshot().Unverified || len(s.OpenTickets()) != 0 {
		t.Fatal("timeout not held")
	}
}
