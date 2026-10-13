package wss_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/preview"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
)

func TestHandler_PreviewOpenAndClose(t *testing.T) {
	// 1. Mock Target TCP Server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port

	// 2. Mock SaaS Preview Gateway
	gateway := mock.NewPreviewGateway()
	defer gateway.Close()

	mockProv := &provider.MockProvider{}
	sentMessages := make(chan interface{}, 10)
	sender := wss.SendMessageFunc(func(ctx context.Context, msg interface{}) error {
		sentMessages <- msg
		return nil
	})

	h := wss.NewHandler(mockProv, sender)

	forwarder := preview.NewDirectTCPForwarder(nil)
	mgr := preview.NewSessionManager(forwarder, nil)

	h.SetPreviewManager(mgr, preview.DataWSSClientConfig{
		EndpointURL:   gateway.URL(),
		Credential:    "test-token",
		RuntimeID:     "runtime-test-handler",
		DialTimeout:   2 * time.Second,
		AllowInsecure: true,
	})

	// 3. PREVIEW_OPEN 메시지 전송
	sessID := "preview-control-sess-1"
	openReq := protocol.PreviewOpenMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:             protocol.MessageTypePreviewOpen,
			MessageID:        "open-msg-1",
			SentAt:           time.Now().UTC(),
			PreviewSessionID: sessID,
			LabInstanceID:    "inst-1",
			Generation:       1,
		},
		Payload: protocol.PreviewOpenPayload{
			TargetVmKey:      "vm-web-1",
			ProviderServerID: "srv-uuid-1",
			TargetPort:       port,
		},
	}
	openRaw, _ := json.Marshal(openReq)

	if err := h.HandleMessage(context.Background(), openRaw); err != nil {
		t.Fatalf("HandleMessage(PREVIEW_OPEN) failed: %v", err)
	}

	// 4. PREVIEW_OPEN_RESULT (SUCCEEDED) 회신 검증
	select {
	case msg := <-sentMessages:
		res, ok := msg.(protocol.PreviewOpenResultMessage)
		if !ok {
			t.Fatalf("expected PreviewOpenResultMessage, got %T", msg)
		}
		if res.Payload.Outcome != protocol.OutcomeSucceeded {
			t.Fatalf("expected outcome SUCCEEDED, got %s (err: %+v)", res.Payload.Outcome, res.Payload.Error)
		}
		if res.ReplyToMessageID != "open-msg-1" {
			t.Fatalf("expected replyTo open-msg-1, got %s", res.ReplyToMessageID)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for PREVIEW_OPEN_RESULT")
	}

	// Gateway 측 ATTACH 수신 확인
	_, err = gateway.WaitForAttach(2 * time.Second)
	if err != nil {
		t.Fatalf("WaitForAttach failed: %v", err)
	}

	// 5. 세션 등록 확인
	session, exists := mgr.GetSession(sessID)
	if !exists {
		t.Fatalf("expected session to be registered in manager")
	}
	if session.TargetPort != port {
		t.Fatalf("expected port %d, got %d", port, session.TargetPort)
	}

	// 6. PREVIEW_CLOSE 메시지 전송
	closeReq := protocol.PreviewCloseMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:             protocol.MessageTypePreviewClose,
			MessageID:        "close-msg-1",
			SentAt:           time.Now().UTC(),
			PreviewSessionID: sessID,
			LabInstanceID:    "inst-1",
			Generation:       1,
		},
		Payload: protocol.PreviewClosePayload{
			Reason: protocol.PreviewReasonSessionClosed,
		},
	}
	closeRaw, _ := json.Marshal(closeReq)

	if err := h.HandleMessage(context.Background(), closeRaw); err != nil {
		t.Fatalf("HandleMessage(PREVIEW_CLOSE) failed: %v", err)
	}

	// 7. 세션 종료 확인 (LBT-101 wire contract: PREVIEW_CLOSE는 별도 제어 응답 없이 세션을 정리함)
	if session.GetStatus() != preview.StatusClosed {
		t.Fatalf("expected session status CLOSED, got %s", session.GetStatus())
	}
}

func TestHandler_PreviewOpen_SSHPort22_Rejected(t *testing.T) {
	mockProv := &provider.MockProvider{}
	sentMessages := make(chan interface{}, 10)
	sender := wss.SendMessageFunc(func(ctx context.Context, msg interface{}) error {
		sentMessages <- msg
		return nil
	})

	h := wss.NewHandler(mockProv, sender)
	forwarder := preview.NewDirectTCPForwarder(nil)
	mgr := preview.NewSessionManager(forwarder, nil)
	h.SetPreviewManager(mgr, preview.DataWSSClientConfig{
		EndpointURL:   "ws://localhost:9999",
		AllowInsecure: true,
	})

	// SSH 관리 포트 22 차단 검증
	openReq := protocol.PreviewOpenMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:             protocol.MessageTypePreviewOpen,
			MessageID:        "open-ssh-msg",
			SentAt:           time.Now().UTC(),
			PreviewSessionID: "sess-ssh",
			LabInstanceID:    "inst-1",
			Generation:       1,
		},
		Payload: protocol.PreviewOpenPayload{
			TargetVmKey:      "vm-1",
			ProviderServerID: "srv-1",
			TargetPort:       22, // Port 22 should be rejected!
		},
	}
	openRaw, _ := json.Marshal(openReq)

	_ = h.HandleMessage(context.Background(), openRaw)

	select {
	case msg := <-sentMessages:
		res, ok := msg.(protocol.PreviewOpenResultMessage)
		if !ok {
			t.Fatalf("expected PreviewOpenResultMessage, got %T", msg)
		}
		if res.Payload.Outcome != protocol.OutcomeFailed {
			t.Fatalf("expected outcome FAILED, got %s", res.Payload.Outcome)
		}
		if res.Payload.Error == nil || res.Payload.Error.Code != protocol.PreviewErrPortRejected {
			t.Fatalf("expected error code %s, got %+v", protocol.PreviewErrPortRejected, res.Payload.Error)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for PREVIEW_OPEN_RESULT")
	}
}

