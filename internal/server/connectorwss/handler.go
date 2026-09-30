// Package connectorwss는 contracts/connector/의 Connector Control WSS endpoint(/connector/v1/control) transport다.
//
// 이 package는 WebSocket Upgrade, subprotocol, message 크기 제한, HELLO 검증과 HELLO_ACK 응답, 그리고 연결 수명을
// 담당한다. 연결 수명은 인증과 Upgrade에 성공한 새 connection의 소유권 확보와 이전 connection 교체(4002),
// HEARTBEAT 수신 기반 OFFLINE 판단과 last_seen 기록, Credential revoke(4001), shutdown이다.
// Credential 판정과 heartbeat 기록은 use case(connector.Service)에 위임하고 SQL/pgx를 알지 못한다.
//
// command/result routing은 connector.Router가 소유한다. 이 package는 HELLO_ACK를 마친 connection의 writer를 Registry에
// protocol-ready route로 등록하고(소유와 ready는 다르다), HELLO 이후의 OPERATION_ACK/PROGRESS/RESULT, RECONCILE_RESULT,
// TerminalSession lifecycle의 TERMINAL_OPEN_RESULT/TERMINAL_ENDED를 exact-case Schema로 검증해 그 Session이 아직 current일 때만
// Router에 넘긴다. Router가 없으면 이 message들은 해석하지 않고 버린다.
// Provider/Operation 실행, durable Operation 상태 반영은 이 package의 범위가 아니다.
package connectorwss

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
)

// Path는 Connector Control WSS endpoint다.
const Path = "/connector/v1/control"

const (
	defaultHelloTimeout      = 10 * time.Second
	defaultHeartbeatInterval = 15 * time.Second
	defaultOfflineTimeout    = 45 * time.Second
	writeTimeout             = 10 * time.Second
	// heartbeatRecordTimeout은 heartbeat 하나를 저장소에 기록하는 데 쓸 수 있는 시간이다.
	heartbeatRecordTimeout = 5 * time.Second

	// closeProtocolError는 contracts/connector/README.md §15의 "복구 불가능한 protocol message 오류"다.
	closeProtocolError = 4004
)

// 연결이 성립한 뒤 HELLO/HEARTBEAT가 계약에 맞지 않을 때 ERROR payload에 싣는 code다(connector.schema.json의 예시 code).
const (
	errorCodeInvalidMessage         = "INVALID_MESSAGE"
	errorCodeUnsupportedMessageType = "UNSUPPORTED_MESSAGE_TYPE"
)

// Authenticator는 handler가 사용하는 인증 use case다. *connector.Service가 구현한다.
type Authenticator interface {
	Authenticate(ctx context.Context, credential connector.Credential) (connector.Principal, error)
}

// HeartbeatRecorder는 유효한 HEARTBEAT를 영속 관측값(connectors.last_seen_at)으로 기록하는 use case다.
// *connector.Service가 구현한다. seenAt은 Backend가 HEARTBEAT를 수신한 서버 시각이다.
// principal의 Credential이나 Connector가 revoke되었다면 기록하지 않고 connector.ErrUnauthenticated를 반환해야 한다.
type HeartbeatRecorder interface {
	RecordHeartbeat(ctx context.Context, principal connector.Principal, seenAt time.Time) error
}

// Options는 Handler 구성이다.
type Options struct {
	Auth       Authenticator
	Heartbeats HeartbeatRecorder
	Registry   *connector.Registry
	// Router가 있으면 HELLO 이후의 command 응답 message(OPERATION_ACK/PROGRESS/RESULT, RECONCILE_RESULT)를 검증해 넘긴다.
	// Router는 이 Options의 Registry와 같은 Registry를 사용해야 한다. nil이면 그 message들을 해석하지 않고 버린다.
	Router *connector.Router
	// Logger가 nil이면 로그를 남기지 않는다.
	Logger *slog.Logger
	// HelloTimeout은 Upgrade 후 HELLO를 기다리는 시간이다. 0이면 기본값을 사용한다.
	HelloTimeout time.Duration
	// HeartbeatInterval은 Connector가 HEARTBEAT를 보낼 주기이고, OfflineTimeout은 유효한 HEARTBEAT 없이
	// Connector를 OFFLINE으로 판단하기까지의 시간이다. 0이면 계약 기본값(15초/45초)을 사용한다.
	// OfflineTimeout은 HeartbeatInterval보다 길어야 한다.
	//
	// HELLO_ACK에는 초 단위 정수(최소 1)로 올려서 전달한다. 1초 미만 값은 이 timer를 짧게 구동하는 용도이며,
	// 그 경우 Connector에 전달되는 값은 실제 timer보다 길다.
	HeartbeatInterval time.Duration
	OfflineTimeout    time.Duration
}

