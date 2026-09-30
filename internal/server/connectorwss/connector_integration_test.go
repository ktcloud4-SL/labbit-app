package connectorwss

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	connectorwss "github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
)

// 이 file은 Backend Router와 Connector 쪽 실제 구현(wss.Supervisor + wss.Handler + provider.MockProvider)을 실제 WebSocket으로 연결한다.
// OpenStack은 쓰지 않는다. Mock 통과는 실제 Provider E2E 성공을 뜻하지 않는다(LBT-16 범위).

// realConnectorHeartbeat는 실제 Connector가 HELLO_ACK의 heartbeat 주기를 초 단위로 따르므로 1초 이상이어야 한다.
// 1초 미만 값은 서버 timer만 짧게 구동하고 Connector에는 더 긴 값을 전달해 OFFLINE 오탐을 만든다.
func realConnectorHeartbeat(o *Options) {
	o.HeartbeatInterval = time.Second
	o.OfflineTimeout = 3 * time.Second
}

type connectorRig struct {
	supervisor *connectorwss.Supervisor
	handler    *connectorwss.Handler
	errs       *handlerErrors
}

type handlerErrors struct {
	mu   sync.Mutex
	errs []error
}

func (e *handlerErrors) add(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.errs = append(e.errs, err)
}

func (e *handlerErrors) all() []error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]error(nil), e.errs...)
}

// startConnector는 credential로 인증하는 실제 Connector를 시작하고 서버 쪽 protocol-ready가 될 때까지 기다린다.
func startConnector(t *testing.T, h *routedHarness, credential string, connectorID uuid.UUID, p *provider.MockProvider) *connectorRig {
	t.Helper()
	handler := connectorwss.NewHandler(p, nil)
	errs := &handlerErrors{}
	handler.SetOnError(errs.add)

	supervisor := connectorwss.NewSupervisor(connectorwss.Config{
		BaseURL:       h.server.URL,
		Credential:    credential,
		AllowInsecure: true,
	}, handler, connectorwss.BackoffPolicy{InitialInterval: 10 * time.Millisecond, MaxInterval: 50 * time.Millisecond, Multiplier: 2, RandomizationFactor: 0.1})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = supervisor.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("Connector Supervisor가 종료되지 않음")
		}
	})
	h.waitReady(connectorID)
	return &connectorRig{supervisor: supervisor, handler: handler, errs: errs}
}

func succeeded(r provider.Correlation, providerID string) provider.OperationResult {
	return provider.OperationResult{
		Outcome: provider.OutcomeSucceeded,
		ProviderResources: []provider.ResourceResult{{
			ResourceRef:   provider.ResourceRef{ResourceType: "SERVER", ProviderID: providerID, Generation: r.Generation, LogicalName: "vm-1"},
			ObservedState: "ACTIVE",
		}},
	}
}

func wantNoHandlerErrors(t *testing.T, rig *connectorRig) {
	t.Helper()
	if errs := rig.errs.all(); len(errs) != 0 {
		t.Fatalf("Connector Handler 오류 = %v", errs)
	}
}