func TestHandler_PreviewOpen_Duplicate_Idempotent(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port

	gateway := mock.NewPreviewGateway()
	defer gateway.Close()

	mockProv := &provider.MockProvider{}
	sentMessages := make(chan interface{}, 10)
	sender := wss.SendMessageFunc(func(ctx context.Context, msg interface{}) error {
		sentMessages <- msg
		return nil
	})

	h := wss.NewHandler(mockProv, sender)
	forwarder := preview.NewDirectTCPForwarder(nil)
	mgr := preview.NewSessionManager(forwarder, nil)
	h.SetPreviewManager(mgr, preview.DataWSSClientConfig{
		EndpointURL:   gateway.URL(),
		Credential:    "test-token",
		RuntimeID:     "runtime-dup",
		DialTimeout:   2 * time.Second,
		AllowInsecure: true,
	})

	openReq := protocol.PreviewOpenMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:             protocol.MessageTypePreviewOpen,
			MessageID:        "open-msg-1",
			SentAt:           time.Now().UTC(),
			PreviewSessionID: "sess-dup-test",
			LabInstanceID:    "inst-1",
			Generation:       1,
		},
		Payload: protocol.PreviewOpenPayload{
			TargetVmKey:      "vm-1",
			ProviderServerID: "srv-1",
			TargetPort:       port,
		},
	}
	openRaw, _ := json.Marshal(openReq)

	// 첫 번째 PREVIEW_OPEN
	if err := h.HandleMessage(context.Background(), openRaw); err != nil {
		t.Fatalf("first HandleMessage failed: %v", err)
	}

	<-sentMessages // drain first PREVIEW_OPEN_RESULT

	// Gateway 측 ATTACH 수신 확인
	_, err = gateway.WaitForAttach(2 * time.Second)
	if err != nil {
		t.Fatalf("WaitForAttach failed: %v", err)
	}

	// 두 번째 동일한 PREVIEW_OPEN (동일 messageId 멱등 재시도)
	openReq2 := openReq
	openReq2.MessageID = "open-msg-1"
	openRaw2, _ := json.Marshal(openReq2)

	if err := h.HandleMessage(context.Background(), openRaw2); err != nil {
		t.Fatalf("second HandleMessage failed: %v", err)
	}

	select {
	case msg := <-sentMessages:
		res, ok := msg.(protocol.PreviewOpenResultMessage)
		if !ok {
			t.Fatalf("expected PreviewOpenResultMessage, got %T", msg)
		}
		if res.Payload.Outcome != protocol.OutcomeSucceeded {
			t.Fatalf("expected outcome SUCCEEDED on idempotent request, got %s", res.Payload.Outcome)
		}
		if res.ReplyToMessageID != "open-msg-1" {
			t.Fatalf("expected replyTo open-msg-1, got %s", res.ReplyToMessageID)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for second PREVIEW_OPEN_RESULT")
	}

	// 동일 openMessageId 재시도 시 중복 Attach가 발생하지 않았는지 확인
	secondAttach, err := gateway.WaitForAttach(200 * time.Millisecond)
	if err == nil {
		t.Fatalf("unexpected second attach received on idempotent retry: %+v", secondAttach)
	}
}

func TestHandler_PreviewOpen_Conflict_Rejected(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port

	gateway := mock.NewPreviewGateway()
	defer gateway.Close()

	mockProv := &provider.MockProvider{}
	sentMessages := make(chan interface{}, 10)
	sender := wss.SendMessageFunc(func(ctx context.Context, msg interface{}) error {
		sentMessages <- msg
		return nil
	})

	h := wss.NewHandler(mockProv, sender)
	forwarder := preview.NewDirectTCPForwarder(nil)
	mgr := preview.NewSessionManager(forwarder, nil)
	h.SetPreviewManager(mgr, preview.DataWSSClientConfig{
		EndpointURL:   gateway.URL(),
		Credential:    "test-token",
		RuntimeID:     "runtime-conflict",
		DialTimeout:   2 * time.Second,
		AllowInsecure: true,
	})

	openReq := protocol.PreviewOpenMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:             protocol.MessageTypePreviewOpen,
			MessageID:        "open-msg-1",
			SentAt:           time.Now().UTC(),
			PreviewSessionID: "sess-conflict-test",
			LabInstanceID:    "inst-1",
			Generation:       1,
		},
		Payload: protocol.PreviewOpenPayload{
			TargetVmKey:      "vm-1",
			ProviderServerID: "srv-1",
			TargetPort:       port,
		},
	}
	openRaw, _ := json.Marshal(openReq)
	_ = h.HandleMessage(context.Background(), openRaw)
	<-sentMessages // drain result
	_, _ = gateway.WaitForAttach(2 * time.Second)

	// 세션 ID는 같지만 generation 또는 포트가 다른 충돌 요청
	openConflict := openReq
	openConflict.MessageID = "open-msg-conflict"
	openConflict.Generation = 2 // generation 불일치!
	openRawConflict, _ := json.Marshal(openConflict)

	_ = h.HandleMessage(context.Background(), openRawConflict)

	select {
	case msg := <-sentMessages:
		res, ok := msg.(protocol.PreviewOpenResultMessage)
		if !ok {
			t.Fatalf("expected PreviewOpenResultMessage, got %T", msg)
		}
		if res.Payload.Outcome != protocol.OutcomeFailed {
			t.Fatalf("expected outcome FAILED for conflict, got %s", res.Payload.Outcome)
		}
		if res.Payload.Error == nil || res.Payload.Error.Code != protocol.PreviewErrUnavailable {
			t.Fatalf("expected error code %s, got %+v", protocol.PreviewErrUnavailable, res.Payload.Error)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for conflict PREVIEW_OPEN_RESULT")
	}
}

