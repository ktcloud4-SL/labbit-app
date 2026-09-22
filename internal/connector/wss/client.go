package wss

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/rabbit-app/internal/connector/protocol"
)

// Client 는 SaaS 와의 Control WSS 통신을 담당하는 클라이언트입니다.
type Client struct {
	cfg       Config
	mu        sync.RWMutex
	conn      *websocket.Conn
	startedAt time.Time
}

// NewClient 는 새 WSS Client 인스턴스를 생성합니다.
func NewClient(cfg Config) *Client {
	cfg.EnsureDefaults()
	if cfg.RuntimeID == "" {
		cfg.RuntimeID = generateUUID()
	}
	return &Client{
		cfg:       cfg,
		startedAt: time.Now().UTC(),
	}
}

// Dial 은 SaaS Control WSS 엔드포인트로 연결합니다.
// 1. Subprotocol labbit.connector.v1 요청
// 2. Authorization: Bearer <credential> 헤더 전송
// 3. ReadLimit 1 MiB 설정 (contracts/connector SSOT 규격)
func (c *Client) Dial(ctx context.Context) error {
	endpoint, err := c.cfg.ResolveEndpoint()
	if err != nil {
		return err
	}

	cred, err := c.cfg.GetCredential()
	if err != nil {
		return err
	}

	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+cred)

	dialer := websocket.Dialer{
		Subprotocols:     []string{protocol.SubprotocolControl},
		HandshakeTimeout: 10 * time.Second,
	}

	conn, resp, err := dialer.DialContext(ctx, endpoint, headers)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("wss dial failed with status %d: %w", resp.StatusCode, err)
		}
		return fmt.Errorf("wss dial failed: %w", err)
	}

	// 1 MiB JSON 메시지 크기 상한 설정 (contracts/connector SSOT)
	conn.SetReadLimit(c.cfg.ReadLimit)

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	return nil
}

// SendHello 는 연결 직후 HELLO 메시지를 전송하고 SaaS 의 HELLO_ACK 를 수신·검증합니다.
func (c *Client) SendHello(ctx context.Context) (*protocol.HelloAckPayload, error) {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		return nil, fmt.Errorf("not connected: must call Dial first")
	}

	msgID := generateUUID()
	now := time.Now().UTC()

	helloMsg := protocol.HelloMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:      protocol.MessageTypeHello,
			MessageID: msgID,
			SentAt:    now,
		},
		Payload: protocol.HelloPayload{
			ConnectorVersion: c.cfg.ConnectorVersion,
			RuntimeID:        c.cfg.RuntimeID,
			StartedAt:        c.startedAt,
			Capabilities:     c.cfg.Capabilities,
		},
	}

	data, err := json.Marshal(helloMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal HELLO message: %w", err)
	}

	// HELLO 메시지 전송
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return nil, fmt.Errorf("failed to send HELLO message: %w", err)
	}

	// SaaS 의 HELLO_ACK 회신 수신 (Context 취소 및 데드라인 지원)
	type readResult struct {
		msg []byte
		err error
	}
	ch := make(chan readResult, 1)

	go func() {
		_, msg, err := conn.ReadMessage()
		ch <- readResult{msg: msg, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		if res.err != nil {
			return nil, fmt.Errorf("failed to read HELLO_ACK: %w", res.err)
		}

		var ack protocol.HelloAckMessage
		if err := json.Unmarshal(res.msg, &ack); err != nil {
			return nil, fmt.Errorf("failed to decode HELLO_ACK response: %w", err)
		}

		if ack.Type != protocol.MessageTypeHelloAck {
			return nil, fmt.Errorf("expected %s but received %s", protocol.MessageTypeHelloAck, ack.Type)
		}

		if ack.ReplyToMessageID != "" && ack.ReplyToMessageID != msgID {
			return nil, fmt.Errorf("replyToMessageId mismatch: expected %s, got %s", msgID, ack.ReplyToMessageID)
		}

		return &ack.Payload, nil
	}
}

// Conn 은 활성화된 websocket.Conn 인스턴스를 반환합니다.
func (c *Client) Conn() *websocket.Conn {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn
}

// SendMessage 는 WebSocket 연결을 통해 JSON 메시지를 전송합니다.
func (c *Client) SendMessage(ctx context.Context, msg interface{}) error {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		return fmt.Errorf("not connected: must call Dial first")
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	return conn.WriteMessage(websocket.TextMessage, data)
}

// Close 는 WebSocket 연결을 정상 종료합니다.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		err := c.conn.WriteMessage(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "connector client closing"),
		)
		_ = c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

func generateUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
