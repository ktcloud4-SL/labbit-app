package openstackprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Exercise the public constructor, not an injected SDK ReauthFunc. All tokens
// and credentials belong to this local fake; no OpenStack environment is used.
type reauthPeer struct {
	url                            string
	authCalls, posts               atomic.Int32
	expired, rejectAuth, blockAuth atomic.Bool
	mutationStatus                 atomic.Int32
}

func newReauthPeer(t *testing.T, authKind string) (*reauthPeer, Config) {
	t.Helper()
	p := &reauthPeer{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v3/auth/tokens" && r.Method == http.MethodPost:
			issue := p.authCalls.Add(1)
			if p.blockAuth.Load() {
				<-r.Context().Done()
				return
			}
			if p.rejectAuth.Load() {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("X-Subject-Token", fmt.Sprintf("fake-token-%d", issue))
			w.WriteHeader(http.StatusCreated)
			p.catalog(w)
		case r.URL.Path == "/v3/auth/tokens" && r.Method == http.MethodGet:
			w.Header().Set("X-Subject-Token", "fake-token-1")
			p.catalog(w)
		case r.URL.Path == "/v3/auth/tokens" && r.Method == http.MethodHead:
			if p.expired.Load() && r.Header.Get("X-Subject-Token") == "fake-token-1" {
				w.WriteHeader(http.StatusUnauthorized)
			} else {
				w.WriteHeader(http.StatusNoContent)
			}
		case r.URL.Path == "/image/" || r.URL.Path == "/image":
			_ = json.NewEncoder(w).Encode(map[string]any{"versions": []map[string]any{{"id": "v2.15", "status": "CURRENT", "links": []map[string]string{{"rel": "self", "href": p.url + "/image/v2/"}}}}})
		case r.URL.Path == "/network/" || r.URL.Path == "/network":
			_ = json.NewEncoder(w).Encode(map[string]any{"versions": []map[string]any{{"id": "v2.0", "status": "CURRENT", "links": []map[string]string{{"rel": "self", "href": p.url + "/network/v2.0/"}}}}})
		case r.URL.Path == "/v3/" || r.URL.Path == "/v3":
			_ = json.NewEncoder(w).Encode(map[string]any{"version": map[string]any{"id": "v3.14", "status": "stable", "links": []map[string]string{{"rel": "self", "href": p.url + "/v3/"}}}})
		case r.URL.Path == "/image/v2/images":
			if p.expired.Load() && r.Header.Get("X-Auth-Token") == "fake-token-1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"images":[{"id":"image-1","name":"ubuntu","status":"active"}],"images_links":[]}`))
		case r.URL.Path == "/network/v2.0/networks" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"networks":[]}`))
		case r.URL.Path == "/network/v2.0/networks" && r.Method == http.MethodPost:
			p.posts.Add(1)
			if p.expired.Load() && r.Header.Get("X-Auth-Token") == "fake-token-1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if status := p.mutationStatus.Load(); status != 0 {
				if status == -1 {
					<-r.Context().Done()
					return
				}
				w.WriteHeader(int(status))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"network":{"id":"network-1","name":"network","status":"ACTIVE"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	p.url = server.URL
	t.Cleanup(server.Close)
	var auth string
	switch authKind {
	case "password":
		auth = "      username: connector\n      password: fake-password\n      project_name: labbit\n      user_domain_name: Default\n      project_domain_name: Default\n"
	case "application":
		auth = "      application_credential_id: fake-app\n      application_credential_secret: fake-secret\n"
	case "token":
		auth = "      token: fake-token-1\n"
	default:
		t.Fatal("unsupported fake auth kind")
	}
	path := filepath.Join(t.TempDir(), "clouds.yaml")
	data := fmt.Sprintf("clouds:\n  reauth-test:\n    auth:\n      auth_url: %s/v3\n%s    region_name: RegionOne\n    interface: public\n", p.url, auth)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return p, Config{File: path, CloudName: "reauth-test"}
}

func (p *reauthPeer) catalog(w http.ResponseWriter) {
	catalog := []map[string]any{}
	for _, service := range []struct{ kind, path string }{{"image", "/image/"}, {"compute", "/compute/v2/project/"}, {"network", "/network/"}} {
		catalog = append(catalog, map[string]any{"id": service.kind, "name": service.kind, "type": service.kind, "endpoints": []map[string]string{{"id": service.kind, "interface": "public", "region": "RegionOne", "url": p.url + service.path}}})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"token": map[string]any{"methods": []string{"password"}, "expires_at": "2099-01-01T00:00:00Z", "issued_at": "2026-10-06T00:00:00Z", "catalog": catalog, "user": map[string]string{"id": "fake-user"}, "project": map[string]string{"id": "project"}}})
}

func TestPublicNewReauthRecoversExpiredToken(t *testing.T) {
	for _, kind := range []string{"password", "application"} {
		t.Run(kind, func(t *testing.T) {
			p, cfg := newReauthPeer(t, kind)
			a, err := New(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.ListImages(context.Background()); err != nil {
				t.Fatal(err)
			}
			p.expired.Store(true)
			var wg sync.WaitGroup
			for i := 0; i < 16; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					items, err := a.ListImages(context.Background())
					if err != nil || len(items) != 1 {
						t.Errorf("query did not recover: count=%d error=%v", len(items), err)
					}
				}()
			}
			wg.Wait()
			if p.authCalls.Load() != 2 {
				t.Fatalf("token issues=%d, want initial + one concurrent refresh", p.authCalls.Load())
			}
			if err := a.ValidateConnection(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPublicNewReauthFailedRefreshCanRecoverOnLaterRequest(t *testing.T) {
	p, cfg := newReauthPeer(t, "password")
	a, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	p.expired.Store(true)
	p.rejectAuth.Store(true)
	if _, err := a.ListImages(context.Background()); !errors.Is(err, ErrImageList) || strings.Contains(err.Error(), "fake-password") {
		t.Fatalf("unsafe/unexpected error: %v", err)
	}
	p.rejectAuth.Store(false)
	if items, err := a.ListImages(context.Background()); err != nil || len(items) != 1 {
		t.Fatalf("later query: %d %v", len(items), err)
	}
	if p.authCalls.Load() != 3 {
		t.Fatalf("auth attempts=%d, want 3", p.authCalls.Load())
	}
}

func TestPublicNewReauthTokenOnlyDoesNotRenew(t *testing.T) {
	p, cfg := newReauthPeer(t, "token")
	a, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	p.expired.Store(true)
	if _, err := a.ListImages(context.Background()); !errors.Is(err, ErrImageList) {
		t.Fatalf("expired token error=%v", err)
	}
	if a.provider.ReauthFunc != nil || p.authCalls.Load() != 0 {
		t.Fatal("token-only credential attempted renewal")
	}
}

func TestBoundedReauthenticationAddsDeadlineAndReleasesContext(t *testing.T) {
	// SDK captures a private HTTP client during New. Check this auth-only
	// wrapper separately; the public-constructor tests cover real fake HTTP.
	var captured context.Context
	bound := boundedReauthentication(func(ctx context.Context) error {
		captured = ctx
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > authenticationTimeout {
			t.Fatal("missing/default authentication deadline")
		}
		return ErrAuthentication
	})
	if err := bound(context.Background()); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("callback error lost: %v", err)
	}
	if !errors.Is(captured.Err(), context.Canceled) {
		t.Fatal("auth child context not released")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	short := boundedReauthentication(func(authCtx context.Context) error {
		got, _ := authCtx.Deadline()
		if !got.Equal(deadline) {
			t.Fatal("caller deadline extended")
		}
		return nil
	})
	if err := short(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestLoadConfigMixedTokenAndPasswordDoesNotEnableReauth(t *testing.T) {
	_, cfg := newReauthPeer(t, "password")
	data, err := os.ReadFile(cfg.File)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "      username:", "      token: fake-token-1\n      username:", 1))
	if err := os.WriteFile(cfg.File, data, 0o600); err != nil {
		t.Fatal(err)
	}
	auth, _, _, err := loadConfig(cfg)
	if err != nil || auth.AllowReauth {
		t.Fatalf("token takes precedence over renewable password: error=%v reauth=%v", err, auth.AllowReauth)
	}
}

func TestPublicNewReauthHonorsCancellation(t *testing.T) {
	for _, initial := range []bool{true, false} {
		t.Run(fmt.Sprint(initial), func(t *testing.T) {
			p, cfg := newReauthPeer(t, "password")
			var a *Adapter
			if !initial {
				var err error
				a, err = New(context.Background(), cfg)
				if err != nil {
					t.Fatal(err)
				}
				p.expired.Store(true)
			}
			p.blockAuth.Store(true)
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			started := time.Now()
			if initial {
				_, err := New(ctx, cfg)
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("initial auth=%v", err)
				}
			} else {
				_, err := a.ListImages(ctx)
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("refresh=%v", err)
				}
			}
			if time.Since(started) > time.Second {
				t.Fatal("authentication ignored request deadline")
			}
		})
	}
}

func TestPublicNewReauthMutationRetryIsOnlyDefinitive401(t *testing.T) {
	for _, tc := range []struct {
		name            string
		expired, reject bool
		status          int32
		posts, auth     int32
		rejected        bool
	}{
		{"expired_then_success", true, false, 0, 2, 2, false},
		{"refresh_rejected", true, true, 0, 1, 2, true},
		{"second_401", true, false, 401, 2, 2, true},
		{"second_500", true, false, 500, 2, 2, false},
		{"second_timeout", true, false, -1, 2, 2, false},
		{"initial_500", false, false, 500, 1, 1, false},
		{"initial_timeout", false, false, -1, 1, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, cfg := newReauthPeer(t, "password")
			a, err := New(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			p.expired.Store(tc.expired)
			p.rejectAuth.Store(tc.reject)
			p.mutationStatus.Store(tc.status)
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			_, err = a.EnsureNetwork(ctx, ResourceIdentity{Generation: 1, LogicalName: "network"}, NetworkSpec{Name: "network"})
			if tc.status == 0 && !tc.reject {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || errors.Is(err, ErrMutationRejected) != tc.rejected {
				t.Fatalf("mutation error=%v, definite rejection want %v", err, tc.rejected)
			}
			if p.posts.Load() != tc.posts || p.authCalls.Load() != tc.auth {
				t.Fatalf("posts/auth=%d/%d want %d/%d; uncertain mutation must not replay", p.posts.Load(), p.authCalls.Load(), tc.posts, tc.auth)
			}
		})
	}
}
