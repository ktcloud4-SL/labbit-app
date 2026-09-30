// Package connectorwss는 contracts/connector/의 Connector Control WSS endpoint(/connector/v1/control) transport다.
//
// 이 package는 WebSocket Upgrade, subprotocol, message 크기 제한, HELLO 검증과 HELLO_ACK 응답만 담당한다.
// Credential 판정은 Authenticator(connector.Service)에 위임하고 SQL/pgx를 알지 못한다.
// Heartbeat/OFFLINE/reconnect lifecycle, command/result routing, Provider/Operation 실행은 이 package의 범위가 아니다.
package connectorwss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

	// closeProtocolError는 contracts/connector/README.md §15의 "복구 불가능한 protocol message 오류"다.
	closeProtocolError = 4004
)

// 연결이 성립한 뒤 HELLO가 계약에 맞지 않을 때 ERROR payload에 싣는 code다(connector.schema.json의 예시 code).
const (
	errorCodeInvalidMessage         = "INVALID_MESSAGE"
	errorCodeUnsupportedMessageType = "UNSUPPORTED_MESSAGE_TYPE"
)

// Authenticator는 handler가 사용하는 인증 use case다. *connector.Service가 구현한다.
type Authenticator interface {
	Authenticate(ctx context.Context, credential connector.Credential) (connector.Principal, error)
}

// Options는 Handler 구성이다.
type Options struct {
	Auth     Authenticator
	Registry *connector.Registry
	// Logger가 nil이면 로그를 남기지 않는다.
	Logger *slog.Logger
	// HelloTimeout은 Upgrade 후 HELLO를 기다리는 시간이다. 0이면 기본값을 사용한다.
	HelloTimeout time.Duration
	// HeartbeatInterval과 OfflineTimeout은 HELLO_ACK로 전달하는 계약 기본값(15초/45초)을 바꾼다.
	// 0이면 기본값을 사용하고, 1초 미만의 값은 허용하지 않는다. 이 값으로 timer를 구동하지는 않는다.
	HeartbeatInterval time.Duration
	OfflineTimeout    time.Duration
}

// Handler는 Connector Control WSS Upgrade 요청을 처리한다.
type Handler struct {
	auth     Authenticator
	registry *connector.Registry
	logger   *slog.Logger
	upgrader websocket.Upgrader

	helloTimeout      time.Duration
	heartbeatInterval time.Duration
	offlineTimeout    time.Duration

	closeOnce sync.Once
	done      chan struct{}
}

func New(opts Options) (*Handler, error) {
	if opts.Auth == nil {
		return nil, errors.New("connectorwss: Authenticator가 필요합니다")
	}
	if opts.Registry == nil {
		return nil, errors.New("connectorwss: Registry가 필요합니다")
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	h := &Handler{
		auth:     opts.Auth,
		registry: opts.Registry,
		logger:   logger,
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
	if h.heartbeatInterval < time.Second || h.offlineTimeout < time.Second {
		return nil, errors.New("connectorwss: HeartbeatInterval과 OfflineTimeout은 1초 이상이어야 합니다")
	}
	return h, nil
}

func durationOrDefault(value, fallback time.Duration) time.Duration {
	if value == 0 {
		return fallback
	}
	return value
}

// Close는 새 Upgrade를 거절하고 열려 있는 Control connection을 1001(Going Away)로 닫는다. 여러 번 호출해도 안전하다.
// http.Server.Shutdown은 hijack된 WebSocket connection을 기다리거나 닫지 않으므로 호출자가 Shutdown 시 함께 호출한다.
func (h *Handler) Close() {
	h.closeOnce.Do(func() { close(h.done) })
}

func (h *Handler) closing() bool {
	select {
	case <-h.done:
		return true
	default:
		return false
	}
}

// ServeHTTP는 Upgrade 전에 Credential과 subprotocol을 확인하므로 인증되지 않은 요청은 WebSocket connection이 되지 못한다.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.closing() {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
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

	defer conn.Close()
	// 모든 JSON Text message는 fragmentation 재조립 후 1 MiB를 넘을 수 없다. 첫 read 전에 설정하며
	// 초과하면 gorilla가 payload를 읽기 전에 1009 close frame을 보내고 ErrReadLimit을 반환한다.
	conn.SetReadLimit(protocol.MaxJSONMessageSize)

	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-h.done:
			closeWith(conn, websocket.CloseGoingAway, "server shutting down")
			_ = conn.Close()
		case <-finished:
		}
	}()

	_ = conn.SetReadDeadline(time.Now().Add(h.helloTimeout))
	messageType, data, err := conn.ReadMessage()
	if err != nil {
		log.Warn("Connector Control HELLO 수신 실패", "reason", readFailureReason(err))
		// 1 MiB 초과(ErrReadLimit)는 gorilla가 이미 1009 close frame을 보냈으므로 추가로 보내지 않는다.
		if isTimeout(err) {
			closeWith(conn, closeProtocolError, "protocol error")
		}
		return
	}
	if messageType != websocket.TextMessage {
		h.rejectHello(conn, log, errorCodeInvalidMessage, "HELLO must be a JSON text message")
		return
	}
	messageID, errorCode := validateHello(data)
	switch errorCode {
	case "":
	case errorCodeUnsupportedMessageType:
		h.rejectHello(conn, log, errorCode, "first message must be HELLO")
		return
	default:
		h.rejectHello(conn, log, errorCode, "HELLO does not match the connector contract")
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
			HeartbeatIntervalSeconds: int(h.heartbeatInterval / time.Second),
			OfflineTimeoutSeconds:    int(h.offlineTimeout / time.Second),
		},
	}
	if err := writeJSON(conn, ack); err != nil {
		log.Warn("Connector Control HELLO_ACK 전송 실패", "reason", "write_failed")
		return
	}
	// handshake 전용 deadline이 이후 read/write로 이어지지 않게 해제한다.
	_ = conn.SetReadDeadline(time.Time{})
	_ = conn.SetWriteDeadline(time.Time{})

	// HELLO_ACK를 보낸 뒤에만 등록한다. 이후 command routing이 ACK보다 먼저 나가지 않게 하기 위해서다.
	_, release := h.registry.Register(principal.ConnectorID)
	defer release()
	log.Info("Connector Control connection 수립")

	// HEARTBEAT 처리(LBT-70)와 command/result routing(LBT-71)은 아직 없다. 읽은 message는 해석하지 않고 버린다.
	// 크기 제한과 control frame(ping/pong/close) 처리는 계속 적용된다.
	for {
		_, reader, err := conn.NextReader()
		if err == nil {
			_, err = io.Copy(io.Discard, reader)
		}
		if err != nil {
			log.Info("Connector Control connection 종료", "reason", readFailureReason(err))
			return
		}
	}
}

