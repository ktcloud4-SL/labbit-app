package openstackprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"testing"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

func TestKTCloudTierNamesBoundFullIdentity(t *testing.T) {
	lab := "lbt145-control-189782903"
	if len(provisionBaseName(lab, 1)+"-lab-tier") != 54 || len(provisionBaseName(lab, 1)+"-workspace-management-tier") != 71 {
		t.Fatal("historical deletion failure fixture changed")
	}
	seen := map[string]bool{}
	for _, labID := range []string{lab, strings.Repeat("a", 255) + "x", strings.Repeat("a", 255) + "y", "한글/ID"} {
		for _, generation := range []int64{1, 2, math.MaxInt64} {
			names := []string{ktCloudLabTierName(labID, generation)}
			for _, vmKey := range []string{"workspace", strings.Repeat("v", 255) + "x", strings.Repeat("v", 255) + "y", "Workspace/한글"} {
				names = append(names, ktCloudManagementTierName(labID, generation, vmKey))
			}
			for _, name := range names {
				if len(name) > ktCloudTierNameLimit || seen[name] {
					t.Fatalf("overlong or colliding provider name: %q", name)
				}
				seen[name] = true
				for _, c := range name {
					if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
						t.Fatal("provider name must use ASCII letters, digits and hyphens")
					}
				}
			}
			if names[0] != ktCloudLabTierName(labID, generation) {
				t.Fatal("resource identity must be deterministic")
			}
		}
	}
}

func TestKTCloudTierNameBoundaryRejectsBeforeMutation(t *testing.T) {
	posts := 0
	n := ktCloudNetworkPeer(t, func(w http.ResponseWriter, r *http.Request) {
		posts++
		w.WriteHeader(201)
		fmt.Fprint(w, `{"httpStatus":201,"data":{"networkId":"owned-tier"}}`)
	})
	for _, size := range []int{50, 51, 255} {
		_, err := n.createTier(context.Background(), strings.Repeat("x", size), "172.25.240.0/24")
		if !errors.Is(err, ErrInvalidResourceSpec) || posts != 0 {
			t.Fatalf("name length %d reached the cloud mutation boundary", size)
		}
	}
	if _, err := n.createTier(context.Background(), strings.Repeat("x", 49), "172.25.240.0/24"); err != nil || posts != 1 {
		t.Fatalf("safe boundary rejected: %v posts=%d", err, posts)
	}
}

