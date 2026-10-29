package provision

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// Real Connector admission/ACK/execution gate and Backend result timer, with
// Provider and repository boundaries faked. Both roles may enter the gate first.
func TestTwoSequentialItemsShareDispatchResultBudget(t *testing.T) {
	for _, order := range []string{"student first", "instructor first", "exceeds deadline"} {
		t.Run(order, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				writes := make(chan repository.ProvisionUpdate, 20)
				base := provisionTestStore(t, writes)
				dispatches := map[uuid.UUID]repository.ProvisionDispatch{}
				claims := make([]repository.OperationItemClaim, 2)
				for i := range 2 {
					lease := repository.OperationItemLease{ItemID: uuid.New(), Owner: uuid.New()}
					d, _ := base.prepare(context.Background(), lease)
					d.OperationID = uuid.New()
					d.LabInstanceID = uuid.New()
					dispatches[lease.ItemID] = d
					claims[i] = repository.OperationItemClaim{Operation: repository.Operation{ID: d.OperationID, Type: "PROVISION"}, Lease: lease}
				}
				base.prepare = func(_ context.Context, l repository.OperationItemLease) (repository.ProvisionDispatch, error) {
					return dispatches[l.ItemID], nil
				}
				first := dispatches[claims[0].Lease.ItemID].LabInstanceID.String()
				durations := []time.Duration{333 * time.Second, 473 * time.Second}
				if order == "instructor first" {
					durations[0], durations[1] = durations[1], durations[0]
				}
				if order == "exceeds deadline" {
					// Even ACTIVE+SSH+Startup+PTY alone can consume 17 minutes per item.
					// Preflight/network/bootstrap/API delivery add further time, so 15m
					// does not guarantee success at every stage limit or queue size.
					durations = []time.Duration{17 * time.Minute, 17 * time.Minute}
				}
				var executor *Executor
				executions := []string{}
				var executionsMu sync.Mutex
				handler := wss.NewHandler(&coreprovider.MockProvider{ProvisionFunc: func(ctx context.Context, r coreprovider.ProvisionRequest) (coreprovider.OperationResult, error) {
					executionsMu.Lock()
					executions = append(executions, r.LabInstanceID)
					executionsMu.Unlock()
					i := 1
					if r.LabInstanceID == first {
						i = 0
					}
					time.Sleep(durations[i])
					return coreprovider.OperationResult{Outcome: coreprovider.OutcomeSucceeded, Readiness: &coreprovider.Readiness{WorkspaceVMKey: "vm-1", ServerID: "fixture-server", Generation: 1}}, nil
				}}, wss.SendMessageFunc(func(_ context.Context, msg interface{}) error {
					switch m := msg.(type) {
					case protocol.OperationAckMessage:
						c := connector.OperationCommand{ConnectorID: dispatches[claims[0].Lease.ItemID].ConnectorID, Correlation: connector.Correlation{OperationID: m.OperationID, LabInstanceID: m.LabInstanceID, Generation: m.Generation}}
						routed := routedFor(c)
						routed.RequestMessageID = m.ReplyToMessageID
						executor.HandleEvent(connector.OperationAckEvent{Routed: routed, Payload: m.Payload})
					case protocol.OperationResultMessage:
						time.Sleep(2 * time.Second) // controlled delivery delay, not a production guarantee
						c := connector.OperationCommand{ConnectorID: dispatches[claims[0].Lease.ItemID].ConnectorID, Correlation: connector.Correlation{OperationID: m.OperationID, LabInstanceID: m.LabInstanceID, Generation: m.Generation}}
						routed := routedFor(c)
						routed.RequestMessageID = m.ReplyToMessageID
						executor.HandleEvent(connector.OperationResultEvent{Routed: routed, Payload: m.Payload})
					}
					return nil
				}))
				handlerDone := make(chan struct{}, 2)
				router := provisionRouterFake{send: func(ctx context.Context, c connector.OperationCommand) (connector.SentMessage, error) {
					raw, err := json.Marshal(protocol.OperationCommandMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeOperationCommand, MessageID: "outbound-message", SentAt: time.Now(), OperationID: c.Correlation.OperationID, LabInstanceID: c.Correlation.LabInstanceID, Generation: c.Correlation.Generation}, Payload: c.Payload})
					if err != nil {
						return connector.SentMessage{}, err
					}
					go func() {
						defer func() { handlerDone <- struct{}{} }()
						if err := handler.HandleMessage(ctx, raw); err != nil {
							t.Error(err)
						}
					}()
					return connector.SentMessage{MessageID: "outbound-message"}, nil
				}}
				var err error
				executor, err = NewExecutor(Options{Store: base, Router: router})
				if err != nil {
					t.Fatal(err)
				}
				started := time.Now()
				done := make(chan error, 2)
				go func() { done <- executor.execute(context.Background(), claims[0]) }()
				synctest.Wait()
				go func() { done <- executor.execute(context.Background(), claims[1]) }()
				synctest.Wait()
				executionsMu.Lock()
				firstOnly := len(executions) == 1 && executions[0] == first
				executionsMu.Unlock()
				if !firstOnly {
					t.Fatal("second Item bypassed execution gate")
				}
				for range 2 {
					if err := <-done; err != nil {
						t.Fatal(err)
					}
				}
				last := map[uuid.UUID]repository.ProvisionUpdate{}
				for len(writes) > 0 {
					u := <-writes
					last[u.Dispatch.LabInstanceID] = u
				}
				want := "SUCCEEDED"
				elapsed := 810 * time.Second
				if order == "exceeds deadline" {
					want = "RECONCILING"
					elapsed = 15 * time.Minute
				}
				if time.Since(started) != elapsed {
					t.Fatalf("result time=%v want=%v", time.Since(started), elapsed)
				}
				for _, u := range last {
					if u.Status != want {
						t.Fatalf("deadline classification=%s want=%s", u.Status, want)
					}
					if want == "RECONCILING" && u.ErrorCode != "provision_response_unconfirmed" {
						t.Fatal("timeout did not preserve unknown")
					}
				}
				if len(last) != 2 {
					t.Fatalf("results=%d", len(last))
				}
				// Allow late Provider completions to drain. They cannot become READY
				// after the Backend forgot the pending correlation.
				for range 2 {
					<-handlerDone
				}
				if len(writes) != 0 || len(executions) != 2 {
					t.Fatal("late result changed terminal state or execution was repeated")
				}
			})
		})
	}
}