func TestHandler_PreviewOpen_MissingEndpoint_Fails(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port

	mockProv := &provider.MockProvider{}
	sentMessages := make(chan interface{}, 10)
	sender := wss.SendMessageFunc(func(ctx context.Context, msg interface{}) error {
		sentMessages <- msg
		return nil
	})

	h := wss.NewHandler(mockProv, sender)
	forwarder := preview.NewDirectTCPForwarder(nil)
	mgr := preview.NewSessionManager(forwarder, nil)

	// Endpoint 미설정
	h.SetPreviewManager(mgr, preview.DataWSSClientConfig{
		EndpointURL: "",
	})

	openReq := protocol.PreviewOpenMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:             protocol.MessageTypePreviewOpen,
			MessageID:        "open-no-ep",
			SentAt:           time.Now().UTC(),
			PreviewSessionID: "sess-no-ep",
			LabInstanceID:    "inst-1",
			Generation:       1,
		},
		Payload: protocol.PreviewOpenPayload{
			TargetVmKey:      "vm-1",
			ProviderServerID: "srv-1",
			TargetPort:       port,
		},
	}
	openRaw, _ := json.Marshal(openReq)

	err = h.HandleMessage(context.Background(), openRaw)
	if err == nil {
		t.Fatalf("expected error for missing endpoint, got nil")
	}

	select {
	case msg := <-sentMessages:
		res, ok := msg.(protocol.PreviewOpenResultMessage)
		if !ok {
			t.Fatalf("expected PreviewOpenResultMessage, got %T", msg)
		}
		if res.Payload.Outcome != protocol.OutcomeFailed {
			t.Fatalf("expected outcome FAILED, got %s", res.Payload.Outcome)
		}
		if res.Payload.Error == nil || res.Payload.Error.Code != protocol.PreviewErrUnavailable {
			t.Fatalf("expected error code %s, got %+v", protocol.PreviewErrUnavailable, res.Payload.Error)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for PREVIEW_OPEN_RESULT")
	}
}

func TestHandler_PreviewOpen_InvalidEnvelopeOrPayload_Fails(t *testing.T) {
	mockProv := &provider.MockProvider{}
	sentMessages := make(chan interface{}, 10)
	sender := wss.SendMessageFunc(func(ctx context.Context, msg interface{}) error {
		sentMessages <- msg
		return nil
	})

	h := wss.NewHandler(mockProv, sender)
	forwarder := preview.NewDirectTCPForwarder(nil)
	mgr := preview.NewSessionManager(forwarder, nil)
	h.SetPreviewManager(mgr, preview.DataWSSClientConfig{
		EndpointURL:   "ws://localhost:9999",
		AllowInsecure: true,
	})

	// 포트 누락/잘못된 포트
	openReq := protocol.PreviewOpenMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:             protocol.MessageTypePreviewOpen,
			MessageID:        "open-invalid-port",
			SentAt:           time.Now().UTC(),
			PreviewSessionID: "sess-inv",
			LabInstanceID:    "inst-1",
			Generation:       1,
		},
		Payload: protocol.PreviewOpenPayload{
			TargetVmKey:      "vm-1",
			ProviderServerID: "srv-1",
			TargetPort:       0, // invalid port!
		},
	}
	openRaw, _ := json.Marshal(openReq)

	err := h.HandleMessage(context.Background(), openRaw)
	if err == nil {
		t.Fatalf("expected error for invalid port, got nil")
	}

	select {
	case msg := <-sentMessages:
		res, ok := msg.(protocol.PreviewOpenResultMessage)
		if !ok {
			t.Fatalf("expected PreviewOpenResultMessage, got %T", msg)
		}
		if res.Payload.Outcome != protocol.OutcomeFailed {
			t.Fatalf("expected outcome FAILED, got %s", res.Payload.Outcome)
		}
		if res.Payload.Error == nil || res.Payload.Error.Code != protocol.PreviewErrPortRejected {
			t.Fatalf("expected error code %s, got %+v", protocol.PreviewErrPortRejected, res.Payload.Error)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for PREVIEW_OPEN_RESULT")
	}
}

