package terminal

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

// SessionStatus 는 터미널 세션의 라이프사이클 상태입니다.
type SessionStatus string

const (
	StatusCreated  SessionStatus = "CREATED"
	StatusActive   SessionStatus = "ACTIVE"
	StatusDetached SessionStatus = "DETACHED" // Grace period 실행 중
	StatusClosed   SessionStatus = "CLOSED"
)

var (
	ErrSessionNotFound      = errors.New("terminal session not found")
	ErrSessionAlreadyClosed = errors.New("terminal session already closed")
	ErrStaleGeneration      = errors.New("stale generation drop")
	ErrSessionConflict      = errors.New("terminal session conflict")
)

// SessionEndedCallback 은 세션이 완전 종료되었을 때 호출되는 콜백입니다.
type SessionEndedCallback func(session *Session, reason string, exitCode *int, err error)

// Session 은 단일 터미널 세션(PTY + WebSocket 스트림)의 상태 및 라이프사이클을 관리합니다.
type Session struct {
	mu sync.Mutex

	SessionID        string
	LabInstanceID    string
	Generation       int64
	TargetVmKey      string
	ProviderServerID string
	Cols             int
	Rows             int

	Status   SessionStatus
	PTY      PTYChannel
	DataConn *websocket.Conn
	writeMu  sync.Mutex

	detachChan chan struct{}

	graceTimer    *time.Timer
	graceDuration time.Duration

	onEnded   SessionEndedCallback
	closeOnce sync.Once
	wasActive bool
}

// SessionManager 는 모든 활성 터미널 세션을 보관하는 Thread-safe 레지스트리입니다.
type SessionManager struct {
	mu            sync.RWMutex
	sessions      map[string]*Session
	graceDuration time.Duration
	onEnded       SessionEndedCallback
}

// NewSessionManager 는 지정된 Grace Period(기본 60초)를 갖는 세션 관리자를 생성합니다.
func NewSessionManager(graceDuration time.Duration, onEnded SessionEndedCallback) *SessionManager {
	if graceDuration <= 0 {
		graceDuration = 60 * time.Second
	}
	return &SessionManager{
		sessions:      make(map[string]*Session),
		graceDuration: graceDuration,
		onEnded:       onEnded,
	}
}

// SetOnEnded 는 세션 종료 콜백을 등록하거나 변경합니다.
func (sm *SessionManager) SetOnEnded(cb SessionEndedCallback) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.onEnded = cb
}

// GetOrCreateSession 은 동일한 terminalSessionId 가 존재하면 기존 세션을 반환(멱등성)하고,
// 없으면 새 PTY를 생성하여 세션을 등록합니다.
func (sm *SessionManager) GetOrCreateSession(
	payload protocol.TerminalOpenPayload,
	envelope protocol.BaseEnvelope,
	ptyFactory func() (PTYChannel, error),
) (*Session, bool, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sessionID := envelope.TerminalSessionID
	if sessionID == "" {
		sessionID = envelope.RequestID
	}
	if sessionID == "" {
		return nil, false, fmt.Errorf("missing terminalSessionId in request")
	}

	// 1. 기존 세션 존재 여부 확인 (멱등성 및 충돌 검증)
	if existing, exists := sm.sessions[sessionID]; exists {
		existing.mu.Lock()
		defer existing.mu.Unlock()

		// Generation 및 대상 타겟 시맨틱 일치성 검증
		if existing.Generation != envelope.Generation ||
			existing.LabInstanceID != envelope.LabInstanceID ||
			existing.TargetVmKey != payload.TargetVmKey ||
			existing.ProviderServerID != payload.ProviderServerID {
			if envelope.Generation < existing.Generation {
				return nil, false, fmt.Errorf("%w: current=%d, got=%d", ErrStaleGeneration, existing.Generation, envelope.Generation)
			}
			return nil, false, fmt.Errorf("%w: cannot reuse session %q with different generation or target (existing gen=%d target=%s server=%s, got gen=%d target=%s server=%s)",
				ErrSessionConflict, sessionID, existing.Generation, existing.TargetVmKey, existing.ProviderServerID,
				envelope.Generation, payload.TargetVmKey, payload.ProviderServerID)
		}

		if existing.Status != StatusClosed {
			// 이미 유효한 세션이 존재하므로 재사용 (reused = true)
			return existing, true, nil
		}
		// 이미 CLOSED 된 세션이면 맵에서 정리 후 새로 생성
		delete(sm.sessions, sessionID)
	}

	// 2. 신규 PTY 생성
	pty, err := ptyFactory()
	if err != nil {
		return nil, false, fmt.Errorf("failed to allocate PTY: %w", err)
	}

	session := &Session{
		SessionID:        sessionID,
		LabInstanceID:    envelope.LabInstanceID,
		Generation:       envelope.Generation,
		TargetVmKey:      payload.TargetVmKey,
		ProviderServerID: payload.ProviderServerID,
		Cols:             payload.Cols,
		Rows:             payload.Rows,
		Status:           StatusCreated,
		PTY:              pty,
		detachChan:       make(chan struct{}),
		graceDuration:    sm.graceDuration,
		onEnded: func(s *Session, reason string, exitCode *int, sessionErr error) {
			sm.removeSession(s.SessionID)
			sm.mu.RLock()
			cb := sm.onEnded
			sm.mu.RUnlock()
			if cb != nil {
				cb(s, reason, exitCode, sessionErr)
			}
		},
	}

	session.startPtyPump()
	sm.sessions[sessionID] = session
	return session, false, nil
}

