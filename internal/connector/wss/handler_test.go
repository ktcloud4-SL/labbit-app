package wss_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
)

func TestHandler_OperationCommand_Provision_Success(t *testing.T) {
	testToken := "test-secret-token"
	mockSaaS := mock.NewMockSaaS(testToken)
	defer mockSaaS.Close()

	// 1. Client 연결 및 HELLO 핸드셰이크
	cfg := wss.Config{
		BaseURL:       mockSaaS.URL(),
		Credential:    testToken,
		AllowInsecure: true,
	}
	client := wss.NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Dial(ctx); err != nil {
		t.Fatalf("client.Dial failed: %v", err)
	}
	defer client.Close()

	if _, err := client.SendHello(ctx); err != nil {
		t.Fatalf("client.SendHello failed: %v", err)
	}

	// 2. MockProvider 구성 (서빈 님 PR #24 인터페이스)
	provisionCalled := false
	mockProv := &provider.MockProvider{
		ProvisionFunc: func(ctx context.Context, req provider.ProvisionRequest) (provider.OperationResult, error) {
			provisionCalled = true
			if req.OperationID != "op-test-123" {
				return provider.OperationResult{}, fmt.Errorf("unexpected opID: %s", req.OperationID)
			}
			if req.LabInstanceID != "inst-abc-456" {
				return provider.OperationResult{}, fmt.Errorf("unexpected instID: %s", req.LabInstanceID)
			}
			if req.Generation != 1 {
				return provider.OperationResult{}, fmt.Errorf("unexpected generation: %d", req.Generation)
			}
			if req.CreationSnapshot.WorkspaceVMKey != "workspace-vm" {
				return provider.OperationResult{}, fmt.Errorf("unexpected workspaceVMKey: %s", req.CreationSnapshot.WorkspaceVMKey)
			}

			return provider.OperationResult{
				Outcome: provider.OutcomeSucceeded,
				ProviderResources: []provider.ResourceResult{
					{
						ResourceRef: provider.ResourceRef{
							ResourceType: "SERVER",
							ProviderID:   "server-uuid-1",
							Generation:   1,
							LogicalName:  "workspace-vm",
						},
						ObservedState: "ACTIVE",
					},
				},
			}, nil
		},
	}

	// 3. Handler 연결 및 리스너 가동
	handler := wss.NewHandler(mockProv, client)
	startTestListener(t, ctx, handler, client)

	// 4. Mock SaaS에서 OPERATION_COMMAND (PROVISION) 전송
	cmdMsg := protocol.OperationCommandMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:          protocol.MessageTypeOperationCommand,
			MessageID:     "msg-cmd-1",
			SentAt:        time.Now().UTC(),
			OperationID:   "op-test-123",
			LabInstanceID: "inst-abc-456",
			Generation:    1,
			RequestID:     "req-http-789",
			TraceParent:   "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		},
		Payload: protocol.OperationCommandPayload{
			MutationType: "PROVISION",
			CreationSnapshot: &protocol.CreationSnapshot{
				ProviderConnectionID: "conn-os-1",
				WorkspaceVMKey:       "workspace-vm",
				InternetOutbound:     true,
				VMs: []protocol.ResolvedVmSpec{
					{
						VMKey:         "workspace-vm",
						Role:          "default",
						InstanceIndex: 0,
						ImageID:       "ubuntu-img-id",
						FlavorID:      "flavor-small-id",
						FlavorSpec: &protocol.ResolvedFlavorSpec{
							VCPUs:   2,
							RAMMiB:  2048,
							DiskGiB: 20,
						},
					},
				},
			},
		},
	}

	if err := mockSaaS.SendRaw(cmdMsg); err != nil {
		t.Fatalf("failed to send OPERATION_COMMAND from SaaS: %v", err)
	}

	// 5. SaaS 측에서 OPERATION_ACK 및 OPERATION_RESULT 수신 대기 및 검증
	var ackMsg *protocol.OperationAckMessage
	var resMsg *protocol.OperationResultMessage

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		msgs := mockSaaS.ReceivedMessages()
		for _, m := range msgs {
			var env protocol.BaseEnvelope
			if err := json.Unmarshal(m, &env); err == nil {
				if env.Type == protocol.MessageTypeOperationAck && env.ReplyToMessageID == "msg-cmd-1" {
					var ack protocol.OperationAckMessage
					_ = json.Unmarshal(m, &ack)
					ackMsg = &ack
				}
				if env.Type == protocol.MessageTypeOperationResult && env.ReplyToMessageID == "msg-cmd-1" {
					var res protocol.OperationResultMessage
					_ = json.Unmarshal(m, &res)
					resMsg = &res
				}
			}
		}
		if ackMsg != nil && resMsg != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !provisionCalled {
		t.Fatalf("expected MockProvider.Provision to be called, but was not")
	}

	if ackMsg == nil {
		t.Fatalf("expected OPERATION_ACK to be received by SaaS")
	}
	if !ackMsg.Payload.Accepted {
		t.Fatalf("expected OPERATION_ACK accepted to be true")
	}

	if resMsg == nil {
		t.Fatalf("expected OPERATION_RESULT to be received by SaaS")
	}
	if resMsg.Payload.Outcome != "SUCCEEDED" {
		t.Fatalf("expected outcome SUCCEEDED, got %s", resMsg.Payload.Outcome)
	}
	if resMsg.OperationID != "op-test-123" || resMsg.LabInstanceID != "inst-abc-456" || resMsg.Generation != 1 {
		t.Fatalf("envelope correlation mismatch in result: %+v", resMsg.BaseEnvelope)
	}
	if resMsg.RequestID != "req-http-789" {
		t.Fatalf("expected requestId to be preserved, got %s", resMsg.RequestID)
	}
	if resMsg.TraceParent != cmdMsg.TraceParent {
		t.Fatalf("expected traceparent to be preserved, got %s", resMsg.TraceParent)
	}
	if len(resMsg.Payload.ProviderResources) != 1 {
		t.Fatalf("expected 1 resource in result, got %d", len(resMsg.Payload.ProviderResources))
	}
	if resMsg.Payload.ProviderResources[0].ProviderID != "server-uuid-1" {
		t.Fatalf("unexpected provider ID: %s", resMsg.Payload.ProviderResources[0].ProviderID)
	}
	if resMsg.Payload.ProviderResources[0].ObservedState != "ACTIVE" {
		t.Fatalf("unexpected observed state: %s", resMsg.Payload.ProviderResources[0].ObservedState)
	}
}