func TestHandler_PreviewOpen_DialFailure_SafeErrorClassification(t *testing.T) {
	gateway := mock.NewPreviewGateway()
	defer gateway.Close()

	mockProv := &provider.MockProvider{}
	sentMessages := make(chan interface{}, 10)
	sender := wss.SendMessageFunc(func(ctx context.Context, msg interface{}) error {
		sentMessages <- msg
		return nil
	})

	h := wss.NewHandler(mockProv, sender)
	forwarder := preview.NewDirectTCPForwarder(nil)
	mgr := preview.NewSessionManager(forwarder, nil)
	h.SetPreviewManager(mgr, preview.DataWSSClientConfig{
		EndpointURL:   gateway.URL(),
		Credential:    "token",
		RuntimeID:     "runtime-fail",
		DialTimeout:   2 * time.Second,
		AllowInsecure: true,
	})

	// 미사용 포트 (connection refused)
	openReq := protocol.PreviewOpenMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:             protocol.MessageTypePreviewOpen,
			MessageID:        "open-refused",
			SentAt:           time.Now().UTC(),
			PreviewSessionID: "sess-refused",
			LabInstanceID:    "inst-1",
			Generation:       1,
		},
		Payload: protocol.PreviewOpenPayload{
			TargetVmKey:      "vm-1",
			ProviderServerID: "srv-1",
			TargetPort:       58999, // Unused port
		},
	}
	openRaw, _ := json.Marshal(openReq)

	_ = h.HandleMessage(context.Background(), openRaw)

	select {
	case msg := <-sentMessages:
		res, ok := msg.(protocol.PreviewOpenResultMessage)
		if !ok {
			t.Fatalf("expected PreviewOpenResultMessage, got %T", msg)
		}
		if res.Payload.Outcome != protocol.OutcomeFailed {
			t.Fatalf("expected outcome FAILED, got %s", res.Payload.Outcome)
		}
		if res.Payload.Error == nil {
			t.Fatalf("expected error object, got nil")
		}
		if res.Payload.Error.Code != protocol.PreviewErrAppNotRunning {
			t.Fatalf("expected error code %s, got %s", protocol.PreviewErrAppNotRunning, res.Payload.Error.Code)
		}
		// 사설 IP나 상세 스택트레이스가 포함되지 않았는지 검증
		if strings.Contains(res.Payload.Error.Message, "127.0.0.1") || strings.Contains(res.Payload.Error.Message, "connectex") {
			t.Fatalf("raw network details leaked in message: %s", res.Payload.Error.Message)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for PREVIEW_OPEN_RESULT")
	}
}

func TestHandler_PreviewClose_StaleGeneration_Rejected(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port

	gateway := mock.NewPreviewGateway()
	defer gateway.Close()

	mockProv := &provider.MockProvider{}
	sender := wss.SendMessageFunc(func(ctx context.Context, msg interface{}) error {
		return nil
	})

	h := wss.NewHandler(mockProv, sender)
	forwarder := preview.NewDirectTCPForwarder(nil)
	mgr := preview.NewSessionManager(forwarder, nil)
	h.SetPreviewManager(mgr, preview.DataWSSClientConfig{
		EndpointURL:   gateway.URL(),
		AllowInsecure: true,
	})

	sessID := "sess-stale-gen"
	openReq := protocol.PreviewOpenMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:             protocol.MessageTypePreviewOpen,
			MessageID:        "open-gen-1",
			SentAt:           time.Now().UTC(),
			PreviewSessionID: sessID,
			LabInstanceID:    "inst-1",
			Generation:       1,
		},
		Payload: protocol.PreviewOpenPayload{
			TargetVmKey:      "vm-1",
			ProviderServerID: "srv-1",
			TargetPort:       port,
		},
	}
	openRaw, _ := json.Marshal(openReq)
	if err := h.HandleMessage(context.Background(), openRaw); err != nil {
		t.Fatalf("HandleMessage(OPEN) failed: %v", err)
	}

	// Generation mismatch: session has gen 1, but closeMsg has gen 2
	closeReq := protocol.PreviewCloseMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:             protocol.MessageTypePreviewClose,
			MessageID:        "close-gen-2",
			SentAt:           time.Now().UTC(),
			PreviewSessionID: sessID,
			LabInstanceID:    "inst-1",
			Generation:       2, // mismatch!
		},
		Payload: protocol.PreviewClosePayload{
			Reason: protocol.PreviewReasonSessionClosed,
		},
	}
	closeRaw, _ := json.Marshal(closeReq)

	err = h.HandleMessage(context.Background(), closeRaw)
	if err == nil {
		t.Fatalf("expected error for stale generation mismatch, got nil")
	}
}

