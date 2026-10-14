package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
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

// Observe entry into the context-aware wait without relying on sleeps or on
// lazyProvider's internal locks/state. No cancellation is injected by this hook.
type observedWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *observedWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func awaitProviderSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("Provider initialization/wait did not start")
	}
}

func awaitProviderError(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		t.Fatal("Provider request did not return while initialization was held")
		return nil
	}
}

func providerReleaseGate() (chan struct{}, func()) {
	release := make(chan struct{})
	var once sync.Once
	return release, func() { once.Do(func() { close(release) }) }
}

type lazyAccessTestProvider struct {
	*provider.MockProvider
	resolveCalls atomic.Int32
}

func (p *lazyAccessTestProvider) ResolveVMAccess(context.Context, vmaccess.Target) (vmaccess.Endpoint, error) {
	p.resolveCalls.Add(1)
	return vmaccess.Endpoint{}, vmaccess.ErrInvalidTarget
}

func TestLazyProvider_WaitingVMResolutionHonorsContext(t *testing.T) {
	for _, mode := range []string{"deadline", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			entered := make(chan struct{})
			release, unblock := providerReleaseGate()
			defer unblock()
			var attempts atomic.Int32
			initialized := &lazyAccessTestProvider{MockProvider: &provider.MockProvider{
				ConnectionID: "provider-1", ValidateConnectionFunc: func(context.Context) error { return nil },
			}}
			lazy := newLazyProvider("provider-1", func(ctx context.Context) (runtimeProvider, error) {
				attempts.Add(1)
				close(entered)
				select {
				case <-release:
					return initialized, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			})
			ownerDone := make(chan error, 1)
			go func() { ownerDone <- lazy.ValidateConnection(context.Background()) }()
			awaitProviderSignal(t, entered)
			alive := &observedWaitContext{Context: context.Background(), waiting: make(chan struct{})}
			aliveDone := make(chan error, 1)
			go func() { aliveDone <- lazy.ValidateConnection(alive) }()
			awaitProviderSignal(t, alive.waiting)
			ctx, cancel := context.WithCancel(context.Background())
			if mode == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
			}
			defer cancel()
			waiting := &observedWaitContext{Context: ctx, waiting: make(chan struct{})}
			done := make(chan error, 1)
			go func() {
				_, err := lazy.ResolveVMAccess(waiting, vmaccess.Target{LabInstanceID: "lab-1", Generation: 1, VMKey: "workspace", ServerID: "server-1"})
				done <- err
			}()
			awaitProviderSignal(t, waiting.waiting)
			if mode == "cancel" {
				cancel()
			}
			want := context.Canceled
			if mode == "deadline" {
				want = context.DeadlineExceeded
			}
			if err := awaitProviderError(t, done); !errors.Is(err, want) {
				t.Fatalf("waiting VM resolution = %v, want %v", err, want)
			}
			if initialized.resolveCalls.Load() != 0 {
				t.Fatal("canceled waiter called the VM resolver")
			}
			unblock()
			if err := awaitProviderError(t, ownerDone); err != nil {
				t.Fatalf("waiter canceled initialization owner: %v", err)
			}
			if err := awaitProviderError(t, aliveDone); err != nil {
				t.Fatalf("waiter canceled another waiter: %v", err)
			}
			if err := lazy.ValidateConnection(context.Background()); err != nil || attempts.Load() != 1 {
				t.Fatalf("initialized Provider was not reused: err=%v attempts=%d", err, attempts.Load())
			}
		})
	}
}

func TestLazyProvider_VMSetupTimeoutWhileInitializationIsPending(t *testing.T) {
	entered := make(chan struct{})
	var enteredOnce sync.Once
	release, unblock := providerReleaseGate()
	defer unblock()
	lazy := newLazyProvider("provider-1", func(context.Context) (runtimeProvider, error) {
		enteredOnce.Do(func() { close(entered) })
		<-release
		return nil, errors.New("safe fixture failure")
	})
	ownerDone := make(chan error, 1)
	go func() { ownerDone <- lazy.ValidateConnection(context.Background()) }()
	awaitProviderSignal(t, entered)
	manager := vmaccess.NewManager(lazy, vmaccess.Config{SetupTimeout: 40 * time.Millisecond})
	defer manager.Close()
	done := make(chan error, 1)
	go func() {
		_, err := manager.OpenSession(context.Background(), vmaccess.Target{LabInstanceID: "lab-1", Generation: 1, VMKey: "workspace", ServerID: "server-1"})
		done <- err
	}()
	if err := awaitProviderError(t, done); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("VM setup deadline = %v", err)
	}
	unblock()
	if err := awaitProviderError(t, ownerDone); !errors.Is(err, errProviderInitialization) {
		t.Fatalf("owner failure = %v", err)
	}
}

