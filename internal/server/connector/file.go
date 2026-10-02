package connector

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

// 이 file은 Workspace File 요청의 lifecycle Control(contracts/connector/file-control.schema.json)의 routing이다.
// 기존 persistent Control connection을 그대로 사용하며 파일 경로, 디렉터리 목록, 본문은 이 경로에 싣지 않는다
// (요청별 File Data WSS가 담당). Browser Cookie, Password, Session Token도 Connector로 보내지 않는다.
// FILE_OPEN/FILE_CLOSE는 HELLO에서 protocol.CapabilityFileV1을 선언한 Connector에만 보낸다.

// File 작업 종류와 FILE_OPEN_RESULT outcome이다(file-control.schema.json).
const (
	FileOperationTree = "TREE"
	FileOperationRead = "READ"
	FileOperationSave = "SAVE"

	FileOutcomeSucceeded = "SUCCEEDED"
	FileOutcomeFailed    = "FAILED"
)

// FileCorrelation은 File 요청 하나를 구분하는 식별자다. 세 값이 모두 맞아야 같은 요청이다.
type FileCorrelation struct {
	// FileRequestID는 SaaS가 요청마다 발급하는 ID다.
	FileRequestID string
	LabInstanceID string
	// Generation은 1 이상이다. Inbound에서 int64로 표현할 수 없는 값은 0이며 어떤 요청과도 맞지 않는다.
	Generation int64
}

// FileOpen은 SendFileOpen의 입력이다. SaaS가 권한 검증을 마친 뒤 resolved Workspace target만 담는다. wire messageId는 Router가 만든다.
type FileOpen struct {
	// ConnectorID는 내부 routing identity다. wire로 받은 값이 아니며 message에 실리지 않는다.
	ConnectorID uuid.UUID
	// RequestID는 원본 HTTP request와 연결할 수 있을 때만 채운다(선택).
	RequestID   string
	Correlation FileCorrelation
	// Operation은 FileOperation* 중 하나다.
	Operation string
	// TargetVMKey와 ProviderServerID는 서버가 현재 DB 상태에서 결정한 값이다.
	TargetVMKey      string
	ProviderServerID string
	Trace            TraceContext
}

// FileClose는 SendFileClose의 입력이다.
type FileClose struct {
	ConnectorID uuid.UUID
	RequestID   string
	Correlation FileCorrelation
	// Reason은 REQUEST_CANCELED, REQUEST_TIMEOUT, WORKSPACE_CHANGED, SERVICE_RESTARTING 같은 종료 원인이다.
	Reason string
	Trace  TraceContext
}

// FileInbound는 schema 검증을 통과한 Connector → SaaS File Control message의 correlation 정보다.
type FileInbound struct {
	MessageID string
	// ReplyToMessageID는 message에 없으면 빈 문자열이다.
	ReplyToMessageID string
	Correlation      FileCorrelation
	Trace            TraceContext
}

// FileOpenResultPayload는 FILE_OPEN_RESULT의 payload다.
type FileOpenResultPayload struct {
	// Outcome은 FileOutcomeSucceeded 또는 FileOutcomeFailed다.
	Outcome string
	Error   *protocol.SafeError
}

// FileSink는 Router가 File lifecycle 결과를 넘기는 application 경계다. File transport adapter가 구현한다.
// Operation 결과를 받는 EventSink, Terminal 결과를 받는 TerminalSink와 별개다.
//
// HandleFileEvent는 해당 Connector Session의 read loop에서, 그 Session이 아직 current임을 보장하는 fence 안에서 호출된다.
// 따라서 오래 걸리는 일은 goroutine으로 넘기고 짧게 반환해야 한다.
type FileSink interface {
	HandleFileEvent(FileEvent)
}

// FileEvent는 Router가 만든 typed 결과다.
type FileEvent interface{ fileEvent() }

// FileOpenResultEvent는 FILE_OPEN_RESULT가 pending FILE_OPEN에 연결되었음을 나타낸다. 그 FILE_OPEN의 pending은 여기서 끝난다.
// SUCCEEDED라는 주장만으로 요청을 성공으로 만들지 않는다. 호출자는 같은 요청의 File Data WSS가 실제로 attach되었는지 별도로 확인한다.
type FileOpenResultEvent struct {
	// ConnectorID는 인증된 connection에서 결정된 Connector다. message가 주장한 값이 아니다.
	ConnectorID uuid.UUID
	Correlation FileCorrelation
	// RequestMessageID는 이 message가 응답하는 원본 FILE_OPEN의 messageId다.
	RequestMessageID string
	MessageID        string
	// RequestID는 원본 FILE_OPEN에 실은 값이다. Connector가 돌려준 값은 routing에 쓰지 않는다.
	RequestID string
	Payload   FileOpenResultPayload
	Trace     TraceContext
}

