package wss_test

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

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