func TestLazyProvider_SharesFailedAttemptAndRetriesOnNextRequest(t *testing.T) {
	for _, failure := range []string{"error", "nil", "wrong-connection"} {
		t.Run(failure, func(t *testing.T) {
			entered := make(chan struct{})
			release, unblock := providerReleaseGate()
			defer unblock()
			var attempts atomic.Int32
			initialized := &provider.MockProvider{ConnectionID: "provider-1", ValidateConnectionFunc: func(context.Context) error { return nil }}
			lazy := newLazyProvider("provider-1", func(context.Context) (runtimeProvider, error) {
				if attempts.Add(1) != 1 {
					return initialized, nil
				}
				close(entered)
				<-release
				switch failure {
				case "error":
					return nil, errors.New("sensitive authentication detail")
				case "wrong-connection":
					return &provider.MockProvider{ConnectionID: "other-provider"}, nil
				default:
					return nil, nil
				}
			})
			done := make(chan error, 7)
			go func() { done <- lazy.ValidateConnection(context.Background()) }()
			awaitProviderSignal(t, entered)
			for i := 0; i < 6; i++ {
				ctx := &observedWaitContext{Context: context.Background(), waiting: make(chan struct{})}
				go func() { done <- lazy.ValidateConnection(ctx) }()
				awaitProviderSignal(t, ctx.waiting)
			}
			unblock()
			for i := 0; i < 7; i++ {
				if err := awaitProviderError(t, done); err != errProviderInitialization {
					t.Fatalf("failed initialization exposed detail or retried: %v", err)
				}
			}
			if attempts.Load() != 1 {
				t.Fatal("waiters retried the failed initialization")
			}
			for i := 0; i < 2; i++ {
				if err := lazy.ValidateConnection(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			if attempts.Load() != 2 {
				t.Fatal("next request failed to retry/cache initialization")
			}
		})
	}
}

func TestLazyProvider_OwnerCancellationReleasesWaitersAndAllowsRetry(t *testing.T) {
	entered := make(chan struct{})
	var attempts atomic.Int32
	initialized := &provider.MockProvider{ConnectionID: "provider-1", ValidateConnectionFunc: func(context.Context) error { return nil }}
	lazy := newLazyProvider("provider-1", func(ctx context.Context) (runtimeProvider, error) {
		if attempts.Add(1) == 1 {
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return initialized, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ownerDone, waiterDone := make(chan error, 1), make(chan error, 1)
	go func() { ownerDone <- lazy.ValidateConnection(ctx) }()
	awaitProviderSignal(t, entered)
	waiter := &observedWaitContext{Context: context.Background(), waiting: make(chan struct{})}
	go func() { waiterDone <- lazy.ValidateConnection(waiter) }()
	awaitProviderSignal(t, waiter.waiting)
	cancel()
	if err := awaitProviderError(t, ownerDone); !errors.Is(err, context.Canceled) {
		t.Fatalf("owner cancellation = %v", err)
	}
	if err := awaitProviderError(t, waiterDone); err != errProviderInitialization {
		t.Fatalf("waiter inherited owner's cancellation or stayed blocked: %v", err)
	}
	if err := lazy.ValidateConnection(context.Background()); err != nil || attempts.Load() != 2 {
		t.Fatalf("retry after owner cancellation = %v, attempts=%d", err, attempts.Load())
	}
}

func TestLazyProvider_AlreadyCanceledRequestNeverUsesProvider(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(map[bool]string{false: "uninitialized", true: "cached"}[cached], func(t *testing.T) {
			var attempts atomic.Int32
			initialized := &lazyAccessTestProvider{MockProvider: &provider.MockProvider{
				ConnectionID: "provider-1", ValidateConnectionFunc: func(context.Context) error { return nil },
			}}
			lazy := newLazyProvider("provider-1", func(context.Context) (runtimeProvider, error) {
				attempts.Add(1)
				return initialized, nil
			})
			if cached {
				if err := lazy.ValidateConnection(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			before := attempts.Load()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := lazy.ResolveVMAccess(ctx, vmaccess.Target{})
			if !errors.Is(err, context.Canceled) || attempts.Load() != before || initialized.resolveCalls.Load() != 0 {
				t.Fatalf("canceled request reached Provider: err=%v attempts=%d resolves=%d", err, attempts.Load(), initialized.resolveCalls.Load())
			}
		})
	}
}
