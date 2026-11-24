package connector

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

func controlledRouterFixture(t *testing.T, mode workercontrol.Mode) (*routerFixture, *workercontrol.Control) {
	t.Helper()
	f := newRouterFixture(t)
	c, err := workercontrol.New(mode)
	if err != nil {
		t.Fatal(err)
	}
	f.router, err = NewRouter(RouterOptions{Registry: f.registry, Sink: f.sink, WorkerControl: c})
	if err != nil {
		t.Fatal(err)
	}
	return f, c
}

func TestWorkerControlDirectRouterCallsCannotBypassPause(t *testing.T) {
	for _, mode := range []workercontrol.Mode{workercontrol.PauseNew, workercontrol.ReconcileOnly} {
		t.Run(map[workercontrol.Mode]string{workercontrol.PauseNew: "pause", workercontrol.ReconcileOnly: "reconcile"}[mode], func(t *testing.T) {
			f, _ := controlledRouterFixture(t, mode)
			p := principalOf(uuid.New())
			_, frames := f.connect(p)
			for _, kind := range []string{"PROVISION", "RESET", "CLEANUP"} {
				cmd := commandFor(p.ConnectorID, corr("op-"+kind, "instance", 1))
				cmd.Payload.MutationType = kind
				if sent, err := f.router.SendOperationCommand(t.Context(), cmd); sent.MessageID != "" || !errors.Is(err, workercontrol.ErrPaused) {
					t.Fatalf("mutation %s=%v", kind, err)
				}
			}
			if frames.count() != 0 {
				t.Fatal("mutation frame sent")
			}
			_, err := f.router.SendReconcileRequest(t.Context(), reconcileFor(p.ConnectorID, corr("op-query", "instance", 1)))
			if mode == workercontrol.PauseNew {
				if !errors.Is(err, workercontrol.ErrPaused) || frames.count() != 0 {
					t.Fatalf("paused query=%v", err)
				}
			} else if err != nil || frames.count() != 1 {
				t.Fatalf("query blocked=%v", err)
			}
			ops, _ := f.router.PendingCount(p.ConnectorID)
			if ops != 0 {
				t.Fatal("blocked operation left pending")
			}
		})
	}
}

func TestWorkerControlRouterPauseDrainsSendAndPreservesLateResult(t *testing.T) {
	f, c := controlledRouterFixture(t, workercontrol.Active)
	p := principalOf(uuid.New())
	reg := f.registry.Register(p, nil)
	entered, releaseSend := make(chan struct{}), make(chan struct{})
	if !reg.MarkReady(func([]byte) error { close(entered); <-releaseSend; return nil }) {
		t.Fatal("not ready")
	}
	defer reg.Release()
	cmd := commandFor(p.ConnectorID, corr("accepted", "instance", 1))
	type reply struct {
		sent SentMessage
		err  error
	}
	done := make(chan reply, 1)
	go func() { sent, err := f.router.SendOperationCommand(t.Context(), cmd); done <- reply{sent, err} }()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	paused := make(chan error, 1)
	go func() { paused <- c.Pause(ctx) }()
	for c.Snapshot().Mode != workercontrol.PauseNew {
		if ctx.Err() != nil {
			t.Fatal("not latched")
		}
		runtime.Gosched()
	}
	if c.Snapshot().Quiesced {
		t.Fatal("route still sending")
	}
	if _, err := f.router.SendOperationCommand(t.Context(), commandFor(p.ConnectorID, corr("new", "instance2", 1))); !errors.Is(err, workercontrol.ErrPaused) {
		t.Fatal(err)
	}
	close(releaseSend)
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	if err := <-paused; err != nil || !c.Snapshot().Quiesced {
		t.Fatalf("pause=%v", err)
	}
	if !f.router.RouteOperationResult(p.ConnectorID, replyFor(r.sent, cmd.Correlation), protocol.OperationResultPayload{Outcome: protocol.OutcomeUnknown}) {
		t.Fatal("pause dropped accepted result")
	}
	if len(f.sink.all()) != 1 {
		t.Fatal("result not delivered")
	}
}
