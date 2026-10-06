package connector

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

// 이 file은 PreviewSession의 lifecycle Control(contracts/connector/preview-control.schema.json)의 routing이다.
// 기존 persistent Control connection을 그대로 사용하며 Preview HTTP 요청/응답 본문, 경로, query, Cookie는 이 경로에 싣지 않는다
// (PreviewSession별 Preview Data WSS의 byte stream이 담당). Browser Session Cookie, Preview bootstrap/auth credential, 사설 IP,
// SSH/OpenStack Credential도 Connector로 보내지 않는다.
// PREVIEW_OPEN/PREVIEW_CLOSE는 HELLO에서 protocol.CapabilityPreviewV1을 선언한 Connector에만 보낸다.

// PREVIEW_OPEN_RESULT outcome이다(preview-control.schema.json).
const (
	PreviewOutcomeSucceeded = "SUCCEEDED"
	PreviewOutcomeFailed    = "FAILED"
)

// PreviewCorrelation은 PreviewSession 하나를 구분하는 식별자다. 세 값이 모두 맞아야 같은 PreviewSession이다.
type PreviewCorrelation struct {
	// PreviewSessionID는 SaaS가 PreviewSession마다 발급하는 ID다.
	PreviewSessionID string
	LabInstanceID    string
	// Generation은 1 이상이다. Inbound에서 int64로 표현할 수 없는 값은 0이며 어떤 PreviewSession과도 맞지 않는다.
	Generation int64
}

// PreviewOpen은 SendPreviewOpen의 입력이다. SaaS가 권한 검증과 port 승인을 마친 뒤 resolved Workspace target만 담는다.
// wire messageId는 Router가 만든다.
type PreviewOpen struct {
	// ConnectorID는 내부 routing identity다. wire로 받은 값이 아니며 message에 실리지 않는다.
	ConnectorID uuid.UUID
	// RequestID는 원본 HTTP request와 연결할 수 있을 때만 채운다(선택).
	RequestID   string
	Correlation PreviewCorrelation
	// TargetVMKey, ProviderServerID, TargetPort는 서버가 현재 DB 상태와 명시적 허용 목록으로 결정·승인한 값이다.
	TargetVMKey      string
	ProviderServerID string
	TargetPort       int
	Trace            TraceContext

	// OnRoute가 nil이 아니면 PREVIEW_OPEN을 쓰기 직전, 그 message를 받을 Control Session이 정해진 시점에 그 Session으로 호출된다.
	// Registry가 잡고 있는 route lock 안이므로 이 호출이 끝나기 전에는 그 Session의 교체·revoke가 완료되지 않는다. 그래서 PREVIEW_OPEN을
	// 받은 Connector가 Preview Data WSS를 붙이기 전에 호출자가 "이 PreviewSession은 이 Control Session의 것"이라는 사실을 기록할 수 있고,
	// 기록한 뒤의 교체·revoke는 SessionObserver로 반드시 통지된다. 오래 막으면 안 되며 다른 Session의 Register/Revoke를 기다리면 안 된다.
	// 오류를 반환하면 아무것도 쓰지 않고 pending도 만들지 않으며 SendPreviewOpen이 그 오류를 반환한다.
	OnRoute func(Session) error
}

// PreviewClose는 SendPreviewClose의 입력이다.
type PreviewClose struct {
	ConnectorID uuid.UUID
	RequestID   string
	Correlation PreviewCorrelation
	// Reason은 SESSION_CLOSED, SESSION_EXPIRED, LAB_RESET, LAB_CLEANUP, OPEN_TIMEOUT, REQUEST_CANCELED, OPEN_FAILED,
	// SERVICE_RESTARTING 같은 종료 원인이다.
	Reason string
	Trace  TraceContext
}

// PreviewInbound는 schema 검증을 통과한 Connector → SaaS Preview Control message의 correlation 정보다.
type PreviewInbound struct {
	MessageID string
	// ReplyToMessageID는 message에 없으면 빈 문자열이다.
	ReplyToMessageID string
	Correlation      PreviewCorrelation
	Trace            TraceContext
}

