package wss

import (
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

func TestValidateOperationCommandResetRequiresPreviousGenerationResources(t *testing.T) {
	snapshot := &protocol.CreationSnapshot{
		ProviderConnectionID: "provider-1",
		VMs: []protocol.ResolvedVmSpec{{
			VMKey: "workspace", Role: "WORKSPACE", ImageID: "image-1", FlavorID: "flavor-1",
			FlavorSpec: &protocol.ResolvedFlavorSpec{VCPUs: 1, RAMMiB: 1024, DiskGiB: 10},
		}},
		WorkspaceVMKey: "workspace",
	}
	validResource := protocol.ProviderResourceRef{
		ResourceType: provider.ResourceTypeServer,
		ProviderID:   "server-generation-1",
		Generation:   1,
		LogicalName:  "workspace",
	}
	base := protocol.OperationCommandMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type: protocol.MessageTypeOperationCommand, MessageID: "reset-message", SentAt: time.Now().UTC(),
			OperationID: "reset-operation", LabInstanceID: "lab-1", Generation: 2,
		},
		Payload: protocol.OperationCommandPayload{MutationType: protocol.MutationTypeReset, CreationSnapshot: snapshot},
	}

	tests := []struct {
		name      string
		resources []protocol.ProviderResourceRef
		wantError bool
	}{
		{name: "missing", resources: nil, wantError: true},
		{name: "empty", resources: []protocol.ProviderResourceRef{}, wantError: true},
		{name: "missing logical name", resources: []protocol.ProviderResourceRef{{ResourceType: validResource.ResourceType, ProviderID: validResource.ProviderID, Generation: validResource.Generation}}, wantError: true},
		{name: "wrong generation", resources: []protocol.ProviderResourceRef{{ResourceType: validResource.ResourceType, ProviderID: validResource.ProviderID, Generation: 2, LogicalName: validResource.LogicalName}}, wantError: true},
		{name: "valid previous generation", resources: []protocol.ProviderResourceRef{validResource}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := base
			command.Payload.ProviderResources = test.resources
			err := validateOperationCommand(&command)
			if (err != nil) != test.wantError {
				t.Fatalf("validateOperationCommand() error = %v, wantError=%t", err, test.wantError)
			}
		})
	}
}
