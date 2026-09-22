package wss_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/rabbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/rabbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/rabbit-app/internal/connector/wss"
)

func TestBackoffPolicy_CalculationAndJitter(t *testing.T) {
	policy := wss.BackoffPolicy{
		InitialInterval:     1 * time.Second,
		MaxInterval:         10 * time.Second,
		Multiplier:          2.0,
		RandomizationFactor: 0.2, // ±20%
	}

	// Attempt 0: base 1s -> [0.8s, 1.2s]
	d0 := policy.NextBackoff(0)
	if d0 < 800*time.Millisecond || d0 > 1200*time.Millisecond {
		t.Fatalf("attempt 0 backoff %v out of range [0.8s, 1.2s]", d0)
	}

	// Attempt 1: base 2s -> [1.6s, 2.4s]
	d1 := policy.NextBackoff(1)
	if d1 < 1600*time.Millisecond || d1 > 2400*time.Millisecond {
		t.Fatalf("attempt 1 backoff %v out of range [1.6s, 2.4s]", d1)
	}

	// Attempt 2: base 4s -> [3.2s, 4.8s]
	d2 := policy.NextBackoff(2)
	if d2 < 3200*time.Millisecond || d2 > 4800*time.Millisecond {
		t.Fatalf("attempt 2 backoff %v out of range [3.2s, 4.8s]", d2)
	}

	// Attempt 10: base exceeds MaxInterval (10s) -> capped at <= 10s
	d10 := policy.NextBackoff(10)
	if d10 > 10*time.Second {
		t.Fatalf("attempt 10 backoff %v exceeds maxInterval 10s", d10)
	}
}

// ReconnectableMockServer 는 단절 후 재시작 가능한 웹소켓 테스트 서버입니다.
type ReconnectableMockServer struct {
	mu           sync.Mutex
	server       *httptest.Server
	activeConn   *websocket.Conn
	helloCount   int
	disconnectCh chan struct{}
}

func newReconnectableMockServer(t *testing.T) *ReconnectableMockServer {
	rms := &ReconnectableMockServer{
		disconnectCh: make(chan struct{}, 10),
	}

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/connector/v1/control", func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		rms.mu.Lock()
		rms.activeConn = c
		rms.mu.Unlock()

		for {
			_, msg, err := c.ReadMessage()
			if err != nil {
				break
			}
			// HELLO 메시지에 HELLO_ACK 회신
			if strings.Contains(string(msg), "HELLO") && !strings.Contains(string(msg), "HELLO_ACK") {
				rms.mu.Lock()
				rms.helloCount++
				rms.mu.Unlock()

				ack := protocol.HelloAckMessage{
					BaseEnvelope: protocol.BaseEnvelope{
						Type:      protocol.MessageTypeHelloAck,
						MessageID: "ack-reconnect-1",
						SentAt:    time.Now().UTC(),
					},
					Payload: protocol.HelloAckPayload{
						ServerTime:               time.Now().UTC(),
						HeartbeatIntervalSeconds: 1, // 테스트를 위해 1초
						OfflineTimeoutSeconds:    3,
					},
				}
				_ = c.WriteJSON(ack)
			}
		}
	})

	rms.server = httptest.NewServer(mux)
	return rms
}

func (r *ReconnectableMockServer) URL() string {
	return "ws" + strings.TrimPrefix(r.server.URL, "http") + "/connector/v1/control"
}

func (r *ReconnectableMockServer) ForceDisconnect() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.activeConn != nil {
		_ = r.activeConn.Close()
		r.activeConn = nil
	}
}

func (r *ReconnectableMockServer) Close() {
	r.ForceDisconnect()
	r.server.Close()
}

func (r *ReconnectableMockServer) HelloCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.helloCount
}

func TestSupervisor_LifecycleAndReconnect(t *testing.T) {
	server := newReconnectableMockServer(t)
	defer server.Close()

	cfg := wss.Config{
		BaseURL:          server.URL(),
		Credential:       "test-secret-token",
		ConnectorVersion: "0.1.0-test",
	}

	backoff := wss.BackoffPolicy{
		InitialInterval:     20 * time.Millisecond,
		MaxInterval:         100 * time.Millisecond,
		Multiplier:          1.5,
		RandomizationFactor: 0.1,
	}

	mockProv := &provider.MockProvider{}
	handler := wss.NewHandler(mockProv, nil)
	supervisor := wss.NewSupervisor(cfg, handler, backoff)

	connectedCount := 0
	disconnectedCount := 0
	var mu sync.Mutex

	supervisor.SetOnConnected(func(ack *protocol.HelloAckPayload) {
		mu.Lock()
		connectedCount++
		mu.Unlock()
	})

	supervisor.SetOnDisconnected(func(err error) {
		mu.Lock()
		disconnectedCount++
		mu.Unlock()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- supervisor.Run(ctx)
	}()

	// 1. 최초 연결 대기 (HELLO 수신)
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if server.HelloCount() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if server.HelloCount() < 1 {
		t.Fatalf("expected initial HELLO handshake, got %d", server.HelloCount())
	}

	// 2. 서버 측에서 강제 단절 유발
	server.ForceDisconnect()

	// 3. 재접속 대기 (자동으로 다시 Dial 및 HELLO 전송)
	reconnectDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(reconnectDeadline) {
		if server.HelloCount() >= 2 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	mu.Lock()
	finalConn := connectedCount
	finalDisc := disconnectedCount
	mu.Unlock()

	if server.HelloCount() < 2 {
		t.Fatalf("expected at least 2 HELLO handshakes (reconnected), got %d (conn: %d, disc: %d)",
			server.HelloCount(), finalConn, finalDisc)
	}

	if finalDisc < 1 {
		t.Fatalf("expected OnDisconnected callback to be invoked at least once, got %d", finalDisc)
	}

	// 4. 컨텍스트 종료 시 정상 반환 확인
	cancel()
	select {
	case err := <-runErrCh:
		if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
			t.Fatalf("unexpected error from supervisor on cancel: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("supervisor did not stop within timeout after cancel")
	}
}

func TestSupervisor_ContextCancelBeforeDial(t *testing.T) {
	cfg := wss.Config{
		BaseURL: "ws://127.0.0.1:9999/connector/v1/control",
	}
	supervisor := wss.NewSupervisor(cfg, nil, wss.NewDefaultBackoffPolicy())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 즉시 취소

	err := supervisor.Run(ctx)
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