// FileUnmatchedEvent는 schema-valid FILE_OPEN_RESULT를 어느 pending FILE_OPEN에도 연결하지 않았음을 나타낸다.
// 다른 pending으로 fallback하지 않으며 payload는 담지 않는다.
type FileUnmatchedEvent struct {
	ConnectorID uuid.UUID
	MessageType string
	Reason      UnmatchedReason
	// 아래는 Connector가 주장한 값이다. 관측용이며 길이를 제한한다. Generation이 0이면 int64로 표현할 수 없는 값이다.
	FileRequestID string
	LabInstanceID string
	Generation    int64
}

func (FileOpenResultEvent) fileEvent() {}
func (FileUnmatchedEvent) fileEvent()  {}

type fileKey struct {
	connectorID   uuid.UUID
	fileRequestID string
}

type pendingFileOpen struct {
	messageID     string
	requestID     string
	labInstanceID string
	generation    int64
}

// fileEnvelope는 file-control.schema.json의 BaseEnvelope다. fileRequestId, labInstanceId, generation이 required다.
type fileEnvelope struct {
	Type          string    `json:"type"`
	MessageID     string    `json:"messageId"`
	SentAt        time.Time `json:"sentAt"`
	RequestID     string    `json:"requestId,omitempty"`
	FileRequestID string    `json:"fileRequestId"`
	LabInstanceID string    `json:"labInstanceId"`
	Generation    int64     `json:"generation"`
	TraceParent   string    `json:"traceparent,omitempty"`
	TraceState    string    `json:"tracestate,omitempty"`
}

type fileOpenMessage struct {
	fileEnvelope
	Payload struct {
		Operation        string `json:"operation"`
		TargetVMKey      string `json:"targetVmKey"`
		ProviderServerID string `json:"providerServerId"`
	} `json:"payload"`
}

type fileCloseMessage struct {
	fileEnvelope
	Payload struct {
		Reason string `json:"reason"`
	} `json:"payload"`
}

func newFileEnvelope(kind, messageID, requestID string, c FileCorrelation, t TraceContext) fileEnvelope {
	trace := NormalizeTrace(t.Traceparent, t.Tracestate)
	return fileEnvelope{
		Type:          kind,
		MessageID:     messageID,
		SentAt:        time.Now().UTC(),
		RequestID:     requestID,
		FileRequestID: c.FileRequestID,
		LabInstanceID: c.LabInstanceID,
		Generation:    c.Generation,
		TraceParent:   trace.Traceparent,
		TraceState:    trace.Tracestate,
	}
}

func validateFileCorrelation(c FileCorrelation) error {
	if c.FileRequestID == "" {
		return invalidCommand("fileRequestId가 필요합니다")
	}
	if c.LabInstanceID == "" {
		return invalidCommand("labInstanceId가 필요합니다")
	}
	if c.Generation < 1 {
		return invalidCommand("generation은 1 이상이어야 합니다")
	}
	return nil
}

func validateFileOpen(open FileOpen) error {
	if err := validateFileCorrelation(open.Correlation); err != nil {
		return err
	}
	switch open.Operation {
	case FileOperationTree, FileOperationRead, FileOperationSave:
	default:
		return invalidCommand("FILE_OPEN의 operation이 올바르지 않습니다")
	}
	if open.TargetVMKey == "" || open.ProviderServerID == "" {
		return invalidCommand("FILE_OPEN에는 targetVmKey와 providerServerId가 필요합니다")
	}
	return nil
}