// Backend의 OPERATION_COMMAND → 실제 Connector Handler → OPERATION_ACK → MockProvider → OPERATION_RESULT →
// Backend의 정확한 pending command. Connector, message/reply 관계, correlation, requestId, Trace, terminal 뒤 cleanup을 확인한다.
func TestActualConnectorProvisionRoundTrip(t *testing.T) {
	h := newRoutedHarness(t, realConnectorHeartbeat)
	var calls atomic.Int32
	var got provider.ProvisionRequest
	var mu sync.Mutex
	rig := startConnector(t, h, testCredential, h.principal.ConnectorID, &provider.MockProvider{
		ProvisionFunc: func(_ context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
			calls.Add(1)
			mu.Lock()
			got = r
			mu.Unlock()
			return succeeded(r.Correlation, "srv-1"), nil
		},
	})

	cmd := provisionCommand(h.principal.ConnectorID, "op-1", "lab-A", 7)
	sent := send(t, h, cmd)
	events := h.events.waitCount(t, 2)

	ack, ok1 := events[0].(connector.OperationAckEvent)
	result, ok2 := events[1].(connector.OperationResultEvent)
	if !ok1 || !ok2 {
		t.Fatalf("events = %T, %T, want ACK, RESULT", events[0], events[1])
	}
	wantCorrelation := connector.Correlation{OperationID: "op-1", LabInstanceID: "lab-A", Generation: 7}
	for name, routed := range map[string]connector.Routed{"ack": ack.Routed, "result": result.Routed} {
		if routed.ConnectorID != h.principal.ConnectorID || routed.Correlation != wantCorrelation || routed.RequestMessageID != sent.MessageID {
			t.Fatalf("%s routed = %+v, want connector %s, %+v, request %s", name, routed, h.principal.ConnectorID, wantCorrelation, sent.MessageID)
		}
		if routed.RequestID != "request-lab-A" {
			t.Fatalf("%s requestId = %q", name, routed.RequestID)
		}
		// 실제 Connector가 명령의 유효한 Context를 그대로 돌려준다. propagation-only이므로 새 Span/parent를 만들지 않는다.
		if routed.Trace.Traceparent != testTraceparent || routed.Trace.Tracestate != testTracestate {
			t.Fatalf("%s Trace = %+v, want %q/%q", name, routed.Trace, testTraceparent, testTracestate)
		}
		if routed.MessageID == "" || routed.MessageID == sent.MessageID {
			t.Fatalf("%s messageId = %q", name, routed.MessageID)
		}
	}
	if !ack.Payload.Accepted || ack.Payload.Error != nil {
		t.Fatalf("ACK payload = %+v", ack.Payload)
	}
	if result.Payload.Outcome != protocol.OutcomeSucceeded || len(result.Payload.ProviderResources) != 1 ||
		result.Payload.ProviderResources[0].ProviderID != "srv-1" || result.Payload.ProviderResources[0].Generation != 7 ||
		result.Payload.ProviderResources[0].ObservedState != "ACTIVE" {
		t.Fatalf("RESULT payload = %+v", result.Payload)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls.Load() != 1 || got.Correlation != (provider.Correlation{OperationID: "op-1", LabInstanceID: "lab-A", Generation: 7}) {
		t.Fatalf("Provider 호출 = %d번, %+v", calls.Load(), got.Correlation)
	}
	snap := got.CreationSnapshot
	if snap.ProviderConnectionID != "provider-connection-1" || snap.WorkspaceVMKey != "vm-1" || len(snap.VMs) != 1 ||
		snap.VMs[0].VMKey != "vm-1" || snap.VMs[0].InstanceIndex != 0 || snap.VMs[0].ImageID != "image-1" || snap.VMs[0].FlavorSpec.RAMMiB != 512 {
		t.Fatalf("Provider가 받은 CreationSnapshot = %+v", snap)
	}
	if pending(h, h.principal.ConnectorID) != 0 {
		t.Fatalf("terminal result 뒤 pending = %d, want 0", pending(h, h.principal.ConnectorID))
	}
	wantNoHandlerErrors(t, rig)
}

// FAILED/UNKNOWN과 SafeError, Provider의 미분류 오류가 그대로 결과 event로 전달된다. Provider raw 오류 원문은 event/log에 없고,
// 어떤 결과도 같은 mutation을 자동으로 다시 실행하지 않는다.
func TestActualConnectorOutcomesAreRoutedWithoutRetry(t *testing.T) {
	const rawError = "raw-provider-error-marker-c91a"
	tests := []struct {
		name        string
		result      provider.OperationResult
		err         error
		wantOutcome string
		wantCode    string
	}{
		{"failed with safe error", provider.OperationResult{Outcome: provider.OutcomeFailed, Error: &provider.SafeError{Code: "IMAGE_NOT_FOUND", Message: "image missing"}}, nil, protocol.OutcomeFailed, "IMAGE_NOT_FOUND"},
		{"unknown with safe error", provider.OperationResult{Outcome: provider.OutcomeUnknown, Error: &provider.SafeError{Code: "PROVIDER_TIMEOUT"}}, nil, protocol.OutcomeUnknown, "PROVIDER_TIMEOUT"},
		{"unclassified provider error becomes UNKNOWN without its text", provider.OperationResult{}, errors.New(rawError), protocol.OutcomeUnknown, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newRoutedHarness(t, realConnectorHeartbeat)
			var calls atomic.Int32
			startConnector(t, h, testCredential, h.principal.ConnectorID, &provider.MockProvider{
				ProvisionFunc: func(context.Context, provider.ProvisionRequest) (provider.OperationResult, error) {
					calls.Add(1)
					return tt.result, tt.err
				},
			})
			send(t, h, provisionCommand(h.principal.ConnectorID, "op-1", "lab-A", 1))

			result, ok := h.events.waitCount(t, 2)[1].(connector.OperationResultEvent)
			if !ok || result.Payload.Outcome != tt.wantOutcome {
				t.Fatalf("event = %+v, want outcome %s", h.events.all(), tt.wantOutcome)
			}
			if tt.wantCode == "" {
				if result.Payload.Error != nil {
					t.Fatalf("미분류 오류가 SafeError로 노출됨: %+v", result.Payload.Error)
				}
			} else if result.Payload.Error == nil || result.Payload.Error.Code != tt.wantCode {
				t.Fatalf("SafeError = %+v, want code %s", result.Payload.Error, tt.wantCode)
			}
			if strings.Contains(h.logs.String(), rawError) {
				t.Fatalf("log에 Provider raw 오류가 남음: %s", h.logs.String())
			}
			// UNKNOWN도 FAILED도 자동 재실행하지 않는다. 다른 command를 하나 더 처리한 뒤에도 Provider 호출은 정확히 둘이다.
			send(t, h, provisionCommand(h.principal.ConnectorID, "op-2", "lab-B", 1))
			h.events.waitCount(t, 4)
			if calls.Load() != 2 {
				t.Fatalf("Provider 호출 = %d번, want 2(각 command 한 번씩)", calls.Load())
			}
			if pending(h, h.principal.ConnectorID) != 0 {
				t.Fatalf("pending = %d", pending(h, h.principal.ConnectorID))
			}
		})
	}
}

