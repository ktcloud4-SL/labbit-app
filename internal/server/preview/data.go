package preview

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

// DataHandler는 Connector Preview Data WSS(DataPath)의 http.Handler다. WSS Upgrade 전에 Connector Credential과 subprotocol을 확인하므로
// 인증되지 않은 요청은 WebSocket connection이 되지 못한다.
func (g *Gateway) DataHandler() http.Handler { return http.HandlerFunc(g.serveDataHTTP) }

func (g *Gateway) serveDataHTTP(w http.ResponseWriter, r *http.Request) {
	if !g.enter() {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	defer g.wg.Done()

	if !websocket.IsWebSocketUpgrade(r) {
		http.Error(w, "websocket upgrade required", http.StatusBadRequest)
		return
	}
	credential, ok := bearerCredential(r.Header)
	if !ok {
		g.logger.Warn("Connector Preview Data 인증 거절", "reason", "missing_or_malformed_authorization")
		unauthorized(w)
		return
	}
	if !offersSubprotocol(r) {
		g.logger.Warn("Connector Preview Data subprotocol 거절", "reason", "unsupported_subprotocol")
		http.Error(w, "unsupported subprotocol", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), authTimeout)
	identity, err := g.connectors.AuthenticateConnector(ctx, credential)
	cancel()
	switch {
	case errors.Is(err, ErrUnauthenticated):
		g.logger.Warn("Connector Preview Data 인증 거절", "reason", "unauthenticated")
		unauthorized(w)
		return
	case err != nil:
		// 저장소 오류 원문은 응답에 싣지 않는다. 인증에 실패한 것이 아니므로 401도 아니다.
		g.logger.Error("Connector Preview Data 인증 의존성 오류", "error_code", "DEPENDENCY_UNAVAILABLE")
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}

	upgrader := websocket.Upgrader{Subprotocols: []string{protocol.SubprotocolPreviewData}, HandshakeTimeout: writeTimeout}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		g.logger.Warn("Connector Preview Data Upgrade 실패", "connector_id", identity.ConnectorID.String(), "reason", "upgrade_failed")
		return
	}
	g.serveData(ws, identity)
}

// serveData는 Upgrade된 Connector connection 하나를 끝까지 처리한다. 첫 application message는 PREVIEW_ATTACH여야 한다.
// attach가 성립하면 이 goroutine이 tunnel을 운영하고 tunnel이 끝나면 PreviewSession을 끝낸다.
func (g *Gateway) serveData(ws *websocket.Conn, identity ConnectorIdentity) {
	d := &dataConn{ws: ws}
	defer ws.Close()
	log := g.logger.With("connector_id", identity.ConnectorID.String())

	// 종료가 시작되면 attach 전의 connection을 닫는다. attach 전의 connection은 어느 PreviewSession에도 속하지 않으므로 여기서 닫는다.
	// attach된 connection은 PreviewSession이 SERVICE_RESTARTING으로 끝나면서(Close) Lifecycle 통지와 함께 닫으므로 여기서 먼저 닫지 않는다.
	// 먼저 닫으면 PreviewSession이 TUNNEL_CLOSED로 끝나 Connector에 PREVIEW_CLOSE가 가지 않는다.
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-g.done:
			if !d.bound.Load() {
				d.closeNow(closeGoingAway, "server shutting down")
			}
		case <-finished:
		}
	}()

	ws.SetReadLimit(protocol.MaxJSONMessageSize)
	_ = ws.SetReadDeadline(time.Now().Add(g.attachTimeout))
	kind, data, err := ws.ReadMessage()
	if err != nil {
		log.Warn("Connector Preview Data attach 수신 실패", "reason", readFailureReason(err))
		return
	}
	if kind != websocket.TextMessage {
		log.Warn("Connector Preview Data protocol 위반", "reason", "binary_before_attach")
		d.closeNow(closePolicy, "protocol violation")
		return
	}
	env, ok := parseEnvelope(data)
	if !ok || env.Type != typeAttach {
		log.Warn("Connector Preview Data protocol 위반", "reason", "invalid_attach")
		d.closeNow(closePolicy, "protocol violation")
		return
	}
	info, ok := decodeAttach(env.Payload)
	if !ok {
		log.Warn("Connector Preview Data protocol 위반", "reason", "invalid_attach_payload")
		d.closeNow(closePolicy, "protocol violation")
		return
	}

	// Connector identity는 credential 인증 결과다. message가 주장한 값은 기대한 PreviewSession과 대조할 뿐 identity가 아니다.
	// 하나라도 다르면 PreviewSession을 건드리지 않고 이 connection만 거절한다. 다른 Connector나 다른 PreviewSession의 값으로 fallback하지 않는다.
	s := g.lookup(env.PreviewSessionID)
	reject := ""
	switch {
	case s == nil:
		reject = "unknown_session"
	case s.connectorID != identity.ConnectorID:
		reject = "wrong_connector"
	case s.labInstanceID != env.LabInstanceID:
		reject = "wrong_lab_instance"
	case s.generation != env.Generation:
		reject = "wrong_generation"
	case s.vmKey != info.TargetVMKey || s.serverID != info.ProviderServerID:
		reject = "wrong_workspace_vm"
	case s.port != info.TargetPort:
		reject = "wrong_target_port"
	default:
		reject = s.claimAttach(d, identity, env.ReplyToMessageID)
	}
	if reject != "" {
		log.Warn("Connector Preview Data attach 거절", "reason", reject)
		d.closeNow(closePolicy, "attach rejected")
		return
	}
	d.bound.Store(true)
	s.log.Debug("Connector Preview Data attach")

	// attach가 성립했다. 이후 이 connection은 Binary byte stream이며 읽기 deadline이 없다.
	_ = ws.SetReadDeadline(time.Time{})
	ack := outboundEnvelope{
		Type: typeAttached, MessageID: uuid.NewString(), SentAt: time.Now().UTC(), ReplyToMessageID: env.MessageID,
		PreviewSessionID: s.id, LabInstanceID: s.labInstanceID, Generation: s.generation, Payload: attachedPayload{},
	}
	if err := writeJSON(ws, ack); err != nil {
		s.log.Warn("Connector Preview Data 전송 실패", "reason", "attached_write_failed")
		if !s.activated {
			g.endSession(s, End{Reason: EndTunnelClosed}, closeNormal, "attach failed", false)
		} else {
			d.closeNow(closeNormal, "attach failed")
		}
		return
	}

	t := newTunnel(ws, g.maxFrame)
	if !s.setTunnel(t) {
		// attach와 tunnel 준비 사이에 PreviewSession이 끝났다(revoke, 종료 등). 끝낸 쪽이 이 connection을 이미 닫았거나 닫는다.
		t.close(closeNormal, "session ended")
		return
	}
	cause := t.run()
	// tunnel이 끝났다. Connector가 TCP 연결을 닫았거나 Data WSS가 끊겼거나 HTTP transport가 연결을 닫았거나 프로토콜 위반이다.
	// EndTunnelClosed는 TCP/WSS tunnel만 종료하며 logical PreviewSession은 유지한다.
	// 프로토콜 위반 등 비정상 종료는 PreviewSession도 종료한다.
	if cause.Reason == EndTunnelClosed {
		s.clearTunnel(t)
	} else {
		g.endSession(s, End{Reason: cause.Reason, NotifyConnector: cause.Notify}, closeNormal, "tunnel closed", false)
	}
}

