package app

import (
	"context"
	"errors"
	"testing"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

func TestLazyProvider_RetriesInitializationAfterFailure(t *testing.T) {
	attempts := 0
	initialized := &provider.MockProvider{
		ConnectionID: "provider-connection-1",
		ValidateConnectionFunc: func(context.Context) error {
			return nil
		},
	}
	lazy := newLazyProvider("provider-connection-1", func(context.Context) (runtimeProvider, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("authentication failed with sensitive provider detail")
		}
		return initialized, nil
	})

	if err := lazy.ValidateConnection(context.Background()); !errors.Is(err, errProviderInitialization) {
		t.Fatalf("first ValidateConnection() error = %v", err)
	}
	if err := lazy.ValidateConnection(context.Background()); err != nil {
		t.Fatalf("second ValidateConnection() error = %v", err)
	}
	if err := lazy.ValidateConnection(context.Background()); err != nil {
		t.Fatalf("cached ValidateConnection() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("factory attempts = %d, want 2", attempts)
	}
}

func TestLazyProvider_AuthenticationFailureIsDefiniteBeforeMutation(t *testing.T) {
	lazy := newLazyProvider("provider-connection-1", func(context.Context) (runtimeProvider, error) {
		return nil, errors.New("authentication failed")
	})
	result, err := lazy.Provision(context.Background(), provider.ProvisionRequest{})
	if err != nil {
		t.Fatalf("Provision() error = %v", err)
	}
	if result.Outcome != provider.OutcomeFailed || result.Error == nil || result.Error.Code != "ERR_INFRA_OPENSTACK" || len(result.ProviderResources) != 0 {
		t.Fatalf("Provision() result = %+v", result)
	}
}
