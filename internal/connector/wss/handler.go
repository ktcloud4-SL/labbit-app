package wss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/terminal"
)

// MessageSender 는 SaaS 로 WSS 메시지를 전송하는 인터페이스입니다.
type MessageSender interface {
	SendMessage(ctx context.Context, msg interface{}) error
}

// SendMessageFunc 는 함수 타입으로 MessageSender 인터페이스를 구현할 수 있게 합니다.
type SendMessageFunc func(ctx context.Context, msg interface{}) error

func (f SendMessageFunc) SendMessage(ctx context.Context, msg interface{}) error {
	return f(ctx, msg)
}

// PTYFactoryFunc 는 대상 VM 정보를 기반으로 PTY 채널을 생성하는 함수 타입입니다.
type PTYFactoryFunc func(targetVmKey string, serverId string, cols, rows int) (terminal.PTYChannel, error)

// Handler 는 SaaS 로부터 수신한 Control WSS 메시지를 검증하고
// 내부 Provider(DispatchOperation / DispatchReconcile) 및 터미널 세션 관리자로 연결한 후 결과를 회신합니다.
type Handler struct {
	mu            sync.RWMutex
	provider      provider.Provider
	sender        MessageSender
	onError       func(err error)
	terminalMgr   *terminal.SessionManager
	ptyFactory    PTYFactoryFunc
	terminalCfg   terminal.DataWSSClientConfig
	endedSessions sync.Map
}

// NewHandler 는 새 Control WSS 메시지 핸들러를 생성합니다.
func NewHandler(p provider.Provider, sender MessageSender) *Handler {
	return &Handler{
		provider: p,
		sender:   sender,
	}
}

// SetSender 는 WSS 메시지 발송 인터페이스를 갱신합니다.
func (h *Handler) SetSender(sender MessageSender) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sender = sender
}

// SetTerminalManager 는 터미널 세션 관리자와 PTY 팩토리 설정을 등록합니다.
// 세션 종료 시 Control WSS로 TERMINAL_ENDED 가 정확히 한 번 전파되도록 콜백을 연결합니다.
func (h *Handler) SetTerminalManager(mgr *terminal.SessionManager, ptyFactory PTYFactoryFunc, cfg terminal.DataWSSClientConfig) {
	h.mu.Lock()
	h.terminalMgr = mgr
	h.ptyFactory = ptyFactory
	h.terminalCfg = cfg
	h.mu.Unlock()

	if mgr != nil {
		mgr.SetOnEnded(func(session *terminal.Session, reason string, exitCode *int, err error) {
			h.sendTerminalEnded(session, reason, exitCode, err)
		})
	}
}

// Sender 는 현재 설정된 MessageSender 를 반환합니다.
func (h *Handler) Sender() MessageSender {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.sender
}

// SetOnError 는 메시지 수신/처리 중 에러 발생 시 호출될 콜백을 설정합니다.
func (h *Handler) SetOnError(cb func(err error)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onError = cb
}

// OnError 는 현재 설정된 에러 콜백을 반환합니다.
func (h *Handler) OnError() func(err error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.onError
}

// HandleMessage 는 수신된 raw JSON 메시지를 Envelope 기준으로 판별하여 적절한 처리기로 분기합니다.
func (h *Handler) HandleMessage(ctx context.Context, raw []byte) error {
	var env protocol.BaseEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("failed to unmarshal base envelope: %w", err)
	}

	switch env.Type {
	case protocol.MessageTypeOperationCommand:
		return h.handleOperationCommand(ctx, env, raw)
	case protocol.MessageTypeReconcileRequest:
		return h.handleReconcileRequest(ctx, env, raw)
	case protocol.MessageTypeTerminalOpen:
		return h.handleTerminalOpen(ctx, env, raw)
	case protocol.MessageTypeTerminalClose:
		return h.handleTerminalClose(ctx, env, raw)
	default:
		// 지원되지 않는 메시지 타입은 Protocol Error 회신 후 무시
		if sender := h.Sender(); sender != nil && env.MessageID != "" {
			errPayload := protocol.ProtocolErrorMessage{
				BaseEnvelope: protocol.BaseEnvelope{
					Type:             protocol.MessageTypeError,
					MessageID:        generateUUID(),
					ReplyToMessageID: env.MessageID,
					SentAt:           time.Now().UTC(),
				},
				Payload: protocol.ProtocolErrorPayload{
					Code:    "UNSUPPORTED_MESSAGE_TYPE",
					Message: fmt.Sprintf("unsupported message type: %s", env.Type),
					Fatal:   false,
				},
			}
			_ = sender.SendMessage(ctx, errPayload)
		}
		return nil
	}
}

