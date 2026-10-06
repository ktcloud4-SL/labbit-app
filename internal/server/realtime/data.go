package realtime

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/server/tracecontext"
)

// serveDataHTTP는 Upgrade 전에 Connector credential과 subprotocol을 확인한다. 인증되지 않은 요청은 WebSocket connection이 되지 못한다.
func (r *Relay) serveDataHTTP(w http.ResponseWriter, req *http.Request) {
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
	credential, ok := bearerCredential(req.Header)
	if !ok {
		r.rejected(http.StatusUnauthorized)
		r.logger.Warn("Connector Terminal Data 인증 거절", "reason", "missing_or_malformed_authorization")
		unauthorized(w)
		return
	}
	if !offersSubprotocol(req, DataSubprotocol) {
		r.rejected(http.StatusBadRequest)
		r.logger.Warn("Connector Terminal Data subprotocol 거절", "reason", "unsupported_subprotocol")
		http.Error(w, "unsupported subprotocol", http.StatusBadRequest)
		return
	}

	// 인증을 시작하기 전에 revoke 순번을 기억한다. 인증이 끝난 뒤 connection으로 등록되기 전에 이 trust가 revoke되면
	// 추적 목록에 없어 놓치게 되므로, 등록할 때 그 사이의 revoke를 확인한다(trust.go).
	ticket := r.trust.begin()
	defer ticket.release()

	ctx, cancel := context.WithTimeout(req.Context(), controlTimeout)
	identity, err := r.connectors.AuthenticateConnector(ctx, credential)
	cancel()
	switch {
	case errors.Is(err, ErrUnauthenticated):
		r.rejected(http.StatusUnauthorized)
		r.logger.Warn("Connector Terminal Data 인증 거절", "reason", "unauthenticated")
		unauthorized(w)
		return
	case err != nil:
		r.rejected(http.StatusServiceUnavailable)
		r.logger.Error("Connector Terminal Data 인증 의존성 오류", "error_code", codeInternalError)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}

	ws, err := r.dataUpgrader.Upgrade(w, req, nil)
	if err != nil {
		r.logger.Warn("Connector Terminal Data Upgrade 실패", "connector_id", identity.ConnectorID, "reason", "upgrade_failed")
		return
	}
	r.serveData(ws, identity, ticket)
}

