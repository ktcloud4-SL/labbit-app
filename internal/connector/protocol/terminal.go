package protocol

// Terminal Control / Data WSS 관련 메시지 타입 상수
const (
	// Terminal Data WSS 메시지 타입 (terminal-data.schema.json)
	MessageTypeTerminalDataAttach   = "TERMINAL_DATA_ATTACH"
	MessageTypeTerminalDataAttached = "TERMINAL_DATA_ATTACHED"
	MessageTypeTerminalDataResize   = "TERMINAL_DATA_RESIZE"
	MessageTypeTerminalDataClose    = "TERMINAL_DATA_CLOSE"
	MessageTypeTerminalDataEnded    = "TERMINAL_DATA_ENDED"
)

// Terminal Close / Ended 공통 사유 상수
const (
	TerminalReasonSessionClosed   = "SESSION_CLOSED"
	TerminalReasonSessionExpired  = "SESSION_EXPIRED"
	TerminalReasonLabReset        = "LAB_RESET"
	TerminalReasonLabCleanup      = "LAB_CLEANUP"
	TerminalReasonPtyExited       = "PTY_EXITED"
	TerminalReasonSSHDisconnected = "SSH_DISCONNECTED"
	TerminalReasonServiceRestart  = "SERVICE_RESTARTING"
	TerminalReasonGraceTimeout    = "GRACE_TIMEOUT"
)

// Terminal Error 코드 상수
const (
	TerminalErrInvalidSession  = "INVALID_SESSION"
	TerminalErrForbidden       = "FORBIDDEN"
	TerminalErrStaleGeneration = "STALE_GENERATION"
	TerminalErrProtocolError   = "PROTOCOL_ERROR"
	TerminalErrInternalError   = "INTERNAL_ERROR"
	TerminalErrAttachFailed    = "ATTACH_FAILED"
)

// ==========================================
// Terminal Control WSS 메시지 (Control 채널)
// ==========================================

// TerminalOpenPayload 는 TERMINAL_OPEN 메시지의 본문입니다.
type TerminalOpenPayload struct {
	TargetVmKey      string `json:"targetVmKey"`
	ProviderServerID string `json:"providerServerId"`
	Cols             int    `json:"cols"`
	Rows             int    `json:"rows"`
}

// TerminalOpenMessage 는 SaaS -> Connector 로 터미널 세션 오픈을 요청하는 메시지입니다.
type TerminalOpenMessage struct {
	BaseEnvelope
	Payload TerminalOpenPayload `json:"payload"`
}

// TerminalOpenResultPayload 는 TERMINAL_OPEN_RESULT 메시지의 본문입니다.
type TerminalOpenResultPayload struct {
	Outcome string     `json:"outcome"` // SUCCEEDED, FAILED
	Error   *SafeError `json:"error,omitempty"`
}

// TerminalOpenResultMessage 는 Connector -> SaaS 로 터미널 오픈 결과를 회신하는 메시지입니다.
type TerminalOpenResultMessage struct {
	BaseEnvelope
	Payload TerminalOpenResultPayload `json:"payload"`
}

// TerminalClosePayload 는 TERMINAL_CLOSE 메시지의 본문입니다.
type TerminalClosePayload struct {
	Reason string `json:"reason"`
}

// TerminalCloseMessage 는 SaaS -> Connector 로 터미널 세션 종료를 요청하는 메시지입니다.
type TerminalCloseMessage struct {
	BaseEnvelope
	Payload TerminalClosePayload `json:"payload"`
}

// TerminalEndedPayload 는 TERMINAL_ENDED 메시지의 본문입니다.
type TerminalEndedPayload struct {
	Reason   string     `json:"reason"`
	ExitCode *int       `json:"exitCode,omitempty"`
	Error    *SafeError `json:"error,omitempty"`
}

// TerminalEndedMessage 는 Connector -> SaaS 로 터미널 세션 종료를 통보하는 메시지입니다.
type TerminalEndedMessage struct {
	BaseEnvelope
	Payload TerminalEndedPayload `json:"payload"`
}

// ==========================================
// Terminal Data WSS 메시지 (별도 Data 채널)
// ==========================================

// TerminalDataAttachPayload 는 TERMINAL_DATA_ATTACH 메시지의 본문입니다.
type TerminalDataAttachPayload struct {
	RuntimeID string `json:"runtimeId"`
}

// TerminalDataAttachMessage 는 Connector -> Relay 첫 attach 바인딩 메시지입니다.
type TerminalDataAttachMessage struct {
	BaseEnvelope
	Payload TerminalDataAttachPayload `json:"payload"`
}

// TerminalDataAttachedPayload 는 TERMINAL_DATA_ATTACHED 메시지의 본문입니다.
type TerminalDataAttachedPayload struct {
	Resumed          bool `json:"resumed"`
	HistoryAvailable bool `json:"historyAvailable"`
}

// TerminalDataAttachedMessage 는 Relay -> Connector attach 수락 메시지입니다.
type TerminalDataAttachedMessage struct {
	BaseEnvelope
	Payload TerminalDataAttachedPayload `json:"payload"`
}

// TerminalDataResizePayload 는 TERMINAL_DATA_RESIZE 메시지의 본문입니다.
type TerminalDataResizePayload struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// TerminalDataResizeMessage 는 Relay -> Connector 로 창 크기 조절을 요청하는 메시지입니다.
type TerminalDataResizeMessage struct {
	BaseEnvelope
	Payload TerminalDataResizePayload `json:"payload"`
}

// TerminalDataClosePayload 는 TERMINAL_DATA_CLOSE 메시지의 본문입니다.
type TerminalDataClosePayload struct {
	Reason string `json:"reason"`
}

// TerminalDataCloseMessage 는 Relay -> Connector 로 PTY/세션 종료를 요구하는 메시지입니다.
type TerminalDataCloseMessage struct {
	BaseEnvelope
	Payload TerminalDataClosePayload `json:"payload"`
}

// TerminalDataEndedPayload 는 TERMINAL_DATA_ENDED 메시지의 본문입니다.
type TerminalDataEndedPayload struct {
	Reason   string     `json:"reason"`
	ExitCode *int       `json:"exitCode,omitempty"`
	Error    *SafeError `json:"error,omitempty"`
}

// TerminalDataEndedMessage 는 Connector -> Relay 로 세션 종료를 통보하는 메시지입니다.
type TerminalDataEndedMessage struct {
	BaseEnvelope
	Payload TerminalDataEndedPayload `json:"payload"`
}

// TerminalDataErrorPayload 는 ERROR 메시지의 본문입니다.
type TerminalDataErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
	Fatal   bool   `json:"fatal,omitempty"`
}

// TerminalDataErrorMessage 는 터미널 에러 통보 메시지입니다.
type TerminalDataErrorMessage struct {
	BaseEnvelope
	Payload TerminalDataErrorPayload `json:"payload"`
}
