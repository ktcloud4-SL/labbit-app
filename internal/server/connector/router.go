package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

var (
	// ErrSendFailed는 protocol-ready connection에 write를 시도했지만 실패했음을 나타낸다. 일부 byte가 전송되었을 수 있어
	// Connector가 command를 받았는지 알 수 없다. Router는 다른 connection으로 재전송하거나 mutation을 다시 보내지 않으며
	// 그 command의 pending은 제거한다. 이후 결정은 호출자의 Reconciliation 정책을 따른다.
	ErrSendFailed = errors.New("connector: message 전송 실패")

	// ErrDuplicateCorrelation은 같은 Connector에 같은 operationId/labInstanceId/generation의 command가 이미 진행 중임을 나타낸다.
	// PROGRESS/RESULT를 구분할 wire key가 없으므로 한 correlation에는 active command를 하나만 둔다(D-20: LabInstance당 mutation 1개).
	ErrDuplicateCorrelation = errors.New("connector: 같은 correlation의 command가 이미 진행 중")
)

// OperationCommand는 SendOperationCommand의 입력이다. wire messageId는 Router가 만든다.
type OperationCommand struct {
	// ConnectorID는 내부 routing identity다. wire로 받은 값이 아니며 message에 실리지 않는다.
	ConnectorID uuid.UUID
	// RequestID는 원본 HTTP request와 연결할 수 있을 때만 채운다(선택).
	RequestID   string
	Correlation Correlation
	Payload     protocol.OperationCommandPayload
	// Trace는 유효한 현재 Trace Context다. 유효하지 않으면 버리며 command를 실패시키지 않는다.
	Trace TraceContext
}

// ReconcileRequest는 SendReconcileRequest의 입력이다. wire messageId는 Router가 만든다.
type ReconcileRequest struct {
	ConnectorID uuid.UUID
	RequestID   string
	Correlation Correlation
	Payload     protocol.ReconcileRequestPayload
	Trace       TraceContext
}

// SentMessage는 전송에 성공한 outbound message의 wire messageId다. Connector의 ACK/RESULT는 이 값을 replyToMessageId로 돌려준다.
type SentMessage struct {
	MessageID string
}

// Inbound는 schema 검증을 통과한 Connector → SaaS message의 correlation 정보다.
type Inbound struct {
	MessageID string
	// ReplyToMessageID는 message에 없으면 빈 문자열이다.
	ReplyToMessageID string
	OperationID      string
	LabInstanceID    string
	// Generation이 0이면 schema-valid이지만 int64로 표현할 수 없는 값이라 어떤 pending과도 맞지 않는다.
	Generation int64
	Trace      TraceContext
}

// RouterOptions는 Router 구성이다.
type RouterOptions struct {
	// Registry는 command를 보낼 protocol-ready route의 원본이다. Connector Control WSS handler와 같은 Registry여야 한다.
	Registry *Registry
	// Sink가 nil이면 event를 버린다.
	Sink EventSink
	// TerminalSink는 TerminalSession lifecycle 결과(TERMINAL_OPEN_RESULT, TERMINAL_ENDED)를 받는다. Operation 결과를 받는 Sink와
	// 별개이며 nil이면 그 결과를 버린다.
	TerminalSink TerminalSink
	// FileSink는 Workspace File lifecycle 결과(FILE_OPEN_RESULT)를 받는다. Operation 결과를 받는 Sink, Terminal 결과를 받는 TerminalSink와
	// 별개이며 nil이면 그 결과를 버린다.
	FileSink FileSink
	// PreviewSink는 PreviewSession lifecycle 결과(PREVIEW_OPEN_RESULT)를 받는다. 다른 Sink와 별개이며 nil이면 그 결과를 버린다.
	PreviewSink PreviewSink
	// Logger가 nil이면 로그를 남기지 않는다.
	Logger *slog.Logger
}

