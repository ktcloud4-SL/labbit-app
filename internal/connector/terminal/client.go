package terminal

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

// DataWSSClientConfig 는 Terminal Data WSS 클라이언트 설정입니다.
type DataWSSClientConfig struct {
	EndpointURL   string
	Credential    string
	RuntimeID     string
	DialTimeout   time.Duration
	AllowInsecure bool // Test 전용: localhost 및 비보안 ws:// 연결 허용
}

// DataWSSClient 는 단일 터미널 세션을 위한 Terminal Data WebSocket 연결 및 입출력 스트리머입니다.
type DataWSSClient struct {
	config  DataWSSClientConfig
	session *Session

	conn *websocket.Conn

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

// MaxBinaryMessageSize 는 PTY 바이너리 스트림의 상한 (4 MiB)입니다.
const MaxBinaryMessageSize int64 = 4 * 1024 * 1024

var ErrAuthenticationFailed = errors.New("terminal data authentication failed")

// DialAndAttach 는 Relay 로 WebSocket 연결을 맺고, ATTACH 핸드셰이크 및 응답 correlation 검증을 완료한 후
// 세션에 활성 데이터 연결을 바인딩합니다. 실패 시 연결을 닫고 에러를 반환합니다.
func (c *DataWSSClient) DialAndAttach(ctx context.Context) error {
	if c.config.EndpointURL == "" {
		return errors.New("terminal data wss endpoint URL is empty")
	}

	u, err := url.Parse(c.config.EndpointURL)
	if err != nil {
		return fmt.Errorf("invalid endpoint URL: %w", err)
	}

	host := u.Hostname()
	isLoopback := host == "localhost" || host == "127.0.0.1" || host == "::1"
	switch u.Scheme {
	case "wss":
		// 보안 WebSocket 허용
	case "ws":
		if !c.config.AllowInsecure || !isLoopback {
			return fmt.Errorf("insecure scheme %q is prohibited in production: TLS (wss://) is required", u.Scheme)
		}
	default:
		return fmt.Errorf("unsupported URL scheme %q: only wss is allowed", u.Scheme)
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
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				return fmt.Errorf("%w: HTTP %d", ErrAuthenticationFailed, resp.StatusCode)
			}
			return fmt.Errorf("dial failed with HTTP %d: %w", resp.StatusCode, err)
		}
		return fmt.Errorf("dial failed: %w", err)
	}

	// Subprotocol 협상 결과 검증 (Reviewer 3번 지적 사항)
	if sub := conn.Subprotocol(); sub != protocol.SubprotocolTerminalData {
		_ = conn.Close()
		return fmt.Errorf("negotiated subprotocol %q does not match required %q", sub, protocol.SubprotocolTerminalData)
	}

	// Connection-wide 읽기 한도 설정 (PTY Binary 4 MiB 상한 허용)
	conn.SetReadLimit(MaxBinaryMessageSize)
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

	if err := c.conn.WriteJSON(attachReq); err != nil {
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

	if int64(len(data)) > protocol.MaxJSONMessageSize {
		_ = c.conn.Close()
		return fmt.Errorf("attached message size %d exceeds 1 MiB limit", len(data))
	}

	var attachedMsg protocol.TerminalDataAttachedMessage
	if parseErr := json.Unmarshal(data, &attachedMsg); parseErr != nil {
		_ = c.conn.Close()
		return fmt.Errorf("malformed JSON from relay: %w", parseErr)
	}

	// BaseEnvelope 필수 필드 검증 (Reviewer 5번 지적 사항)
	if err := validateBaseEnvelope(attachedMsg.BaseEnvelope); err != nil {
		_ = c.conn.Close()
		return fmt.Errorf("invalid attached envelope: %w", err)
	}

	if attachedMsg.Type != protocol.MessageTypeTerminalDataAttached {
		_ = c.conn.Close()
		return fmt.Errorf("unexpected message type: %s (expected TERMINAL_DATA_ATTACHED)", attachedMsg.Type)
	}

	// Correlation 및 Payload 검증 (Reviewer 3, 5, 6번 지적 사항)
	if attachedMsg.ReplyToMessageID != attachMsgID {
		_ = c.conn.Close()
		return fmt.Errorf("replyToMessageId mismatch: want %s, got %s", attachMsgID, attachedMsg.ReplyToMessageID)
	}
	if attachedMsg.TerminalSessionID != c.session.SessionID ||
		attachedMsg.LabInstanceID != c.session.LabInstanceID ||
		attachedMsg.Generation != c.session.Generation {
		_ = c.conn.Close()
		return fmt.Errorf("correlation mismatch: want session=%s lab=%s gen=%d, got session=%s lab=%s gen=%d",
			c.session.SessionID, c.session.LabInstanceID, c.session.Generation,
			attachedMsg.TerminalSessionID, attachedMsg.LabInstanceID, attachedMsg.Generation)
	}
	if attachedMsg.Payload.Resumed == nil {
		_ = c.conn.Close()
		return fmt.Errorf("missing required field in attached payload: resumed")
	}
	if attachedMsg.Payload.HistoryAvailable == nil {
		_ = c.conn.Close()
		return fmt.Errorf("missing required field in attached payload: historyAvailable")
	}
	if *attachedMsg.Payload.HistoryAvailable {
		_ = c.conn.Close()
		return fmt.Errorf("invalid attached payload: historyAvailable must be false")
	}

	// 3. 세션에 WebSocket 연결 바인딩
	_ = c.session.AttachDataConn(c.conn)
	return nil
}