// Handler는 Connector Control WSS Upgrade 요청을 처리한다.
type Handler struct {
	auth       Authenticator
	heartbeats HeartbeatRecorder
	registry   *connector.Registry
	router     *connector.Router
	logger     *slog.Logger
	upgrader   websocket.Upgrader

	helloTimeout      time.Duration
	heartbeatInterval time.Duration
	offlineTimeout    time.Duration

	// mu는 closed와, wg에 요청을 더하는 시점을 Close/Shutdown과 직렬화한다.
	mu     sync.Mutex
	closed bool
	done   chan struct{}
	// wg는 처리 중인 Upgrade 요청과 열린 Control connection을 센다.
	wg sync.WaitGroup
}

func New(opts Options) (*Handler, error) {
	if opts.Auth == nil {
		return nil, errors.New("connectorwss: Authenticator가 필요합니다")
	}
	if opts.Heartbeats == nil {
		return nil, errors.New("connectorwss: HeartbeatRecorder가 필요합니다")
	}
	if opts.Registry == nil {
		return nil, errors.New("connectorwss: Registry가 필요합니다")
	}
	if opts.Router != nil && opts.Router.Registry() != opts.Registry {
		return nil, errors.New("connectorwss: Router는 Handler와 같은 Registry를 사용해야 합니다")
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	h := &Handler{
		auth:       opts.Auth,
		heartbeats: opts.Heartbeats,
		registry:   opts.Registry,
		router:     opts.Router,
		logger:     logger,
		// CheckOrigin은 기본값을 유지한다. Connector는 Browser가 아니므로 Origin을 보내지 않으며,
		// Browser가 보낸 cross-origin Upgrade는 거절된다.
		upgrader: websocket.Upgrader{
			Subprotocols:     []string{protocol.SubprotocolControl},
			HandshakeTimeout: writeTimeout,
		},
		helloTimeout:      durationOrDefault(opts.HelloTimeout, defaultHelloTimeout),
		heartbeatInterval: durationOrDefault(opts.HeartbeatInterval, defaultHeartbeatInterval),
		offlineTimeout:    durationOrDefault(opts.OfflineTimeout, defaultOfflineTimeout),
		done:              make(chan struct{}),
	}
	if h.helloTimeout <= 0 || h.heartbeatInterval <= 0 || h.offlineTimeout <= 0 {
		return nil, errors.New("connectorwss: HelloTimeout, HeartbeatInterval, OfflineTimeout은 0보다 커야 합니다")
	}
	if h.offlineTimeout <= h.heartbeatInterval {
		return nil, errors.New("connectorwss: OfflineTimeout은 HeartbeatInterval보다 길어야 합니다")
	}
	return h, nil
}

func durationOrDefault(value, fallback time.Duration) time.Duration {
	if value == 0 {
		return fallback
	}
	return value
}

// wholeSeconds는 HELLO_ACK가 전달하는 초 단위 정수다. Schema의 minimum(1)을 지키도록 올림하고 최소 1로 한다.
func wholeSeconds(d time.Duration) int {
	seconds := int((d + time.Second - 1) / time.Second)
	return max(seconds, 1)
}

// Close는 새 Upgrade를 거절하고 열려 있는 Control connection을 1001(Going Away)로 닫도록 요청한다. 기다리지 않는다.
// 여러 번 호출해도 안전하다. http.Server.Shutdown은 hijack된 WebSocket connection을 기다리거나 닫지 않으므로
// 호출자가 Shutdown 시 함께 호출한다.
func (h *Handler) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.closed {
		h.closed = true
		close(h.done)
	}
}