// RESET과 CLEANUP도 같은 경로로 실제 Connector의 Provider 경계에 도달하고 결과가 연결된다.
func TestActualConnectorResetAndCleanupCommands(t *testing.T) {
	h := newRoutedHarness(t, realConnectorHeartbeat)
	var mu sync.Mutex
	var reset provider.ResetRequest
	var cleanup provider.CleanupRequest
	startConnector(t, h, testCredential, h.principal.ConnectorID, &provider.MockProvider{
		ResetFunc: func(_ context.Context, r provider.ResetRequest) (provider.OperationResult, error) {
			mu.Lock()
			reset = r
			mu.Unlock()
			return succeeded(r.Correlation, "srv-new"), nil
		},
		CleanupFunc: func(_ context.Context, r provider.CleanupRequest) (provider.OperationResult, error) {
			mu.Lock()
			cleanup = r
			mu.Unlock()
			return provider.OperationResult{Outcome: provider.OutcomeSucceeded}, nil
		},
	})
	refs := []protocol.ProviderResourceRef{{ResourceType: "SERVER", ProviderID: "srv-old", Generation: 1, LogicalName: "vm-1"}}

	resetCmd := provisionCommand(h.principal.ConnectorID, "op-1", "lab-A", 2)
	resetCmd.Payload.MutationType = protocol.MutationTypeReset
	resetCmd.Payload.ProviderResources = refs
	send(t, h, resetCmd)

	cleanupCmd := provisionCommand(h.principal.ConnectorID, "op-2", "lab-B", 1)
	cleanupCmd.Payload = protocol.OperationCommandPayload{MutationType: protocol.MutationTypeCleanup, ProviderResources: refs}
	send(t, h, cleanupCmd)

	events := h.events.waitCount(t, 4)
	results := map[string]connector.OperationResultEvent{}
	for _, event := range events {
		if r, ok := event.(connector.OperationResultEvent); ok {
			results[r.Correlation.LabInstanceID] = r
		}
	}
	if len(results) != 2 || results["lab-A"].Payload.Outcome != protocol.OutcomeSucceeded || results["lab-B"].Payload.Outcome != protocol.OutcomeSucceeded {
		t.Fatalf("results = %+v", results)
	}
	mu.Lock()
	defer mu.Unlock()
	if reset.Correlation.Generation != 2 || len(reset.CreationSnapshot.VMs) != 1 || len(reset.ProviderResources) != 1 || reset.ProviderResources[0].ProviderID != "srv-old" {
		t.Fatalf("Reset request = %+v", reset)
	}
	if cleanup.Correlation.LabInstanceID != "lab-B" || len(cleanup.ProviderResources) != 1 || cleanup.ProviderResources[0].Generation != 1 {
		t.Fatalf("Cleanup request = %+v", cleanup)
	}
}

