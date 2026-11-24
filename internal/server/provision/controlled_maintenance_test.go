package provision

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

func TestApprovedControlProcessesExistingWorkWhileNewHTTPStaysClosed(t *testing.T) {
	for _, kind := range []string{"PROVISION", "RESET", "CLEANUP"} {
		t.Run(kind, func(t *testing.T) {
			_, g := trialMaintenance(t)
			b, s := approvedTrialResume(t, wc.Active, g)
			base := provisionTestStore(t, make(chan repository.ProvisionUpdate, 20))
			var sends atomic.Int32
			var wg sync.WaitGroup
			for index := range 2 {
				next := b
				next.BootID = uuid.New()
				next.PodID = []string{"pod-a", "pod-b"}[index]
				c, e := wc.NewDurable(next, s)
				if e != nil {
					t.Fatal(e)
				}
				var executor *Executor
				executor = controlledExecutor(t, kind, c, base, provisionRouterFake{send: func(_ context.Context, cmd connector.OperationCommand) (connector.SentMessage, error) {
					sends.Add(1)
					executor.HandleEvent(connector.OperationResultEvent{Routed: routedFor(cmd), Payload: protocol.OperationResultPayload{Outcome: "SUCCEEDED"}})
					return connector.SentMessage{MessageID: "fixture"}, nil
				}}, func(o *Options) {
					o.Admission = g.EnterControlledWorker
					o.ObserveReconciliation = g.ObserveReconciliation
				})
				wg.Go(func() {
					if _, err := executor.RunOne(t.Context()); err != nil {
						t.Error(err)
					}
				})
			}
			wg.Wait()
			if sends.Load() != 1 {
				t.Fatal("existing item duplicated or blocked", sends.Load())
			}
			if _, ok := g.Enter("http"); ok {
				t.Fatal("HTTP admission opened")
			}
			closed, counts := g.Snapshot()
			if !closed || counts["worker"] != 0 {
				t.Fatal("closed/drain lost")
			}
			old := b
			old.Revision--
			old.Epoch--
			old.Mode = wc.PauseNew
			c, _ := wc.NewDurable(old, s)
			stale := controlledExecutor(t, kind, c, base, provisionRouterFake{}, func(o *Options) { o.Admission = g.EnterControlledWorker })
			if claimed, e := stale.RunOne(t.Context()); claimed || !errors.Is(e, wc.ErrPaused) {
				t.Fatal("old binding resumed", e)
			}
		})
	}
}