func writeJSON(ws *websocket.Conn, v outboundEnvelope) error {
	_ = ws.SetWriteDeadline(time.Now().Add(writeTimeout))
	return ws.WriteJSON(v)
}

// bearerCredential은 정확히 하나의 "Authorization: Bearer <credential>"에서 Credential을 꺼낸다.
func bearerCredential(header http.Header) (string, bool) {
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
	return token, true
}

func offersSubprotocol(r *http.Request) bool {
	for _, offered := range websocket.Subprotocols(r) {
		if offered == protocol.SubprotocolPreviewData {
			return true
		}
	}
	return false
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// readFailureReason은 read 오류를 log용 고정 분류로 바꾼다. 오류 원문은 남기지 않는다.
func readFailureReason(err error) string {
	switch {
	case errors.Is(err, websocket.ErrReadLimit):
		return "message_too_big"
	case isTimeout(err):
		return "read_timeout"
	default:
		return "connection_closed"
	}
}

// CredentialRevoked는 connector.RevokeObserver의 구현이다. Connector의 Control Session이 그 Credential로 PREVIEW_OPEN을 전달한 모든
// PreviewSession(attach를 기다리는 것 포함)을 끝내고 Data WSS를 close 4001로 종료한다. 이미 끝난 PreviewSession은 건드리지 않는다.
func (g *Gateway) CredentialRevoked(credentialID uuid.UUID) {
	g.endMatching(func(s *session) bool { return s.boundCredential() == credentialID },
		End{Reason: EndCredentialRevoked}, closeCredentialRevoked, "credential revoked")
}

// ConnectorRevoked는 connector.RevokeObserver의 구현이다. 그 Connector의 모든 PreviewSession을 끝내고 Data WSS를 close 4001로 종료한다.
func (g *Gateway) ConnectorRevoked(connectorID uuid.UUID) {
	g.endMatching(func(s *session) bool { return s.connectorID == connectorID },
		End{Reason: EndCredentialRevoked}, closeCredentialRevoked, "credential revoked")
}

// ControlSessionEnded는 PREVIEW_OPEN을 전달한 Control Session이 교체되거나 revoke되었음을 알린다(connector.SessionObserver를 옮긴 것).
// 그 Control Session에 묶인 PreviewSession은 trust를 잃는다. attach를 기다리던 것은 정리하고 attach된 tunnel은 close 4002(교체)나
// 4001(revoke)로 종료한다. 이전 Data WSS가 새 Control Session의 PreviewSession을 완료시키지 못한다.
func (g *Gateway) ControlSessionEnded(controlSessionID uuid.UUID, replaced bool) {
	end, code, text := End{Reason: EndCredentialRevoked}, closeCredentialRevoked, "credential revoked"
	if replaced {
		end, code, text = End{Reason: EndControlReplaced}, closeControlReplaced, "control session replaced"
	}
	g.endMatching(func(s *session) bool { return s.boundControlSession() == controlSessionID }, end, code, text)
}

// endMatching은 match인 끝나지 않은 PreviewSession을 모두 끝낸다. Lifecycle에는 Connector에 알릴 필요가 없는 종료로 알린다.
func (g *Gateway) endMatching(match func(*session) bool, end End, code int, text string) {
	g.mu.Lock()
	all := make([]*session, 0, len(g.sessions))
	for _, s := range g.sessions {
		all = append(all, s)
	}
	g.mu.Unlock()
	for _, s := range all {
		if match(s) {
			g.endSession(s, end, code, text, false)
		}
	}
}