func TestHandler_PreviewOpen_SequentialTunnel_NewOpenMessageID(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port

	gateway := mock.NewPreviewGateway()
	defer gateway.Close()

	mockProv := &provider.MockProvider{}
	sentMessages := make(chan interface{}, 10)
	sender := wss.SendMessageFunc(func(ctx context.Context, msg interface{}) error {
		sentMessages <- msg
		return nil
	})

	h := wss.NewHandler(mockProv, sender)
	forwarder := preview.NewDirectTCPForwarder(nil)
	mgr := preview.NewSessionManager(forwarder, nil)
	h.SetPreviewManager(mgr, preview.DataWSSClientConfig{
		EndpointURL:   gateway.URL(),
		Credential:    "test-token",
		RuntimeID:     "runtime-seq",
		DialTimeout:   2 * time.Second,
		AllowInsecure: true,
	})

	sessID := "sess-seq-test"
	openReq1 := protocol.PreviewOpenMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:             protocol.MessageTypePreviewOpen,
			MessageID:        "open-msg-1",
			SentAt:           time.Now().UTC(),
			PreviewSessionID: sessID,
			LabInstanceID:    "inst-1",
			Generation:       1,
		},
		Payload: protocol.PreviewOpenPayload{
			TargetVmKey:      "vm-1",
			ProviderServerID: "srv-1",
			TargetPort:       port,
		},
	}
	raw1, _ := json.Marshal(openReq1)

	// 1. 첫 번째 터널 오픈
	if err := h.HandleMessage(context.Background(), raw1); err != nil {
		t.Fatalf("first HandleMessage failed: %v", err)
	}

	select {
	case msg := <-sentMessages:
		res := msg.(protocol.PreviewOpenResultMessage)
		if res.Payload.Outcome != protocol.OutcomeSucceeded || res.ReplyToMessageID != "open-msg-1" {
			t.Fatalf("unexpected first outcome: %+v", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for first PREVIEW_OPEN_RESULT")
	}

	attach1, err := gateway.WaitForAttach(2 * time.Second)
	if err != nil {
		t.Fatalf("first WaitForAttach failed: %v", err)
	}
	if attach1.ReplyToMessageID != "open-msg-1" {
		t.Fatalf("expected replyTo open-msg-1, got %s", attach1.ReplyToMessageID)
	}

	// 2. 두 번째 터널 오픈 (새 openMessageId -> 새 sequential 터널)
	openReq2 := openReq1
	openReq2.MessageID = "open-msg-2"
	raw2, _ := json.Marshal(openReq2)

	if err := h.HandleMessage(context.Background(), raw2); err != nil {
		t.Fatalf("second HandleMessage failed: %v", err)
	}

	select {
	case msg := <-sentMessages:
		res := msg.(protocol.PreviewOpenResultMessage)
		if res.Payload.Outcome != protocol.OutcomeSucceeded || res.ReplyToMessageID != "open-msg-2" {
			t.Fatalf("unexpected second outcome: %+v", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for second PREVIEW_OPEN_RESULT")
	}

	attach2, err := gateway.WaitForAttach(2 * time.Second)
	if err != nil {
		t.Fatalf("second WaitForAttach failed: %v", err)
	}
	if attach2.ReplyToMessageID != "open-msg-2" {
		t.Fatalf("expected replyTo open-msg-2, got %s", attach2.ReplyToMessageID)
	}
}

func TestHandler_PreviewOpen_NilPreviewManager_FailClosed(t *testing.T) {
	mockProv := &provider.MockProvider{}
	sentMessages := make(chan interface{}, 10)
	sender := wss.SendMessageFunc(func(ctx context.Context, msg interface{}) error {
		sentMessages <- msg
		return nil
	})

	// preview manager 가 배선되지 않은 Handler (프로덕션 미지원 상태 모사)
	h := wss.NewHandler(mockProv, sender)

	openReq := protocol.PreviewOpenMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:             protocol.MessageTypePreviewOpen,
			MessageID:        "open-msg-prod-fail",
			SentAt:           time.Now().UTC(),
			PreviewSessionID: "sess-prod-fail",
			LabInstanceID:    "inst-1",
			Generation:       1,
		},
		Payload: protocol.PreviewOpenPayload{
			TargetVmKey:      "vm-1",
			ProviderServerID: "srv-1",
			TargetPort:       8080,
		},
	}
	openRaw, _ := json.Marshal(openReq)

	err := h.HandleMessage(context.Background(), openRaw)
	if err == nil {
		t.Fatalf("expected error when preview manager is nil")
	}

	select {
	case msg := <-sentMessages:
		res, ok := msg.(protocol.PreviewOpenResultMessage)
		if !ok {
			t.Fatalf("expected PreviewOpenResultMessage, got %T", msg)
		}
		if res.Payload.Outcome != protocol.OutcomeFailed {
			t.Fatalf("expected outcome FAILED, got %s", res.Payload.Outcome)
		}
		if res.Payload.Error == nil || res.Payload.Error.Code != protocol.PreviewErrorCodeUnavailable {
			t.Fatalf("expected error code UNAVAILABLE, got %+v", res.Payload.Error)
		}
		if res.Payload.Error.Message != "preview transport unavailable" {
			t.Fatalf("expected message 'preview transport unavailable', got %q", res.Payload.Error.Message)
		}
		if res.ReplyToMessageID != "open-msg-prod-fail" {
			t.Fatalf("expected replyTo open-msg-prod-fail, got %s", res.ReplyToMessageID)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for PREVIEW_OPEN_RESULT FAILED")
	}
}

type mockCredentialedSender struct {
	dialCredential string
	sentMessages   chan interface{}
}

func (s *mockCredentialedSender) SendMessage(ctx context.Context, msg interface{}) error {
	if s.sentMessages != nil {
		s.sentMessages <- msg
	}
	return nil
}

func (s *mockCredentialedSender) DialCredential() string {
	return s.dialCredential
}

// TestHandler_PreviewDataCredential_SyncWithControlReconnect 은
// Control WSS 재연결 시 Preview Data WSS 가 새 세션의 dial credential 로 동기화되는지 검증합니다 (Blocker 1).
func TestHandler_PreviewDataCredential_SyncWithControlReconnect(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	// Gateway: rotated 토큰(token-rotated-B)만 200 수락하고 이전 토큰(token-stale-A)은 401 거절
	gateway := mock.NewPreviewGateway()
	defer gateway.Close()

	gateway.SetAuthValidator(func(req *http.Request) int {
		auth := req.Header.Get("Authorization")
		if auth == "Bearer token-rotated-B" {
			return http.StatusOK
		}
		return http.StatusUnauthorized
	})

	mockProv := &provider.MockProvider{}
	sentMessages := make(chan interface{}, 10)
	sender := &mockCredentialedSender{
		dialCredential: "token-rotated-B",
		sentMessages:   sentMessages,
	}

	h := wss.NewHandler(mockProv, sender)

	forwarder := preview.NewDirectTCPForwarder(nil)
	mgr := preview.NewSessionManager(forwarder, nil)

	// 초기 설정에는 오래된 token-A 가 설정되어 있다고 가정
	h.SetPreviewManager(mgr, preview.DataWSSClientConfig{
		EndpointURL:   gateway.URL(),
		Credential:    "token-stale-A",
		RuntimeID:     "runtime-test-cred-sync",
		DialTimeout:   2 * time.Second,
		AllowInsecure: true,
	})

	sessID := "preview-cred-sync-sess-1"
	openReq := protocol.PreviewOpenMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:             protocol.MessageTypePreviewOpen,
			MessageID:        "open-msg-cred-sync",
			SentAt:           time.Now().UTC(),
			PreviewSessionID: sessID,
			LabInstanceID:    "inst-1",
			Generation:       1,
		},
		Payload: protocol.PreviewOpenPayload{
			TargetVmKey:      "vm-web-1",
			ProviderServerID: "srv-uuid-1",
			TargetPort:       port,
		},
	}
	openRaw, _ := json.Marshal(openReq)

	// HandleMessage 실행 시, ActiveDialCredential 에 의해 sender 의 token-rotated-B 로 다이얼되어 성공해야 함
	if err := h.HandleMessage(context.Background(), openRaw); err != nil {
		t.Fatalf("HandleMessage(PREVIEW_OPEN) failed: %v", err)
	}

	select {
	case msg := <-sentMessages:
		res, ok := msg.(protocol.PreviewOpenResultMessage)
		if !ok {
			t.Fatalf("expected PreviewOpenResultMessage, got %T", msg)
		}
		if res.Payload.Outcome != protocol.OutcomeSucceeded {
			t.Fatalf("expected outcome SUCCEEDED, got %s (err: %+v)", res.Payload.Outcome, res.Payload.Error)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for PREVIEW_OPEN_RESULT SUCCEEDED")
	}
}

// TestHandler_PreviewOpen_InitialAttachFailure_CleansUpLogicalSession 은
// 초기 PREVIEW_OPEN 에서 attach 실패 시 logical session 이 manager 에서 즉시 정리되는지 검증합니다 (Blocker 2).
func TestHandler_PreviewOpen_InitialAttachFailure_CleansUpLogicalSession(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	// Gateway: 인증 실패 401 반환
	gateway := mock.NewPreviewGateway()
	defer gateway.Close()
	gateway.SetAuthValidator(func(req *http.Request) int {
		return http.StatusUnauthorized
	})

	mockProv := &provider.MockProvider{}
	sentMessages := make(chan interface{}, 10)
	sender := wss.SendMessageFunc(func(ctx context.Context, msg interface{}) error {
		sentMessages <- msg
		return nil
	})

	h := wss.NewHandler(mockProv, sender)
	forwarder := preview.NewDirectTCPForwarder(nil)
	mgr := preview.NewSessionManager(forwarder, nil)

	h.SetPreviewManager(mgr, preview.DataWSSClientConfig{
		EndpointURL:   gateway.URL(),
		Credential:    "invalid-token",
		RuntimeID:     "runtime-test-cleanup",
		DialTimeout:   2 * time.Second,
		AllowInsecure: true,
	})

	sessID := "preview-fail-sess-cleanup"
	openReq := protocol.PreviewOpenMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:             protocol.MessageTypePreviewOpen,
			MessageID:        "open-msg-fail-cleanup",
			SentAt:           time.Now().UTC(),
			PreviewSessionID: sessID,
			LabInstanceID:    "inst-1",
			Generation:       1,
		},
		Payload: protocol.PreviewOpenPayload{
			TargetVmKey:      "vm-web-1",
			ProviderServerID: "srv-uuid-1",
			TargetPort:       port,
		},
	}
	openRaw, _ := json.Marshal(openReq)

	err = h.HandleMessage(context.Background(), openRaw)
	if err == nil {
		t.Fatalf("expected error from HandleMessage on attach failure")
	}

	// 초기 생성에서 attach 실패 시 logical session 은 manager 에서 즉시 제거되어야 함 (Blocker 2)
	if mgr.Count() != 0 {
		t.Fatalf("expected manager count 0 after initial attach failure, got %d", mgr.Count())
	}
	if _, exists := mgr.GetSession(sessID); exists {
		t.Fatalf("expected session removed from manager on initial attach failure")
	}

	select {
	case msg := <-sentMessages:
		res, ok := msg.(protocol.PreviewOpenResultMessage)
		if !ok {
			t.Fatalf("expected PreviewOpenResultMessage, got %T", msg)
		}
		if res.Payload.Outcome != protocol.OutcomeFailed {
			t.Fatalf("expected outcome FAILED, got %s", res.Payload.Outcome)
		}
		if res.Payload.Error == nil || res.Payload.Error.Code != protocol.PreviewErrorCodeUnavailable {
			t.Fatalf("expected error code UNAVAILABLE, got %+v", res.Payload.Error)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for PREVIEW_OPEN_RESULT FAILED")
	}
}

