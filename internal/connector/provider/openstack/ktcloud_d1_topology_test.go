package openstackprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

func ktTopologyAdapter(t *testing.T, handler http.HandlerFunc) *Adapter {
	a := newTestAdapter(t, handler)
	a.ktNetwork = &ktCloudD1Network{client: &gophercloud.ServiceClient{ProviderClient: a.provider, Endpoint: a.compute.Endpoint + "nsm/"}}
	a.volume = &gophercloud.ServiceClient{ProviderClient: a.provider, Endpoint: a.compute.Endpoint + "volume/"}
	a.provision = normalizedProvisionConfig(ProvisionConfig{ProjectID: "fixture-project", ProviderConnectionID: "local-provider", KTCloudConnectorTierID: "connector-tier", KTCloudManagementCIDR: "172.25.241.0/24", LabSubnetCIDR: "172.25.240.0/24", KeyPairName: "key", SSHAllowedCIDR: "172.25.0.11/32", SSHUsername: "ubuntu", SSHPrivateKeyFile: "private-key", SSHKnownHostsFile: "pins", PollInterval: time.Millisecond, ActiveTimeout: time.Second})
	return a
}

func TestKTCloudHTTP200FaultIsUnavailableNotEmptyInventory(t *testing.T) {
	posts := 0
	a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts++
		}
		writeJSON(t, w, 200, map[string]any{"computeFault": map[string]any{"code": 500, "message": "sensitive-provider-payload"}})
	})
	_, _, err := a.EnsureServer(context.Background(), resourceIdentity(1, "workspace"), ServerSpec{Name: "fixture", ImageID: "image", FlavorID: "flavor", KeyPair: "key", NetworkIDs: []string{"management-ref", "lab-ref"}})
	if err == nil || posts != 0 || strings.Contains(err.Error(), "sensitive-provider-payload") {
		t.Fatalf("unavailable inventory permitted mutation or exposed data: posts=%d err=%v", posts, err)
	}
	for _, kind := range []string{coreprovider.ResourceTypeServer, coreprovider.ResourceTypeVolume} {
		state, exists, err := a.observeResource(context.Background(), coreprovider.ResourceRef{ResourceType: kind, ProviderID: "known-id", Generation: 1})
		if err == nil || exists || state == stateAbsent {
			t.Fatalf("%s fault was reported as a known reality: state=%s exists=%t err=%v", kind, state, exists, err)
		}
	}
}

func TestKTCloudServerHTTP400NeverConfirmsAbsence(t *testing.T) {
	a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/servers/known-id") {
			t.Fatalf("unexpected observation request: %s %s", r.Method, r.URL.Path)
		}
		writeJSON(t, w, 400, map[string]any{"badRequest": map[string]any{"code": 400}})
	})
	state, exists, err := a.observeResource(context.Background(), coreprovider.ResourceRef{ResourceType: coreprovider.ResourceTypeServer, ProviderID: "known-id", Generation: 1})
	if err == nil || exists || state == stateAbsent {
		t.Fatalf("soft-deleted or uncertain server was marked absent: %s %t %v", state, exists, err)
	}
}

func TestKTCloudResetCreditsObservedExtendedFlavor(t *testing.T) {
	cores, ram, err := ktCloudServerQuotaCredit(servers.Server{Flavor: map[string]any{"original_name": "2x4.itl", "vcpus": json.Number("2"), "ram": json.Number("4096")}})
	if err != nil || cores != 2 || ram != 4096 {
		t.Fatalf("observed quota credit: %d %d %v", cores, ram, err)
	}
	for _, flavor := range []map[string]any{{"original_name": "2x4.itl", "vcpus": 2}, {"vcpus": 2, "ram": 4096}, {"original_name": "2x4.itl", "vcpus": 2.5, "ram": 4096}} {
		if _, _, err := ktCloudServerQuotaCredit(servers.Server{Flavor: flavor}); err == nil {
			t.Fatal("incomplete or invalid observed capacity was credited")
		}
	}
}

