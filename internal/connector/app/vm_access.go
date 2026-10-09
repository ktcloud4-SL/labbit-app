package app

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
)

func sshIdleTimeoutFromEnvironment() (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv("LABBIT_OPENSTACK_SSH_IDLE_TIMEOUT"))
	if value == "" {
		return time.Minute, nil
	}
	timeout, err := time.ParseDuration(value)
	if err != nil || timeout <= 0 {
		return 0, fmt.Errorf("LABBIT_OPENSTACK_SSH_IDLE_TIMEOUT must be a positive duration")
	}
	return timeout, nil
}

// Query dispatch must continue to see the same ProviderConnection affinity.
type accessLifecycleProvider struct {
	provider.Provider
	manager *vmaccess.Manager
}

func (p *accessLifecycleProvider) ProviderConnectionID() string {
	if query, ok := p.Provider.(provider.QueryProvider); ok {
		return query.ProviderConnectionID()
	}
	return ""
}
func (p *accessLifecycleProvider) ValidateConnection(ctx context.Context) error {
	query, ok := p.Provider.(provider.QueryProvider)
	if !ok {
		return errProviderInitialization
	}
	return query.ValidateConnection(ctx)
}
func (p *accessLifecycleProvider) ListImages(ctx context.Context) ([]provider.Image, error) {
	query, ok := p.Provider.(provider.QueryProvider)
	if !ok {
		return nil, errProviderInitialization
	}
	return query.ListImages(ctx)
}
func (p *accessLifecycleProvider) ListFlavors(ctx context.Context) ([]provider.Flavor, error) {
	query, ok := p.Provider.(provider.QueryProvider)
	if !ok {
		return nil, errProviderInitialization
	}
	return query.ListFlavors(ctx)
}
func (p *accessLifecycleProvider) Reset(ctx context.Context, req provider.ResetRequest) (provider.OperationResult, error) {
	result, err := p.Provider.Reset(ctx, req)
	// A rejected preflight leaves the old generation usable. Success or an
	// uncertain reset must never reuse its old transport or accept late opens.
	if err != nil || result.Outcome == provider.OutcomeSucceeded || result.Outcome == provider.OutcomeUnknown {
		p.manager.InvalidateBefore(req.LabInstanceID, req.Generation)
	}
	return result, err
}
func (p *accessLifecycleProvider) Cleanup(ctx context.Context, req provider.CleanupRequest) (provider.OperationResult, error) {
	result, err := p.Provider.Cleanup(ctx, req)
	if err != nil || result.Outcome == provider.OutcomeSucceeded || result.Outcome == provider.OutcomeUnknown {
		generation := req.Generation
		for _, resource := range req.ProviderResources {
			if resource.Generation > generation {
				generation = resource.Generation
			}
		}
		p.manager.InvalidateBefore(req.LabInstanceID, generation+1)
	}
	return result, err
}
