//go:build integration

package app

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
)

// routedEvents는 Router가 넘긴 event를 순서대로 모은다.
type routedEvents struct {
	mu     sync.Mutex
	events []connector.Event
}

func (r *routedEvents) HandleEvent(e connector.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *routedEvents) all() []connector.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]connector.Event(nil), r.events...)
}

// startRoutingConnector는 MockProvider를 쓰는 실제 Connector Supervisor를 시작하고 Backend가 protocol-ready로 볼 때까지 기다린다.
func startRoutingConnector(t *testing.T, env *lifecycleEnv, mock *provider.MockProvider) {
	t.Helper()
	handler := wss.NewHandler(mock, nil)
	var handlerErrors atomic.Int64
	handler.SetOnError(func(error) { handlerErrors.Add(1) })
	supervisor := wss.NewSupervisor(wss.Config{
		BaseURL: env.url, Credential: e2eConnectorCredential, ConnectorVersion: "test-1.0.0", AllowInsecure: true,
	}, handler, wss.BackoffPolicy{InitialInterval: 50 * time.Millisecond, MaxInterval: 200 * time.Millisecond, Multiplier: 2, RandomizationFactor: 0.1})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("Supervisor가 종료되지 않음")
		}
		if n := handlerErrors.Load(); n != 0 {
			t.Errorf("Connector Handler 오류 %d건", n)
		}
	})
	eventually(t, "protocol-ready route", 15*time.Second, func() bool {
		return env.registry.WithReadyRoute(env.connectorID, func(connector.Session, connector.Route) error { return nil }) == nil
	})
}

// PostgreSQL로 인증한 실제 Connector가 Backend Router의 command/reconcile을 처리한다. 결과 event는 정확한 pending에 연결되고,
// 이 경계는 durable Operation 상태(operations/operation_items/provider_resources)를 만들거나 바꾸지 않는다(LBT-18 범위).
func TestRouterRoundTripWithActualConnectorAgainstPostgreSQL(t *testing.T) {
	env := startLifecycleEnv(t)
	var provisions, reconciles atomic.Int32
	startRoutingConnector(t, env, &provider.MockProvider{
		ProvisionFunc: func(_ context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
			provisions.Add(1)
			return provider.OperationResult{Outcome: provider.OutcomeSucceeded, ProviderResources: []provider.ResourceResult{{
				ResourceRef:   provider.ResourceRef{ResourceType: "SERVER", ProviderID: "srv-1", Generation: r.Generation},
				ObservedState: "ACTIVE",
			}}}, nil
		},
		ReconcileFunc: func(_ context.Context, r provider.ReconcileRequest) (provider.ReconcileResult, error) {
			reconciles.Add(1)
			return provider.ReconcileResult{Observations: []provider.ResourceObservation{
				{ResourceType: "SERVER", ProviderID: "srv-1", Generation: r.Generation, Exists: true, Source: provider.SourceKnownResource},
			}}, nil
		},
	})

	c := connector.Correlation{OperationID: "op-1", LabInstanceID: "lab-A", Generation: 1}
	sent, err := env.router.SendOperationCommand(t.Context(), connector.OperationCommand{
		ConnectorID: env.connectorID,
		RequestID:   "request-1",
		Correlation: c,
		Payload: protocol.OperationCommandPayload{
			MutationType: protocol.MutationTypeProvision,
			CreationSnapshot: &protocol.CreationSnapshot{
				ProviderConnectionID: "provider-connection-1",
				VMs: []protocol.ResolvedVmSpec{{
					VMKey: "vm-1", Role: "workspace", InstanceIndex: 0, ImageID: "image-1", FlavorID: "flavor-1",
					FlavorSpec: &protocol.ResolvedFlavorSpec{VCPUs: 1, RAMMiB: 512, DiskGiB: 0},
				}},
				WorkspaceVMKey: "vm-1",
			},
		},
	})
	if err != nil {
		t.Fatalf("SendOperationCommand() error = %v", err)
	}
	reconcile, err := env.router.SendReconcileRequest(t.Context(), connector.ReconcileRequest{
		ConnectorID: env.connectorID,
		Correlation: c,
		Payload:     protocol.ReconcileRequestPayload{KnownResources: []protocol.ProviderResourceRef{{ResourceType: "SERVER", ProviderID: "srv-1", Generation: 1}}},
	})
	if err != nil {
		t.Fatalf("SendReconcileRequest() error = %v", err)
	}

	// ACK, RESULT(command)와 RECONCILE_RESULT가 모두 도착할 때까지 기다린다.
	eventually(t, "ACK, RESULT, RECONCILE_RESULT event", 15*time.Second, func() bool { return len(env.events.all()) >= 3 })
	var ack, result, reconciled bool
	for _, event := range env.events.all() {
		switch e := event.(type) {
		case connector.OperationAckEvent:
			ack = e.RequestMessageID == sent.MessageID && e.Correlation == c && e.Payload.Accepted
		case connector.OperationResultEvent:
			result = e.RequestMessageID == sent.MessageID && e.Correlation == c && e.RequestID == "request-1" &&
				e.Payload.Outcome == protocol.OutcomeSucceeded && len(e.Payload.ProviderResources) == 1
		case connector.ReconcileResultEvent:
			reconciled = e.RequestMessageID == reconcile.MessageID && e.Correlation == c && len(e.Payload.Observations) == 1
		default:
			t.Fatalf("예상하지 못한 event: %+v", event)
		}
	}
	if !ack || !result || !reconciled {
		t.Fatalf("event 연결 = ack %v, result %v, reconcile %v: %+v", ack, result, reconciled, env.events.all())
	}
	if provisions.Load() != 1 || reconciles.Load() != 1 {
		t.Fatalf("Provider 호출 = provision %d, reconcile %d, want 1, 1", provisions.Load(), reconciles.Load())
	}
	if ops, recs := env.router.PendingCount(env.connectorID); ops != 0 || recs != 0 {
		t.Fatalf("pending = %d, %d, want 0, 0", ops, recs)
	}
	env.requireNoOperationActivity() // command/result routing은 durable Operation row를 만들지 않는다.
}
