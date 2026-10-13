package openstackprovider

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

var ktManagementSharingCases = []struct {
	name    string
	present bool
	value   any
}{
	{"false", true, false},
	{"true", true, true},
	{"missing", false, nil},
	{"null", true, nil},
}

func TestKTCloudTerminalRequiresExplicitNonSharedManagementTier(t *testing.T) {
	for _, naming := range []string{"short", "legacy"} {
		for _, tc := range ktManagementSharingCases {
			t.Run(naming+"/"+tc.name, func(t *testing.T) {
				lab, key := "lab", "workspace"
				name := ktCloudManagementTierName(lab, 1, key)
				if naming == "legacy" {
					name = provisionBaseName(lab, 1) + "-" + key + "-management-tier"
				}
				tier := map[string]any{"networkId": "management-id", "refId": "management-ref", "networkName": name, "cidr": "172.25.241.0/24", "status": "ACTIVE"}
				if tc.present {
					tier["shared"] = tc.value
				}
				tierReads := 0
				a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						t.Error("terminal target resolution mutated resources")
						w.WriteHeader(500)
						return
					}
					switch r.URL.Path {
					case "/compute/v2/servers/owned-server":
						writeJSON(t, w, 200, map[string]any{"server": map[string]any{"id": "owned-server", "tenant_id": "fixture-project", "status": "ACTIVE", "name": provisionBaseName(lab, 1) + "-" + key, "metadata": map[string]string{"labbit_lab_instance_id": lab, "labbit_generation": "1", "labbit_operation_id": "provision", "labbit_vm_key": key}, "addresses": map[string]any{name: []any{map[string]any{"addr": "172.25.241.10", "OS-EXT-IPS:type": "fixed"}}}}})
					case "/compute/v2/nsm/network":
						tierReads++
						writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]any{"total": 1, "offset": 0}, "data": []any{tier}})
					default:
						t.Errorf("unexpected target lookup: %s", r.URL.Path)
						w.WriteHeader(500)
					}
				})
				address, err := a.ResolveServerAddress(context.Background(), key, "owned-server")
				if tc.name == "false" {
					if err != nil || address != "172.25.241.10" {
						t.Fatalf("explicit non-shared Tier rejected: address=%q err=%v", address, err)
					}
				} else if !errors.Is(err, ErrManagementIP) || address != "" {
					t.Fatalf("unverified Management Tier accepted: address=%q err=%v", address, err)
				}
				if tierReads != 1 {
					t.Fatalf("expected a current Tier observation, got %d", tierReads)
				}
			})
		}
	}
}

func TestKTCloudRefreshRequiresExplicitNonSharedManagementTierAndPreservesResources(t *testing.T) {
	for _, tc := range ktManagementSharingCases {
		t.Run(tc.name, func(t *testing.T) {
			name := ktCloudManagementTierName("lab", 1, "workspace")
			tier := map[string]any{"networkId": "management-id", "refId": "management-ref", "networkName": name, "cidr": "172.25.241.0/24", "status": "ACTIVE"}
			if tc.present {
				tier["shared"] = tc.value
			}
			tierReads := 0
			a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("post-create mapping validation mutated resources")
					w.WriteHeader(500)
					return
				}
				switch r.URL.Path {
				case "/compute/v2/servers/owned-server":
					writeJSON(t, w, 200, map[string]any{"server": map[string]any{"id": "owned-server", "status": "ACTIVE", "os-extended-volumes:volumes_attached": []any{map[string]any{"id": "root-id"}}, "addresses": map[string]any{name: []any{map[string]any{"addr": "172.25.241.10", "OS-EXT-IPS:type": "fixed"}}}}})
				case "/compute/v2/volume/volumes/root-id":
					writeJSON(t, w, 200, map[string]any{"volume": map[string]any{"id": "root-id", "size": 50, "volume_image_metadata": map[string]string{"image_id": "expected-image"}, "attachments": []any{map[string]any{"server_id": "owned-server"}}}})
				case "/compute/v2/nsm/network":
					tierReads++
					writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]any{"total": 1, "offset": 0}, "data": []any{tier}})
				default:
					t.Errorf("unexpected mapping lookup: %s", r.URL.Path)
					w.WriteHeader(500)
				}
			})
			request := coreprovider.ProvisionRequest{Correlation: coreprovider.Correlation{LabInstanceID: "lab", Generation: 1}}
			vm := coreprovider.VMSpec{VMKey: "workspace", ImageID: "expected-image"}
			nics := provisionNICs{managementTier: ktCloudTier{ID: "management-id", RefID: "management-ref", Name: name, CIDR: "172.25.241.0/24", sharedKnown: true, Shared: false}}
			prior := []coreprovider.ResourceResult{
				ktResource(resourceIdentity(1, "lab-tier"), coreprovider.ResourceTypeTier, "lab-tier-id", "ACTIVE"),
				ktResource(resourceIdentity(1, "workspace:management-tier"), coreprovider.ResourceTypeTier, "management-id", "ACTIVE"),
				ktResource(resourceIdentity(1, "workspace:management-ssh"), coreprovider.ResourceTypeFirewall, "policy-id", "ACTIVE"),
				ktResource(resourceIdentity(1, "workspace"), coreprovider.ResourceTypeServer, "owned-server", "ACTIVE"),
				ktResource(resourceIdentity(1, "previous-root"), coreprovider.ResourceTypeVolume, "previously-known-root", "PRESENT"),
			}
			address, resources, err := a.refreshKTCloudNICs(context.Background(), request, vm, nics, "owned-server", append([]coreprovider.ResourceResult{}, prior...))
			if tc.name == "false" {
				if err != nil || address != "172.25.241.10" {
					t.Fatalf("explicit non-shared Tier rejected: address=%q err=%v", address, err)
				}
			} else if !errors.Is(err, ErrManagementIP) || address != "" {
				t.Fatalf("unverified Management Tier accepted: address=%q err=%v", address, err)
			}
			want := append(append([]coreprovider.ResourceResult{}, prior...), ktResource(resourceIdentity(1, "workspace:root-volume"), coreprovider.ResourceTypeVolume, "root-id", "PRESENT"))
			if !reflect.DeepEqual(resources, want) {
				t.Fatalf("confirmed resources lost or changed: got=%#v want=%#v", resources, want)
			}
			if tierReads != 1 {
				t.Fatalf("expected a fresh Tier observation despite prior non-shared state, got %d", tierReads)
			}
		})
	}
}
