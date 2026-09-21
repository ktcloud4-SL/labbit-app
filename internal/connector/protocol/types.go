package protocol

import "time"

// WSS Subprotocol 상수
const (
	SubprotocolControl      = "labbit.connector.v1"
	SubprotocolTerminalData = "labbit.connector-terminal.v1"
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

// BaseEnvelope 는 v1 Control WSS의 공통 Envelope입니다.
type BaseEnvelope struct {
	Type             string    `json:"type"`
	MessageID        string    `json:"messageId"`
	SentAt           time.Time `json:"sentAt"`
	ReplyToMessageID string    `json:"replyToMessageId,omitempty"`
	RequestID        string    `json:"requestId,omitempty"`
	OperationID      string    `json:"operationId,omitempty"`
	LabInstanceID    string    `json:"labInstanceId,omitempty"`
	Generation       int       `json:"generation,omitempty"`
	TraceParent      string    `json:"traceparent,omitempty"`
	TraceState       string    `json:"tracestate,omitempty"`
}

// SafeError 는 로그/에러 메시지에 노출 가능한 민감정보가 제거된 오류입니다.
type SafeError struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// ProviderResourceRef 는 OpenStack 리소스의 기본 참조 정보입니다.
type ProviderResourceRef struct {
	ResourceType string `json:"resourceType"` // SERVER, NETWORK, SUBNET, ROUTER 등
	ProviderID   string `json:"providerId"`   // 실제 OpenStack 리소스 UUID
	Generation   int    `json:"generation,omitempty"`
	LogicalName  string `json:"logicalName,omitempty"`
}

// ProviderResourceResult 는 작업 후 관측된 리소스 결과입니다.
type ProviderResourceResult struct {
	ProviderResourceRef
	ObservedState string `json:"observedState,omitempty"` // ACTIVE, BUILD, DELETED 등
}

// ResolvedVmSpec 은 생성할 VM의 상세 스펙입니다.
type ResolvedVmSpec struct {
	VMKey     string `json:"vmKey"`
	Role      string `json:"role"`
	ImageRef  string `json:"imageRef"`
	FlavorRef string `json:"flavorRef"`
}

// CreationSnapshot 은 VM 및 네트워크 생성 기준 스냅샷입니다.
type CreationSnapshot struct {
	ProviderConnectionID string           `json:"providerConnectionId"`
	VMs                  []ResolvedVmSpec `json:"vms"`
	WorkspaceVMKey       string           `json:"workspaceVmKey"`
	InternetOutbound     bool             `json:"internetOutbound"`
}

// OperationCommandPayload 는 SaaS가 지시하는 Provision/Reset/Cleanup 명령 본문입니다.
type OperationCommandPayload struct {
	MutationType      string                `json:"mutationType"` // PROVISION, RESET, CLEANUP
	CreationSnapshot  *CreationSnapshot     `json:"creationSnapshot,omitempty"`
	ProviderResources []ProviderResourceRef `json:"providerResources,omitempty"`
}

// OperationResultPayload 는 작업 완료 후 보고하는 결과 본문입니다.
type OperationResultPayload struct {
	Outcome           string                   `json:"outcome"` // SUCCEEDED, FAILED, UNKNOWN
	ProviderResources []ProviderResourceResult `json:"providerResources"`
	Error             *SafeError               `json:"error,omitempty"`
}

// ReconcileRequestPayload 는 DB 기록과 OpenStack 현실 대조 요청입니다.
type ReconcileRequestPayload struct {
	KnownResources     []ProviderResourceRef `json:"knownResources"`
	DiscoverCandidates bool                  `json:"discoverCandidates,omitempty"`
}

// ResourceObservation 은 OpenStack에서 관측된 실제 상태입니다.
type ResourceObservation struct {
	ResourceType string `json:"resourceType"`
	ProviderID   string `json:"providerId"`
	Status       string `json:"status"` // PRESENT, MISSING, DELETED, DISCOVERED_CANDIDATE
}

// ReconcileResultPayload 는 리소스 대조 결과입니다.
type ReconcileResultPayload struct {
	Observations []ResourceObservation `json:"observations"`
}
