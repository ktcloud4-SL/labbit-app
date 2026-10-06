package wss

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

func validTestCreationSnapshot() *protocol.CreationSnapshot {
	return &protocol.CreationSnapshot{
		ProviderConnectionID: "pc-1",
		WorkspaceVMKey:       "vm-1",
		VMs: []protocol.ResolvedVmSpec{
			{
				VMKey:         "vm-1",
				Role:          "workspace",
				InstanceIndex: 0,
				ImageID:       "img-1",
				FlavorID:      "fl-1",
				FlavorSpec: &protocol.ResolvedFlavorSpec{
					VCPUs:   1,
					RAMMiB:  512,
					DiskGiB: 10,
				},
			},
		},
	}
}

func validResetCommandMessage() *protocol.OperationCommandMessage {
	return &protocol.OperationCommandMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:          protocol.MessageTypeOperationCommand,
			MessageID:     "msg-reset-valid-1",
			SentAt:        time.Now().UTC(),
			OperationID:   "op-reset-1",
			LabInstanceID: "lab-inst-1",
			Generation:    2,
		},
		Payload: protocol.OperationCommandPayload{
			MutationType:     protocol.MutationTypeReset,
			CreationSnapshot: validTestCreationSnapshot(),
			ProviderResources: []protocol.ProviderResourceRef{
				{
					ResourceType: "SERVER",
					ProviderID:   "srv-1",
					LogicalName:  "vm-1",
					Generation:   1,
				},
			},
		},
	}
}

func cloneCommand(cmd *protocol.OperationCommandMessage) *protocol.OperationCommandMessage {
	raw, err := json.Marshal(cmd)
	if err != nil {
		panic(err)
	}
	var clone protocol.OperationCommandMessage
	if err := json.Unmarshal(raw, &clone); err != nil {
		panic(err)
	}
	return &clone
}

func TestValidateOperationCommand_Reset_Rejections(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*protocol.OperationCommandMessage)
		errKeyword string
	}{
		{
			name: "1. providerResources == nil",
			mutate: func(cmd *protocol.OperationCommandMessage) {
				cmd.Payload.ProviderResources = nil
			},
			errKeyword: "providerResources is required for RESET",
		},
		{
			name: "2. providerResources == []",
			mutate: func(cmd *protocol.OperationCommandMessage) {
				cmd.Payload.ProviderResources = []protocol.ProviderResourceRef{}
			},
			errKeyword: "providerResources must not be empty for RESET",
		},
		{
			name: "3. resource logicalName == \"\"",
			mutate: func(cmd *protocol.OperationCommandMessage) {
				cmd.Payload.ProviderResources[0].LogicalName = ""
			},
			errKeyword: "logicalName is required",
		},
		{
			name: "4. resource resourceType == \"\"",
			mutate: func(cmd *protocol.OperationCommandMessage) {
				cmd.Payload.ProviderResources[0].ResourceType = ""
			},
			errKeyword: "resourceType is required",
		},
		{
			name: "5. resource providerId == \"\"",
			mutate: func(cmd *protocol.OperationCommandMessage) {
				cmd.Payload.ProviderResources[0].ProviderID = ""
			},
			errKeyword: "providerId is required",
		},
		{
			name: "6. resource generation == RESET current generation",
			mutate: func(cmd *protocol.OperationCommandMessage) {
				cmd.Generation = 3
				cmd.Payload.ProviderResources[0].Generation = 3
			},
			errKeyword: "generation must match previous generation",
		},
		{
			name: "7. resource generation older than previous generation",
			mutate: func(cmd *protocol.OperationCommandMessage) {
				cmd.Generation = 3
				cmd.Payload.ProviderResources[0].Generation = 1
			},
			errKeyword: "generation must match previous generation",
		},
		{
			name: "8. resource generation newer than expected previous generation",
			mutate: func(cmd *protocol.OperationCommandMessage) {
				cmd.Generation = 3
				cmd.Payload.ProviderResources[0].Generation = 4
			},
			errKeyword: "generation must match previous generation",
		},
		{
			name: "9. RESET command generation == 1",
			mutate: func(cmd *protocol.OperationCommandMessage) {
				cmd.Generation = 1
				cmd.Payload.ProviderResources[0].Generation = 1
			},
			errKeyword: "generation must be >= 2 for RESET",
		},
		{
			name: "10. RESET generation 1 + resource generation 0",
			mutate: func(cmd *protocol.OperationCommandMessage) {
				cmd.Generation = 1
				cmd.Payload.ProviderResources[0].Generation = 0
			},
			errKeyword: "generation must be >= 2 for RESET",
		},
		{
			name: "11. 여러 resource 중 두 번째 resource만 invalid",
			mutate: func(cmd *protocol.OperationCommandMessage) {
				cmd.Payload.ProviderResources = append(cmd.Payload.ProviderResources, protocol.ProviderResourceRef{
					ResourceType: "SERVER",
					ProviderID:   "srv-2",
					LogicalName:  "",
					Generation:   1,
				})
			},
			errKeyword: "logicalName is required for reset provider resource at index 1",
		},
		{
			name: "12. creationSnapshot missing",
			mutate: func(cmd *protocol.OperationCommandMessage) {
				cmd.Payload.CreationSnapshot = nil
			},
			errKeyword: "creationSnapshot is required for RESET",
		},
		{
			name: "13. resource generation 0 with valid command generation 2",
			mutate: func(cmd *protocol.OperationCommandMessage) {
				cmd.Generation = 2
				cmd.Payload.ProviderResources[0].Generation = 0
			},
			errKeyword: "generation must be >= 1 for reset provider resource at index 0",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := cloneCommand(validResetCommandMessage())
			tc.mutate(cmd)

			err := validateOperationCommand(cmd)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.errKeyword)
			}
			if tc.errKeyword != "" && !strings.Contains(err.Error(), tc.errKeyword) {
				t.Fatalf("expected error containing %q, got %q", tc.errKeyword, err.Error())
			}
		})
	}
}

