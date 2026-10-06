package provider

import (
	"context"
	"errors"
)

var ErrMockNotConfigured = errors.New("mock provider method not configured")

// MockProvider lets Control tests supply outcomes without OpenStack or Gophercloud.
// An unconfigured method fails explicitly instead of reporting a false success.
type MockProvider struct {
	ConnectionID             string
	ValidateConnectionFunc   func(context.Context) error
	ListImagesFunc           func(context.Context) ([]Image, error)
	ListFlavorsFunc          func(context.Context) ([]Flavor, error)
	ProvisionFunc            func(context.Context, ProvisionRequest) (OperationResult, error)
	ResetFunc                func(context.Context, ResetRequest) (OperationResult, error)
	CleanupFunc              func(context.Context, CleanupRequest) (OperationResult, error)
	ReconcileFunc            func(context.Context, ReconcileRequest) (ReconcileResult, error)
	ResolveServerAddressFunc func(context.Context, string, string) (string, error)
}

var _ Provider = (*MockProvider)(nil)
var _ QueryProvider = (*MockProvider)(nil)
var _ ServerAddressResolver = (*MockProvider)(nil)

func (m *MockProvider) ProviderConnectionID() string {
	if m == nil {
		return ""
	}
	return m.ConnectionID
}

func (m *MockProvider) ValidateConnection(ctx context.Context) error {
	if m == nil || m.ValidateConnectionFunc == nil {
		return ErrMockNotConfigured
	}
	return m.ValidateConnectionFunc(ctx)
}

func (m *MockProvider) ListImages(ctx context.Context) ([]Image, error) {
	if m == nil || m.ListImagesFunc == nil {
		return nil, ErrMockNotConfigured
	}
	return m.ListImagesFunc(ctx)
}

func (m *MockProvider) ListFlavors(ctx context.Context) ([]Flavor, error) {
	if m == nil || m.ListFlavorsFunc == nil {
		return nil, ErrMockNotConfigured
	}
	return m.ListFlavorsFunc(ctx)
}

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

func (m *MockProvider) ResolveServerAddress(ctx context.Context, targetVmKey, serverID string) (string, error) {
	if m == nil || m.ResolveServerAddressFunc == nil {
		return "", ErrMockNotConfigured
	}
	return m.ResolveServerAddressFunc(ctx, targetVmKey, serverID)
}