func TestHandler_OperationCommand_Unknown_OnUnclassifiedError(t *testing.T) {
	testToken := "test-secret-token"
	mockSaaS := mock.NewMockSaaS(testToken)
	defer mockSaaS.Close()

	cfg := wss.Config{
		BaseURL:       mockSaaS.URL(),
		Credential:    testToken,
		AllowInsecure: true,
	}
	client := wss.NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Dial(ctx); err != nil {
		t.Fatalf("client.Dial failed: %v", err)
	}
	defer client.Close()

	if _, err := client.SendHello(ctx); err != nil {
		t.Fatalf("client.SendHello failed: %v", err)
	}

	// 미분류 Provider 오류 발생 시뮬레이션
	mockProv := &provider.MockProvider{
		CleanupFunc: func(ctx context.Context, req provider.CleanupRequest) (provider.OperationResult, error) {
			return provider.OperationResult{
				ProviderResources: []provider.ResourceResult{
					{
						ResourceRef: provider.ResourceRef{
							ResourceType: "SERVER",
							ProviderID:   "server-uuid-dirty",
							Generation:   1,
						},
					},
				},
			}, errors.New("underlying network socket connection reset by peer")
		},
	}

	handler := wss.NewHandler(mockProv, client)
	startTestListener(t, ctx, handler, client)

	cmdMsg := protocol.OperationCommandMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:          protocol.MessageTypeOperationCommand,
			MessageID:     "msg-clean-1",
			SentAt:        time.Now().UTC(),
			OperationID:   "op-clean-999",
			LabInstanceID: "inst-clean-999",
			Generation:    1,
		},
		Payload: protocol.OperationCommandPayload{
			MutationType: "CLEANUP",
			ProviderResources: []protocol.ProviderResourceRef{
				{
					ResourceType: "SERVER",
					ProviderID:   "server-uuid-dirty",
					Generation:   1,
				},
			},
		},
	}

	if err := mockSaaS.SendRaw(cmdMsg); err != nil {
		t.Fatalf("failed to send CLEANUP command: %v", err)
	}

	var resMsg *protocol.OperationResultMessage
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		msgs := mockSaaS.ReceivedMessages()
		for _, m := range msgs {
			var env protocol.BaseEnvelope
			if err := json.Unmarshal(m, &env); err == nil {
				if env.Type == protocol.MessageTypeOperationResult && env.ReplyToMessageID == "msg-clean-1" {
					var res protocol.OperationResultMessage
					_ = json.Unmarshal(m, &res)
					resMsg = &res
				}
			}
		}
		if resMsg != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if resMsg == nil {
		t.Fatalf("expected OPERATION_RESULT to be received by SaaS")
	}

	// DispatchOperation의 safeOperationResult에 의해 UNKNOWN으로 정규화되고 관측 리소스는 보존되어야 함
	if resMsg.Payload.Outcome != "UNKNOWN" {
		t.Fatalf("expected outcome UNKNOWN for unclassified error, got %s", resMsg.Payload.Outcome)
	}
	if len(resMsg.Payload.ProviderResources) != 1 || resMsg.Payload.ProviderResources[0].ProviderID != "server-uuid-dirty" {
		t.Fatalf("expected tracked dirty resource to be preserved, got %+v", resMsg.Payload.ProviderResources)
	}
	// 미분류 raw error 원문은 외부에 노출되지 않아야 함
	if resMsg.Payload.Error != nil && resMsg.Payload.Error.Message == "underlying network socket connection reset by peer" {
		t.Fatalf("raw error string must not be exposed to SaaS")
	}
}

