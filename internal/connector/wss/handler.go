package wss

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/rabbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/rabbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/rabbit-app/internal/connector/terminal"
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
	mu          sync.RWMutex
	provider    provider.Provider
	sender      MessageSender
	terminalMgr *terminal.SessionManager
	ptyFactory  PTYFactoryFunc
	terminalCfg terminal.DataWSSClientConfig
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
func (h *Handler) SetTerminalManager(mgr *terminal.SessionManager, ptyFactory PTYFactoryFunc, cfg terminal.DataWSSClientConfig) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.terminalMgr = mgr
	h.ptyFactory = ptyFactory
	h.terminalCfg = cfg
}

// Sender 는 현재 설정된 MessageSender 를 반환합니다.
func (h *Handler) Sender() MessageSender {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.sender
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
		// 알 수 없는 메시지 타입이거나 핸들러 대상이 아닌 메시지는 무시
		return nil
	}
}

func (h *Handler) handleOperationCommand(ctx context.Context, env protocol.BaseEnvelope, raw []byte) error {
	var cmdMsg protocol.OperationCommandMessage
	if err := json.Unmarshal(raw, &cmdMsg); err != nil {
		return fmt.Errorf("failed to unmarshal OPERATION_COMMAND: %w", err)
	}

	// 1. 필수 Correlation 필드 검증 (OperationID, LabInstanceID, Generation >= 1)
	if cmdMsg.OperationID == "" || cmdMsg.LabInstanceID == "" || cmdMsg.Generation < 1 {
		if sender := h.Sender(); sender != nil {
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
						Message: "missing required correlation fields in envelope",
					},
				},
			}
			_ = sender.SendMessage(ctx, ackErr)
		}
		return fmt.Errorf("invalid operation command: missing correlation fields")
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
			if vmSpec.ImageID == "" && v.ImageRef != "" {
				vmSpec.ImageID = v.ImageRef
			}
			if vmSpec.FlavorID == "" && v.FlavorRef != "" {
				vmSpec.FlavorID = v.FlavorRef
			}
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

func (h *Handler) handleReconcileRequest(ctx context.Context, env protocol.BaseEnvelope, raw []byte) error {
	var reqMsg protocol.ReconcileRequestMessage
	if err := json.Unmarshal(raw, &reqMsg); err != nil {
		return fmt.Errorf("failed to unmarshal RECONCILE_REQUEST: %w", err)
	}

	if reqMsg.OperationID == "" || reqMsg.LabInstanceID == "" || reqMsg.Generation < 1 {
		return fmt.Errorf("invalid reconcile request: missing correlation fields")
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
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return err
			}
			_ = h.HandleMessage(ctx, msg)
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

	// 1. 유효성 검증 (targetVmKey, providerServerId, cols, rows 필수)
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

	// 2. 세션 조회 또는 생성 (멱등성 보장)
	session, _, err := mgr.GetOrCreateSession(openMsg.Payload, openMsg.BaseEnvelope, func() (terminal.PTYChannel, error) {
		return factory(openMsg.Payload.TargetVmKey, openMsg.Payload.ProviderServerID, openMsg.Payload.Cols, openMsg.Payload.Rows)
	})
	if err != nil {
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
						Message: "failed to create terminal session",
					},
				},
			}
			_ = sender.SendMessage(ctx, failResult)
		}
		return fmt.Errorf("failed to get/create terminal session: %w", err)
	}

	// 3. Terminal Data WSS Endpoint 가 설정되어 있다면 백그라운드로 연결 및 스트리밍 개시
	if dataCfg.EndpointURL != "" {
		dataClient := terminal.NewDataWSSClient(dataCfg, session)
		go func() {
			_ = dataClient.ConnectAndStream()
		}()
	}

	// 4. TERMINAL_OPEN_RESULT (SUCCEEDED) 회신
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

	h.mu.RLock()
	mgr := h.terminalMgr
	h.mu.RUnlock()

	if mgr != nil && sessionID != "" {
		_ = mgr.CloseSession(sessionID, closeMsg.Payload.Reason)
	}

	// TERMINAL_ENDED 회신
	if sender := h.Sender(); sender != nil {
		endedMsg := protocol.TerminalEndedMessage{
			BaseEnvelope: protocol.BaseEnvelope{
				Type:              protocol.MessageTypeTerminalEnded,
				MessageID:         generateUUID(),
				ReplyToMessageID:  closeMsg.MessageID,
				SentAt:            time.Now().UTC(),
				TerminalSessionID: sessionID,
				LabInstanceID:     closeMsg.LabInstanceID,
				Generation:        closeMsg.Generation,
			},
			Payload: protocol.TerminalEndedPayload{
				Reason: closeMsg.Payload.Reason,
			},
		}
		_ = sender.SendMessage(ctx, endedMsg)
	}

	return nil
}

