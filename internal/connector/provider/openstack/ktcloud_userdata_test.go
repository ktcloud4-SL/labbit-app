package openstackprovider

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"testing"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

func TestKTCloudRejectedCreateBodyRemainsUnknownWithoutRetry(t *testing.T) {
	for _, body := range []string{"<html>sensitive provider rejection</html>", `{"server":`} {
		t.Run(fmt.Sprintf("length-%d", len(body)), func(t *testing.T) {
			posts := 0
			a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					writeJSON(t, w, 200, map[string]any{"servers": []any{}})
					return
				}
				posts++
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(200)
				_, _ = io.WriteString(w, body)
			})
			resource, server, err := a.EnsureServer(context.Background(), resourceIdentity(1, "workspace"), ServerSpec{Name: "fixture", ImageID: "image", FlavorID: "flavor", KeyPair: "key", NetworkIDs: []string{"management-ref", "lab-ref"}, UserData: []byte("#!/bin/sh\ntrue\n")})
			result := mutationFailure(nil, err, "safe create outcome")
			if err == nil || posts != 1 || resource.ProviderID != "" || server.ID != "" || result.Outcome != coreprovider.OutcomeUnknown || strings.Contains(err.Error(), "sensitive provider") {
				t.Fatal("uncertain response caused retry, ownership claim, unsafe error, or false success")
			}
		})
	}
}

func TestKTCloudCompressedUserDataRejectsFinalTransportOverflow(t *testing.T) {
	content := make([]byte, 1536)
	_, _ = rand.New(rand.NewSource(1)).Read(content)
	if base64.StdEncoding.EncodedLen(len(content)) != 2048 {
		t.Fatal("fixture must fit the existing uncompressed limit")
	}
	if _, err := ktCloudCompressedUserData(content); err == nil {
		t.Fatal("gzip overhead must not exceed the final user_data limit")
	}
	calls := 0
	a := ktTopologyAdapter(t, func(http.ResponseWriter, *http.Request) { calls++ })
	snapshot := validSnapshot()
	snapshot.ProviderConnectionID = a.provision.ProviderConnectionID
	snapshot.InternetOutbound = false
	snapshot.StartupScript.Content = string(content)
	snapshot.StartupScript.SHA256 = fmt.Sprintf("%x", sha256.Sum256(content))
	request := coreprovider.ProvisionRequest{Correlation: coreprovider.Correlation{OperationID: "overflow-operation", LabInstanceID: "overflow-instance", Generation: 1}, CreationSnapshot: snapshot}
	if err := validateProvisionRequest(request); err != nil {
		t.Fatalf("overflow fixture must be an otherwise valid request: %v", err)
	}
	control := snapshot
	control.StartupScript = nil
	if err := a.validateProvisionSettings(a.provision, control); err != nil {
		t.Fatalf("settings unrelated to user_data must be valid: %v", err)
	}
	if err := a.validateProvisionSettings(a.provision, snapshot); !errors.Is(err, ErrProvisionConfig) {
		t.Fatalf("final transport overflow must fail config preflight: %v", err)
	}
	result, err := a.Provision(context.Background(), request)
	if err != nil || result.Outcome != coreprovider.OutcomeFailed || calls != 0 || len(result.ProviderResources) != 0 {
		t.Fatal("oversized compressed transport reached an external request")
	}
}

func TestKTCloudServerPreservesStartupScriptThroughCompressedUserData(t *testing.T) {
	for _, script := range []string{"", "#!/bin/sh\ntrue\n", "#!/bin/sh\nprintf '%s' '<form method=\"post\">한글</form>'\n"} {
		t.Run(script, func(t *testing.T) {
			original := []byte(script)
			before := bytes.Clone(original)
			posts := 0
			a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "GET" && r.URL.Path == "/compute/v2/servers/detail":
					writeJSON(t, w, 200, map[string]any{"servers": []any{}})
				case r.Method == "POST" && r.URL.Path == "/compute/v2/servers":
					posts++
					var body struct {
						Server map[string]any `json:"server"`
					}
					if json.NewDecoder(r.Body).Decode(&body) != nil {
						t.Fatal("invalid request JSON")
					}
					value, present := body.Server["user_data"]
					if script == "" {
						if present {
							t.Fatal("empty startup must omit user_data")
						}
					} else {
						encoded, ok := value.(string)
						if !ok {
							t.Fatal("user_data must be base64 text")
						}
						compressed, err := base64.StdEncoding.DecodeString(encoded)
						if err != nil {
							t.Fatal("invalid base64")
						}
						reader, err := gzip.NewReader(bytes.NewReader(compressed))
						if err != nil {
							t.Fatal("KT user_data must use cloud-init gzip transport")
						}
						decoded, err := io.ReadAll(reader)
						closeErr := reader.Close()
						if err != nil || closeErr != nil || !bytes.Equal(decoded, before) {
							t.Fatal("immutable startup content changed")
						}
					}
					writeJSON(t, w, 202, map[string]any{"server": map[string]any{"id": "created-server"}})
				default:
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
			})
			_, _, err := a.EnsureServer(context.Background(), resourceIdentity(1, "workspace"), ServerSpec{Name: "fixture", ImageID: "image", FlavorID: "flavor", KeyPair: "key", NetworkIDs: []string{"management-ref", "lab-ref"}, UserData: original})
			if err != nil || posts != 1 || !bytes.Equal(original, before) {
				t.Fatalf("create or immutable input failed: posts=%d err=%v", posts, err)
			}
		})
	}
}
