package connector

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/observability"
)

// 이 file은 TerminalSession lifecycle Control(contracts/connector/terminal-control.schema.json)의 routing이다.
// 기존 persistent Control connection을 그대로 사용하며 별도 connection을 만들지 않는다. PTY byte stream은 이 경로에 싣지 않는다
// (Terminal Data WSS가 담당). Browser Cookie, Password, Terminal Session Token은 Connector로 보내지 않는다.

// TerminalCorrelation은 TerminalSession lifecycle message를 구분하는 식별자다. 세 값이 모두 맞아야 같은 TerminalSession이다.
type TerminalCorrelation struct {
	TerminalSessionID string
	LabInstanceID     string
	// Generation은 1 이상이다. Inbound에서 int64로 표현할 수 없는 값은 0이며 어떤 TerminalSession과도 맞지 않는다.
	Generation int64
}

// TerminalOpen은 SendTerminalOpen의 입력이다. SaaS가 권한 검증을 마친 뒤 resolved target만 담는다. wire messageId는 Router가 만든다.
type TerminalOpen struct {
	// ConnectorID는 내부 routing identity다. wire로 받은 값이 아니며 message에 실리지 않는다.
	ConnectorID uuid.UUID
	// RequestID는 원본 HTTP request와 연결할 수 있을 때만 채운다(선택).
	RequestID   string
	Correlation TerminalCorrelation
	// TargetVMKey와 ProviderServerID는 서버가 현재 DB 상태에서 결정한 값이다.
	TargetVMKey      string
	ProviderServerID string
	// Cols와 Rows는 이미 검증된 양의 정수 JSON number 원문이다. Schema에 없는 상한을 만들지 않으려고 값을 다시 만들지 않는다.
	Cols, Rows json.RawMessage
	Trace      TraceContext
}

// TerminalClose는 SendTerminalClose의 입력이다.
type TerminalClose struct {
	ConnectorID uuid.UUID
	RequestID   string
	// OperationID는 Reset/Cleanup처럼 Operation 때문에 종료할 때 상관관계에 쓰는 선택 값이다.
	OperationID string
	Correlation TerminalCorrelation
	// Reason은 SESSION_CLOSED, SESSION_EXPIRED, LAB_RESET, LAB_CLEANUP 같은 종료 원인이다.
	Reason string
	Trace  TraceContext
}

// TerminalInbound는 schema 검증을 통과한 Connector → SaaS TerminalSession lifecycle message의 correlation 정보다.
type TerminalInbound struct {
	MessageID string
	// ReplyToMessageID는 message에 없으면 빈 문자열이다.
	ReplyToMessageID string
	Correlation      TerminalCorrelation
	Trace            TraceContext
}

// TerminalOpenResultPayload는 TERMINAL_OPEN_RESULT의 payload다.
type TerminalOpenResultPayload struct {
	// Outcome은 SUCCEEDED 또는 FAILED다.
	Outcome string
	Error   *protocol.SafeError
}

// TerminalEndedPayload는 TERMINAL_ENDED의 payload다.
type TerminalEndedPayload struct {
	Reason string
	// ExitCode는 Connector가 알려 주지 않았거나 int64로 표현할 수 없으면 nil이다. 알 수 없는 값을 0으로 만들지 않는다.
	ExitCode *int64
	Error    *protocol.SafeError
}

// TerminalOpenOutcome 값이다(terminal-control.schema.json TerminalOpenResultPayload.outcome).
const (
	TerminalOutcomeSucceeded = "SUCCEEDED"
	TerminalOutcomeFailed    = "FAILED"
)

// TerminalSink는 Router가 TerminalSession lifecycle 결과를 넘기는 application 경계다. terminal.Service가 구현한다.
// Operation 결과를 받는 EventSink와 별개다.
//
// HandleTerminalEvent는 해당 Connector Session의 read loop에서, 그 Session이 아직 current임을 보장하는 fence 안에서 호출된다.
// 따라서 오래 걸리는 일(저장소 기록 여러 번, Relay 정리)은 goroutine으로 넘기고 짧게 반환해야 한다.
type TerminalSink interface {
	HandleTerminalEvent(TerminalEvent)
}

// TerminalEvent는 Router가 만든 typed 결과다.
type TerminalEvent interface{ terminalEvent() }