func TestHandler_OperationCommand_MissingCorrelation_Rejected(t *testing.T) {
	testToken := "test-secret-token"
	mockSaaS := mock.NewMockSaaS(testToken)
	defer mockSaaS.Close()

	cfg := wss.Config{
		BaseURL:       mockSaaS.URL(),
		Credential:    testToken,
		AllowInsecure: true,
	}
	client := wss.NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Dial(ctx); err != nil {
		t.Fatalf("client.Dial failed: %v", err)
	}
	defer client.Close()

	if _, err := client.SendHello(ctx); err != nil {
		t.Fatalf("client.SendHello failed: %v", err)
	}

	mockProv := &provider.MockProvider{}
	handler := wss.NewHandler(mockProv, client)
	startTestListener(t, ctx, handler, client)

	// OperationID 누락된 비정상 명령
	invalidCmd := protocol.OperationCommandMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:          protocol.MessageTypeOperationCommand,
			MessageID:     "msg-invalid-1",
			SentAt:        time.Now().UTC(),
			OperationID:   "", // 누락
			LabInstanceID: "inst-1",
			Generation:    1,
		},
		Payload: protocol.OperationCommandPayload{
			MutationType: "PROVISION",
		},
	}

	if err := mockSaaS.SendRaw(invalidCmd); err != nil {
		t.Fatalf("failed to send invalid command: %v", err)
	}

	var protoErrMsg *protocol.ProtocolErrorMessage
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		msgs := mockSaaS.ReceivedMessages()
		for _, m := range msgs {
			var env protocol.BaseEnvelope
			if err := json.Unmarshal(m, &env); err == nil {
				if env.Type == protocol.MessageTypeError && env.ReplyToMessageID == "msg-invalid-1" {
					var protoErr protocol.ProtocolErrorMessage
					_ = json.Unmarshal(m, &protoErr)
					protoErrMsg = &protoErr
				}
			}
		}
		if protoErrMsg != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if protoErrMsg == nil {
		t.Fatalf("expected ProtocolErrorMessage (ERROR) for missing correlation (팀장님 리뷰 2번)")
	}
	if protoErrMsg.Payload.Code != "INVALID_MESSAGE" {
		t.Fatalf("expected ProtocolError code INVALID_MESSAGE, got %+v", protoErrMsg.Payload)
	}
}

