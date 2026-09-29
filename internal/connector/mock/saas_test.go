package mock_test

import (
	"encoding/json"
	"fmt"
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

	errCh := make(chan error, workers*iterations*4)
	recordErr := func(err error) {
		if err != nil {
			errCh <- err
		}
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				// 1. SendCommand 동시 호출 (MockSaaS 서버 측 write)
				if err := server.SendCommand(map[string]interface{}{
					"type":      protocol.MessageTypeOperationCommand,
					"messageId": "cmd-concurrent",
					"worker":    workerID,
					"iteration": j,
				}); err != nil {
					recordErr(fmt.Errorf("server.SendCommand failed (worker %d, iter %d): %w", workerID, j, err))
				}

				// 2. SendRaw 동시 호출 (MockSaaS 서버 측 write)
				if err := server.SendRaw(map[string]interface{}{
					"type": "MOCK_RAW",
					"id":   j,
				}); err != nil {
					recordErr(fmt.Errorf("server.SendRaw failed (worker %d, iter %d): %w", workerID, j, err))
				}

				// 3. SendBytes 동시 호출 (MockSaaS 서버 측 write)
				if err := server.SendBytes([]byte(`{"type":"PING"}`)); err != nil {
					recordErr(fmt.Errorf("server.SendBytes failed (worker %d, iter %d): %w", workerID, j, err))
				}

				// 4. Client HELLO 전송을 통한 서버측 sendHelloAck 동시 유도
				hello := map[string]interface{}{
					"type":      protocol.MessageTypeHello,
					"messageId": "hello-concurrent",
				}
				b, _ := json.Marshal(hello)
				clientWriteMu.Lock()
				err := conn.WriteMessage(websocket.TextMessage, b)
				clientWriteMu.Unlock()
				if err != nil {
					recordErr(fmt.Errorf("client conn.WriteMessage failed (worker %d, iter %d): %w", workerID, j, err))
				}
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	var writeErrs []error
	for err := range errCh {
		writeErrs = append(writeErrs, err)
	}
	if len(writeErrs) > 0 {
		for _, err := range writeErrs {
			t.Errorf("concurrent write error: %v", err)
		}
		t.Fatalf("encountered %d write errors during concurrency test", len(writeErrs))
	}
}

func TestMockSaaS_ReconnectConnectionAffinity(t *testing.T) {
	token := "reconnect-test-token"
	server := mock.NewMockSaaS(token)
	defer server.Close()

	header := make(http.Header)
	header.Set("Authorization", "Bearer "+token)

	dialer := websocket.Dialer{
		Subprotocols: []string{protocol.SubprotocolControl},
	}

	connectedCh := make(chan struct{}, 2)
	server.OnConnected = func(conn *websocket.Conn) {
		connectedCh <- struct{}{}
	}

	// 1. Connection 1 연결
	conn1, _, err := dialer.Dial(server.URL(), header)
	if err != nil {
		t.Fatalf("conn1 dial failed: %v", err)
	}
	defer conn1.Close()

	// conn1 등록 완료 동기 대기
	select {
	case <-connectedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for conn1 to register on server")
	}

	// 2. Connection 2 연결 (재연결 시뮬레이션: server.conn이 conn2로 갱신됨)
	conn2, _, err := dialer.Dial(server.URL(), header)
	if err != nil {
		t.Fatalf("conn2 dial failed: %v", err)
	}
	defer conn2.Close()

	// conn2 비동기 수신 수집 goroutine
	conn2Messages := make(chan []byte, 20)
	go func() {
		for {
			_, msg, err := conn2.ReadMessage()
			if err != nil {
				return
			}
			conn2Messages <- msg
		}
	}()

	// server 측에서 conn2 등록 완료 동기 대기 (deterministic sync: time.Sleep 제거)
	select {
	case <-connectedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for conn2 to register on server")
	}

	// 3. Conn1에서 HELLO 전송 -> HELLO_ACK는 Conn1으로만 회신되어야 함
	hello1 := map[string]interface{}{
		"type":      protocol.MessageTypeHello,
		"messageId": "msg-hello-conn1",
		"sentAt":    time.Now().UTC().Format(time.RFC3339),
	}
	b1, _ := json.Marshal(hello1)
	if err := conn1.WriteMessage(websocket.TextMessage, b1); err != nil {
		t.Fatalf("conn1 write hello failed: %v", err)
	}

	// Conn1에서 HELLO_ACK 수신 확인
	_ = conn1.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, ackBytes1, err := conn1.ReadMessage()
	if err != nil {
		t.Fatalf("conn1 failed to receive hello_ack: %v", err)
	}

	var env1 protocol.BaseEnvelope
	if err := json.Unmarshal(ackBytes1, &env1); err != nil {
		t.Fatalf("unmarshal conn1 ack failed: %v", err)
	}
	if env1.Type != protocol.MessageTypeHelloAck || env1.ReplyToMessageID != "msg-hello-conn1" {
		t.Fatalf("conn1 received unexpected ack: %+v", env1)
	}

	// Conn2에서는 Conn1의 HELLO_ACK를 수신하지 않아야 함
	select {
	case msg := <-conn2Messages:
		t.Fatalf("conn2 unexpectedly received a message meant for conn1: %s", string(msg))
	case <-time.After(100 * time.Millisecond):
		// 정상: conn2로 전달되지 않음
	}

	// 4. Conn2에서도 HELLO 전송 시 자신의 connection으로 정상 수신되는지 확인
	hello2 := map[string]interface{}{
		"type":      protocol.MessageTypeHello,
		"messageId": "msg-hello-conn2",
		"sentAt":    time.Now().UTC().Format(time.RFC3339),
	}
	b2, _ := json.Marshal(hello2)
	if err := conn2.WriteMessage(websocket.TextMessage, b2); err != nil {
		t.Fatalf("conn2 write hello failed: %v", err)
	}

	select {
	case ackBytes2 := <-conn2Messages:
		var env2 protocol.BaseEnvelope
		if err := json.Unmarshal(ackBytes2, &env2); err != nil {
			t.Fatalf("unmarshal conn2 ack failed: %v", err)
		}
		if env2.Type != protocol.MessageTypeHelloAck || env2.ReplyToMessageID != "msg-hello-conn2" {
			t.Fatalf("conn2 received unexpected ack: %+v", env2)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("conn2 timed out waiting for hello_ack")
	}

	// 5. 서버 측 SendCommand 호출 시 현재 활성 connection(conn2)으로 전송되는지 확인
	cmd := map[string]interface{}{
		"type":      protocol.MessageTypeOperationCommand,
		"messageId": "cmd-test-conn2",
	}
	if err := server.SendCommand(cmd); err != nil {
		t.Fatalf("server.SendCommand failed: %v", err)
	}

	select {
	case cmdBytes := <-conn2Messages:
		var cmdEnv protocol.BaseEnvelope
		if err := json.Unmarshal(cmdBytes, &cmdEnv); err != nil {
			t.Fatalf("unmarshal command failed: %v", err)
		}
		if cmdEnv.MessageID != "cmd-test-conn2" {
			t.Fatalf("expected command cmd-test-conn2, got %s", cmdEnv.MessageID)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("conn2 timed out waiting for command")
	}
}