// TerminalOpenResultEvent는 TERMINAL_OPEN_RESULT가 pending TERMINAL_OPEN에 연결되었음을 나타낸다. 그 OPEN의 pending은 여기서 끝난다.
// SUCCEEDED라는 주장만으로 TerminalSession을 성공으로 만들지 않는다. 호출자는 같은 TerminalSession의 Terminal Data WSS가
// 실제로 bind되었는지 별도로 확인한다.
type TerminalOpenResultEvent struct {
	// ConnectorID는 인증된 connection에서 결정된 Connector다. message가 주장한 값이 아니다.
	ConnectorID uuid.UUID
	Correlation TerminalCorrelation
	// RequestMessageID는 이 message가 응답하는 원본 TERMINAL_OPEN의 messageId다.
	RequestMessageID string
	MessageID        string
	// RequestID는 원본 TERMINAL_OPEN에 실은 값이다. Connector가 돌려준 값은 routing에 쓰지 않는다.
	RequestID string
	Payload   TerminalOpenResultPayload
	Trace     TraceContext
}

// TerminalEndedEvent는 schema-valid TERMINAL_ENDED다. 종료 통지는 요청에 대한 응답이 아니므로 pending에 연결하지 않는다.
// ConnectorID는 인증된 connection에서 결정한 값이고 Correlation은 Connector가 주장한 값이다.
// 받는 쪽이 그 TerminalSession이 정말 이 Connector의 것인지, labInstanceId/generation이 맞는지 권위 있는 상태와 대조한다.
type TerminalEndedEvent struct {
	ConnectorID uuid.UUID
	Correlation TerminalCorrelation
	MessageID   string
	Payload     TerminalEndedPayload
	Trace       TraceContext
}

// TerminalUnmatchedEvent는 schema-valid TERMINAL_OPEN_RESULT를 어느 pending TERMINAL_OPEN에도 연결하지 않았음을 나타낸다.
// 다른 pending으로 fallback하지 않으며 payload는 담지 않는다.
type TerminalUnmatchedEvent struct {
	ConnectorID uuid.UUID
	MessageType string
	Reason      UnmatchedReason
	// 아래는 Connector가 주장한 값이다. 관측용이며 길이를 제한한다.
	TerminalSessionID string
	LabInstanceID     string
	Generation        int64
}

func (TerminalOpenResultEvent) terminalEvent() {}
func (TerminalEndedEvent) terminalEvent()      {}
func (TerminalUnmatchedEvent) terminalEvent()  {}

type terminalKey struct {
	connectorID       uuid.UUID
	terminalSessionID string
}

type pendingTerminalOpen struct {
	messageID     string
	requestID     string
	labInstanceID string
	generation    int64
}

// terminalEnvelope는 terminal-control.schema.json의 BaseEnvelope다. terminalSessionId, labInstanceId, generation이 required다.
type terminalEnvelope struct {
	Type              string    `json:"type"`
	MessageID         string    `json:"messageId"`
	SentAt            time.Time `json:"sentAt"`
	RequestID         string    `json:"requestId,omitempty"`
	OperationID       string    `json:"operationId,omitempty"`
	TerminalSessionID string    `json:"terminalSessionId"`
	LabInstanceID     string    `json:"labInstanceId"`
	Generation        int64     `json:"generation"`
	TraceParent       string    `json:"traceparent,omitempty"`
	TraceState        string    `json:"tracestate,omitempty"`
}

type terminalOpenMessage struct {
	terminalEnvelope
	Payload struct {
		TargetVMKey      string          `json:"targetVmKey"`
		ProviderServerID string          `json:"providerServerId"`
		Cols             json.RawMessage `json:"cols"`
		Rows             json.RawMessage `json:"rows"`
	} `json:"payload"`
}

type terminalCloseMessage struct {
	terminalEnvelope
	Payload struct {
		Reason string `json:"reason"`
	} `json:"payload"`
}

func newTerminalEnvelope(kind, messageID, requestID, operationID string, c TerminalCorrelation, t TraceContext) terminalEnvelope {
	trace := NormalizeTrace(t.Traceparent, t.Tracestate)
	return terminalEnvelope{
		Type:              kind,
		MessageID:         messageID,
		SentAt:            time.Now().UTC(),
		RequestID:         requestID,
		OperationID:       operationID,
		TerminalSessionID: c.TerminalSessionID,
		LabInstanceID:     c.LabInstanceID,
		Generation:        c.Generation,
		TraceParent:       trace.Traceparent,
		TraceState:        trace.Tracestate,
	}
}