// Backend의 RECONCILE_REQUEST → 실제 Connector Handler + MockProvider → RECONCILE_RESULT → 정확한 pending reconcile request.
// discoverCandidates는 생략하면 true, 명시적 false는 그대로 Provider에 전달된다. Reconciliation은 mutation을 실행하지 않는다.
func TestActualConnectorReconcileRoundTrip(t *testing.T) {
	h := newRoutedHarness(t, realConnectorHeartbeat)
	var mu sync.Mutex
	var requests []provider.ReconcileRequest
	var mutations atomic.Int32
	startConnector(t, h, testCredential, h.principal.ConnectorID, &provider.MockProvider{
		ReconcileFunc: func(_ context.Context, r provider.ReconcileRequest) (provider.ReconcileResult, error) {
			mu.Lock()
			requests = append(requests, r)
			mu.Unlock()
			return provider.ReconcileResult{Observations: []provider.ResourceObservation{
				{ResourceType: "SERVER", ProviderID: "srv-1", Generation: r.Generation, Exists: true, ObservedState: "ACTIVE", Source: provider.SourceKnownResource, LogicalName: "vm-1"},
				{ResourceType: "SERVER", ProviderID: "srv-orphan", Exists: true, Source: provider.SourceDiscoveredCandidate},
			}}, nil
		},
		ProvisionFunc: func(context.Context, provider.ProvisionRequest) (provider.OperationResult, error) {
			mutations.Add(1)
			return provider.OperationResult{}, errors.New("reconcile이 mutation을 실행함")
		},
	})

	c := connector.Correlation{OperationID: "op-1", LabInstanceID: "lab-A", Generation: 2}
	base := connector.ReconcileRequest{
		ConnectorID: h.principal.ConnectorID, RequestID: "request-reconcile", Correlation: c,
		Payload: protocol.ReconcileRequestPayload{KnownResources: []protocol.ProviderResourceRef{{ResourceType: "SERVER", ProviderID: "srv-1", Generation: 2}}},
		Trace:   connector.TraceContext{Traceparent: testTraceparent, Tracestate: testTracestate},
	}
	first, err := h.router.SendReconcileRequest(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	explicitFalse := false
	second := base
	second.Payload.DiscoverCandidates = &explicitFalse
	secondSent, err := h.router.SendReconcileRequest(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}

	events := h.events.waitCount(t, 2)
	byRequest := map[string]connector.ReconcileResultEvent{}
	for _, event := range events {
		e, ok := event.(connector.ReconcileResultEvent)
		if !ok {
			t.Fatalf("event = %+v", event)
		}
		byRequest[e.RequestMessageID] = e
	}
	for _, sent := range []connector.SentMessage{first, secondSent} {
		e, ok := byRequest[sent.MessageID]
		if !ok || e.ConnectorID != h.principal.ConnectorID || e.Correlation != c || e.RequestID != "request-reconcile" ||
			e.Trace.Traceparent != testTraceparent || len(e.Payload.Observations) != 2 ||
			e.Payload.Observations[0].Source != "KNOWN_RESOURCE" || e.Payload.Observations[1].Source != "DISCOVERED_CANDIDATE" {
			t.Fatalf("request %s의 event = %+v", sent.MessageID, e)
		}
	}
	if _, recs := h.router.PendingCount(h.principal.ConnectorID); recs != 0 {
		t.Fatalf("reconcile pending = %d", recs)
	}

	mu.Lock()
	defer mu.Unlock()
	discover := map[bool]int{}
	for _, r := range requests {
		discover[r.DiscoverCandidates]++
		if r.Correlation.OperationID != "op-1" || len(r.KnownResources) != 1 {
			t.Fatalf("Provider가 받은 reconcile = %+v", r)
		}
	}
	if len(requests) != 2 || discover[true] != 1 || discover[false] != 1 {
		t.Fatalf("Provider reconcile 요청 = %+v, want discoverCandidates 생략→true, false→false 각 1번", requests)
	}
	if mutations.Load() != 0 {
		t.Fatal("RECONCILE이 Provider mutation을 실행함")
	}
}

// Connector가 둘 연결돼 있어도 command는 대상 Connector의 Provider에만 도달하고, 결과도 그 Connector의 pending에만 연결된다.
func TestActualConnectorsDoNotCrossRoute(t *testing.T) {
	h := newRoutedHarness(t, realConnectorHeartbeat)
	other := connector.Principal{ConnectorID: uuid.New(), OrganizationID: uuid.New(), CredentialID: uuid.New()}
	const otherCredential = "credential-of-connector-b-unique-77aa"
	h.auth.add(otherCredential, other)

	var callsA, callsB atomic.Int32
	startConnector(t, h, testCredential, h.principal.ConnectorID, &provider.MockProvider{
		ProvisionFunc: func(_ context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
			callsA.Add(1)
			return succeeded(r.Correlation, "srv-of-a"), nil
		},
	})
	startConnector(t, h, otherCredential, other.ConnectorID, &provider.MockProvider{
		ProvisionFunc: func(_ context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
			callsB.Add(1)
			return succeeded(r.Correlation, "srv-of-b"), nil
		},
	})

	// 같은 operationId/labInstanceId/generation이어도 Connector가 다르면 서로 다른 command다.
	send(t, h, provisionCommand(h.principal.ConnectorID, "op-1", "lab-A", 1))
	send(t, h, provisionCommand(other.ConnectorID, "op-1", "lab-A", 1))

	seen := map[uuid.UUID]string{}
	for _, event := range h.events.waitCount(t, 4) {
		if r, ok := event.(connector.OperationResultEvent); ok {
			seen[r.ConnectorID] = r.Payload.ProviderResources[0].ProviderID
		}
	}
	if seen[h.principal.ConnectorID] != "srv-of-a" || seen[other.ConnectorID] != "srv-of-b" {
		t.Fatalf("Connector별 결과 = %+v", seen)
	}
	if callsA.Load() != 1 || callsB.Load() != 1 {
		t.Fatalf("Provider 호출 = A %d, B %d, want 1, 1", callsA.Load(), callsB.Load())
	}
}

// 실제 Supervisor가 command 처리 중 연결을 잃고 재접속해도 Backend는 같은 command를 다시 보내지 않는다.
// Provider가 이미 실행 중이던 command의 결과는 재접속한 current connection으로 도착하고 남아 있던 pending에 연결된다.
// 재접속 뒤 command를 하나 더 처리한 뒤 Provider 호출이 정확히 둘(각 command 한 번씩)임을 확인한다.
func TestActualSupervisorReconnectDoesNotRetryAndLateResultRoutes(t *testing.T) {
	h := newRoutedHarness(t, realConnectorHeartbeat)
	var mu sync.Mutex
	var executed []string
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	rig := startConnector(t, h, testCredential, h.principal.ConnectorID, &provider.MockProvider{
		ProvisionFunc: func(_ context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
			mu.Lock()
			executed = append(executed, r.LabInstanceID)
			mu.Unlock()
			if r.LabInstanceID == "lab-A" {
				once.Do(func() { close(entered) })
				<-release // 첫 command는 연결이 끊긴 동안에도 Provider에서 실행 중이다.
			}
			return succeeded(r.Correlation, "srv-"+r.LabInstanceID), nil
		},
	})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	first := send(t, h, provisionCommand(h.principal.ConnectorID, "op-1", "lab-A", 1))
	<-entered
	if _, ok := h.events.waitCount(t, 1)[0].(connector.OperationAckEvent); !ok {
		t.Fatalf("첫 event = %+v, want ACK", h.events.all())
	}
	oldSession, _ := h.registry.Current(h.principal.ConnectorID)

	// 단절: Connector가 연결을 닫는다. Provider는 계속 실행 중이다. 실제 Supervisor가 heartbeat 실패로 알아채고 재접속한다.
	client := rig.supervisor.CurrentClient()
	if client == nil {
		t.Fatal("Supervisor에 current client가 없음")
	}
	_ = client.Close()
	waitFor(t, "재접속한 새 Session", func() bool {
		s, ok := h.registry.Current(h.principal.ConnectorID)
		return ok && s.ID != oldSession.ID
	})
	h.waitReady(h.principal.ConnectorID)

	if pending(h, h.principal.ConnectorID) != 1 {
		t.Fatalf("재접속 뒤 pending = %d, want 1", pending(h, h.principal.ConnectorID))
	}
	mu.Lock()
	if len(executed) != 1 {
		mu.Unlock()
		t.Fatalf("재접속 뒤 Provider 실행 = %v, want 첫 command 하나뿐(자동 재전송 없음)", executed)
	}
	mu.Unlock()

	close(release) // 이제 Provider가 끝나 결과가 재접속한 connection으로 나간다.
	result, ok := h.events.waitCount(t, 2)[1].(connector.OperationResultEvent)
	if !ok || result.RequestMessageID != first.MessageID || result.Correlation.LabInstanceID != "lab-A" || result.Payload.Outcome != protocol.OutcomeSucceeded {
		t.Fatalf("늦은 결과 event = %+v", h.events.all())
	}

	// 재접속한 연결로 새 command를 보내 처리한다. 이때까지 첫 command가 다시 실행되었다면 Provider 호출이 셋 이상이 된다.
	send(t, h, provisionCommand(h.principal.ConnectorID, "op-2", "lab-B", 1))
	h.events.waitCount(t, 4)
	mu.Lock()
	defer mu.Unlock()
	if len(executed) != 2 || executed[0] != "lab-A" || executed[1] != "lab-B" {
		t.Fatalf("Provider 실행 = %v, want [lab-A lab-B]", executed)
	}
	if pending(h, h.principal.ConnectorID) != 0 {
		t.Fatalf("pending = %d", pending(h, h.principal.ConnectorID))
	}
}

// 빈 providerResources의 CLEANUP은 Schema-valid이다. Backend Router가 `"providerResources": []`로 보내고 실제 Connector Handler가
// ACK를 수락해 Provider.Cleanup을 정확히 한 번, 빈 non-nil 목록으로 호출하며, ACK/RESULT가 기존 pending으로 돌아온다.
func TestActualConnectorEmptyCleanupRoundTrip(t *testing.T) {
	h := newRoutedHarness(t, realConnectorHeartbeat)
	var mu sync.Mutex
	var calls int
	var got provider.CleanupRequest
	rig := startConnector(t, h, testCredential, h.principal.ConnectorID, &provider.MockProvider{
		CleanupFunc: func(_ context.Context, r provider.CleanupRequest) (provider.OperationResult, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			got = r
			return provider.OperationResult{Outcome: provider.OutcomeSucceeded}, nil
		},
	})

	cmd := provisionCommand(h.principal.ConnectorID, "op-1", "lab-A", 4)
	cmd.Payload = protocol.OperationCommandPayload{MutationType: protocol.MutationTypeCleanup, ProviderResources: []protocol.ProviderResourceRef{}}
	sent := send(t, h, cmd)

	events := h.events.waitCount(t, 2)
	ack, ok1 := events[0].(connector.OperationAckEvent)
	result, ok2 := events[1].(connector.OperationResultEvent)
	if !ok1 || !ok2 {
		t.Fatalf("events = %T, %T, want ACK, RESULT", events[0], events[1])
	}
	want := connector.Correlation{OperationID: "op-1", LabInstanceID: "lab-A", Generation: 4}
	if ack.RequestMessageID != sent.MessageID || ack.Correlation != want || !ack.Payload.Accepted || ack.Payload.Error != nil {
		t.Fatalf("ACK event = %+v", ack)
	}
	if result.RequestMessageID != sent.MessageID || result.Correlation != want || result.RequestID != "request-lab-A" ||
		result.Payload.Outcome != protocol.OutcomeSucceeded || result.Payload.Error != nil {
		t.Fatalf("RESULT event = %+v (빈 CLEANUP이 Connector에서 실패로 끝나면 안 됨)", result)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("Provider.Cleanup 호출 = %d번, want 정확히 1", calls)
	}
	if got.ProviderResources == nil || len(got.ProviderResources) != 0 {
		t.Fatalf("Provider가 받은 ProviderResources = %#v, want 빈 non-nil 목록", got.ProviderResources)
	}
	if got.Correlation != (provider.Correlation{OperationID: "op-1", LabInstanceID: "lab-A", Generation: 4}) {
		t.Fatalf("Provider가 받은 correlation = %+v", got.Correlation)
	}
	if pending(h, h.principal.ConnectorID) != 0 {
		t.Fatalf("pending = %d, want 0", pending(h, h.principal.ConnectorID))
	}
	wantNoHandlerErrors(t, rig)
}

// startupScript.content가 빈 문자열인 PROVISION도 Schema-valid이다. 실제 Connector Handler가 수락하고 Provider를 호출한다.
func TestActualConnectorEmptyStartupScriptRoundTrip(t *testing.T) {
	h := newRoutedHarness(t, realConnectorHeartbeat)
	const emptyDigest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	var mu sync.Mutex
	var calls int
	var got provider.ProvisionRequest
	rig := startConnector(t, h, testCredential, h.principal.ConnectorID, &provider.MockProvider{
		ProvisionFunc: func(_ context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			got = r
			return succeeded(r.Correlation, "srv-1"), nil
		},
	})

	cmd := provisionCommand(h.principal.ConnectorID, "op-1", "lab-A", 1)
	cmd.Payload.CreationSnapshot.StartupScript = &protocol.StartupScriptSnapshot{Content: "", SHA256: emptyDigest}
	sent := send(t, h, cmd)

	events := h.events.waitCount(t, 2)
	ack, ok1 := events[0].(connector.OperationAckEvent)
	result, ok2 := events[1].(connector.OperationResultEvent)
	if !ok1 || !ok2 {
		t.Fatalf("events = %T, %T, want ACK, RESULT", events[0], events[1])
	}
	if !ack.Payload.Accepted || ack.Payload.Error != nil || ack.RequestMessageID != sent.MessageID {
		t.Fatalf("빈 content의 startupScript가 거절됨: %+v", ack.Payload)
	}
	if result.Payload.Outcome != protocol.OutcomeSucceeded || len(result.Payload.ProviderResources) != 1 || result.RequestMessageID != sent.MessageID {
		t.Fatalf("RESULT event = %+v", result)
	}

	mu.Lock()
	defer mu.Unlock()
	script := got.CreationSnapshot.StartupScript
	if calls != 1 || script == nil || script.Content != "" || script.SHA256 != emptyDigest {
		t.Fatalf("Provider 호출 %d번, StartupScript = %+v, want 1번, {content:\"\" sha256:%s}", calls, script, emptyDigest)
	}
	if pending(h, h.principal.ConnectorID) != 0 {
		t.Fatalf("pending = %d, want 0", pending(h, h.principal.ConnectorID))
	}
	wantNoHandlerErrors(t, rig)
}
