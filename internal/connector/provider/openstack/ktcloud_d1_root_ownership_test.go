package openstackprovider

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

func TestKTCloudRootOwnershipRequiresVerifiedMappingBeforeCleanupReference(t *testing.T) {
	for _, scenario := range []string{"valid", "additional attachment", "no attachment", "empty ID", "wrong image", "wrong size", "wrong server", "multiple owners", "volume ID mismatch", "lookup unavailable", "management lookup unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			mutations := 0
			a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					mutations++
					t.Error("mapping observation mutated cloud resources")
					w.WriteHeader(500)
					return
				}
				switch {
				case strings.HasSuffix(r.URL.Path, "/servers/owned-server"):
					attached := []any{map[string]any{"id": "root-id"}}
					switch scenario {
					case "additional attachment":
						attached = append(attached, map[string]any{"id": "foreign-volume"})
					case "no attachment":
						attached = []any{}
					case "empty ID":
						attached = []any{map[string]any{"id": ""}}
					}
					writeJSON(t, w, 200, map[string]any{"server": map[string]any{"id": "owned-server", "status": "ACTIVE", "os-extended-volumes:volumes_attached": attached, "addresses": map[string]any{"management": []any{map[string]any{"addr": "172.25.241.10", "OS-EXT-IPS:type": "fixed"}}}}})
				case strings.HasSuffix(r.URL.Path, "/volumes/root-id"):
					if scenario == "lookup unavailable" {
						w.WriteHeader(503)
						return
					}
					root := map[string]any{"id": "root-id", "size": 50, "volume_image_metadata": map[string]string{"image_id": "expected-image"}, "attachments": []any{map[string]any{"server_id": "owned-server"}}}
					switch scenario {
					case "wrong image":
						root["volume_image_metadata"] = map[string]string{"image_id": "other-image"}
					case "wrong size":
						root["size"] = 40
					case "wrong server":
						root["attachments"] = []any{map[string]any{"server_id": "other-server"}}
					case "multiple owners":
						root["attachments"] = []any{map[string]any{"server_id": "owned-server"}, map[string]any{"server_id": "other-server"}}
					case "volume ID mismatch":
						root["id"] = "foreign-volume"
					}
					writeJSON(t, w, 200, map[string]any{"volume": root})
				case strings.HasSuffix(r.URL.Path, "/nsm/network"):
					if scenario == "management lookup unavailable" {
						w.WriteHeader(503)
						return
					}
					writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]any{"total": 1, "offset": 0}, "data": []any{map[string]any{"networkId": "management-id", "refId": "management-ref", "networkName": "management", "cidr": "172.25.241.0/24", "status": "ACTIVE"}}})
				default:
					t.Errorf("unexpected mapping lookup: %s", r.URL.Path)
					w.WriteHeader(500)
				}
			})
			request := coreprovider.ProvisionRequest{Correlation: coreprovider.Correlation{Generation: 1}}
			vm := coreprovider.VMSpec{VMKey: "workspace", ImageID: "expected-image"}
			nics := provisionNICs{managementTier: ktCloudTier{ID: "management-id", RefID: "management-ref", Name: "management", CIDR: "172.25.241.0/24"}}
			prior := []coreprovider.ResourceResult{ktResource(resourceIdentity(1, "workspace"), coreprovider.ResourceTypeServer, "owned-server", "ACTIVE"), ktResource(resourceIdentity(1, "previous-root"), coreprovider.ResourceTypeVolume, "previously-known-root", "PRESENT")}
			address, resources, err := a.refreshKTCloudNICs(context.Background(), request, vm, nics, "owned-server", append([]coreprovider.ResourceResult{}, prior...))
			verified := scenario == "valid" || scenario == "management lookup unavailable"
			if (err == nil) != (scenario == "valid") || (scenario == "valid" && address != "172.25.241.10") {
				t.Fatalf("mapping outcome: address=%s err=%v", address, err)
			}
			if !verified && !reflect.DeepEqual(resources, prior) {
				t.Fatal("unverified attachment entered owned cleanup references or prior refs were lost")
			}
			if verified {
				if len(resources) != len(prior)+1 || resources[len(prior)].ProviderID != "root-id" || resources[len(prior)].ResourceType != coreprovider.ResourceTypeVolume {
					t.Fatal("verified root receipt not preserved")
				}
			}
			for _, ref := range resources {
				if ref.ProviderID == "foreign-volume" {
					t.Fatal("foreign volume became a cleanup target")
				}
			}
			if mutations != 0 {
				t.Fatal("mapping validation performed deletion")
			}
		})
	}
}
