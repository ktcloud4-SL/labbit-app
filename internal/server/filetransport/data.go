package filetransport

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/workspacefile"
)

// 계약(contracts/connector/README.md §15, §7a)의 close code다.
const (
	closeNormal            = websocket.CloseNormalClosure
	closePolicy            = websocket.ClosePolicyViolation
	closeCredentialRevoked = 4001

	// authTimeout은 Connector Credential 인증 한 번에 쓸 수 있는 시간이다.
	authTimeout = 5 * time.Second
	// readDeadlineGrace는 결과를 기다리는 read deadline이 요청 timer(OperationTimeout)보다 늦게 끝나도록 더하는 여유다.
	readDeadlineGrace = 2 * time.Second
)

// Handler는 Connector File Data WSS(DataPath)의 http.Handler다. WSS Upgrade 전에 Connector Credential과 subprotocol을 확인하므로
// 인증되지 않은 요청은 WebSocket connection이 되지 못한다.
func (b *Broker) Handler() http.Handler { return http.HandlerFunc(b.serveHTTP) }

func (b *Broker) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if !b.enter() {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	defer b.wg.Done()

	if !websocket.IsWebSocketUpgrade(r) {
		http.Error(w, "websocket upgrade required", http.StatusBadRequest)
		return
	}
	credential, ok := bearerCredential(r.Header)
	if !ok {
		b.logger.Warn("Connector File Data 인증 거절", "reason", "missing_or_malformed_authorization")
		unauthorized(w)
		return
	}
	if !offersSubprotocol(r) {
		b.logger.Warn("Connector File Data subprotocol 거절", "reason", "unsupported_subprotocol")
		http.Error(w, "unsupported subprotocol", http.StatusBadRequest)
		return
	}

	// 인증을 시작하기 전에 revoke 순번을 기억한다. 인증이 끝난 뒤 connection으로 등록되기 전에 이 trust가 revoke되면 추적 목록에 없어
	// 놓치게 되므로, 등록할 때 그 사이의 revoke를 확인한다(trust.go).
	ticket := b.trust.begin()
	defer ticket.release()

	ctx, cancel := context.WithTimeout(r.Context(), authTimeout)
	principal, err := b.auth.Authenticate(ctx, credential)
	cancel()
	switch {
	case errors.Is(err, connector.ErrUnauthenticated):
		b.logger.Warn("Connector File Data 인증 거절", "reason", "unauthenticated")
		unauthorized(w)
		return
	case err != nil:
		// 저장소 오류 원문은 응답에 싣지 않는다. 인증에 실패한 것이 아니므로 401도 아니다.
		b.logger.Error("Connector File Data 인증 의존성 오류", "error_code", "DEPENDENCY_UNAVAILABLE")
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}

	upgrader := websocket.Upgrader{Subprotocols: []string{protocol.SubprotocolFileData}, HandshakeTimeout: writeTimeout}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		b.logger.Warn("Connector File Data Upgrade 실패", "connector_id", principal.ConnectorID.String(), "reason", "upgrade_failed")
		return
	}
	b.serveData(ws, principal, ticket)
}

