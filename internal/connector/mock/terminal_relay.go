package mock

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

// TerminalRelay 는 Terminal Data WSS 통신 테스트를 위한 In-memory Mock Relay 서버입니다.
type TerminalRelay struct {
	server   *httptest.Server
	upgrader websocket.Upgrader

	mu             sync.Mutex
	conn           *websocket.Conn
	activeSess     string
	activeLabID    string
	activeGen      int64
	attachRecv     chan protocol.TerminalDataAttachMessage
	binRecv        chan []byte
	textRecv       chan []byte
	closeRecv      chan int
	authRejectRecv chan string
	authValidator  func(req *http.Request) int
	beforeAttached func(attachMsg protocol.TerminalDataAttachMessage)
	closed         bool
}

// NewTerminalRelay 는 로컬 테스트용 Mock Terminal Relay 서버를 시작합니다.
func NewTerminalRelay() *TerminalRelay {
	r := &TerminalRelay{
		upgrader: websocket.Upgrader{
			CheckOrigin:  func(req *http.Request) bool { return true },
			Subprotocols: []string{protocol.SubprotocolTerminalData},
		},
		attachRecv:     make(chan protocol.TerminalDataAttachMessage, 10),
		binRecv:        make(chan []byte, 100),
		textRecv:       make(chan []byte, 10),
		closeRecv:      make(chan int, 10),
		authRejectRecv: make(chan string, 10),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/connector/v1/terminal-data", r.handleWebSocket)
	r.server = httptest.NewServer(mux)

	return r
}

// URL 은 WebSocket 엔드포인트 URL을 반환합니다.
func (r *TerminalRelay) URL() string {
	return strings.Replace(r.server.URL, "http://", "ws://", 1) + "/connector/v1/terminal-data"
}

// Close 는 Mock 서버를 종료합니다.
func (r *TerminalRelay) Close() {
	r.mu.Lock()
	r.closed = true
	if r.conn != nil {
		_ = r.conn.Close()
	}
	r.mu.Unlock()
	r.server.Close()
}

// SetAuthValidator 는 WSS 업그레이드 전 HTTP 인증 검증 콜백을 설정합니다.
func (r *TerminalRelay) SetAuthValidator(fn func(req *http.Request) int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authValidator = fn
}

// SetBeforeAttached 는 ATTACHED 회신 발송 직전에 실행할 인터리빙 훅을 설정합니다.
func (r *TerminalRelay) SetBeforeAttached(fn func(attachMsg protocol.TerminalDataAttachMessage)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.beforeAttached = fn
}

// CloseWithCode 는 특정 WebSocket Close 코드로 활성 연결을 종료합니다.
func (r *TerminalRelay) CloseWithCode(code int, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conn != nil {
		_ = r.conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(code, reason),
			time.Now().Add(time.Second))
		_ = r.conn.Close()
		r.conn = nil
	}
}

// WaitForAuthReject 는 인증 실패(401/403)로 거절된 요청의 Authorization 헤더를 수신 대기합니다.
func (r *TerminalRelay) WaitForAuthReject(timeout time.Duration) (string, error) {
	select {
	case auth := <-r.authRejectRecv:
		return auth, nil
	case <-time.After(timeout):
		return "", fmt.Errorf("timeout waiting for auth rejection")
	}
}

