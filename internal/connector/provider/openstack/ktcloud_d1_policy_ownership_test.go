package openstackprovider

import (
	"context"
	"net/http"
	"strings"
	"testing"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

func TestKTCloudPolicyOwnershipRequiresSuccessfulJobAndCompleteMapping(t *testing.T) {
	for _, scenario := range []string{"valid", "wrong destination", "wrong source", "wrong source IP", "wrong port", "deny action", "NAT enabled", "FAILED job", "unknown job", "duplicate comment"} {
		t.Run(scenario, func(t *testing.T) {
			createdTier, createdPolicy := false, false
			deletes := 0
			comment := provisionBaseName("lab", 1) + ":workspace:management-ssh"
			a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					deletes++
					w.WriteHeader(204)
					return
				}
				switch {
				case strings.HasSuffix(r.URL.Path, "/nsm/network"):
					if r.Method == http.MethodPost {
						createdTier = true
						writeJSON(t, w, 201, map[string]any{"httpStatus": 201, "data": map[string]any{"networkId": "new-tier"}})
						return
					}
					items := []any{}
					if createdTier {
						items = append(items, map[string]any{"networkId": "new-tier", "refId": "new-ref", "networkName": ktCloudManagementTierName("lab", 1, "workspace"), "cidr": "172.25.241.0/24", "status": "ACTIVE"})
					}
					writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]any{"total": len(items), "offset": 0}, "data": items})
				case strings.HasSuffix(r.URL.Path, "/nsm/firewall/policy"):
					if r.Method == http.MethodPost {
						createdPolicy = true
						writeJSON(t, w, 202, map[string]any{"httpStatus": 202, "jobId": "created-job"})
						return
					}
					items := []any{}
					if createdPolicy {
						policy := map[string]any{"policyId": "candidate-policy", "comment": comment, "action": "accept", "status": "enable", "nat": "disable", "srcInterface": []any{map[string]any{"networkId": "connector-tier"}}, "dstInterface": []any{map[string]any{"networkId": "new-tier"}}, "srcAddress": []any{map[string]any{"name": "172.25.0.11/32"}}, "dstAddress": []any{map[string]any{"name": "all"}}, "services": []any{map[string]any{"protocol": "TCP", "startPort": "22", "endPort": "22"}}}
						switch scenario {
						case "wrong destination":
							policy["dstInterface"] = []any{map[string]any{"networkId": "foreign-tier"}}
						case "wrong source":
							policy["srcInterface"] = []any{map[string]any{"networkId": "foreign-tier"}}
						case "wrong source IP":
							policy["srcAddress"] = []any{map[string]any{"name": "0.0.0.0/0"}}
						case "wrong port":
							policy["services"] = []any{map[string]any{"protocol": "TCP", "startPort": "23", "endPort": "23"}}
						case "deny action":
							policy["action"] = "deny"
						case "NAT enabled":
							policy["nat"] = "enable"
						}
						items = append(items, policy)
						if scenario == "duplicate comment" {
							items = append(items, policy)
						}
					}
					writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]any{"total": len(items), "offset": 0}, "data": items})
				case strings.Contains(r.URL.Path, "/job/status/"):
					state := "SUCCESS"
					if scenario == "FAILED job" {
						state = "FAILED"
					}
					if scenario == "unknown job" {
						state = "unrecognized"
					}
					writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "jobId": "created-job", "jobStatus": state})
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			})
			request := coreprovider.ProvisionRequest{Correlation: coreprovider.Correlation{LabInstanceID: "lab", Generation: 1}}
			vm := coreprovider.VMSpec{VMKey: "workspace"}
			prior := []coreprovider.ResourceResult{ktResource(resourceIdentity(1, "lab-tier"), coreprovider.ResourceTypeTier, "lab-tier", "ACTIVE")}
			_, resources, err := a.prepareKTCloudNICs(context.Background(), request, vm, a.provision, provisionTopology{tier: ktCloudTier{ID: "lab-tier", RefID: "lab-ref"}}, prior)
			if (err == nil) != (scenario == "valid") {
				t.Fatalf("policy mapping outcome: %v", err)
			}
			confirmed := false
			for _, ref := range resources {
				if ref.ResourceType == coreprovider.ResourceTypeFirewall {
					confirmed = true
					if scenario != "valid" {
						t.Fatal("unverified policy entered owned cleanup refs")
					}
				}
			}
			if confirmed != (scenario == "valid") || deletes != 0 {
				t.Fatal("policy ownership/cleanup boundary violated")
			}
			if len(resources) < 2 || resources[0].ProviderID != "lab-tier" || resources[1].ProviderID != "new-tier" {
				t.Fatal("known Tier refs lost after policy failure")
			}
		})
	}
}
