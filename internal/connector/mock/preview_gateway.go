package mock

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

// PreviewGateway 는 Preview Data WSS 통신 테스트를 위한 In-memory Mock Gateway 서버입니다.
type PreviewGateway struct {
	server   *httptest.Server
	upgrader websocket.Upgrader

	mu                          sync.Mutex
	conn                        *websocket.Conn
	activeSess                  string
	activeLabID                 string
	activeGen                   int64
	attachRecv                  chan protocol.PreviewAttachMessage
	binRecv                     chan []byte
	textRecv                    chan []byte
	authRejectRecv              chan string
	authValidator               func(req *http.Request) int
	simulateAttachTimeout       bool
	simulateCorrelationMismatch bool
	closeRecv                   chan struct{}
	closed                      bool
}

// NewPreviewGateway 는 로컬 테스트용 Mock Preview Gateway 서버를 시작합니다.
func NewPreviewGateway() *PreviewGateway {
	g := &PreviewGateway{
		upgrader: websocket.Upgrader{
			CheckOrigin:  func(req *http.Request) bool { return true },
			Subprotocols: []string{protocol.SubprotocolPreviewData},
		},
		attachRecv:     make(chan protocol.PreviewAttachMessage, 10),
		binRecv:        make(chan []byte, 100),
		textRecv:       make(chan []byte, 10),
		authRejectRecv: make(chan string, 10),
		closeRecv:      make(chan struct{}, 10),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/connector/v1/preview-data", g.handleWebSocket)
	g.server = httptest.NewServer(mux)

	return g
}

// URL 은 WebSocket 엔드포인트 URL을 반환합니다.
func (g *PreviewGateway) URL() string {
	return strings.Replace(g.server.URL, "http://", "ws://", 1) + "/connector/v1/preview-data"
}

// Close 는 Mock 서버를 종료합니다.
func (g *PreviewGateway) Close() {
	g.mu.Lock()
	g.closed = true
	if g.conn != nil {
		_ = g.conn.Close()
	}
	g.mu.Unlock()
	g.server.Close()
}

// SetAuthValidator 는 WSS 업그레이드 전 HTTP 인증 검증 콜백을 설정합니다.
func (g *PreviewGateway) SetAuthValidator(fn func(req *http.Request) int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.authValidator = fn
}

// SetSimulateAttachTimeout 은 ATTACH 응답을 보내지 않고 무응답을 시뮬레이션할지 설정합니다.
func (g *PreviewGateway) SetSimulateAttachTimeout(simulate bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.simulateAttachTimeout = simulate
}

// SetSimulateCorrelationMismatch 는 응답 시 다른 generation/replyToMessageId를 반환할지 설정합니다.
func (g *PreviewGateway) SetSimulateCorrelationMismatch(simulate bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.simulateCorrelationMismatch = simulate
}

// WaitForAuthReject 는 특정 크리덴셜 거절(401/403) 이벤트가 발생할 때까지 대기합니다.
func (g *PreviewGateway) WaitForAuthReject(timeout time.Duration) (string, error) {
	select {
	case cred := <-g.authRejectRecv:
		return cred, nil
	case <-time.After(timeout):
		return "", errors.New("timeout waiting for auth rejection")
	}
}

// WaitForAttach 는 PREVIEW_ATTACH 수신을 대기합니다.
func (g *PreviewGateway) WaitForAttach(timeout time.Duration) (*protocol.PreviewAttachMessage, error) {
	select {
	case msg := <-g.attachRecv:
		return &msg, nil
	case <-time.After(timeout):
		return nil, errors.New("timeout waiting for attach message")
	}
}

// SendBinary 는 활성 연결로 바이너리 프레임을 전송합니다.
func (g *PreviewGateway) SendBinary(data []byte) error {
	g.mu.Lock()
	conn := g.conn
	g.mu.Unlock()
	if conn == nil {
		return errors.New("no active connection")
	}
	return conn.WriteMessage(websocket.BinaryMessage, data)
}

// WaitForBinary 는 클라이언트로부터 바이너리 수신을 대기합니다.
func (g *PreviewGateway) WaitForBinary(timeout time.Duration) ([]byte, error) {
	select {
	case b := <-g.binRecv:
		return b, nil
	case <-time.After(timeout):
		return nil, errors.New("timeout waiting for binary message")
	}
}

// SendText 는 활성 연결로 텍스트 프레임을 전송합니다.
func (g *PreviewGateway) SendText(data []byte) error {
	g.mu.Lock()
	conn := g.conn
	g.mu.Unlock()
	if conn == nil {
		return errors.New("no active connection")
	}
	return conn.WriteMessage(websocket.TextMessage, data)
}

// WaitForClose 는 클라이언트 측에서 연결이 닫힐 때까지 대기합니다.
func (g *PreviewGateway) WaitForClose(timeout time.Duration) error {
	select {
	case <-g.closeRecv:
		return nil
	case <-time.After(timeout):
		return errors.New("timeout waiting for connection close")
	}
}

// SendClose 는 클라이언트로 WebSocket CloseControl 프레임을 전송합니다.
func (g *PreviewGateway) SendClose(closeCode int, reason string) error {
	g.mu.Lock()
	conn := g.conn
	g.mu.Unlock()

	if conn == nil {
		return errors.New("no active connection")
	}

	return conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(closeCode, reason),
		time.Now().Add(time.Second),
	)
}

