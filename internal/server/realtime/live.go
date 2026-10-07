package realtime

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/server/tracecontext"
)

// serveLiveHTTP는 Upgrade 전에 Origin, Cookie, subprotocol, 현재 인증을 확인한다.
// 인증되지 않은 요청은 WebSocket connection이 되지 못한다. Terminal Token은 받지 않는다.
func (r *Relay) serveLiveHTTP(w http.ResponseWriter, req *http.Request) {
	if !r.enter() {
		if r.metrics != nil {
			r.metrics.DrainingRejected.Inc()
		}
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	defer r.leave()

	if !websocket.IsWebSocketUpgrade(req) {
		r.rejected(http.StatusBadRequest)
		http.Error(w, "websocket upgrade required", http.StatusBadRequest)
		return
	}
	// 허용된 Origin이 정확히 하나 있어야 한다.
	origins := req.Header.Values("Origin")
	if len(origins) != 1 || !r.allowOrigin(origins[0]) {
		r.rejected(http.StatusForbidden)
		r.logger.Warn("Browser Live 거절", "reason", "origin_not_allowed")
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	cookie, err := req.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		r.rejected(http.StatusUnauthorized)
		r.logger.Warn("Browser Live 거절", "reason", "missing_session_cookie")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !offersSubprotocol(req, LiveSubprotocol) {
		r.rejected(http.StatusBadRequest)
		r.logger.Warn("Browser Live 거절", "reason", "unsupported_subprotocol")
		http.Error(w, "unsupported subprotocol", http.StatusBadRequest)
		return
	}

	session := SessionToken(cookie.Value)
	ctx, cancel := context.WithTimeout(req.Context(), controlTimeout)
	err = r.control.AuthenticateBrowser(ctx, session)
	cancel()
	switch {
	case errors.Is(err, ErrUnauthenticated):
		r.rejected(http.StatusUnauthorized)
		r.logger.Warn("Browser Live 거절", "reason", "unauthenticated")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	case err != nil:
		r.rejected(http.StatusServiceUnavailable)
		r.logger.Error("Browser Live 인증 의존성 오류", "error_code", codeInternalError)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}

	ws, err := r.liveUpgrader.Upgrade(w, req, nil)
	if err != nil {
		r.logger.Warn("Browser Live Upgrade 실패", "reason", "upgrade_failed")
		return
	}
	r.serveLive(ws, session)
}

// serveLive는 Upgrade된 학생 Browser Live connection 하나를 처리한다. 첫 application message는 LIVE_SUBSCRIBE여야 한다.
func (r *Relay) serveLive(ws *websocket.Conn, session SessionToken) {
	ws.SetReadLimit(r.readLimit)
	p := newPeer(ws, r.browserQueueBytes, r.browserQueueMessages, r.writeTimeout, r.closeGrace)

	var subscribed atomic.Bool
	defer func() {
		p.shutdown()
		if subscribed.Load() && r.metrics != nil {
			r.metrics.BrowserLiveConnections.Dec()
		}
	}()

	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-r.done:
			if !subscribed.Load() {
				p.close(closeServiceRestart, "server shutting down", true)
			}
		case <-finished:
		}
	}()

	_ = ws.SetReadDeadline(time.Now().Add(r.attachTimeout))
	kind, data, err := readMessage(ws, maxJSONTextBytes)
	if err != nil {
		r.closeOnReadError(p, err, r.logger)
		return
	}
	if kind != websocket.TextMessage {
		r.protocolViolation(p, r.logger, "binary_before_subscribe")
		return
	}
	msg, err := decodeLiveMessage(data)
	if err != nil || msg.Type != typeLiveSubscribe {
		r.protocolViolation(p, r.logger, "invalid_subscribe")
		return
	}

	if r.liveControl == nil {
		p.closeWithError(liveError(msg.LiveSessionID, codeInternalError, "live service unavailable", true, msg.MessageID, msg.Trace), closeInternal, "unavailable")
		return
	}

	ctx, cancel := context.WithTimeout(tracecontext.NewContext(context.Background(), msg.Trace), controlTimeout)
	grant, err := r.liveControl.AuthorizeLiveSubscribe(ctx, session, msg.LiveSessionID)
	cancel()
	if err != nil {
		r.rejectLiveSubscribe(p, withTrace(r.logger.With("live_session_id", boundID(msg.LiveSessionID)), msg.Trace), err, msg.MessageID, msg.LiveSessionID, msg.Trace)
		return
	}

	r.mu.Lock()
	live := r.liveSessions[grant.LiveSessionID]
	r.mu.Unlock()
	if live == nil {
		r.rejectLiveSubscribe(p, withTrace(r.logger.With("live_session_id", boundID(msg.LiveSessionID)), msg.Trace), ErrSessionNotFound, msg.MessageID, msg.LiveSessionID, msg.Trace)
		return
	}

	sub := newLiveSubscriber(p, live.log)
	defer sub.cancel()

	// 1. LIVE_SUBSCRIBED 메시지를 queue에 먼저 넣어 첫 application message로 전달되도록 한다 (ACK ordering: D-21).
	if err := sub.p.send(websocket.TextMessage, liveSubscribed(msg.LiveSessionID, msg.MessageID, msg.Trace)); err != nil {
		sub.close(closeInternal, "failed to enqueue subscribe ack", false)
		return
	}

	// 2. ACK를 큐에 넣은 뒤 active fan-out 대상에 등록한다.
	live.mu.Lock()
	if live.ended {
		live.mu.Unlock()
		sub.close(closeLifecycle, "live session ended", false)
		return
	}
	live.subscribers[sub] = struct{}{}
	live.mu.Unlock()

	defer r.removeLiveSubscriber(live, sub)

	if r.metrics != nil {
		r.metrics.BrowserLiveConnections.Inc()
	}
	subscribed.Store(true)
	withTrace(live.log, msg.Trace).Info("Browser Live subscribed")

	// 3. 학생 연결은 read-only다. 클라이언트가 Binary나 Text application message를 보내면 protocol violation으로 종료한다.
	_ = ws.SetReadDeadline(time.Time{})
	for {
		msgType, _, err := ws.ReadMessage()
		if err != nil {
			return
		}
		if msgType == websocket.BinaryMessage || msgType == websocket.TextMessage {
			live.log.Warn("Browser Live read-only 위반", "reason", "client_sent_data")
			sub.p.closeWithError(liveError(live.id, codeProtocolError, "live subscription is read-only", true, "", noTrace), closePolicy, "read-only violation")
			return
		}
	}
}