// PreviewOpenResultPayload는 PREVIEW_OPEN_RESULT의 payload다.
type PreviewOpenResultPayload struct {
	// Outcome은 PreviewOutcomeSucceeded 또는 PreviewOutcomeFailed다.
	Outcome string
	Error   *protocol.SafeError
}

// PreviewSink는 Router가 Preview lifecycle 결과(PREVIEW_OPEN_RESULT)를 넘기는 application 경계다. PreviewSession use case가 구현한다.
// Operation 결과를 받는 EventSink, Terminal/File 결과를 받는 TerminalSink/FileSink와 별개다.
//
// HandlePreviewEvent는 해당 Connector Session의 read loop에서, 그 Session이 아직 current임을 보장하는 fence 안에서 호출된다.
// 따라서 오래 걸리는 일은 goroutine으로 넘기고 짧게 반환해야 한다.
type PreviewSink interface {
	HandlePreviewEvent(PreviewEvent)
}

// PreviewEvent는 Router가 만든 typed 결과다.
type PreviewEvent interface{ previewEvent() }

// PreviewOpenResultEvent는 PREVIEW_OPEN_RESULT가 pending PREVIEW_OPEN에 연결되었음을 나타낸다. 그 PREVIEW_OPEN의 pending은 여기서 끝난다.
// SUCCEEDED라는 주장만으로 PreviewSession을 성공으로 만들지 않는다. 호출자는 같은 PreviewSession의 Preview Data WSS가 실제로
// attach되었는지 별도로 확인한다.
type PreviewOpenResultEvent struct {
	// ConnectorID는 인증된 connection에서 결정된 Connector다. message가 주장한 값이 아니다.
	ConnectorID uuid.UUID
	Correlation PreviewCorrelation
	// RequestMessageID는 이 message가 응답하는 원본 PREVIEW_OPEN의 messageId다.
	RequestMessageID string
	MessageID        string
	// RequestID는 원본 PREVIEW_OPEN에 실은 값이다. Connector가 돌려준 값은 routing에 쓰지 않는다.
	RequestID string
	Payload   PreviewOpenResultPayload
	Trace     TraceContext
}

// PreviewUnmatchedEvent는 schema-valid PREVIEW_OPEN_RESULT를 어느 pending PREVIEW_OPEN에도 연결하지 않았음을 나타낸다.
// 다른 pending으로 fallback하지 않으며 payload는 담지 않는다.
type PreviewUnmatchedEvent struct {
	ConnectorID uuid.UUID
	MessageType string
	Reason      UnmatchedReason
	// 아래는 Connector가 주장한 값이다. 관측용이며 길이를 제한한다. Generation이 0이면 int64로 표현할 수 없는 값이다.
	PreviewSessionID string
	LabInstanceID    string
	Generation       int64
}

func (PreviewOpenResultEvent) previewEvent() {}
func (PreviewUnmatchedEvent) previewEvent()  {}

type previewKey struct {
	connectorID      uuid.UUID
	previewSessionID string
}

type pendingPreviewOpen struct {
	messageID     string
	requestID     string
	labInstanceID string
	generation    int64
}

// previewEnvelope는 preview-control.schema.json의 BaseEnvelope다. previewSessionId, labInstanceId, generation이 required다.
type previewEnvelope struct {
	Type             string    `json:"type"`
	MessageID        string    `json:"messageId"`
	SentAt           time.Time `json:"sentAt"`
	RequestID        string    `json:"requestId,omitempty"`
	PreviewSessionID string    `json:"previewSessionId"`
	LabInstanceID    string    `json:"labInstanceId"`
	Generation       int64     `json:"generation"`
	TraceParent      string    `json:"traceparent,omitempty"`
	TraceState       string    `json:"tracestate,omitempty"`
}

type previewOpenMessage struct {
	previewEnvelope
	Payload struct {
		TargetVMKey      string `json:"targetVmKey"`
		ProviderServerID string `json:"providerServerId"`
		TargetPort       int    `json:"targetPort"`
	} `json:"payload"`
}

type previewCloseMessage struct {
	previewEnvelope
	Payload struct {
		Reason string `json:"reason"`
	} `json:"payload"`
}