// Shutdown은 Close를 호출하고, 열린 Control connection과 진행 중인 요청이 모두 끝나거나 ctx가 끝날 때까지 기다린다.
func (h *Handler) Shutdown(ctx context.Context) error {
	h.Close()
	drained := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(drained)
	}()
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// enter는 요청 하나의 처리를 시작한다. Close 이후에는 false다. true이면 호출자가 h.wg.Done을 호출해야 한다.
func (h *Handler) enter() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	h.wg.Add(1)
	return true
}

// ServeHTTP는 Upgrade 전에 Credential과 subprotocol을 확인하므로 인증되지 않은 요청은 WebSocket connection이 되지 못한다.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.enter() {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	defer h.wg.Done()

	if !websocket.IsWebSocketUpgrade(r) {
		http.Error(w, "websocket upgrade required", http.StatusBadRequest)
		return
	}

	credential, ok := bearerCredential(r.Header)
	if !ok {
		h.logger.Warn("Connector Control 인증 거절", "reason", "missing_or_malformed_authorization")
		unauthorized(w)
		return
	}
	if !offersControlSubprotocol(r) {
		h.logger.Warn("Connector Control subprotocol 거절", "reason", "unsupported_subprotocol")
		http.Error(w, "unsupported subprotocol", http.StatusBadRequest)
		return
	}

	principal, err := h.auth.Authenticate(r.Context(), credential)
	switch {
	case errors.Is(err, connector.ErrUnauthenticated):
		h.logger.Warn("Connector Control 인증 거절", "reason", "unauthenticated")
		unauthorized(w)
		return
	case err != nil:
		// 저장소 오류 원문은 응답에 싣지 않는다. 인증에 실패한 것이 아니므로 401도 아니다.
		h.logger.Error("Connector Control 인증 저장소 오류", "error_code", "DEPENDENCY_UNAVAILABLE")
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade가 이미 응답을 썼다.
		h.logger.Warn("Connector Control Upgrade 실패", "connector_id", principal.ConnectorID.String(), "reason", "upgrade_failed")
		return
	}
	h.serve(conn, principal)
}

