package main

import (
	"context"
	"errors"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

// Harness-only scope selection; does not change the Connector runtime contract.
func plannedGenerations(value string) (int, error) {
	switch value {
	case "", "2":
		return 2, nil
	case "1":
		return 1, nil
	default:
		return 0, errors.New("Control generations must be 1 or 2")
	}
}

// A failed mutation may have already removed resources. Never retry it from
// the previous result alone; require a complete exact-ID observation first.
func cleanupAfterReconcile(ctx context.Context, known []protocol.ProviderResourceRef,
	observe func(context.Context, []protocol.ProviderResourceRef) ([]protocol.ResourceObservation, error),
	cleanup func(context.Context, []protocol.ProviderResourceRef) error) error {
	observations, err := observe(ctx, known)
	if err != nil {
		return err
	}
	present, err := ownedPresentResources(known, observations)
	if err != nil {
		return err
	}
	if len(present) == 0 {
		return nil
	}
	return cleanup(ctx, present)
}

func ownedPresentResources(known []protocol.ProviderResourceRef, observations []protocol.ResourceObservation) ([]protocol.ProviderResourceRef, error) {
	if len(known) == 0 || len(observations) != len(known) {
		return nil, errors.New("teardown inventory incomplete")
	}
	pending := make(map[protocol.ProviderResourceRef]bool, len(known))
	for _, ref := range known {
		if pending[ref] {
			return nil, errors.New("duplicate teardown resource")
		}
		pending[ref] = true
	}
	present := []protocol.ProviderResourceRef{}
	for _, observation := range observations {
		ref := protocol.ProviderResourceRef{ResourceType: observation.ResourceType, ProviderID: observation.ProviderID, Generation: observation.Generation, LogicalName: observation.LogicalName}
		if !pending[ref] || observation.Source != "KNOWN_RESOURCE" {
			return nil, errors.New("teardown exact owned inventory uncertain")
		}
		delete(pending, ref)
		if observation.Exists {
			if observation.ObservedState == "" || observation.ObservedState == "UNKNOWN" || observation.ObservedState == "ABSENT" {
				return nil, errors.New("teardown present state uncertain")
			}
			present = append(present, ref)
		} else if observation.ObservedState != "ABSENT" {
			return nil, errors.New("teardown absence uncertain")
		}
	}
	return present, nil
}

// Cardinality alone cannot prove that every exact owned ID is absent.
func confirmKnownAbsence(known []protocol.ProviderResourceRef, observations []protocol.ResourceObservation) error {
	return confirmKnownState(known, observations, func(protocol.ProviderResourceRef) bool { return false })
}

func confirmKnownPresence(known []protocol.ProviderResourceRef, observations []protocol.ResourceObservation) error {
	return confirmKnownState(known, observations, func(protocol.ProviderResourceRef) bool { return true })
}

func confirmResetReality(old, replacement []protocol.ProviderResourceRef, observations []protocol.ResourceObservation) error {
	if len(old) == 0 || len(replacement) == 0 {
		return errors.New("Reset generation reference set incomplete")
	}
	newRefs := make(map[protocol.ProviderResourceRef]bool, len(replacement))
	for _, ref := range replacement {
		newRefs[ref] = true
	}
	all := append(append([]protocol.ProviderResourceRef{}, old...), replacement...)
	return confirmKnownState(all, observations, func(ref protocol.ProviderResourceRef) bool { return newRefs[ref] })
}

func confirmKnownState(known []protocol.ProviderResourceRef, observations []protocol.ResourceObservation, wantExists func(protocol.ProviderResourceRef) bool) error {
	if len(known) == 0 || len(observations) != len(known) {
		return errors.New("final owned inventory incomplete")
	}
	pending := make(map[protocol.ProviderResourceRef]bool, len(known))
	for _, ref := range known {
		if pending[ref] {
			return errors.New("duplicate owned resource reference")
		}
		pending[ref] = true
	}
	for _, observation := range observations {
		ref := protocol.ProviderResourceRef{ResourceType: observation.ResourceType, ProviderID: observation.ProviderID, Generation: observation.Generation, LogicalName: observation.LogicalName}
		if !pending[ref] || observation.Source != "KNOWN_RESOURCE" || observation.Exists != wantExists(ref) {
			return errors.New("exact owned resource reality not confirmed")
		}
		if (!observation.Exists && observation.ObservedState != "ABSENT") || (observation.Exists && (observation.ObservedState == "" || observation.ObservedState == "UNKNOWN" || observation.ObservedState == "ABSENT")) {
			return errors.New("exact owned resource state uncertain")
		}
		delete(pending, ref)
	}
	return nil
}