// serveData는 Upgrade된 Connector connection 하나를 끝까지 처리한다. 첫 application message는 TERMINAL_DATA_ATTACH여야 한다.
func (r *Relay) serveData(ws *websocket.Conn, identity ConnectorIdentity, ticket *trustTicket) {
	ws.SetReadLimit(r.readLimit)
	p := newPeer(ws, r.dataQueueBytes, r.dataQueueMessages, r.writeTimeout, r.closeGrace)
	var attached atomic.Bool
	defer func() {
		p.shutdown()
		if attached.Load() && r.metrics != nil {
			r.metrics.ConnectorConnections.Dec()
		}
	}()
	log := r.logger.With("connector_id", identity.ConnectorID)

	// attach 전부터 trust를 추적한다. TERMINAL_DATA_ATTACH를 기다리는 connection도 Credential이 revoke되면 종료 대상이다.
	d := &dataConn{p: p, credentialID: identity.CredentialID, connectorID: identity.ConnectorID}
	if !r.trust.admit(ticket, d) {
		r.rejected(http.StatusUnauthorized)
		// 인증한 뒤 등록하기 전에 같은 Credential/Connector가 revoke되었다. 오래된 trust로 connection을 열어 두지 않는다.
		log.Warn("Connector Terminal Data 인증 직후 trust 상실", "reason", "revoked_during_upgrade")
		p.close(closeCredentialRevoked, "credential revoked", false)
		return
	}
	defer r.trust.forget(d)

	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-r.done:
			if !attached.Load() {
				p.close(closeServiceRestart, "server shutting down", true)
			}
		case <-finished:
		}
	}()

	_ = ws.SetReadDeadline(time.Now().Add(r.attachTimeout))
	kind, data, err := readMessage(ws, maxJSONTextBytes)
	if err != nil {
		r.closeOnReadError(p, err, log)
		return
	}
	if kind != websocket.TextMessage {
		r.rejected(http.StatusBadRequest)
		log.Warn("Connector Terminal Data protocol 위반", "reason", "binary_before_attach")
		p.close(closePolicy, "protocol violation", false)
		return
	}
	msg, err := decodeDataMessage(data)
	if err != nil || msg.Type != typeDataAttach {
		log.Warn("Connector Terminal Data protocol 위반", "reason", "invalid_attach")
		r.rejectData(p, msg, codeProtocolError, "protocol violation", "")
		return
	}

	// Connector identity는 credential에서 결정한 값이다. message가 주장한 값은 기대한 TerminalSession과 대조할 뿐 identity가 아니다.
	s := r.lookup(msg.TerminalSessionID)
	var code string
	switch {
	case s == nil:
		code = codeDataInvalidSession
	case s.connectorID != identity.ConnectorID:
		code = codeForbidden
	case s.corr.LabInstanceID != msg.LabInstanceID:
		code = codeDataInvalidSession
	case s.corr.Generation != msg.Generation:
		code = codeDataStaleGen
	}
	if code != "" {
		withTrace(log, msg.Trace).Warn("Connector Terminal Data attach 거절", "terminal_session_id", boundID(msg.TerminalSessionID), "error_code", code)
		r.rejectData(p, msg, code, "terminal data attach was rejected", msg.MessageID)
		return
	}

	log = withTrace(s.log, msg.Trace)
	d.runtimeID = msg.RuntimeID
	resumed, err := r.bindData(s, d, msg.MessageID, msg.Trace)
	if errors.Is(err, errDataRevoked) {
		r.rejected(http.StatusUnauthorized)
		// attach를 기다리는 동안 Credential이 revoke되었다. 이미 종료가 요청되었으므로 사유만 남긴다.
		log.Warn("Connector Terminal Data attach 거절", "reason", "credential_revoked")
		p.close(closeCredentialRevoked, "credential revoked", false)
		return
	}
	if err != nil {
		log.Warn("Connector Terminal Data attach 거절", "error_code", codeDataInvalidSession)
		r.rejectData(p, msg, codeDataInvalidSession, "terminal data attach was rejected", msg.MessageID)
		return
	}
	if r.metrics != nil {
		r.metrics.ConnectorConnections.Inc()
		if resumed {
			r.metrics.ConnectorReconnects.Inc()
		}
	}
	attached.Store(true)
	log.Info("Connector Terminal Data attach", "resumed", resumed)

	_ = ws.SetReadDeadline(time.Time{})
	ended := r.dataLoop(s, d, ws)
	if !ended {
		r.dataGone(s, d)
	}
}

// rejectData는 attach를 거절하고 close 1008로 끝낸다. ERROR는 terminal-data.schema.json의 Envelope가 요구하는
// correlation을 message에서 안전하게 되돌려 줄 수 있을 때만 보낸다(길이가 긴 값을 되돌려 보내지 않는다).
func (r *Relay) rejectData(p *peer, msg dataMessage, code, message, replyTo string) {
	if code == codeForbidden {
		r.rejected(http.StatusForbidden)
	} else {
		r.rejected(http.StatusBadRequest)
	}
	if msg.TerminalSessionID != "" && len(msg.TerminalSessionID) <= 128 && len(msg.LabInstanceID) > 0 && len(msg.LabInstanceID) <= 128 && msg.Generation >= 1 {
		c := correlation{TerminalSessionID: msg.TerminalSessionID, LabInstanceID: msg.LabInstanceID, Generation: msg.Generation}
		p.closeWithError(dataError(c, code, message, true, replyTo, msg.Trace), closePolicy, "rejected")
		return
	}
	p.close(closePolicy, "rejected", false)
}