func (h *Handler) serve(conn *websocket.Conn, principal connector.Principal) {
	log := h.logger.With("connector_id", principal.ConnectorID.String())
	cc := newControlConn(conn)

	defer conn.Close()
	defer cc.release()
	// 모든 JSON Text message는 fragmentation 재조립 후 1 MiB를 넘을 수 없다. 첫 read 전에 설정하며
	// 초과하면 gorilla가 payload를 읽기 전에 1009 close frame을 보내고 ErrReadLimit을 반환한다.
	conn.SetReadLimit(protocol.MaxJSONMessageSize)

	// shutdown이 시작되면 이 connection을 1001로 닫는다. read loop는 상대의 close 응답이나 connection 종료로 끝난다.
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-h.done:
			cc.close(websocket.CloseGoingAway, "server shutting down")
		case <-finished:
		}
	}()

	// Bearer 인증과 WebSocket Upgrade에 성공한 이 connection이 Connector의 current Control connection이 된다
	// (contracts/connector/README.md §4). HELLO를 기다리기 전에 등록하므로 같은 Connector의 이전 connection은
	// 새 peer가 HELLO를 보내지 않거나 잘못된 HELLO를 보내도 이 호출 안에서 4002로 종료가 요청된다.
	// 등록된 Session은 소유자일 뿐 protocol-ready가 아니다. HELLO_ACK를 보내기 전에는 이 connection으로 나가는
	// message가 없고, command routing도 아래 MarkReady로 이 Session의 route를 등록하기 전에는 이 connection을 사용하지 못한다.
	// HELLO 실패·timeout이면 아래 Release가 이 Session만 제거하며 더 새로운 Session은 건드리지 않는다.
	registration := h.registry.Register(principal, cc.closeFor)
	defer registration.Release()
	log = log.With("session_id", registration.Session().ID.String())
	log.Info("Connector Control connection 소유")

	_ = conn.SetReadDeadline(time.Now().Add(h.helloTimeout))
	messageType, data, err := conn.ReadMessage()
	if err != nil {
		log.Warn("Connector Control HELLO 수신 실패", "reason", readFailureReason(err))
		// 1 MiB 초과(ErrReadLimit)는 gorilla가 이미 1009 close frame을 보냈으므로 추가로 보내지 않는다.
		if isTimeout(err) {
			cc.close(closeProtocolError, "protocol error")
		}
		return
	}
	// HELLO를 기다리는 동안 교체·revoke·shutdown으로 종료가 시작되었다면 이 connection은 HELLO를 처리하지 않는다.
	if cc.isClosing() {
		log.Info("Connector Control connection 종료", "reason", "close_requested_before_hello")
		return
	}
	if messageType != websocket.TextMessage {
		h.reject(cc, log, "HELLO", errorCodeInvalidMessage, "HELLO must be a JSON text message")
		return
	}
	messageID, errorCode := validateHello(data)
	switch errorCode {
	case "":
	case errorCodeUnsupportedMessageType:
		h.reject(cc, log, "HELLO", errorCode, "first message must be HELLO")
		return
	default:
		h.reject(cc, log, "HELLO", errorCode, "HELLO does not match the connector contract")
		return
	}

	now := time.Now().UTC()
	ack := protocol.HelloAckMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:             protocol.MessageTypeHelloAck,
			MessageID:        uuid.NewString(),
			SentAt:           now,
			ReplyToMessageID: messageID,
		},
		Payload: protocol.HelloAckPayload{
			ServerTime:               now,
			HeartbeatIntervalSeconds: wholeSeconds(h.heartbeatInterval),
			OfflineTimeoutSeconds:    wholeSeconds(h.offlineTimeout),
		},
	}
	if err := cc.writeJSON(ack); err != nil {
		log.Warn("Connector Control HELLO_ACK 전송 실패", "reason", "write_failed")
		return
	}

	// HELLO_ACK를 마친 이 Session만 command를 받을 수 있다. 등록 시점부터 Router는 이 exact Session의 writer로 보낸다.
	// 그 사이 교체·revoke되었다면 등록되지 않는다. 이미 종료가 요청된 connection이므로 아래 read loop가 상대의 close 응답이나
	// 종료로 끝나도록 그대로 진행한다(여기서 곧바로 반환하면 close code를 전달하기 전에 TCP를 닫을 수 있다).
	if !registration.MarkReady(cc.route) {
		log.Info("Connector Control connection이 ready 전에 교체·revoke됨")
	}

	// HELLO_ACK를 보낸 시점부터 OFFLINE timeout을 잰다. 이후에는 current Session의 유효한 HEARTBEAT를 받았을 때만 연장한다.
	// WebSocket Ping/Pong, 알 수 없는 message, 잘못된 HEARTBEAT는 연장하지 않는다.
	_ = conn.SetReadDeadline(time.Now().Add(h.offlineTimeout))
	log.Info("Connector Control connection 수립")

	h.readLoop(conn, cc, registration, principal, log)
}

