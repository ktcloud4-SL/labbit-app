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

func TestInspectionNeverAdoptsCandidateOrForeignRoot(t *testing.T) {
	oldState, oldCompute, oldVolume, oldNSM, oldPath := state, compute, volume, nsm, ledgerPath
	t.Cleanup(func() { state, compute, volume, nsm, ledgerPath = oldState, oldCompute, oldVolume, oldNSM, oldPath })
	for _, mode := range []string{"lostResponse", "sameNameForeign", "wrongProject", "wrongName", "wrongMetadata", "wrongKey"} {
		t.Run(mode, func(t *testing.T) {
			state = ledger{Name: "fixture", Project: "project", KeyName: "key", Image: "image", ServerID: "known", PolicyIDs: []string{"owned-policy"}}
			ledgerPath = filepath.Join(t.TempDir(), "ledger.json")
			if mode == "lostResponse" {
				state.ServerID = ""
			}
			mutations := 0
			known := map[string]any{"id": "known", "status": "ACTIVE", "name": "fixture", "tenant_id": "project", "key_name": "key", "metadata": map[string]string{"labbit_fixture": "fixture", "labbit_test": "LBT-145"}, "os-extended-volumes:volumes_attached": []any{map[string]string{"id": "owned-root"}}}
			if mode == "wrongProject" {
				known["tenant_id"] = "foreign"
			}
			if mode == "wrongName" {
				known["name"] = "foreign"
			}
			if mode == "wrongMetadata" {
				known["metadata"] = map[string]string{"labbit_fixture": "foreign", "labbit_test": "LBT-145"}
			}
			if mode == "wrongKey" {
				known["key_name"] = "foreign"
			}
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method != "GET" {
					mutations++
					w.WriteHeader(500)
					return
				}
				switch r.URL.Path {
				case "/servers", "/servers/detail":
					json.NewEncoder(w).Encode(map[string]any{"servers": []any{known, map[string]any{"id": "foreign", "name": "fixture", "tenant_id": "project"}}})
				case "/servers/known":
					json.NewEncoder(w).Encode(map[string]any{"server": known})
				case "/volumes/owned-root":
					json.NewEncoder(w).Encode(map[string]any{"volume": map[string]any{"id": "owned-root", "size": 50, "volume_image_metadata": map[string]string{"image_id": "image"}, "attachments": []any{map[string]string{"server_id": "known"}}}})
				case "/volumes/detail":
					json.NewEncoder(w).Encode(map[string]any{"volumes": []any{map[string]any{"id": "foreign-root", "attachments": []any{map[string]string{"server_id": "foreign"}}}}})
				default:
					t.Errorf("unexpected ownership fixture request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer api.Close()
			client := &gophercloud.ServiceClient{ProviderClient: &gophercloud.ProviderClient{}, Endpoint: api.URL + "/"}
			compute, volume, nsm = client, client, client
			if mode == "sameNameForeign" {
				if e := inspect(context.Background()); e != nil {
					t.Fatal(e)
				}
				if len(state.VolumeIDs) != 1 || state.VolumeIDs[0] != "owned-root" {
					t.Fatal("foreign volume adopted")
				}
			} else {
				if cleanup(context.Background()) == nil || mutations != 0 {
					t.Fatal("uncertain ownership allowed mutation")
				}
			}
			if mode == "lostResponse" && state.ServerID != "" {
				t.Fatal("name candidate adopted")
			}
		})
	}
}