// Router는 Backend ↔ Connector의 command/result correlation 경계다.
//
//   - outbound: SendOperationCommand/SendReconcileRequest는 Connector의 current Session이 protocol-ready(HELLO_ACK 완료)일 때만
//     그 exact Session의 writer로 보낸다. pending correlation을 write보다 먼저 등록해 Connector의 즉각적인 ACK가 유실되지 않게 하고,
//     write가 실패하면 그 command의 pending을 되돌린다. 실패를 다른 connection으로의 재전송이나 mutation 재시도로 바꾸지 않는다.
//   - inbound: Route*는 인증된 ConnectorID와 operationId/labInstanceId/generation, 그리고 messageId/replyToMessageId 관계가
//     모두 맞는 pending에만 결과를 연결한다. 맞지 않으면 다른 pending으로 fallback하지 않고 UnmatchedEvent로 알린다.
//
// pending은 WebSocket Session이 아니라 Connector와 업무 correlation에 묶인다. 재접속은 command retry가 아니므로 Router는
// 어떤 message도 다시 보내지 않고, 재접속한 current connection으로 도착한 늦은 결과도 pending이 남아 있으면 연결한다.
// pending은 프로세스 안의 ephemeral routing 상태이며 durable Operation 상태(operations/operation_items)가 아니다.
// timer, retry, lease를 두지 않으므로 더 이상 기다리지 않을 command는 호출자가 ForgetOperation/ForgetReconcile로 정리한다.
type Router struct {
	registry     *Registry
	sink         EventSink
	terminalSink TerminalSink
	fileSink     FileSink
	previewSink  PreviewSink
	logger       *slog.Logger

	mu         sync.Mutex
	operations map[operationKey]pendingOperation
	reconciles map[reconcileKey]pendingReconcile
	// terminals는 진행 중인 TERMINAL_OPEN이다. 이것도 process 안의 ephemeral routing 상태이며 TerminalSession의 durable 상태가 아니다.
	terminals map[terminalKey]pendingTerminalOpen
	// files는 진행 중인 FILE_OPEN이다. 이것도 process 안의 ephemeral routing 상태이며 File 요청의 durable 상태가 아니다.
	files map[fileKey]pendingFileOpen
	// previews는 진행 중인 PREVIEW_OPEN이다. 이것도 process 안의 ephemeral routing 상태이며 PreviewSession의 durable 상태가 아니다.
	previews map[previewKey]pendingPreviewOpen
}

type operationKey struct {
	connectorID   uuid.UUID
	operationID   string
	labInstanceID string
	generation    int64
}

type pendingOperation struct {
	messageID string
	requestID string
}

type reconcileKey struct {
	connectorID uuid.UUID
	messageID   string
}

type pendingReconcile struct {
	correlation Correlation
	requestID   string
}