// readLoop는 HELLO 이후의 message를 읽는다. HEARTBEAT와, Router가 있을 때 command 응답 message(routeInbound)만 해석하고
// 나머지는 버린다. 크기 제한과 control frame(ping/pong/close) 처리는 계속 적용된다.
// 유효한 HEARTBEAT만 offline deadline을 연장한다. command 응답 message는 유효해도 연장하지 않는다.
func (h *Handler) readLoop(conn *websocket.Conn, cc *controlConn, registration *connector.Registration, principal connector.Principal, log *slog.Logger) {
	for {
		messageType, data, err := conn.ReadMessage()
		receivedAt := time.Now()
		if err != nil {
			switch {
			case cc.isClosing():
				log.Info("Connector Control connection 종료", "reason", "close_requested")
			case isTimeout(err):
				// 유효한 HEARTBEAT 없이 offline timeout이 지났다. 계약에 OFFLINE 전용 application close code가 없으므로
				// shutdown과 같은 표준 1001로 끝낸다. Registry release는 호출한 쪽의 defer가 한다.
				log.Info("Connector OFFLINE", "reason", "heartbeat_timeout")
				cc.close(websocket.CloseGoingAway, "heartbeat timeout")
			default:
				log.Info("Connector Control connection 종료", "reason", readFailureReason(err))
			}
			return
		}
		// 종료가 시작된 connection의 message는 처리하지 않는다.
		if cc.isClosing() {
			return
		}
		if messageType != websocket.TextMessage {
			continue
		}
		envelope, ok := jsonObject(data)
		if !ok {
			continue
		}
		kind, ok := jsonString(envelope["type"])
		if !ok {
			continue
		}
		switch kind {
		case protocol.MessageTypeHeartbeat:
			// 아래에서 처리한다.
		case protocol.MessageTypeOperationAck, protocol.MessageTypeOperationProgress,
			protocol.MessageTypeOperationResult, protocol.MessageTypeReconcileResult:
			h.routeInbound(cc, registration, principal, log, kind, envelope)
			continue
		case protocol.MessageTypeTerminalOpenResult, protocol.MessageTypeTerminalEnded:
			h.routeTerminalInbound(cc, registration, principal, log, kind, envelope)
			continue
		case protocol.MessageTypeError:
			// Connector의 ERROR는 업무 결과가 아니다. 어떤 pending도 바꾸지 않고 안전한 code만 남긴다.
			log.Warn("Connector ERROR 수신", "error_code", safeErrorCode(envelope))
			continue
		default:
			continue
		}

		if !validHeartbeat(envelope) {
			h.reject(cc, log, "HEARTBEAT", errorCodeInvalidMessage, "HEARTBEAT does not match the connector contract")
			return
		}
		// 이 Session이 아직 current인 동안 유효한 HEARTBEAT를 받았다는 사실 자체가 application liveness의 증거다.
		// OFFLINE은 HEARTBEAT를 받지 못한 상태이므로 deadline은 last_seen 기록의 성패와 무관하게 먼저 연장하고,
		// 그 다음에 last_seen을 기록한다. 같은 IfCurrent 안에서 하므로 교체·revoke된 Session은 deadline도 갱신하지 못하고
		// 교체는 진행 중인 이 처리를 기다린다.
		// last_seen은 Connector의 observedAt이 아니라 Backend가 수신한 서버 시각으로 기록한다.
		recorded, err := registration.IfCurrent(func() error {
			_ = conn.SetReadDeadline(receivedAt.Add(h.offlineTimeout))
			ctx, cancel := context.WithTimeout(context.Background(), heartbeatRecordTimeout)
			defer cancel()
			return h.heartbeats.RecordHeartbeat(ctx, principal, receivedAt.UTC())
		})
		switch {
		case !recorded:
			// 이미 교체되었거나 revoke된 Session이다. 종료가 요청된 connection이므로 deadline도 last_seen도 갱신하지 않는다.
			continue
		case errors.Is(err, connector.ErrUnauthenticated):
			// DB에서 Credential 또는 Connector가 revoke되었다. last_seen을 갱신하지 않고 이 connection을 4001로 끝낸다.
			log.Warn("Connector Control Credential revoke 감지", "reason", "revoked_on_heartbeat")
			cc.close(closeCredentialRevoked, "credential revoked")
			return
		case err != nil:
			// 저장소 장애는 Connector가 OFFLINE인 것이 아니다. HEARTBEAT는 받았으므로 deadline은 이미 연장되었고 연결은 유지한다.
			// last_seen만 기록하지 못했으며, revoke 여부도 이번에는 확인하지 못했다. 오류 원문은 남기지 않는다.
			log.Error("Connector heartbeat 기록 실패", "error_code", "DEPENDENCY_UNAVAILABLE")
			continue
		}
		log.Debug("Connector heartbeat 수신")
	}
}

// reject는 고정된 설명만 담은 fatal ERROR를 보내고 4004로 닫는다. 입력 값은 응답과 로그에 복사하지 않는다.
func (h *Handler) reject(cc *controlConn, log *slog.Logger, what, code, message string) {
	log.Warn("Connector Control "+what+" 거절", "error_code", code)
	sendProtocolError(cc, code, message, true)
	cc.close(closeProtocolError, "protocol error")
}

// sendProtocolError는 ERROR message 하나를 이 connection의 writer 순서대로 보낸다. 실패는 무시한다.
// 입력에서 온 값은 싣지 않는다. 호출자는 고정된 code와 설명만 넘긴다.
func sendProtocolError(cc *controlConn, code, message string, fatal bool) {
	_ = cc.writeJSON(protocol.ProtocolErrorMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:      protocol.MessageTypeError,
			MessageID: uuid.NewString(),
			SentAt:    time.Now().UTC(),
		},
		Payload: protocol.ProtocolErrorPayload{Code: code, Message: message, Fatal: fatal},
	})
}

