package connector

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/valkeyowner"
	"github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

func TestReconciliationReadbackDoesNotAcceptReplacementSession(t *testing.T) {
	f := newRouterFixture(t)
	p := principalOf(uuid.New())
	reg := f.registry.Register(p, nil)
	outbound := make(chan []byte, 1)
	if !reg.MarkReady(func(data []byte) error { outbound <- data; return nil }) {
		t.Fatal("session could not become ready")
	}
	c := corr("op-1", "lab-A", 1)
	req := reconcileFor(p.ConnectorID, c)
	req.SessionID = reg.Session().ID
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out := make(chan error, 1)
	go func() {
		_, err := f.router.readReconciliation(ctx, req, 0)
		out <- err
	}()
	var msg protocol.ReconcileRequestMessage
	select {
	case wire := <-outbound:
		if err := json.Unmarshal(wire, &msg); err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("readback did not send")
	}

	replaced, frames := f.connect(p)
	in := reconcileReply(SentMessage{MessageID: msg.MessageID}, c)
	verified, _ := replaced.IfCurrent(func() error {
		if f.router.RouteReconcileResultFromSession(replaced.Session(), in, protocol.ReconcileResultPayload{}) {
			t.Error("replacement session satisfied previous readback")
		}
		return nil
	})
	if !verified {
		t.Fatal("replacement not current")
	}
	if _, pending := f.router.PendingCount(p.ConnectorID); pending != 1 {
		t.Fatalf("nonmatching reply removed the request, pending = %d", pending)
	}
	cancel()
	if err := <-out; !errors.Is(err, context.Canceled) {
		t.Fatalf("unverified observation returned %v", err)
	}
	if _, pending := f.router.PendingCount(p.ConnectorID); pending != 0 || frames.count() != 0 {
		t.Fatalf("canceled request left %d pending or retried on replacement", pending)
	}
}

func TestReconciliationReadbackDiscardsProviderFailure(t *testing.T) {
	f := newRouterFixture(t)
	p := principalOf(uuid.New())
	reg := f.registry.Register(p, nil)
	if !reg.MarkReady(func(data []byte) error {
		var msg protocol.ReconcileRequestMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return err
		}
		in := Inbound{MessageID: uuid.NewString(), ReplyToMessageID: msg.MessageID,
			OperationID: msg.OperationID, LabInstanceID: msg.LabInstanceID, Generation: msg.Generation}
		if !f.router.RouteReconcileResultFromSession(reg.Session(), in,
			protocol.ReconcileResultPayload{Error: &protocol.SafeError{}}) {
			t.Fatal("correlated failure was not delivered")
		}
		return nil
	}) {
		t.Fatal("session could not become ready")
	}
	req := reconcileFor(p.ConnectorID, corr("op-1", "lab-A", 1))
	req.SessionID = reg.Session().ID
	got, err := f.router.readReconciliation(context.Background(), req, 0)
	if !errors.Is(err, ErrReconciliationReadback) || got.RequestMessageID != "" {
		t.Fatalf("failed provider yielded %+v, err = %v", got, err)
	}
}

func TestReconciliationReadbackHonorsWorkerMode(t *testing.T) {
	for _, mode := range []workercontrol.Mode{workercontrol.PauseNew, workercontrol.ReconcileOnly} {
		t.Run(string(mode), func(t *testing.T) {
			f := newRouterFixture(t)
			control, err := workercontrol.New(mode)
			if err != nil {
				t.Fatal(err)
			}
			f.router.workerControl = control
			p := principalOf(uuid.New())
			reg := f.registry.Register(p, nil)
			queries := 0
			if !reg.MarkReady(func(data []byte) error {
				var req protocol.ReconcileRequestMessage
				if err := json.Unmarshal(data, &req); err != nil {
					return err
				}
				if req.Type != protocol.MessageTypeReconcileRequest {
					t.Fatalf("unexpected message type %q", req.Type)
				}
				queries++
				in := Inbound{MessageID: uuid.NewString(), ReplyToMessageID: req.MessageID,
					OperationID: req.OperationID, LabInstanceID: req.LabInstanceID, Generation: req.Generation}
				if !f.router.RouteReconcileResultFromSession(reg.Session(), in, protocol.ReconcileResultPayload{}) {
					t.Fatal("same-session result was not delivered")
				}
				return nil
			}) {
				t.Fatal("session could not become ready")
			}
			req := reconcileFor(p.ConnectorID, corr("op-1", "lab-A", 1))
			req.SessionID = reg.Session().ID
			got, err := f.router.readReconciliation(context.Background(), req, 0)
			if mode == workercontrol.PauseNew {
				if !errors.Is(err, workercontrol.ErrPaused) || queries != 0 {
					t.Fatalf("pause queried provider %d times, err = %v", queries, err)
				}
				return
			}
			if err != nil || queries != 1 || got.SessionID != req.SessionID {
				t.Fatalf("reconcile-only read = %+v, queries = %d, err = %v", got, queries, err)
			}
			if _, err := control.AcquireMutation(context.Background()); !errors.Is(err, workercontrol.ErrPaused) {
				t.Fatalf("reconcile-only mutation gate = %v", err)
			}
		})
	}
}

