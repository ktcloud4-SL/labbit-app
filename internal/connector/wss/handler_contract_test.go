package wss_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
)

// 이 file은 connector.schema.json이 허용하는 값을 Handler가 계약보다 엄격하게 거절하지 않는지 확인한다.
// wire는 구조체 encoding에 의존하지 않도록 raw JSON으로 정확히 지정한다.

// emptySHA256는 빈 문자열의 SHA-256 hex digest(64자리)다.
const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// contractHarness는 Mock SaaS에 연결된 실제 wss.Handler다. 서버 쪽 raw frame을 보내고 그 응답을 기다린다.
type contractHarness struct {
	t    *testing.T
	saas *mock.MockSaaS
}

func newContractHarness(t *testing.T, prov *provider.MockProvider) *contractHarness {
	t.Helper()
	const token = "contract-test-token"
	saas := mock.NewMockSaaS(token)
	t.Cleanup(saas.Close)

	client := wss.NewClient(wss.Config{BaseURL: saas.URL(), Credential: token, AllowInsecure: true})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := client.Dial(ctx); err != nil {
		cancel()
		t.Fatalf("client.Dial failed: %v", err)
	}
	if _, err := client.SendHello(ctx); err != nil {
		cancel()
		_ = client.Close()
		t.Fatalf("client.SendHello failed: %v", err)
	}
	// t.Cleanup은 LIFO다. listener 종료 대기(startTestListener)보다 나중에 등록해야 client를 먼저 닫아 Listen이 끝난 뒤에 기다린다.
	startTestListener(t, ctx, wss.NewHandler(prov, client), client)
	t.Cleanup(func() { _ = client.Close() })
	t.Cleanup(cancel)
	return &contractHarness{t: t, saas: saas}
}

// send는 raw JSON frame을 그대로 보낸다.
func (h *contractHarness) send(frame string) {
	h.t.Helper()
	if !json.Valid([]byte(frame)) {
		h.t.Fatalf("test frame이 JSON이 아님: %s", frame)
	}
	if err := h.saas.SendBytes([]byte(frame)); err != nil {
		h.t.Fatalf("SendBytes() error = %v", err)
	}
}

