package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gophercloud/gophercloud/v2"
)

func TestPolicyCandidatesNeverBecomeCleanupOwned(t *testing.T) {
	for _, candidate := range []bool{true, false} {
		t.Run(map[bool]string{true: "untracked matching candidate stops deletion", false: "only previously journaled ID deleted"}[candidate], func(t *testing.T) {
			oldState, oldNSM, oldPath := state, nsm, ledgerPath
			defer func() { state, nsm, ledgerPath = oldState, oldNSM, oldPath }()
			state = ledger{Name: "fixture", PolicyAttempted: map[string]bool{"bootstrap-ssh": true}, PolicyIDs: []string{"owned-policy"}}
			ledgerPath = filepath.Join(t.TempDir(), "ledger.json")
			deleted := []string{}
			present := true
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					deleted = append(deleted, r.URL.Path)
					if r.URL.Path != "/firewall/policy/owned-policy" {
						t.Errorf("unowned deletion: %s", r.URL.Path)
					}
					present = false
					w.WriteHeader(204)
					return
				}
				items := []policy{{ID: "unrelated-policy", Comment: "unrelated"}}
				if present {
					items = append(items, policy{ID: "owned-policy", Comment: "fixture:bootstrap-ssh"})
				}
				if candidate {
					items = append(items, policy{ID: "untracked-policy", Comment: "fixture:bootstrap-https"})
				}
				json.NewEncoder(w).Encode(map[string]any{"httpStatus": 200, "data": items, "pagination": map[string]int{"total": len(items)}})
			}))
			defer server.Close()
			nsm = &gophercloud.ServiceClient{ProviderClient: &gophercloud.ProviderClient{}, Endpoint: server.URL + "/"}
			err := cleanupAccess(context.Background())
			if candidate {
				if err == nil || len(deleted) != 0 || len(state.PolicyIDs) != 1 {
					t.Fatal("candidate was adopted or deleted")
				}
			} else if err != nil || len(deleted) != 1 {
				t.Fatalf("known-only cleanup failed: %v", err)
			}
		})
	}
}

func TestMatchingPolicyWithoutReceiptStopsProvision(t *testing.T) {
	oldState, oldNSM := state, nsm
	defer func() { state, nsm = oldState, oldNSM }()
	state = ledger{Name: "fixture"}
	mutations := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutations++
			t.Error("mutation requested before ownership confirmation")
		}
		json.NewEncoder(w).Encode(map[string]any{"httpStatus": 200, "data": []policy{{ID: "candidate", Comment: "fixture:bootstrap-ssh"}}, "pagination": map[string]int{"total": 1}})
	}))
	defer server.Close()
	nsm = &gophercloud.ServiceClient{ProviderClient: &gophercloud.ProviderClient{}, Endpoint: server.URL + "/"}
	if fixturePolicy(context.Background(), "bootstrap-ssh", map[string]any{}) == nil || mutations != 0 || len(state.PolicyIDs) != 0 {
		t.Fatal("unowned policy adopted")
	}
}