// NewRouter는 Router를 만든다.
func NewRouter(opts RouterOptions) (*Router, error) {
	if opts.Registry == nil {
		return nil, errors.New("connector: Router에는 Registry가 필요합니다")
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Router{
		registry:     opts.Registry,
		sink:         opts.Sink,
		terminalSink: opts.TerminalSink,
		fileSink:     opts.FileSink,
		previewSink:  opts.PreviewSink,
		logger:       logger,
		operations:   make(map[operationKey]pendingOperation),
		reconciles:   make(map[reconcileKey]pendingReconcile),
		terminals:    make(map[terminalKey]pendingTerminalOpen),
		files:        make(map[fileKey]pendingFileOpen),
		previews:     make(map[previewKey]pendingPreviewOpen),
	}, nil
}

// Registry는 이 Router가 route를 얻는 Registry다.
func (r *Router) Registry() *Registry { return r.registry }

// SendOperationCommand는 OPERATION_COMMAND 하나(LabInstance mutation 하나)를 Connector에 보낸다.
//
// ErrConnectorUnavailable(ErrNotConnected, ErrNotReady, ErrConnectionClosing)이면 아무것도 쓰지 않았고 pending도 없다.
// ErrInvalidCommand, ErrDuplicateCorrelation도 아무것도 쓰지 않았다. ErrSendFailed는 전송 여부가 불명확하며 pending은 제거되었다.
// ctx는 전송을 시작하기 전에만 확인한다. 전송이 시작되면 부분 write를 만들지 않도록 ctx로 중단하지 않는다.
func (r *Router) SendOperationCommand(ctx context.Context, cmd OperationCommand) (SentMessage, error) {
	if err := ctx.Err(); err != nil {
		return SentMessage{}, err
	}
	if err := validateOperationCommand(cmd); err != nil {
		return SentMessage{}, err
	}

	messageID := uuid.NewString()
	data, err := marshalOutbound(protocol.OperationCommandMessage{
		BaseEnvelope: outboundEnvelope(protocol.MessageTypeOperationCommand, messageID, cmd.RequestID, cmd.Correlation, cmd.Trace),
		Payload:      cmd.Payload,
	})
	if err != nil {
		return SentMessage{}, err
	}

	key := operationKey{
		connectorID:   cmd.ConnectorID,
		operationID:   cmd.Correlation.OperationID,
		labInstanceID: cmd.Correlation.LabInstanceID,
		generation:    cmd.Correlation.Generation,
	}
	log := r.logger.With(
		"connector_id", cmd.ConnectorID.String(),
		"operation_id", boundID(cmd.Correlation.OperationID),
		"lab_instance_id", boundID(cmd.Correlation.LabInstanceID),
		"generation", cmd.Correlation.Generation,
		"message_type", protocol.MessageTypeOperationCommand,
	)
	err = r.registry.WithReadyRoute(cmd.ConnectorID, func(_ Session, route Route) error {
		// pending을 write보다 먼저 등록한다. Connector는 command를 받자마자 ACK를 보낼 수 있다.
		r.mu.Lock()
		if _, exists := r.operations[key]; exists {
			r.mu.Unlock()
			return ErrDuplicateCorrelation
		}
		r.operations[key] = pendingOperation{messageID: messageID, requestID: cmd.RequestID}
		r.mu.Unlock()

		return r.write(route, data, func() { r.removeOperation(key, messageID) })
	})
	if err != nil {
		log.Warn("Connector command 전송 안 함", "reason", sendFailureReason(err))
		return SentMessage{}, err
	}
	log.Debug("Connector command 전송")
	return SentMessage{MessageID: messageID}, nil
}

// SendReconcileRequest는 RECONCILE_REQUEST를 Connector에 보낸다. Reconciliation은 Provider mutation 재실행이 아니다.
// 반환과 오류의 의미는 SendOperationCommand와 같다. RECONCILE_RESULT는 replyToMessageId로 이 request에 연결한다.
func (r *Router) SendReconcileRequest(ctx context.Context, req ReconcileRequest) (SentMessage, error) {
	if err := ctx.Err(); err != nil {
		return SentMessage{}, err
	}
	if err := validateReconcileRequest(req); err != nil {
		return SentMessage{}, err
	}
	payload := req.Payload
	if payload.KnownResources == nil {
		payload.KnownResources = []protocol.ProviderResourceRef{} // required 배열은 null이 아니라 []여야 한다.
	}

	messageID := uuid.NewString()
	data, err := marshalOutbound(protocol.ReconcileRequestMessage{
		BaseEnvelope: outboundEnvelope(protocol.MessageTypeReconcileRequest, messageID, req.RequestID, req.Correlation, req.Trace),
		Payload:      payload,
	})
	if err != nil {
		return SentMessage{}, err
	}

	key := reconcileKey{connectorID: req.ConnectorID, messageID: messageID}
	log := r.logger.With(
		"connector_id", req.ConnectorID.String(),
		"operation_id", boundID(req.Correlation.OperationID),
		"lab_instance_id", boundID(req.Correlation.LabInstanceID),
		"generation", req.Correlation.Generation,
		"message_type", protocol.MessageTypeReconcileRequest,
	)
	err = r.registry.WithReadyRoute(req.ConnectorID, func(_ Session, route Route) error {
		r.mu.Lock()
		r.reconciles[key] = pendingReconcile{correlation: req.Correlation, requestID: req.RequestID}
		r.mu.Unlock()

		return r.write(route, data, func() { r.removeReconcile(key) })
	})
	if err != nil {
		log.Warn("Connector reconcile 요청 전송 안 함", "reason", sendFailureReason(err))
		return SentMessage{}, err
	}
	log.Debug("Connector reconcile 요청 전송")
	return SentMessage{MessageID: messageID}, nil
}

// write는 등록된 pending 뒤에 route로 message를 쓴다. 실패하면 rollback으로 방금 등록한 pending을 되돌린다.
func (r *Router) write(route Route, data []byte, rollback func()) error {
	err := route(data)
	if err == nil {
		return nil
	}
	rollback()
	if errors.Is(err, ErrRouteClosed) {
		return ErrConnectionClosing
	}
	return fmt.Errorf("%w: %w", ErrSendFailed, err)
}

// removeOperation은 messageID가 일치하는 pending만 제거한다. 그 사이 같은 correlation에 다른 command가 등록되었어도 지우지 않는다.
func (r *Router) removeOperation(key operationKey, messageID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.operations[key]; ok && p.messageID == messageID {
		delete(r.operations, key)
	}
}

func (r *Router) removeReconcile(key reconcileKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.reconciles, key)
}