// routeInbound는 type이 확인된 command 응답 message를 검증해 Router에 넘긴다. Router가 없으면 버린다.
//
// Schema-invalid이면 어떤 pending에도 넘기지 않고 non-fatal ERROR만 보낸다. 계약(README §15)은 4004를 "복구 불가능한
// protocol message 오류"로 정하는데 message 하나가 잘못된 것만으로 연결을 복구 불가능하다고 볼 근거가 없어 연결과 기존
// pending을 유지한다. Schema는 만족하지만 int64를 넘는 정수는 Schema 위반이 아니므로 ERROR 없이 unmatched로 알린다.
// 넘기는 일은 이 Session이 아직 current인 동안에만 한다. 교체·revoke된 Session의 message는 routing하지 않는다.
func (h *Handler) routeInbound(cc *controlConn, registration *connector.Registration, principal connector.Principal, log *slog.Logger, kind string, envelope map[string]json.RawMessage) {
	if h.router == nil {
		return
	}
	in, payload, status := inboundHeader(envelope, kind == protocol.MessageTypeOperationAck || kind == protocol.MessageTypeReconcileResult)

	var route func()
	if status == decodeOK {
		route, status = h.decodeInbound(kind, principal.ConnectorID, in, payload)
	}
	switch status {
	case decodeInvalid:
		log.Warn("Connector Control 응답 message 거절", "message_type", kind, "error_code", errorCodeInvalidMessage)
		sendProtocolError(cc, errorCodeInvalidMessage, kind+" does not match the connector contract", false)
		return
	case decodeUnrepresentable:
		route = func() { h.router.RouteUnrepresentable(principal.ConnectorID, kind, in) }
	}

	current, _ := registration.IfCurrent(func() error {
		route()
		return nil
	})
	if !current {
		log.Debug("교체·revoke된 Session의 응답 message를 routing하지 않음", "message_type", kind)
	}
}

// decodeInbound는 payload를 kind의 typed model로 decode하고, 성공하면 Router로 넘기는 함수를 돌려준다.
func (h *Handler) decodeInbound(kind string, connectorID uuid.UUID, in connector.Inbound, payload map[string]json.RawMessage) (func(), decodeStatus) {
	switch kind {
	case protocol.MessageTypeOperationAck:
		decoded, status := decodeOperationAck(payload)
		return func() { h.router.RouteOperationAck(connectorID, in, decoded) }, status
	case protocol.MessageTypeOperationProgress:
		decoded, status := decodeOperationProgress(payload)
		return func() { h.router.RouteOperationProgress(connectorID, in, decoded) }, status
	case protocol.MessageTypeOperationResult:
		decoded, status := decodeOperationResult(payload)
		return func() { h.router.RouteOperationResult(connectorID, in, decoded) }, status
	default: // protocol.MessageTypeReconcileResult
		decoded, status := decodeReconcileResult(payload)
		return func() { h.router.RouteReconcileResult(connectorID, in, decoded) }, status
	}
}

// validateHello는 connector.schema.json의 HelloMessage required 조건을 확인한다.
// Schema의 property 이름은 대소문자를 구분한다. struct decode는 "TYPE"을 "type"으로 받아들이고 대소문자만 다른 key가
// 정확한 key의 값을 덮어쓸 수 있으므로, 판단에 쓰는 값은 모두 정확한 이름으로 조회한 RawMessage에서 직접 decode한다.
// 정의되지 않은 field(대소문자만 다른 key 포함)와 traceparent/tracestate는 검증하지 않고 무시한다(README §9).
// 성공하면 HELLO의 messageId를, 실패하면 ERROR code를 반환한다.
func validateHello(data []byte) (messageID, errorCode string) {
	envelope, ok := jsonObject(data)
	if !ok {
		return "", errorCodeInvalidMessage
	}
	messageType, ok := jsonString(envelope["type"])
	if !ok {
		return "", errorCodeInvalidMessage
	}
	if messageType != protocol.MessageTypeHello {
		return "", errorCodeUnsupportedMessageType
	}
	messageID, payload, ok := checkEnvelope(envelope)
	if !ok {
		return "", errorCodeInvalidMessage
	}
	if !nonEmptyString(payload["connectorVersion"]) || !nonEmptyString(payload["runtimeId"]) || !validTimestamp(payload["startedAt"]) {
		return "", errorCodeInvalidMessage
	}
	if raw, present := payload["capabilities"]; present && !validCapabilities(raw) {
		return "", errorCodeInvalidMessage
	}
	return messageID, ""
}

