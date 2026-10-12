package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"github.com/gophercloud/gophercloud/v2"
	"golang.org/x/crypto/ssh"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegisteredPublicKeyEquality(t *testing.T) {
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	expected, _ := ssh.NewSignerFromKey(private)
	_, different, _ := ed25519.GenerateKey(rand.Reader)
	other, _ := ssh.NewSignerFromKey(different)
	for _, tc := range []struct {
		name, key      string
		match, unknown bool
	}{
		{"equalIgnoringComment", strings.TrimSpace(string(ssh.MarshalAuthorizedKey(expected.PublicKey()))) + " imported key comment\n", true, false},
		{"different", string(ssh.MarshalAuthorizedKey(other.PublicKey())), false, false},
		{"missing", "", false, true}, {"malformed", "not-a-key", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			match, err := publicKeyEqual(tc.key, expected.PublicKey())
			if match != tc.match || (err != nil) != tc.unknown {
				t.Fatalf("unexpected match=%t unknown=%t", match, err != nil)
			}
		})
	}
}

func TestServerDetailExactIdentityAndProject(t *testing.T) {
	previousCompute, previousState := compute, state
	t.Cleanup(func() { compute, state = previousCompute, previousState })
	state.Project = "expected-project"
	for _, shape := range []string{"server", "servers"} {
		for _, tc := range []struct {
			name, id, project string
			rejected          bool
		}{
			{"exact", "expected-id", "expected-project", false},
			{"wrongID", "other-id", "expected-project", true},
			{"wrongProject", "expected-id", "other-project", true},
		} {
			t.Run(shape+"/"+tc.name, func(t *testing.T) {
				api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "GET" || r.URL.Path != "/servers/expected-id" {
						t.Errorf("unexpected request method/path")
					}
					json.NewEncoder(w).Encode(map[string]any{shape: map[string]any{"id": tc.id, "tenant_id": tc.project}})
				}))
				defer api.Close()
				compute = &gophercloud.ServiceClient{ProviderClient: &gophercloud.ProviderClient{}, Endpoint: api.URL + "/"}
				result, err := getServer(context.Background(), "expected-id")
				if (err != nil) != tc.rejected || (tc.rejected && result != nil) {
					t.Fatal("exact server boundary not enforced")
				}
			})
		}
	}
}
