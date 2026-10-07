package preview

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

// ErrSessionConflict 는 동일 세션 ID로 기존과 다른 generation/target 요청이 들어왔을 때 반환됩니다.
var ErrSessionConflict = errors.New("preview: session conflict (stale generation or target mismatch)")

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

// GetOrCreateSession 은 동일한 previewSessionId 가 존재하면 기존 세션을 반환하고,
// 없으면 TCPForwarder 를 통해 VM 대상 포트에 연결하여 새 PreviewSession 을 등록합니다.
// attempt 멱등성 및 순차 터널 규칙:
// 1) 동일 openMessageId (envelope.MessageID)로 활성 터널이 존재하면 isRetry=true 반환 (재다이얼 없음).
// 2) 다른 openMessageId 인 경우 새 sequential 터널을 위해 새로 TCP 다이얼 후 바인딩 (isRetry=false).
// 3) Generation/Target 불일치 시 fail-closed (ErrSessionConflict) 반환.
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
		if existing.IsDestroyed() {
			sm.mu.Unlock()
			return nil, false, ErrSessionClosed
		}

		if existing.LabInstanceID != envelope.LabInstanceID ||
			existing.Generation != envelope.Generation ||
			existing.TargetVmKey != payload.TargetVmKey ||
			existing.ProviderServerID != payload.ProviderServerID ||
			existing.TargetPort != payload.TargetPort {
			sm.mu.Unlock()
			return nil, false, fmt.Errorf("%w: mismatch in existing session correlation/target", ErrSessionConflict)
		}

		// 동일 openMessageId (또는 envelope.MessageID가 비어있는 경우 활성 터널 존재) 재시도인 경우 멱등 성공 처리
		isSameAttempt := (envelope.MessageID == "" && existing.HasActiveTunnel()) || (envelope.MessageID != "" && existing.CurrentAttemptID() == envelope.MessageID && existing.HasActiveTunnel())
		if isSameAttempt {
			sm.mu.Unlock()
			return existing, true, nil
		}
	}
	sm.mu.Unlock()

	if sm.forwarder == nil {
		return nil, false, errors.New("tcp forwarder is not configured")
	}

	// Lock 외부에서 TCP 다이얼 수행
	conn, err := sm.forwarder.DialTCP(ctx, payload.TargetVmKey, payload.ProviderServerID, payload.TargetPort)
	if err != nil {
		return nil, false, err
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Double check
	if existing, ok := sm.sessions[sessionID]; ok {
		if existing.IsDestroyed() {
			_ = conn.Close()
			return nil, false, ErrSessionClosed
		}

		if existing.LabInstanceID != envelope.LabInstanceID ||
			existing.Generation != envelope.Generation ||
			existing.TargetVmKey != payload.TargetVmKey ||
			existing.ProviderServerID != payload.ProviderServerID ||
			existing.TargetPort != payload.TargetPort {
			_ = conn.Close()
			return nil, false, fmt.Errorf("%w: mismatch in existing session correlation/target", ErrSessionConflict)
		}

		isSameAttempt := (envelope.MessageID == "" && existing.HasActiveTunnel()) || (envelope.MessageID != "" && existing.CurrentAttemptID() == envelope.MessageID && existing.HasActiveTunnel())
		if isSameAttempt {
			_ = conn.Close()
			return existing, true, nil
		}

		if _, err := existing.BindTunnel(envelope.MessageID, conn); err != nil {
			_ = conn.Close()
			return nil, false, err
		}
		return existing, false, nil
	}

	session := NewPreviewSession(
		sessionID,
		envelope.LabInstanceID,
		envelope.Generation,
		payload.TargetVmKey,
		payload.ProviderServerID,
		payload.TargetPort,
		nil,
		func(s *PreviewSession, reason string, err error) {
			sm.mu.RLock()
			cb := sm.onEnded
			sm.mu.RUnlock()
			if cb != nil {
				cb(s, reason, err)
			}
		},
	)

	if _, err := session.BindTunnel(envelope.MessageID, conn); err != nil {
		_ = conn.Close()
		return nil, false, err
	}

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
	sm.mu.Lock()
	s, ok := sm.sessions[sessionID]
	if ok {
		delete(sm.sessions, sessionID)
	}
	sm.mu.Unlock()

	if !ok {
		return ErrSessionNotFound
	}
	s.Close(reason)
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