func TestHandler_ReconcileRequest_Success(t *testing.T) {
	testToken := "test-secret-token"
	mockSaaS := mock.NewMockSaaS(testToken)
	defer mockSaaS.Close()

	cfg := wss.Config{
		BaseURL:       mockSaaS.URL(),
		Credential:    testToken,
		AllowInsecure: true,
	}
	client := wss.NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Dial(ctx); err != nil {
		t.Fatalf("client.Dial failed: %v", err)
	}
	defer client.Close()

	if _, err := client.SendHello(ctx); err != nil {
		t.Fatalf("client.SendHello failed: %v", err)
	}

	reconcileCalled := false
	mockProv := &provider.MockProvider{
		ReconcileFunc: func(ctx context.Context, req provider.ReconcileRequest) (provider.ReconcileResult, error) {
			reconcileCalled = true
			// 공동 결정: discoverCandidates 생략 시 기본값 true 적용 확인
			if !req.DiscoverCandidates {
				return provider.ReconcileResult{}, fmt.Errorf("expected DiscoverCandidates true by default")
			}
			if len(req.KnownResources) != 1 {
				return provider.ReconcileResult{}, fmt.Errorf("expected 1 known resource")
			}

			return provider.ReconcileResult{
				Observations: []provider.ResourceObservation{
					{
						ResourceType:  "SERVER",
						ProviderID:    req.KnownResources[0].ProviderID,
						Generation:    1,
						Exists:        true,
						ObservedState: "ACTIVE",
						Source:        provider.SourceKnownResource,
					},
					{
						ResourceType:  "PORT",
						ProviderID:    "port-orphan-1",
						Generation:    1,
						Exists:        true,
						ObservedState: "DOWN",
						Source:        provider.SourceDiscoveredCandidate,
					},
				},
			}, nil
		},
	}

	handler := wss.NewHandler(mockProv, client)
	startTestListener(t, ctx, handler, client)

	// discoverCandidates 생략된 Reconcile 요청
	reqMsg := protocol.ReconcileRequestMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:          protocol.MessageTypeReconcileRequest,
			MessageID:     "msg-rec-1",
			SentAt:        time.Now().UTC(),
			OperationID:   "op-rec-100",
			LabInstanceID: "inst-rec-100",
			Generation:    1,
		},
		Payload: protocol.ReconcileRequestPayload{
			KnownResources: []protocol.ProviderResourceRef{
				{
					ResourceType: "SERVER",
					ProviderID:   "server-uuid-reconciled",
					Generation:   1,
				},
			},
			DiscoverCandidates: nil, // 생략 -> 기본값 true
		},
	}

	if err := mockSaaS.SendRaw(reqMsg); err != nil {
		t.Fatalf("failed to send RECONCILE_REQUEST: %v", err)
	}

	var resMsg *protocol.ReconcileResultMessage
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		msgs := mockSaaS.ReceivedMessages()
		for _, m := range msgs {
			var env protocol.BaseEnvelope
			if err := json.Unmarshal(m, &env); err == nil {
				if env.Type == protocol.MessageTypeReconcileResult && env.ReplyToMessageID == "msg-rec-1" {
					var res protocol.ReconcileResultMessage
					_ = json.Unmarshal(m, &res)
					resMsg = &res
				}
			}
		}
		if resMsg != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !reconcileCalled {
		t.Fatalf("expected MockProvider.Reconcile to be called")
	}
	if resMsg == nil {
		t.Fatalf("expected RECONCILE_RESULT to be received by SaaS")
	}
	if len(resMsg.Payload.Observations) != 2 {
		t.Fatalf("expected 2 observations, got %d", len(resMsg.Payload.Observations))
	}
	if resMsg.Payload.Observations[0].Source != "KNOWN_RESOURCE" {
		t.Fatalf("unexpected source: %s", resMsg.Payload.Observations[0].Source)
	}
	if resMsg.Payload.Observations[1].Source != "DISCOVERED_CANDIDATE" {
		t.Fatalf("unexpected candidate source: %s", resMsg.Payload.Observations[1].Source)
	}
}

