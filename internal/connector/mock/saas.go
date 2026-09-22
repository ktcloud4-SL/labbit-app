package mock

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/rabbit-app/internal/connector/protocol"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
	Subprotocols: []string{protocol.SubprotocolControl},
}

// MockSaaS 는 Connector WSS 개발 및 통합 검증을 위한 모의 SaaS 서버입니다.
type MockSaaS struct {
	Server          *httptest.Server
	ExpectedToken   string
	OnHelloReceived func(raw []byte)
	OnMsgReceived   func(raw []byte)

	mu       sync.Mutex
	conn     *websocket.Conn
	received [][]byte
}

// NewMockSaaS 는 테스트용 Mock SaaS 서버를 기동합니다.
func NewMockSaaS(expectedToken string) *MockSaaS {
	m := &MockSaaS{
		ExpectedToken: expectedToken,
		received:      make([][]byte, 0),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/connector/v1/control", m.handleControlWSS)

	m.Server = httptest.NewServer(mux)
	return m
}

// Close 는 Mock 서버를 종료합니다.
func (m *MockSaaS) Close() {
	m.mu.Lock()
	if m.conn != nil {
		_ = m.conn.Close()
	}
	m.mu.Unlock()
	m.Server.Close()
}

// URL 은 ws:// 형태의 Control 엔드포인트 URL을 반환합니다.
func (m *MockSaaS) URL() string {
	return "ws" + strings.TrimPrefix(m.Server.URL, "http") + "/connector/v1/control"
}

func (m *MockSaaS) handleControlWSS(w http.ResponseWriter, r *http.Request) {
	// 1. Authorization: Bearer <token> 검증
	authHeader := r.Header.Get("Authorization")
	if m.ExpectedToken != "" {
		expected := "Bearer " + m.ExpectedToken
		if authHeader != expected {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	// 2. WSS Upgrade
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	m.mu.Lock()
	m.conn = c
	m.mu.Unlock()

	defer c.Close()

	for {
		_, message, err := c.ReadMessage()
		if err != nil {
			break
		}

		m.mu.Lock()
		m.received = append(m.received, message)
		m.mu.Unlock()

		if m.OnMsgReceived != nil {
			m.OnMsgReceived(message)
		}

		// HELLO 메시지 수신 시 자동으로 HELLO_ACK 회신
		var env protocol.BaseEnvelope
		if err := json.Unmarshal(message, &env); err == nil {
			if env.Type == protocol.MessageTypeHello {
				if m.OnHelloReceived != nil {
					m.OnHelloReceived(message)
				}
				m.sendHelloAck(c, env.MessageID)
			}
		}
	}
}

func (m *MockSaaS) sendHelloAck(c *websocket.Conn, replyTo string) {
	ack := map[string]interface{}{
		"type":             protocol.MessageTypeHelloAck,
		"messageId":        "mock-ack-msg-1",
		"replyToMessageId": replyTo,
		"sentAt":           time.Now().UTC().Format(time.RFC3339),
		"payload": map[string]interface{}{
			"serverTime":               time.Now().UTC().Format(time.RFC3339),
			"heartbeatIntervalSeconds": 15,
			"offlineTimeoutSeconds":    45,
		},
	}
	bytes, _ := json.Marshal(ack)
	_ = c.WriteMessage(websocket.TextMessage, bytes)
}

// SendCommand 는 모의 SaaS에서 Connector로 OperationCommand를 전송합니다.
func (m *MockSaaS) SendCommand(cmd map[string]interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	bytes, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	return m.conn.WriteMessage(websocket.TextMessage, bytes)
}

// SendRaw 는 모의 SaaS에서 임의의 구조체 메시지를 JSON 직렬화하여 전송합니다.
func (m *MockSaaS) SendRaw(msg interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	bytes, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return m.conn.WriteMessage(websocket.TextMessage, bytes)
}

// ReceivedMessages 는 지금까지 수신된 모든 원본 메시지 사본을 반환합니다.
func (m *MockSaaS) ReceivedMessages() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := make([][]byte, len(m.received))
	copy(res, m.received)
	return res
}