type testReconciliationOwner struct {
	read func(uuid.UUID) (*valkeyowner.ConnectorOwnerRecord, error)
}

func (testReconciliationOwner) PodID() string { return "osaka-pod" }

func (o testReconciliationOwner) ObserveConnectorOwner(_ context.Context, id uuid.UUID) (*valkeyowner.ConnectorOwnerRecord, error) {
	return o.read(id)
}

func TestOwnedReconciliationRequiresCurrentValkeyLease(t *testing.T) {
	for _, scenario := range []struct {
		name          string
		before, after func(*valkeyowner.ConnectorOwnerRecord) error
		writes, reads int
	}{
		{name: "stable", writes: 1, reads: 2},
		{name: "foreign initial pod", before: func(r *valkeyowner.ConnectorOwnerRecord) error {
			r.PodID = "other-pod"
			return nil
		}, reads: 1},
		{name: "expired", before: func(r *valkeyowner.ConnectorOwnerRecord) error {
			r.ExpiresAt = time.Now().Add(-time.Second)
			return nil
		}, reads: 1},
		{name: "pod takeover", after: func(r *valkeyowner.ConnectorOwnerRecord) error {
			r.PodID = "other-pod"
			r.Epoch++
			return nil
		}, writes: 1, reads: 2},
		{name: "same pod newer epoch", after: func(r *valkeyowner.ConnectorOwnerRecord) error {
			r.Epoch++
			return nil
		}, writes: 1, reads: 2},
		{name: "owner lookup unavailable", after: func(_ *valkeyowner.ConnectorOwnerRecord) error {
			return valkeyowner.ErrLocatorUnavailable
		}, writes: 1, reads: 2},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := newRouterFixture(t)
			p := principalOf(uuid.New())
			reg := f.registry.Register(p, nil)
			const epoch = 17
			writes, reads := 0, 0
			if !reg.MarkReadyWithOwnerEpoch(func(data []byte) error {
				writes++
				var msg protocol.ReconcileRequestMessage
				if err := json.Unmarshal(data, &msg); err != nil {
					return err
				}
				in := Inbound{MessageID: uuid.NewString(), ReplyToMessageID: msg.MessageID,
					OperationID: msg.OperationID, LabInstanceID: msg.LabInstanceID, Generation: msg.Generation}
				if !f.router.RouteReconcileResultFromSession(reg.Session(), in, protocol.ReconcileResultPayload{}) {
					t.Fatal("correlated reply was lost")
				}
				return nil
			}, epoch) {
				t.Fatal("could not bind local session")
			}
			if reg.MarkReadyWithOwnerEpoch(func([]byte) error { return nil }, epoch+1) {
				t.Fatal("already ready session changed epoch")
			}
			req := reconcileFor(p.ConnectorID, corr("op-1", "lab-A", 1))
			req.SessionID = reg.Session().ID
			owner := testReconciliationOwner{read: func(id uuid.UUID) (*valkeyowner.ConnectorOwnerRecord, error) {
				reads++
				record := &valkeyowner.ConnectorOwnerRecord{ConnectorID: id, PodID: "osaka-pod", Epoch: epoch,
					ExpiresAt: time.Now().Add(time.Minute)}
				edit := scenario.before
				if reads == 2 {
					edit = scenario.after
				}
				if edit != nil {
					return record, edit(record)
				}
				return record, nil
			}}
			result, err := f.router.ReadReconciliation(t.Context(), req, owner)
			if writes != scenario.writes || reads != scenario.reads {
				t.Fatalf("observed writes/reads %d/%d, err = %v", writes, reads, err)
			}
			if scenario.before != nil || scenario.after != nil {
				if !errors.Is(err, ErrReconciliationReadback) || result.SessionID != uuid.Nil {
					t.Fatalf("unverified lease returned %+v, %v", result, err)
				}
				return
			}
			if err != nil || result.OwnerBefore.Epoch != epoch || result.OwnerAfter.SessionID != req.SessionID ||
				result.OwnerAfter.ObservedAt.Before(result.ObservedAt) {
				t.Fatalf("expected owned readback, got %+v, %v", result, err)
			}
		})
	}
}
