package protocol

import (
	"encoding/json"
	"time"
)

// WSS Subprotocol 상수
const (
	SubprotocolControl      = "labbit.connector.v1"
	SubprotocolTerminalData = "labbit.connector-terminal.v1"
	// SubprotocolPreviewData는 Preview Data WSS의 subprotocol이다(preview-data.schema.json).
	SubprotocolPreviewData = "labbit.connector-preview.v1"
)

// HELLO capabilities 값이다. SaaS는 connection이 선언한 capability에만 해당 기능의 Control message를 보낸다(버전으로 추론하지 않는다).
const (
	// CapabilityFileV1은 Workspace File transport(file-control.schema.json, file-data.schema.json)를 지원함을 뜻한다.
	CapabilityFileV1 = "file-v1"
	// CapabilityPreviewV1은 Preview transport(preview-control.schema.json, preview-data.schema.json)를 지원함을 뜻한다.
	CapabilityPreviewV1 = "preview-v1"
)

// Control 메시지 타입 정의 (connector.schema.json 기준)
const (
	MessageTypeHello             = "HELLO"
	MessageTypeHelloAck          = "HELLO_ACK"
	MessageTypeHeartbeat         = "HEARTBEAT"
	MessageTypeProviderRequest   = "PROVIDER_REQUEST"
	MessageTypeProviderResponse  = "PROVIDER_RESPONSE"
	MessageTypeOperationCommand  = "OPERATION_COMMAND"
	MessageTypeOperationAck      = "OPERATION_ACK"
	MessageTypeOperationProgress = "OPERATION_PROGRESS"
	MessageTypeOperationResult   = "OPERATION_RESULT"
	MessageTypeReconcileRequest  = "RECONCILE_REQUEST"
	MessageTypeReconcileResult   = "RECONCILE_RESULT"
	MessageTypeError             = "ERROR"
)

// Terminal Control 메시지 타입 정의 (terminal-control.schema.json 기준)
const (
	MessageTypeTerminalOpen       = "TERMINAL_OPEN"
	MessageTypeTerminalOpenResult = "TERMINAL_OPEN_RESULT"
	MessageTypeTerminalClose      = "TERMINAL_CLOSE"
	MessageTypeTerminalEnded      = "TERMINAL_ENDED"
)

// Workspace File Control 메시지 타입 정의 (file-control.schema.json 기준)
const (
	MessageTypeFileOpen       = "FILE_OPEN"
	MessageTypeFileOpenResult = "FILE_OPEN_RESULT"
	MessageTypeFileClose      = "FILE_CLOSE"
)

// Preview Control 메시지 타입 정의 (preview-control.schema.json 기준)
const (
	MessageTypePreviewOpen       = "PREVIEW_OPEN"
	MessageTypePreviewOpenResult = "PREVIEW_OPEN_RESULT"
	MessageTypePreviewClose      = "PREVIEW_CLOSE"
)

// Preview Data WSS 메시지 타입 정의 (preview-data.schema.json 기준)
const (
	MessageTypePreviewAttach   = "PREVIEW_ATTACH"
	MessageTypePreviewAttached = "PREVIEW_ATTACHED"
)

// Operation Mutation 타입
const (
	MutationTypeProvision = "PROVISION"
	MutationTypeReset     = "RESET"
	MutationTypeCleanup   = "CLEANUP"
)

// Operation 결과 Outcome (SUCCEEDED, FAILED, UNKNOWN)
const (
	OutcomeSucceeded = "SUCCEEDED"
	OutcomeFailed    = "FAILED"
	OutcomeUnknown   = "UNKNOWN"
)

// Provider 조회 요청 타입
const (
	ProviderRequestValidateConnection = "VALIDATE_CONNECTION"
	ProviderRequestListImages         = "LIST_IMAGES"
	ProviderRequestListFlavors        = "LIST_FLAVORS"
)

// BaseEnvelope 는 v1 Control WSS의 공통 Envelope입니다.
type BaseEnvelope struct {
	Type              string    `json:"type"`
	MessageID         string    `json:"messageId"`
	SentAt            time.Time `json:"sentAt"`
	ReplyToMessageID  string    `json:"replyToMessageId,omitempty"`
	RequestID         string    `json:"requestId,omitempty"`
	OperationID       string    `json:"operationId,omitempty"`
	TerminalSessionID string    `json:"terminalSessionId,omitempty"`
	PreviewSessionID  string    `json:"previewSessionId,omitempty"`
	LabInstanceID     string    `json:"labInstanceId,omitempty"`
	Generation        int64     `json:"generation,omitempty"`
	TraceParent       string    `json:"traceparent,omitempty"`
	TraceState        string    `json:"tracestate,omitempty"`
}