func TestKTCloudSoftDeleteRequiresOneForceDeleteAndActual404(t *testing.T) {
	for _, tc := range []struct {
		code             int
		delayedInventory bool
	}{{202, false}, {202, true}, {503, false}} {
		t.Run(fmt.Sprintf("%d/delayed=%t", tc.code, tc.delayedInventory), func(t *testing.T) {
			soft, forced := false, false
			deletes, actions := 0, 0
			inventoryReads := 0
			a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "DELETE" && strings.HasSuffix(r.URL.Path, "/servers/owned-server"):
					deletes++
					soft = true
					w.WriteHeader(204)
				case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/servers/owned-server/action"):
					actions++
					var body map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil {
						t.Fatal("force payload invalid")
					}
					if value, ok := body["forceDelete"]; !ok || value != "" {
						t.Fatal("force delete payload differs from documented action")
					}
					if tc.delayedInventory && inventoryReads < 2 {
						t.Fatal("forceDelete preceded observed inventory absence")
					}
					forced = tc.code == 202
					w.WriteHeader(tc.code)
				case strings.HasSuffix(r.URL.Path, "/servers/owned-server"):
					if forced {
						w.WriteHeader(404)
					} else if soft {
						w.WriteHeader(400)
					} else {
						writeJSON(t, w, 200, map[string]any{"server": map[string]any{"id": "owned-server", "status": "ACTIVE"}})
					}
				case strings.HasSuffix(r.URL.Path, "/servers/detail"):
					inventoryReads++
					items := []any{}
					if tc.delayedInventory && inventoryReads == 1 {
						items = append(items, map[string]any{"id": "owned-server", "status": "SOFT_DELETED"})
					}
					writeJSON(t, w, 200, map[string]any{"servers": items})
				case strings.HasSuffix(r.URL.Path, "/volumes/detail"):
					writeJSON(t, w, 200, map[string]any{"volumes": []any{map[string]any{"id": "root-volume", "status": "in-use", "attachments": []any{map[string]any{"server_id": "owned-server"}}}}})
				default:
					t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
				}
			})
			err := a.deleteKTCloudServer(context.Background(), "owned-server")
			if deletes != 1 || actions != 1 {
				t.Fatalf("mutations retried: delete=%d force=%d", deletes, actions)
			}
			if (err == nil) != (tc.code == 202) {
				t.Fatalf("unconfirmed deletion outcome: %v", err)
			}
		})
	}
}

