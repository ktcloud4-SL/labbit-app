package openstackprovider

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestEnsureOwnedResourcesRejectNameAndShapeMatchesWithoutProviderID(t *testing.T) {
	t.Run("security group", func(t *testing.T) {
		adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			writeJSON(t, response, http.StatusOK, map[string]any{"security_groups": []any{map[string]any{
				"id": "foreign-sg", "name": "lab-sg", "stateful": true, "security_group_rules": []any{},
			}}, "security_groups_links": []any{}})
		}))
		resource, err := adapter.EnsureSecurityGroup(context.Background(), ResourceIdentity{Generation: 1, LogicalName: "lab-security-group"}, SecurityGroupSpec{Name: "lab-sg"})
		if !errors.Is(err, ErrResourceOwnership) || resource.ProviderID != "" {
			t.Fatalf("EnsureSecurityGroup() = %+v, %v", resource, err)
		}
	})

	t.Run("port", func(t *testing.T) {
		adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			writeJSON(t, response, http.StatusOK, map[string]any{"ports": []any{map[string]any{
				"id": "foreign-port", "name": "lab-port", "network_id": "network-1", "status": "DOWN",
				"fixed_ips":       []any{map[string]any{"subnet_id": "subnet-1", "ip_address": "198.19.0.10"}},
				"security_groups": []string{"sg-1"},
			}}, "ports_links": []any{}})
		}))
		resource, port, err := adapter.EnsurePort(context.Background(), ResourceIdentity{Generation: 1, LogicalName: "workspace:lab"}, PortSpec{
			Name: "lab-port", NetworkID: "network-1", SubnetID: "subnet-1", SecurityGroupIDs: []string{"sg-1"},
		})
		if !errors.Is(err, ErrResourceOwnership) || resource.ProviderID != "" || port.ID != "" {
			t.Fatalf("EnsurePort() = %+v, %+v, %v", resource, port, err)
		}
	})

	t.Run("router", func(t *testing.T) {
		calls := 0
		adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			calls++
			if request.URL.Path != "/network/v2.0/routers" || request.Method != http.MethodGet {
				t.Fatalf("existing router must be rejected before another call: %s %s", request.Method, request.URL.String())
			}
			writeJSON(t, response, http.StatusOK, map[string]any{"routers": []any{map[string]any{
				"id": "foreign-router", "name": "lab-router", "status": "ACTIVE",
				"external_gateway_info": map[string]any{"network_id": "external-network"},
			}}, "routers_links": []any{}})
		}))
		resource, err := adapter.EnsureRouter(context.Background(), ResourceIdentity{Generation: 1, LogicalName: "lab-router"}, RouterSpec{
			Name: "lab-router", ExternalNetworkID: "external-network", SubnetID: "subnet-1",
		})
		if !errors.Is(err, ErrResourceOwnership) || resource.ProviderID != "" || calls != 1 {
			t.Fatalf("EnsureRouter() = %+v, %v, calls=%d", resource, err, calls)
		}
	})
}