// TestHandler_PreviewOpen_AttachTimeout_ReturnsUnavailableErrorCode 은
// attach timeout 시 에러 코드가 UNAVAILABLE 로 매핑되고 세션이 정리되는지 검증합니다.
func TestHandler_PreviewOpen_AttachTimeout_ReturnsUnavailableErrorCode(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	// Gateway: ATTACH 응답을 주지 않고 타임아웃 유발
	gateway := mock.NewPreviewGateway()
	defer gateway.Close()
	gateway.SetSimulateAttachTimeout(true)

	mockProv := &provider.MockProvider{}
	sentMessages := make(chan interface{}, 10)
	sender := wss.SendMessageFunc(func(ctx context.Context, msg interface{}) error {
		sentMessages <- msg
		return nil
	})

	h := wss.NewHandler(mockProv, sender)
	forwarder := preview.NewDirectTCPForwarder(nil)
	mgr := preview.NewSessionManager(forwarder, nil)

	h.SetPreviewManager(mgr, preview.DataWSSClientConfig{
		EndpointURL:   gateway.URL(),
		Credential:    "valid-token",
		RuntimeID:     "runtime-test-timeout",
		DialTimeout:   500 * time.Millisecond,
		AttachTimeout: 200 * time.Millisecond,
		AllowInsecure: true,
	})

	sessID := "preview-timeout-sess"
	openReq := protocol.PreviewOpenMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:             protocol.MessageTypePreviewOpen,
			MessageID:        "open-msg-timeout",
			SentAt:           time.Now().UTC(),
			PreviewSessionID: sessID,
			LabInstanceID:    "inst-1",
			Generation:       1,
		},
		Payload: protocol.PreviewOpenPayload{
			TargetVmKey:      "vm-web-1",
			ProviderServerID: "srv-uuid-1",
			TargetPort:       port,
		},
	}
	openRaw, _ := json.Marshal(openReq)

	err = h.HandleMessage(context.Background(), openRaw)
	if err == nil {
		t.Fatalf("expected error from HandleMessage on attach timeout")
	}

	// 세션 정리 검증
	if mgr.Count() != 0 {
		t.Fatalf("expected manager count 0 after initial attach timeout, got %d", mgr.Count())
	}

	select {
	case msg := <-sentMessages:
		res, ok := msg.(protocol.PreviewOpenResultMessage)
		if !ok {
			t.Fatalf("expected PreviewOpenResultMessage, got %T", msg)
		}
		if res.Payload.Outcome != protocol.OutcomeFailed {
			t.Fatalf("expected outcome FAILED, got %s", res.Payload.Outcome)
		}
		// ErrAttachTimeout 은 VM_UNREACHABLE 이 아니라 UNAVAILABLE 이어야 함
		if res.Payload.Error == nil || res.Payload.Error.Code != protocol.PreviewErrorCodeUnavailable {
			t.Fatalf("expected error code UNAVAILABLE, got %+v", res.Payload.Error)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for PREVIEW_OPEN_RESULT FAILED")
	}
}

