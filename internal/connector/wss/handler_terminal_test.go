package wss_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/terminal"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
)

func TestHandler_TerminalOpenAndClose(t *testing.T) {
	relay := mock.NewTerminalRelay()
	defer relay.Close()

	mockProv := &provider.MockProvider{}
	sentMessages := make(chan interface{}, 10)
	sender := wss.SendMessageFunc(func(ctx context.Context, msg interface{}) error {
		sentMessages <- msg
		return nil
	})

	h := wss.NewHandler(mockProv, sender)

	mgr := terminal.NewSessionManager(5*time.Second, nil)
	h.SetTerminalManager(mgr, func(targetVmKey, serverId string, cols, rows int) (terminal.PTYChannel, error) {
		return terminal.NewMockEchoPTY(cols, rows), nil
	}, terminal.DataWSSClientConfig{
		EndpointURL:   relay.URL(),
		RuntimeID:     "runtime-test-handler",
		DialTimeout:   2 * time.Second,
		AllowInsecure: true,
	})

	// 1. TERMINAL_OPEN 메시지 전송
	openReq := protocol.TerminalOpenMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:              protocol.MessageTypeTerminalOpen,
			MessageID:         "open-msg-1",
			SentAt:            time.Now().UTC(),
			TerminalSessionID: "sess-control-1",
			LabInstanceID:     "inst-1",
			Generation:        1,
		},
		Payload: protocol.TerminalOpenPayload{
			TargetVmKey:      "vm-web-1",
			ProviderServerID: "srv-uuid-1",
			Cols:             80,
			Rows:             24,
		},
	}
	openRaw, _ := json.Marshal(openReq)

	if err := h.HandleMessage(context.Background(), openRaw); err != nil {
		t.Fatalf("HandleMessage(TERMINAL_OPEN) failed: %v", err)
	}

	// 2. TERMINAL_OPEN_RESULT (SUCCEEDED) 회신 검증 (동기 attach 완료 후 회신)
	select {
	case msg := <-sentMessages:
		res, ok := msg.(protocol.TerminalOpenResultMessage)
		if !ok {
			t.Fatalf("expected TerminalOpenResultMessage, got %T", msg)
		}
		if res.Payload.Outcome != protocol.OutcomeSucceeded {
			t.Fatalf("expected outcome SUCCEEDED, got %s", res.Payload.Outcome)
		}
		if res.ReplyToMessageID != "open-msg-1" {
			t.Fatalf("expected replyTo open-msg-1, got %s", res.ReplyToMessageID)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for TERMINAL_OPEN_RESULT")
	}

	// 3. 세션 등록 확인
	session, exists := mgr.GetSession("sess-control-1")
	if !exists {
		t.Fatalf("expected session to be registered in manager")
	}
	if session.Cols != 80 || session.Rows != 24 {
		t.Fatalf("expected 80x24, got %dx%d", session.Cols, session.Rows)
	}

	// 4. TERMINAL_CLOSE 메시지 전송
	closeReq := protocol.TerminalCloseMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:              protocol.MessageTypeTerminalClose,
			MessageID:         "close-msg-1",
			SentAt:            time.Now().UTC(),
			TerminalSessionID: "sess-control-1",
			LabInstanceID:     "inst-1",
			Generation:        1,
		},
		Payload: protocol.TerminalClosePayload{
			Reason: protocol.TerminalReasonSessionClosed,
		},
	}
	closeRaw, _ := json.Marshal(closeReq)

	if err := h.HandleMessage(context.Background(), closeRaw); err != nil {
		t.Fatalf("HandleMessage(TERMINAL_CLOSE) failed: %v", err)
	}

	// 5. TERMINAL_ENDED 회신 검증
	select {
	case msg := <-sentMessages:
		ended, ok := msg.(protocol.TerminalEndedMessage)
		if !ok {
			t.Fatalf("expected TerminalEndedMessage, got %T", msg)
		}
		if ended.Payload.Reason != protocol.TerminalReasonSessionClosed {
			t.Fatalf("expected reason SESSION_CLOSED, got %s", ended.Payload.Reason)
		}
		if ended.TerminalSessionID != "sess-control-1" {
			t.Fatalf("expected session sess-control-1, got %s", ended.TerminalSessionID)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for TERMINAL_ENDED")
	}

	// 6. 세션 종료 확인
	if session.Status != terminal.StatusClosed {
		t.Fatalf("expected session status CLOSED, got %s", session.Status)
	}
}