func newPreviewEnvelope(kind, messageID, requestID string, c PreviewCorrelation, t TraceContext) previewEnvelope {
	trace := NormalizeTrace(t.Traceparent, t.Tracestate)
	return previewEnvelope{
		Type:             kind,
		MessageID:        messageID,
		SentAt:           time.Now().UTC(),
		RequestID:        requestID,
		PreviewSessionID: c.PreviewSessionID,
		LabInstanceID:    c.LabInstanceID,
		Generation:       c.Generation,
		TraceParent:      trace.Traceparent,
		TraceState:       trace.Tracestate,
	}
}

func validatePreviewCorrelation(c PreviewCorrelation) error {
	if c.PreviewSessionID == "" {
		return invalidCommand("previewSessionId가 필요합니다")
	}
	if c.LabInstanceID == "" {
		return invalidCommand("labInstanceId가 필요합니다")
	}
	if c.Generation < 1 {
		return invalidCommand("generation은 1 이상이어야 합니다")
	}
	return nil
}

func validatePreviewOpen(open PreviewOpen) error {
	if err := validatePreviewCorrelation(open.Correlation); err != nil {
		return err
	}
	if open.TargetVMKey == "" || open.ProviderServerID == "" {
		return invalidCommand("PREVIEW_OPEN에는 targetVmKey와 providerServerId가 필요합니다")
	}
	if open.TargetPort < 1 || open.TargetPort > 65535 {
		return invalidCommand("PREVIEW_OPEN의 targetPort는 1~65535여야 합니다")
	}
	return nil
}

// SendPreviewOpen은 PREVIEW_OPEN 하나를 Connector의 current Control connection으로 보낸다. Connector가 HELLO에서
// protocol.CapabilityPreviewV1을 선언하지 않았다면 아무것도 쓰지 않고 ErrCapabilityUnsupported를 반환한다.
//
// 반환과 오류의 의미는 SendOperationCommand와 같다. ErrConnectorUnavailable이면 아무것도 쓰지 않았고 pending도 없다.
// ErrSendFailed는 전송 여부가 불명확하며 pending은 제거되었다. 어떤 경우에도 다른 connection으로 자동 재전송하지 않는다.
// 같은 Connector와 previewSessionId에 이미 진행 중인 PREVIEW_OPEN이 있으면 ErrDuplicateCorrelation이다.
// PREVIEW_OPEN_RESULT는 replyToMessageId로 이 PREVIEW_OPEN에 연결한다.
func (r *Router) SendPreviewOpen(ctx context.Context, open PreviewOpen) (SentMessage, error) {
	if err := ctx.Err(); err != nil {
		return SentMessage{}, err
	}
	if err := validatePreviewOpen(open); err != nil {
		return SentMessage{}, err
	}

	messageID := uuid.NewString()
	msg := previewOpenMessage{previewEnvelope: newPreviewEnvelope(protocol.MessageTypePreviewOpen, messageID, open.RequestID, open.Correlation, open.Trace)}
	msg.Payload.TargetVMKey = open.TargetVMKey
	msg.Payload.ProviderServerID = open.ProviderServerID
	msg.Payload.TargetPort = open.TargetPort
	data, err := marshalOutbound(msg)
	if err != nil {
		return SentMessage{}, err
	}

	key := previewKey{connectorID: open.ConnectorID, previewSessionID: open.Correlation.PreviewSessionID}
	log := r.logger.With(
		"connector_id", open.ConnectorID.String(),
		"preview_session_id", boundID(open.Correlation.PreviewSessionID),
		"lab_instance_id", boundID(open.Correlation.LabInstanceID),
		"generation", open.Correlation.Generation,
		"message_type", protocol.MessageTypePreviewOpen,
	)
	err = r.registry.WithReadyRouteCapability(open.ConnectorID, protocol.CapabilityPreviewV1, func(session Session, route Route) error {
		// pending을 write보다 먼저 등록한다. Connector는 PREVIEW_OPEN을 받자마자 Preview Data WSS를 붙이고 PREVIEW_OPEN_RESULT를 보낼 수 있다.
		r.mu.Lock()
		if _, exists := r.previews[key]; exists {
			r.mu.Unlock()
			return ErrDuplicateCorrelation
		}
		r.previews[key] = pendingPreviewOpen{
			messageID: messageID, requestID: open.RequestID,
			labInstanceID: open.Correlation.LabInstanceID, generation: open.Correlation.Generation,
		}
		r.mu.Unlock()

		// 이 Session이 PREVIEW_OPEN을 받는 Session이다. Connector가 message를 받기 전에 호출자가 이 사실을 기록한다.
		if open.OnRoute != nil {
			if err := open.OnRoute(session); err != nil {
				r.removePreviewOpen(key, messageID)
				return err
			}
		}
		return r.write(route, data, func() { r.removePreviewOpen(key, messageID) })
	})
	if err != nil {
		log.Warn("Connector PREVIEW_OPEN 전송 안 함", "reason", sendFailureReason(err))
		return SentMessage{}, err
	}
	log.Debug("Connector PREVIEW_OPEN 전송")
	return SentMessage{MessageID: messageID}, nil
}