// ForgetOperation은 connectorID의 correlation에 해당하는 pending command를 제거한다. 제거했으면 true다.
// 로컬 추적만 지운다. Connector에 취소를 보내지 않으며 Provider 작업을 중단하거나 되돌리지 않는다.
// 이후 그 command의 ACK/PROGRESS/RESULT는 UnmatchedEvent가 된다.
func (r *Router) ForgetOperation(connectorID uuid.UUID, c Correlation) bool {
	key := operationKey{connectorID: connectorID, operationID: c.OperationID, labInstanceID: c.LabInstanceID, generation: c.Generation}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.operations[key]
	delete(r.operations, key)
	return ok
}

// ForgetReconcile은 SendReconcileRequest가 돌려준 messageID의 pending을 제거한다. 제거했으면 true다.
func (r *Router) ForgetReconcile(connectorID uuid.UUID, requestMessageID string) bool {
	key := reconcileKey{connectorID: connectorID, messageID: requestMessageID}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.reconciles[key]
	delete(r.reconciles, key)
	return ok
}

// PendingCount는 connectorID의 진행 중인 command와 reconcile request 수다.
func (r *Router) PendingCount(connectorID uuid.UUID) (operations, reconciles int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key := range r.operations {
		if key.connectorID == connectorID {
			operations++
		}
	}
	for key := range r.reconciles {
		if key.connectorID == connectorID {
			reconciles++
		}
	}
	return operations, reconciles
}

// RouteOperationAck는 OPERATION_ACK를 그 replyToMessageId의 pending command에 연결한다. 연결했으면 true다.
// replyToMessageId는 필수이며 command의 messageId와 정확히 같아야 한다. accepted=false이면 그 command는 여기서 끝난다.
func (r *Router) RouteOperationAck(connectorID uuid.UUID, in Inbound, payload protocol.OperationAckPayload) bool {
	terminal := !payload.Accepted
	routed, reason, ok := r.matchOperation(connectorID, in, true, terminal)
	if !ok {
		r.unmatched(connectorID, protocol.MessageTypeOperationAck, in, reason)
		return false
	}
	r.emit(OperationAckEvent{Routed: routed, Payload: payload})
	return true
}

// RouteOperationProgress는 OPERATION_PROGRESS를 pending command에 연결한다. 연결했으면 true다.
// replyToMessageId는 필수가 아니지만 있으면 command의 messageId와 같아야 한다.
func (r *Router) RouteOperationProgress(connectorID uuid.UUID, in Inbound, payload protocol.OperationProgressPayload) bool {
	routed, reason, ok := r.matchOperation(connectorID, in, false, false)
	if !ok {
		r.unmatched(connectorID, protocol.MessageTypeOperationProgress, in, reason)
		return false
	}
	r.emit(OperationProgressEvent{Routed: routed, Payload: payload})
	return true
}

