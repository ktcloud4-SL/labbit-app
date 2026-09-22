package mock

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/rabbit-app/internal/connector/protocol"
)

// TerminalRelay 는 Terminal Data WSS 통신 테스트를 위한 In-memory Mock Relay 서버입니다.
type TerminalRelay struct {
	server   *httptest.Server
	upgrader websocket.Upgrader

	mu         sync.Mutex
	conn       *websocket.Conn
	activeSess string
	attachRecv chan protocol.TerminalDataAttachMessage
	binRecv    chan []byte
	closed     bool
}

// NewTerminalRelay 는 로컬 테스트용 Mock Terminal Relay 서버를 시작합니다.
func NewTerminalRelay() *TerminalRelay {
	r := &TerminalRelay{
		upgrader: websocket.Upgrader{
			CheckOrigin:  func(req *http.Request) bool { return true },
			Subprotocols: []string{protocol.SubprotocolTerminalData},
		},
		attachRecv: make(chan protocol.TerminalDataAttachMessage, 10),
		binRecv:    make(chan []byte, 100),
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

func (r *TerminalRelay) handleWebSocket(w http.ResponseWriter, req *http.Request) {
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
			Resumed:          false,
			HistoryAvailable: false,
		},
	}

	r.mu.Lock()
	_ = conn.WriteJSON(attachedResp)
	r.activeSess = attachMsg.TerminalSessionID
	r.mu.Unlock()

	// 3. 메시지 루프: 커넥터로부터 오는 바이너리 및 텍스트 프레임 수신
	for {
		mType, p, rErr := conn.ReadMessage()
		if rErr != nil {
			return
		}
		if mType == websocket.BinaryMessage {
			r.binRecv <- p
		}
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

// ReadBinary 는 커넥터 PTY 에서 전달된 출력 바이트를 대기하여 수신합니다.
func (r *TerminalRelay) ReadBinary(timeout time.Duration) ([]byte, error) {
	select {
	case b := <-r.binRecv:
		return b, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout waiting for binary output")
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