// TestHandler_ControlTrustTermination_4001_CleansUpIdlePreviewSessions 은
// Data WSS 가 정상(1000) 종료되어 logical PreviewSession 만 남은 상태에서
// Control WSS 가 4001(Credential Revoke)로 종료될 때,
// logical PreviewSession 이 Connector 메모리(SessionManager)에서 완전히 제거되는지 검증합니다.
func TestHandler_ControlTrustTermination_4001_CleansUpIdlePreviewSessions(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	gateway := mock.NewPreviewGateway()
	defer gateway.Close()

	mockProv := &provider.MockProvider{}
	sentMessages := make(chan interface{}, 10)
	sender := wss.SendMessageFunc(func(ctx context.Context, msg interface{}) error {
		sentMessages <- msg
		return nil
	})

	h := wss.NewHandler(mockProv, sender)
	forwarder := preview.NewDirectTCPForwarder(nil)
	mgr := preview.NewSessionManager(forwarder, nil)

	h.SetPreviewManager(mgr, preview.DataWSSClientConfig{
		EndpointURL:   gateway.URL(),
		Credential:    "valid-token",
		RuntimeID:     "runtime-test-4001",
		AllowInsecure: true,
	})

	sessID := "preview-idle-sess-4001"
	openReq := protocol.PreviewOpenMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:             protocol.MessageTypePreviewOpen,
			MessageID:        "open-msg-4001",
			SentAt:           time.Now().UTC(),
			PreviewSessionID: sessID,
			LabInstanceID:    "inst-1",
			Generation:       1,
		},
		Payload: protocol.PreviewOpenPayload{
			TargetVmKey:      "vm-web-1",
			ProviderServerID: "srv-uuid-1",
			TargetPort:       port,
		},
	}
	openRaw, _ := json.Marshal(openReq)

	if err := h.HandleMessage(context.Background(), openRaw); err != nil {
		t.Fatalf("HandleMessage(PREVIEW_OPEN) failed: %v", err)
	}

	// Gateway 측 ATTACH 수신 대기
	if _, err := gateway.WaitForAttach(2 * time.Second); err != nil {
		t.Fatalf("WaitForAttach failed: %v", err)
	}

	if mgr.Count() != 1 {
		t.Fatalf("expected 1 session in manager, got %d", mgr.Count())
	}

	// 1. Data WSS 가 정상 1000 으로 종료 (upstream 작업 완료)
	if err := gateway.SendClose(1000, "normal closure"); err != nil {
		t.Fatalf("SendClose failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Data WSS 1000 정상 종료 후에는 순차 터널 계약에 따라 logical session 이 유지되어야 함
	if mgr.Count() != 1 {
		t.Fatalf("expected logical session preserved on 1000 close, got %d", mgr.Count())
	}
	sess, exists := mgr.GetSession(sessID)
	if !exists || sess.IsDestroyed() {
		t.Fatalf("expected session alive in manager")
	}

	// 2. Control WSS 가 4001 (Credential Revoke)로 명시적 trust termination
	h.OnControlDisconnected(&websocket.CloseError{
		Code: 4001,
		Text: "credential revoked",
	})

	// 3. Manager 에서 모든 logical session 이 즉시 정리되어야 함
	if mgr.Count() != 0 {
		t.Fatalf("expected manager count 0 after Control 4001, got %d", mgr.Count())
	}
	if _, exists := mgr.GetSession(sessID); exists {
		t.Fatalf("expected session removed from manager after Control 4001")
	}
	if !sess.IsDestroyed() {
		t.Fatalf("expected session destroyed after Control 4001")
	}
}

// TestHandler_ControlTrustTermination_4002_CleansUpIdlePreviewSessions 은
// Data WSS 가 정상(1000) 종료된 후 Control WSS 가 4002(Control Session Replaced)로 종료될 때
// logical PreviewSession 이 Connector 메모리에서 완전히 제거되는지 검증합니다.
func TestHandler_ControlTrustTermination_4002_CleansUpIdlePreviewSessions(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	gateway := mock.NewPreviewGateway()
	defer gateway.Close()

	mockProv := &provider.MockProvider{}
	sentMessages := make(chan interface{}, 10)
	sender := wss.SendMessageFunc(func(ctx context.Context, msg interface{}) error {
		sentMessages <- msg
		return nil
	})

	h := wss.NewHandler(mockProv, sender)
	forwarder := preview.NewDirectTCPForwarder(nil)
	mgr := preview.NewSessionManager(forwarder, nil)

	h.SetPreviewManager(mgr, preview.DataWSSClientConfig{
		EndpointURL:   gateway.URL(),
		Credential:    "valid-token",
		RuntimeID:     "runtime-test-4002",
		AllowInsecure: true,
	})

	sessID := "preview-idle-sess-4002"
	openReq := protocol.PreviewOpenMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:             protocol.MessageTypePreviewOpen,
			MessageID:        "open-msg-4002",
			SentAt:           time.Now().UTC(),
			PreviewSessionID: sessID,
			LabInstanceID:    "inst-1",
			Generation:       1,
		},
		Payload: protocol.PreviewOpenPayload{
			TargetVmKey:      "vm-web-1",
			ProviderServerID: "srv-uuid-1",
			TargetPort:       port,
		},
	}
	openRaw, _ := json.Marshal(openReq)

	if err := h.HandleMessage(context.Background(), openRaw); err != nil {
		t.Fatalf("HandleMessage(PREVIEW_OPEN) failed: %v", err)
	}

	if _, err := gateway.WaitForAttach(2 * time.Second); err != nil {
		t.Fatalf("WaitForAttach failed: %v", err)
	}

	// 1. Data WSS 가 정상 1000 으로 종료
	if err := gateway.SendClose(1000, "normal closure"); err != nil {
		t.Fatalf("SendClose failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if mgr.Count() != 1 {
		t.Fatalf("expected logical session preserved on 1000 close, got %d", mgr.Count())
	}

	// 2. Control WSS 가 4002 (Control Session Replaced)로 명시적 trust termination
	h.OnControlDisconnected(&websocket.CloseError{
		Code: 4002,
		Text: "replaced by a newer connection",
	})

	// 3. Manager 에서 logical session 이 즉시 정리되어야 함
	if mgr.Count() != 0 {
		t.Fatalf("expected manager count 0 after Control 4002, got %d", mgr.Count())
	}
	if _, exists := mgr.GetSession(sessID); exists {
		t.Fatalf("expected session removed from manager after Control 4002")
	}
}