func TestKTCloudPrepareNetworkUsesBoundedTierName(t *testing.T) {
	request := coreprovider.ProvisionRequest{Correlation: coreprovider.Correlation{LabInstanceID: strings.Repeat("long-lab", 40), Generation: math.MaxInt64}}
	name := ""
	a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "POST":
			var body struct {
				Name string `json:"name"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Name) >= 50 {
				t.Fatal("Tier creation emitted an unsafe provider name")
			}
			name = body.Name
			writeJSON(t, w, 201, map[string]any{"httpStatus": 201, "data": map[string]any{"networkId": "owned-tier"}})
		case "GET":
			items := []any{}
			if name != "" {
				items = append(items, map[string]any{"networkId": "owned-tier", "networkName": name, "cidr": "172.25.240.0/24", "refId": "physical-ref", "status": "ACTIVE"})
			}
			writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]any{"total": len(items), "offset": 0}, "data": items})
		}
	})
	topology, refs, err := a.prepareKTCloudNetwork(context.Background(), request, a.provision)
	if err != nil || len(refs) != 1 || topology.tier.Name != ktCloudLabTierName(request.LabInstanceID, request.Generation) || refs[0].LogicalName != "lab-tier" {
		t.Fatalf("bounded create mapping failed: %v resources=%d", err, len(refs))
	}
}

func TestKTCloudPrepareManagementNICUsesBoundedTierName(t *testing.T) {
	request := coreprovider.ProvisionRequest{Correlation: coreprovider.Correlation{LabInstanceID: "lbt145-control-189782903", Generation: 1}}
	vm := coreprovider.VMSpec{VMKey: "Workspace/" + strings.Repeat("v", 200)}
	name, comment := "", ""
	a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/network"):
			var body struct {
				Name string `json:"name"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Name) >= 50 {
				t.Fatal("Management Tier POST exceeded the deletion name boundary")
			}
			name = body.Name
			writeJSON(t, w, 201, map[string]any{"httpStatus": 201, "data": map[string]any{"networkId": "management-tier"}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/network"):
			items := []any{}
			if name != "" {
				items = append(items, map[string]any{"networkId": "management-tier", "networkName": name, "cidr": "172.25.241.0/24", "refId": "management-ref", "status": "ACTIVE"})
			}
			writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]any{"total": len(items), "offset": 0}, "data": items})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/firewall/policy"):
			var body struct {
				Comment     string   `json:"comment"`
				Destination []string `json:"dstNetwork"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Destination) != 1 || body.Destination[0] != "management-tier" {
				t.Fatal("SSH policy lost the actual NSM Tier ID")
			}
			comment = body.Comment
			writeJSON(t, w, 202, map[string]any{"httpStatus": 202, "jobId": "policy-job"})
		case strings.HasSuffix(r.URL.Path, "/job/status/policy-job"):
			writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "jobId": "policy-job", "jobStatus": "SUCCESS"})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/firewall/policy"):
			items := []any{}
			if comment != "" {
				items = append(items, map[string]any{"policyId": "policy-id", "comment": comment, "action": "accept", "status": "enable", "srcInterface": []any{map[string]any{"networkId": "connector-tier"}}, "dstInterface": []any{map[string]any{"networkId": "management-tier"}}, "srcAddress": []any{map[string]any{"name": "172.25.0.11/32"}}, "dstAddress": []any{map[string]any{"name": "all"}}, "services": []any{map[string]any{"protocol": "TCP", "startPort": "22", "endPort": "22"}}, "nat": "disable"})
			}
			writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]any{"total": len(items), "offset": 0}, "data": items})
		default:
			t.Fatalf("unexpected management request: %s %s", r.Method, r.URL.Path)
		}
	})
	nics, resources, err := a.prepareKTCloudNICs(context.Background(), request, vm, a.provision, provisionTopology{tier: ktCloudTier{ID: "lab-tier", RefID: "lab-ref"}}, nil)
	if err != nil || len(resources) != 2 || len(nics.networkIDs) != 2 || nics.networkIDs[0] != "management-ref" || name != ktCloudManagementTierName(request.LabInstanceID, 1, vm.VMKey) {
		t.Fatalf("Management creation mapping failed: %v resources=%d", err, len(resources))
	}
	if resources[0].LogicalName != vm.VMKey+":management-tier" || resources[1].LogicalName != vm.VMKey+":management-ssh" {
		t.Fatal("short provider names changed logical resource identity")
	}
}

func TestKTCloudShortAndLegacyDiscoveryRemainAdvisory(t *testing.T) {
	for _, source := range []string{"none", "server", "policy"} {
		t.Run(source, func(t *testing.T) {
			lab, key := "long-lab-identifier", "Workspace/"+strings.Repeat("a", 80)
			generation := int64(1)
			short := ktCloudManagementTierName(lab, generation, key)
			legacy := provisionBaseName(lab, generation) + "-" + safeName(key, 28) + "-management-tier"
			deletes := 0
			a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					deletes++
					t.Fatal("discovery mutated provider resources")
				}
				switch {
				case strings.HasSuffix(r.URL.Path, "/servers/detail"):
					items := []any{}
					if source == "server" {
						items = append(items, map[string]any{"id": "server", "metadata": map[string]string{"labbit_lab_instance_id": lab, "labbit_generation": "1", "labbit_vm_key": key}})
					}
					writeJSON(t, w, 200, map[string]any{"servers": items})
				case strings.HasSuffix(r.URL.Path, "/network"):
					items := []any{}
					for id, name := range map[string]string{"short": short, "legacy": legacy, "lab": ktCloudLabTierName(lab, generation), "other": ktCloudManagementTierName(lab, 2, key), "shared": short, "malformed": ktCloudTierBaseName(lab, 1) + "-m-not-a-hash"} {
						items = append(items, map[string]any{"networkId": id, "networkName": name, "status": "ACTIVE", "shared": id == "shared"})
					}
					writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]any{"total": len(items), "offset": 0}, "data": items})
				case strings.HasSuffix(r.URL.Path, "/firewall/policy"):
					items := []any{}
					if source == "policy" {
						items = append(items, map[string]any{"policyId": "policy", "comment": provisionBaseName(lab, 1) + ":" + key + ":management-ssh"})
					}
					writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]any{"total": len(items), "offset": 0}, "data": items})
				default:
					t.Fatalf("unexpected lookup %s", r.URL.Path)
				}
			})
			correlation := coreprovider.Correlation{OperationID: "reconcile", LabInstanceID: lab, Generation: generation}
			result, err := a.Reconcile(context.Background(), coreprovider.ReconcileRequest{Correlation: correlation, KnownResources: []coreprovider.ResourceRef{}, DiscoverCandidates: true})
			if err != nil {
				t.Fatal(err)
			}
			found := map[string]string{}
			for _, item := range result.Observations {
				if item.Source != coreprovider.SourceDiscoveredCandidate {
					t.Fatal("candidate established ownership")
				}
				if item.ResourceType == coreprovider.ResourceTypeTier {
					found[item.ProviderID] = item.LogicalName
				}
			}
			want := ""
			if source != "none" {
				want = key + ":management-tier"
			}
			if len(found) != 3 || found["short"] != want || found["legacy"] != want || found["lab"] != "lab-tier" {
				t.Fatalf("candidate labels: %#v", found)
			}
			cleanup, err := a.Cleanup(context.Background(), coreprovider.CleanupRequest{Correlation: correlation, ProviderResources: []coreprovider.ResourceRef{}})
			if err != nil || cleanup.Outcome != coreprovider.OutcomeSucceeded || deletes != 0 {
				t.Fatal("advisory candidate entered cleanup")
			}
		})
	}
}

func TestKTCloudTerminalResolvesShortAndLegacyTierNames(t *testing.T) {
	for _, mode := range []string{"short", "legacy", "ambiguous", "other-generation", "shared"} {
		t.Run(mode, func(t *testing.T) {
			lab, key := "lbt145-control-189782903", "Workspace/"+strings.Repeat("a", 80)
			short := ktCloudManagementTierName(lab, 1, key)
			legacy := provisionBaseName(lab, 1) + "-" + safeName(key, 28) + "-management-tier"
			a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/servers/server"):
					writeJSON(t, w, 200, map[string]any{"server": map[string]any{"id": "server", "tenant_id": "fixture-project", "status": "ACTIVE", "name": provisionBaseName(lab, 1) + "-" + safeName(key, 28), "metadata": map[string]string{"labbit_lab_instance_id": lab, "labbit_generation": "1", "labbit_operation_id": "provision", "labbit_vm_key": key}, "addresses": map[string]any{short: []any{map[string]any{"addr": "172.25.241.6", "OS-EXT-IPS:type": "fixed"}}, legacy: []any{map[string]any{"addr": "172.25.241.6", "OS-EXT-IPS:type": "fixed"}}}}})
				case strings.HasSuffix(r.URL.Path, "/network"):
					names := []string{short}
					if mode == "legacy" {
						names = []string{legacy}
					}
					if mode == "ambiguous" {
						names = append(names, legacy)
					}
					if mode == "other-generation" {
						names = []string{ktCloudManagementTierName(lab, 2, key)}
					}
					items := []any{}
					for i, name := range names {
						items = append(items, map[string]any{"networkId": fmt.Sprintf("tier-%d", i), "networkName": name, "cidr": "172.25.241.0/24", "refId": "physical-ref", "status": "ACTIVE", "shared": mode == "shared"})
					}
					writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]any{"total": len(items), "offset": 0}, "data": items})
				default:
					t.Fatalf("unexpected request %s", r.URL.Path)
				}
			})
			ip, err := a.ResolveServerAddress(context.Background(), key, "server")
			if mode == "short" || mode == "legacy" {
				if err != nil || ip != "172.25.241.6" {
					t.Fatalf("management resolution: %s %v", ip, err)
				}
			} else if !errors.Is(err, ErrManagementIP) {
				t.Fatalf("unsafe resolution permitted: %s %v", ip, err)
			}
		})
	}
}
