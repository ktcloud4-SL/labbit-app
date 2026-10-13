package openstackprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gophercloud/gophercloud/v2"
)

type ktCloudClientPeer struct {
	server       *httptest.Server
	authCalls    atomic.Int32
	expireServer atomic.Bool
	expireHead   atomic.Bool
	authFailure  atomic.Int32
	mutations    atomic.Int32
	mutationCode atomic.Int32
}

func newKTCloudClientPeer(t *testing.T) *ktCloudClientPeer {
	t.Helper()
	p := &ktCloudClientPeer{}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/d1/identity/auth/tokens" && (r.Method == http.MethodPost || r.Method == http.MethodGet):
			if code := p.authFailure.Load(); code != 0 {
				w.WriteHeader(int(code))
				fmt.Fprint(w, `{"error":"sensitive-provider-payload"}`)
				return
			}
			if r.Method == http.MethodPost {
				var body struct {
					Auth struct {
						Scope struct {
							Project struct {
								ID string `json:"id"`
							} `json:"project"`
						} `json:"scope"`
					} `json:"auth"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil || body.Auth.Scope.Project.ID != "fixture-project" {
					t.Error("expected a scoped SDK Keystone v3 authentication request")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				p.authCalls.Add(1)
			}
			w.Header().Set("X-Subject-Token", fmt.Sprintf("fixture-token-%d", p.authCalls.Load()))
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusCreated)
			}
			fmt.Fprintf(w, `{"token":{"catalog":[
				{"type":"compute","endpoints":[{"interface":"public","url":%q}]},
				{"type":"image","endpoints":[{"interface":"public","url":%q}]},
				{"type":"network","endpoints":[{"interface":"public","url":%q}]}]}}`, p.server.URL+"/d1/nova/v2.1", p.server.URL+"/d1/glance", p.server.URL+"/d1/neutron")
		case r.URL.Path == "/d1/identity/auth/tokens" && r.Method == http.MethodHead:
			if p.expireHead.Swap(false) || r.Header.Get("X-Subject-Token") != fmt.Sprintf("fixture-token-%d", p.authCalls.Load()) {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/d1/server/servers/detail" && r.Method == http.MethodGet:
			if p.expireServer.Swap(false) {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			fmt.Fprint(w, `{"servers":[{"id":"fixture-server","name":"workspace","status":"ACTIVE"}]}`)
		case r.URL.Path == "/d1/server/flavors/detail" && r.Method == http.MethodGet:
			fmt.Fprint(w, `{"flavors":[{"id":"fixture-flavor","name":"small","vcpus":1,"ram":2048,"disk":50}]}`)
		case r.URL.Path == "/d1/image/images" && r.Method == http.MethodGet:
			fmt.Fprint(w, `{"images":[{"id":"fixture-image","name":"linux","status":"active"}]}`)
		case r.URL.Path == "/d1/server/servers" && r.Method == http.MethodPost:
			attempt := p.mutations.Add(1)
			if attempt == 1 {
				w.WriteHeader(int(p.mutationCode.Load()))
				return
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"server":{"id":"fixture-created-server"}}`)
		default:
			t.Errorf("unexpected endpoint request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(p.server.Close)
	return p
}

func (p *ktCloudClientPeer) auth() gophercloud.AuthOptions {
	return gophercloud.AuthOptions{IdentityEndpoint: p.server.URL + "/d1/identity/", Username: "fixture-user", Password: "fixture-password", DomainID: "default", TenantID: "fixture-project", AllowReauth: true}
}

func TestKTCloudD1EndpointSelectionIsExact(t *testing.T) {
	for _, endpoint := range []string{ktCloudD1Identity, ktCloudD1Identity + "/"} {
		if !isKTCloudD1Identity(endpoint) {
			t.Errorf("expected D1 Gateway selection for %s", endpoint)
		}
	}
	for _, endpoint := range []string{"http://api.ucloudbiz.olleh.com/d1/identity/", "https://identity.example.test/v3", ktCloudD1Identity + "/v3", ktCloudD1Identity + "?other=true", "https://api.ucloudbiz.olleh.com.attacker.test/d1/identity/"} {
		if isKTCloudD1Identity(endpoint) {
			t.Errorf("unexpected Gateway selection for %s", endpoint)
		}
	}
}

func TestKTCloudD1SDKQueriesAndValidationUseGatewayPaths(t *testing.T) {
	p := newKTCloudClientPeer(t)
	a, err := newKTCloudD1Adapter(context.Background(), Config{}, p.auth(), gophercloud.EndpointOpts{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if items, err := a.ListServers(context.Background()); err != nil || len(items) != 1 || items[0].Status != "ACTIVE" {
		t.Fatalf("server query failed: %v", err)
	}
	if items, err := a.ListFlavors(context.Background()); err != nil || len(items) != 1 || items[0].RAMMiB != 2048 {
		t.Fatalf("flavor query failed: %v", err)
	}
	if items, err := a.ListImages(context.Background()); err != nil || len(items) != 1 || items[0].Status != "ACTIVE" {
		t.Fatalf("image query failed: %v", err)
	}
	if err := a.ValidateConnection(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.network.ResourceBase != p.server.URL+"/d1/neutron/v2.0/" {
		t.Fatal("Neutron must retain its own catalog identity, separate from Tier")
	}
}

func TestKTCloudD1ReauthRefreshesQueryAndValidationSubject(t *testing.T) {
	p := newKTCloudClientPeer(t)
	a, err := newKTCloudD1Adapter(context.Background(), Config{}, p.auth(), gophercloud.EndpointOpts{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p.expireServer.Store(true)
	if _, err := a.ListServers(context.Background()); err != nil {
		t.Fatal(err)
	}
	p.expireHead.Store(true)
	if err := a.ValidateConnection(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.authCalls.Load() != 3 {
		t.Fatalf("auth calls = %d, want initial + two distinct refreshes", p.authCalls.Load())
	}
}

func TestKTCloudD1MutationRetriesOnlyDefinitive401(t *testing.T) {
	for _, status := range []int{401, 403, 408, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			p := newKTCloudClientPeer(t)
			a, err := newKTCloudD1Adapter(context.Background(), Config{}, p.auth(), gophercloud.EndpointOpts{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			p.mutationCode.Store(int32(status))
			_, requestErr := a.compute.Post(context.Background(), a.compute.ServiceURL("servers"), map[string]any{"server": map[string]string{"name": "fixture"}}, nil, nil)
			if status == 401 {
				if requestErr != nil || p.mutations.Load() != 2 || p.authCalls.Load() != 2 {
					t.Fatal("401 should refresh and retry once")
				}
			} else if requestErr == nil || p.mutations.Load() != 1 || p.authCalls.Load() != 1 {
				t.Fatal("uncertain/rejected mutation must not refresh or retry")
			}
		})
	}
}

func TestKTCloudD1AuthErrorsAreSafeAndTokenOnlyDoesNotRenew(t *testing.T) {
	p := newKTCloudClientPeer(t)
	p.authFailure.Store(403)
	_, err := newKTCloudD1Adapter(context.Background(), Config{}, p.auth(), gophercloud.EndpointOpts{}, nil)
	if !errors.Is(err, ErrAuthentication) || strings.Contains(err.Error(), "sensitive-provider-payload") {
		t.Fatal("authentication failure should expose only the safe sentinel")
	}
	p.authFailure.Store(0)
	auth := p.auth()
	auth.Username, auth.Password, auth.TenantID, auth.TokenID, auth.AllowReauth = "", "", "", "fixture-token-0", false
	a, err := newKTCloudD1Adapter(context.Background(), Config{}, auth, gophercloud.EndpointOpts{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.provider.ReauthFunc != nil || p.authCalls.Load() != 0 {
		t.Fatal("token-only auth must use validation without renewable credentials")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := newKTCloudD1Adapter(ctx, Config{}, p.auth(), gophercloud.EndpointOpts{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