func TestHandler_TerminalOpen_MissingEndpoint_Fails(t *testing.T) {
	mockProv := &provider.MockProvider{}
	sentMessages := make(chan interface{}, 10)
	sender := wss.SendMessageFunc(func(ctx context.Context, msg interface{}) error {
		sentMessages <- msg
		return nil
	})

	h := wss.NewHandler(mockProv, sender)
	mgr := terminal.NewSessionManager(5*time.Second, nil)
	h.SetTerminalManager(mgr, func(targetVmKey, serverId string, cols, rows int) (terminal.PTYChannel, error) {
		return terminal.NewMockEchoPTY(cols, rows), nil
	}, terminal.DataWSSClientConfig{EndpointURL: ""}) // empty endpoint

	openReq := protocol.TerminalOpenMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:              protocol.MessageTypeTerminalOpen,
			MessageID:         "open-empty-ep",
			SentAt:            time.Now().UTC(),
			TerminalSessionID: "sess-empty-ep",
			LabInstanceID:     "inst-1",
			Generation:        1,
		},
		Payload: protocol.TerminalOpenPayload{
			TargetVmKey:      "vm-1",
			ProviderServerID: "srv-1",
			Cols:             80,
			Rows:             24,
		},
	}
	raw, _ := json.Marshal(openReq)
	_ = h.HandleMessage(context.Background(), raw)

	select {
	case msg := <-sentMessages:
		res, ok := msg.(protocol.TerminalOpenResultMessage)
		if !ok {
			t.Fatalf("expected TerminalOpenResultMessage, got %T", msg)
		}
		if res.Payload.Outcome != protocol.OutcomeFailed {
			t.Fatalf("expected Outcome FAILED for empty endpoint, got %s", res.Payload.Outcome)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timeout waiting for response")
	}
}

func TestHandler_TerminalOpen_AttachFailure_CleansUpSession(t *testing.T) {
	mockProv := &provider.MockProvider{}
	sentMessages := make(chan interface{}, 10)
	sender := wss.SendMessageFunc(func(ctx context.Context, msg interface{}) error {
		sentMessages <- msg
		return nil
	})

	h := wss.NewHandler(mockProv, sender)
	mgr := terminal.NewSessionManager(5*time.Second, nil)
	h.SetTerminalManager(mgr, func(targetVmKey, serverId string, cols, rows int) (terminal.PTYChannel, error) {
		return terminal.NewMockEchoPTY(cols, rows), nil
	}, terminal.DataWSSClientConfig{
		EndpointURL: "ws://127.0.0.1:59999/unreachable", // unreachable
		DialTimeout: 200 * time.Millisecond,
	})

	openReq := protocol.TerminalOpenMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:              protocol.MessageTypeTerminalOpen,
			MessageID:         "open-fail-attach",
			SentAt:            time.Now().UTC(),
			TerminalSessionID: "sess-fail-attach",
			LabInstanceID:     "inst-1",
			Generation:        1,
		},
		Payload: protocol.TerminalOpenPayload{
			TargetVmKey:      "vm-1",
			ProviderServerID: "srv-1",
			Cols:             80,
			Rows:             24,
		},
	}
	raw, _ := json.Marshal(openReq)
	_ = h.HandleMessage(context.Background(), raw)

	// FAILED 응답 수신 검증
	select {
	case msg := <-sentMessages:
		res, ok := msg.(protocol.TerminalOpenResultMessage)
		if !ok {
			t.Fatalf("expected TerminalOpenResultMessage, got %T", msg)
		}
		if res.Payload.Outcome != protocol.OutcomeFailed {
			t.Fatalf("expected Outcome FAILED, got %s", res.Payload.Outcome)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for FAILED response")
	}

	// 실패 시 세션이 매니저에 남아있지 않음을 검증 (Reviewer 3번 지적 사항)
	_, exists := mgr.GetSession("sess-fail-attach")
	if exists {
		t.Fatalf("expected session to be cleaned up from manager after attach failure")
	}
}

