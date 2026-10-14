package wss

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/terminal"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
)

func TestTerminalControlRedactsRawSSHFailures(t *testing.T) {
	const raw = "private SSH credential/path and target address"
	var sent []any
	h := NewHandler(&provider.MockProvider{}, SendMessageFunc(func(_ context.Context, message any) error { sent = append(sent, message); return nil }))
	mgr := terminal.NewSessionManager(time.Minute, nil)
	h.SetTerminalManager(mgr, nil, terminal.DataWSSClientConfig{EndpointURL: "ws://localhost:1"})
	h.SetVMPTYFactory(func(context.Context, vmaccess.Target, int, int) (terminal.PTYChannel, error) {
		return nil, errors.New(raw)
	})
	request := protocol.TerminalOpenMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeTerminalOpen, MessageID: "open-1", TerminalSessionID: "session-1", LabInstanceID: "lab-1", Generation: 1}, Payload: protocol.TerminalOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server-1", Cols: 80, Rows: 24}}
	data, _ := json.Marshal(request)
	if err := h.HandleMessage(context.Background(), data); err == nil {
		t.Fatal("SSH failure was accepted")
	}
	h.sendTerminalEndedRaw("", "session-1", "lab-1", 1, protocol.TerminalReasonSSHDisconnected, nil, errors.New(raw))
	if len(sent) != 2 {
		t.Fatalf("expected failed OPEN and ENDED, got %d", len(sent))
	}
	for _, message := range sent {
		data, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), raw) {
			t.Fatal("raw SSH error reached Control wire")
		}
	}
}
