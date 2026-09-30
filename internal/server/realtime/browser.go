package realtime

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// serveBrowserHTTP는 Upgrade 전에 Origin, Cookie, subprotocol, 현재 인증을 확인한다.
// 인증되지 않은 요청은 WebSocket connection이 되지 못한다. Session token은 URL query로 받지 않는다.
func (r *Relay) serveBrowserHTTP(w http.ResponseWriter, req *http.Request) {
	if !r.enter() {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	defer r.leave()

	if !websocket.IsWebSocketUpgrade(req) {
		http.Error(w, "websocket upgrade required", http.StatusBadRequest)
		return
	}
	// 허용된 Origin이 정확히 하나 있어야 한다. Referer로 대체하지 않는다.
	origins := req.Header.Values("Origin")
	if len(origins) != 1 || !r.allowOrigin(origins[0]) {
		r.logger.Warn("Browser Terminal 거절", "reason", "origin_not_allowed")
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	cookie, err := req.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		r.logger.Warn("Browser Terminal 거절", "reason", "missing_session_cookie")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !offersSubprotocol(req, BrowserSubprotocol) {
		r.logger.Warn("Browser Terminal 거절", "reason", "unsupported_subprotocol")
		http.Error(w, "unsupported subprotocol", http.StatusBadRequest)
		return
	}

	session := SessionToken(cookie.Value)
	ctx, cancel := context.WithTimeout(req.Context(), controlTimeout)
	err = r.control.AuthenticateBrowser(ctx, session)
	cancel()
	switch {
	case errors.Is(err, ErrUnauthenticated):
		r.logger.Warn("Browser Terminal 거절", "reason", "unauthenticated")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	case err != nil:
		r.logger.Error("Browser Terminal 인증 의존성 오류", "error_code", codeInternalError)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}

	ws, err := r.browserUpgrader.Upgrade(w, req, nil)
	if err != nil {
		// Upgrade가 이미 응답을 썼다.
		r.logger.Warn("Browser Terminal Upgrade 실패", "reason", "upgrade_failed")
		return
	}
	r.serveBrowser(ws, session)
}

// serveBrowser는 Upgrade된 Browser connection 하나를 끝까지 처리한다. 첫 application message는 TERMINAL_ATTACH여야 한다.
func (r *Relay) serveBrowser(ws *websocket.Conn, session SessionToken) {
	ws.SetReadLimit(r.readLimit)
	p := newPeer(ws, r.browserQueueBytes, r.browserQueueMessages, r.writeTimeout, r.closeGrace)
	defer p.shutdown()

	var attached atomic.Bool
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-r.done:
			// attach된 connection은 Shutdown이 TERMINAL_SESSION_ENDED를 먼저 보낸 뒤 닫는다.
			if !attached.Load() {
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
		r.protocolViolation(p, r.logger, "binary_before_attach")
		return
	}
	msg, err := decodeBrowserMessage(data)
	if err != nil || msg.Type != typeTerminalAttach {
		r.protocolViolation(p, r.logger, "invalid_attach")
		return
	}

	grant, err := r.authorizeAttach(session, msg)
	if err != nil {
		r.rejectAttach(p, r.logger.With("terminal_session_id", boundID(msg.TerminalSessionID)), err, msg.MessageID)
		return
	}
	s := r.lookup(grant.TerminalSessionID)
	// Control이 허용한 TerminalSession이 Relay가 등록한 correlation과 정확히 같을 때만 사용한다.
	if s == nil || s.corr.LabInstanceID != grant.LabInstanceID || s.corr.Generation != grant.Generation {
		r.rejectAttach(p, r.logger.With("terminal_session_id", boundID(msg.TerminalSessionID)), ErrSessionNotFound, msg.MessageID)
		return
	}

	b := newBrowserConn(p, s.log)
	defer b.cancel()
	resumed, err := r.attachBrowser(s, b, msg)
	if err != nil {
		r.rejectAttach(p, s.log, err, msg.MessageID)
		return
	}
	attached.Store(true)
	s.log.Info("Browser Terminal attach", "resumed", resumed)

	_ = ws.SetReadDeadline(time.Time{})
	r.browserLoop(s, b, ws)
	r.browserGone(s, b)
}

func (r *Relay) authorizeAttach(session SessionToken, msg browserMessage) (AttachGrant, error) {
	ctx, cancel := context.WithTimeout(context.Background(), controlTimeout)
	defer cancel()
	return r.control.AuthorizeAttach(ctx, session, AttachRequest{TerminalSessionID: msg.TerminalSessionID, Token: msg.Token})
}

// attachFailure는 attach를 거절한 이유를 Browser의 ERROR code와 close code로 바꾼다.
func attachFailure(err error) (code string, closeCode int) {
	switch {
	case errors.Is(err, ErrUnauthenticated):
		return codeAuthRequired, closeAuth
	case errors.Is(err, ErrInvalidToken):
		return codeInvalidSessionTok, closeAuth
	case errors.Is(err, ErrForbidden):
		return codeForbidden, closeForbidden
	case errors.Is(err, ErrSessionNotFound):
		return codeSessionNotFound, closeNotFound
	case errors.Is(err, ErrSessionEnded):
		return codeSessionExpired, closeNotFound
	case errors.Is(err, ErrLabMutation):
		return codeLabMutation, closeLifecycle
	default:
		return codeInternalError, closeInternal
	}
}

// rejectAttach는 고정된 설명의 fatal ERROR를 보내고 close한다. 입력 값(token, ID)은 응답과 log에 복사하지 않는다.
func (r *Relay) rejectAttach(p *peer, log *slog.Logger, err error, replyTo string) {
	code, closeCode := attachFailure(err)
	log.Warn("Browser Terminal attach 거절", "error_code", code)
	p.closeWithError(browserError(code, "terminal attach was rejected", true, replyTo), closeCode, "attach rejected")
}

// protocolViolation은 계약을 어긴 Browser connection을 ERROR(PROTOCOL_ERROR)와 close 1008로 끝낸다.
func (r *Relay) protocolViolation(p *peer, log *slog.Logger, reason string) {
	log.Warn("Browser Terminal protocol 위반", "reason", reason)
	p.closeWithError(browserError(codeProtocolError, "protocol violation", true, ""), closePolicy, "protocol violation")
}

// closeOnReadError는 읽기 오류를 log용 고정 분류로 바꾸고 필요한 close를 보낸다.
func (r *Relay) closeOnReadError(p *peer, err error, log *slog.Logger) {
	switch {
	case errors.Is(err, errJSONTooLarge):
		log.Warn("WebSocket protocol 위반", "reason", "message_too_big")
		p.close(closeTooBig, "message too big", false)
	case isTimeout(err):
		log.Warn("WebSocket attach 시간 초과")
		p.close(closePolicy, "attach timeout", false)
	}
	// 그 외(상대가 닫았거나 연결이 끊김)는 보낼 것이 없다. gorilla가 read limit 초과의 1009 close frame을 이미 보냈다.
}

// attachBrowser는 새 Browser connection을 TerminalSession의 current attachment로 만든다.
// 같은 TerminalSession의 이전 Browser connection만 4004로 종료하고 PTY와 data channel은 다시 만들지 않는다.
func (r *Relay) attachBrowser(s *session, b *browserConn, msg browserMessage) (resumed bool, err error) {
	s.tmu.Lock()
	defer s.tmu.Unlock()

	s.mu.Lock()
	state := s.state
	s.mu.Unlock()
	switch state {
	case stateEnded:
		return false, ErrSessionEnded
	case stateOpening:
		return false, ErrSessionNotFound
	}

	ctx, cancel := context.WithTimeout(context.Background(), controlTimeout)
	defer cancel()
	if err := r.control.RecordAttached(ctx, s.corr.TerminalSessionID, r.clock.Now()); err != nil {
		return false, err
	}

	s.mu.Lock()
	resumed = s.attachedBefore
	s.mu.Unlock()
	// TERMINAL_ATTACHED를 먼저 queue에 넣어 이 connection으로 나가는 어떤 OUTPUT보다 앞서게 한다. 아직 current가 아니므로 OUTPUT은 들어오지 않는다.
	if err := b.p.send(websocket.TextMessage, browserAttached(s.corr.TerminalSessionID, msg.MessageID, resumed)); err != nil {
		return false, err
	}

	s.mu.Lock()
	old, d := s.browser, s.data
	s.browser = b
	s.attachedBefore = true
	s.stopGraceLocked()
	s.mu.Unlock()

	if old != nil {
		old.close(closeReplaced, "replaced by a newer attachment", true)
	}
	// Browser의 현재 terminal 크기를 PTY에 반영한다. 기다리지 않는다.
	if d != nil {
		_ = d.p.send(websocket.TextMessage, dataResize(s.corr, msg.Cols, msg.Rows))
	}
	return resumed, nil
}

// browserLoop는 attach된 Browser connection의 message를 읽는다. Binary는 PTY INPUT, Text는 TERMINAL_RESIZE만 허용한다.
func (r *Relay) browserLoop(s *session, b *browserConn, ws *websocket.Conn) {
	for {
		kind, data, err := readMessage(ws, maxJSONTextBytes)
		if err != nil {
			if errors.Is(err, errJSONTooLarge) {
				b.log.Warn("WebSocket protocol 위반", "reason", "message_too_big")
				b.close(closeTooBig, "message too big", false)
			}
			return
		}
		if kind == websocket.BinaryMessage {
			r.browserInput(s, b, data)
			continue
		}
		msg, err := decodeBrowserMessage(data)
		if err != nil || msg.Type != typeTerminalResize || msg.TerminalSessionID != s.corr.TerminalSessionID {
			b.log.Warn("Browser Terminal protocol 위반", "reason", "invalid_control")
			b.p.closeWithError(browserError(codeProtocolError, "protocol violation", true, ""), closePolicy, "protocol violation")
			b.cancel()
			return
		}
		r.browserResize(s, b, msg)
	}
}

// browserInput은 PTY INPUT Binary frame을 그대로 Connector data channel로 보낸다. 본문을 해석하거나 기록하지 않는다.
func (r *Relay) browserInput(s *session, b *browserConn, data []byte) {
	d := r.dataFor(s, b)
	if d == nil {
		r.inputUnavailable(s, b)
		return
	}
	// 수신자(Connector)가 느리면 이 Browser의 read loop만 기다린다. 다른 TerminalSession과 OUTPUT 경로는 막지 않는다.
	if err := d.p.sendWait(b.ctx, websocket.BinaryMessage, data); err != nil && !errors.Is(err, context.Canceled) {
		r.inputUnavailable(s, b)
	}
}

func (r *Relay) browserResize(s *session, b *browserConn, msg browserMessage) {
	d := r.dataFor(s, b)
	if d == nil {
		r.inputUnavailable(s, b)
		return
	}
	if err := d.p.sendWait(b.ctx, websocket.TextMessage, dataResize(s.corr, msg.Cols, msg.Rows)); err != nil && !errors.Is(err, context.Canceled) {
		r.inputUnavailable(s, b)
	}
}

// dataFor는 b가 아직 current attachment일 때 current data channel을 반환한다. 아니면 nil이다.
func (r *Relay) dataFor(s *session, b *browserConn) *dataConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.browser != b {
		return nil
	}
	return s.data
}

// inputUnavailable은 data channel이 없어 INPUT을 버렸음을 Browser에 한 번 알린다. 이미 교체된 attachment에는 알리지 않는다.
// 연결 끊김이 곧 PTY 종료는 아니므로 세션을 끝내지 않는다.
func (r *Relay) inputUnavailable(s *session, b *browserConn) {
	s.mu.Lock()
	current := s.browser == b
	s.mu.Unlock()
	if !current || b.unavailableSent.Swap(true) {
		return
	}
	_ = b.p.send(websocket.TextMessage, browserError(codeConnectorUnavail, "terminal connector is not connected", false, ""))
}

// browserGone은 Browser connection이 끝났을 때 호출한다. current attachment였다면 TerminalSession을 DETACHED로 두고
// 기본 60초 grace를 시작한다. PTY와 data channel은 유지한다. 이미 교체된 connection이면 아무것도 하지 않는다.
func (r *Relay) browserGone(s *session, b *browserConn) {
	b.cancel()

	s.tmu.Lock()
	defer s.tmu.Unlock()

	s.mu.Lock()
	if s.state != stateLive || s.browser != b {
		s.mu.Unlock()
		return
	}
	s.browser = nil
	now := r.clock.Now()
	graceExpiresAt := now.Add(r.grace)
	r.armGraceLocked(s, graceExpiresAt)
	s.mu.Unlock()

	s.log.Info("Browser Terminal detach", "grace_seconds", int(r.grace/time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), controlTimeout)
	defer cancel()
	if err := r.control.RecordDetached(ctx, s.corr.TerminalSessionID, now, graceExpiresAt); err != nil && !errors.Is(err, ErrSessionEnded) {
		// 기록하지 못해도 grace timer는 이미 돌고 있다. 만료되면 종료 기록이 상태를 바로잡는다.
		s.log.Error("TerminalSession detach 기록 실패", "error_code", errorCode(err))
	}
}

// graceExpired는 grace timer가 만료되었을 때 호출한다. 그 사이 재attach되었거나 종료되었다면 아무것도 하지 않는다.
// 만료하면 Control에 종료(Connector CLOSE와 기록)를 요청하고 Relay 상태를 정리한다.
func (r *Relay) graceExpired(s *session, epoch uint64) {
	s.tmu.Lock()
	defer s.tmu.Unlock()

	s.mu.Lock()
	stale := s.state != stateLive || s.graceEpoch != epoch || s.browser != nil
	if !stale {
		s.grace = nil
	}
	s.mu.Unlock()
	if stale {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), controlTimeout)
	defer cancel()
	if err := r.control.CloseSession(ctx, s.corr.TerminalSessionID, EndReasonSessionExpired); err != nil {
		s.log.Error("TerminalSession grace 만료 종료 기록 실패", "error_code", errorCode(err))
	}
	r.finish(s, End{Reason: EndReasonSessionExpired}, true, true)
}

