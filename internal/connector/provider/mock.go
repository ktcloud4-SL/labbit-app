package provider

import (
	"context"
	"errors"
)

var ErrMockNotConfigured = errors.New("mock provider method not configured")

// MockProvider lets Control tests supply outcomes without OpenStack or Gophercloud.
// An unconfigured method fails explicitly instead of reporting a false success.
type MockProvider struct {
	ProvisionFunc func(context.Context, ProvisionRequest) (OperationResult, error)
	ResetFunc     func(context.Context, ResetRequest) (OperationResult, error)
	CleanupFunc   func(context.Context, CleanupRequest) (OperationResult, error)
	ReconcileFunc func(context.Context, ReconcileRequest) (ReconcileResult, error)
}

var _ Provider = (*MockProvider)(nil)

func (m *MockProvider) Provision(ctx context.Context, request ProvisionRequest) (OperationResult, error) {
	if m == nil || m.ProvisionFunc == nil {
		return OperationResult{}, ErrMockNotConfigured
	}
	return m.ProvisionFunc(ctx, request)
}

func (m *MockProvider) Reset(ctx context.Context, request ResetRequest) (OperationResult, error) {
	if m == nil || m.ResetFunc == nil {
		return OperationResult{}, ErrMockNotConfigured
	}
	return m.ResetFunc(ctx, request)
}

func (m *MockProvider) Cleanup(ctx context.Context, request CleanupRequest) (OperationResult, error) {
	if m == nil || m.CleanupFunc == nil {
		return OperationResult{}, ErrMockNotConfigured
	}
	return m.CleanupFunc(ctx, request)
}

func (m *MockProvider) Reconcile(ctx context.Context, request ReconcileRequest) (ReconcileResult, error) {
	if m == nil || m.ReconcileFunc == nil {
		return ReconcileResult{}, ErrMockNotConfigured
	}
	return m.ReconcileFunc(ctx, request)
}
