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

// DispatchOperation routes one mutation to one Provider call without retrying.
// Its own errors occur before any Provider call; an unclassified Provider error
// becomes UNKNOWN without exposing the raw error to Control.
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
		result, err := p.Provision(ctx, ProvisionRequest{
			Correlation:      command.Correlation,
			CreationSnapshot: cloneSnapshot(*command.CreationSnapshot),
		})
		return safeOperationResult(result, err)
	case MutationReset:
		if command.CreationSnapshot == nil {
			return OperationResult{}, ErrInvalidCommand
		}
		result, err := p.Reset(ctx, ResetRequest{
			Correlation:       command.Correlation,
			CreationSnapshot:  cloneSnapshot(*command.CreationSnapshot),
			ProviderResources: cloneResources(command.ProviderResources),
		})
		return safeOperationResult(result, err)
	case MutationCleanup:
		if command.ProviderResources == nil {
			return OperationResult{}, ErrInvalidCommand
		}
		result, err := p.Cleanup(ctx, CleanupRequest{
			Correlation:       command.Correlation,
			ProviderResources: cloneResources(command.ProviderResources),
		})
		return safeOperationResult(result, err)
	default:
		return OperationResult{}, ErrInvalidCommand
	}
}

func safeOperationResult(result OperationResult, err error) (OperationResult, error) {
	if errors.Is(err, ErrMockNotConfigured) {
		return OperationResult{}, ErrMockNotConfigured
	}
	if err != nil || (result.Outcome != OutcomeSucceeded && result.Outcome != OutcomeFailed && result.Outcome != OutcomeUnknown) {
		resources := make([]ResourceResult, len(result.ProviderResources))
		copy(resources, result.ProviderResources)
		return OperationResult{Outcome: OutcomeUnknown, ProviderResources: resources}, nil
	}
	if result.ProviderResources == nil {
		result.ProviderResources = []ResourceResult{}
	}
	return result, nil
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