// validateOperationCommand 는 connector.schema.json 기준 필수 Correlation 및 Payload 필드를 Provider 호출 전에 사전 검증합니다.
func validateOperationCommand(cmd *protocol.OperationCommandMessage) error {
	if cmd.MessageID == "" {
		return fmt.Errorf("missing required messageId in envelope")
	}
	if cmd.OperationID == "" {
		return fmt.Errorf("missing required operationId in envelope")
	}
	if cmd.LabInstanceID == "" {
		return fmt.Errorf("missing required labInstanceId in envelope")
	}
	if cmd.Generation < 1 {
		return fmt.Errorf("invalid generation in envelope: must be >= 1, got %d", cmd.Generation)
	}

	switch cmd.Payload.MutationType {
	case protocol.MutationTypeProvision, protocol.MutationTypeReset:
		if cmd.Payload.CreationSnapshot == nil {
			return fmt.Errorf("creationSnapshot is required for %s", cmd.Payload.MutationType)
		}
		snap := cmd.Payload.CreationSnapshot
		if snap.ProviderConnectionID == "" {
			return fmt.Errorf("providerConnectionId is required in creationSnapshot")
		}
		if snap.WorkspaceVMKey == "" {
			return fmt.Errorf("workspaceVmKey is required in creationSnapshot")
		}
		if len(snap.VMs) == 0 {
			return fmt.Errorf("creationSnapshot must contain at least 1 VM")
		}
		workspaceVMFound := false
		for i, v := range snap.VMs {
			if v.VMKey == "" {
				return fmt.Errorf("vmKey is required for VM at index %d", i)
			}
			if v.Role == "" {
				return fmt.Errorf("role is required for VM %q", v.VMKey)
			}
			if v.InstanceIndex < 0 {
				return fmt.Errorf("instanceIndex must be >= 0 for VM %q", v.VMKey)
			}
			// imageId, flavorId 필수 검증: imageRef/flavorRef 대체는 금지되며 invalid command로 거절
			if v.ImageID == "" {
				return fmt.Errorf("imageId is required for VM %q: imageRef fallback is prohibited", v.VMKey)
			}
			if v.FlavorID == "" {
				return fmt.Errorf("flavorId is required for VM %q: flavorRef fallback is prohibited", v.VMKey)
			}
			if v.FlavorSpec == nil {
				return fmt.Errorf("flavorSpec is required for VM %q", v.VMKey)
			}
			if v.FlavorSpec.VCPUs < 1 {
				return fmt.Errorf("flavorSpec.vcpus must be >= 1 for VM %q", v.VMKey)
			}
			if v.FlavorSpec.RAMMiB < 1 {
				return fmt.Errorf("flavorSpec.ramMiB must be >= 1 for VM %q", v.VMKey)
			}
			if v.FlavorSpec.DiskGiB < 0 {
				return fmt.Errorf("flavorSpec.diskGiB must be >= 0 for VM %q", v.VMKey)
			}
			if v.VMKey == snap.WorkspaceVMKey {
				workspaceVMFound = true
			}
		}
		if !workspaceVMFound {
			return fmt.Errorf("workspaceVmKey %q does not match any VM in creationSnapshot", snap.WorkspaceVMKey)
		}
		if snap.StartupScript != nil {
			if snap.StartupScript.Content == "" {
				return fmt.Errorf("startupScript content cannot be empty")
			}
			if len(snap.StartupScript.SHA256) != 64 {
				return fmt.Errorf("startupScript sha256 must be 64-character hex string")
			}
		}

	case protocol.MutationTypeCleanup:
		// CLEANUP 시 providerResources 필수 검증 (팀장님 리뷰 1번)
		if cmd.Payload.ProviderResources == nil {
			return fmt.Errorf("providerResources is required for CLEANUP")
		}
		for i, r := range cmd.Payload.ProviderResources {
			if r.ResourceType == "" {
				return fmt.Errorf("resourceType is required for cleanup provider resource at index %d", i)
			}
			if r.ProviderID == "" {
				return fmt.Errorf("providerId is required for cleanup provider resource at index %d", i)
			}
			if r.Generation < 1 {
				return fmt.Errorf("generation must be >= 1 for cleanup provider resource at index %d", i)
			}
		}

	default:
		return fmt.Errorf("unsupported mutationType: %s", cmd.Payload.MutationType)
	}

	return nil
}

