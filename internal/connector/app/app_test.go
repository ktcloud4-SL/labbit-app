package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/terminal"
)

type recordSender struct {
	sent []any
}

func (r *recordSender) SendMessage(ctx context.Context, msg any) error {
	r.sent = append(r.sent, msg)
	return nil
}

func TestBuildConnector_ProductionTerminalWiring(t *testing.T) {
	sender := &recordSender{}
	mockProv := &provider.MockProvider{}

	connectorApp, err := BuildConnector(mockProv, sender)
	if err != nil {
		t.Fatalf("BuildConnector failed: %v", err)
	}

	if connectorApp.Handler == nil {
		t.Fatal("expected Handler to be initialized, got nil")
	}
	if connectorApp.TerminalManager == nil {
		t.Fatal("expected TerminalManager to be initialized, got nil")
	}
	if connectorApp.PTYFactory == nil {
		t.Fatal("expected PTYFactory to be initialized, got nil")
	}

	// TERMINAL_OPEN 요청을 Handler로 전달했을 때
	// "terminal manager or PTY factory not configured" 오류가 발생하지 않음을 검증
	openMsg := protocol.TerminalOpenMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:              protocol.MessageTypeTerminalOpen,
			MessageID:         "msg-open-prod-wiring",
			SentAt:            time.Now().UTC(),
			TerminalSessionID: "sess-prod-wiring",
			LabInstanceID:     "inst-prod-wiring",
			Generation:        1,
		},
		Payload: protocol.TerminalOpenPayload{
			TargetVmKey:      "vm-01",
			ProviderServerID: "srv-01",
			Cols:             80,
			Rows:             24,
		},
	}
	raw, err := json.Marshal(openMsg)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	// handleMessage 실행 (dial은 실패하더라도 manager/factory 미설정 에러가 아님을 확인)
	_ = connectorApp.Handler.HandleMessage(context.Background(), raw)

	if len(sender.sent) == 0 {
		t.Fatal("expected response message to be sent")
	}

	res, ok := sender.sent[0].(protocol.TerminalOpenResultMessage)
	if !ok {
		t.Fatalf("expected TerminalOpenResultMessage, got %T", sender.sent[0])
	}

	if res.Payload.Error != nil && res.Payload.Error.Message == "terminal manager or PTY factory not configured" {
		t.Fatalf("production handler failed with unconfigured terminal manager or PTY factory")
	}
}

func TestConnectorApp_GracefulShutdown(t *testing.T) {
	sender := &recordSender{}
	mockProv := &provider.MockProvider{}

	connectorApp, err := BuildConnector(mockProv, sender)
	if err != nil {
		t.Fatalf("BuildConnector failed: %v", err)
	}

	// 세션 하나 수동 등록
	session, _, err := connectorApp.TerminalManager.GetOrCreateSession(
		protocol.TerminalOpenPayload{
			TargetVmKey:      "vm-shutdown",
			ProviderServerID: "srv-shutdown",
			Cols:             80,
			Rows:             24,
		},
		protocol.BaseEnvelope{
			TerminalSessionID: "sess-shutdown",
			LabInstanceID:     "inst-shutdown",
			Generation:        1,
		},
		func() (terminal.PTYChannel, error) {
			return terminal.NewBufferPTY(80, 24), nil
		},
	)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	if session.Status == terminal.StatusClosed {
		t.Fatal("expected session to be created/active")
	}

	// CloseAll 호출 (Connector 종료 시의 동작)
	connectorApp.TerminalManager.CloseAll("SERVICE_RESTARTING")

	if session.Status != terminal.StatusClosed {
		t.Fatalf("expected session to be CLOSED after shutdown, got: %s", session.Status)
	}
	if connectorApp.TerminalManager.ActiveCount() != 0 {
		t.Fatalf("expected active count 0, got %d", connectorApp.TerminalManager.ActiveCount())
	}
}