// SendFileOpen은 FILE_OPEN 하나를 Connector의 current Control connection으로 보낸다. Connector가 HELLO에서 protocol.CapabilityFileV1을
// 선언하지 않았다면 아무것도 쓰지 않고 ErrCapabilityUnsupported를 반환한다.
//
// 반환과 오류의 의미는 SendOperationCommand와 같다. ErrConnectorUnavailable이면 아무것도 쓰지 않았고 pending도 없다.
// ErrSendFailed는 전송 여부가 불명확하며 pending은 제거되었다. 어떤 경우에도 다른 connection으로 자동 재전송하지 않는다.
// 같은 Connector와 fileRequestId에 이미 진행 중인 FILE_OPEN이 있으면 ErrDuplicateCorrelation이다.
// FILE_OPEN_RESULT는 replyToMessageId로 이 FILE_OPEN에 연결한다.
func (r *Router) SendFileOpen(ctx context.Context, open FileOpen) (SentMessage, error) {
	if err := ctx.Err(); err != nil {
		return SentMessage{}, err
	}
	if err := validateFileOpen(open); err != nil {
		return SentMessage{}, err
	}

	messageID := uuid.NewString()
	msg := fileOpenMessage{fileEnvelope: newFileEnvelope(protocol.MessageTypeFileOpen, messageID, open.RequestID, open.Correlation, open.Trace)}
	msg.Payload.Operation = open.Operation
	msg.Payload.TargetVMKey = open.TargetVMKey
	msg.Payload.ProviderServerID = open.ProviderServerID
	data, err := marshalOutbound(msg)
	if err != nil {
		return SentMessage{}, err
	}

	key := fileKey{connectorID: open.ConnectorID, fileRequestID: open.Correlation.FileRequestID}
	log := r.logger.With(
		"connector_id", open.ConnectorID.String(),
		"file_request_id", boundID(open.Correlation.FileRequestID),
		"lab_instance_id", boundID(open.Correlation.LabInstanceID),
		"generation", open.Correlation.Generation,
		"message_type", protocol.MessageTypeFileOpen,
	)
	err = r.registry.WithReadyRouteCapability(open.ConnectorID, protocol.CapabilityFileV1, func(_ Session, route Route) error {
		// pending을 write보다 먼저 등록한다. Connector는 FILE_OPEN을 받자마자 File Data WSS를 붙이고 FILE_OPEN_RESULT를 보낼 수 있다.
		r.mu.Lock()
		if _, exists := r.files[key]; exists {
			r.mu.Unlock()
			return ErrDuplicateCorrelation
		}
		r.files[key] = pendingFileOpen{
			messageID: messageID, requestID: open.RequestID,
			labInstanceID: open.Correlation.LabInstanceID, generation: open.Correlation.Generation,
		}
		r.mu.Unlock()

		return r.write(route, data, func() { r.removeFileOpen(key, messageID) })
	})
	if err != nil {
		log.Warn("Connector FILE_OPEN 전송 안 함", "reason", sendFailureReason(err))
		return SentMessage{}, err
	}
	log.Debug("Connector FILE_OPEN 전송")
	return SentMessage{MessageID: messageID}, nil
}

// SendFileClose는 FILE_CLOSE 하나를 Connector의 current Control connection으로 보낸다. CLOSE는 idempotent 통지이며 응답을
// 기다리지 않으므로 pending을 만들지 않는다. 반환과 오류의 의미는 SendFileOpen과 같고 자동 재전송하지 않는다.
func (r *Router) SendFileClose(ctx context.Context, cl FileClose) (SentMessage, error) {
	if err := ctx.Err(); err != nil {
		return SentMessage{}, err
	}
	if err := validateFileCorrelation(cl.Correlation); err != nil {
		return SentMessage{}, err
	}
	if cl.Reason == "" {
		return SentMessage{}, invalidCommand("FILE_CLOSE에는 reason이 필요합니다")
	}

	messageID := uuid.NewString()
	msg := fileCloseMessage{fileEnvelope: newFileEnvelope(protocol.MessageTypeFileClose, messageID, cl.RequestID, cl.Correlation, cl.Trace)}
	msg.Payload.Reason = cl.Reason
	data, err := marshalOutbound(msg)
	if err != nil {
		return SentMessage{}, err
	}

	log := r.logger.With(
		"connector_id", cl.ConnectorID.String(),
		"file_request_id", boundID(cl.Correlation.FileRequestID),
		"lab_instance_id", boundID(cl.Correlation.LabInstanceID),
		"generation", cl.Correlation.Generation,
		"message_type", protocol.MessageTypeFileClose,
	)
	err = r.registry.WithReadyRouteCapability(cl.ConnectorID, protocol.CapabilityFileV1, func(_ Session, route Route) error {
		return r.write(route, data, func() {})
	})
	if err != nil {
		log.Warn("Connector FILE_CLOSE 전송 안 함", "reason", sendFailureReason(err))
		return SentMessage{}, err
	}
	log.Debug("Connector FILE_CLOSE 전송")
	return SentMessage{MessageID: messageID}, nil
}

// removeFileOpen은 messageID가 일치하는 pending만 제거한다.
func (r *Router) removeFileOpen(key fileKey, messageID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.files[key]; ok && p.messageID == messageID {
		delete(r.files, key)
	}
}