func validateTerminalCorrelation(c TerminalCorrelation) error {
	if c.TerminalSessionID == "" {
		return invalidCommand("terminalSessionId가 필요합니다")
	}
	if c.LabInstanceID == "" {
		return invalidCommand("labInstanceId가 필요합니다")
	}
	if c.Generation < 1 {
		return invalidCommand("generation은 1 이상이어야 합니다")
	}
	return nil
}

func validateTerminalOpen(open TerminalOpen) error {
	if err := validateTerminalCorrelation(open.Correlation); err != nil {
		return err
	}
	if open.TargetVMKey == "" || open.ProviderServerID == "" {
		return invalidCommand("TERMINAL_OPEN에는 targetVmKey와 providerServerId가 필요합니다")
	}
	if !json.Valid(open.Cols) || !json.Valid(open.Rows) {
		return invalidCommand("TERMINAL_OPEN에는 cols와 rows가 필요합니다")
	}
	return nil
}

// SendTerminalOpen은 TERMINAL_OPEN 하나를 Connector의 current Control connection으로 보낸다.
//
// 반환과 오류의 의미는 SendOperationCommand와 같다. ErrConnectorUnavailable이면 아무것도 쓰지 않았고 pending도 없다.
// ErrSendFailed는 전송 여부가 불명확하며 pending은 제거되었다. 어떤 경우에도 다른 connection으로 자동 재전송하지 않는다.
// 같은 Connector와 TerminalSession에 이미 진행 중인 OPEN이 있으면 ErrDuplicateCorrelation이다.
// TERMINAL_OPEN_RESULT는 replyToMessageId로 이 OPEN에 연결한다.
func (r *Router) SendTerminalOpen(ctx context.Context, open TerminalOpen) (SentMessage, error) {
	if err := ctx.Err(); err != nil {
		return SentMessage{}, err
	}
	if err := validateTerminalOpen(open); err != nil {
		return SentMessage{}, err
	}

	messageID := uuid.NewString()
	msg := terminalOpenMessage{terminalEnvelope: newTerminalEnvelope(protocol.MessageTypeTerminalOpen, messageID, open.RequestID, "", open.Correlation, open.Trace)}
	msg.Payload.TargetVMKey = open.TargetVMKey
	msg.Payload.ProviderServerID = open.ProviderServerID
	msg.Payload.Cols, msg.Payload.Rows = open.Cols, open.Rows
	data, err := marshalOutbound(msg)
	if err != nil {
		return SentMessage{}, err
	}

	key := terminalKey{connectorID: open.ConnectorID, terminalSessionID: open.Correlation.TerminalSessionID}
	log := r.terminalLog(open.ConnectorID, open.Correlation, open.RequestID, "", open.Trace).With("message_type", protocol.MessageTypeTerminalOpen)
	err = r.registry.WithReadyRoute(open.ConnectorID, func(_ Session, route Route) error {
		// pending을 write보다 먼저 등록한다. Connector는 OPEN을 받자마자 Data WSS를 붙이고 OPEN_RESULT를 보낼 수 있다.
		r.mu.Lock()
		if _, exists := r.terminals[key]; exists {
			r.mu.Unlock()
			return ErrDuplicateCorrelation
		}
		r.terminals[key] = pendingTerminalOpen{
			messageID: messageID, requestID: open.RequestID,
			labInstanceID: open.Correlation.LabInstanceID, generation: open.Correlation.Generation,
		}
		r.mu.Unlock()

		return r.write(route, data, func() { r.removeTerminalOpen(key, messageID) })
	})
	if err != nil {
		log.Warn("Connector TERMINAL_OPEN 전송 안 함", "reason", sendFailureReason(err))
		return SentMessage{}, err
	}
	log.Debug("Connector TERMINAL_OPEN 전송")
	return SentMessage{MessageID: messageID}, nil
}

