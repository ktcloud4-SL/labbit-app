package openstackprovider

import (
	"context"
	"net/http"
	"testing"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

func TestQuotaRequiredForTwoVMOutboundTopology(t *testing.T) {
	snapshot := validSnapshot()
	snapshot.VMs = append(snapshot.VMs, coreprovider.VMSpec{
		VMKey: "worker", Role: "WORKER", InstanceIndex: 1, ImageID: "image-ubuntu", FlavorID: "flavor-small",
		FlavorSpec: coreprovider.FlavorSpec{VCPUs: 2, RAMMiB: 4096, DiskGiB: 20},
	})
	required := quotaRequired(snapshot)
	if required.instances != 2 || required.cores != 3 || required.ramMiB != 6144 || required.networks != 1 || required.subnets != 1 || required.ports != 6 || required.routers != 1 || required.securityGroups != 1 || required.securityGroupRules != 3 {
		t.Fatalf("unexpected quota requirement: %+v", required)
	}
}

func TestQuotaCreditForResetUsesOnlyObservedResourcesAndActualServerFlavor(t *testing.T) {
	resources := []coreprovider.ResourceRef{
		{ResourceType: coreprovider.ResourceTypeServer, ProviderID: "server", LogicalName: "workspace", Generation: 1},
		{ResourceType: coreprovider.ResourceTypeNetwork, ProviderID: "network", Generation: 1},
		{ResourceType: coreprovider.ResourceTypeRouter, ProviderID: "router", Generation: 1},
		{ResourceType: coreprovider.ResourceTypeSecurityGroup, ProviderID: "lab-sg", Generation: 1},
		{ResourceType: coreprovider.ResourceTypeSecurityRule, ProviderID: "lab-rule", Generation: 1},
	}
	adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/compute/v2/servers/server":
			writeJSON(t, response, http.StatusOK, map[string]any{"server": map[string]any{
				"id": "server", "status": "ACTIVE", "flavor": map[string]any{"id": "actual-small"},
			}})
		case "/compute/v2/flavors/actual-small":
			writeJSON(t, response, http.StatusOK, map[string]any{"flavor": map[string]any{
				"id": "actual-small", "vcpus": 1, "ram": 2048, "disk": 20,
			}})
		case "/network/v2.0/networks/network":
			http.NotFound(response, request)
		case "/network/v2.0/routers/router":
			writeJSON(t, response, http.StatusOK, map[string]any{"router": map[string]any{"id": "router", "status": "ACTIVE"}})
		case "/network/v2.0/ports":
			if request.URL.Query().Get("device_id") != "router" {
				t.Fatalf("router port query = %q", request.URL.RawQuery)
			}
			writeJSON(t, response, http.StatusOK, map[string]any{"ports": []any{map[string]any{"id": "router-interface"}}, "ports_links": []any{}})
		case "/network/v2.0/security-groups/lab-sg":
			writeJSON(t, response, http.StatusOK, map[string]any{"security_group": map[string]any{
				"id": "lab-sg", "security_group_rules": []any{
					map[string]any{"id": "default-egress", "security_group_id": "lab-sg"},
					map[string]any{"id": "lab-rule", "security_group_id": "lab-sg"},
				},
			}})
		case "/network/v2.0/security-group-rules/lab-rule":
			writeJSON(t, response, http.StatusOK, map[string]any{"security_group_rule": map[string]any{"id": "lab-rule", "security_group_id": "lab-sg"}})
		default:
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.String())
		}
	}))
	credit, err := adapter.quotaCreditForExistingReset(context.Background(), resources)
	if err != nil {
		t.Fatalf("quotaCreditForExistingReset() error = %v", err)
	}
	if credit.instances != 1 || credit.cores != 1 || credit.ramMiB != 2048 || credit.networks != 0 || credit.subnets != 0 || credit.ports != 1 || credit.routers != 1 || credit.securityGroups != 1 || credit.securityGroupRules != 2 {
		t.Fatalf("unexpected quota credit: %+v", credit)
	}
}

func TestQuotaAvailableAccountsForReservedAndResetCredit(t *testing.T) {
	if quotaAvailable(10, 8, 1, 2, 0) {
		t.Fatal("quota with only one free slot accepted a requirement of two")
	}
	if !quotaAvailable(10, 8, 1, 2, 1) {
		t.Fatal("reset credit was not applied")
	}
	if !quotaAvailable(-1, 100, 100, 100, 0) {
		t.Fatal("unlimited quota was rejected")
	}
}
