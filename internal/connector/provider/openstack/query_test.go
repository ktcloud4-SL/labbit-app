package openstackprovider

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestAdapterListsAndNormalizesM1Resources(t *testing.T) {
	adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Auth-Token") != "test-token" {
			t.Errorf("missing auth token for %s", request.URL.Path)
		}
		switch request.URL.Path {
		case "/image/v2/images":
			writeJSON(t, response, http.StatusOK, map[string]any{
				"images": []map[string]any{
					{"id": "image-z", "name": "Zulu", "status": "queued"},
					{"id": "image-a", "name": "alpha", "status": "active"},
				},
				"next": "",
			})
		case "/compute/v2/flavors/detail":
			writeJSON(t, response, http.StatusOK, map[string]any{
				"flavors": []map[string]any{
					{"id": "flavor-2", "name": "medium", "vcpus": 2, "ram": 4096, "disk": 20},
					{"id": "flavor-1", "name": "Small", "vcpus": 1, "ram": 2048, "disk": 10},
				},
				"flavors_links": []any{},
			})
		case "/compute/v2/servers/detail":
			writeJSON(t, response, http.StatusOK, map[string]any{
				"servers": []map[string]any{
					{"id": "server-2", "name": "worker", "status": "build"},
					{"id": "server-1", "name": "Control", "status": "ACTIVE"},
				},
				"servers_links": []any{},
			})
		case "/network/v2.0/networks":
			writeJSON(t, response, http.StatusOK, map[string]any{
				"networks": []map[string]any{
					{"id": "network-2", "name": "lab-z", "status": "down", "shared": false, "subnets": []string{"subnet-z"}},
					{"id": "network-1", "name": "Lab-A", "status": "ACTIVE", "shared": true, "subnets": []string{"subnet-b", "subnet-a"}},
				},
				"networks_links": []any{},
			})
		case "/network/v2.0/subnets":
			writeJSON(t, response, http.StatusOK, map[string]any{
				"subnets": []map[string]any{
					{"id": "subnet-1", "name": "Lab-A-subnet", "network_id": "network-1", "cidr": "10.0.0.0/24", "gateway_ip": "10.0.0.1", "enable_dhcp": true},
				},
				"subnets_links": []any{},
			})
		case "/network/v2.0/security-groups":
			writeJSON(t, response, http.StatusOK, map[string]any{
				"security_groups": []map[string]any{
					{"id": "sg-1", "name": "lab-sg", "description": "Lab ingress", "security_group_rules": []any{}},
				},
				"security_groups_links": []any{},
			})
		default:
			http.NotFound(response, request)
		}
	}))

	images, err := adapter.ListImages(context.Background())
	if err != nil {
		t.Fatalf("ListImages() error = %v", err)
	}
	if len(images) != 2 || images[0].ID != "image-a" || images[0].Status != "ACTIVE" || images[1].Status != "QUEUED" {
		t.Fatalf("unexpected images: %+v", images)
	}

	flavors, err := adapter.ListFlavors(context.Background())
	if err != nil {
		t.Fatalf("ListFlavors() error = %v", err)
	}
	if len(flavors) != 2 || flavors[0].ID != "flavor-2" || flavors[0].VCPUs != 2 || flavors[1].RAMMiB != 2048 {
		t.Fatalf("unexpected flavors: %+v", flavors)
	}

	servers, err := adapter.ListServers(context.Background())
	if err != nil {
		t.Fatalf("ListServers() error = %v", err)
	}
	if len(servers) != 2 || servers[0].ID != "server-1" || servers[1].Status != "BUILD" {
		t.Fatalf("unexpected servers: %+v", servers)
	}

	networks, err := adapter.ListNetworks(context.Background())
	if err != nil {
		t.Fatalf("ListNetworks() error = %v", err)
	}
	if len(networks) != 2 || networks[0].ID != "network-1" || networks[0].SubnetIDs[0] != "subnet-a" || networks[1].Status != "DOWN" {
		t.Fatalf("unexpected networks: %+v", networks)
	}

	subnets, err := adapter.ListSubnets(context.Background())
	if err != nil || len(subnets) != 1 || !subnets[0].EnableDHCP || subnets[0].CIDR != "10.0.0.0/24" {
		t.Fatalf("unexpected subnets: %+v, error = %v", subnets, err)
	}

	securityGroups, err := adapter.ListSecurityGroups(context.Background())
	if err != nil || len(securityGroups) != 1 || securityGroups[0].ID != "sg-1" {
		t.Fatalf("unexpected security groups: %+v, error = %v", securityGroups, err)
	}
}

func TestListErrorsDoNotExposeProviderResponse(t *testing.T) {
	adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		writeJSON(t, response, http.StatusInternalServerError, map[string]string{
			"error": "provider response contains raw-secret",
		})
	}))

	_, err := adapter.ListImages(context.Background())
	if !errors.Is(err, ErrImageList) {
		t.Fatalf("error = %v, want ErrImageList", err)
	}
	if strings.Contains(err.Error(), "raw-secret") {
		t.Fatalf("provider response leaked through error: %v", err)
	}
}
