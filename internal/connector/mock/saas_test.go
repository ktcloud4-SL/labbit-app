package mock_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/rabbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/rabbit-app/internal/connector/protocol"
)

func TestMockSaaS_Handshake(t *testing.T) {
	token := "valid-secret-token"
	server := mock.NewMockSaaS(token)
	defer server.Close()

	header := make(http.Header)
	header.Set("Authorization", "Bearer "+token)

	dialer := websocket.Dialer{
		Subprotocols: []string{protocol.SubprotocolControl},
	}

	conn, resp, err := dialer.Dial(server.URL(), header)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("expected 101, got %d", resp.StatusCode)
	}

	// HELLO 전송
	hello := map[string]interface{}{
		"type":      protocol.MessageTypeHello,
		"messageId": "msg-hello-001",
		"sentAt":    time.Now().UTC().Format(time.RFC3339),
		"payload": map[string]interface{}{
			"connectorVersion": "v0.1.0",
			"runtimeId":        "run-test-1",
			"startedAt":        time.Now().UTC().Format(time.RFC3339),
			"capabilities":     []string{"terminal-v1"},
		},
	}
	bytes, _ := json.Marshal(hello)
	if err := conn.WriteMessage(websocket.TextMessage, bytes); err != nil {
		t.Fatalf("write hello failed: %v", err)
	}

	// HELLO_ACK 수신 대기
	_, ackBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read hello_ack failed: %v", err)
	}

	var env protocol.BaseEnvelope
	if err := json.Unmarshal(ackBytes, &env); err != nil {
		t.Fatalf("unmarshal ack failed: %v", err)
	}

	if env.Type != protocol.MessageTypeHelloAck {
		t.Errorf("expected HELLO_ACK, got %s", env.Type)
	}
	if env.ReplyToMessageID != "msg-hello-001" {
		t.Errorf("expected replyTo msg-hello-001, got %s", env.ReplyToMessageID)
	}
}