// SendTerminalClose는 TERMINAL_CLOSE 하나를 Connector의 current Control connection으로 보낸다.
// CLOSE는 idempotent lifecycle command이며 별도 응답을 기다리지 않으므로 pending을 만들지 않는다. Connector의 종료 통지는
// TERMINAL_ENDED로 온다. 반환과 오류의 의미는 SendOperationCommand와 같고 자동 재전송하지 않는다.
func (r *Router) SendTerminalClose(ctx context.Context, cl TerminalClose) (SentMessage, error) {
	if err := ctx.Err(); err != nil {
		return SentMessage{}, err
	}
	if err := validateTerminalCorrelation(cl.Correlation); err != nil {
		return SentMessage{}, err
	}
	if cl.Reason == "" {
		return SentMessage{}, invalidCommand("TERMINAL_CLOSE에는 reason이 필요합니다")
	}

	// effective request ID는 wire와 log가 같은 값을 쓰도록 marshal 전에 정한다. 명시한 값이 context 값보다 우선하고,
	// 둘 다 없으면 requestId를 만들지 않는다.
	requestID := cl.RequestID
	if requestID == "" {
		requestID = observability.RequestIDFromContext(ctx)
	}

	messageID := uuid.NewString()
	msg := terminalCloseMessage{terminalEnvelope: newTerminalEnvelope(protocol.MessageTypeTerminalClose, messageID, requestID, cl.OperationID, cl.Correlation, cl.Trace)}
	msg.Payload.Reason = cl.Reason
	data, err := marshalOutbound(msg)
	if err != nil {
		return SentMessage{}, err
	}

	log := r.terminalLog(cl.ConnectorID, cl.Correlation, requestID, cl.OperationID, cl.Trace).With("message_type", protocol.MessageTypeTerminalClose)
	err = r.registry.WithReadyRoute(cl.ConnectorID, func(_ Session, route Route) error {
		return r.write(route, data, func() {})
	})
	if err != nil {
		log.Warn("Connector TERMINAL_CLOSE 전송 안 함", "reason", sendFailureReason(err))
		return SentMessage{}, err
	}
	log.Debug("Connector TERMINAL_CLOSE 전송")
	return SentMessage{MessageID: messageID}, nil
}

func (r *Router) terminalLog(connectorID uuid.UUID, c TerminalCorrelation, requestID, operationID string, trace TraceContext) *slog.Logger {
	log := r.logger.With(
		"connector_id", connectorID.String(),
		"terminal_session_id", boundID(c.TerminalSessionID),
		"lab_instance_id", boundID(c.LabInstanceID),
		"generation", c.Generation,
	)
	if requestID != "" {
		log = log.With("request_id", boundID(requestID))
	}
	if operationID != "" {
		log = log.With("operation_id", boundID(operationID))
	}
	trace = NormalizeTrace(trace.Traceparent, trace.Tracestate)
	if id := trace.TraceID(); id != "" {
		log = log.With("trace_id", id)
	}
	return log
}

// removeTerminalOpen은 messageID가 일치하는 pending만 제거한다.
func (r *Router) removeTerminalOpen(key terminalKey, messageID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.terminals[key]; ok && p.messageID == messageID {
		delete(r.terminals, key)
	}
}

// ForgetTerminalOpen은 connectorID의 TerminalSession에 진행 중인 TERMINAL_OPEN pending을 제거한다. 제거했으면 true다.
// 로컬 추적만 지운다. Connector에 취소를 보내지 않는다. 이후 그 OPEN의 OPEN_RESULT는 TerminalUnmatchedEvent가 된다.
func (r *Router) ForgetTerminalOpen(connectorID uuid.UUID, terminalSessionID string) bool {
	key := terminalKey{connectorID: connectorID, terminalSessionID: terminalSessionID}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.terminals[key]
	delete(r.terminals, key)
	return ok
}

// PendingTerminalOpens는 connectorID의 진행 중인 TERMINAL_OPEN 수다.
func (r *Router) PendingTerminalOpens(connectorID uuid.UUID) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for key := range r.terminals {
		if key.connectorID == connectorID {
			count++
		}
	}
	return count
}

