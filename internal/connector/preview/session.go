package preview

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
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
	ErrSessionNotFound      = errors.New("preview session not found")
	ErrSessionAlreadyClosed = errors.New("preview session already closed")
	ErrSessionClosed        = errors.New("preview session is closed")
)

// PreviewEndedCallback 은 프리뷰 세션이 종료되었을 때 호출되는 콜백입니다.
type PreviewEndedCallback func(session *PreviewSession, reason string, err error)

// PreviewSession 은 단일 웹 프리뷰 터널(VM TCP 포트 ↔ SaaS Preview Data WSS)을 관리합니다.
type PreviewSession struct {
	mu sync.Mutex

	SessionID        string
	LabInstanceID    string
	Generation       int64
	TargetVmKey      string
	ProviderServerID string
	Port             int

	Status   SessionStatus
	TCPConn  net.Conn
	DataConn *websocket.Conn
	writeMu  sync.Mutex

	done      chan struct{}
	closeOnce sync.Once
	onEnded   PreviewEndedCallback
}

// NewPreviewSession 은 지정된 식별자와 TCP 연결을 갖는 PreviewSession 을 생성합니다.
func NewPreviewSession(
	sessionID, labInstanceID string,
	generation int64,
	targetVmKey, serverID string,
	port int,
	tcpConn net.Conn,
	onEnded PreviewEndedCallback,
) *PreviewSession {
	return &PreviewSession{
		SessionID:        sessionID,
		LabInstanceID:    labInstanceID,
		Generation:       generation,
		TargetVmKey:      targetVmKey,
		ProviderServerID: serverID,
		Port:             port,
		Status:           StatusConnecting,
		TCPConn:          tcpConn,
		done:             make(chan struct{}),
		onEnded:          onEnded,
	}
}

// GetStatus 는 현재 세션의 상태를 안전하게 반환합니다.
func (s *PreviewSession) GetStatus() SessionStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Status
}

// AttachDataConn 은 SaaS Preview Data WSS 연결을 세션에 바인딩하고 양방향 프록시를 시작합니다.
func (s *PreviewSession) AttachDataConn(conn *websocket.Conn) error {
	s.mu.Lock()
	if s.Status == StatusClosed {
		s.mu.Unlock()
		if conn != nil {
			_ = conn.Close()
		}
		return ErrSessionClosed
	}

	s.DataConn = conn
	s.Status = StatusActive
	s.mu.Unlock()

	go s.pipeTCPToWSS()
	go s.pipeWSSToTCP()

	return nil
}

// pipeTCPToWSS 는 VM TCP 포트에서 읽은 바이트를 WebSocket Binary Frame으로 SaaS Gateway에 전달합니다.
func (s *PreviewSession) pipeTCPToWSS() {
	buf := make([]byte, 32*1024)
	for {
		select {
		case <-s.done:
			return
		default:
		}

		n, err := s.TCPConn.Read(buf)
		if n > 0 {
			s.writeMu.Lock()
			writeErr := s.DataConn.WriteMessage(websocket.BinaryMessage, buf[:n])
			s.writeMu.Unlock()
			if writeErr != nil {
				s.Close(protocol.PreviewReasonSessionClosed)
				return
			}
		}

		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				s.Close(protocol.PreviewReasonTargetClosed)
			} else {
				s.Close(protocol.PreviewReasonPortUnreachable)
			}
			return
		}
	}
}

// pipeWSSToTCP 는 SaaS Gateway에서 수신한 바이너리 데이터를 VM TCP 포트로 기록하고, 제어 메시지를 처리합니다.
func (s *PreviewSession) pipeWSSToTCP() {
	for {
		select {
		case <-s.done:
			return
		default:
		}

		msgType, r, err := s.DataConn.NextReader()
		if err != nil {
			s.Close(protocol.PreviewReasonSessionClosed)
			return
		}

		switch msgType {
		case websocket.BinaryMessage:
			if _, err := io.Copy(s.TCPConn, r); err != nil {
				s.Close(protocol.PreviewReasonPortUnreachable)
				return
			}
		case websocket.TextMessage:
			// 1 MiB bounded read
			data, err := io.ReadAll(io.LimitReader(r, protocol.MaxJSONMessageSize+1))
			if err != nil || int64(len(data)) > protocol.MaxJSONMessageSize {
				s.writeMu.Lock()
				_ = s.DataConn.WriteControl(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(protocol.CloseMessageTooBig, "message too big"),
					time.Now().Add(time.Second),
				)
				s.writeMu.Unlock()
				s.Close(protocol.PreviewErrProtocolError)
				return
			}

			var env protocol.BaseEnvelope
			if err := json.Unmarshal(data, &env); err == nil {
				if env.Type == protocol.MessageTypePreviewDataClose {
					var closeMsg protocol.PreviewDataCloseMessage
					if err := json.Unmarshal(data, &closeMsg); err == nil && closeMsg.Payload.Reason != "" {
						s.Close(closeMsg.Payload.Reason)
					} else {
						s.Close(protocol.PreviewReasonSessionClosed)
					}
					return
				}
			}
		case websocket.CloseMessage:
			s.Close(protocol.PreviewReasonSessionClosed)
			return
		}
	}
}

// Close 는 세션을 닫고 리소스를 정리합니다 (멱등성 보장).
func (s *PreviewSession) Close(reason string) {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.Status = StatusClosed
		close(s.done)

		if s.TCPConn != nil {
			_ = s.TCPConn.Close()
		}

		if s.DataConn != nil {
			s.writeMu.Lock()
			endedMsg := protocol.PreviewDataEndedMessage{
				BaseEnvelope: protocol.BaseEnvelope{
					Type:             protocol.MessageTypePreviewDataEnded,
					MessageID:        uuid.NewString(),
					SentAt:           time.Now().UTC(),
					PreviewSessionID: s.SessionID,
					LabInstanceID:    s.LabInstanceID,
					Generation:       s.Generation,
				},
				Payload: protocol.PreviewDataEndedPayload{
					Reason: reason,
				},
			}
			if data, err := json.Marshal(endedMsg); err == nil {
				_ = s.DataConn.WriteMessage(websocket.TextMessage, data)
			}
			_ = s.DataConn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, reason),
				time.Now().Add(time.Second),
			)
			_ = s.DataConn.Close()
			s.writeMu.Unlock()
		}
		s.mu.Unlock()

		if s.onEnded != nil {
			s.onEnded(s, reason, nil)
		}
	})
}