func TestKTCloudServerVolumeAndPhysicalNetworkMapping(t *testing.T) {
	for _, test := range []struct {
		name               string
		httpCode, bodyCode int
		wantOutcome        coreprovider.Outcome
	}{{"accepted", 202, 0, coreprovider.OutcomeSucceeded}, {"HTTP200Fault400", 200, 400, coreprovider.OutcomeFailed}, {"HTTP200Fault500", 200, 500, coreprovider.OutcomeUnknown}, {"HTTP429", 429, 0, coreprovider.OutcomeUnknown}} {
		t.Run(test.name, func(t *testing.T) {
			posts := 0
			a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "GET" && r.URL.Path == "/compute/v2/servers/detail":
					writeJSON(t, w, 200, map[string]any{"servers": []any{}})
				case r.Method == "POST" && r.URL.Path == "/compute/v2/servers":
					posts++
					var body map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil {
						t.Fatal("request decode failed")
					}
					s := body["server"].(map[string]any)
					if _, exists := s["imageRef"]; exists {
						t.Error("KT boot from volume must not send an empty imageRef")
					}
					if s["key_name"] != "key" || s["availability_zone"] != "DX-M1" {
						t.Error("missing key/zone mapping")
					}
					networks := s["networks"].([]any)
					if len(networks) != 2 || networks[0].(map[string]any)["uuid"] != "management-ref" || networks[1].(map[string]any)["uuid"] != "lab-ref" {
						t.Error("Nova requires physical refs, management NIC first")
					}
					root := s["block_device_mapping_v2"].([]any)[0].(map[string]any)
					if root["uuid"] != "image" || root["volume_size"] != float64(50) || root["boot_index"] != "0" || root["source_type"] != "image" || root["destination_type"] != "volume" || root["delete_on_termination"] != true {
						t.Error("root volume mapping is invalid")
					}
					if test.bodyCode != 0 {
						writeJSON(t, w, test.httpCode, map[string]any{"computeFault": map[string]any{"code": test.bodyCode, "message": "sensitive-provider-payload"}})
					} else {
						writeJSON(t, w, test.httpCode, map[string]any{"server": map[string]any{"id": "server-created"}})
					}
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(404)
				}
			})
			resource, _, err := a.EnsureServer(context.Background(), resourceIdentity(1, "workspace"), ServerSpec{Name: "labbit-workspace", ImageID: "image", FlavorID: "flavor", KeyPair: "key", NetworkIDs: []string{"management-ref", "lab-ref"}})
			if posts != 1 {
				t.Fatalf("POST calls=%d, expected one", posts)
			}
			if test.wantOutcome == coreprovider.OutcomeSucceeded {
				if err != nil || resource.ProviderID != "server-created" || resource.ResourceType != coreprovider.ResourceTypeServer {
					t.Fatalf("server mapping failed: %v", err)
				}
			} else {
				if err == nil || strings.Contains(err.Error(), "sensitive-provider-payload") {
					t.Fatalf("unsafe or missing error: %v", err)
				}
				outcome := mutationFailure(nil, err, "safe").Outcome
				if outcome != test.wantOutcome {
					t.Fatalf("outcome=%s", outcome)
				}
			}
		})
	}
}

func TestKTCloudUnsupportedResetAndMixedCleanupDoNotMutate(t *testing.T) {
	calls := 0
	a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) })
	snapshot := validSnapshot()
	snapshot.ProviderConnectionID = a.provision.ProviderConnectionID
	snapshot.InternetOutbound = true
	reset, err := a.Reset(context.Background(), coreprovider.ResetRequest{Correlation: coreprovider.Correlation{OperationID: "reset", LabInstanceID: "lab", Generation: 2}, CreationSnapshot: snapshot, ProviderResources: []coreprovider.ResourceRef{{ResourceType: coreprovider.ResourceTypeServer, ProviderID: "server", Generation: 1, LogicalName: "workspace"}}})
	if err != nil || reset.Outcome != coreprovider.OutcomeFailed || calls != 0 {
		t.Fatal("unsupported Reset must reject before existing resources are touched")
	}
	for _, kind := range []string{coreprovider.ResourceTypePort, coreprovider.ResourceTypeTier} {
		id := "port"
		if kind == coreprovider.ResourceTypeTier {
			id = a.provision.KTCloudConnectorTierID
		}
		result, err := a.Cleanup(context.Background(), coreprovider.CleanupRequest{Correlation: coreprovider.Correlation{OperationID: "cleanup", LabInstanceID: "lab", Generation: 1}, ProviderResources: []coreprovider.ResourceRef{{ResourceType: kind, ProviderID: id, Generation: 1}}})
		if err != nil || result.Outcome != coreprovider.OutcomeFailed || calls != 0 {
			t.Fatal("mixed profile/shared Connector Tier must reject before DELETE")
		}
	}
}