func (h *Handler) handleOperationCommand(ctx context.Context, env protocol.BaseEnvelope, raw []byte) error {
	var cmdMsg protocol.OperationCommandMessage
	if err := json.Unmarshal(raw, &cmdMsg); err != nil {
		return fmt.Errorf("failed to unmarshal OPERATION_COMMAND: %w", err)
	}

	// 1. Wire Validation (Envelope 및 Payload 스키마 필수값 엄격 검증)
	if valErr := validateOperationCommand(&cmdMsg); valErr != nil {
		if sender := h.Sender(); sender != nil && cmdMsg.MessageID != "" {
			hasCorrelation := cmdMsg.OperationID != "" && cmdMsg.LabInstanceID != "" && cmdMsg.Generation >= 1
			if !hasCorrelation {
				// Correlation 누락 시 OPERATION_ACK 스키마 필수 조건(operationId, labInstanceId, generation)을
				// 만족할 수 없으므로 올바른 Protocol Error(ERROR) 메시지로 회신 (팀장님 리뷰 2번)
				protoErr := protocol.ProtocolErrorMessage{
					BaseEnvelope: protocol.BaseEnvelope{
						Type:             protocol.MessageTypeError,
						MessageID:        generateUUID(),
						ReplyToMessageID: cmdMsg.MessageID,
						SentAt:           time.Now().UTC(),
						RequestID:        cmdMsg.RequestID,
						TraceParent:      cmdMsg.TraceParent,
						TraceState:       cmdMsg.TraceState,
					},
					Payload: protocol.ProtocolErrorPayload{
						Code:    "INVALID_MESSAGE",
						Message: valErr.Error(),
						Fatal:   false,
					},
				}
				_ = sender.SendMessage(ctx, protoErr)
			} else {
				// Correlation 은 정상이지만 Payload 스키마 유효성 검증 실패 시 OPERATION_ACK(accepted: false) 회신
				ackErr := protocol.OperationAckMessage{
					BaseEnvelope: protocol.BaseEnvelope{
						Type:             protocol.MessageTypeOperationAck,
						MessageID:        generateUUID(),
						ReplyToMessageID: cmdMsg.MessageID,
						SentAt:           time.Now().UTC(),
						RequestID:        cmdMsg.RequestID,
						OperationID:      cmdMsg.OperationID,
						LabInstanceID:    cmdMsg.LabInstanceID,
						Generation:       cmdMsg.Generation,
						TraceParent:      cmdMsg.TraceParent,
						TraceState:       cmdMsg.TraceState,
					},
					Payload: protocol.OperationAckPayload{
						Accepted: false,
						Error: &protocol.SafeError{
							Code:    "INVALID_COMMAND",
							Message: valErr.Error(),
						},
					},
				}
				_ = sender.SendMessage(ctx, ackErr)
			}
		}
		return fmt.Errorf("invalid operation command: %w", valErr)
	}

	// 2. 계약에 따른 OPERATION_ACK(Accepted: true) 즉시 회신
	if sender := h.Sender(); sender != nil {
		ackMsg := protocol.OperationAckMessage{
			BaseEnvelope: protocol.BaseEnvelope{
				Type:             protocol.MessageTypeOperationAck,
				MessageID:        generateUUID(),
				ReplyToMessageID: cmdMsg.MessageID,
				SentAt:           time.Now().UTC(),
				RequestID:        cmdMsg.RequestID,
				OperationID:      cmdMsg.OperationID,
				LabInstanceID:    cmdMsg.LabInstanceID,
				Generation:       cmdMsg.Generation,
				TraceParent:      cmdMsg.TraceParent,
				TraceState:       cmdMsg.TraceState,
			},
			Payload: protocol.OperationAckPayload{
				Accepted: true,
			},
		}
		if err := sender.SendMessage(ctx, ackMsg); err != nil {
			return fmt.Errorf("failed to send OPERATION_ACK: %w", err)
		}
	}

	// 3. 내부 provider.OperationCommand 모델로 변환
	internalCmd := provider.OperationCommand{
		Correlation: provider.Correlation{
			OperationID:   cmdMsg.OperationID,
			LabInstanceID: cmdMsg.LabInstanceID,
			Generation:    cmdMsg.Generation,
		},
		MutationType: provider.MutationType(cmdMsg.Payload.MutationType),
	}

	if cmdMsg.Payload.CreationSnapshot != nil {
		snap := cmdMsg.Payload.CreationSnapshot
		internalSnap := provider.CreationSnapshot{
			ProviderConnectionID: snap.ProviderConnectionID,
			WorkspaceVMKey:       snap.WorkspaceVMKey,
			InternetOutbound:     snap.InternetOutbound,
		}
		if snap.StartupScript != nil {
			internalSnap.StartupScript = &provider.StartupScript{
				Content: snap.StartupScript.Content,
				SHA256:  snap.StartupScript.SHA256,
			}
		}
		for _, v := range snap.VMs {
			vmSpec := provider.VMSpec{
				VMKey:         v.VMKey,
				Role:          v.Role,
				InstanceIndex: v.InstanceIndex,
				ImageID:       v.ImageID,
				FlavorID:      v.FlavorID,
			}
			// imageRef / flavorRef 대체 로직 완전 제거 (스키마 필수값 imageId / flavorId 만 사용)
			if v.FlavorSpec != nil {
				vmSpec.FlavorSpec = provider.FlavorSpec{
					VCPUs:   v.FlavorSpec.VCPUs,
					RAMMiB:  v.FlavorSpec.RAMMiB,
					DiskGiB: v.FlavorSpec.DiskGiB,
				}
			}
			internalSnap.VMs = append(internalSnap.VMs, vmSpec)
		}
		internalCmd.CreationSnapshot = &internalSnap
	}

	if cmdMsg.Payload.ProviderResources != nil {
		for _, r := range cmdMsg.Payload.ProviderResources {
			internalCmd.ProviderResources = append(internalCmd.ProviderResources, provider.ResourceRef{
				ResourceType: r.ResourceType,
				ProviderID:   r.ProviderID,
				Generation:   r.Generation,
				LogicalName:  r.LogicalName,
			})
		}
	}

	// 4. 서빈님의 DispatchOperation 호출 (Provider 작업 수행 및 에러 정규화)
	result, err := provider.DispatchOperation(ctx, h.provider, internalCmd)
	if err != nil {
		result = provider.OperationResult{
			Outcome:           provider.OutcomeFailed,
			ProviderResources: []provider.ResourceResult{},
			Error: &provider.SafeError{
				Code:    "DISPATCH_ERROR",
				Message: err.Error(),
			},
		}
	}

	// 5. wire OPERATION_RESULT 생성 및 SaaS 회신 (Correlation 및 Trace 보존)
	resultMsg := protocol.OperationResultMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:             protocol.MessageTypeOperationResult,
			MessageID:        generateUUID(),
			ReplyToMessageID: cmdMsg.MessageID,
			SentAt:           time.Now().UTC(),
			RequestID:        cmdMsg.RequestID,
			OperationID:      cmdMsg.OperationID,
			LabInstanceID:    cmdMsg.LabInstanceID,
			Generation:       cmdMsg.Generation,
			TraceParent:      cmdMsg.TraceParent,
			TraceState:       cmdMsg.TraceState,
		},
		Payload: protocol.OperationResultPayload{
			Outcome:           string(result.Outcome),
			ProviderResources: make([]protocol.ProviderResourceResult, 0, len(result.ProviderResources)),
		},
	}

	for _, r := range result.ProviderResources {
		resultMsg.Payload.ProviderResources = append(resultMsg.Payload.ProviderResources, protocol.ProviderResourceResult{
			ProviderResourceRef: protocol.ProviderResourceRef{
				ResourceType: r.ResourceType,
				ProviderID:   r.ProviderID,
				Generation:   r.Generation,
				LogicalName:  r.LogicalName,
			},
			ObservedState: r.ObservedState,
		})
	}

	if result.Error != nil {
		resultMsg.Payload.Error = &protocol.SafeError{
			Code:    result.Error.Code,
			Message: result.Error.Message,
		}
	}

	if sender := h.Sender(); sender != nil {
		if err := sender.SendMessage(ctx, resultMsg); err != nil {
			return fmt.Errorf("failed to send OPERATION_RESULT: %w", err)
		}
	}

	return nil
}

