package protocol

// Preview Control / Data WSS 관련 메시지 타입 상수
const (
	// Preview Control 메시지 타입 (Control WSS)
	MessageTypePreviewOpen       = "PREVIEW_OPEN"
	MessageTypePreviewOpenResult = "PREVIEW_OPEN_RESULT"
	MessageTypePreviewClose      = "PREVIEW_CLOSE"
	MessageTypePreviewEnded      = "PREVIEW_ENDED"

	// Preview Data WSS 메시지 타입 (Preview Data WSS: labbit.connector-preview.v1)
	MessageTypePreviewDataAttach   = "PREVIEW_DATA_ATTACH"
	MessageTypePreviewDataAttached = "PREVIEW_DATA_ATTACHED"
	MessageTypePreviewDataClose    = "PREVIEW_DATA_CLOSE"
	MessageTypePreviewDataEnded    = "PREVIEW_DATA_ENDED"
	MessageTypePreviewDataError    = "ERROR"
)

// Preview Close / Ended 공통 사유 상수
const (
	PreviewReasonSessionClosed   = "SESSION_CLOSED"
	PreviewReasonSessionExpired  = "SESSION_EXPIRED"
	PreviewReasonLabReset        = "LAB_RESET"
	PreviewReasonLabCleanup      = "LAB_CLEANUP"
	PreviewReasonPortUnreachable = "PORT_UNREACHABLE"
	PreviewReasonTargetClosed    = "TARGET_CLOSED"
	PreviewReasonServiceRestart  = "SERVICE_RESTARTING"
)

// Preview Error 코드 상수
const (
	PreviewErrInvalidSession  = "INVALID_SESSION"
	PreviewErrForbidden       = "FORBIDDEN"
	PreviewErrStaleGeneration = "STALE_GENERATION"
	PreviewErrPortRefused     = "PORT_CONNECTION_REFUSED"
	PreviewErrProtocolError   = "PROTOCOL_ERROR"
	PreviewErrInternalError   = "INTERNAL_ERROR"
	PreviewErrAttachFailed    = "ATTACH_FAILED"
)

// ==========================================
// Preview Control WSS 메시지 (Control 채널)
// ==========================================

// PreviewOpenPayload 는 PREVIEW_OPEN 메시지의 본문입니다.
type PreviewOpenPayload struct {
	TargetVmKey      string `json:"targetVmKey"`
	ProviderServerID string `json:"providerServerId"`
	Port             int    `json:"port"`
}

// PreviewOpenMessage 는 SaaS -> Connector 로 미리보기 오픈을 요청하는 메시지입니다.
type PreviewOpenMessage struct {
	BaseEnvelope
	Payload PreviewOpenPayload `json:"payload"`
}

// PreviewOpenResultPayload 는 PREVIEW_OPEN_RESULT 메시지의 본문입니다.
type PreviewOpenResultPayload struct {
	Outcome string     `json:"outcome"` // SUCCEEDED, FAILED
	Error   *SafeError `json:"error,omitempty"`
}

// PreviewOpenResultMessage 는 Connector -> SaaS 로 미리보기 오픈 결과를 회신하는 메시지입니다.
type PreviewOpenResultMessage struct {
	BaseEnvelope
	Payload PreviewOpenResultPayload `json:"payload"`
}

// PreviewClosePayload 는 PREVIEW_CLOSE 메시지의 본문입니다.
type PreviewClosePayload struct {
	Reason string `json:"reason"`
}

// PreviewCloseMessage 는 SaaS -> Connector 로 미리보기 세션 종료를 요청하는 메시지입니다.
type PreviewCloseMessage struct {
	BaseEnvelope
	Payload PreviewClosePayload `json:"payload"`
}

// PreviewEndedPayload 는 PREVIEW_ENDED 메시지의 본문입니다.
type PreviewEndedPayload struct {
	Reason string     `json:"reason"`
	Error  *SafeError `json:"error,omitempty"`
}

// PreviewEndedMessage 는 Connector -> SaaS 로 미리보기 세션 종료를 통보하는 메시지입니다.
type PreviewEndedMessage struct {
	BaseEnvelope
	Payload PreviewEndedPayload `json:"payload"`
}

// ==========================================
// Preview Data WSS 메시지 (별도 Data 채널)
// ==========================================

// PreviewDataAttachPayload 는 PREVIEW_DATA_ATTACH 메시지의 본문입니다.
type PreviewDataAttachPayload struct {
	RuntimeID        string `json:"runtimeId"`
	TargetVmKey      string `json:"targetVmKey,omitempty"`
	ProviderServerID string `json:"providerServerId,omitempty"`
	Port             int    `json:"port,omitempty"`
}

// PreviewDataAttachMessage 는 Connector -> Preview Gateway 첫 attach 바인딩 메시지입니다.
type PreviewDataAttachMessage struct {
	BaseEnvelope
	Payload PreviewDataAttachPayload `json:"payload"`
}

// PreviewDataAttachedPayload 는 PREVIEW_DATA_ATTACHED 메시지의 본문입니다.
type PreviewDataAttachedPayload struct {
	Status string `json:"status"` // ATTACHED
}

// PreviewDataAttachedMessage 는 Preview Gateway -> Connector attach 수락 메시지입니다.
type PreviewDataAttachedMessage struct {
	BaseEnvelope
	Payload PreviewDataAttachedPayload `json:"payload"`
}

// PreviewDataClosePayload 는 PREVIEW_DATA_CLOSE 메시지의 본문입니다.
type PreviewDataClosePayload struct {
	Reason string `json:"reason"`
}

// PreviewDataCloseMessage 는 Gateway -> Connector 로 세션 종료를 요구하는 메시지입니다.
type PreviewDataCloseMessage struct {
	BaseEnvelope
	Payload PreviewDataClosePayload `json:"payload"`
}

// PreviewDataEndedPayload 는 PREVIEW_DATA_ENDED 메시지의 본문입니다.
type PreviewDataEndedPayload struct {
	Reason string     `json:"reason"`
	Error  *SafeError `json:"error,omitempty"`
}

// PreviewDataEndedMessage 는 Connector -> Gateway 로 세션 종료를 통보하는 메시지입니다.
type PreviewDataEndedMessage struct {
	BaseEnvelope
	Payload PreviewDataEndedPayload `json:"payload"`
}

// PreviewDataErrorPayload 는 ERROR 메시지의 본문입니다.
type PreviewDataErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
	Fatal   bool   `json:"fatal,omitempty"`
}

// PreviewDataErrorMessage 는 프리뷰 에러 통보 메시지입니다.
type PreviewDataErrorMessage struct {
	BaseEnvelope
	Payload PreviewDataErrorPayload `json:"payload"`
}
