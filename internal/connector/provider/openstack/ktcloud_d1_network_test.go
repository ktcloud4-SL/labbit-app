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
	"github.com/gophercloud/gophercloud/v2/openstack"
	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

func ktCloudNetworkPeer(t *testing.T, handler http.HandlerFunc) *ktCloudD1Network {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := openstack.NewClient(server.URL + "/identity/")
	if err != nil {
		t.Fatal(err)
	}
	client.SetToken("fixture-token")
	return &ktCloudD1Network{client: &gophercloud.ServiceClient{ProviderClient: client, Endpoint: server.URL + "/nsm/v1/", Type: "network"}}
}

func TestKTCloudTierPaginationAndDistinctIDs(t *testing.T) {
	var calls atomic.Int32
	n := ktCloudNetworkPeer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/nsm/v1/network" || r.URL.Query().Get("networkType") != "ALL" || r.URL.Query().Get("size") != "2000" {
			t.Error("unexpected Tier inventory request")
			w.WriteHeader(400)
			return
		}
		if r.URL.Query().Get("page") == "1" {
			fmt.Fprint(w, `{"httpStatus":200,"pagination":{"total":2,"offset":0},"data":[{"networkId":"tier-id","refId":"neutron-ref","networkName":"lab","cidr":"172.25.3.0/24","status":"ACTIVE"}]}`)
		} else {
			fmt.Fprint(w, `{"httpStatus":"200","pagination":{"total":2,"offset":1},"data":[{"networkId":"external-id","refId":"","networkName":"external"}]}`)
		}
	})
	items, err := n.tiers(context.Background())
	if err != nil || len(items) != 2 || items[0].ID != "tier-id" || items[0].RefID != "neutron-ref" || items[1].RefID != "" || calls.Load() != 2 {
		t.Fatalf("Tier mapping/pagination failed: %v", err)
	}
}

func TestKTCloudTierMutationUsesTierIDAndCustomRanges(t *testing.T) {
	n := ktCloudNetworkPeer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			if r.URL.Path != "/nsm/v1/network" {
				t.Error("unexpected Tier create path")
			}
			var body struct {
				Name   string            `json:"name"`
				Type   string            `json:"type"`
				Custom bool              `json:"isCustom"`
				Detail map[string]string `json:"detail"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.Name != "lab" || body.Type != "tier" || !body.Custom || body.Detail["gatewayIp"] != "172.25.3.1" || body.Detail["startIp"] != "172.25.3.6" || body.Detail["iscsiEndIp"] != "172.25.3.254" {
				t.Error("invalid custom Tier request mapping")
			}
			w.WriteHeader(201)
			fmt.Fprint(w, `{"httpStatus":201,"jobId":"create-job","data":{"networkId":"tier-id"}}`)
		case http.MethodDelete:
			if r.URL.Path != "/nsm/v1/network/tier-id" {
				t.Error("delete must use Tier UUID, not Neutron refId")
			}
			w.WriteHeader(202)
			fmt.Fprint(w, `{"httpStatus":"202","jobId":"delete-job"}`)
		default:
			t.Error("unexpected Tier method")
			w.WriteHeader(400)
		}
	})
	result, err := n.createTier(context.Background(), "lab", "172.25.3.0/24")
	if err != nil || result.Data.ID != "tier-id" || result.JobID != "create-job" {
		t.Fatalf("create mapping: %v", err)
	}
	if err := n.deleteTier(context.Background(), result.Data.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := n.createTier(context.Background(), "lab", "172.25.3.1/24"); !errors.Is(err, ErrInvalidResourceSpec) {
		t.Fatal("noncanonical CIDR must be rejected before calling the API")
	}
}

func TestKTCloudFirewallPolicyAndJobMapping(t *testing.T) {
	n := ktCloudNetworkPeer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/nsm/v1/firewall/policy":
			var body ktCloudFirewallSpec
			if json.NewDecoder(r.Body).Decode(&body) != nil || !body.Accept || body.SourceNAT || body.Protocol != "TCP" || body.Start != "22" || body.End != "22" || len(body.Source) != 1 || body.Source[0] != "connector-tier" || body.Destination[0] != "management-tier" || body.SourceIPs[0] != "172.25.0.11/32" {
				t.Error("incorrect Connector-only SSH policy mapping")
			}
			w.WriteHeader(202)
			fmt.Fprint(w, `{"httpStatus":202,"jobId":"policy-job"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/nsm/v1/job/status/policy-job":
			fmt.Fprint(w, `{"httpStatus":"200","jobId":"policy-job","jobStatus":"SUCCESS","data":null}`)
		case r.Method == http.MethodGet && r.URL.Path == "/nsm/v1/firewall/policy":
			fmt.Fprint(w, `{"httpStatus":200,"pagination":{"total":1,"offset":0},"data":[{"policyId":"policy-id","comment":"labbit-owner","action":"accept","status":"enable","srcInterface":[{"networkId":"connector-tier"}],"dstInterface":[{"networkId":"management-tier"}],"services":[{"protocol":"TCP","startPort":"22","endPort":"22"}]}]}`)
		case r.Method == http.MethodDelete && r.URL.Path == "/nsm/v1/firewall/policy/policy-id":
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	receipt, err := n.createPolicy(context.Background(), ktCloudFirewallSpec{Accept: true, Protocol: "TCP", Start: "22", End: "22", Source: []string{"connector-tier"}, Destination: []string{"management-tier"}, SourceIPs: []string{"172.25.0.11/32"}, Comment: "labbit-owner"})
	if err != nil || receipt.JobID != "policy-job" {
		t.Fatalf("policy create: %v", err)
	}
	if status, err := n.job(context.Background(), receipt.JobID); err != nil || status != "SUCCESS" {
		t.Fatalf("job: %v", err)
	}
	policies, err := n.policies(context.Background())
	if err != nil || len(policies) != 1 || policies[0].ID != "policy-id" || policies[0].Destination[0].ID != "management-tier" {
		t.Fatalf("policy inventory: %v", err)
	}
	if err := n.deletePolicy(context.Background(), policies[0].ID); err != nil {
		t.Fatal(err)
	}
}

func TestKTCloudMutationFailureClassificationNeverBlindRetries(t *testing.T) {
	for _, status := range []int{400, 403, 408, 429, 500} {
		for _, envelope := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/envelope=%t", status, envelope), func(t *testing.T) {
				var calls atomic.Int32
				n := ktCloudNetworkPeer(t, func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if envelope {
						fmt.Fprintf(w, `{"httpStatus":%d,"error":"sensitive-provider-payload"}`, status)
					} else {
						w.WriteHeader(status)
						fmt.Fprint(w, `{"error":"sensitive-provider-payload"}`)
					}
				})
				_, err := n.createTier(context.Background(), "lab", "172.25.3.0/24")
				if err == nil || calls.Load() != 1 || strings.Contains(err.Error(), "sensitive-provider-payload") {
					t.Fatal("unsafe error or unexpected mutation retry")
				}
				outcome := mutationFailure(nil, err, "fixture").Outcome
				want := coreprovider.OutcomeUnknown
				if status == 400 || status == 403 {
					want = coreprovider.OutcomeFailed
				}
				if outcome != want {
					t.Fatalf("outcome=%s want %s", outcome, want)
				}
			})
		}
	}
}