// validateReconcileRequest 는 connector.schema.json 기준 RECONCILE_REQUEST 필수 필드를 사전 검증합니다.
func validateReconcileRequest(req *protocol.ReconcileRequestMessage) error {
	if req.MessageID == "" {
		return fmt.Errorf("missing required messageId in envelope")
	}
	if req.OperationID == "" {
		return fmt.Errorf("missing required operationId in envelope")
	}
	if req.LabInstanceID == "" {
		return fmt.Errorf("missing required labInstanceId in envelope")
	}
	if req.Generation < 1 {
		return fmt.Errorf("invalid generation in envelope: must be >= 1, got %d", req.Generation)
	}
	// knownResources 필수 검증 (팀장님 리뷰 3번)
	if req.Payload.KnownResources == nil {
		return fmt.Errorf("knownResources is required in payload")
	}
	for i, r := range req.Payload.KnownResources {
		if r.ResourceType == "" {
			return fmt.Errorf("resourceType is required in knownResources at index %d", i)
		}
		if r.ProviderID == "" {
			return fmt.Errorf("providerId is required in knownResources at index %d", i)
		}
		if r.Generation < 1 {
			return fmt.Errorf("generation must be >= 1 in knownResources at index %d", i)
		}
	}
	return nil
}

func (h *Handler) handleReconcileRequest(ctx context.Context, env protocol.BaseEnvelope, raw []byte) error {
	var reqMsg protocol.ReconcileRequestMessage
	if err := json.Unmarshal(raw, &reqMsg); err != nil {
		return fmt.Errorf("failed to unmarshal RECONCILE_REQUEST: %w", err)
	}

	// 1. Wire Validation (Correlation 및 knownResources 필수값 검증)
	if valErr := validateReconcileRequest(&reqMsg); valErr != nil {
		if sender := h.Sender(); sender != nil && reqMsg.MessageID != "" {
			hasCorrelation := reqMsg.OperationID != "" && reqMsg.LabInstanceID != "" && reqMsg.Generation >= 1
			if !hasCorrelation {
				// Correlation 누락 시 RECONCILE_RESULT 대신 올바른 Protocol Error(ERROR) 메시지로 회신
				protoErr := protocol.ProtocolErrorMessage{
					BaseEnvelope: protocol.BaseEnvelope{
						Type:             protocol.MessageTypeError,
						MessageID:        generateUUID(),
						ReplyToMessageID: reqMsg.MessageID,
						SentAt:           time.Now().UTC(),
						RequestID:        reqMsg.RequestID,
						TraceParent:      reqMsg.TraceParent,
						TraceState:       reqMsg.TraceState,
					},
					Payload: protocol.ProtocolErrorPayload{
						Code:    "INVALID_MESSAGE",
						Message: valErr.Error(),
						Fatal:   false,
					},
				}
				_ = sender.SendMessage(ctx, protoErr)
			} else {
				errRes := protocol.ReconcileResultMessage{
					BaseEnvelope: protocol.BaseEnvelope{
						Type:             protocol.MessageTypeReconcileResult,
						MessageID:        generateUUID(),
						ReplyToMessageID: reqMsg.MessageID,
						SentAt:           time.Now().UTC(),
						RequestID:        reqMsg.RequestID,
						OperationID:      reqMsg.OperationID,
						LabInstanceID:    reqMsg.LabInstanceID,
						Generation:       reqMsg.Generation,
						TraceParent:      reqMsg.TraceParent,
						TraceState:       reqMsg.TraceState,
					},
					Payload: protocol.ReconcileResultPayload{
						Observations: []protocol.ResourceObservation{},
						Error: &protocol.SafeError{
							Code:    "INVALID_REQUEST",
							Message: valErr.Error(),
						},
					},
				}
				_ = sender.SendMessage(ctx, errRes)
			}
		}
		return fmt.Errorf("invalid reconcile request: %w", valErr)
	}

	// 공동 결정 규칙: discoverCandidates 생략 시 true 적용, 명시적 false 유지
	discover := true
	if reqMsg.Payload.DiscoverCandidates != nil {
		discover = *reqMsg.Payload.DiscoverCandidates
	}

	knownResources := make([]provider.ResourceRef, 0, len(reqMsg.Payload.KnownResources))
	for _, r := range reqMsg.Payload.KnownResources {
		knownResources = append(knownResources, provider.ResourceRef{
			ResourceType: r.ResourceType,
			ProviderID:   r.ProviderID,
			Generation:   r.Generation,
			LogicalName:  r.LogicalName,
		})
	}

	internalReq := provider.ReconcileRequest{
		Correlation: provider.Correlation{
			OperationID:   reqMsg.OperationID,
			LabInstanceID: reqMsg.LabInstanceID,
			Generation:    reqMsg.Generation,
		},
		KnownResources:     knownResources,
		DiscoverCandidates: discover,
	}

	result, err := provider.DispatchReconcile(ctx, h.provider, internalReq)
	if err != nil {
		result = provider.ReconcileResult{
			Observations: []provider.ResourceObservation{},
			Error: &provider.SafeError{
				Code:    "DISPATCH_ERROR",
				Message: err.Error(),
			},
		}
	}

	resultMsg := protocol.ReconcileResultMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:             protocol.MessageTypeReconcileResult,
			MessageID:        generateUUID(),
			ReplyToMessageID: reqMsg.MessageID,
			SentAt:           time.Now().UTC(),
			RequestID:        reqMsg.RequestID,
			OperationID:      reqMsg.OperationID,
			LabInstanceID:    reqMsg.LabInstanceID,
			Generation:       reqMsg.Generation,
			TraceParent:      reqMsg.TraceParent,
			TraceState:       reqMsg.TraceState,
		},
		Payload: protocol.ReconcileResultPayload{
			Observations: make([]protocol.ResourceObservation, 0, len(result.Observations)),
		},
	}

	for _, obs := range result.Observations {
		resultMsg.Payload.Observations = append(resultMsg.Payload.Observations, protocol.ResourceObservation{
			ResourceType:  obs.ResourceType,
			ProviderID:    obs.ProviderID,
			Generation:    obs.Generation,
			Exists:        obs.Exists,
			ObservedState: obs.ObservedState,
			Source:        string(obs.Source),
			LogicalName:   obs.LogicalName,
		})
	}

	if result.Error != nil {
		resultMsg.Payload.Error = &protocol.SafeError{
			Code:    result.Error.Code,
			Message: result.Error.Message,
		}
	}

	if sender := h.Sender(); sender != nil {
		if err := sender.SendMessage(ctx, resultMsg); err != nil {
			return fmt.Errorf("failed to send RECONCILE_RESULT: %w", err)
		}
	}

	return nil
}