// SendPreviewClose는 PREVIEW_CLOSE 하나를 Connector의 current Control connection으로 보낸다. CLOSE는 idempotent 통지이며 응답을
// 기다리지 않으므로 pending을 만들지 않는다. 반환과 오류의 의미는 SendPreviewOpen과 같고 자동 재전송하지 않는다.
func (r *Router) SendPreviewClose(ctx context.Context, cl PreviewClose) (SentMessage, error) {
	if err := ctx.Err(); err != nil {
		return SentMessage{}, err
	}
	if err := validatePreviewCorrelation(cl.Correlation); err != nil {
		return SentMessage{}, err
	}
	if cl.Reason == "" {
		return SentMessage{}, invalidCommand("PREVIEW_CLOSE에는 reason이 필요합니다")
	}

	messageID := uuid.NewString()
	msg := previewCloseMessage{previewEnvelope: newPreviewEnvelope(protocol.MessageTypePreviewClose, messageID, cl.RequestID, cl.Correlation, cl.Trace)}
	msg.Payload.Reason = cl.Reason
	data, err := marshalOutbound(msg)
	if err != nil {
		return SentMessage{}, err
	}

	log := r.logger.With(
		"connector_id", cl.ConnectorID.String(),
		"preview_session_id", boundID(cl.Correlation.PreviewSessionID),
		"lab_instance_id", boundID(cl.Correlation.LabInstanceID),
		"generation", cl.Correlation.Generation,
		"message_type", protocol.MessageTypePreviewClose,
	)
	err = r.registry.WithReadyRouteCapability(cl.ConnectorID, protocol.CapabilityPreviewV1, func(_ Session, route Route) error {
		return r.write(route, data, func() {})
	})
	if err != nil {
		log.Warn("Connector PREVIEW_CLOSE 전송 안 함", "reason", sendFailureReason(err))
		return SentMessage{}, err
	}
	log.Debug("Connector PREVIEW_CLOSE 전송")
	return SentMessage{MessageID: messageID}, nil
}

// removePreviewOpen은 messageID가 일치하는 pending만 제거한다.
func (r *Router) removePreviewOpen(key previewKey, messageID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.previews[key]; ok && p.messageID == messageID {
		delete(r.previews, key)
	}
}

// ForgetPreviewOpen은 connectorID의 PreviewSession에 진행 중인 PREVIEW_OPEN pending을 제거한다. 제거했으면 true다.
// 로컬 추적만 지운다. Connector에 취소를 보내지 않는다(SendPreviewClose가 한다). 이후 그 PREVIEW_OPEN의 PREVIEW_OPEN_RESULT는
// PreviewUnmatchedEvent가 된다.
func (r *Router) ForgetPreviewOpen(connectorID uuid.UUID, previewSessionID string) bool {
	key := previewKey{connectorID: connectorID, previewSessionID: previewSessionID}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.previews[key]
	delete(r.previews, key)
	return ok
}

// PendingPreviewOpens는 connectorID의 진행 중인 PREVIEW_OPEN 수다.
func (r *Router) PendingPreviewOpens(connectorID uuid.UUID) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for key := range r.previews {
		if key.connectorID == connectorID {
			count++
		}
	}
	return count
}

