package provider

import (
	"context"
	"errors"
)

var ErrInvalidReconcileRequest = errors.New("invalid reconcile request")

// DispatchReconcile applies the same input and error boundary as operations.
// A failed Provider lookup is not evidence that a resource is absent.
func DispatchReconcile(ctx context.Context, p Provider, request ReconcileRequest) (ReconcileResult, error) {
	if request.OperationID == "" || request.LabInstanceID == "" || request.Generation < 1 || request.KnownResources == nil {
		return ReconcileResult{}, ErrInvalidReconcileRequest
	}
	if p == nil {
		return ReconcileResult{}, ErrProviderUnavailable
	}

	result, err := p.Reconcile(ctx, ReconcileRequest{
		Correlation:        request.Correlation,
		KnownResources:     cloneResources(request.KnownResources),
		DiscoverCandidates: request.DiscoverCandidates,
	})
	if errors.Is(err, ErrMockNotConfigured) {
		return ReconcileResult{}, ErrMockNotConfigured
	}
	if err != nil {
		return ReconcileResult{
			Observations: []ResourceObservation{},
			Error: &SafeError{
				Code:    "PROVIDER_RECONCILE_UNAVAILABLE",
				Message: "Provider resources could not be verified",
			},
		}, nil
	}
	if result.Observations == nil {
		result.Observations = []ResourceObservation{}
	}
	return result, nil
}