// TestHandler_OperationCommand_MissingImageId_RejectedWithoutProviderCall 는
// CreationSnapshot 의 ResolvedVmSpec 에 imageId 가 누락된 경우 imageRef 로 대체하지 않고
// Provider 호출 전에 INVALID_COMMAND 로 즉시 거절하는지 검증하는 테스트입니다 (팀장님 리뷰 3번).
func TestHandler_OperationCommand_MissingImageId_RejectedWithoutProviderCall(t *testing.T) {
	testToken := "test-secret-token"
	mockSaaS := mock.NewMockSaaS(testToken)
	defer mockSaaS.Close()

	cfg := wss.Config{
		BaseURL:       mockSaaS.URL(),
		Credential:    testToken,
		AllowInsecure: true,
	}
	client := wss.NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Dial(ctx); err != nil {
		t.Fatalf("client.Dial failed: %v", err)
	}
	defer client.Close()

	if _, err := client.SendHello(ctx); err != nil {
		t.Fatalf("client.SendHello failed: %v", err)
	}

	provisionCalled := false
	mockProv := &provider.MockProvider{
		ProvisionFunc: func(ctx context.Context, req provider.ProvisionRequest) (provider.OperationResult, error) {
			provisionCalled = true
			return provider.OperationResult{Outcome: provider.OutcomeSucceeded}, nil
		},
	}

	handler := wss.NewHandler(mockProv, client)
	startTestListener(t, ctx, handler, client)

	// ImageID 누락 및 ImageRef만 제공된 비정상 명령 (imageRef fallback 금지 검증)
	cmdMsg := protocol.OperationCommandMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:          protocol.MessageTypeOperationCommand,
			MessageID:     "msg-missing-img-1",
			SentAt:        time.Now().UTC(),
			OperationID:   "op-missing-img",
			LabInstanceID: "inst-missing-img",
			Generation:    1,
		},
		Payload: protocol.OperationCommandPayload{
			MutationType: "PROVISION",
			CreationSnapshot: &protocol.CreationSnapshot{
				ProviderConnectionID: "conn-1",
				WorkspaceVMKey:       "vm-1",
				InternetOutbound:     true,
				VMs: []protocol.ResolvedVmSpec{
					{
						VMKey:         "vm-1",
						Role:          "default",
						InstanceIndex: 0,
						ImageID:       "", // ImageID 누락
						ImageRef:      "deprecated-image-ref",
						FlavorID:      "flavor-id-1",
						FlavorSpec: &protocol.ResolvedFlavorSpec{
							VCPUs:   1,
							RAMMiB:  1024,
							DiskGiB: 10,
						},
					},
				},
			},
		},
	}

	if err := mockSaaS.SendRaw(cmdMsg); err != nil {
		t.Fatalf("failed to send command: %v", err)
	}

	var ackMsg *protocol.OperationAckMessage
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		msgs := mockSaaS.ReceivedMessages()
		for _, m := range msgs {
			var env protocol.BaseEnvelope
			if err := json.Unmarshal(m, &env); err == nil {
				if env.Type == protocol.MessageTypeOperationAck && env.ReplyToMessageID == "msg-missing-img-1" {
					var ack protocol.OperationAckMessage
					_ = json.Unmarshal(m, &ack)
					ackMsg = &ack
				}
			}
		}
		if ackMsg != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Provider가 절대 호출되지 않아야 함
	if provisionCalled {
		t.Fatalf("provider must NOT be called when wire validation fails")
	}

	if ackMsg == nil {
		t.Fatalf("expected OPERATION_ACK to be received by SaaS")
	}
	if ackMsg.Payload.Accepted {
		t.Fatalf("expected OPERATION_ACK accepted to be false")
	}
	if ackMsg.Payload.Error == nil || ackMsg.Payload.Error.Code != "INVALID_COMMAND" {
		t.Fatalf("expected SafeError with code INVALID_COMMAND, got %+v", ackMsg.Payload.Error)
	}
}

// TestHandler_OperationCommand_MissingFlavorId_RejectedWithoutProviderCall 는
// CreationSnapshot 의 ResolvedVmSpec 에 flavorId 가 누락된 경우 flavorRef 로 대체하지 않고
// Provider 호출 전에 INVALID_COMMAND 로 즉시 거절하는지 검증하는 테스트입니다.
func TestHandler_OperationCommand_MissingFlavorId_RejectedWithoutProviderCall(t *testing.T) {
	testToken := "test-secret-token"
	mockSaaS := mock.NewMockSaaS(testToken)
	defer mockSaaS.Close()

	cfg := wss.Config{
		BaseURL:       mockSaaS.URL(),
		Credential:    testToken,
		AllowInsecure: true,
	}
	client := wss.NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Dial(ctx); err != nil {
		t.Fatalf("client.Dial failed: %v", err)
	}
	defer client.Close()

	if _, err := client.SendHello(ctx); err != nil {
		t.Fatalf("client.SendHello failed: %v", err)
	}

	provisionCalled := false
	mockProv := &provider.MockProvider{
		ProvisionFunc: func(ctx context.Context, req provider.ProvisionRequest) (provider.OperationResult, error) {
			provisionCalled = true
			return provider.OperationResult{Outcome: provider.OutcomeSucceeded}, nil
		},
	}

	handler := wss.NewHandler(mockProv, client)
	startTestListener(t, ctx, handler, client)

	cmdMsg := protocol.OperationCommandMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:          protocol.MessageTypeOperationCommand,
			MessageID:     "msg-missing-flavor-1",
			SentAt:        time.Now().UTC(),
			OperationID:   "op-missing-flavor",
			LabInstanceID: "inst-missing-flavor",
			Generation:    1,
		},
		Payload: protocol.OperationCommandPayload{
			MutationType: "PROVISION",
			CreationSnapshot: &protocol.CreationSnapshot{
				ProviderConnectionID: "conn-1",
				WorkspaceVMKey:       "vm-1",
				InternetOutbound:     true,
				VMs: []protocol.ResolvedVmSpec{
					{
						VMKey:         "vm-1",
						Role:          "default",
						InstanceIndex: 0,
						ImageID:       "image-id-1",
						FlavorID:      "", // FlavorID 누락
						FlavorRef:     "deprecated-flavor-ref",
						FlavorSpec: &protocol.ResolvedFlavorSpec{
							VCPUs:   1,
							RAMMiB:  1024,
							DiskGiB: 10,
						},
					},
				},
			},
		},
	}

	if err := mockSaaS.SendRaw(cmdMsg); err != nil {
		t.Fatalf("failed to send command: %v", err)
	}

	var ackMsg *protocol.OperationAckMessage
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		msgs := mockSaaS.ReceivedMessages()
		for _, m := range msgs {
			var env protocol.BaseEnvelope
			if err := json.Unmarshal(m, &env); err == nil {
				if env.Type == protocol.MessageTypeOperationAck && env.ReplyToMessageID == "msg-missing-flavor-1" {
					var ack protocol.OperationAckMessage
					_ = json.Unmarshal(m, &ack)
					ackMsg = &ack
				}
			}
		}
		if ackMsg != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if provisionCalled {
		t.Fatalf("provider must NOT be called when wire validation fails")
	}

	if ackMsg == nil {
		t.Fatalf("expected OPERATION_ACK to be received by SaaS")
	}
	if ackMsg.Payload.Accepted {
		t.Fatalf("expected OPERATION_ACK accepted to be false")
	}
	if ackMsg.Payload.Error == nil || ackMsg.Payload.Error.Code != "INVALID_COMMAND" {
		t.Fatalf("expected SafeError with code INVALID_COMMAND, got %+v", ackMsg.Payload.Error)
	}
}