// RoutePreviewOpenResult는 PREVIEW_OPEN_RESULT를 pending PREVIEW_OPEN에 연결하고 그 PREVIEW_OPEN을 끝낸다. 연결했으면 true다.
//
// 인증된 ConnectorID, previewSessionId, labInstanceId, generation, 그리고 replyToMessageId(필수)가 PREVIEW_OPEN과 모두 같아야 한다.
// 하나라도 다르면 다른 pending으로 fallback하지 않고 PreviewUnmatchedEvent로 알리며 pending은 그대로 남긴다.
// 이미 끝난 PREVIEW_OPEN의 중복이나 늦은 PREVIEW_OPEN_RESULT는 pending이 없으므로 연결되지 않는다.
func (r *Router) RoutePreviewOpenResult(connectorID uuid.UUID, in PreviewInbound, payload PreviewOpenResultPayload) bool {
	routed, reason, ok := r.matchPreviewOpen(connectorID, in)
	if !ok {
		r.previewUnmatched(connectorID, protocol.MessageTypePreviewOpenResult, in, reason)
		return false
	}
	r.emitPreview(PreviewOpenResultEvent{
		ConnectorID: connectorID, Correlation: in.Correlation, RequestMessageID: routed.messageID,
		MessageID: in.MessageID, RequestID: routed.requestID, Payload: payload, Trace: in.Trace,
	})
	return true
}

// RoutePreviewUnrepresentable은 schema-valid이지만 SaaS의 typed model로 표현할 수 없는 값(int64를 넘는 generation)을 담은
// message를 어느 PreviewSession에도 연결하지 않고 PreviewUnmatchedEvent로 알린다.
func (r *Router) RoutePreviewUnrepresentable(connectorID uuid.UUID, messageType string, in PreviewInbound) {
	r.previewUnmatched(connectorID, messageType, in, ReasonUnrepresentable)
}

func (r *Router) matchPreviewOpen(connectorID uuid.UUID, in PreviewInbound) (pendingPreviewOpen, UnmatchedReason, bool) {
	if in.Correlation.Generation < 1 {
		return pendingPreviewOpen{}, ReasonUnrepresentable, false
	}
	key := previewKey{connectorID: connectorID, previewSessionID: in.Correlation.PreviewSessionID}

	r.mu.Lock()
	defer r.mu.Unlock()
	pending, ok := r.previews[key]
	if !ok {
		return pendingPreviewOpen{}, ReasonNoPending, false
	}
	if in.ReplyToMessageID != pending.messageID {
		return pendingPreviewOpen{}, ReasonReplyMismatch, false
	}
	if in.Correlation.LabInstanceID != pending.labInstanceID || in.Correlation.Generation != pending.generation {
		return pendingPreviewOpen{}, ReasonCorrelationMismatch, false
	}
	delete(r.previews, key)
	return pending, "", true
}

func (r *Router) emitPreview(e PreviewEvent) {
	if r.previewSink != nil {
		r.previewSink.HandlePreviewEvent(e)
	}
}

// previewUnmatched는 연결하지 못한 message를 로그와 PreviewUnmatchedEvent로 알린다. payload와 Connector가 준 error 문구는 남기지 않는다.
func (r *Router) previewUnmatched(connectorID uuid.UUID, messageType string, in PreviewInbound, reason UnmatchedReason) {
	event := PreviewUnmatchedEvent{
		ConnectorID:      connectorID,
		MessageType:      messageType,
		Reason:           reason,
		PreviewSessionID: boundID(in.Correlation.PreviewSessionID),
		LabInstanceID:    boundID(in.Correlation.LabInstanceID),
		Generation:       in.Correlation.Generation,
	}
	r.logger.Warn("Connector Preview message를 pending에 연결하지 못함",
		"connector_id", connectorID.String(),
		"message_type", messageType,
		"reason", string(reason),
		"preview_session_id", event.PreviewSessionID,
		"lab_instance_id", event.LabInstanceID,
		"generation", event.Generation,
	)
	r.emitPreview(event)
}
