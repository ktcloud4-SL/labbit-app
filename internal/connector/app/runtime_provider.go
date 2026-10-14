package app

import (
	"context"
	"errors"
	"sync"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"golang.org/x/crypto/ssh"
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
	initializing         *providerInitialization
}

type providerInitialization struct {
	done     chan struct{}
	provider runtimeProvider
	err      error
}

var _ runtimeProvider = (*lazyProvider)(nil)
var _ provider.ServerAddressResolver = (*lazyProvider)(nil)

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
	if err := ctx.Err(); err != nil {
		p.mu.Unlock()
		return nil, err
	}
	if p.initializedProvider != nil {
		initialized := p.initializedProvider
		p.mu.Unlock()
		return initialized, nil
	}
	attempt := p.initializing
	if attempt == nil {
		attempt = &providerInitialization{done: make(chan struct{})}
		p.initializing = attempt
		p.mu.Unlock()

		// The caller starting initialization owns its context. Waiting callers
		// can leave independently without canceling this shared attempt.
		initialized, err := p.factory(ctx)
		if err != nil || ctx.Err() != nil || initialized == nil || initialized.ProviderConnectionID() != p.providerConnectionID {
			initialized, err = nil, errProviderInitialization
		}
		p.mu.Lock()
		p.initializedProvider = initialized
		attempt.provider, attempt.err = initialized, err
		p.initializing = nil
		close(attempt.done)
		p.mu.Unlock()
	} else {
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-attempt.done:
		}
	}
	// Completion and cancellation can arrive together. Never pass a canceled
	// waiter to the initialized Provider, nor leak the owner's context error.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return attempt.provider, attempt.err
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

func (p *lazyProvider) ResolveServerAddress(ctx context.Context, targetVmKey, serverID string) (string, error) {
	initialized, err := p.get(ctx)
	if err != nil {
		return "", err
	}
	resolver, ok := initialized.(provider.ServerAddressResolver)
	if !ok {
		return "", errProviderInitialization
	}
	return resolver.ResolveServerAddress(ctx, targetVmKey, serverID)
}

func (p *lazyProvider) ResolveVMAccess(ctx context.Context, target vmaccess.Target) (vmaccess.Endpoint, error) {
	initialized, err := p.get(ctx)
	if err != nil {
		return vmaccess.Endpoint{}, err
	}
	resolver, ok := initialized.(vmaccess.Resolver)
	if !ok {
		return vmaccess.Endpoint{}, errProviderInitialization
	}
	return resolver.ResolveVMAccess(ctx, target)
}

func (p *lazyProvider) SSHHostKeyCallback(ctx context.Context, serverID string) (ssh.HostKeyCallback, error) {
	initialized, err := p.get(ctx)
	if err != nil {
		return nil, err
	}
	verifier, ok := initialized.(serverHostKeyProvider)
	if !ok {
		return nil, errProviderInitialization
	}
	return verifier.SSHHostKeyCallback(ctx, serverID)
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