// replies는 messageID에 대한 OPERATION_ACK와 OPERATION_RESULT를 기다려 반환한다.
func (h *contractHarness) replies(messageID string) (ack protocol.OperationAckMessage, result protocol.OperationResultMessage) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var gotAck, gotResult bool
	for time.Now().Before(deadline) && !(gotAck && gotResult) {
		for _, raw := range h.saas.ReceivedMessages() {
			var env protocol.BaseEnvelope
			if json.Unmarshal(raw, &env) != nil || env.ReplyToMessageID != messageID {
				continue
			}
			switch env.Type {
			case protocol.MessageTypeOperationAck:
				gotAck = json.Unmarshal(raw, &ack) == nil
			case protocol.MessageTypeOperationResult:
				gotResult = json.Unmarshal(raw, &result) == nil
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !gotAck || !gotResult {
		h.t.Fatalf("messageId %s의 ACK(%v)/RESULT(%v)를 받지 못함", messageID, gotAck, gotResult)
	}
	return ack, result
}

// ackOnly는 messageID에 대한 OPERATION_ACK만 기다린다(거절 ACK는 RESULT가 없다).
func (h *contractHarness) ackOnly(messageID string) protocol.OperationAckMessage {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, raw := range h.saas.ReceivedMessages() {
			var ack protocol.OperationAckMessage
			if json.Unmarshal(raw, &ack) == nil && ack.Type == protocol.MessageTypeOperationAck && ack.ReplyToMessageID == messageID {
				return ack
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("messageId %s의 ACK를 받지 못함", messageID)
	return protocol.OperationAckMessage{}
}

func commandFrame(messageID, payload string) string {
	return fmt.Sprintf(`{"type":"OPERATION_COMMAND","messageId":%q,"sentAt":%q,"operationId":"op-1","labInstanceId":"lab-1","generation":2,"payload":%s}`,
		messageID, time.Now().UTC().Format(time.RFC3339Nano), payload)
}

func provisionPayload(startupScript string) string {
	script := ""
	if startupScript != "" {
		script = `,"startupScript":` + startupScript
	}
	return `{"mutationType":"PROVISION","creationSnapshot":{"providerConnectionId":"pc-1","vms":[{"vmKey":"vm-1","role":"workspace","instanceIndex":0,"imageId":"img-1","flavorId":"fl-1","flavorSpec":{"vcpus":1,"ramMiB":512,"diskGiB":0}}],"workspaceVmKey":"vm-1","internetOutbound":false` + script + `}}`
}

// CLEANUP의 providerResources는 property가 required이지만 array에 minItems가 없다. 빈 배열은 Schema-valid이므로 거절하지 않고
// Provider까지 전달한다. 빈 목록은 "누락"과 구분되어 non-nil로 유지된다.
func TestHandler_Cleanup_EmptyProviderResources_IsSchemaValid(t *testing.T) {
	var mu sync.Mutex
	var calls int
	var got provider.CleanupRequest
	h := newContractHarness(t, &provider.MockProvider{
		CleanupFunc: func(_ context.Context, req provider.CleanupRequest) (provider.OperationResult, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			got = req
			return provider.OperationResult{Outcome: provider.OutcomeSucceeded}, nil
		},
	})

	h.send(commandFrame("msg-empty-cleanup", `{"mutationType":"CLEANUP","providerResources":[]}`))
	ack, result := h.replies("msg-empty-cleanup")

	if !ack.Payload.Accepted || ack.Payload.Error != nil {
		t.Fatalf("빈 providerResources의 CLEANUP이 거절됨: %+v", ack.Payload)
	}
	if result.Payload.Outcome != protocol.OutcomeSucceeded || result.OperationID != "op-1" || result.LabInstanceID != "lab-1" || result.Generation != 2 {
		t.Fatalf("RESULT = %+v %+v", result.BaseEnvelope, result.Payload)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("Provider.Cleanup 호출 = %d번, want 1", calls)
	}
	if got.ProviderResources == nil || len(got.ProviderResources) != 0 {
		t.Fatalf("Provider가 받은 ProviderResources = %#v, want 빈 non-nil 목록", got.ProviderResources)
	}
	if got.Correlation.OperationID != "op-1" || got.Correlation.Generation != 2 {
		t.Fatalf("Provider가 받은 correlation = %+v", got.Correlation)
	}
}

// startupScript.content는 string이며 minLength가 없다. content가 빈 문자열인 script도 Schema-valid이므로 거절하지 않는다.
func TestHandler_Provision_EmptyStartupScriptContent_IsSchemaValid(t *testing.T) {
	var mu sync.Mutex
	var calls int
	var got provider.ProvisionRequest
	h := newContractHarness(t, &provider.MockProvider{
		ProvisionFunc: func(_ context.Context, req provider.ProvisionRequest) (provider.OperationResult, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			got = req
			return provider.OperationResult{Outcome: provider.OutcomeSucceeded}, nil
		},
	})

	h.send(commandFrame("msg-empty-script", provisionPayload(`{"content":"","sha256":"`+emptySHA256+`"}`)))
	ack, result := h.replies("msg-empty-script")

	if !ack.Payload.Accepted || ack.Payload.Error != nil {
		t.Fatalf("빈 content의 startupScript가 거절됨: %+v", ack.Payload)
	}
	if result.Payload.Outcome != protocol.OutcomeSucceeded {
		t.Fatalf("RESULT = %+v", result.Payload)
	}
	mu.Lock()
	defer mu.Unlock()
	script := got.CreationSnapshot.StartupScript
	if calls != 1 || script == nil || script.Content != "" || script.SHA256 != emptySHA256 {
		t.Fatalf("Provider 호출 %d번, StartupScript = %+v, want 1번, {content:\"\" sha256:%s}", calls, script, emptySHA256)
	}
}

// startupScript object가 없는 것(optional)과 sha256의 Schema 제약은 그대로 유지된다. 빈 content 허용이 sha256 검증을 약화하지 않는다.
func TestHandler_Provision_StartupScriptConstraintsAreKept(t *testing.T) {
	var mu sync.Mutex
	var scripts []*provider.StartupScript
	h := newContractHarness(t, &provider.MockProvider{
		ProvisionFunc: func(_ context.Context, req provider.ProvisionRequest) (provider.OperationResult, error) {
			mu.Lock()
			defer mu.Unlock()
			scripts = append(scripts, req.CreationSnapshot.StartupScript)
			return provider.OperationResult{Outcome: provider.OutcomeSucceeded}, nil
		},
	})

	// startupScript 없음: optional이므로 그대로 받아들인다.
	h.send(commandFrame("msg-no-script", provisionPayload("")))
	if ack, _ := h.replies("msg-no-script"); !ack.Payload.Accepted {
		t.Fatalf("startupScript가 없는 command가 거절됨: %+v", ack.Payload)
	}

	// sha256이 64자리가 아니면 content가 비어 있어도 거절한다. raw script 값은 오류 문구에 복사하지 않는다.
	const secretScript = "curl http://internal.example/secret-token-marker-6b0e"
	h.send(commandFrame("msg-bad-sha", provisionPayload(`{"content":"`+secretScript+`","sha256":"abc"}`)))
	ack := h.ackOnly("msg-bad-sha")
	if ack.Payload.Accepted || ack.Payload.Error == nil || ack.Payload.Error.Code != "INVALID_COMMAND" {
		t.Fatalf("잘못된 sha256이 받아들여짐: %+v", ack.Payload)
	}
	if strings.Contains(ack.Payload.Error.Message, "secret-token-marker") {
		t.Fatalf("오류 문구가 script 원문을 복사함: %q", ack.Payload.Error.Message)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(scripts) != 1 || scripts[0] != nil {
		t.Fatalf("Provider 호출 = %d번 %v, want 1번(startupScript 없음 command만)", len(scripts), scripts)
	}
}
