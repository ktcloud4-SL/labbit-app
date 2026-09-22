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