func (r *Relay) rejectLiveSubscribe(p *peer, log *slog.Logger, err error, messageID, liveSessionID string, trace tracecontext.Context) {
	code := errorCode(err)
	closeCode := closeCodeForErr(err)
	log.Warn("Browser Live subscribe 거절", "error_code", code)
	p.closeWithError(liveError(liveSessionID, code, "live subscription was rejected", true, messageID, trace), closeCode, "rejected")
}

func closeCodeForErr(err error) int {
	switch {
	case errors.Is(err, ErrUnauthenticated):
		return closeAuth
	case errors.Is(err, ErrForbidden):
		return closeForbidden
	case errors.Is(err, ErrSessionNotFound), errors.Is(err, ErrSessionEnded):
		return closeNotFound
	case errors.Is(err, ErrLabMutation):
		return closeLifecycle
	default:
		return closeInternal
	}
}

// RegisterLive는 생성된 LiveSession을 source TerminalSession과 함께 Relay에 등록한다.
// source TerminalSession이 없거나 이미 종료되었으면 실패한다.
func (r *Relay) RegisterLive(liveSessionID, sourceTerminalSessionID, classID string) error {
	if liveSessionID == "" || sourceTerminalSessionID == "" || classID == "" {
		return errors.New("realtime: liveSessionID, sourceTerminalSessionID, classID가 필요합니다")
	}

	s := r.lookup(sourceTerminalSessionID)
	if s == nil {
		return ErrSessionNotFound
	}

	s.tmu.Lock()
	defer s.tmu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state == stateEnded {
		return ErrSessionEnded
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return ErrRelayClosed
	}
	if r.sessions[sourceTerminalSessionID] != s {
		return ErrSessionNotFound
	}
	if _, exists := r.liveSessions[liveSessionID]; exists {
		return ErrDuplicateSession
	}
	if _, exists := r.terminalToLive[sourceTerminalSessionID]; exists {
		return ErrDuplicateSession
	}

	live := &liveSession{
		id:                      liveSessionID,
		sourceTerminalSessionID: sourceTerminalSessionID,
		classID:                 classID,
		subscribers:             make(map[*liveSubscriber]struct{}),
		log: r.logger.With(
			"live_session_id", liveSessionID,
			"terminal_session_id", sourceTerminalSessionID,
			"class_id", classID,
		),
	}
	r.liveSessions[liveSessionID] = live
	r.terminalToLive[sourceTerminalSessionID] = live
	s.liveSession = live
	return nil
}

