package mock_test

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
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

func TestMockSaaS_ConcurrentWrites(t *testing.T) {
	token := "concurrent-test-token"
	server := mock.NewMockSaaS(token)
	defer server.Close()

	header := make(http.Header)
	header.Set("Authorization", "Bearer "+token)

	dialer := websocket.Dialer{
		Subprotocols: []string{protocol.SubprotocolControl},
	}

	conn, _, err := dialer.Dial(server.URL(), header)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	// 클라이언트 수신 drain goroutine
	stopCh := make(chan struct{})
	defer close(stopCh)
	go func() {
		for {
			select {
			case <-stopCh:
				return
			default:
				_, _, err := conn.ReadMessage()
				if err != nil {
					return
				}
			}
		}
	}()

	var clientWriteMu sync.Mutex
	var wg sync.WaitGroup
	workers := 10
	iterations := 20

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				// 1. SendCommand 동시 호출 (MockSaaS 서버 측 write)
				_ = server.SendCommand(map[string]interface{}{
					"type":      protocol.MessageTypeOperationCommand,
					"messageId": "cmd-concurrent",
					"worker":    workerID,
					"iteration": j,
				})
				// 2. SendRaw 동시 호출 (MockSaaS 서버 측 write)
				_ = server.SendRaw(map[string]interface{}{
					"type": "MOCK_RAW",
					"id":   j,
				})
				// 3. SendBytes 동시 호출 (MockSaaS 서버 측 write)
				_ = server.SendBytes([]byte(`{"type":"PING"}`))
				// 4. Client HELLO 전송을 통한 서버측 sendHelloAck 동시 유도
				hello := map[string]interface{}{
					"type":      protocol.MessageTypeHello,
					"messageId": "hello-concurrent",
				}
				b, _ := json.Marshal(hello)
				clientWriteMu.Lock()
				_ = conn.WriteMessage(websocket.TextMessage, b)
				clientWriteMu.Unlock()
			}
		}(i)
	}

	wg.Wait()
}