// rejectHello는 고정된 설명만 담은 fatal ERROR를 보내고 4004로 닫는다. 입력 값은 응답과 로그에 복사하지 않는다.
func (h *Handler) rejectHello(conn *websocket.Conn, log *slog.Logger, code, message string) {
	log.Warn("Connector Control HELLO 거절", "error_code", code)
	_ = writeJSON(conn, protocol.ProtocolErrorMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:      protocol.MessageTypeError,
			MessageID: uuid.NewString(),
			SentAt:    time.Now().UTC(),
		},
		Payload: protocol.ProtocolErrorPayload{Code: code, Message: message, Fatal: true},
	})
	closeWith(conn, closeProtocolError, "protocol error")
}

// helloFrame과 helloBody는 handshake가 판단에 쓰는 필드만 decode한다. traceparent/tracestate 같은 선택 field는
// 일부러 선언하지 않는다. 잘못된 Trace 값 때문에 HELLO를 거절하지 않고, 알 수 없는 선택 field도 무시한다.
type helloFrame struct {
	Type      string          `json:"type"`
	MessageID string          `json:"messageId"`
	SentAt    time.Time       `json:"sentAt"`
	Payload   json.RawMessage `json:"payload"`
}

type helloBody struct {
	ConnectorVersion string          `json:"connectorVersion"`
	RuntimeID        string          `json:"runtimeId"`
	StartedAt        time.Time       `json:"startedAt"`
	Capabilities     json.RawMessage `json:"capabilities"`
}

// validateHello는 connector.schema.json의 HelloMessage required 조건을 확인한다.
// 성공하면 HELLO의 messageId를, 실패하면 ERROR code를 반환한다.
func validateHello(data []byte) (messageID, errorCode string) {
	var frame helloFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		return "", errorCodeInvalidMessage
	}
	if frame.Type != protocol.MessageTypeHello {
		return "", errorCodeUnsupportedMessageType
	}
	if frame.MessageID == "" || frame.SentAt.IsZero() {
		return "", errorCodeInvalidMessage
	}
	var body helloBody
	if err := json.Unmarshal(frame.Payload, &body); err != nil {
		return "", errorCodeInvalidMessage
	}
	if body.ConnectorVersion == "" || body.RuntimeID == "" || body.StartedAt.IsZero() {
		return "", errorCodeInvalidMessage
	}
	if !validCapabilities(body.Capabilities) {
		return "", errorCodeInvalidMessage
	}
	return frame.MessageID, ""
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

func writeJSON(conn *websocket.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("connectorwss: message marshal: %w", err)
	}
	_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	return conn.WriteMessage(websocket.TextMessage, data)
}

func closeWith(conn *websocket.Conn, code int, reason string) {
	_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(writeTimeout))
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
