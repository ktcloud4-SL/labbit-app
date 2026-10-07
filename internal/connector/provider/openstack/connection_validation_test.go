package openstackprovider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gophercloud/gophercloud/v2"
)

func TestValidateConnectionChecksKeystone(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   error
	}{
		{"valid", http.StatusNoContent, nil},
		{"valid200", http.StatusOK, nil},
		{"revoked", http.StatusNotFound, ErrAuthentication},
		{"unauthorized", http.StatusUnauthorized, ErrAuthentication},
		{"forbidden", http.StatusForbidden, ErrAuthentication},
		{"unavailable", http.StatusServiceUnavailable, ErrConnectionUnavailable},
		{"rateLimited", http.StatusTooManyRequests, ErrConnectionUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodHead || r.URL.Path != "/v3/auth/tokens" {
					t.Errorf("unexpected validation request: %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("X-Auth-Token") != "test-validation-token" || r.Header.Get("X-Subject-Token") != "test-validation-token" {
					t.Error("validation did not authenticate and check the selected token")
				}
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			client := &gophercloud.ProviderClient{IdentityBase: server.URL + "/", HTTPClient: *server.Client()}
			client.UseTokenLock()
			client.SetToken("test-validation-token")
			adapter := &Adapter{provider: client}
			err := adapter.ValidateConnection(context.Background())
			if !errors.Is(err, tc.want) {
				t.Fatalf("validation error = %v, want %v", err, tc.want)
			}
			if calls.Load() != 1 {
				t.Fatalf("Keystone calls = %d, want 1", calls.Load())
			}
			if err != nil && (strings.Contains(err.Error(), "test-validation-token") || strings.Contains(err.Error(), server.URL)) {
				t.Fatal("validation error exposed a token or endpoint")
			}
		})
	}
}

func TestValidateConnectionRechecksRefreshedToken(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "refresh", true: "refreshRejected"}[failed], func(t *testing.T) {
			var calls, reauths atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("X-Auth-Token") == "old-test-token" {
					w.WriteHeader(http.StatusUnauthorized)
				} else if r.Header.Get("X-Subject-Token") == "old-test-token" {
					w.WriteHeader(http.StatusNotFound)
				} else {
					w.WriteHeader(http.StatusNoContent)
				}
			}))
			defer server.Close()
			client := &gophercloud.ProviderClient{IdentityBase: server.URL + "/", HTTPClient: *server.Client()}
			client.UseTokenLock()
			client.SetToken("old-test-token")
			client.ReauthFunc = func(context.Context) error {
				reauths.Add(1)
				if failed {
					return errors.New("test-sensitive-reauth-error")
				}
				client.SetToken("new-test-token")
				return nil
			}
			err := (&Adapter{provider: client}).ValidateConnection(context.Background())
			if failed {
				if !errors.Is(err, ErrAuthentication) || strings.Contains(err.Error(), "test-sensitive") || calls.Load() != 1 {
					t.Fatalf("unsafe failed refresh: error=%v calls=%d", err, calls.Load())
				}
			} else if err != nil || calls.Load() != 3 {
				t.Fatalf("refreshed token was not validated: error=%v calls=%d", err, calls.Load())
			}
			if reauths.Load() != 1 {
				t.Fatalf("reauth count = %d", reauths.Load())
			}
		})
	}
}

type validationTransport func(*http.Request) (*http.Response, error)

func (f validationTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestValidateConnectionBoundsDefaultRequestAndRedactsTransportErrors(t *testing.T) {
	client := &gophercloud.ProviderClient{IdentityBase: "https://identity.example.test/", TokenID: "test-validation-token"}
	client.HTTPClient.Transport = validationTransport(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 15*time.Second || time.Until(deadline) <= 0 {
			t.Error("validation has no bounded default deadline")
		}
		return nil, errors.New("test-sensitive-transport-error")
	})
	err := (&Adapter{provider: client}).ValidateConnection(context.Background())
	if !errors.Is(err, ErrConnectionUnavailable) || err.Error() != ErrConnectionUnavailable.Error() {
		t.Fatalf("unsafe transport error = %v", err)
	}
}

func TestValidateConnectionConcurrentRefresh(t *testing.T) {
	var calls, reauths atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("X-Auth-Token") == "old-test-token" {
			w.WriteHeader(http.StatusUnauthorized)
		} else if r.Header.Get("X-Subject-Token") == "old-test-token" {
			w.WriteHeader(http.StatusNotFound)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	client := &gophercloud.ProviderClient{IdentityBase: server.URL + "/", HTTPClient: *server.Client()}
	client.UseTokenLock()
	client.SetToken("old-test-token")
	client.ReauthFunc = func(context.Context) error { reauths.Add(1); client.SetToken("new-test-token"); return nil }
	adapter := &Adapter{provider: client}
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			if err := adapter.ValidateConnection(context.Background()); err != nil {
				t.Errorf("concurrent validation failed: %v", err)
			}
		})
	}
	wg.Wait()
	if reauths.Load() != 1 || calls.Load() > 36 {
		t.Fatalf("unbounded refresh: reauths=%d calls=%d", reauths.Load(), calls.Load())
	}
}

func TestValidateConnectionRejectsMissingIdentityOrToken(t *testing.T) {
	for _, client := range []*gophercloud.ProviderClient{
		nil,
		{TokenID: "test-token"},
		{IdentityBase: "https://identity.example.test/"},
		{IdentityBase: "https://identity.example.test/", TokenID: "   "},
	} {
		if err := (&Adapter{provider: client}).ValidateConnection(context.Background()); !errors.Is(err, ErrClientUnavailable) {
			t.Fatalf("uninitialized client error = %v", err)
		}
	}
}

func TestValidateConnectionHonorsDeadlineAndCancellation(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "deadline"}[deadline], func(t *testing.T) {
			started := make(chan struct{})
			closed := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(started)
				<-r.Context().Done()
				close(closed)
			}))
			defer server.Close()
			client := &gophercloud.ProviderClient{IdentityBase: server.URL + "/", HTTPClient: *server.Client(), TokenID: "test-validation-token"}
			ctx, cancel := context.WithCancel(context.Background())
			want := context.Canceled
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
				want = context.DeadlineExceeded
			}
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- (&Adapter{provider: client}).ValidateConnection(ctx) }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("validation did not contact Keystone")
			}
			if !deadline {
				cancel()
			}
			select {
			case err := <-result:
				if !errors.Is(err, want) {
					t.Fatalf("error = %v, want %v", err, want)
				}
			case <-time.After(time.Second):
				t.Fatal("validation did not stop after context completion")
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("validation left the HTTP request open")
			}
		})
	}
}