// RouteOperationResult는 OPERATION_RESULT를 pending command에 연결하고 그 command를 끝낸다. 연결했으면 true다.
// replyToMessageId는 필수가 아니지만 있으면 command의 messageId와 같아야 한다.
// 이미 끝난 command의 중복 result는 pending이 없으므로 UnmatchedEvent가 된다.
func (r *Router) RouteOperationResult(connectorID uuid.UUID, in Inbound, payload protocol.OperationResultPayload) bool {
	routed, reason, ok := r.matchOperation(connectorID, in, false, true)
	if !ok {
		r.unmatched(connectorID, protocol.MessageTypeOperationResult, in, reason)
		return false
	}
	r.emit(OperationResultEvent{Routed: routed, Payload: payload})
	return true
}

// RouteReconcileResult는 RECONCILE_RESULT를 replyToMessageId의 pending RECONCILE_REQUEST에 연결하고 그 request를 끝낸다.
// 연결했으면 true다. operationId/labInstanceId/generation도 원본 request와 같아야 한다.
func (r *Router) RouteReconcileResult(connectorID uuid.UUID, in Inbound, payload protocol.ReconcileResultPayload) bool {
	routed, reason, ok := r.matchReconcile(connectorID, in)
	if !ok {
		r.unmatched(connectorID, protocol.MessageTypeReconcileResult, in, reason)
		return false
	}
	r.emit(ReconcileResultEvent{Routed: routed, Payload: payload})
	return true
}

// RouteUnrepresentable은 schema-valid이지만 SaaS의 typed model로 표현할 수 없는 값(int64를 넘는 정수)을 담은 message를
// 어느 pending에도 연결하지 않고 UnmatchedEvent로 알린다. pending은 그대로 남는다.
func (r *Router) RouteUnrepresentable(connectorID uuid.UUID, messageType string, in Inbound) {
	r.unmatched(connectorID, messageType, in, ReasonUnrepresentable)
}

// matchOperation은 operation pending을 찾아 검증한다. terminal이면 성공 시 pending을 제거한다.
// requireReply는 replyToMessageId를 필수로 확인한다(OPERATION_ACK). 아니면 있을 때만 확인한다.
func (r *Router) matchOperation(connectorID uuid.UUID, in Inbound, requireReply, terminal bool) (Routed, UnmatchedReason, bool) {
	if in.Generation < 1 {
		return Routed{}, ReasonUnrepresentable, false
	}
	key := operationKey{connectorID: connectorID, operationID: in.OperationID, labInstanceID: in.LabInstanceID, generation: in.Generation}

	r.mu.Lock()
	defer r.mu.Unlock()
	pending, ok := r.operations[key]
	if !ok {
		return Routed{}, ReasonNoPending, false
	}
	if (requireReply || in.ReplyToMessageID != "") && in.ReplyToMessageID != pending.messageID {
		return Routed{}, ReasonReplyMismatch, false
	}
	if terminal {
		delete(r.operations, key)
	}
	return Routed{
		ConnectorID:      connectorID,
		Correlation:      Correlation{OperationID: in.OperationID, LabInstanceID: in.LabInstanceID, Generation: in.Generation},
		RequestMessageID: pending.messageID,
		MessageID:        in.MessageID,
		RequestID:        pending.requestID,
		Trace:            in.Trace,
	}, "", true
}

