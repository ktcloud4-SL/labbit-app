package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	openstackprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider/openstack"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/workspacefile"
	"golang.org/x/crypto/ssh"
)

var errProviderInitialization = errors.New("connector Provider initialization failed")

// Keep only a fixed diagnostic category, never the original SDK error.
type providerInitializationError struct{ category string }

func (e *providerInitializationError) Error() string { return errProviderInitialization.Error() }
func (e *providerInitializationError) Unwrap() error { return errProviderInitialization }

func safeProviderInitializationError(err error) error {
	category := "internal_init"
	switch {
	case errors.Is(err, openstackprovider.ErrConfigRequired), errors.Is(err, openstackprovider.ErrConfigUnavailable),
		errors.Is(err, openstackprovider.ErrConfigInvalid), errors.Is(err, openstackprovider.ErrInsecureTLS):
		category = "config"
	case errors.Is(err, openstackprovider.ErrAuthentication):
		category = "auth"
	case errors.Is(err, openstackprovider.ErrServiceUnavailable):
		category = "catalog"
	}
	return &providerInitializationError{category: category}
}

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
	diagnosticLogger     *slog.Logger
}

type providerInitialization struct {
	done     chan struct{}
	provider runtimeProvider
	err      error
}

var _ runtimeProvider = (*lazyProvider)(nil)
var _ provider.ServerAddressResolver = (*lazyProvider)(nil)
var _ workspacefile.WorkspaceValidator = (*lazyProvider)(nil)

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
	if p == nil || p.factory == nil {
		return nil, safeProviderInitializationError(nil)
	}
	if p.providerConnectionID == "" {
		return nil, safeProviderInitializationError(openstackprovider.ErrConfigRequired)
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
			initialized, err = nil, safeProviderInitializationError(err)
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

func (p *lazyProvider) setDiagnosticLogger(logger *slog.Logger) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.diagnosticLogger = logger
}

func (p *lazyProvider) logInitializationFailure(ctx context.Context, correlation provider.Correlation, err error) {
	var failure *providerInitializationError
	if p == nil || !errors.As(err, &failure) {
		return
	}
	p.mu.Lock()
	logger := p.diagnosticLogger
	p.mu.Unlock()
	if logger == nil {
		return
	}
	attrs := []any{"failure_stage", "provider_initialization", "failure_category", failure.category}
	// Correlation fields are opaque wire strings; log only validated UUIDs so
	// malformed inputs cannot turn a credential or endpoint into a log value.
	for _, field := range []struct{ name, value string }{
		{"operation_id", correlation.OperationID}, {"lab_instance_id", correlation.LabInstanceID},
		{"provider_connection_id", p.providerConnectionID},
	} {
		if id, err := uuid.Parse(field.value); err == nil && id != uuid.Nil {
			attrs = append(attrs, field.name, id.String())
		}
	}
	if correlation.Generation > 0 {
		attrs = append(attrs, "generation", correlation.Generation)
	}
	logger.WarnContext(ctx, "Connector Provider initialization failed", attrs...)
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

func (p *lazyProvider) ValidateWorkspace(ctx context.Context, target vmaccess.Target) error {
	initialized, err := p.get(ctx)
	if err != nil {
		return err
	}
	validator, ok := initialized.(workspacefile.WorkspaceValidator)
	if !ok {
		return errProviderInitialization
	}
	return validator.ValidateWorkspace(ctx, target)
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
		p.logInitializationFailure(ctx, request.Correlation, err)
		return providerUnavailableOperationResult(), nil
	}
	return initialized.Provision(ctx, request)
}

func (p *lazyProvider) Reset(ctx context.Context, request provider.ResetRequest) (provider.OperationResult, error) {
	initialized, err := p.get(ctx)
	if err != nil {
		p.logInitializationFailure(ctx, request.Correlation, err)
		return providerUnavailableOperationResult(), nil
	}
	return initialized.Reset(ctx, request)
}

func (p *lazyProvider) Cleanup(ctx context.Context, request provider.CleanupRequest) (provider.OperationResult, error) {
	initialized, err := p.get(ctx)
	if err != nil {
		p.logInitializationFailure(ctx, request.Correlation, err)
		return providerUnavailableOperationResult(), nil
	}
	return initialized.Cleanup(ctx, request)
}

func (p *lazyProvider) Reconcile(ctx context.Context, request provider.ReconcileRequest) (provider.ReconcileResult, error) {
	initialized, err := p.get(ctx)
	if err != nil {
		p.logInitializationFailure(ctx, request.Correlation, err)
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
