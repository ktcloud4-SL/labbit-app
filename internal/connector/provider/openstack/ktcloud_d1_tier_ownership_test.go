package openstackprovider

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

func TestKTCloudTierOwnershipBeforeResultAndFollowupCleanup(t *testing.T) {
	cases := []struct {
		name    string
		owned   bool
		outcome coreprovider.Outcome
	}{
		{"body409", false, coreprovider.OutcomeFailed},
		{"body500", false, coreprovider.OutcomeUnknown},
		{"name mismatch", false, coreprovider.OutcomeUnknown},
		{"CIDR mismatch", false, coreprovider.OutcomeUnknown},
		{"shared", false, coreprovider.OutcomeUnknown},
		{"shared missing", false, coreprovider.OutcomeUnknown},
		{"shared null", false, coreprovider.OutcomeUnknown},
		{"name missing", false, coreprovider.OutcomeUnknown},
		{"CIDR missing", false, coreprovider.OutcomeUnknown},
		{"ID existed before POST", false, coreprovider.OutcomeUnknown},
		{"lookup failed before confirmation", false, coreprovider.OutcomeUnknown},
		{"matching BUILD then lookup failed", true, coreprovider.OutcomeUnknown},
		{"matching BUILD then shared", false, coreprovider.OutcomeUnknown},
		{"matching BUILD then shared missing", true, coreprovider.OutcomeUnknown},
		{"matching BUILD then shared null", true, coreprovider.OutcomeUnknown},
		{"matching BUILD then name missing", true, coreprovider.OutcomeUnknown},
		{"matching BUILD then CIDR missing", true, coreprovider.OutcomeUnknown},
		{"matching ERROR", true, coreprovider.OutcomeUnknown},
		{"matching BUILD then timeout", true, coreprovider.OutcomeUnknown},
		{"matching ACTIVE", true, coreprovider.OutcomeSucceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const name = "owned-management-tier"
			const cidr = "172.25.241.0/24"
			created, cleanup := false, false
			postReads, posts := 0, 0
			deleted := map[string]bool{}
			a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					id := strings.TrimPrefix(r.URL.Path, "/compute/v2/nsm/network/")
					if !cleanup || id == r.URL.Path {
						t.Error("unexpected automatic mutation")
					}
					deleted[id] = true
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if r.URL.Path != "/compute/v2/nsm/network" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
					return
				}
				if r.Method == http.MethodPost {
					created = true
					posts++
					status := 201
					code := 201
					if tc.name == "body409" || tc.name == "body500" {
						status = 200
						code = 409
						if tc.name == "body500" {
							code = 500
						}
					}
					writeJSON(t, w, status, map[string]any{"httpStatus": code, "data": map[string]any{"networkId": "receipt-tier"}, "error": "sensitive-provider-payload"})
					return
				}
				if r.Method != http.MethodGet || r.URL.Query().Get("networkType") != "ALL" {
					t.Error("Tier ownership requires complete inventory")
					w.WriteHeader(500)
					return
				}
				if created && !cleanup {
					postReads++
					if tc.name == "lookup failed before confirmation" || (tc.name == "matching BUILD then lookup failed" && postReads > 1) {
						w.WriteHeader(500)
						return
					}
				}
				items := []any{}
				if !deleted["prior-tier"] {
					items = append(items, map[string]any{"networkId": "prior-tier", "networkName": "prior-lab-tier", "cidr": "172.25.240.0/24", "status": "ACTIVE"})
				}
				if (created || tc.name == "ID existed before POST") && !deleted["receipt-tier"] {
					item := map[string]any{"networkId": "receipt-tier", "networkName": name, "cidr": cidr, "shared": false, "status": "ACTIVE", "refId": "physical-ref"}
					switch tc.name {
					case "name mismatch":
						item["networkName"] = "foreign-tier"
					case "CIDR mismatch":
						item["cidr"] = "172.25.242.0/24"
					case "shared":
						item["shared"] = true
					case "shared missing":
						delete(item, "shared")
					case "shared null":
						item["shared"] = nil
					case "name missing":
						delete(item, "networkName")
					case "CIDR missing":
						delete(item, "cidr")
					case "ID existed before POST":
						if !created {
							item["networkName"], item["cidr"] = "foreign-tier", "172.25.242.0/24"
						}
					case "matching BUILD then shared":
						if postReads == 1 {
							item["status"], item["refId"] = "BUILD", ""
						} else {
							item["shared"] = true
						}
					case "matching BUILD then lookup failed", "matching BUILD then timeout":
						item["status"], item["refId"] = "BUILD", ""
					case "matching BUILD then shared missing", "matching BUILD then shared null", "matching BUILD then name missing", "matching BUILD then CIDR missing":
						item["status"], item["refId"] = "BUILD", ""
						if postReads > 1 {
							switch tc.name {
							case "matching BUILD then shared missing":
								delete(item, "shared")
							case "matching BUILD then shared null":
								item["shared"] = nil
							case "matching BUILD then name missing":
								delete(item, "networkName")
							case "matching BUILD then CIDR missing":
								delete(item, "cidr")
							}
						}
					case "matching ERROR":
						item["status"] = "ERROR"
					}
					items = append(items, item)
				}
				writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]int{"total": len(items), "offset": 0}, "data": items})
			})
			if tc.name == "matching BUILD then timeout" {
				a.provision.ActiveTimeout = 20 * time.Millisecond
			}
			prior := []coreprovider.ResourceResult{ktResource(resourceIdentity(1, "lab-tier"), coreprovider.ResourceTypeTier, "prior-tier", "ACTIVE")}
			before := cloneResourceResults(prior)
			tier, resources, err := a.createKTCloudTier(context.Background(), resourceIdentity(1, "workspace:management-tier"), name, cidr, prior)
			result := coreprovider.OperationResult{Outcome: coreprovider.OutcomeSucceeded, ProviderResources: resources}
			if err != nil {
				result = mutationFailure(resources, err, "VM network could not be created")
			}
			if result.Outcome != tc.outcome || posts != 1 || !reflect.DeepEqual(prior, before) {
				t.Fatalf("outcome=%s, posts=%d, preserved=%t, err=%v", result.Outcome, posts, reflect.DeepEqual(prior, before), err)
			}
			if tc.outcome == coreprovider.OutcomeSucceeded && (tier.ID != "receipt-tier" || tier.RefID != "physical-ref") {
				t.Fatal("confirmed Tier mapping lost")
			}
			if err != nil && strings.Contains(err.Error(), "sensitive-provider-payload") {
				t.Fatal("raw error exposed")
			}
			refs := []coreprovider.ResourceRef{}
			for _, ref := range result.ProviderResources {
				refs = append(refs, ref.ResourceRef)
			}
			want := 1
			if tc.owned {
				want++
			}
			if len(refs) != want || refs[0] != before[0].ResourceRef {
				t.Fatalf("unverified Tier adopted or confirmed prior ref lost: %+v", refs)
			}
			if tc.owned && (refs[1].ProviderID != "receipt-tier" || refs[1].LogicalName != "workspace:management-tier" || refs[1].Generation != 1) {
				t.Fatal("incorrect confirmed ownership")
			}
			if tc.name == "body409" && !errors.Is(err, ErrMutationRejected) {
				t.Fatal("definite rejection changed")
			}
			cleanup = true
			cleaned, cleanupErr := a.Cleanup(context.Background(), coreprovider.CleanupRequest{Correlation: coreprovider.Correlation{OperationID: "cleanup", LabInstanceID: "lab", Generation: 1}, ProviderResources: refs})
			if cleanupErr != nil || cleaned.Outcome != coreprovider.OutcomeSucceeded || !deleted["prior-tier"] || deleted["receipt-tier"] != tc.owned || len(deleted) != want {
				t.Fatal("follow-up Cleanup crossed ownership boundary")
			}
		})
	}
}
