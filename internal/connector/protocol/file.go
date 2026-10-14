package protocol

// File lifecycle and per-request Data WSS messages are owned by the two
// contracts/connector/file-*.schema.json files. Content never goes on Control.
const (
	MessageTypeFileDataAttach   = "FILE_DATA_ATTACH"
	MessageTypeFileDataAttached = "FILE_DATA_ATTACHED"
	MessageTypeFileTree         = "FILE_TREE"
	MessageTypeFileRead         = "FILE_READ"
	MessageTypeFileSave         = "FILE_SAVE"
	MessageTypeFileTreeResult   = "FILE_TREE_RESULT"
	MessageTypeFileReadResult   = "FILE_READ_RESULT"
	MessageTypeFileSaveResult   = "FILE_SAVE_RESULT"
)

type FileOpenPayload struct {
	Operation        string `json:"operation"`
	TargetVMKey      string `json:"targetVmKey"`
	ProviderServerID string `json:"providerServerId"`
}
type FileOpenMessage struct {
	BaseEnvelope
	Payload FileOpenPayload `json:"payload"`
}
type FileOpenResultPayload struct {
	Outcome string     `json:"outcome"`
	Error   *SafeError `json:"error,omitempty"`
}
type FileOpenResultMessage struct {
	BaseEnvelope
	Payload FileOpenResultPayload `json:"payload"`
}
type FileClosePayload struct {
	Reason string `json:"reason"`
}
type FileCloseMessage struct {
	BaseEnvelope
	Payload FileClosePayload `json:"payload"`
}
type FileDataAttachPayload struct {
	RuntimeID        string `json:"runtimeId"`
	TargetVMKey      string `json:"targetVmKey"`
	ProviderServerID string `json:"providerServerId"`
}
type FileDataAttachMessage struct {
	BaseEnvelope
	Payload FileDataAttachPayload `json:"payload"`
}