// Listen 은 연결된 WebSocket 으로부터 메시지를 지속 수신하여 Handler 로 처리합니다.
func (h *Handler) Listen(ctx context.Context, conn *websocket.Conn) error {
	if conn == nil {
		return fmt.Errorf("connector wss: cannot listen on nil websocket connection")
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return err
			}
			// HandleMessage 에러를 무시하지 않고 등록된 onError 콜백으로 전달 (팀장님 리뷰 6번)
			if handleErr := h.HandleMessage(ctx, msg); handleErr != nil {
				h.mu.RLock()
				onErr := h.onError
				h.mu.RUnlock()
				if onErr != nil {
					onErr(handleErr)
				}
			}
		}
	}
}

func (h *Handler) handleTerminalOpen(ctx context.Context, env protocol.BaseEnvelope, raw []byte) error {
	var openMsg protocol.TerminalOpenMessage
	if err := json.Unmarshal(raw, &openMsg); err != nil {
		return fmt.Errorf("failed to unmarshal TERMINAL_OPEN: %w", err)
	}

	h.mu.RLock()
	mgr := h.terminalMgr
	factory := h.ptyFactory
	dataCfg := h.terminalCfg
	h.mu.RUnlock()

	sender := h.Sender()

	// 1. Envelope 필수 필드 검증 (Reviewer 6번 지적 사항)
	if openMsg.MessageID == "" || openMsg.TerminalSessionID == "" || openMsg.LabInstanceID == "" || openMsg.Generation < 1 {
		if sender != nil {
			failResult := protocol.TerminalOpenResultMessage{
				BaseEnvelope: protocol.BaseEnvelope{
					Type:              protocol.MessageTypeTerminalOpenResult,
					MessageID:         generateUUID(),
					ReplyToMessageID:  openMsg.MessageID,
					SentAt:            time.Now().UTC(),
					TerminalSessionID: openMsg.TerminalSessionID,
					LabInstanceID:     openMsg.LabInstanceID,
					Generation:        openMsg.Generation,
				},
				Payload: protocol.TerminalOpenResultPayload{
					Outcome: protocol.OutcomeFailed,
					Error: &protocol.SafeError{
						Code:    protocol.TerminalErrInvalidSession,
						Message: "missing required envelope fields: messageId, terminalSessionId, labInstanceId, or generation < 1",
					},
				},
			}
			_ = sender.SendMessage(ctx, failResult)
		}
		return fmt.Errorf("invalid TERMINAL_OPEN envelope: missing required correlation fields")
	}

	// 2. Payload 필수 필드 검증 (targetVmKey, providerServerId, cols, rows 필수)
	if openMsg.Payload.TargetVmKey == "" || openMsg.Payload.ProviderServerID == "" || openMsg.Payload.Cols <= 0 || openMsg.Payload.Rows <= 0 {
		if sender != nil {
			failResult := protocol.TerminalOpenResultMessage{
				BaseEnvelope: protocol.BaseEnvelope{
					Type:              protocol.MessageTypeTerminalOpenResult,
					MessageID:         generateUUID(),
					ReplyToMessageID:  openMsg.MessageID,
					SentAt:            time.Now().UTC(),
					TerminalSessionID: openMsg.TerminalSessionID,
					LabInstanceID:     openMsg.LabInstanceID,
					Generation:        openMsg.Generation,
				},
				Payload: protocol.TerminalOpenResultPayload{
					Outcome: protocol.OutcomeFailed,
					Error: &protocol.SafeError{
						Code:    protocol.TerminalErrInvalidSession,
						Message: "missing required targetVmKey, providerServerId, cols, or rows",
					},
				},
			}
			_ = sender.SendMessage(ctx, failResult)
		}
		return fmt.Errorf("invalid TERMINAL_OPEN: missing target or dimensions")
	}

	if mgr == nil || factory == nil {
		if sender != nil {
			failResult := protocol.TerminalOpenResultMessage{
				BaseEnvelope: protocol.BaseEnvelope{
					Type:              protocol.MessageTypeTerminalOpenResult,
					MessageID:         generateUUID(),
					ReplyToMessageID:  openMsg.MessageID,
					SentAt:            time.Now().UTC(),
					TerminalSessionID: openMsg.TerminalSessionID,
					LabInstanceID:     openMsg.LabInstanceID,
					Generation:        openMsg.Generation,
				},
				Payload: protocol.TerminalOpenResultPayload{
					Outcome: protocol.OutcomeFailed,
					Error: &protocol.SafeError{
						Code:    protocol.TerminalErrAttachFailed,
						Message: "terminal manager or PTY factory not configured",
					},
				},
			}
			_ = sender.SendMessage(ctx, failResult)
		}
		return fmt.Errorf("terminal manager not configured")
	}

	// 3. Endpoint 미설정 거부 (Reviewer 3번 지적 사항)
	if dataCfg.EndpointURL == "" {
		if sender != nil {
			failResult := protocol.TerminalOpenResultMessage{
				BaseEnvelope: protocol.BaseEnvelope{
					Type:              protocol.MessageTypeTerminalOpenResult,
					MessageID:         generateUUID(),
					ReplyToMessageID:  openMsg.MessageID,
					SentAt:            time.Now().UTC(),
					TerminalSessionID: openMsg.TerminalSessionID,
					LabInstanceID:     openMsg.LabInstanceID,
					Generation:        openMsg.Generation,
				},
				Payload: protocol.TerminalOpenResultPayload{
					Outcome: protocol.OutcomeFailed,
					Error: &protocol.SafeError{
						Code:    protocol.TerminalErrAttachFailed,
						Message: "terminal data wss endpoint not configured",
					},
				},
			}
			_ = sender.SendMessage(ctx, failResult)
		}
		return fmt.Errorf("terminal data wss endpoint not configured")
	}

	// 4. 세션 조회 또는 생성 (동일 Generation/Target 시맨틱 일치성 보장)
	session, _, err := mgr.GetOrCreateSession(openMsg.Payload, openMsg.BaseEnvelope, func() (terminal.PTYChannel, error) {
		return factory(openMsg.Payload.TargetVmKey, openMsg.Payload.ProviderServerID, openMsg.Payload.Cols, openMsg.Payload.Rows)
	})
	if err != nil {
		errCode := protocol.TerminalErrAttachFailed
		if errors.Is(err, terminal.ErrStaleGeneration) || errors.Is(err, terminal.ErrSessionConflict) {
			errCode = protocol.TerminalErrInvalidSession
		}
		if sender != nil {
			failResult := protocol.TerminalOpenResultMessage{
				BaseEnvelope: protocol.BaseEnvelope{
					Type:              protocol.MessageTypeTerminalOpenResult,
					MessageID:         generateUUID(),
					ReplyToMessageID:  openMsg.MessageID,
					SentAt:            time.Now().UTC(),
					TerminalSessionID: openMsg.TerminalSessionID,
					LabInstanceID:     openMsg.LabInstanceID,
					Generation:        openMsg.Generation,
				},
				Payload: protocol.TerminalOpenResultPayload{
					Outcome: protocol.OutcomeFailed,
					Error: &protocol.SafeError{
						Code:    errCode,
						Message: err.Error(),
					},
				},
			}
			_ = sender.SendMessage(ctx, failResult)
		}
		return fmt.Errorf("failed to get/create terminal session: %w", err)
	}

	// 5. Terminal Data WSS 동기식 Dial 및 Attach (Reviewer 3번 지적 사항: attach 완료 전에는 SUCCEEDED 금지)
	dataClient := terminal.NewDataWSSClient(dataCfg, session)
	if attachErr := dataClient.DialAndAttach(ctx); attachErr != nil {
		// Attach 실패 시 세션 및 PTY 정리
		_ = mgr.CloseSession(openMsg.TerminalSessionID, protocol.TerminalReasonSessionClosed)

		if sender != nil {
			failResult := protocol.TerminalOpenResultMessage{
				BaseEnvelope: protocol.BaseEnvelope{
					Type:              protocol.MessageTypeTerminalOpenResult,
					MessageID:         generateUUID(),
					ReplyToMessageID:  openMsg.MessageID,
					SentAt:            time.Now().UTC(),
					TerminalSessionID: openMsg.TerminalSessionID,
					LabInstanceID:     openMsg.LabInstanceID,
					Generation:        openMsg.Generation,
				},
				Payload: protocol.TerminalOpenResultPayload{
					Outcome: protocol.OutcomeFailed,
					Error: &protocol.SafeError{
						Code:    protocol.TerminalErrAttachFailed,
						Message: attachErr.Error(),
					},
				},
			}
			_ = sender.SendMessage(ctx, failResult)
		}
		return fmt.Errorf("failed to attach terminal data WSS: %w", attachErr)
	}

	// 6. Attach 성공 후에만 백그라운드 스트리밍 시작 및 TERMINAL_OPEN_RESULT (SUCCEEDED) 회신
	dataClient.StartStreaming()

	if sender != nil {
		succResult := protocol.TerminalOpenResultMessage{
			BaseEnvelope: protocol.BaseEnvelope{
				Type:              protocol.MessageTypeTerminalOpenResult,
				MessageID:         generateUUID(),
				ReplyToMessageID:  openMsg.MessageID,
				SentAt:            time.Now().UTC(),
				TerminalSessionID: openMsg.TerminalSessionID,
				LabInstanceID:     openMsg.LabInstanceID,
				Generation:        openMsg.Generation,
			},
			Payload: protocol.TerminalOpenResultPayload{
				Outcome: protocol.OutcomeSucceeded,
			},
		}
		_ = sender.SendMessage(ctx, succResult)
	}

	return nil
}