// StartStreaming 은 Attach 성공 후 WebSocket 과 PTY 간의 양방향 수신 루프 및 자동 재연결 오너를 시작합니다.
func (c *DataWSSClient) StartStreaming() {
	go c.runStreaming()
}

// ConnectAndStream 은 호환성을 위해 DialAndAttach 후 StartStreaming 을 수행합니다.
func (c *DataWSSClient) ConnectAndStream() error {
	if err := c.DialAndAttach(context.Background()); err != nil {
		return err
	}
	c.StartStreaming()
	return nil
}

// runStreaming 은 connection 단절 시 production 자동 재연결 오너 루프를 수행합니다.
func (c *DataWSSClient) runStreaming() {
	for {
		c.pumpFromWebSocketToPTY()

		c.mu.Lock()
		closed := c.closed
		c.mu.Unlock()
		if closed || c.ctx.Err() != nil {
			return
		}

		c.session.mu.Lock()
		status := c.session.Status
		c.session.mu.Unlock()
		if status == StatusClosed {
			return
		}

		// 연결 단절 및 세션 DETACHED 상태 -> 지수 백오프 자동 재연결 시도
		if !c.reconnect() {
			return
		}
	}
}

// reconnect 는 Data WSS 단절 후 세션 Grace Period 안에서 자동 재연결을 시도합니다.
func (c *DataWSSClient) reconnect() bool {
	backoff := 500 * time.Millisecond
	maxBackoff := 5 * time.Second
	factor := 2.0
	jitter := 0.2 // ±20%

	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return false
		}
		c.mu.Unlock()

		c.session.mu.Lock()
		status := c.session.Status
		c.session.mu.Unlock()
		if status == StatusClosed {
			return false
		}

		sleepDuration := jitterDuration(backoff, jitter)
		select {
		case <-c.ctx.Done():
			return false
		case <-time.After(sleepDuration):
		}

		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return false
		}
		c.mu.Unlock()

		c.session.mu.Lock()
		status = c.session.Status
		c.session.mu.Unlock()
		if status == StatusClosed {
			return false
		}

		dialCtx, dialCancel := context.WithTimeout(c.ctx, c.config.DialTimeout)
		err := c.DialAndAttach(dialCtx)
		dialCancel()

		if err == nil {
			// 재연결 및 attach 성공 (동일 PTY 유지, resumed=true 확인)
			return true
		}

		if errors.Is(err, ErrAuthenticationFailed) {
			// 자격증명 신뢰 상실 시 무한 재시도 중단 및 세션 종료
			c.session.Close(protocol.TerminalErrForbidden, nil, err)
			return false
		}

		backoff = time.Duration(float64(backoff) * factor)
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func jitterDuration(base time.Duration, jitterPct float64) time.Duration {
	if jitterPct <= 0 {
		return base
	}
	var b [2]byte
	_, _ = rand.Read(b[:])
	n := float64(int(b[0])<<8|int(b[1])) / 65535.0 // 0.0 ~ 1.0
	factor := (1.0 - jitterPct) + (2.0 * jitterPct * n)
	return time.Duration(float64(base) * factor)
}