func (g *PreviewGateway) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	validator := g.authValidator
	g.mu.Unlock()

	if validator != nil {
		if code := validator(r); code != http.StatusOK {
			authHeader := r.Header.Get("Authorization")
			select {
			case g.authRejectRecv <- authHeader:
			default:
			}
			http.Error(w, "Unauthorized", code)
			return
		}
	}

	conn, err := g.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	g.mu.Lock()
	g.conn = conn
	simTimeout := g.simulateAttachTimeout
	simMismatch := g.simulateCorrelationMismatch
	g.mu.Unlock()

	defer func() {
		g.mu.Lock()
		if g.conn == conn {
			g.conn = nil
		}
		g.mu.Unlock()
		_ = conn.Close()
		select {
		case g.closeRecv <- struct{}{}:
		default:
		}
	}()

	// 1. 첫 메시지 PREVIEW_ATTACH 수신
	msgType, data, err := conn.ReadMessage()
	if err != nil {
		return
	}

	if msgType != websocket.TextMessage {
		return
	}

	var attachMsg protocol.PreviewAttachMessage
	if err := json.Unmarshal(data, &attachMsg); err != nil {
		return
	}

	// LBT-101 계약 검증: PREVIEW_ATTACH 프레임은 replyToMessageId가 필수 (없으면 1008 Policy Violation 거절)
	if attachMsg.ReplyToMessageID == "" {
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "replyToMessageId is required in PREVIEW_ATTACH"),
			time.Now().Add(time.Second),
		)
		return
	}

	g.mu.Lock()
	g.activeSess = attachMsg.PreviewSessionID
	g.activeLabID = attachMsg.LabInstanceID
	g.activeGen = attachMsg.Generation
	g.mu.Unlock()

	select {
	case g.attachRecv <- attachMsg:
	default:
	}

	if simTimeout {
		// 응답을 보내지 않고 대기하여 타임아웃을 유발
		time.Sleep(10 * time.Second)
		return
	}

	// 2. PREVIEW_ATTACHED 회신
	replyToID := attachMsg.MessageID
	generation := attachMsg.Generation
	if simMismatch {
		replyToID = "wrong-id"
		generation = 9999
	}

	attachedMsg := protocol.PreviewAttachedMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:             protocol.MessageTypePreviewAttached,
			MessageID:        "attached-msg-01",
			ReplyToMessageID: replyToID,
			SentAt:           time.Now().UTC(),
			PreviewSessionID: attachMsg.PreviewSessionID,
			LabInstanceID:    attachMsg.LabInstanceID,
			Generation:       generation,
		},
		Payload: protocol.PreviewAttachedPayload{},
	}
	attachedBytes, _ := json.Marshal(attachedMsg)
	if err := conn.WriteMessage(websocket.TextMessage, attachedBytes); err != nil {
		return
	}

	// 3. 메시지 루프
	for {
		mType, p, err := conn.ReadMessage()
		if err != nil {
			return
		}

		if mType == websocket.BinaryMessage {
			select {
			case g.binRecv <- p:
			default:
			}
		} else if mType == websocket.TextMessage {
			select {
			case g.textRecv <- p:
			default:
			}
		}
	}
}