func (h *Handler) handleTerminalClose(ctx context.Context, env protocol.BaseEnvelope, raw []byte) error {
	var closeMsg protocol.TerminalCloseMessage
	if err := json.Unmarshal(raw, &closeMsg); err != nil {
		return fmt.Errorf("failed to unmarshal TERMINAL_CLOSE: %w", err)
	}

	sessionID := closeMsg.TerminalSessionID
	if sessionID == "" {
		sessionID = closeMsg.RequestID
	}

	// Envelope 및 Payload 필수 필드 검증 (Reviewer 6번 지적 사항)
	if closeMsg.MessageID == "" || sessionID == "" || closeMsg.LabInstanceID == "" || closeMsg.Generation < 1 || closeMsg.Payload.Reason == "" {
		return fmt.Errorf("invalid TERMINAL_CLOSE: missing required fields")
	}

	h.mu.RLock()
	mgr := h.terminalMgr
	h.mu.RUnlock()

	if mgr != nil && sessionID != "" {
		session, exists := mgr.GetSession(sessionID)
		if exists {
			// 활성 세션의 labInstanceId 및 generation 일치성 검증 (Reviewer 5번 지적 사항: stale generation 방어)
			if session.LabInstanceID != closeMsg.LabInstanceID || session.Generation != closeMsg.Generation {
				return fmt.Errorf("stale or mismatched TERMINAL_CLOSE: session has lab=%s gen=%d, got lab=%s gen=%d",
					session.LabInstanceID, session.Generation, closeMsg.LabInstanceID, closeMsg.Generation)
			}
			_ = mgr.CloseSession(sessionID, closeMsg.Payload.Reason)
		} else {
			// 세션이 이미 정리된 경우 멱등적으로 TERMINAL_ENDED 회신
			h.sendTerminalEndedRaw(closeMsg.MessageID, sessionID, closeMsg.LabInstanceID, closeMsg.Generation, closeMsg.Payload.Reason, nil, nil)
		}
	} else {
		// 세션 매니저가 없거나 세션이 이미 정리된 경우에도 멱등적으로 TERMINAL_ENDED 회신
		h.sendTerminalEndedRaw(closeMsg.MessageID, sessionID, closeMsg.LabInstanceID, closeMsg.Generation, closeMsg.Payload.Reason, nil, nil)
	}

	return nil
}