// TerminateLive는 LiveSession을 명시적으로 종료한다. 학생 subscriber들에게 LIVE_ENDED를 전달하고 WSS를 종료한다.
// source TerminalSession과 PTY는 그대로 유지된다.
func (r *Relay) TerminateLive(liveSessionID string, end End) {
	r.mu.Lock()
	live := r.liveSessions[liveSessionID]
	if live == nil {
		r.mu.Unlock()
		return
	}
	delete(r.liveSessions, liveSessionID)
	delete(r.terminalToLive, live.sourceTerminalSessionID)
	s := r.sessions[live.sourceTerminalSessionID]
	r.mu.Unlock()

	if s != nil {
		s.mu.Lock()
		if s.liveSession == live {
			s.liveSession = nil
		}
		s.mu.Unlock()
	}

	r.finishLive(live, end, false)
}

// SourceUsable은 sourceTerminalSessionID가 Relay에 등록되어 있고 종료되지 않았는지 확인한다.
func (r *Relay) SourceUsable(sourceTerminalSessionID string) bool {
	s := r.lookup(sourceTerminalSessionID)
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state != stateEnded
}

// LiveSessions는 Relay가 추적 중인 LiveSession 수다.
func (r *Relay) LiveSessions() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.liveSessions)
}

func (r *Relay) removeLive(live *liveSession) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.liveSessions, live.id)
	delete(r.terminalToLive, live.sourceTerminalSessionID)
}

func (r *Relay) finishLive(live *liveSession, end End, sourceTerminalEnded bool) {
	live.mu.Lock()
	if live.ended {
		live.mu.Unlock()
		return
	}
	live.ended = true
	subs := make([]*liveSubscriber, 0, len(live.subscribers))
	for sub := range live.subscribers {
		subs = append(subs, sub)
	}
	live.subscribers = nil
	live.mu.Unlock()

	withTrace(live.log, end.Trace).Info("LiveSession Relay 정리", "reason", end.Reason)

	reason := end.Reason
	if reason == "" {
		reason = "SESSION_CLOSED"
	}

	var code int
	switch {
	case reason == EndReasonServiceRestarting:
		code = closeServiceRestart
	case sourceTerminalEnded:
		code = closeLifecycle
	case reason == EndReasonLabReset, reason == EndReasonLabCleanup, reason == "SOURCE_TERMINAL_ENDED":
		code = closeLifecycle
	default:
		code = closeNormal
	}

	endedBytes := liveEnded(live.id, End{Reason: reason, ExitCode: end.ExitCode, Trace: end.Trace})

	for _, sub := range subs {
		sub.closeWithFinalMessage(websocket.TextMessage, endedBytes, code, "live session ended")
	}

	if sourceTerminalEnded && r.liveControl != nil {
		ctx, cancel := context.WithTimeout(context.Background(), controlTimeout)
		defer cancel()
		if err := r.liveControl.SourceTerminalEnded(ctx, live.sourceTerminalSessionID, End{Reason: reason, Trace: end.Trace}); err != nil {
			live.log.Warn("LiveSession DB 종료 기록 실패", "error_code", errorCode(err))
		}
	}
}

func (r *Relay) slowLiveSubscriber(live *liveSession, sub *liveSubscriber) {
	sub.log.Warn("Browser Live 느린 수신자", "error_code", codeSlowConsumer)
	sub.p.closeWithError(liveError(live.id, codeSlowConsumer, "live output consumer is too slow", true, "", noTrace), closeSlowConsumer, "slow consumer")
	sub.cancel()
	r.removeLiveSubscriber(live, sub)
}

func (r *Relay) removeLiveSubscriber(live *liveSession, sub *liveSubscriber) {
	live.mu.Lock()
	delete(live.subscribers, sub)
	live.mu.Unlock()
}