func TestHandler_Reset_Rejections_DoNotCallProvider(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*protocol.OperationCommandMessage)
	}{
		{
			name: "1. providerResources == nil",
			mutate: func(cmd *protocol.OperationCommandMessage) {
				cmd.Payload.ProviderResources = nil
			},
		},
		{
			name: "2. providerResources == []",
			mutate: func(cmd *protocol.OperationCommandMessage) {
				cmd.Payload.ProviderResources = []protocol.ProviderResourceRef{}
			},
		},
		{
			name: "3. resource logicalName == \"\"",
			mutate: func(cmd *protocol.OperationCommandMessage) {
				cmd.Payload.ProviderResources[0].LogicalName = ""
			},
		},
		{
			name: "4. resource resourceType == \"\"",
			mutate: func(cmd *protocol.OperationCommandMessage) {
				cmd.Payload.ProviderResources[0].ResourceType = ""
			},
		},
		{
			name: "5. resource providerId == \"\"",
			mutate: func(cmd *protocol.OperationCommandMessage) {
				cmd.Payload.ProviderResources[0].ProviderID = ""
			},
		},
		{
			name: "6. resource generation == RESET current generation",
			mutate: func(cmd *protocol.OperationCommandMessage) {
				cmd.Generation = 3
				cmd.Payload.ProviderResources[0].Generation = 3
			},
		},
		{
			name: "7. resource generation older than previous generation",
			mutate: func(cmd *protocol.OperationCommandMessage) {
				cmd.Generation = 3
				cmd.Payload.ProviderResources[0].Generation = 1
			},
		},
		{
			name: "8. resource generation newer than expected previous generation",
			mutate: func(cmd *protocol.OperationCommandMessage) {
				cmd.Generation = 3
				cmd.Payload.ProviderResources[0].Generation = 4
			},
		},
		{
			name: "9. RESET command generation == 1",
			mutate: func(cmd *protocol.OperationCommandMessage) {
				cmd.Generation = 1
				cmd.Payload.ProviderResources[0].Generation = 1
			},
		},
		{
			name: "10. RESET generation 1 + resource generation 0",
			mutate: func(cmd *protocol.OperationCommandMessage) {
				cmd.Generation = 1
				cmd.Payload.ProviderResources[0].Generation = 0
			},
		},
		{
			name: "11. 여러 resource 중 두 번째 resource만 invalid",
			mutate: func(cmd *protocol.OperationCommandMessage) {
				cmd.Payload.ProviderResources = append(cmd.Payload.ProviderResources, protocol.ProviderResourceRef{
					ResourceType: "SERVER",
					ProviderID:   "srv-2",
					LogicalName:  "",
					Generation:   1,
				})
			},
		},
		{
			name: "12. creationSnapshot missing",
			mutate: func(cmd *protocol.OperationCommandMessage) {
				cmd.Payload.CreationSnapshot = nil
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := cloneCommand(validResetCommandMessage())
			tc.mutate(cmd)

			resetCalls := 0
			prov := &provider.MockProvider{
				ResetFunc: func(_ context.Context, _ provider.ResetRequest) (provider.OperationResult, error) {
					resetCalls++
					return provider.OperationResult{Outcome: provider.OutcomeSucceeded}, nil
				},
			}

			var sentMessages []any
			sender := SendMessageFunc(func(_ context.Context, msg any) error {
				sentMessages = append(sentMessages, msg)
				return nil
			})

			handler := NewHandler(prov, sender)
			raw, err := json.Marshal(cmd)
			if err != nil {
				t.Fatalf("marshal error: %v", err)
			}

			handleErr := handler.HandleMessage(context.Background(), raw)
			if handleErr == nil {
				t.Fatal("expected HandleMessage to return validation error, got nil")
			}

			if resetCalls != 0 {
				t.Fatalf("expected Provider.Reset to be called 0 times, got %d", resetCalls)
			}

			if len(sentMessages) == 0 {
				t.Fatal("expected negative ACK to be sent, but no message was sent")
			}

			ack, ok := sentMessages[0].(protocol.OperationAckMessage)
			if !ok {
				t.Fatalf("expected OperationAckMessage, got %T", sentMessages[0])
			}

			if ack.Payload.Accepted {
				t.Fatalf("expected accepted=false, got accepted=true")
			}
			if ack.Payload.Error == nil || ack.Payload.Error.Code != "INVALID_COMMAND" {
				t.Fatalf("expected SafeError code INVALID_COMMAND, got %+v", ack.Payload.Error)
			}
		})
	}
}

