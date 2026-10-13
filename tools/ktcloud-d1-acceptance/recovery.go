package main

import (
	"context"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

type recoveryActions struct {
	observe func(context.Context, string, []protocol.ProviderResourceRef) ([]protocol.ResourceObservation, error)
	cleanup func(context.Context, []protocol.ProviderResourceRef) error
	record  func([]protocol.ResourceObservation)
	save    func(string) error
}

// Recovery must validate the whole typed inventory before changing the ledger
// or requesting Cleanup. A count match is not proof of resource ownership.
func recoverKnownResources(ctx context.Context, known []protocol.ProviderResourceRef, readOnly bool, actions recoveryActions) error {
	observations, err := actions.observe(ctx, "recover-observe-before-any-mutation", known)
	if err != nil {
		return err
	}
	present, err := ownedPresentResources(known, observations)
	if err != nil {
		return err
	}
	actions.record(observations)
	if err := actions.save("RECOVERY_RECONCILE_BEFORE_MUTATION_PASS"); err != nil {
		return err
	}
	if readOnly {
		return actions.save("CURRENT_CONNECTOR_READ_ONLY_RECONCILE_COMPLETE_FULL_ACCEPTANCE_PENDING")
	}
	if len(present) != 0 {
		if err := actions.cleanup(ctx, present); err != nil {
			return err
		}
	}
	observations, err = actions.observe(ctx, "recover-confirm-absence", known)
	if err != nil {
		return err
	}
	if err := confirmKnownAbsence(known, observations); err != nil {
		return err
	}
	actions.record(observations)
	return actions.save("RECOVERY_KNOWN_RESOURCE_CLEANUP_ABSENCE_CONFIRMED")
}
