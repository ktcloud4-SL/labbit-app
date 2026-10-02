package wss_test

import (
	"context"
	"encoding/json"
	"net"
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
			Port:             port,
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
	if session.Port != port {
		t.Fatalf("expected port %d, got %d", port, session.Port)
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

	// 7. PREVIEW_ENDED 전파 검증
	select {
	case msg := <-sentMessages:
		ended, ok := msg.(protocol.PreviewEndedMessage)
		if !ok {
			t.Fatalf("expected PreviewEndedMessage, got %T", msg)
		}
		if ended.PreviewSessionID != sessID {
			t.Fatalf("expected session ID %s, got %s", sessID, ended.PreviewSessionID)
		}
		if ended.Payload.Reason != protocol.PreviewReasonSessionClosed {
			t.Fatalf("expected reason %s, got %s", protocol.PreviewReasonSessionClosed, ended.Payload.Reason)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for PREVIEW_ENDED")
	}

	if session.GetStatus() != preview.StatusClosed {
		t.Fatalf("expected session status CLOSED, got %s", session.GetStatus())
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
			Port:             port,
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
			Port:             0, // invalid port!
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
			Port:             port,
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
