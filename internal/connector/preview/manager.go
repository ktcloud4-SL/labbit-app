package preview

import (
	"context"
	"errors"
	"sync"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
)

var ErrSessionConflict = errors.New("preview: session conflict (stale generation, target or active attempt)")

// SessionManager owns pending opens as well as attached tunnels. Network IO
// runs outside its lock, so Close can cancel a dial without waiting for its peer.
type SessionManager struct {
	mu                sync.RWMutex
	sessions          map[string]*PreviewSession
	minimumGeneration map[string]int64
	forwarder         TCPForwarder
	onEnded           PreviewEndedCallback
	stopped           bool
}

func NewSessionManager(forwarder TCPForwarder, onEnded PreviewEndedCallback) *SessionManager {
	return &SessionManager{sessions: make(map[string]*PreviewSession), minimumGeneration: make(map[string]int64), forwarder: forwarder, onEnded: onEnded}
}
func (sm *SessionManager) SetOnEnded(cb PreviewEndedCallback) {
	sm.mu.Lock()
	sm.onEnded = cb
	sm.mu.Unlock()
}

type OpenAttempt struct {
	manager *SessionManager
	session *PreviewSession
	tunnel  *PreviewTunnel
	retry   bool
	env     protocol.BaseEnvelope
	payload protocol.PreviewOpenPayload
}

