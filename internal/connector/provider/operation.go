package provider

import (
	"context"
	"errors"
)

type MutationType string

const (
	MutationProvision MutationType = "PROVISION"
	MutationReset     MutationType = "RESET"
	MutationCleanup   MutationType = "CLEANUP"
)

var ErrInvalidCommand = errors.New("invalid provider command")
var ErrProviderUnavailable = errors.New("provider not configured")

// OperationCommand is an internal command, not a Control WSS message.
type OperationCommand struct {
	Correlation
	MutationType      MutationType
	CreationSnapshot  *CreationSnapshot
	ProviderResources []ResourceRef
}

// DispatchOperation routes one mutation to one Provider call. It does not retry or
// decide whether a Provider error means FAILED or UNKNOWN.
func DispatchOperation(ctx context.Context, p Provider, command OperationCommand) (OperationResult, error) {
	if command.OperationID == "" || command.LabInstanceID == "" || command.Generation < 1 {
		return OperationResult{}, ErrInvalidCommand
	}
	if p == nil {
		return OperationResult{}, ErrProviderUnavailable
	}

	switch command.MutationType {
	case MutationProvision:
		if command.CreationSnapshot == nil {
			return OperationResult{}, ErrInvalidCommand
		}
		return p.Provision(ctx, ProvisionRequest{
			Correlation:      command.Correlation,
			CreationSnapshot: cloneSnapshot(*command.CreationSnapshot),
		})
	case MutationReset:
		if command.CreationSnapshot == nil {
			return OperationResult{}, ErrInvalidCommand
		}
		return p.Reset(ctx, ResetRequest{
			Correlation:       command.Correlation,
			CreationSnapshot:  cloneSnapshot(*command.CreationSnapshot),
			ProviderResources: cloneResources(command.ProviderResources),
		})
	case MutationCleanup:
		if command.ProviderResources == nil {
			return OperationResult{}, ErrInvalidCommand
		}
		return p.Cleanup(ctx, CleanupRequest{
			Correlation:       command.Correlation,
			ProviderResources: cloneResources(command.ProviderResources),
		})
	default:
		return OperationResult{}, ErrInvalidCommand
	}
}

func cloneSnapshot(snapshot CreationSnapshot) CreationSnapshot {
	snapshot.VMs = append([]VMSpec(nil), snapshot.VMs...)
	if snapshot.StartupScript != nil {
		script := *snapshot.StartupScript
		snapshot.StartupScript = &script
	}
	return snapshot
}

func cloneResources(resources []ResourceRef) []ResourceRef {
	if resources == nil {
		return nil
	}
	cloned := make([]ResourceRef, len(resources))
	copy(cloned, resources)
	return cloned
}