func TestKTCloudMalformedInventoryIsUnknownRatherThanAbsent(t *testing.T) {
	for _, payload := range []string{`{"httpStatus":200,"pagination":{"total":0,"offset":0}}`, `{"httpStatus":200,"pagination":{"total":1,"offset":0},"data":[]}`, `{"httpStatus":200,"pagination":{"total":1,"offset":1},"data":[{"networkId":"tier-id"}]}`, `{"httpStatus":200,"pagination":{"total":1,"offset":0},"data":[{}]}`, `{"httpStatus":200,"pagination":{"total":2,"offset":0},"data":[{"networkId":"other-id"},{"networkId":"other-id"}]}`} {
		n := ktCloudNetworkPeer(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, payload) })
		if _, exists, err := n.tier(context.Background(), "tier-id"); err == nil || exists {
			t.Fatal("incomplete inventory must not claim resource absence")
		}
	}
}

func TestKTCloudDelete404RequiresCompleteInventoryAbsence(t *testing.T) {
	for _, resource := range []string{"network", "firewall/policy"} {
		for _, present := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/present=%t", resource, present), func(t *testing.T) {
				deletes := 0
				n := ktCloudNetworkPeer(t, func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodDelete {
						deletes++
						w.WriteHeader(404)
						return
					}
					if r.Method != http.MethodGet || r.URL.Path != "/nsm/v1/"+resource {
						t.Fatalf("unexpected reconciliation request: %s %s", r.Method, r.URL.Path)
					}
					data := []map[string]string{}
					if present {
						key := "networkId"
						if resource == "firewall/policy" {
							key = "policyId"
						}
						data = append(data, map[string]string{key: "owned-id"})
					}
					writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]int{"total": len(data), "offset": 0}, "data": data})
				})
				kind := coreprovider.ResourceTypeTier
				if resource == "firewall/policy" {
					kind = coreprovider.ResourceTypeFirewall
				}
				result := (&Adapter{ktNetwork: n}).cleanupResources(context.Background(), []coreprovider.ResourceRef{{ResourceType: kind, ProviderID: "owned-id", Generation: 1}})
				want := coreprovider.OutcomeSucceeded
				if present {
					want = coreprovider.OutcomeUnknown
				}
				if deletes != 1 || result.Outcome != want {
					t.Fatalf("contradictory receipt accepted or deletion retried: deletes=%d outcome=%s", deletes, result.Outcome)
				}
			})
		}
	}
}
