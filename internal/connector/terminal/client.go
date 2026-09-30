package terminal

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

// DataWSSClientConfig 는 Terminal Data WSS 클라이언트 설정입니다.
type DataWSSClientConfig struct {
	EndpointURL string
	Credential  string
	RuntimeID   string
	DialTimeout time.Duration
}

// DataWSSClient 는 단일 터미널 세션을 위한 Terminal Data WebSocket 연결 및 입출력 스트리머입니다.
type DataWSSClient struct {
	config  DataWSSClientConfig
	session *Session

	conn    *websocket.Conn
	writeMu sync.Mutex

	ctx    context.Context
	cancel context.CancelFunc

	closed bool
	mu     sync.Mutex
}

// NewDataWSSClient 는 지정된 설정과 세션에 대한 데이터 스트리머를 생성합니다.
func NewDataWSSClient(cfg DataWSSClientConfig, session *Session) *DataWSSClient {
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 10 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &DataWSSClient{
		config:  cfg,
		session: session,
		ctx:     ctx,
		cancel:  cancel,
	}
}

// DialAndAttach 는 Relay 로 WebSocket 연결을 맺고, ATTACH 핸드셰이크 및 응답 correlation 검증을 완료한 후
// 세션에 활성 데이터 연결을 바인딩합니다. 실패 시 연결을 닫고 에러를 반환합니다.
func (c *DataWSSClient) DialAndAttach(ctx context.Context) error {
	if c.config.EndpointURL == "" {
		return errors.New("terminal data wss endpoint URL is empty")
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: c.config.DialTimeout,
		Subprotocols:     []string{protocol.SubprotocolTerminalData},
	}

	header := http.Header{}
	if c.config.Credential != "" {
		header.Set("Authorization", "Bearer "+c.config.Credential)
	}

	conn, resp, err := dialer.DialContext(ctx, c.config.EndpointURL, header)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("dial failed with HTTP %d: %w", resp.StatusCode, err)
		}
		return fmt.Errorf("dial failed: %w", err)
	}

	c.conn = conn

	// 1. TERMINAL_DATA_ATTACH 핸드셰이크 발송
	attachMsgID := generateUUID()
	attachReq := protocol.TerminalDataAttachMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:              protocol.MessageTypeTerminalDataAttach,
			MessageID:         attachMsgID,
			SentAt:            time.Now().UTC(),
			TerminalSessionID: c.session.SessionID,
			LabInstanceID:     c.session.LabInstanceID,
			Generation:        c.session.Generation,
		},
		Payload: protocol.TerminalDataAttachPayload{
			RuntimeID: c.config.RuntimeID,
		},
	}

	c.writeMu.Lock()
	err = c.conn.WriteJSON(attachReq)
	c.writeMu.Unlock()
	if err != nil {
		_ = c.conn.Close()
		return fmt.Errorf("failed to send ATTACH message: %w", err)
	}

	// 2. TERMINAL_DATA_ATTACHED 응답 대기 및 검증
	readTimeout := c.config.DialTimeout
	if readTimeout <= 0 {
		readTimeout = 10 * time.Second
	}
	_ = c.conn.SetReadDeadline(time.Now().Add(readTimeout))
	msgType, data, err := c.conn.ReadMessage()
	if err != nil {
		_ = c.conn.Close()
		return fmt.Errorf("failed to read ATTACHED response: %w", err)
	}
	_ = c.conn.SetReadDeadline(time.Time{})

	if msgType != websocket.TextMessage {
		_ = c.conn.Close()
		return fmt.Errorf("expected text JSON response, got binary frame")
	}

	var baseEnv protocol.BaseEnvelope
	if parseErr := json.Unmarshal(data, &baseEnv); parseErr != nil {
		_ = c.conn.Close()
		return fmt.Errorf("malformed JSON from relay: %w", parseErr)
	}

	if baseEnv.Type != protocol.MessageTypeTerminalDataAttached {
		_ = c.conn.Close()
		return fmt.Errorf("unexpected message type: %s (expected TERMINAL_DATA_ATTACHED)", baseEnv.Type)
	}

	// Correlation 검증 (Reviewer 3, 6번 지적 사항)
	if baseEnv.ReplyToMessageID != attachMsgID {
		_ = c.conn.Close()
		return fmt.Errorf("replyToMessageId mismatch: want %s, got %s", attachMsgID, baseEnv.ReplyToMessageID)
	}
	if baseEnv.TerminalSessionID != c.session.SessionID ||
		baseEnv.LabInstanceID != c.session.LabInstanceID ||
		baseEnv.Generation != c.session.Generation {
		_ = c.conn.Close()
		return fmt.Errorf("correlation mismatch: want session=%s lab=%s gen=%d, got session=%s lab=%s gen=%d",
			c.session.SessionID, c.session.LabInstanceID, c.session.Generation,
			baseEnv.TerminalSessionID, baseEnv.LabInstanceID, baseEnv.Generation)
	}

	// 3. 세션에 WebSocket 연결 바인딩
	_ = c.session.AttachDataConn(c.conn)
	return nil
}