func (r *TerminalRelay) handleWebSocket(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	authFn := r.authValidator
	r.mu.Unlock()
	if authFn != nil {
		if code := authFn(req); code != http.StatusOK {
			select {
			case r.authRejectRecv <- req.Header.Get("Authorization"):
			default:
			}
			http.Error(w, "unauthorized", code)
			return
		}
	}

	conn, err := r.upgrader.Upgrade(w, req, nil)
	if err != nil {
		return
	}

	r.mu.Lock()
	if r.conn != nil {
		_ = r.conn.Close()
	}
	r.conn = conn
	r.mu.Unlock()

	// 1. TERMINAL_DATA_ATTACH 메시지 수신 대기
	msgType, data, err := conn.ReadMessage()
	if err != nil {
		return
	}
	if msgType != websocket.TextMessage {
		return
	}

	var attachMsg protocol.TerminalDataAttachMessage
	if err := json.Unmarshal(data, &attachMsg); err != nil {
		return
	}

	r.attachRecv <- attachMsg

	r.mu.Lock()
	hook := r.beforeAttached
	r.mu.Unlock()
	if hook != nil {
		hook(attachMsg)
	}

	r.mu.Lock()
	isResume := (r.activeSess == attachMsg.TerminalSessionID && r.activeSess != "")
	r.mu.Unlock()

	f := false
	// 2. TERMINAL_DATA_ATTACHED 회신
	attachedResp := protocol.TerminalDataAttachedMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:              protocol.MessageTypeTerminalDataAttached,
			MessageID:         "attached-msg-1",
			ReplyToMessageID:  attachMsg.MessageID,
			SentAt:            time.Now().UTC(),
			TerminalSessionID: attachMsg.TerminalSessionID,
			LabInstanceID:     attachMsg.LabInstanceID,
			Generation:        attachMsg.Generation,
		},
		Payload: protocol.TerminalDataAttachedPayload{
			Resumed:          &isResume,
			HistoryAvailable: &f,
		},
	}

	r.mu.Lock()
	_ = conn.WriteJSON(attachedResp)
	r.activeSess = attachMsg.TerminalSessionID
	r.activeLabID = attachMsg.LabInstanceID
	r.activeGen = attachMsg.Generation
	r.mu.Unlock()

	// 3. 메시지 루프: 커넥터로부터 오는 바이너리 및 텍스트 프레임 수신
	for {
		mType, p, rErr := conn.ReadMessage()
		if rErr != nil {
			var closeCode int
			var closeErr *websocket.CloseError
			if errors.As(rErr, &closeErr) {
				closeCode = closeErr.Code
			}
			select {
			case r.closeRecv <- closeCode:
			default:
			}
			return
		}
		if mType == websocket.BinaryMessage {
			select {
			case r.binRecv <- p:
			default:
			}
		} else if mType == websocket.TextMessage {
			select {
			case r.textRecv <- p:
			default:
			}
		}
	}
}

// WaitForClose 는 Connector 로부터 소켓 Close 프레임이 수신될 때까지 대기합니다.
func (r *TerminalRelay) WaitForClose(timeout time.Duration) (int, error) {
	select {
	case code := <-r.closeRecv:
		return code, nil
	case <-time.After(timeout):
		return 0, fmt.Errorf("timeout waiting for connection close")
	}
}

// WaitForAttach 는 Connector 가 ATTACH 메시지를 보낼 때까지 대기합니다.
func (r *TerminalRelay) WaitForAttach(timeout time.Duration) (*protocol.TerminalDataAttachMessage, error) {
	select {
	case msg := <-r.attachRecv:
		return &msg, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout waiting for TERMINAL_DATA_ATTACH")
	}
}

// SendBinary 는 브라우저 사용자가 키보드로 타이핑한 입력을 시뮬레이션하여 커넥터로 보냅니다.
func (r *TerminalRelay) SendBinary(data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conn == nil {
		return fmt.Errorf("no active connection")
	}
	return r.conn.WriteMessage(websocket.BinaryMessage, data)
}

// SendText 는 제어 텍스트 프레임(JSON 등)을 커넥터로 보냅니다.
func (r *TerminalRelay) SendText(data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conn == nil {
		return fmt.Errorf("no active connection")
	}
	return r.conn.WriteMessage(websocket.TextMessage, data)
}

// ReadBinary 는 커넥터 PTY 에서 전달된 출력 바이트를 대기하여 수신합니다.
func (r *TerminalRelay) ReadBinary(timeout time.Duration) ([]byte, error) {
	select {
	case b := <-r.binRecv:
		return b, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout waiting for binary output")
	}
}

// ReadText 는 커넥터로부터 전달된 JSON 제어 텍스트 프레임(예: TERMINAL_DATA_ENDED)을 수신합니다.
func (r *TerminalRelay) ReadText(timeout time.Duration) ([]byte, error) {
	select {
	case b := <-r.textRecv:
		return b, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout waiting for text control frame")
	}
}

// SendResize 는 창 크기 조절 제어 프레임을 커넥터로 보냅니다.
func (r *TerminalRelay) SendResize(cols, rows int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conn == nil {
		return fmt.Errorf("no active connection")
	}
	resizeMsg := protocol.TerminalDataResizeMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:              protocol.MessageTypeTerminalDataResize,
			MessageID:         "resize-1",
			SentAt:            time.Now().UTC(),
			TerminalSessionID: r.activeSess,
			LabInstanceID:     r.activeLabID,
			Generation:        r.activeGen,
		},
		Payload: protocol.TerminalDataResizePayload{
			Cols: cols,
			Rows: rows,
		},
	}
	return r.conn.WriteJSON(resizeMsg)
}

// DisconnectConnection 은 브라우저 탭 닫힘 / 일시적 네트워크 단절을 시뮬레이션하기 위해 소켓을 강제 종료합니다.
func (r *TerminalRelay) DisconnectConnection() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conn != nil {
		_ = r.conn.Close()
		r.conn = nil
	}
}