// GetSession 은 ID로 활성 세션을 조회합니다.
func (sm *SessionManager) GetSession(sessionID string) (*Session, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	s, exists := sm.sessions[sessionID]
	return s, exists
}

// CloseSession 은 특정 세션을 명시적으로 종료합니다.
func (sm *SessionManager) CloseSession(sessionID string, reason string) error {
	session, exists := sm.GetSession(sessionID)
	if !exists {
		return ErrSessionNotFound
	}
	session.Close(reason, nil, nil)
	return nil
}

// CloseAll 은 관리 중인 모든 세션을 종료합니다.
func (sm *SessionManager) CloseAll(reason string) {
	sm.mu.Lock()
	sessionsToClose := make([]*Session, 0, len(sm.sessions))
	for _, s := range sm.sessions {
		sessionsToClose = append(sessionsToClose, s)
	}
	sm.sessions = make(map[string]*Session)
	sm.mu.Unlock()

	for _, s := range sessionsToClose {
		s.Close(reason, nil, nil)
	}
}

// ActiveCount 는 현재 등록된 세션 수를 반환합니다.
func (sm *SessionManager) ActiveCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.sessions)
}

func (sm *SessionManager) removeSession(sessionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.sessions, sessionID)
}

// AttachDataConn 은 WebSocket 데이터 연결을 세션에 바인딩합니다.
// 이전에 Detached 상태(Grace period 중)였다면 재연결(resumed = true)로 처리하고 타이머를 취소합니다.
func (s *Session) AttachDataConn(conn *websocket.Conn) (resumed bool) {
	s.mu.Lock()
	if s.Status == StatusClosed {
		s.mu.Unlock()
		return false
	}

	// 기존 Grace Period 타이머가 돌고 있다면 정지
	if s.graceTimer != nil {
		s.graceTimer.Stop()
		s.graceTimer = nil
	}

	if s.Status == StatusDetached {
		resumed = true
	} else {
		resumed = false
	}

	oldConn := s.DataConn
	s.DataConn = conn
	s.Status = StatusActive
	s.wasActive = true
	s.detachChan = make(chan struct{})
	s.mu.Unlock()

	// 이전 활성 연결이 남아있다면 s.mu 밖에서 안전하게 닫기
	if oldConn != nil && oldConn != conn {
		s.writeMu.Lock()
		_ = oldConn.Close()
		s.writeMu.Unlock()
	}

	return resumed
}

// WasActive 는 세션이 한 번이라도 활성화(Data WSS attach 완료)되었는지 여부를 반환합니다.
func (s *Session) WasActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wasActive
}

// DetachDataConn 은 WebSocket 데이터 연결이 비정상 종료되거나 닫혔을 때 호출됩니다.
// conn 파라미터가 제공된 경우 현재 활성 연결과 일치할 때만 분리를 수행합니다.
func (s *Session) DetachDataConn(conn *websocket.Conn) {
	s.mu.Lock()
	if conn != nil && s.DataConn != conn {
		s.mu.Unlock()
		return
	}

	if s.Status != StatusActive {
		s.mu.Unlock()
		return
	}

	s.Status = StatusDetached
	oldConn := s.DataConn
	s.DataConn = nil

	// detachChan 시그널링으로 진행 중인 스트리밍 고루틴 중단
	select {
	case <-s.detachChan:
	default:
		close(s.detachChan)
	}

	// Grace Period 타이머 시작
	s.graceTimer = time.AfterFunc(s.graceDuration, func() {
		s.mu.Lock()
		if s.Status == StatusDetached {
			s.mu.Unlock()
			// 60초 경과: PTY 및 세션 완전 정리
			s.Close(protocol.TerminalReasonGraceTimeout, nil, nil)
			return
		}
		s.mu.Unlock()
	})
	s.mu.Unlock()

	if oldConn != nil {
		s.writeMu.Lock()
		_ = oldConn.Close()
		s.writeMu.Unlock()
	}
}