// PrepareOpen reserves synchronously in Control receive order. Open may do
// network IO in a worker while the reader continues to process CLOSE.
func (sm *SessionManager) PrepareOpen(ctx context.Context, payload protocol.PreviewOpenPayload, env protocol.BaseEnvelope) (*OpenAttempt, error) {
	if env.PreviewSessionID == "" {
		return nil, errors.New("previewSessionId is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sm.mu.Lock()
	if sm.stopped || env.Generation < sm.minimumGeneration[env.LabInstanceID] {
		sm.mu.Unlock()
		return nil, ErrSessionClosed
	}
	s, exists := sm.sessions[env.PreviewSessionID]
	if exists && (s.LabInstanceID != env.LabInstanceID || s.Generation != env.Generation || s.TargetVmKey != payload.TargetVmKey || s.ProviderServerID != payload.ProviderServerID || s.TargetPort != payload.TargetPort) {
		sm.mu.Unlock()
		return nil, ErrSessionConflict
	}
	if !exists {
		s = NewPreviewSession(env.PreviewSessionID, env.LabInstanceID, env.Generation, payload.TargetVmKey, payload.ProviderServerID, payload.TargetPort, nil, func(s *PreviewSession, reason string, err error) {
			sm.mu.Lock()
			if s.IsDestroyed() && sm.sessions[s.SessionID] == s {
				delete(sm.sessions, s.SessionID)
			}
			cb := sm.onEnded
			sm.mu.Unlock()
			if cb != nil {
				cb(s, reason, err)
			}
		})
		sm.sessions[s.SessionID] = s
	}
	tunnel, retry, err := s.reserveTunnel(env.MessageID)
	sm.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &OpenAttempt{manager: sm, session: s, tunnel: tunnel, retry: retry, env: env, payload: payload}, nil
}
func (sm *SessionManager) GetOrCreateSession(ctx context.Context, payload protocol.PreviewOpenPayload, env protocol.BaseEnvelope) (*PreviewSession, bool, error) {
	a, err := sm.PrepareOpen(ctx, payload, env)
	if err != nil {
		return nil, false, err
	}
	return a.Open(ctx)
}
func (a *OpenAttempt) Open(ctx context.Context) (*PreviewSession, bool, error) {
	sm, s, tunnel, retry, env, payload := a.manager, a.session, a.tunnel, a.retry, a.env, a.payload
	var err error
	if retry {
		select {
		case <-ctx.Done():
			return nil, true, ctx.Err()
		case <-tunnel.ready:
			if err := tunnel.dialResult(); err != nil {
				return nil, true, err
			}
			return s, true, nil
		case <-tunnel.done:
			return nil, true, ErrSessionClosed
		}
	}
	dialCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(tunnel.ctx, cancel)
	defer stop()
	defer cancel()
	if sm.forwarder == nil {
		err = errors.New("tcp forwarder is not configured")
	} else {
		conn, dialErr := sm.forwarder.DialTCP(dialCtx, vmaccess.Target{LabInstanceID: env.LabInstanceID, Generation: env.Generation, VMKey: payload.TargetVmKey, ServerID: payload.ProviderServerID}, payload.TargetPort)
		err = tunnel.completeDial(conn, dialErr)
	}
	if err == nil {
		err = dialCtx.Err()
	}
	if err != nil {
		tunnel.closeWithReason(protocol.PreviewErrorCodeUnavailable, err)
		sm.discardUnattached(s, env.MessageID)
		return nil, false, err
	}
	return s, false, nil
}

// A delayed failure cannot discard a newer attempt or an established session.
func (sm *SessionManager) discardUnattached(s *PreviewSession, attempt string) {
	sm.mu.Lock()
	s.mu.Lock()
	if sm.sessions[s.SessionID] == s && !s.everAttached && s.currentAttemptID == attempt && s.activeTunnel == nil {
		s.destroyed = true
		delete(sm.sessions, s.SessionID)
	}
	s.mu.Unlock()
	sm.mu.Unlock()
}
func (sm *SessionManager) GetSession(id string) (*PreviewSession, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	s, ok := sm.sessions[id]
	return s, ok
}
func (sm *SessionManager) CloseSession(id, reason string) error {
	sm.mu.Lock()
	s, ok := sm.sessions[id]
	delete(sm.sessions, id)
	sm.mu.Unlock()
	if !ok {
		return ErrSessionNotFound
	}
	s.Close(reason)
	return nil
}

// CloseMatchingSession validates and removes under the same registry lock.
func (sm *SessionManager) CloseMatchingSession(id, lab string, generation int64, reason string) error {
	closeSession, err := sm.RetireMatchingSession(id, lab, generation, reason)
	closeSession()
	return err
}

// RetireMatchingSession detaches under the registry lock; the returned closure
// does network IO and callbacks outside an enclosing Control authority lock.
func (sm *SessionManager) RetireMatchingSession(id, lab string, generation int64, reason string) (func(), error) {
	sm.mu.Lock()
	s, ok := sm.sessions[id]
	if ok && (s.LabInstanceID != lab || s.Generation != generation) {
		sm.mu.Unlock()
		return func() {}, ErrSessionConflict
	}
	if ok {
		delete(sm.sessions, id)
	}
	sm.mu.Unlock()
	return func() {
		if ok {
			s.Close(reason)
		}
	}, nil
}
func (sm *SessionManager) CloseAttempt(s *PreviewSession, attempt, reason string) {
	s.CloseAttempt(attempt, reason)
	sm.discardUnattached(s, attempt)
}
func (sm *SessionManager) CloseAll(reason string) { sm.closeWhere(reason, false, "", 0) }

// RetireAll atomically detaches the current registry without blocking on peers.
// Call the returned closure after releasing the Control authority lock.
func (sm *SessionManager) RetireAll(reason string) func() {
	return sm.retireWhere(reason, false, "", 0)
}
func (sm *SessionManager) Shutdown(reason string) { sm.closeWhere(reason, true, "", 0) }
func (sm *SessionManager) InvalidateBefore(lab string, generation int64) {
	sm.closeWhere(protocol.PreviewReasonSessionClosed, false, lab, generation)
}
func (sm *SessionManager) closeWhere(reason string, shutdown bool, lab string, generation int64) {
	sm.retireWhere(reason, shutdown, lab, generation)()
}
func (sm *SessionManager) retireWhere(reason string, shutdown bool, lab string, generation int64) func() {
	sm.mu.Lock()
	if shutdown {
		sm.stopped = true
	}
	if lab != "" && generation > sm.minimumGeneration[lab] {
		sm.minimumGeneration[lab] = generation
	}
	var sessions []*PreviewSession
	for id, s := range sm.sessions {
		if lab == "" || (s.LabInstanceID == lab && s.Generation < generation) {
			delete(sm.sessions, id)
			sessions = append(sessions, s)
		}
	}
	sm.mu.Unlock()
	return func() {
		for _, s := range sessions {
			s.Close(reason)
		}
	}
}
func (sm *SessionManager) Count() int { sm.mu.RLock(); defer sm.mu.RUnlock(); return len(sm.sessions) }