// RouteTerminalOpenResult는 TERMINAL_OPEN_RESULT를 pending TERMINAL_OPEN에 연결하고 그 OPEN을 끝낸다. 연결했으면 true다.
//
// 인증된 ConnectorID, terminalSessionId, labInstanceId, generation, 그리고 replyToMessageId(필수)가 OPEN과 모두 같아야 한다.
// 하나라도 다르면 다른 pending으로 fallback하지 않고 TerminalUnmatchedEvent로 알리며 pending은 그대로 남긴다.
// 이미 끝난 OPEN의 중복이나 늦은 OPEN_RESULT는 pending이 없으므로 연결되지 않는다.
func (r *Router) RouteTerminalOpenResult(connectorID uuid.UUID, in TerminalInbound, payload TerminalOpenResultPayload) bool {
	routed, reason, ok := r.matchTerminalOpen(connectorID, in)
	if !ok {
		r.terminalUnmatched(connectorID, protocol.MessageTypeTerminalOpenResult, in, reason)
		return false
	}
	r.emitTerminal(TerminalOpenResultEvent{
		ConnectorID: connectorID, Correlation: in.Correlation, RequestMessageID: routed.messageID,
		MessageID: in.MessageID, RequestID: routed.requestID, Payload: payload, Trace: in.Trace,
	})
	return true
}

// RouteTerminalEnded는 TERMINAL_ENDED를 TerminalEndedEvent로 넘긴다. 종료 통지는 pending에 연결하지 않는다.
func (r *Router) RouteTerminalEnded(connectorID uuid.UUID, in TerminalInbound, payload TerminalEndedPayload) {
	r.emitTerminal(TerminalEndedEvent{
		ConnectorID: connectorID, Correlation: in.Correlation, MessageID: in.MessageID, Payload: payload, Trace: in.Trace,
	})
}

// RouteTerminalUnrepresentable은 schema-valid이지만 SaaS의 typed model로 표현할 수 없는 값(int64를 넘는 generation)을 담은
// message를 어느 TerminalSession에도 연결하지 않고 TerminalUnmatchedEvent로 알린다.
func (r *Router) RouteTerminalUnrepresentable(connectorID uuid.UUID, messageType string, in TerminalInbound) {
	r.terminalUnmatched(connectorID, messageType, in, ReasonUnrepresentable)
}

func (r *Router) matchTerminalOpen(connectorID uuid.UUID, in TerminalInbound) (pendingTerminalOpen, UnmatchedReason, bool) {
	if in.Correlation.Generation < 1 {
		return pendingTerminalOpen{}, ReasonUnrepresentable, false
	}
	key := terminalKey{connectorID: connectorID, terminalSessionID: in.Correlation.TerminalSessionID}

	r.mu.Lock()
	defer r.mu.Unlock()
	pending, ok := r.terminals[key]
	if !ok {
		return pendingTerminalOpen{}, ReasonNoPending, false
	}
	if in.ReplyToMessageID != pending.messageID {
		return pendingTerminalOpen{}, ReasonReplyMismatch, false
	}
	if in.Correlation.LabInstanceID != pending.labInstanceID || in.Correlation.Generation != pending.generation {
		return pendingTerminalOpen{}, ReasonCorrelationMismatch, false
	}
	delete(r.terminals, key)
	return pending, "", true
}

func (r *Router) emitTerminal(e TerminalEvent) {
	if r.terminalSink != nil {
		r.terminalSink.HandleTerminalEvent(e)
	}
}

// terminalUnmatched는 연결하지 못한 message를 로그와 TerminalUnmatchedEvent로 알린다. payload와 Connector가 준 error 문구는 남기지 않는다.
func (r *Router) terminalUnmatched(connectorID uuid.UUID, messageType string, in TerminalInbound, reason UnmatchedReason) {
	event := TerminalUnmatchedEvent{
		ConnectorID:       connectorID,
		MessageType:       messageType,
		Reason:            reason,
		TerminalSessionID: boundID(in.Correlation.TerminalSessionID),
		LabInstanceID:     boundID(in.Correlation.LabInstanceID),
		Generation:        in.Correlation.Generation,
	}
	trace := NormalizeTrace(in.Trace.Traceparent, in.Trace.Tracestate)
	log := r.logger
	if id := trace.TraceID(); id != "" {
		log = log.With("trace_id", id)
	}
	log.Warn("Connector TerminalSession message를 pending에 연결하지 못함",
		"connector_id", connectorID.String(),
		"message_type", messageType,
		"reason", string(reason),
		"terminal_session_id", event.TerminalSessionID,
		"lab_instance_id", event.LabInstanceID,
		"generation", event.Generation,
	)
	r.emitTerminal(event)
}
