package protocol

// Preview Close 사유 상수 (preview-control.schema.json 기준)
const (
	PreviewReasonSessionClosed   = "SESSION_CLOSED"
	PreviewReasonSessionExpired  = "SESSION_EXPIRED"
	PreviewReasonLabReset        = "LAB_RESET"
	PreviewReasonLabCleanup      = "LAB_CLEANUP"
	PreviewReasonOpenTimeout     = "OPEN_TIMEOUT"
	PreviewReasonRequestCanceled = "REQUEST_CANCELED"
	PreviewReasonOpenFailed      = "OPEN_FAILED"
	PreviewReasonServiceRestart  = "SERVICE_RESTARTING"
	PreviewReasonTargetClosed    = "TARGET_CLOSED"
)

// Preview SafeError 코드 상수 (preview-control.schema.json 기준)
const (
	PreviewErrorCodeAppNotRunning = "APP_NOT_RUNNING"
	PreviewErrorCodePortRejected  = "PORT_REJECTED"
	PreviewErrorCodeVMUnreachable = "VM_UNREACHABLE"
	PreviewErrorCodeUnavailable   = "UNAVAILABLE"
	PreviewErrorCodeInternalError = "INTERNAL_ERROR"

	PreviewErrAppNotRunning = PreviewErrorCodeAppNotRunning
	PreviewErrPortRejected  = PreviewErrorCodePortRejected
	PreviewErrVMUnreachable = PreviewErrorCodeVMUnreachable
	PreviewErrUnavailable   = PreviewErrorCodeUnavailable
	PreviewErrInternalError = PreviewErrorCodeInternalError
	PreviewErrProtocolError = "PROTOCOL_ERROR"
)

// ==========================================
// Preview Control WSS 메시지 (Control 채널)
// ==========================================

// PreviewOpenPayload 는 PREVIEW_OPEN 메시지의 본문입니다.
type PreviewOpenPayload struct {
	TargetVmKey      string `json:"targetVmKey"`
	ProviderServerID string `json:"providerServerId"`
	TargetPort       int    `json:"targetPort"`
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

// PreviewCloseMessage 는 SaaS -> Connector 로 미리보기 세션 종료를 통지하는 메시지입니다.
type PreviewCloseMessage struct {
	BaseEnvelope
	Payload PreviewClosePayload `json:"payload"`
}

// ==========================================
// Preview Data WSS 메시지 (별도 Data 채널)
// ==========================================

// PreviewAttachPayload 는 PREVIEW_ATTACH 메시지의 본문입니다.
type PreviewAttachPayload struct {
	RuntimeID        string `json:"runtimeId"`
	TargetVmKey      string `json:"targetVmKey"`
	ProviderServerID string `json:"providerServerId"`
	TargetPort       int    `json:"targetPort"`
}

// PreviewAttachMessage 는 Connector -> Preview Gateway 첫 attach 바인딩 메시지입니다.
type PreviewAttachMessage struct {
	BaseEnvelope
	Payload PreviewAttachPayload `json:"payload"`
}

// PreviewAttachedPayload 는 PREVIEW_ATTACHED 메시지의 본문입니다 (empty object).
type PreviewAttachedPayload struct{}

// PreviewAttachedMessage 는 Preview Gateway -> Connector attach 수락 메시지입니다.
type PreviewAttachedMessage struct {
	BaseEnvelope
	Payload PreviewAttachedPayload `json:"payload"`
}