// TestHandler_ControlNormalDisconnect_PreservesIdlePreviewSessions 은
// Data WSS 가 정상(1000) 종료된 후 Control WSS 가 일반 네트워크 단절(1006 / EOF)을 겪더라도
// logical PreviewSession 이 임의로 폐기되지 않고 유지되는지 대조 검증합니다.
func TestHandler_ControlNormalDisconnect_PreservesIdlePreviewSessions(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	gateway := mock.NewPreviewGateway()
	defer gateway.Close()

	mockProv := &provider.MockProvider{}
	sentMessages := make(chan interface{}, 10)
	sender := wss.SendMessageFunc(func(ctx context.Context, msg interface{}) error {
		sentMessages <- msg
		return nil
	})

	h := wss.NewHandler(mockProv, sender)
	forwarder := preview.NewDirectTCPForwarder(nil)
	mgr := preview.NewSessionManager(forwarder, nil)

	h.SetPreviewManager(mgr, preview.DataWSSClientConfig{
		EndpointURL:   gateway.URL(),
		Credential:    "valid-token",
		RuntimeID:     "runtime-test-normal-disc",
		AllowInsecure: true,
	})

	sessID := "preview-idle-sess-normal-disc"
	openReq := protocol.PreviewOpenMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:             protocol.MessageTypePreviewOpen,
			MessageID:        "open-msg-disc",
			SentAt:           time.Now().UTC(),
			PreviewSessionID: sessID,
			LabInstanceID:    "inst-1",
			Generation:       1,
		},
		Payload: protocol.PreviewOpenPayload{
			TargetVmKey:      "vm-web-1",
			ProviderServerID: "srv-uuid-1",
			TargetPort:       port,
		},
	}
	openRaw, _ := json.Marshal(openReq)

	if err := h.HandleMessage(context.Background(), openRaw); err != nil {
		t.Fatalf("HandleMessage(PREVIEW_OPEN) failed: %v", err)
	}

	if _, err := gateway.WaitForAttach(2 * time.Second); err != nil {
		t.Fatalf("WaitForAttach failed: %v", err)
	}

	// 1. Data WSS 가 정상 1000 으로 종료
	if err := gateway.SendClose(1000, "normal closure"); err != nil {
		t.Fatalf("SendClose failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if mgr.Count() != 1 {
		t.Fatalf("expected logical session preserved on 1000 close, got %d", mgr.Count())
	}

	// 2. Control WSS 가 일반 네트워크 단절 (CloseAbnormalClosure 1006)
	h.OnControlDisconnected(&websocket.CloseError{
		Code: websocket.CloseAbnormalClosure,
		Text: "unexpected EOF",
	})

	// 3. 일반 단절에서는 logical session 이 폐기되지 않고 유지되어야 함
	if mgr.Count() != 1 {
		t.Fatalf("expected manager count 1 preserved after normal Control disconnect, got %d", mgr.Count())
	}
	sess, exists := mgr.GetSession(sessID)
	if !exists || sess.IsDestroyed() {
		t.Fatalf("expected session preserved and not destroyed after normal Control disconnect")
	}
}