func TestKTCloudReconcileKnownMissingAndCandidateNeverOwns(t *testing.T) {
	deleted := 0
	correlation := coreprovider.Correlation{OperationID: "reconcile", LabInstanceID: "lab", Generation: 1}
	base := provisionBaseName("lab", 1)
	a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			deleted++
			w.WriteHeader(204)
			return
		}
		switch r.URL.Path {
		case "/compute/v2/servers/detail":
			writeJSON(t, w, 200, map[string]any{"servers": []any{}})
		case "/compute/v2/nsm/network":
			writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]any{"total": 1, "offset": 0}, "data": []any{map[string]any{"networkId": "candidate-tier", "refId": "physical-ref", "networkName": base + "-lab-tier", "status": "ACTIVE"}}})
		case "/compute/v2/nsm/firewall/policy":
			writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]any{"total": 0, "offset": 0}, "data": []any{}})
		default:
			w.WriteHeader(404)
		}
	})
	result, err := a.Reconcile(context.Background(), coreprovider.ReconcileRequest{Correlation: correlation, KnownResources: []coreprovider.ResourceRef{{ResourceType: coreprovider.ResourceTypeTier, ProviderID: "missing-tier", Generation: 1, LogicalName: "lab-tier"}}, DiscoverCandidates: true})
	if err != nil || len(result.Observations) != 2 {
		t.Fatalf("reconcile: %v observations=%d", err, len(result.Observations))
	}
	known, candidate := result.Observations[0], result.Observations[1]
	if known.Exists || known.ObservedState != stateAbsent || known.Source != coreprovider.SourceKnownResource || !candidate.Exists || candidate.Source != coreprovider.SourceDiscoveredCandidate {
		t.Fatal("known/candidate boundary was lost")
	}
	cleanup, err := a.Cleanup(context.Background(), coreprovider.CleanupRequest{Correlation: correlation, ProviderResources: []coreprovider.ResourceRef{}})
	if err != nil || cleanup.Outcome != coreprovider.OutcomeSucceeded || deleted != 0 {
		t.Fatal("discovered candidate was treated as owned")
	}
}

func TestKTCloudCleanupActualResourceOrderAndAbsence(t *testing.T) {
	order := []string{}
	serverDeleted := false
	volumeDeleted := false
	a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			if strings.HasSuffix(r.URL.Path, "/servers/server-id") {
				serverDeleted = true
			}
			if strings.HasSuffix(r.URL.Path, "/volumes/volume-id") {
				volumeDeleted = true
			}
			order = append(order, r.URL.Path)
			w.WriteHeader(204)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/servers/server-id") && !serverDeleted {
			writeJSON(t, w, 200, map[string]any{"server": map[string]any{"id": "server-id", "status": "ACTIVE"}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/volumes/volume-id") && !volumeDeleted {
			writeJSON(t, w, 200, map[string]any{"volume": map[string]any{"id": "volume-id", "status": "available", "attachments": []any{}}})
			return
		}
		if strings.Contains(r.URL.Path, "nsm/") {
			writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]any{"total": 0, "offset": 0}, "data": []any{}})
			return
		}
		w.WriteHeader(404)
	})
	refs := []coreprovider.ResourceRef{}
	for _, entry := range []struct{ kind, id string }{{coreprovider.ResourceTypeTier, "tier-id"}, {coreprovider.ResourceTypeFirewall, "policy-id"}, {coreprovider.ResourceTypeVolume, "volume-id"}, {coreprovider.ResourceTypeServer, "server-id"}} {
		refs = append(refs, coreprovider.ResourceRef{ResourceType: entry.kind, ProviderID: entry.id, Generation: 1})
	}
	result, err := a.Cleanup(context.Background(), coreprovider.CleanupRequest{Correlation: coreprovider.Correlation{OperationID: "cleanup", LabInstanceID: "lab", Generation: 1}, ProviderResources: refs})
	if err != nil || result.Outcome != coreprovider.OutcomeSucceeded {
		t.Fatalf("cleanup: %v outcome=%s", err, result.Outcome)
	}
	expected := []string{"/compute/v2/servers/server-id", "/compute/v2/volume/volumes/volume-id", "/compute/v2/nsm/firewall/policy/policy-id", "/compute/v2/nsm/network/tier-id"}
	if !reflect.DeepEqual(order, expected) {
		t.Fatalf("delete order=%v", order)
	}
	for _, r := range result.ProviderResources {
		if r.ObservedState != stateDeleted {
			t.Fatal("unconfirmed absence marked deleted")
		}
	}
}

