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
	"mime"
	"mime/multipart"
	"net/http"
	"net/mail"
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

func TestKTCloudCompressibleStartupPassesPreflightAndServerCreate(t *testing.T) {
	original := []byte("#!/bin/sh\n" + strings.Repeat("# Repeated startup documentation for the workspace application.\n", 64) + "true\n")
	before := bytes.Clone(original)
	digest := fmt.Sprintf("%x", sha256.Sum256(original))
	if len(original) <= 1536 || base64.StdEncoding.EncodedLen(len(original)) <= 2048 {
		t.Fatal("fixture must exceed the obsolete raw-base64 limit")
	}
	posts := 0
	a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/compute/v2/servers/detail":
			writeJSON(t, w, 200, map[string]any{"servers": []any{}})
		case r.Method == "POST" && r.URL.Path == "/compute/v2/servers":
			posts++
			var body struct {
				Server struct {
					UserData string `json:"user_data"`
				} `json:"server"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal("invalid server create JSON")
			}
			if body.Server.UserData == "" || len(body.Server.UserData) > 2048 {
				t.Fatal("final SDK-base64 user_data must fit the transport limit")
			}
			compressed, err := base64.StdEncoding.DecodeString(body.Server.UserData)
			if err != nil {
				t.Fatal("invalid user_data base64")
			}
			reader, err := gzip.NewReader(bytes.NewReader(compressed))
			if err != nil {
				t.Fatal("user_data must contain gzip-compressed startup bytes")
			}
			decoded, readErr := io.ReadAll(reader)
			closeErr := reader.Close()
			startup := ktUserStartupPart(t, decoded)
			if readErr != nil || closeErr != nil || !bytes.Equal(startup, before) || fmt.Sprintf("%x", sha256.Sum256(startup)) != digest {
				t.Fatal("startup content or SHA256 changed during transport")
			}
			writeJSON(t, w, 202, map[string]any{"server": map[string]any{"id": "created-server"}})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
	snapshot := validSnapshot()
	snapshot.ProviderConnectionID = a.provision.ProviderConnectionID
	snapshot.InternetOutbound = false
	snapshot.StartupScript.Content = string(original)
	snapshot.StartupScript.SHA256 = digest
	request := coreprovider.ProvisionRequest{Correlation: coreprovider.Correlation{OperationID: "compressible-operation", LabInstanceID: "compressible-instance", Generation: 1}, CreationSnapshot: snapshot}
	if err := validateProvisionRequest(request); err != nil {
		t.Fatalf("long compressible startup must pass request validation: %v", err)
	}
	if err := a.validateProvisionSettings(a.provision, snapshot); err != nil {
		t.Fatalf("long compressible startup must pass preflight: %v", err)
	}
	resource, server, err := a.EnsureServer(context.Background(), resourceIdentity(1, "workspace"), ServerSpec{Name: "fixture", ImageID: "image", FlavorID: "flavor", KeyPair: "key", NetworkIDs: []string{"management-ref", "lab-ref"}, UserData: original})
	if err != nil || posts != 1 || resource.ProviderID != "created-server" || server.ID != "created-server" || !bytes.Equal(original, before) || snapshot.StartupScript.Content != string(before) || snapshot.StartupScript.SHA256 != digest {
		t.Fatalf("server create or immutable startup preservation failed: posts=%d err=%v", posts, err)
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
					value := body.Server["user_data"]
					{
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
						if err != nil || closeErr != nil || !bytes.Equal(ktUserStartupPart(t, decoded), before) {
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

func ktUserStartupPart(t *testing.T, data []byte) []byte {
	t.Helper()
	message, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	_, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	reader := multipart.NewReader(message.Body, params["boundary"])
	bootstrap, err := reader.NextPart()
	if err != nil || bootstrap.Header.Get("Content-Type") != "text/cloud-boothook" {
		t.Fatal("management boot part is required even without user startup")
	}
	part, err := reader.NextPart()
	if err == io.EOF {
		return nil
	}
	if err != nil || part.FileName() != "labbit-user-startup" || part.Header.Get("Content-Transfer-Encoding") != "base64" {
		t.Fatal("immutable startup must be a separate base64 MIME part")
	}
	encoded, err := io.ReadAll(part)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}