func (r *Router) matchReconcile(connectorID uuid.UUID, in Inbound) (Routed, UnmatchedReason, bool) {
	if in.ReplyToMessageID == "" {
		return Routed{}, ReasonNoPending, false
	}
	key := reconcileKey{connectorID: connectorID, messageID: in.ReplyToMessageID}
	got := Correlation{OperationID: in.OperationID, LabInstanceID: in.LabInstanceID, Generation: in.Generation}

	r.mu.Lock()
	defer r.mu.Unlock()
	pending, ok := r.reconciles[key]
	if !ok {
		return Routed{}, ReasonNoPending, false
	}
	if pending.correlation != got {
		return Routed{}, ReasonCorrelationMismatch, false
	}
	delete(r.reconciles, key)
	return Routed{
		ConnectorID:      connectorID,
		Correlation:      got,
		RequestMessageID: in.ReplyToMessageID,
		MessageID:        in.MessageID,
		RequestID:        pending.requestID,
		Trace:            in.Trace,
	}, "", true
}

func (r *Router) emit(e Event) {
	if r.sink != nil {
		r.sink.HandleEvent(e)
	}
}

// unmatched는 연결하지 못한 message를 로그와 UnmatchedEvent로 알린다. payload와 Connector가 준 error 문구는 남기지 않는다.
func (r *Router) unmatched(connectorID uuid.UUID, messageType string, in Inbound, reason UnmatchedReason) {
	event := UnmatchedEvent{
		ConnectorID:   connectorID,
		MessageType:   messageType,
		Reason:        reason,
		OperationID:   boundID(in.OperationID),
		LabInstanceID: boundID(in.LabInstanceID),
		Generation:    in.Generation,
	}
	r.logger.Warn("Connector message를 pending command에 연결하지 못함",
		"connector_id", connectorID.String(),
		"message_type", messageType,
		"reason", string(reason),
		"operation_id", event.OperationID,
		"lab_instance_id", event.LabInstanceID,
		"generation", event.Generation,
	)
	r.emit(event)
}

// outboundEnvelope는 Backend가 만드는 message의 공통 Envelope다. Trace는 유효할 때만 싣는다.
func outboundEnvelope(kind, messageID, requestID string, c Correlation, t TraceContext) protocol.BaseEnvelope {
	trace := NormalizeTrace(t.Traceparent, t.Tracestate)
	return protocol.BaseEnvelope{
		Type:          kind,
		MessageID:     messageID,
		SentAt:        time.Now().UTC(),
		RequestID:     requestID,
		OperationID:   c.OperationID,
		LabInstanceID: c.LabInstanceID,
		Generation:    c.Generation,
		TraceParent:   trace.Traceparent,
		TraceState:    trace.Tracestate,
	}
}

// marshalOutbound는 message를 JSON text로 직렬화한다. Connector의 read limit(1 MiB)을 넘으면 보내지 않는다.
func marshalOutbound(message any) ([]byte, error) {
	data, err := json.Marshal(message)
	if err != nil {
		return nil, invalidCommand("message를 직렬화할 수 없습니다")
	}
	if int64(len(data)) > protocol.MaxJSONMessageSize {
		return nil, invalidCommand("message가 JSON Text 한도(1 MiB)를 넘습니다")
	}
	return data, nil
}

// sendFailureReason은 전송 실패를 log용 고정 분류로 바꾼다. 오류 원문은 남기지 않는다.
func sendFailureReason(err error) string {
	switch {
	case errors.Is(err, ErrNotConnected):
		return "not_connected"
	case errors.Is(err, ErrNotReady):
		return "not_ready"
	case errors.Is(err, ErrConnectionClosing):
		return "connection_closing"
	case errors.Is(err, ErrCapabilityUnsupported):
		return "capability_unsupported"
	case errors.Is(err, ErrDuplicateCorrelation):
		return "duplicate_correlation"
	case errors.Is(err, ErrInvalidCommand):
		return "invalid_command"
	default:
		return "write_failed"
	}
}

// maxLoggedIDLen은 Connector가 주장한 ID를 로그와 UnmatchedEvent에 담을 때의 길이 상한(byte)이다.
const maxLoggedIDLen = 128

// boundID는 길이가 제한 없는 opaque ID를 관측용으로 제한한다.
func boundID(id string) string {
	if len(id) <= maxLoggedIDLen {
		return id
	}
	return strings.ToValidUTF8(id[:maxLoggedIDLen], "")
}