// 메시지 크기 및 WebSocket Close 코드 상수 (최신 contracts/connector SSOT)
const (
	// MaxJSONMessageSize 는 WebSocket fragmentation 재조립 후 최대 JSON Text 메시지 크기 (1 MiB)
	MaxJSONMessageSize int64 = 1048576

	// CloseMessageTooBig 은 1 MiB 초과 시 사용하는 WebSocket close code (1009)
	CloseMessageTooBig = 1009
)

// HelloPayload 는 HELLO 메시지 본문입니다.
type HelloPayload struct {
	ConnectorVersion string    `json:"connectorVersion"`
	RuntimeID        string    `json:"runtimeId"`
	StartedAt        time.Time `json:"startedAt"`
	Capabilities     []string  `json:"capabilities,omitempty"`
}

// HelloMessage 는 Connector 가 최초 연결 시 전송하는 HELLO 메시지입니다.
type HelloMessage struct {
	BaseEnvelope
	Payload HelloPayload `json:"payload"`
}

// HelloAckPayload 는 SaaS 가 회신하는 HELLO_ACK 본문입니다.
type HelloAckPayload struct {
	ServerTime               time.Time `json:"serverTime"`
	HeartbeatIntervalSeconds int       `json:"heartbeatIntervalSeconds"`
	OfflineTimeoutSeconds    int       `json:"offlineTimeoutSeconds"`
}

// HelloAckMessage 는 SaaS 가 HELLO 에 회신하는 메시지입니다.
type HelloAckMessage struct {
	BaseEnvelope
	Payload HelloAckPayload `json:"payload"`
}

// HeartbeatPayload 는 Connector 가 주기적으로 전송하는 HEARTBEAT 본문입니다.
type HeartbeatPayload struct {
	ObservedAt time.Time `json:"observedAt"`
}

// HeartbeatMessage 는 Connector 생존 확인 메시지입니다.
type HeartbeatMessage struct {
	BaseEnvelope
	Payload HeartbeatPayload `json:"payload"`
}

// ProviderRequestPayload 는 SaaS가 요청하는 Provider 연결 검증/조회입니다.
type ProviderRequestPayload struct {
	RequestType          string `json:"requestType"`
	ProviderConnectionID string `json:"providerConnectionId"`
}

// ProviderRequestMessage 는 SaaS가 Connector로 보내는 Provider 조회 요청입니다.
type ProviderRequestMessage struct {
	BaseEnvelope
	Payload ProviderRequestPayload `json:"payload"`
}

// ProviderImage 는 PROVIDER_RESPONSE의 정규화된 Image 항목입니다.
type ProviderImage struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status,omitempty"`
}

// ProviderFlavor 는 PROVIDER_RESPONSE의 정규화된 Flavor 항목입니다.
type ProviderFlavor struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	VCPUs   int64  `json:"vcpus"`
	RAMMiB  int64  `json:"ramMiB"`
	DiskGiB int64  `json:"diskGiB"`
}

// ProviderResponsePayload 는 Provider 조회 결과입니다. Items에는
// ProviderImage 또는 ProviderFlavor만 들어갑니다.
type ProviderResponsePayload struct {
	RequestType string        `json:"requestType"`
	Outcome     string        `json:"outcome"`
	Items       []interface{} `json:"items,omitempty"`
	Error       *SafeError    `json:"error,omitempty"`
}

// ProviderResponseMessage 는 Connector가 Provider 조회 결과를 회신하는 메시지입니다.
type ProviderResponseMessage struct {
	BaseEnvelope
	Payload ProviderResponsePayload `json:"payload"`
}

// SafeError 는 로그/에러 메시지에 노출 가능한 민감정보가 제거된 오류입니다.
type SafeError struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// ProtocolErrorPayload 는 프로토콜 및 제어 레벨 오류 본문입니다.
type ProtocolErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
	Fatal   bool   `json:"fatal,omitempty"`
}

