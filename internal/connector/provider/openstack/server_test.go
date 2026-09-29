package openstackprovider

import (
	"context"
	"net/http"
	"testing"
)

func TestEnsureServerReusesMatchingServerAndPorts(t *testing.T) {
	posts := 0
	adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/compute/v2/servers/detail":
			writeJSON(t, response, http.StatusOK, map[string]any{"servers": []map[string]any{{"id": "server-1", "name": "lab-server", "status": "ACTIVE"}}, "servers_links": []any{}})
		case request.Method == http.MethodGet && request.URL.Path == "/compute/v2/servers/server-1":
			writeJSON(t, response, http.StatusOK, map[string]any{"server": map[string]any{
				"id": "server-1", "name": "lab-server", "status": "ACTIVE", "key_name": "openstack2",
				"image": map[string]string{"id": "image-1"}, "flavor": map[string]string{"id": "flavor-1"},
				"metadata": map[string]string{"labbit_vm_key": "workspace"},
			}})
		case request.Method == http.MethodGet && request.URL.Path == "/network/v2.0/ports":
			if request.URL.Query().Get("device_id") != "server-1" {
				t.Fatalf("device_id = %q", request.URL.Query().Get("device_id"))
			}
			writeJSON(t, response, http.StatusOK, map[string]any{"ports": []map[string]any{{"id": "port-management"}, {"id": "port-lab"}}, "ports_links": []any{}})
		case request.Method == http.MethodPost:
			posts++
			http.Error(response, "unexpected create", http.StatusInternalServerError)
		default:
			http.NotFound(response, request)
		}
	}))

	resource, server, err := adapter.EnsureServer(context.Background(), ResourceIdentity{Generation: 1, LogicalName: "workspace"}, ServerSpec{
		Name:     "lab-server",
		ImageID:  "image-1",
		FlavorID: "flavor-1",
		KeyPair:  "openstack2",
		PortIDs:  []string{"port-management", "port-lab"},
		Metadata: map[string]string{"labbit_vm_key": "workspace"},
	})
	if err != nil {
		t.Fatalf("EnsureServer() error = %v", err)
	}
	if posts != 0 || server.ID != "server-1" || resource.ProviderID != "server-1" || resource.ObservedState != "ACTIVE" {
		t.Fatalf("server = %+v, resource = %+v, posts = %d", server, resource, posts)
	}
}