// bindData는 Connector data channel을 TerminalSession의 current channel로 만든다. 같은 TerminalSession에 이미 있는
// data channel은 새 channel로 교체한다. channel이 끊겼다가 다시 붙은 경우 resumed는 true다. OUTPUT replay는 없다.
func (r *Relay) bindData(s *session, d *dataConn, attachMessageID string, trace tracecontext.Context) (resumed bool, err error) {
	s.tmu.Lock()
	defer s.tmu.Unlock()

	// revoke가 이 connection을 이 세션의 data channel에서 내릴 수 있도록 session을 먼저 알린다. 아래에서 s.mu 안에서 revoked를 확인하므로
	// revoke가 session을 보지 못한 경우(이 store 이전)에는 그 확인이 revoke를 본다. revoke는 revoked를 먼저 true로 한 뒤 session을 읽는다.
	d.session.Store(s)

	s.mu.Lock()
	if s.state == stateEnded {
		s.mu.Unlock()
		return false, ErrSessionEnded
	}
	if d.revoked.Load() {
		s.mu.Unlock()
		return false, errDataRevoked
	}
	resumed = s.dataBoundBefore
	s.mu.Unlock()

	// TERMINAL_DATA_ATTACHED를 먼저 queue에 넣어 이 channel로 나가는 어떤 INPUT/control보다 앞서게 한다.
	if err := d.p.send(websocket.TextMessage, dataAttached(s.corr, attachMessageID, resumed, trace)); err != nil {
		return false, err
	}

	s.mu.Lock()
	if d.revoked.Load() {
		// ATTACHED를 queue에 넣는 사이에 revoke되었다. revoke는 아직 공개되지 않은 connection을 s.data에서 내릴 수 없었으므로 여기서 막는다.
		s.mu.Unlock()
		return false, errDataRevoked
	}
	old := s.data
	s.data = d
	s.dataBoundBefore = true
	if s.browser != nil {
		// data channel이 돌아왔으므로 다음 단절 때 Browser에 다시 알릴 수 있다.
		s.browser.unavailableSent.Store(false)
	}
	s.mu.Unlock()

	if old != nil {
		old.p.close(closeNormal, "replaced by a newer data connection", true)
	}
	return resumed, nil
}

// dataLoop는 bind된 Connector connection의 message를 읽는다. Binary는 PTY OUTPUT이다.
// TERMINAL_DATA_ENDED를 받아 TerminalSession을 종료했으면 true를 반환한다.
func (r *Relay) dataLoop(s *session, d *dataConn, ws *websocket.Conn) (ended bool) {
	for {
		kind, data, err := readMessage(ws, maxJSONTextBytes)
		if err != nil {
			if errors.Is(err, errJSONTooLarge) {
				s.log.Warn("Connector Terminal Data protocol 위반", "reason", "message_too_big")
				d.p.close(closeTooBig, "message too big", false)
			}
			return false
		}
		if kind == websocket.BinaryMessage {
			r.dataOutput(s, d, data)
			continue
		}
		msg, err := decodeDataMessage(data)
		switch {
		case errors.Is(err, errUnsupportedType):
			// 이 경계에서 Connector가 보낼 일이 없는 message다. 계약 확장에 대비해 무시한다.
			continue
		case err != nil:
			s.log.Warn("Connector Terminal Data protocol 위반", "reason", "malformed_control")
			d.p.closeWithError(dataError(s.corr, codeProtocolError, "protocol violation", true, "", noTrace), closePolicy, "protocol violation")
			return false
		}
		// 이 connection은 하나의 TerminalSession에만 bind되어 있다. 다른 session을 가리키는 message를 그 session에 연결하지 않는다.
		if msg.TerminalSessionID != s.corr.TerminalSessionID || msg.LabInstanceID != s.corr.LabInstanceID || msg.Generation != s.corr.Generation {
			withTrace(s.log, msg.Trace).Warn("Connector Terminal Data correlation 불일치", "message_type", msg.Type)
			d.p.closeWithError(dataError(s.corr, codeDataInvalidSession, "message does not match the bound terminal session", true, msg.MessageID, msg.Trace), closePolicy, "correlation mismatch")
			return false
		}
		switch msg.Type {
		case typeDataEnded:
			r.dataEnded(s, d, msg)
			return true
		case typeError:
			// Connector의 ERROR는 업무 결과가 아니다. 안전한 code만 남기고 TerminalSession 상태는 바꾸지 않는다.
			withTrace(s.log, msg.Trace).Warn("Connector Terminal Data ERROR 수신", "error_code", safeErrorCode(msg.ErrorCode))
		default:
			// 두 번째 TERMINAL_DATA_ATTACH다.
			withTrace(s.log, msg.Trace).Warn("Connector Terminal Data protocol 위반", "reason", "duplicate_attach")
			d.p.closeWithError(dataError(s.corr, codeProtocolError, "protocol violation", true, msg.MessageID, msg.Trace), closePolicy, "protocol violation")
			return false
		}
	}
}