// validHeartbeat는 type이 HEARTBEAT로 확인된 envelope가 connector.schema.json의 HeartbeatMessage 조건을 만족하는지 확인한다.
// payload.observedAt은 Connector의 시계 값이라 형식만 확인하며, last_seen 시각으로 쓰지 않는다.
// HELLO와 같은 규칙을 적용한다(정확한 property 이름, 선택 Envelope field 제약, Trace와 알 수 없는 field는 무시).
func validHeartbeat(envelope map[string]json.RawMessage) bool {
	_, payload, ok := checkEnvelope(envelope)
	return ok && validTimestamp(payload["observedAt"])
}

// checkEnvelope는 type을 확인한 뒤 모든 message가 공통으로 지켜야 하는 BaseEnvelope 조건을 확인한다.
// 성공하면 messageId와 payload object를 반환한다.
func checkEnvelope(envelope map[string]json.RawMessage) (messageID string, payload map[string]json.RawMessage, ok bool) {
	messageID, ok = jsonString(envelope["messageId"])
	if !ok || messageID == "" || !validTimestamp(envelope["sentAt"]) || !validEnvelopeOptionals(envelope) {
		return "", nil, false
	}
	payload, ok = jsonObject(envelope["payload"])
	if !ok {
		return "", nil, false
	}
	return messageID, payload, true
}

// validEnvelopeOptionals는 connector.schema.json BaseEnvelope의 선택 field 중 Trace를 제외한 것이
// 없거나, 있으면 Schema 제약을 만족할 때만 true다.
func validEnvelopeOptionals(envelope map[string]json.RawMessage) bool {
	for _, name := range []string{"replyToMessageId", "requestId", "operationId", "labInstanceId"} {
		if raw, ok := envelope[name]; ok && !nonEmptyString(raw) {
			return false
		}
	}
	if raw, ok := envelope["generation"]; ok && !integerAtLeastOne(raw) {
		return false
	}
	return true
}

// jsonObject는 raw가 JSON object일 때만 그 member를 반환한다. null과 배열 등은 object가 아니다.
func jsonObject(raw []byte) (map[string]json.RawMessage, bool) {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil || members == nil {
		return nil, false
	}
	return members, true
}

// jsonString은 raw가 JSON string일 때만 그 값을 반환한다. 없는 field(빈 raw)와 null은 string이 아니다.
func jsonString(raw json.RawMessage) (string, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil || string(raw) == "null" {
		return "", false
	}
	return s, true
}

// nonEmptyString은 raw가 비어 있지 않은 JSON string일 때만 true다(MessageId·ResourceId: string, minLength 1).
func nonEmptyString(raw json.RawMessage) bool {
	s, ok := jsonString(raw)
	return ok && s != ""
}

// validTimestamp는 raw가 RFC 3339 date-time string이고 zero time이 아닐 때만 true다.
func validTimestamp(raw json.RawMessage) bool {
	var t time.Time
	return json.Unmarshal(raw, &t) == nil && !t.IsZero()
}

// validCapabilities는 capabilities가 없거나, 중복 없는 string 배열일 때만 true다.
func validCapabilities(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var capabilities []*string
	if err := json.Unmarshal(raw, &capabilities); err != nil || capabilities == nil {
		return false
	}
	seen := make(map[string]struct{}, len(capabilities))
	for _, capability := range capabilities {
		if capability == nil {
			return false
		}
		if _, dup := seen[*capability]; dup {
			return false
		}
		seen[*capability] = struct{}{}
	}
	return true
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

func offersControlSubprotocol(r *http.Request) bool {
	for _, offered := range websocket.Subprotocols(r) {
		if offered == protocol.SubprotocolControl {
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