// serveData는 Upgrade된 Connector connection 하나를 끝까지 처리한다. 첫 application message는 FILE_DATA_ATTACH여야 한다.
// attach가 성립하면 이 goroutine이 요청 frame을 쓰고 결과 frame을 읽은 뒤 요청에 결과를 전하고 connection을 닫는다.
func (b *Broker) serveData(ws *websocket.Conn, principal connector.Principal, ticket *trustTicket) {
	d := &dataConn{ws: ws, credentialID: principal.CredentialID, connectorID: principal.ConnectorID}
	defer ws.Close()
	log := b.logger.With("connector_id", principal.ConnectorID.String())

	// attach 전부터 trust를 추적한다. FILE_DATA_ATTACH를 기다리는 connection도 Credential이 revoke되면 종료 대상이다.
	if !b.trust.admit(ticket, d) {
		log.Warn("Connector File Data 인증 직후 trust 상실", "reason", "revoked_during_upgrade")
		d.closeNow(closeCredentialRevoked, "credential revoked")
		return
	}
	defer b.trust.forget(d)

	// 종료가 시작되면 이 connection을 닫는다. Close는 pending 요청을 끝내고 bind된 connection도 닫지만 attach 전의 connection은
	// 어느 요청에도 속하지 않으므로 여기서 닫는다.
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-b.done:
			d.closeNow(websocket.CloseGoingAway, "server shutting down")
		case <-finished:
		}
	}()

	ws.SetReadLimit(protocol.MaxJSONMessageSize)
	_ = ws.SetReadDeadline(time.Now().Add(b.attachTimeout))
	kind, data, err := ws.ReadMessage()
	if err != nil {
		log.Warn("Connector File Data attach 수신 실패", "reason", readFailureReason(err))
		return
	}
	if kind != websocket.TextMessage {
		log.Warn("Connector File Data protocol 위반", "reason", "binary_before_attach")
		d.closeNow(closePolicy, "protocol violation")
		return
	}
	env, ok := parseEnvelope(data)
	if !ok || env.Type != typeAttach {
		log.Warn("Connector File Data protocol 위반", "reason", "invalid_attach")
		d.closeNow(closePolicy, "protocol violation")
		return
	}
	info, ok := decodeAttach(env.Payload)
	if !ok {
		log.Warn("Connector File Data protocol 위반", "reason", "invalid_attach_payload")
		d.closeNow(closePolicy, "protocol violation")
		return
	}

	// Connector identity는 credential 인증 결과다. message가 주장한 값은 기대한 요청과 대조할 뿐 identity가 아니다.
	// 하나라도 다르면 요청을 건드리지 않고 이 connection만 거절한다. 다른 Connector가 pending 요청을 취소하거나 완료시킬 수 없다.
	req := b.lookup(env.FileRequestID)
	reject := ""
	switch {
	case req == nil:
		reject = "unknown_request"
	case req.connectorID != principal.ConnectorID:
		reject = "wrong_connector"
	case req.labInstanceID != env.LabInstanceID:
		reject = "wrong_lab_instance"
	case req.generation != env.Generation:
		reject = "wrong_generation"
	case req.vmKey != info.TargetVMKey || req.serverID != info.ProviderServerID:
		reject = "wrong_workspace_vm"
	}
	if reject != "" {
		log.Warn("Connector File Data attach 거절", "reason", reject)
		d.closeNow(closePolicy, "attach rejected")
		return
	}

	if !req.claimAttach(d) {
		// 이미 attach되었거나 끝난 요청이다(중복 attach, 취소·시간 초과 뒤의 늦은 attach).
		req.log.Warn("Connector File Data attach 거절", "reason", "request_not_waiting")
		d.closeNow(closePolicy, "attach rejected")
		return
	}
	// revoke가 이 connection을 요청에서 내릴 수 있도록 알린 뒤 revoke 여부를 확인한다. revoke는 revoked를 먼저 true로 한 뒤 req를 읽는다.
	d.req.Store(req)
	if d.revoked.Load() {
		req.finish(req.failure(workspacefile.ErrTransportUnavailable))
		d.closeNow(closeCredentialRevoked, "credential revoked")
		return
	}
	req.log.Debug("Connector File Data attach")

	out := b.exchange(ws, d, req, env.MessageID)
	req.finish(out)
	// 요청 하나의 exchange가 끝났다. 어떤 결과(Connector가 알린 FAILED 포함)든 정상 종료한다. protocol 위반으로 이미 1008을 보낸 경우
	// 두 번째 close frame은 보내지 않는다.
	d.closeNow(closeNormal, "done")
}