// TestHandler_OperationCommand_MissingCreationSnapshot_Rejected 는
// PROVISION 명령에 CreationSnapshot 이 누락된 경우 Provider 호출 없이 거절되는지 검증합니다.
func TestHandler_OperationCommand_MissingCreationSnapshot_Rejected(t *testing.T) {
	testToken := "test-secret-token"
	mockSaaS := mock.NewMockSaaS(testToken)
	defer mockSaaS.Close()

	cfg := wss.Config{
		BaseURL:       mockSaaS.URL(),
		Credential:    testToken,
		AllowInsecure: true,
	}
	client := wss.NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Dial(ctx); err != nil {
		t.Fatalf("client.Dial failed: %v", err)
	}
	defer client.Close()

	if _, err := client.SendHello(ctx); err != nil {
		t.Fatalf("client.SendHello failed: %v", err)
	}

	provisionCalled := false
	mockProv := &provider.MockProvider{
		ProvisionFunc: func(ctx context.Context, req provider.ProvisionRequest) (provider.OperationResult, error) {
			provisionCalled = true
			return provider.OperationResult{Outcome: provider.OutcomeSucceeded}, nil
		},
	}

	handler := wss.NewHandler(mockProv, client)
	startTestListener(t, ctx, handler, client)

	cmdMsg := protocol.OperationCommandMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:          protocol.MessageTypeOperationCommand,
			MessageID:     "msg-no-snapshot-1",
			SentAt:        time.Now().UTC(),
			OperationID:   "op-no-snapshot",
			LabInstanceID: "inst-no-snapshot",
			Generation:    1,
		},
		Payload: protocol.OperationCommandPayload{
			MutationType:     "PROVISION",
			CreationSnapshot: nil, // 누락
		},
	}

	if err := mockSaaS.SendRaw(cmdMsg); err != nil {
		t.Fatalf("failed to send command: %v", err)
	}

	var ackMsg *protocol.OperationAckMessage
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		msgs := mockSaaS.ReceivedMessages()
		for _, m := range msgs {
			var env protocol.BaseEnvelope
			if err := json.Unmarshal(m, &env); err == nil {
				if env.Type == protocol.MessageTypeOperationAck && env.ReplyToMessageID == "msg-no-snapshot-1" {
					var ack protocol.OperationAckMessage
					_ = json.Unmarshal(m, &ack)
					ackMsg = &ack
				}
			}
		}
		if ackMsg != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if provisionCalled {
		t.Fatalf("provider must NOT be called when creationSnapshot is missing")
	}

	if ackMsg == nil {
		t.Fatalf("expected OPERATION_ACK to be received by SaaS")
	}
	if ackMsg.Payload.Accepted {
		t.Fatalf("expected OPERATION_ACK accepted to be false")
	}
}

