package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gophercloud/gophercloud/v2"
)

// A successful POST without an identity is deliberately not cleanup-owned.
// Read-only inspection and a process restart must not permit replaying it.
func TestUnknownAccessReceiptSurvivesInspectionAndRestart(t *testing.T) {
	for _, path := range []string{"publicIp", "portforwarding"} {
		t.Run(path, func(t *testing.T) {
			oldState, oldNSM, oldPath := state, nsm, ledgerPath
			defer func() { state, nsm, ledgerPath = oldState, oldNSM, oldPath }()
			state = ledger{ServerID: "owned-server", PrivateIP: "172.25.1.2", ExternalTierID: "shared-external", SourceCIDR: "192.0.2.1/32"}
			if path == "portforwarding" {
				state.PublicID, state.PublicIP = "owned-public", "192.0.2.2"
			}
			ledgerPath = filepath.Join(t.TempDir(), "ledger.json")
			posts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts++
					if r.URL.Path != "/"+path {
						t.Errorf("unexpected mutation: %s", r.URL.Path)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"httpStatus": 200, "data": map[string]any{}})
					return
				}
				items := []publicIP{}
				if path == "portforwarding" {
					items = append(items, publicIP{ID: "owned-public", IP: "192.0.2.2"})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"httpStatus": 200, "data": items, "pagination": map[string]int{"total": len(items)}})
			}))
			defer server.Close()
			nsm = &gophercloud.ServiceClient{ProviderClient: &gophercloud.ProviderClient{}, Endpoint: server.URL + "/"}
			if access(context.Background()) == nil || posts != 1 {
				t.Fatal("missing receipt must stop after exactly one create")
			}
			// This is the terminal save performed by inspect(), followed by a
			// ledger reload as setup() performs in a fresh process.
			if err := save("READ_ONLY_INSPECTION_COMPLETE"); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(ledgerPath)
			if err != nil {
				t.Fatal(err)
			}
			state = ledger{}
			if err := json.Unmarshal(b, &state); err != nil {
				t.Fatal(err)
			}
			if access(context.Background()) == nil || posts != 1 {
				t.Fatalf("unknown create replayed after inspection/restart: POST count=%d, want 1", posts)
			}
			if state.ForwardID != "" || (path == "publicIp" && state.PublicID != "") {
				t.Fatal("unknown result became cleanup-owned")
			}
		})
	}
}