// exchange는 attach된 Data WSS에서 ATTACHED, 요청 frame, 결과 frame을 처리하고 요청의 결과를 반환한다.
// 계약을 어긴 frame(순서, correlation, Schema, 크기)은 성공으로 처리하지 않고 연결을 1008로 끝낸다.
func (b *Broker) exchange(ws *websocket.Conn, d *dataConn, req *request, attachMessageID string) outcome {
	// 요청의 시간 제한은 run의 timer가 정한다. timer가 끝나면 abort가 FILE_CLOSE를 보내고 이 connection을 닫는다. 여기의 read deadline은
	// 그 timer가 어떤 이유로 동작하지 못해도 이 goroutine이 남지 않게 하는 안전망이므로 timer보다 늦게 끝나야 한다.
	// 그렇지 않으면 이 goroutine이 먼저 요청을 끝내 Connector에 FILE_CLOSE가 가지 않을 수 있다.
	deadline := time.Now().Add(b.opTimeout + readDeadlineGrace)
	fail := func(err error) outcome { return req.failure(err) }
	violation := func(reason string) outcome {
		req.log.Warn("Connector File Data protocol 위반", "reason", reason)
		d.closeNow(closePolicy, "protocol violation")
		return fail(workspacefile.ErrTransportUnavailable)
	}

	if err := writeFrame(ws, websocket.TextMessage, mustMarshal(outboundEnvelope{
		Type: typeAttached, MessageID: uuid.NewString(), SentAt: time.Now().UTC(), ReplyToMessageID: attachMessageID,
		FileRequestID: req.id, LabInstanceID: req.labInstanceID, Generation: req.generation, Payload: attachedPayload{},
	})); err != nil {
		req.log.Warn("Connector File Data 전송 실패", "reason", "attached_write_failed")
		return fail(workspacefile.ErrTransportUnavailable)
	}

	// 요청 frame은 정확히 하나다. 요청 messageId는 결과의 replyToMessageId와 대조한다.
	requestID := uuid.NewString()
	envelope := outboundEnvelope{
		MessageID: requestID, SentAt: time.Now().UTC(),
		FileRequestID: req.id, LabInstanceID: req.labInstanceID, Generation: req.generation,
	}
	switch req.op {
	case opTree:
		envelope.Type, envelope.Payload = typeTree, treePayload{Path: req.path}
	case opRead:
		envelope.Type, envelope.Payload = typeRead, readPayload{Path: req.path, MaxBytes: req.maxBytes}
	default:
		envelope.Type, envelope.Payload = typeSave, savePayload{Path: req.path, ExpectedRevision: req.expected, Size: int64(len(req.content))}
	}
	if err := writeFrame(ws, websocket.TextMessage, mustMarshal(envelope)); err != nil {
		req.log.Warn("Connector File Data 전송 실패", "reason", "request_write_failed")
		return fail(workspacefile.ErrTransportUnavailable)
	}
	if req.op == opSave {
		// 본문 Binary frame을 쓰기 시작하면 Connector가 저장했는지 알 수 없는 실패가 생길 수 있다. 이후의 실패는 자동으로 다시 보내지 않는다.
		req.saveSent.Store(true)
		if err := writeFrame(ws, websocket.BinaryMessage, req.content); err != nil {
			req.log.Warn("Connector File Data 전송 실패", "reason", "body_write_failed")
			return fail(workspacefile.ErrTransportUnavailable)
		}
	}

	// 결과 frame 하나를 기다린다. 다른 어떤 frame도 이 요청을 완료시키지 않는다.
	_ = ws.SetReadDeadline(deadline)
	ws.SetReadLimit(protocol.MaxJSONMessageSize)
	kind, data, err := ws.ReadMessage()
	if err != nil {
		req.log.Warn("Connector File Data 결과 수신 실패", "reason", readFailureReason(err))
		return fail(workspacefile.ErrTransportUnavailable)
	}
	if kind != websocket.TextMessage {
		return violation("binary_before_result")
	}
	env, ok := parseEnvelope(data)
	if !ok {
		return violation("malformed_result")
	}
	if env.Type == typeError {
		// Connector의 ERROR는 업무 결과가 아니다. 요청을 성공시키지 않는다. 안전한 분류만 남긴다.
		req.log.Warn("Connector File Data ERROR 수신")
		d.closeNow(closePolicy, "connector reported an error")
		return fail(workspacefile.ErrTransportUnavailable)
	}
	if env.Type != resultType(req.op) {
		return violation("unexpected_message_type")
	}
	if env.FileRequestID != req.id || env.LabInstanceID != req.labInstanceID || env.Generation != req.generation {
		return violation("correlation_mismatch")
	}
	if env.ReplyToMessageID != requestID {
		return violation("reply_mismatch")
	}
	result, ok := decodeOutcome(env.Payload)
	if !ok {
		return violation("malformed_result")
	}
	if !result.Succeeded {
		// Connector가 요청을 처리하지 못했다고 알렸다. 확정된 업무 결과이며 Save라도 쓰지 않았다는 뜻이다.
		return outcome{err: mapConnectorError(result.ErrorCode, req.op)}
	}

	switch req.op {
	case opTree:
		entries, ok := decodeTreeEntries(env.Payload)
		if !ok {
			return violation("malformed_result")
		}
		return outcome{entries: entries}

	case opSave:
		revision, ok := decodeRevision(env.Payload)
		if !ok {
			return violation("malformed_result")
		}
		return outcome{revision: revision}

	default: // opRead
		revision, ok := decodeRevision(env.Payload)
		if !ok {
			return violation("malformed_result")
		}
		size, inRange, ok := decodeReadSize(env.Payload)
		if !ok {
			return violation("malformed_result")
		}
		if !inRange || size > req.maxBytes {
			// 요청한 한도보다 크다고 Connector가 알렸다. 본문을 읽지 않는다.
			return outcome{err: workspacefile.ErrTooLarge}
		}
		// 본문은 선언한 크기의 Binary frame 하나다. size를 넘는 frame은 읽지 않고 거절한다(limit이 0이면 무제한이므로 size+1).
		ws.SetReadLimit(size + 1)
		kind, body, err := ws.ReadMessage()
		if err != nil {
			req.log.Warn("Connector File Data 본문 수신 실패", "reason", readFailureReason(err))
			return fail(workspacefile.ErrTransportUnavailable)
		}
		if kind != websocket.BinaryMessage || int64(len(body)) != size {
			return violation("body_frame_mismatch")
		}
		return outcome{data: workspacefile.FileData{Content: body, Revision: revision}}
	}
}

func resultType(op operation) string {
	switch op {
	case opTree:
		return typeTreeResult
	case opRead:
		return typeReadResult
	default:
		return typeSaveResult
	}
}

// writeFrame은 frame 하나를 쓴다. 한 connection에는 이 함수를 호출하는 goroutine이 하나뿐이다(close frame은 다른 goroutine이 보낼 수 있다).
func writeFrame(ws *websocket.Conn, kind int, data []byte) error {
	_ = ws.SetWriteDeadline(time.Now().Add(writeTimeout))
	return ws.WriteMessage(kind, data)
}

func mustMarshal(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		// 고정된 구조체만 직렬화한다. 실패는 프로그램 오류다.
		panic("filetransport: " + err.Error())
	}
	return data
}

// bearerCredential은 정확히 하나의 "Authorization: Bearer <credential>"에서 Credential을 꺼낸다.
func bearerCredential(header http.Header) (connector.Credential, bool) {
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
	return connector.Credential(token), true
}

func offersSubprotocol(r *http.Request) bool {
	for _, offered := range websocket.Subprotocols(r) {
		if offered == protocol.SubprotocolFileData {
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