func TestKTCloudRootAlreadyDeletedWithServerDoesNotMutate(t *testing.T) {
	for _, code := range []int{404, 200} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/volumes/root-id") {
					t.Fatalf("unexpected mutation or lookup: %s %s", r.Method, r.URL.Path)
				}
				if code == 404 {
					w.WriteHeader(404)
				} else {
					writeJSON(t, w, 200, map[string]any{"volumeFault": map[string]any{"code": 500, "message": "sensitive-provider-payload"}})
				}
			})
			result, err := a.Cleanup(context.Background(), coreprovider.CleanupRequest{Correlation: coreprovider.Correlation{OperationID: "cleanup", LabInstanceID: "lab", Generation: 1}, ProviderResources: []coreprovider.ResourceRef{{ResourceType: coreprovider.ResourceTypeVolume, ProviderID: "root-id", Generation: 1}}})
			want := coreprovider.OutcomeSucceeded
			if code == 200 {
				want = coreprovider.OutcomeUnknown
			}
			if err != nil || result.Outcome != want {
				t.Fatalf("root absence outcome=%s err=%v", result.Outcome, err)
			}
		})
	}
}

func TestKTCloudManagementAddressNeverFallsBackToLab(t *testing.T) {
	tier := ktCloudTier{Name: "management", RefID: "management-ref", CIDR: "172.25.241.0/24"}
	server := servers.Server{Addresses: map[string]any{"lab": []any{map[string]any{"addr": "172.25.240.10", "OS-EXT-IPS:type": "fixed"}}}}
	if _, err := ktServerManagementAddress(server, tier); !errors.Is(err, ErrManagementIP) {
		t.Fatal("lab address used as management address")
	}
	server.Addresses["management"] = []any{map[string]any{"addr": "172.25.241.10", "OS-EXT-IPS:type": "fixed"}}
	ip, err := ktServerManagementAddress(server, tier)
	if err != nil || ip != "172.25.241.10" {
		t.Fatalf("management mapping: %v", err)
	}
	server.Addresses["management"] = append(server.Addresses["management"].([]any), map[string]any{"addr": "172.25.241.11", "OS-EXT-IPS:type": "fixed"})
	if _, err = ktServerManagementAddress(server, tier); err == nil {
		t.Fatal("ambiguous addresses accepted")
	}
}

func TestKTCloudResetRequiresExactActualResourceSet(t *testing.T) {
	a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected API request") })
	snapshot := validSnapshot()
	snapshot.InternetOutbound = false
	refs := []coreprovider.ResourceRef{}
	vm := snapshot.VMs[0]
	for i, entry := range []struct{ kind, label string }{{coreprovider.ResourceTypeTier, "lab-tier"}, {coreprovider.ResourceTypeTier, vm.VMKey + ":management-tier"}, {coreprovider.ResourceTypeFirewall, vm.VMKey + ":management-ssh"}, {coreprovider.ResourceTypeServer, vm.VMKey}, {coreprovider.ResourceTypeVolume, vm.VMKey + ":root-volume"}} {
		refs = append(refs, coreprovider.ResourceRef{ResourceType: entry.kind, ProviderID: fmt.Sprintf("id-%d", i), Generation: 1, LogicalName: entry.label})
	}
	if _, err := a.validateResetResourceSet(refs, snapshot, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := a.validateResetResourceSet(refs[:4], snapshot, 2); err == nil {
		t.Fatal("missing root volume must reject Reset")
	}
	refs[0].Generation = 2
	if _, err := a.validateResetResourceSet(refs, snapshot, 2); err == nil {
		t.Fatal("wrong generation accepted")
	}
}