// ProtocolErrorMessage 는 연결 레벨 프로토콜 오류 메시지입니다.
type ProtocolErrorMessage struct {
	BaseEnvelope
	Payload ProtocolErrorPayload `json:"payload"`
}

// ProviderResourceRef 는 OpenStack 리소스의 기본 참조 정보입니다.
type ProviderResourceRef struct {
	ResourceType string `json:"resourceType"` // SERVER, NETWORK, SUBNET, ROUTER 등
	ProviderID   string `json:"providerId"`   // 실제 OpenStack 리소스 UUID
	Generation   int64  `json:"generation,omitempty"`
	LogicalName  string `json:"logicalName,omitempty"`
}

// ProviderResourceResult 는 작업 후 관측된 리소스 결과입니다.
type ProviderResourceResult struct {
	ProviderResourceRef
	ObservedState string `json:"observedState,omitempty"` // ACTIVE, BUILD, DELETED 등
}

// ResolvedFlavorSpec 은 VM 하드웨어 리소스 규격입니다.
type ResolvedFlavorSpec struct {
	VCPUs   int64 `json:"vcpus"`
	RAMMiB  int64 `json:"ramMiB"`
	DiskGiB int64 `json:"diskGiB"`
}

// StartupScriptSnapshot 은 VM 기동 스크립트 스냅샷입니다.
type StartupScriptSnapshot struct {
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
}

// ResolvedVmSpec 은 생성할 VM의 상세 스펙입니다.
//
// instanceIndex 는 Schema 상 required(minimum 0)이므로 0 도 직렬화해야 합니다. omitempty 를 두면 첫 VM(0)의
// instanceIndex 가 wire 에서 빠져 Schema-invalid OPERATION_COMMAND 가 됩니다.
type ResolvedVmSpec struct {
	VMKey         string              `json:"vmKey"`
	Role          string              `json:"role"`
	InstanceIndex int64               `json:"instanceIndex"`
	ImageID       string              `json:"imageId,omitempty"`
	ImageRef      string              `json:"imageRef,omitempty"`
	FlavorID      string              `json:"flavorId,omitempty"`
	FlavorRef     string              `json:"flavorRef,omitempty"`
	FlavorSpec    *ResolvedFlavorSpec `json:"flavorSpec,omitempty"`
}

// CreationSnapshot 은 VM 및 네트워크 생성 기준 스냅샷입니다.
type CreationSnapshot struct {
	ProviderConnectionID string                 `json:"providerConnectionId"`
	VMs                  []ResolvedVmSpec       `json:"vms"`
	WorkspaceVMKey       string                 `json:"workspaceVmKey"`
	InternetOutbound     bool                   `json:"internetOutbound"`
	StartupScript        *StartupScriptSnapshot `json:"startupScript,omitempty"`
}

// OperationCommandPayload 는 SaaS가 지시하는 Provision/Reset/Cleanup 명령 본문입니다.
//
// providerResources 에 대한 요구는 mutationType 마다 다릅니다(connector.schema.json 기준).
//
//   - PROVISION: providerResources 를 요구하지 않습니다.
//   - RESET: 직전 generation 리소스를 담은 비어 있지 않은 providerResources 가 필수입니다. 각 항목은 logicalName 이
//     있어야 하고 generation 은 envelope generation - 1 이어야 합니다.
//   - CLEANUP: providerResources property 가 필수입니다. array 에 minItems 가 없으므로 `"providerResources": []` 도 유효합니다.
//
// 이 type 은 값을 직렬화할 뿐 위 요구를 검사하지 않습니다. RESET 의 요구는 SaaS 의 outbound 검증과 Connector 의 inbound 검증이 강제합니다.
//
// ProviderResources 는 nil 과 빈 목록을 구분합니다.
//
//   - nil            → property 를 만들지 않습니다(누락). null 도 만들지 않습니다.
//   - 빈 non-nil     → `"providerResources": []`
//   - 항목이 있는 경우 → 그 목록
//
// 단순한 omitempty 는 빈 목록의 property 를 지워 유효한 빈 CLEANUP 을 Schema-invalid 로 만들고, omitempty 를 빼면
// providerResources 가 요구되지 않는 PROVISION 등의 nil 이 null 로 나가 Schema-invalid 가 되므로 MarshalJSON 으로 구분합니다.
// decode 는 기본 동작이며 `[]` 는 빈 non-nil, 누락과 null 은 nil 입니다.
type OperationCommandPayload struct {
	MutationType      string                `json:"mutationType"` // PROVISION, RESET, CLEANUP
	CreationSnapshot  *CreationSnapshot     `json:"creationSnapshot,omitempty"`
	ProviderResources []ProviderResourceRef `json:"providerResources,omitempty"`
}

