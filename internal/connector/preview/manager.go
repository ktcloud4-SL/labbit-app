package preview

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

// SessionManager 는 모든 활성 프리뷰 세션을 관리하는 Thread-safe 레지스트리입니다.
type SessionManager struct {
	mu        sync.RWMutex
	sessions  map[string]*PreviewSession
	forwarder TCPForwarder
	onEnded   PreviewEndedCallback
}

// NewSessionManager 는 포워더와 종료 콜백을 주입받아 SessionManager 를 초기화합니다.
func NewSessionManager(forwarder TCPForwarder, onEnded PreviewEndedCallback) *SessionManager {
	return &SessionManager{
		sessions:  make(map[string]*PreviewSession),
		forwarder: forwarder,
		onEnded:   onEnded,
	}
}

// SetOnEnded 는 세션 종료 콜백을 등록하거나 변경합니다.
func (sm *SessionManager) SetOnEnded(cb PreviewEndedCallback) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.onEnded = cb
}

// GetOrCreateSession 은 동일한 previewSessionId 가 존재하면 기존 세션을 반환하고(멱등성),
// 없으면 TCPForwarder 를 통해 VM 대상 포트에 연결하여 새 PreviewSession 을 등록합니다.
func (sm *SessionManager) GetOrCreateSession(
	ctx context.Context,
	payload protocol.PreviewOpenPayload,
	envelope protocol.BaseEnvelope,
) (*PreviewSession, bool, error) {
	sessionID := envelope.PreviewSessionID
	if sessionID == "" {
		return nil, false, errors.New("previewSessionId is required")
	}

	sm.mu.Lock()
	if existing, ok := sm.sessions[sessionID]; ok {
		if existing.GetStatus() != StatusClosed {
			sm.mu.Unlock()
			return existing, true, nil
		}
	}
	sm.mu.Unlock()

	if sm.forwarder == nil {
		return nil, false, errors.New("tcp forwarder is not configured")
	}

	// Lock 외부에서 TCP 다이얼 수행
	conn, err := sm.forwarder.DialTCP(ctx, payload.TargetVmKey, payload.ProviderServerID, payload.Port)
	if err != nil {
		return nil, false, fmt.Errorf("failed to dial target VM port %d: %w", payload.Port, err)
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Double check
	if existing, ok := sm.sessions[sessionID]; ok && existing.GetStatus() != StatusClosed {
		_ = conn.Close()
		return existing, true, nil
	}

	session := NewPreviewSession(
		sessionID,
		envelope.LabInstanceID,
		envelope.Generation,
		payload.TargetVmKey,
		payload.ProviderServerID,
		payload.Port,
		conn,
		func(s *PreviewSession, reason string, err error) {
			sm.removeSession(s.SessionID)
			sm.mu.RLock()
			cb := sm.onEnded
			sm.mu.RUnlock()
			if cb != nil {
				cb(s, reason, err)
			}
		},
	)

	sm.sessions[sessionID] = session
	return session, false, nil
}

// GetSession 은 식별자로 세션을 조회합니다.
func (sm *SessionManager) GetSession(sessionID string) (*PreviewSession, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	s, ok := sm.sessions[sessionID]
	return s, ok
}

// CloseSession 은 특정 세션을 닫고 레지스트리에서 제거합니다.
func (sm *SessionManager) CloseSession(sessionID string, reason string) error {
	sm.mu.RLock()
	s, ok := sm.sessions[sessionID]
	sm.mu.RUnlock()

	if !ok {
		return ErrSessionNotFound
	}
	s.Close(reason)
	sm.removeSession(sessionID)
	return nil
}

// CloseAll 은 런타임 종료 시 모든 활성 세션을 닫고 정리합니다.
func (sm *SessionManager) CloseAll(reason string) {
	sm.mu.Lock()
	list := make([]*PreviewSession, 0, len(sm.sessions))
	for _, s := range sm.sessions {
		list = append(list, s)
	}
	sm.sessions = make(map[string]*PreviewSession)
	sm.mu.Unlock()

	for _, s := range list {
		s.Close(reason)
	}
}

// Count 는 현재 관리 중인 세션 개수를 반환합니다.
func (sm *SessionManager) Count() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.sessions)
}

func (sm *SessionManager) removeSession(sessionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.sessions, sessionID)
}