// TestHandler_OperationCommand_Cleanup_MissingProviderResources_Rejected 는
// CLEANUP 요청에서 providerResources 필드가 누락(nil)된 경우 Provider 호출 없이 즉시 거절되는지 검증합니다 (팀장님 리뷰 1번).
func TestHandler_OperationCommand_Cleanup_MissingProviderResources_Rejected(t *testing.T) {
	testToken := "test-secret-token"
	mockSaaS := mock.NewMockSaaS(testToken)
	defer mockSaaS.Close()

	cfg := wss.Config{
		BaseURL:       mockSaaS.URL(),
		Credential:    testToken,
		AllowInsecure: true,
	}
	client := wss.NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Dial(ctx); err != nil {
		t.Fatalf("client.Dial failed: %v", err)
	}
	defer client.Close()

	if _, err := client.SendHello(ctx); err != nil {
		t.Fatalf("client.SendHello failed: %v", err)
	}

	cleanupCalled := false
	mockProv := &provider.MockProvider{
		CleanupFunc: func(ctx context.Context, req provider.CleanupRequest) (provider.OperationResult, error) {
			cleanupCalled = true
			return provider.OperationResult{Outcome: provider.OutcomeSucceeded}, nil
		},
	}

	handler := wss.NewHandler(mockProv, client)
	startTestListener(t, ctx, handler, client)

	// CLEANUP 요청이지만 ProviderResources 가 nil 로 누락된 비정상 명령
	cmdMsg := protocol.OperationCommandMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:          protocol.MessageTypeOperationCommand,
			MessageID:     "msg-cleanup-no-resources",
			SentAt:        time.Now().UTC(),
			OperationID:   "op-cleanup-fail",
			LabInstanceID: "inst-cleanup-fail",
			Generation:    1,
		},
		Payload: protocol.OperationCommandPayload{
			MutationType:      "CLEANUP",
			ProviderResources: nil, // 누락
		},
	}

	if err := mockSaaS.SendRaw(cmdMsg); err != nil {
		t.Fatalf("failed to send command: %v", err)
	}

	var ackMsg *protocol.OperationAckMessage
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		msgs := mockSaaS.ReceivedMessages()
		for _, m := range msgs {
			var env protocol.BaseEnvelope
			if err := json.Unmarshal(m, &env); err == nil {
				if env.Type == protocol.MessageTypeOperationAck && env.ReplyToMessageID == "msg-cleanup-no-resources" {
					var ack protocol.OperationAckMessage
					_ = json.Unmarshal(m, &ack)
					ackMsg = &ack
				}
			}
		}
		if ackMsg != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if cleanupCalled {
		t.Fatalf("provider Cleanup must NOT be called when providerResources is missing")
	}

	if ackMsg == nil {
		t.Fatalf("expected OPERATION_ACK to be received by SaaS")
	}
	if ackMsg.Payload.Accepted {
		t.Fatalf("expected OPERATION_ACK accepted to be false")
	}
	if ackMsg.Payload.Error == nil || ackMsg.Payload.Error.Code != "INVALID_COMMAND" {
		t.Fatalf("expected Error code INVALID_COMMAND, got %+v", ackMsg.Payload.Error)
	}
}

