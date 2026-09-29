package app

import (
	"context"
	"errors"
	"sync"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

var errProviderInitialization = errors.New("connector Provider initialization failed")

type runtimeProvider interface {
	provider.Provider
	provider.QueryProvider
}

type providerFactory func(context.Context) (runtimeProvider, error)

// lazyProvider keeps the Control WSS lifecycle independent from OpenStack
// authentication. It initializes the customer-local Provider on demand and
// retries initialization on a later request after a failure.
type lazyProvider struct {
	mu                   sync.Mutex
	providerConnectionID string
	factory              providerFactory
	initializedProvider  runtimeProvider
}

var _ runtimeProvider = (*lazyProvider)(nil)

func newLazyProvider(providerConnectionID string, factory providerFactory) *lazyProvider {
	return &lazyProvider{
		providerConnectionID: providerConnectionID,
		factory:              factory,
	}
}

func (p *lazyProvider) ProviderConnectionID() string {
	if p == nil {
		return ""
	}
	return p.providerConnectionID
}

func (p *lazyProvider) get(ctx context.Context) (runtimeProvider, error) {
	if p == nil || p.factory == nil || p.providerConnectionID == "" {
		return nil, errProviderInitialization
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.initializedProvider != nil {
		return p.initializedProvider, nil
	}
	initialized, err := p.factory(ctx)
	if err != nil || initialized == nil || initialized.ProviderConnectionID() != p.providerConnectionID {
		return nil, errProviderInitialization
	}
	p.initializedProvider = initialized
	return initialized, nil
}

func (p *lazyProvider) ValidateConnection(ctx context.Context) error {
	initialized, err := p.get(ctx)
	if err != nil {
		return err
	}
	return initialized.ValidateConnection(ctx)
}

func (p *lazyProvider) ListImages(ctx context.Context) ([]provider.Image, error) {
	initialized, err := p.get(ctx)
	if err != nil {
		return nil, err
	}
	return initialized.ListImages(ctx)
}

func (p *lazyProvider) ListFlavors(ctx context.Context) ([]provider.Flavor, error) {
	initialized, err := p.get(ctx)
	if err != nil {
		return nil, err
	}
	return initialized.ListFlavors(ctx)
}

func (p *lazyProvider) Provision(ctx context.Context, request provider.ProvisionRequest) (provider.OperationResult, error) {
	initialized, err := p.get(ctx)
	if err != nil {
		return providerUnavailableOperationResult(), nil
	}
	return initialized.Provision(ctx, request)
}

func (p *lazyProvider) Reset(ctx context.Context, request provider.ResetRequest) (provider.OperationResult, error) {
	initialized, err := p.get(ctx)
	if err != nil {
		return providerUnavailableOperationResult(), nil
	}
	return initialized.Reset(ctx, request)
}

func (p *lazyProvider) Cleanup(ctx context.Context, request provider.CleanupRequest) (provider.OperationResult, error) {
	initialized, err := p.get(ctx)
	if err != nil {
		return providerUnavailableOperationResult(), nil
	}
	return initialized.Cleanup(ctx, request)
}

func (p *lazyProvider) Reconcile(ctx context.Context, request provider.ReconcileRequest) (provider.ReconcileResult, error) {
	initialized, err := p.get(ctx)
	if err != nil {
		return provider.ReconcileResult{
			Observations: []provider.ResourceObservation{},
			Error: &provider.SafeError{
				Code:    "ERR_INFRA_OPENSTACK",
				Message: "OpenStack Provider resources could not be verified",
			},
		}, nil
	}
	return initialized.Reconcile(ctx, request)
}

func providerUnavailableOperationResult() provider.OperationResult {
	return provider.OperationResult{
		Outcome:           provider.OutcomeFailed,
		ProviderResources: []provider.ResourceResult{},
		Error: &provider.SafeError{
			Code:    "ERR_INFRA_OPENSTACK",
			Message: "OpenStack Provider connection is unavailable",
		},
	}
}