// slowConsumer는 Browser attachment의 전송 queue가 한도를 넘었을 때 그 attachment만 4005로 종료한다.
// 조용히 byte를 버리고 연결을 유지하지 않는다. PTY는 종료하지 않고 grace를 따른다.
func (r *Relay) slowConsumer(s *session, b *browserConn) {
	b.log.Warn("Browser Terminal 느린 수신자", "error_code", codeSlowConsumer)
	b.p.closeWithError(browserError(codeSlowConsumer, "terminal output consumer is too slow", true, ""), closeSlowConsumer, "slow consumer")
	b.cancel()
	// 저장소 호출이 Connector output read loop를 막지 않도록 별도 goroutine에서 detach를 처리한다.
	// 호출한 data connection의 handler가 wg를 잡고 있으므로 Shutdown은 이 goroutine을 기다린다.
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.browserGone(s, b)
	}()
}

func offersSubprotocol(req *http.Request, want string) bool {
	for _, offered := range websocket.Subprotocols(req) {
		if offered == want {
			return true
		}
	}
	return false
}

// boundID는 검증되지 않은 ID를 log에 남기기 전에 길이를 제한한다.
func boundID(id string) string {
	const maxLen = 128
	if len(id) <= maxLen {
		return id
	}
	return strings.ToValidUTF8(id[:maxLen], "")
}