// MarshalJSON 은 ProviderResources 의 nil(누락)과 빈 목록([])을 구분해 직렬화합니다.
func (p OperationCommandPayload) MarshalJSON() ([]byte, error) {
	wire := struct {
		MutationType      string                 `json:"mutationType"`
		CreationSnapshot  *CreationSnapshot      `json:"creationSnapshot,omitempty"`
		ProviderResources *[]ProviderResourceRef `json:"providerResources,omitempty"` // nil 포인터만 생략한다.
	}{MutationType: p.MutationType, CreationSnapshot: p.CreationSnapshot}
	if p.ProviderResources != nil {
		wire.ProviderResources = &p.ProviderResources
	}
	return json.Marshal(wire)
}

// OperationCommandMessage 는 SaaS가 Connector로 전달하는 명령 메시지입니다.
type OperationCommandMessage struct {
	BaseEnvelope
	Payload OperationCommandPayload `json:"payload"`
}

// OperationAckPayload 는 명령 수신 수락 여부입니다.
type OperationAckPayload struct {
	Accepted bool       `json:"accepted"`
	Error    *SafeError `json:"error,omitempty"`
}

// OperationAckMessage 는 Connector가 명령 수신 직후 보내는 응답 메시지입니다.
type OperationAckMessage struct {
	BaseEnvelope
	Payload OperationAckPayload `json:"payload"`
}

// OperationProgressPayload 는 작업의 현재 진행 단계입니다(connector.schema.json OperationProgressPayload).
type OperationProgressPayload struct {
	Stage string `json:"stage"` // CREATE_NETWORK, CREATE_VM 같은 확장 가능한 단계 이름
}

// OperationProgressMessage 는 Connector가 작업 중간에 SaaS로 전달하는 진행 메시지입니다.
type OperationProgressMessage struct {
	BaseEnvelope
	Payload OperationProgressPayload `json:"payload"`
}

// OperationResultPayload 는 작업 완료 후 보고하는 결과 본문입니다.
type OperationResultPayload struct {
	Outcome           string                   `json:"outcome"` // SUCCEEDED, FAILED, UNKNOWN
	ProviderResources []ProviderResourceResult `json:"providerResources"`
	Error             *SafeError               `json:"error,omitempty"`
}

// OperationResultMessage 는 작업 완료 후 Connector가 SaaS로 전달하는 결과 메시지입니다.
type OperationResultMessage struct {
	BaseEnvelope
	Payload OperationResultPayload `json:"payload"`
}

// ReconcileRequestPayload 는 DB 기록과 OpenStack 현실 대조 요청입니다.
type ReconcileRequestPayload struct {
	KnownResources     []ProviderResourceRef `json:"knownResources"`
	DiscoverCandidates *bool                 `json:"discoverCandidates,omitempty"`
}

// ReconcileRequestMessage 는 SaaS가 Connector로 전송하는 Reconcile 요청 메시지입니다.
type ReconcileRequestMessage struct {
	BaseEnvelope
	Payload ReconcileRequestPayload `json:"payload"`
}

// ResourceObservation 은 OpenStack에서 관측된 실제 상태입니다.
type ResourceObservation struct {
	ResourceType  string `json:"resourceType"`
	ProviderID    string `json:"providerId"`
	Generation    int64  `json:"generation,omitempty"`
	Exists        bool   `json:"exists"`
	ObservedState string `json:"observedState,omitempty"`
	Source        string `json:"source"` // KNOWN_RESOURCE, DISCOVERED_CANDIDATE
	LogicalName   string `json:"logicalName,omitempty"`
}

// ReconcileResultPayload 는 리소스 대조 결과입니다.
type ReconcileResultPayload struct {
	Observations []ResourceObservation `json:"observations"`
	Error        *SafeError            `json:"error,omitempty"`
}

// ReconcileResultMessage 는 Reconcile 완료 후 회신하는 메시지입니다.
type ReconcileResultMessage struct {
	BaseEnvelope
	Payload ReconcileResultPayload `json:"payload"`
}