// TestHandler_ReconcileRequest_MissingKnownResources_Rejected 는
// RECONCILE 요청에서 knownResources 필드가 누락(nil)된 경우 INVALID_REQUEST 로 거절되는지 검증합니다 (팀장님 리뷰 3번).
func TestHandler_ReconcileRequest_MissingKnownResources_Rejected(t *testing.T) {
	testToken := "test-secret-token"
	mockSaaS := mock.NewMockSaaS(testToken)
	defer mockSaaS.Close()

	cfg := wss.Config{
		BaseURL:       mockSaaS.URL(),
		Credential:    testToken,
		AllowInsecure: true,
	}
	client := wss.NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Dial(ctx); err != nil {
		t.Fatalf("client.Dial failed: %v", err)
	}
	defer client.Close()

	if _, err := client.SendHello(ctx); err != nil {
		t.Fatalf("client.SendHello failed: %v", err)
	}

	reconcileCalled := false
	mockProv := &provider.MockProvider{
		ReconcileFunc: func(ctx context.Context, req provider.ReconcileRequest) (provider.ReconcileResult, error) {
			reconcileCalled = true
			return provider.ReconcileResult{}, nil
		},
	}

	handler := wss.NewHandler(mockProv, client)
	startTestListener(t, ctx, handler, client)

	// knownResources 가 nil 로 누락된 비정상 reconcile 요청
	reqMsg := protocol.ReconcileRequestMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:          protocol.MessageTypeReconcileRequest,
			MessageID:     "msg-reconcile-no-known",
			SentAt:        time.Now().UTC(),
			OperationID:   "op-reconcile-fail",
			LabInstanceID: "inst-reconcile-fail",
			Generation:    1,
		},
		Payload: protocol.ReconcileRequestPayload{
			KnownResources: nil, // 누락
		},
	}

	if err := mockSaaS.SendRaw(reqMsg); err != nil {
		t.Fatalf("failed to send reconcile request: %v", err)
	}

	var resMsg *protocol.ReconcileResultMessage
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		msgs := mockSaaS.ReceivedMessages()
		for _, m := range msgs {
			var env protocol.BaseEnvelope
			if err := json.Unmarshal(m, &env); err == nil {
				if env.Type == protocol.MessageTypeReconcileResult && env.ReplyToMessageID == "msg-reconcile-no-known" {
					var res protocol.ReconcileResultMessage
					_ = json.Unmarshal(m, &res)
					resMsg = &res
				}
			}
		}
		if resMsg != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if reconcileCalled {
		t.Fatalf("provider Reconcile must NOT be called when knownResources is missing")
	}

	if resMsg == nil {
		t.Fatalf("expected RECONCILE_RESULT to be received by SaaS")
	}
	if resMsg.Payload.Error == nil || resMsg.Payload.Error.Code != "INVALID_REQUEST" {
		t.Fatalf("expected Error code INVALID_REQUEST, got %+v", resMsg.Payload.Error)
	}
}

// TestHandler_Listen_HandleMessageErrorCallbackInvoked 는
// Listen 루프에서 HandleMessage 처리 중 발생한 에러가 onError 콜백으로 전달되는지 검증합니다 (팀장님 리뷰 6번).
func TestHandler_Listen_HandleMessageErrorCallbackInvoked(t *testing.T) {
	testToken := "test-secret-token"
	mockSaaS := mock.NewMockSaaS(testToken)
	defer mockSaaS.Close()

	cfg := wss.Config{
		BaseURL:       mockSaaS.URL(),
		Credential:    testToken,
		AllowInsecure: true,
	}
	client := wss.NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Dial(ctx); err != nil {
		t.Fatalf("client.Dial failed: %v", err)
	}
	defer client.Close()

	if _, err := client.SendHello(ctx); err != nil {
		t.Fatalf("client.SendHello failed: %v", err)
	}

	mockProv := &provider.MockProvider{}
	handler := wss.NewHandler(mockProv, client)

	errCh := make(chan error, 1)
	handler.SetOnError(func(err error) {
		select {
		case errCh <- err:
		default:
		}
	})

	startTestListener(t, ctx, handler, client)

	// 잘못된 JSON 전송으로 HandleMessage 에러 유발
	if err := mockSaaS.SendBytes([]byte("invalid-raw-json-message")); err != nil {
		t.Fatalf("failed to send raw message: %v", err)
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected non-nil error from HandleMessage callback")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for onError callback in Handler.Listen")
	}
}

// startTestListener 는 리스너 고루틴을 띄우기 전에 connection이 유효함을 동기적으로 검증하고,
// 테스트 종료 시 리스너 고루틴이 완전히 종료(cleanup)되었음을 보장하여 고루틴 누수를 방지합니다 (LBT-93).
func startTestListener(t *testing.T, ctx context.Context, handler *wss.Handler, client *wss.Client) <-chan error {
	t.Helper()
	conn := client.Conn()
	if conn == nil {
		t.Fatal("cannot start listener: client.Conn() is nil")
	}
	done := make(chan error, 1)
	go func() {
		done <- handler.Listen(ctx, conn)
	}()
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Errorf("listener goroutine failed to stop within timeout")
		}
	})
	return done
}

// TestHandler_Listen_NilConnection_ReturnsError 는 nil websocket.Conn 전달 시
// nil pointer dereference 패닉 대신 명시적 에러를 반환하는지 검증합니다 (LBT-93).
func TestHandler_Listen_NilConnection_ReturnsError(t *testing.T) {
	handler := wss.NewHandler(nil, nil)
	err := handler.Listen(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error when passing nil connection to Listen, got nil")
	}
}
