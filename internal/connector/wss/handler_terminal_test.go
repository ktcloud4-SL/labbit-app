package wss_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ktcloud4-SL/rabbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/rabbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/rabbit-app/internal/connector/terminal"
	"github.com/ktcloud4-SL/rabbit-app/internal/connector/wss"
)

func TestHandler_TerminalOpenAndClose(t *testing.T) {
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
	}, terminal.DataWSSClientConfig{})

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

	// 2. TERMINAL_OPEN_RESULT 회신 검증
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
	case <-time.After(1 * time.Second):
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
	case <-time.After(1 * time.Second):
		t.Fatalf("timeout waiting for TERMINAL_ENDED")
	}

	// 6. 세션 종료 확인
	if session.Status != terminal.StatusClosed {
		t.Fatalf("expected session status CLOSED, got %s", session.Status)
	}
}