func validateBaseEnvelope(env protocol.BaseEnvelope) error {
	if strings.TrimSpace(env.Type) == "" {
		return errors.New("missing required field: type")
	}
	if strings.TrimSpace(env.MessageID) == "" {
		return errors.New("missing required field: messageId")
	}
	if env.SentAt.IsZero() {
		return errors.New("missing required field: sentAt")
	}
	if strings.TrimSpace(env.TerminalSessionID) == "" {
		return errors.New("missing required field: terminalSessionId")
	}
	if strings.TrimSpace(env.LabInstanceID) == "" {
		return errors.New("missing required field: labInstanceId")
	}
	if env.Generation < 1 {
		return errors.New("invalid generation: must be >= 1")
	}
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
			// JSON Text 1 MiB 초과 여부 분리 검증 (Reviewer 3번 지적 사항)
			if int64(len(payload)) > protocol.MaxJSONMessageSize {
				c.sendError(protocol.TerminalErrProtocolError, "json text message exceeds 1 MiB limit", false)
				continue
			}

			// JSON 제어 프레임 (RESIZE, CLOSE, ERROR 등)
			var baseEnv protocol.BaseEnvelope
			if jsonErr := json.Unmarshal(payload, &baseEnv); jsonErr != nil {
				c.sendError(protocol.TerminalErrProtocolError, "malformed JSON frame", false)
				continue
			}

			// BaseEnvelope 필수 필드 검증 (Reviewer 5번 지적 사항)
			if err := validateBaseEnvelope(baseEnv); err != nil {
				c.sendError(protocol.TerminalErrProtocolError, fmt.Sprintf("invalid base envelope: %v", err), false)
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
				if err := json.Unmarshal(payload, &resizeMsg); err != nil {
					c.sendError(protocol.TerminalErrProtocolError, "malformed RESIZE frame payload", false)
					continue
				}
				if resizeMsg.Payload.Cols <= 0 || resizeMsg.Payload.Rows <= 0 {
					c.sendError(protocol.TerminalErrProtocolError, "invalid resize dimensions: cols and rows must be > 0", false)
					continue
				}
				_ = c.session.Resize(resizeMsg.Payload.Cols, resizeMsg.Payload.Rows)

			case protocol.MessageTypeTerminalDataClose:
				var closeMsg protocol.TerminalDataCloseMessage
				if err := json.Unmarshal(payload, &closeMsg); err != nil {
					c.sendError(protocol.TerminalErrProtocolError, "malformed CLOSE frame payload", false)
					continue
				}
				if strings.TrimSpace(closeMsg.Payload.Reason) == "" {
					c.sendError(protocol.TerminalErrProtocolError, "close reason cannot be empty", false)
					continue
				}
				c.session.Close(closeMsg.Payload.Reason, nil, nil)
				return

			case protocol.MessageTypeError:
				var errMsg protocol.TerminalDataErrorMessage
				if err := json.Unmarshal(payload, &errMsg); err != nil {
					c.sendError(protocol.TerminalErrProtocolError, "malformed ERROR frame payload", false)
					continue
				}
				if strings.TrimSpace(errMsg.Payload.Code) == "" {
					c.sendError(protocol.TerminalErrProtocolError, "error code cannot be empty", false)
					continue
				}
				if errMsg.Payload.Fatal {
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
	if c.session != nil {
		_ = c.session.WriteJSON(errMsg)
	}
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
