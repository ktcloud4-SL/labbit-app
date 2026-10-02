package connector

import (
	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

// Correlation은 LabInstance mutation 하나를 식별하는 업무 correlation이다(contracts/connector/README.md §9).
// 하나의 Operation이 여러 LabInstance로 fan-out될 수 있으므로 OperationID 하나로는 command를 구분하지 못한다.
type Correlation struct {
	OperationID   string
	LabInstanceID string
	// Generation은 1 이상이다. Inbound에서 int64로 표현할 수 없는 값은 0이며 어떤 command와도 맞지 않는다.
	Generation int64
}

// EventSink는 Router가 pending command에 연결한 결과를 받는 application 경계다. 이후 Operation 상태 반영(LBT-18)이 구현한다.
//
// HandleEvent는 해당 Connector Session의 read loop에서, 그 Session이 아직 current임을 보장하는 fence 안에서 호출된다.
// 따라서 짧게 반환해야 하고(예: 저장소 기록 한 번이나 queue 등록), 다른 Session의 교체·revoke를 기다리면 안 된다.
// 서로 다른 Connector(와 재접속 전후의 Session)의 read loop가 동시에 호출하므로 goroutine에 안전해야 한다.
// 같은 Router의 Send*를 호출해도 된다. 한 Session 안에서는 event 순서가 wire 도착 순서와 같지만, 재접속을 넘어서는
// 순서는 보장하지 않는다(늦은 결과는 새 Session으로 도착한다).
type EventSink interface {
	HandleEvent(Event)
}

// Event는 Router가 만든 typed 결과다. 구체 type은 이 파일의 *Event뿐이다.
type Event interface{ routerEvent() }

// Routed는 pending command에 연결된 inbound message의 공통 정보다.
type Routed struct {
	// ConnectorID는 인증된 connection에서 결정된 Connector다. message가 주장한 값이 아니다.
	ConnectorID uuid.UUID
	Correlation Correlation
	// RequestMessageID는 이 message가 응답하는 원본 outbound message(OPERATION_COMMAND 또는 RECONCILE_REQUEST)의 messageId다.
	RequestMessageID string
	// MessageID는 Connector가 보낸 inbound message의 messageId다.
	MessageID string
	// RequestID는 원본 outbound message에 실은 값이다. Connector가 돌려준 값은 routing에 쓰지 않는다.
	RequestID string
	// Trace는 inbound message에서 정상화한 Trace Context다. 없거나 유효하지 않았으면 zero value다.
	Trace TraceContext
}

// OperationAckEvent는 OPERATION_ACK가 pending command에 연결되었음을 나타낸다.
// Payload.Accepted=false이면 Connector가 명령을 받아들이지 않았으므로 그 command의 pending은 여기서 끝난다.
// ACK는 Provider 작업 성공을 뜻하지 않는다.
type OperationAckEvent struct {
	Routed
	Payload protocol.OperationAckPayload
}

// OperationProgressEvent는 OPERATION_PROGRESS가 pending command에 연결되었음을 나타낸다.
type OperationProgressEvent struct {
	Routed
	Payload protocol.OperationProgressPayload
}

// OperationResultEvent는 OPERATION_RESULT가 pending command에 연결되었음을 나타낸다. 이 command의 pending은 여기서 끝난다.
// Outcome이 UNKNOWN이어도 같은 mutation을 자동으로 다시 보내지 않는다. 이후 결정은 Reconciliation을 따른다.
type OperationResultEvent struct {
	Routed
	Payload protocol.OperationResultPayload
}

// ReconcileResultEvent는 RECONCILE_RESULT가 pending RECONCILE_REQUEST에 연결되었음을 나타낸다.
// 이 request의 pending은 여기서 끝난다. Reconciliation은 Provider mutation 재실행이 아니다.
type ReconcileResultEvent struct {
	Routed
	Payload protocol.ReconcileResultPayload
}

// UnmatchedReason은 schema-valid inbound message가 pending command에 연결되지 못한 이유다.
type UnmatchedReason string

const (
	// ReasonNoPending은 그 Connector에 그 correlation(또는 replyToMessageId)의 pending이 없음이다.
	// 다른 Connector, 다른 operationId/labInstanceId/generation, 이미 끝난 command의 중복 terminal result가 여기에 든다.
	ReasonNoPending UnmatchedReason = "no_pending"
	// ReasonReplyMismatch는 pending은 있지만 replyToMessageId가 그 command의 messageId와 다름이다.
	ReasonReplyMismatch UnmatchedReason = "reply_mismatch"
	// ReasonCorrelationMismatch는 replyToMessageId의 pending RECONCILE_REQUEST와 operationId/labInstanceId/generation이 다름이다.
	ReasonCorrelationMismatch UnmatchedReason = "correlation_mismatch"
	// ReasonUnrepresentable은 schema-valid이지만 값이 int64를 넘어 SaaS의 typed model로 표현할 수 없음이다.
	ReasonUnrepresentable UnmatchedReason = "unrepresentable"
)

// UnmatchedEvent는 schema-valid inbound message를 어느 pending command에도 연결하지 않았음을 나타낸다.
// 다른 pending으로 fallback하지 않으며 payload는 담지 않는다. 결과를 잃었을 수 있는 mutation은 Reconciliation으로 확인한다.
type UnmatchedEvent struct {
	ConnectorID uuid.UUID
	// MessageType은 protocol.MessageType* 상수다.
	MessageType string
	Reason      UnmatchedReason
	// 아래는 Connector가 주장한 값이다. 관측용이며 길이를 제한한다. Generation이 0이면 int64로 표현할 수 없는 값이다.
	OperationID   string
	LabInstanceID string
	Generation    int64
}

func (OperationAckEvent) routerEvent()      {}
func (OperationProgressEvent) routerEvent() {}
func (OperationResultEvent) routerEvent()   {}
func (ReconcileResultEvent) routerEvent()   {}
func (UnmatchedEvent) routerEvent()         {}
