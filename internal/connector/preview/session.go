package preview

import (
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

// SessionStatus 는 프리뷰 세션의 라이프사이클 상태입니다.
type SessionStatus string

const (
	StatusConnecting SessionStatus = "CONNECTING"
	StatusActive     SessionStatus = "ACTIVE"
	StatusClosed     SessionStatus = "CLOSED"
)

var (
	ErrSessionNotFound        = errors.New("preview session not found")
	ErrSessionAlreadyClosed   = errors.New("preview session already closed")
	ErrSessionClosed          = errors.New("preview session is closed")
	ErrSessionAlreadyAttached = errors.New("preview session already attached")
	ErrNoActiveTunnel         = errors.New("preview session has no active tunnel")
)

// PreviewEndedCallback 은 프리뷰 세션 또는 터널이 종료되었을 때 호출되는 콜백입니다.
type PreviewEndedCallback func(session *PreviewSession, reason string, err error)

// PreviewTunnel 은 단일 TCP 연결(VM application port)과 SaaS Preview Data WSS 간의 양방향 바이트 스트리밍 터널입니다.
// 1 PreviewSession 은 0..N 개의 sequential PreviewTunnel 을 가질 수 있습니다 (LBT-101 / §7b).
type PreviewTunnel struct {
	openMessageID string
	tcpConn       net.Conn
	dataConn      *websocket.Conn
	writeMu       sync.Mutex
	done          chan struct{}
	closeOnce     sync.Once
	onClosed      func(reason string, err error)
}

// Close 는 터널(TCP 연결 및 Data WSS 연결)을 닫고 정리합니다.
func (t *PreviewTunnel) Close(reason string) {
	t.closeWithReason(reason, nil)
}

func (t *PreviewTunnel) closeWithReason(reason string, closeErr error) {
	t.closeOnce.Do(func() {
		close(t.done)

		if t.tcpConn != nil {
			_ = t.tcpConn.Close()
		}

		if t.dataConn != nil {
			t.writeMu.Lock()
			_ = t.dataConn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, reason),
				time.Now().Add(time.Second),
			)
			_ = t.dataConn.Close()
			t.writeMu.Unlock()
		}

		if t.onClosed != nil {
			t.onClosed(reason, closeErr)
		}
	})
}

// pipeTCPToWSS 는 VM TCP 포트에서 읽은 바이트를 WebSocket Binary Frame으로 SaaS Gateway에 전달합니다.
func (t *PreviewTunnel) pipeTCPToWSS() {
	buf := make([]byte, 32*1024)
	for {
		select {
		case <-t.done:
			return
		default:
		}

		n, err := t.tcpConn.Read(buf)
		if n > 0 {
			t.writeMu.Lock()
			writeErr := t.dataConn.WriteMessage(websocket.BinaryMessage, buf[:n])
			t.writeMu.Unlock()
			if writeErr != nil {
				t.Close(protocol.PreviewReasonSessionClosed)
				return
			}
		}

		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				t.Close(protocol.PreviewReasonTargetClosed)
			} else {
				t.Close(protocol.PreviewErrorCodeVMUnreachable)
			}
			return
		}
	}
}

// pipeWSSToTCP 는 SaaS Gateway에서 수신한 바이너리 데이터를 VM TCP 포트로 기록합니다.
// PREVIEW_ATTACHED 이후에는 순수 Binary Frame만 허용되며, Text Frame 수신은 1008 프로토콜 위반으로 종료합니다.
func (t *PreviewTunnel) pipeWSSToTCP() {
	for {
		select {
		case <-t.done:
			return
		default:
		}

		msgType, r, err := t.dataConn.NextReader()
		if err != nil {
			var closeErr *websocket.CloseError
			if errors.As(err, &closeErr) && closeErr.Text != "" {
				t.Close(closeErr.Text)
			} else {
				t.Close(protocol.PreviewReasonSessionClosed)
			}
			return
		}

		switch msgType {
		case websocket.BinaryMessage:
			if _, err := io.Copy(t.tcpConn, r); err != nil {
				t.Close(protocol.PreviewErrorCodeVMUnreachable)
				return
			}
		case websocket.TextMessage:
			// LBT-101 계약: PREVIEW_ATTACHED 이후 JSON Text frame은 프로토콜 위반 (Close 1008)
			t.writeMu.Lock()
			_ = t.dataConn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "text frame is not allowed after attach"),
				time.Now().Add(time.Second),
			)
			t.writeMu.Unlock()
			t.Close(protocol.PreviewErrProtocolError)
			return
		case websocket.CloseMessage:
			t.Close(protocol.PreviewReasonSessionClosed)
			return
		}
	}
}

// PreviewSession 은 logical PreviewSession 을 관리합니다.
type PreviewSession struct {
	mu sync.Mutex

	SessionID        string
	LabInstanceID    string
	Generation       int64
	TargetVmKey      string
	ProviderServerID string
	TargetPort       int

	Status           SessionStatus
	currentAttemptID string
	activeTunnel     *PreviewTunnel
	destroyed        bool

	// 하위 호환성 필드: 현재 활성 터널의 커넥션 참조
	TCPConn  net.Conn
	DataConn *websocket.Conn

	closeOnce sync.Once
	onEnded   PreviewEndedCallback
}