// StartStreaming 은 Attach 성공 후 WebSocket 과 PTY 간의 양방향 수신 루프를 시작합니다.
func (c *DataWSSClient) StartStreaming() {
	go c.pumpFromWebSocketToPTY()
}

// ConnectAndStream 은 호환성을 위해 DialAndAttach 후 StartStreaming 을 수행합니다.
func (c *DataWSSClient) ConnectAndStream() error {
	if err := c.DialAndAttach(context.Background()); err != nil {
		return err
	}
	c.StartStreaming()
	return nil
}

// pumpFromWebSocketToPTY 는 WebSocket 수신 메시지를 PTY 또는 제어 프레임으로 전달합니다.
func (c *DataWSSClient) pumpFromWebSocketToPTY() {
	defer func() {
		c.session.DetachDataConn(c.conn)
	}()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.session.DetachChan():
			return
		default:
		}

		msgType, payload, err := c.conn.ReadMessage()
		if err != nil {
			// 연결 끊김 (브라우저 탭 닫힘 / 네트워크 단절)
			return
		}

		switch msgType {
		case websocket.BinaryMessage:
			// 실제 터미널 stdin 데이터 -> PTY 로 쓰기
			if c.session.PTY != nil {
				_, writeErr := c.session.PTY.Write(payload)
				if writeErr != nil {
					return
				}
			}

		case websocket.TextMessage:
			// JSON 제어 프레임 (RESIZE, CLOSE, ERROR 등)
			var baseEnv protocol.BaseEnvelope
			if jsonErr := json.Unmarshal(payload, &baseEnv); jsonErr != nil {
				continue
			}

			// Correlation 검증 (Reviewer 6번 지적 사항)
			if baseEnv.TerminalSessionID != c.session.SessionID ||
				baseEnv.LabInstanceID != c.session.LabInstanceID ||
				baseEnv.Generation != c.session.Generation {
				c.sendError(protocol.TerminalErrProtocolError, "mismatched session or generation correlation in control frame", false)
				continue
			}

			switch baseEnv.Type {
			case protocol.MessageTypeTerminalDataResize:
				var resizeMsg protocol.TerminalDataResizeMessage
				if err := json.Unmarshal(payload, &resizeMsg); err == nil {
					_ = c.session.Resize(resizeMsg.Payload.Cols, resizeMsg.Payload.Rows)
				}

			case protocol.MessageTypeTerminalDataClose:
				var closeMsg protocol.TerminalDataCloseMessage
				if err := json.Unmarshal(payload, &closeMsg); err == nil {
					c.session.Close(closeMsg.Payload.Reason, nil, nil)
					return
				}

			case protocol.MessageTypeError:
				var errMsg protocol.TerminalDataErrorMessage
				if err := json.Unmarshal(payload, &errMsg); err == nil && errMsg.Payload.Fatal {
					c.session.Close(errMsg.Payload.Code, nil, errors.New(errMsg.Payload.Message))
					return
				}
			}
		}
	}
}

func (c *DataWSSClient) sendError(code, message string, fatal bool) {
	errMsg := protocol.TerminalDataErrorMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:              protocol.MessageTypeError,
			MessageID:         generateUUID(),
			SentAt:            time.Now().UTC(),
			TerminalSessionID: c.session.SessionID,
			LabInstanceID:     c.session.LabInstanceID,
			Generation:        c.session.Generation,
		},
		Payload: protocol.TerminalDataErrorPayload{
			Code:    code,
			Message: message,
			Fatal:   fatal,
		},
	}
	c.writeMu.Lock()
	if c.conn != nil {
		_ = c.conn.WriteJSON(errMsg)
	}
	c.writeMu.Unlock()
}

// Close 는 클라이언트를 종료합니다.
func (c *DataWSSClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	c.cancel()
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

func generateUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