// ForgetFileOpen은 connectorID의 File 요청에 진행 중인 FILE_OPEN pending을 제거한다. 제거했으면 true다.
// 로컬 추적만 지운다. Connector에 취소를 보내지 않는다(SendFileClose가 한다). 이후 그 FILE_OPEN의 FILE_OPEN_RESULT는
// FileUnmatchedEvent가 된다.
func (r *Router) ForgetFileOpen(connectorID uuid.UUID, fileRequestID string) bool {
	key := fileKey{connectorID: connectorID, fileRequestID: fileRequestID}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.files[key]
	delete(r.files, key)
	return ok
}

// PendingFileOpens는 connectorID의 진행 중인 FILE_OPEN 수다.
func (r *Router) PendingFileOpens(connectorID uuid.UUID) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for key := range r.files {
		if key.connectorID == connectorID {
			count++
		}
	}
	return count
}

// RouteFileOpenResult는 FILE_OPEN_RESULT를 pending FILE_OPEN에 연결하고 그 FILE_OPEN을 끝낸다. 연결했으면 true다.
//
// 인증된 ConnectorID, fileRequestId, labInstanceId, generation, 그리고 replyToMessageId(필수)가 FILE_OPEN과 모두 같아야 한다.
// 하나라도 다르면 다른 pending으로 fallback하지 않고 FileUnmatchedEvent로 알리며 pending은 그대로 남긴다.
// 이미 끝난 FILE_OPEN의 중복이나 늦은 FILE_OPEN_RESULT는 pending이 없으므로 연결되지 않는다.
func (r *Router) RouteFileOpenResult(connectorID uuid.UUID, in FileInbound, payload FileOpenResultPayload) bool {
	routed, reason, ok := r.matchFileOpen(connectorID, in)
	if !ok {
		r.fileUnmatched(connectorID, protocol.MessageTypeFileOpenResult, in, reason)
		return false
	}
	r.emitFile(FileOpenResultEvent{
		ConnectorID: connectorID, Correlation: in.Correlation, RequestMessageID: routed.messageID,
		MessageID: in.MessageID, RequestID: routed.requestID, Payload: payload, Trace: in.Trace,
	})
	return true
}

// RouteFileUnrepresentable은 schema-valid이지만 SaaS의 typed model로 표현할 수 없는 값(int64를 넘는 generation)을 담은
// message를 어느 File 요청에도 연결하지 않고 FileUnmatchedEvent로 알린다.
func (r *Router) RouteFileUnrepresentable(connectorID uuid.UUID, messageType string, in FileInbound) {
	r.fileUnmatched(connectorID, messageType, in, ReasonUnrepresentable)
}

func (r *Router) matchFileOpen(connectorID uuid.UUID, in FileInbound) (pendingFileOpen, UnmatchedReason, bool) {
	if in.Correlation.Generation < 1 {
		return pendingFileOpen{}, ReasonUnrepresentable, false
	}
	key := fileKey{connectorID: connectorID, fileRequestID: in.Correlation.FileRequestID}

	r.mu.Lock()
	defer r.mu.Unlock()
	pending, ok := r.files[key]
	if !ok {
		return pendingFileOpen{}, ReasonNoPending, false
	}
	if in.ReplyToMessageID != pending.messageID {
		return pendingFileOpen{}, ReasonReplyMismatch, false
	}
	if in.Correlation.LabInstanceID != pending.labInstanceID || in.Correlation.Generation != pending.generation {
		return pendingFileOpen{}, ReasonCorrelationMismatch, false
	}
	delete(r.files, key)
	return pending, "", true
}

func (r *Router) emitFile(e FileEvent) {
	if r.fileSink != nil {
		r.fileSink.HandleFileEvent(e)
	}
}

// fileUnmatched는 연결하지 못한 message를 로그와 FileUnmatchedEvent로 알린다. payload와 Connector가 준 error 문구는 남기지 않는다.
func (r *Router) fileUnmatched(connectorID uuid.UUID, messageType string, in FileInbound, reason UnmatchedReason) {
	event := FileUnmatchedEvent{
		ConnectorID:   connectorID,
		MessageType:   messageType,
		Reason:        reason,
		FileRequestID: boundID(in.Correlation.FileRequestID),
		LabInstanceID: boundID(in.Correlation.LabInstanceID),
		Generation:    in.Correlation.Generation,
	}
	r.logger.Warn("Connector File message를 pending에 연결하지 못함",
		"connector_id", connectorID.String(),
		"message_type", messageType,
		"reason", string(reason),
		"file_request_id", event.FileRequestID,
		"lab_instance_id", event.LabInstanceID,
		"generation", event.Generation,
	)
	r.emitFile(event)
}