// NewPreviewSession 은 logical PreviewSession 을 생성합니다.
// tcpConn 이 전달되면 초기 터널로 자동 바인딩합니다.
func NewPreviewSession(
	sessionID, labInstanceID string,
	generation int64,
	targetVmKey, serverID string,
	targetPort int,
	tcpConn net.Conn,
	onEnded PreviewEndedCallback,
) *PreviewSession {
	s := &PreviewSession{
		SessionID:        sessionID,
		LabInstanceID:    labInstanceID,
		Generation:       generation,
		TargetVmKey:      targetVmKey,
		ProviderServerID: serverID,
		TargetPort:       targetPort,
		Status:           StatusActive,
		onEnded:          onEnded,
	}
	if tcpConn != nil {
		_, _ = s.BindTunnel("", tcpConn)
	}
	return s
}

// GetStatus 는 현재 세션의 상태를 안전하게 반환합니다.
func (s *PreviewSession) GetStatus() SessionStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Status
}

// IsDestroyed 는 세션이 완전히 파기(종료)되었는지 여부를 반환합니다.
func (s *PreviewSession) IsDestroyed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.destroyed
}

// CurrentAttemptID 는 현재 바인딩된 터널의 openMessageID를 반환합니다.
func (s *PreviewSession) CurrentAttemptID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentAttemptID
}

// HasActiveTunnel 은 현재 활성(열려있는) 터널이 존재하는지 여부를 반환합니다.
func (s *PreviewSession) HasActiveTunnel() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeTunnel != nil
}

// BindTunnel 은 새로운 TCP 커넥션을 터널로 세션에 바인딩합니다.
// 이전 활성 터널이 존재하면 새 터널을 위해 정상 종료합니다.
func (s *PreviewSession) BindTunnel(openMessageID string, tcpConn net.Conn) (*PreviewTunnel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.destroyed {
		if tcpConn != nil {
			_ = tcpConn.Close()
		}
		return nil, ErrSessionClosed
	}

	if s.activeTunnel != nil {
		old := s.activeTunnel
		s.activeTunnel = nil
		old.onClosed = nil
		go old.Close(protocol.PreviewReasonTargetClosed)
	}

	t := &PreviewTunnel{
		openMessageID: openMessageID,
		tcpConn:       tcpConn,
		done:          make(chan struct{}),
	}
	t.onClosed = func(reason string, err error) {
		s.onTunnelFinished(t, reason, err)
	}

	s.activeTunnel = t
	s.currentAttemptID = openMessageID
	s.TCPConn = tcpConn
	s.DataConn = nil
	s.Status = StatusActive

	return t, nil
}

func (s *PreviewSession) onTunnelFinished(t *PreviewTunnel, reason string, err error) {
	s.mu.Lock()
	if s.activeTunnel == t {
		s.activeTunnel = nil
		s.TCPConn = nil
		s.DataConn = nil
		s.Status = StatusClosed
	}
	isDestroyed := s.destroyed
	cb := s.onEnded
	s.mu.Unlock()

	if isDestroyed {
		return
	}

	if cb != nil {
		cb(s, reason, err)
	}
}

// AttachDataConn 은 SaaS Preview Data WSS 연결을 현재 활성 터널에 바인딩하고 프록시 스트리밍을 시작합니다.
func (s *PreviewSession) AttachDataConn(conn *websocket.Conn) error {
	s.mu.Lock()
	if s.destroyed {
		s.mu.Unlock()
		if conn != nil {
			_ = conn.Close()
		}
		return ErrSessionClosed
	}

	t := s.activeTunnel
	if t == nil {
		s.mu.Unlock()
		if conn != nil {
			_ = conn.Close()
		}
		return ErrNoActiveTunnel
	}

	if t.dataConn != nil {
		s.mu.Unlock()
		if conn != nil {
			_ = conn.Close()
		}
		return ErrSessionAlreadyAttached
	}

	t.dataConn = conn
	s.DataConn = conn
	s.Status = StatusActive
	s.mu.Unlock()

	go t.pipeTCPToWSS()
	go t.pipeWSSToTCP()

	return nil
}

// CloseCurrentTunnel 은 현재 활성 터널만 종료하고 세션 자체는 유지합니다 (순차 터널 지원).
func (s *PreviewSession) CloseCurrentTunnel(reason string) {
	s.mu.Lock()
	t := s.activeTunnel
	s.mu.Unlock()

	if t != nil {
		t.Close(reason)
	}
}

// Close 는 logical PreviewSession 과 활성 터널을 완전히 닫고 리소스를 정리합니다 (멱등성 보장).
func (s *PreviewSession) Close(reason string) {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.destroyed = true
		s.Status = StatusClosed
		t := s.activeTunnel
		s.activeTunnel = nil
		s.TCPConn = nil
		s.DataConn = nil
		cb := s.onEnded
		s.mu.Unlock()

		if t != nil {
			t.onClosed = nil
			t.Close(reason)
		}

		if cb != nil {
			cb(s, reason, nil)
		}
	})
}