func TestHandler_Reset_ValidCase_DispatchesToProvider(t *testing.T) {
	cmd := validResetCommandMessage()

	resetCalls := 0
	var receivedReq provider.ResetRequest
	prov := &provider.MockProvider{
		ResetFunc: func(_ context.Context, req provider.ResetRequest) (provider.OperationResult, error) {
			resetCalls++
			receivedReq = req
			return provider.OperationResult{
				Outcome: provider.OutcomeSucceeded,
				ProviderResources: []provider.ResourceResult{
					{
						ResourceType:  "SERVER",
						ProviderID:    "srv-new-1",
						Generation:    2,
						LogicalName:   "vm-1",
						ObservedState: "ACTIVE",
					},
				},
			}, nil
		},
	}

	var sentMessages []any
	sender := SendMessageFunc(func(_ context.Context, msg any) error {
		sentMessages = append(sentMessages, msg)
		return nil
	})

	handler := NewHandler(prov, sender)
	raw, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	handleErr := handler.HandleMessage(context.Background(), raw)
	if handleErr != nil {
		t.Fatalf("expected HandleMessage to succeed, got %v", handleErr)
	}

	if resetCalls != 1 {
		t.Fatalf("expected Provider.Reset to be called 1 time, got %d", resetCalls)
	}

	if receivedReq.Correlation.OperationID != "op-reset-1" {
		t.Errorf("expected OperationID 'op-reset-1', got %q", receivedReq.Correlation.OperationID)
	}
	if receivedReq.Correlation.Generation != 2 {
		t.Errorf("expected Generation 2, got %d", receivedReq.Correlation.Generation)
	}
	if len(receivedReq.ProviderResources) != 1 {
		t.Fatalf("expected 1 ProviderResource, got %d", len(receivedReq.ProviderResources))
	}
	if receivedReq.ProviderResources[0].LogicalName != "vm-1" || receivedReq.ProviderResources[0].Generation != 1 {
		t.Errorf("unexpected ProviderResource: %+v", receivedReq.ProviderResources[0])
	}
	if receivedReq.CreationSnapshot.ProviderConnectionID != "pc-1" {
		t.Errorf("unexpected snapshot: %+v", receivedReq.CreationSnapshot)
	}

	if len(sentMessages) != 2 {
		t.Fatalf("expected 2 messages sent (ACK, RESULT), got %d", len(sentMessages))
	}

	ack, ok := sentMessages[0].(protocol.OperationAckMessage)
	if !ok || !ack.Payload.Accepted {
		t.Fatalf("expected positive ACK, got %+v", sentMessages[0])
	}

	result, ok := sentMessages[1].(protocol.OperationResultMessage)
	if !ok || result.Payload.Outcome != "SUCCEEDED" {
		t.Fatalf("expected SUCCEEDED RESULT, got %+v", sentMessages[1])
	}
}