func (h *Handler) sendTerminalEnded(session *terminal.Session, reason string, exitCode *int, err error) {
	if session == nil || !session.WasActive() {
		return
	}
	h.sendTerminalEndedRaw("", session.SessionID, session.LabInstanceID, session.Generation, reason, exitCode, err)
}

func (h *Handler) sendTerminalEndedRaw(replyToMsgID string, sessionID, labID string, generation int64, reason string, exitCode *int, err error) {
	if sessionID == "" {
		return
	}
	if _, loaded := h.endedSessions.LoadOrStore(sessionID, true); loaded {
		return
	}

	sender := h.Sender()
	if sender == nil {
		return
	}

	endedMsg := protocol.TerminalEndedMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:              protocol.MessageTypeTerminalEnded,
			MessageID:         generateUUID(),
			ReplyToMessageID:  replyToMsgID,
			SentAt:            time.Now().UTC(),
			TerminalSessionID: sessionID,
			LabInstanceID:     labID,
			Generation:        generation,
		},
		Payload: protocol.TerminalEndedPayload{
			Reason:   reason,
			ExitCode: exitCode,
		},
	}
	if err != nil {
		endedMsg.Payload.Error = &protocol.SafeError{
			Code:    "SESSION_ERROR",
			Message: err.Error(),
		}
	}
	_ = sender.SendMessage(context.Background(), endedMsg)
}