func TestHandler_TerminalOpen_InvalidEnvelopeOrPayload_Fails(t *testing.T) {
	mockProv := &provider.MockProvider{}
	sentMessages := make(chan interface{}, 10)
	sender := wss.SendMessageFunc(func(ctx context.Context, msg interface{}) error {
		sentMessages <- msg
		return nil
	})

	h := wss.NewHandler(mockProv, sender)
	mgr := terminal.NewSessionManager(5*time.Second, nil)
	ptyCalled := false
	h.SetTerminalManager(mgr, func(targetVmKey, serverId string, cols, rows int) (terminal.PTYChannel, error) {
		ptyCalled = true
		return terminal.NewMockEchoPTY(cols, rows), nil
	}, terminal.DataWSSClientConfig{EndpointURL: "ws://localhost:8443"})

	// Generation < 1 누락 Envelope
	openReq := protocol.TerminalOpenMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:              protocol.MessageTypeTerminalOpen,
			MessageID:         "open-invalid-gen",
			SentAt:            time.Now().UTC(),
			TerminalSessionID: "sess-invalid-gen",
			LabInstanceID:     "inst-1",
			Generation:        0, // invalid
		},
		Payload: protocol.TerminalOpenPayload{
			TargetVmKey:      "vm-1",
			ProviderServerID: "srv-1",
			Cols:             80,
			Rows:             24,
		},
	}
	raw, _ := json.Marshal(openReq)
	_ = h.HandleMessage(context.Background(), raw)

	if ptyCalled {
		t.Fatalf("ptyFactory should not be invoked on invalid envelope")
	}

	select {
	case msg := <-sentMessages:
		res, ok := msg.(protocol.TerminalOpenResultMessage)
		if !ok {
			t.Fatalf("expected TerminalOpenResultMessage, got %T", msg)
		}
		if res.Payload.Outcome != protocol.OutcomeFailed {
			t.Fatalf("expected Outcome FAILED, got %s", res.Payload.Outcome)
		}
		if res.Payload.Error.Code != protocol.TerminalErrInvalidSession {
			t.Fatalf("expected code INVALID_SESSION, got %s", res.Payload.Error.Code)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timeout waiting for response")
	}
}

func TestHandler_TerminalClose_StaleGeneration_Rejected(t *testing.T) {
	relay := mock.NewTerminalRelay()
	defer relay.Close()

	mockProv := &provider.MockProvider{}
	sentMessages := make(chan interface{}, 10)
	sender := wss.SendMessageFunc(func(ctx context.Context, msg interface{}) error {
		sentMessages <- msg
		return nil
	})

	h := wss.NewHandler(mockProv, sender)
	mgr := terminal.NewSessionManager(5*time.Second, nil)
	h.SetTerminalManager(mgr, func(targetVmKey, serverId string, cols, rows int) (terminal.PTYChannel, error) {
		return terminal.NewMockEchoPTY(cols, rows), nil
	}, terminal.DataWSSClientConfig{
		EndpointURL:   relay.URL(),
		RuntimeID:     "runtime-stale-gen",
		DialTimeout:   2 * time.Second,
		AllowInsecure: true,
	})

	// 1. Generation = 2 세션 생성 (TERMINAL_OPEN)
	openReq := protocol.TerminalOpenMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:              protocol.MessageTypeTerminalOpen,
			MessageID:         "open-gen-2",
			SentAt:            time.Now().UTC(),
			TerminalSessionID: "sess-stale-gen-1",
			LabInstanceID:     "inst-1",
			Generation:        2,
		},
		Payload: protocol.TerminalOpenPayload{
			TargetVmKey:      "vm-web-1",
			ProviderServerID: "srv-uuid-1",
			Cols:             80,
			Rows:             24,
		},
	}
	openRaw, _ := json.Marshal(openReq)
	if err := h.HandleMessage(context.Background(), openRaw); err != nil {
		t.Fatalf("HandleMessage(TERMINAL_OPEN) failed: %v", err)
	}

	// OPEN_RESULT 수신 확인
	select {
	case <-sentMessages:
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for TERMINAL_OPEN_RESULT")
	}

	session, exists := mgr.GetSession("sess-stale-gen-1")
	if !exists || session.Status != terminal.StatusActive {
		t.Fatalf("expected session to be active, got exists=%v", exists)
	}

	// 2. Generation = 1 (stale, 1 < 2) 로 TERMINAL_CLOSE 전송
	staleCloseReq := protocol.TerminalCloseMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:              protocol.MessageTypeTerminalClose,
			MessageID:         "close-stale-1",
			SentAt:            time.Now().UTC(),
			TerminalSessionID: "sess-stale-gen-1",
			LabInstanceID:     "inst-1",
			Generation:        1, // stale generation
		},
		Payload: protocol.TerminalClosePayload{
			Reason: protocol.TerminalReasonSessionClosed,
		},
	}
	staleCloseRaw, _ := json.Marshal(staleCloseReq)

	err := h.HandleMessage(context.Background(), staleCloseRaw)
	if err == nil {
		t.Fatal("expected HandleMessage to return error on stale generation TERMINAL_CLOSE, got nil")
	}

	// 3. 세션이 닫히지 않고 여전히 Active 상태로 유지되는지 확인
	sessionAfter, existsAfter := mgr.GetSession("sess-stale-gen-1")
	if !existsAfter {
		t.Fatal("session should still exist in manager")
	}
	if sessionAfter.GetStatus() == terminal.StatusClosed {
		t.Fatal("session should NOT be closed by stale generation TERMINAL_CLOSE")
	}
}