// Close 는 세션을 완전히 종료하고 리소스를 해제합니다.
func (s *Session) Close(reason string, exitCode *int, err error) {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.Status = StatusClosed

		if s.graceTimer != nil {
			s.graceTimer.Stop()
			s.graceTimer = nil
		}

		select {
		case <-s.detachChan:
		default:
			close(s.detachChan)
		}

		conn := s.DataConn
		s.DataConn = nil

		pty := s.PTY
		s.PTY = nil

		callback := s.onEnded
		s.mu.Unlock()

		if conn != nil {
			endedMsg := protocol.TerminalDataEndedMessage{
				BaseEnvelope: protocol.BaseEnvelope{
					Type:              protocol.MessageTypeTerminalDataEnded,
					MessageID:         generateUUID(),
					SentAt:            time.Now().UTC(),
					TerminalSessionID: s.SessionID,
					LabInstanceID:     s.LabInstanceID,
					Generation:        s.Generation,
				},
				Payload: protocol.TerminalDataEndedPayload{
					Reason:   reason,
					ExitCode: exitCode,
				},
			}
			if err != nil {
				endedMsg.Payload.Error = &protocol.SafeError{
					Code:    "SESSION_ERROR",
					Message: err.Error(),
				}
			}
			s.writeMu.Lock()
			_ = conn.WriteJSON(endedMsg)
			_ = conn.Close()
			s.writeMu.Unlock()
		}

		if pty != nil {
			_ = pty.Close()
		}

		if callback != nil {
			callback(s, reason, exitCode, err)
		}
	})
}

// WriteMessage 는 활성 WebSocket 연결로 메시지를 단일 뮤텍스 직렬화하여 전송합니다.
// s.mu 와 s.writeMu 를 중첩 획득하지 않고, s.mu 아래에서 상태/conn 을 스냅샷 후 s.writeMu 에서 전송합니다.
func (s *Session) WriteMessage(messageType int, data []byte) error {
	s.mu.Lock()
	conn := s.DataConn
	status := s.Status
	s.mu.Unlock()

	if status != StatusActive || conn == nil {
		return net.ErrClosed
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	return conn.WriteMessage(messageType, data)
}

// WriteJSON 은 활성 WebSocket 연결로 JSON 메시지를 단일 뮤텍스 직렬화하여 전송합니다.
// s.mu 와 s.writeMu 를 중첩 획득하지 않고, s.mu 아래에서 상태/conn 을 스냅샷 후 s.writeMu 에서 전송합니다.
func (s *Session) WriteJSON(v interface{}) error {
	s.mu.Lock()
	conn := s.DataConn
	status := s.Status
	s.mu.Unlock()

	if status != StatusActive || conn == nil {
		return net.ErrClosed
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	return conn.WriteJSON(v)
}

// Resize 는 터미널 크기를 변경합니다.
func (s *Session) Resize(cols, rows int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Status == StatusClosed {
		return ErrSessionAlreadyClosed
	}

	s.Cols = cols
	s.Rows = rows
	if s.PTY != nil {
		return s.PTY.Resize(cols, rows)
	}
	return nil
}

// DetachChan 은 현재 연결의 분리 시그널 채널을 반환합니다.
func (s *Session) DetachChan() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.detachChan
}

// startPtyPump 는 PTY의 출력을 읽어 현재 활성인 DataConn으로 전송하는 단일 고루틴을 실행합니다.
func (s *Session) startPtyPump() {
	go func() {
		buf := make([]byte, 4096)
		for {
			s.mu.Lock()
			if s.Status == StatusClosed {
				s.mu.Unlock()
				return
			}
			pty := s.PTY
			s.mu.Unlock()

			if pty == nil {
				return
			}

			n, err := pty.Read(buf)
			if err != nil {
				if errors.Is(err, io.EOF) {
					var exitCode *int
					if esp, ok := pty.(ExitStatusProvider); ok {
						for i := 0; i < 5; i++ {
							if code, exited := esp.ExitStatus(); exited {
								exitCode = code
								break
							}
							time.Sleep(10 * time.Millisecond)
						}
					}
					s.Close(protocol.TerminalReasonPtyExited, exitCode, nil)
				} else {
					// SSH disconnect 또는 non-EOF I/O 오류 발생 시 세션을 ACTIVE로 방치하지 않고 확실히 종료
					reason := protocol.TerminalReasonSSHDisconnected
					s.Close(reason, nil, err)
				}
				return
			}

			if n > 0 {
				_ = s.WriteMessage(websocket.BinaryMessage, buf[:n])
			}
		}
	}()
}