// dataOutput은 PTY OUTPUT Binary frame을 현재 Browser attachment의 queue로 넘긴다. 본문을 해석하거나 기록하지 않는다.
// attachment가 없으면 버린다. 단절된 동안의 OUTPUT은 저장하거나 재생하지 않는다(history 없음).
// queue가 가득 차도 이 함수는 기다리지 않으므로 느린 Browser가 Connector output reader를 막지 못한다.
func (r *Relay) dataOutput(s *session, d *dataConn, data []byte) {
	s.mu.Lock()
	b := s.browser
	current := s.data == d
	s.mu.Unlock()
	if !current || b == nil {
		return
	}
	if err := b.p.send(websocket.BinaryMessage, data); errors.Is(err, errQueueFull) {
		r.slowConsumer(s, b)
	}
}

// dataEnded는 Connector가 알린 종료(PTY 종료, SSH 끊김 등)를 Control에 기록하고 Browser에 알린 뒤 Relay 상태를 정리한다.
func (r *Relay) dataEnded(s *session, d *dataConn, msg dataMessage) {
	s.tmu.Lock()
	defer s.tmu.Unlock()

	s.mu.Lock()
	current := s.data == d && s.state != stateEnded
	s.mu.Unlock()
	if !current {
		return
	}
	end := End{Reason: SanitizeReason(msg.Reason), ExitCode: msg.ExitCode, FromConnector: true, Trace: msg.Trace}
	ctx, cancel := context.WithTimeout(tracecontext.NewContext(context.Background(), msg.Trace), controlTimeout)
	defer cancel()
	if err := r.control.SessionEnded(ctx, s.corr.TerminalSessionID, end); err != nil {
		withTrace(s.log, msg.Trace).Error("TerminalSession 종료 기록 실패", "error_code", errorCode(err))
	}
	r.finish(s, end, true, false)
}

// dataGone은 data channel의 transport가 끊겼을 때 호출한다. 끊김은 PTY 종료가 아니므로 TerminalSession을 끝내지 않는다.
// Connector가 같은 TerminalSession에 다시 attach하면 resumed=true로 새 channel이 된다. 이미 교체된 channel이면 아무것도 하지 않는다.
func (r *Relay) dataGone(s *session, d *dataConn) {
	s.tmu.Lock()
	defer s.tmu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data != d {
		return
	}
	s.data = nil
	s.log.Info("Connector Terminal Data transport 끊김")
}

// bearerCredential은 정확히 하나의 "Authorization: Bearer <credential>"에서 Credential을 꺼낸다.
func bearerCredential(header http.Header) (ConnectorCredential, bool) {
	values := header.Values("Authorization")
	if len(values) != 1 {
		return "", false
	}
	scheme, token, found := strings.Cut(values[0], " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	if strings.IndexFunc(token, func(r rune) bool { return r <= ' ' || r == 0x7f }) >= 0 {
		return "", false
	}
	return ConnectorCredential(token), true
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// safeErrorCode는 Connector가 보낸 ERROR code를 log에 남길 수 있는 형태로 제한한다. 기계 판독용 상수 형태가 아니면 고정 값이다.
func safeErrorCode(code string) string {
	if safeReason.MatchString(code) {
		return code
	}
	return "INVALID"
}
