package openstackprovider

import (
	"context"
	"net/http"
	"strings"
	"testing"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

func TestReconcileObservesKnownPresentAndConfirmedAbsentResources(t *testing.T) {
	adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/compute/v2/servers/server-1":
			writeJSON(t, response, http.StatusOK, map[string]any{"server": map[string]any{"id": "server-1", "status": "ACTIVE"}})
		case "/network/v2.0/networks/network-missing":
			http.NotFound(response, request)
		default:
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
	}))

	result, err := adapter.Reconcile(context.Background(), coreprovider.ReconcileRequest{
		Correlation: coreprovider.Correlation{OperationID: "reconcile-1", LabInstanceID: "lab-1", Generation: 1},
		KnownResources: []coreprovider.ResourceRef{
			{ResourceType: coreprovider.ResourceTypeServer, ProviderID: "server-1", Generation: 1, LogicalName: "workspace"},
			{ResourceType: coreprovider.ResourceTypeNetwork, ProviderID: "network-missing", Generation: 1, LogicalName: "lab-network"},
		},
		DiscoverCandidates: false,
	})
	if err != nil || result.Error != nil || len(result.Observations) != 2 {
		t.Fatalf("Reconcile() = %+v, %v", result, err)
	}
	if !result.Observations[0].Exists || result.Observations[0].ObservedState != "ACTIVE" || result.Observations[0].Source != coreprovider.SourceKnownResource {
		t.Fatalf("present observation = %+v", result.Observations[0])
	}
	if result.Observations[1].Exists || result.Observations[1].ObservedState != stateAbsent || result.Observations[1].Source != coreprovider.SourceKnownResource {
		t.Fatalf("absent observation = %+v", result.Observations[1])
	}
}

func TestDispatchReconcileDoesNotTurnLookupFailureIntoAbsence(t *testing.T) {
	adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		http.Error(response, "raw-provider-secret", http.StatusServiceUnavailable)
	}))

	result, err := coreprovider.DispatchReconcile(context.Background(), adapter, coreprovider.ReconcileRequest{
		Correlation:    coreprovider.Correlation{OperationID: "reconcile-2", LabInstanceID: "lab-2", Generation: 1},
		KnownResources: []coreprovider.ResourceRef{{ResourceType: coreprovider.ResourceTypeServer, ProviderID: "server-2", Generation: 1}},
	})
	if err != nil || len(result.Observations) != 0 || result.Error == nil || result.Error.Code != "PROVIDER_RECONCILE_UNAVAILABLE" {
		t.Fatalf("DispatchReconcile() = %+v, %v", result, err)
	}
	if strings.Contains(strings.ToLower(result.Error.Message), "secret") {
		t.Fatalf("Provider response leaked: %+v", result.Error)
	}
}

func TestReconcileDiscoversGenerationCandidatesWithoutClaimingOwnership(t *testing.T) {
	labID := "candidate-lab"
	generation := int64(3)
	baseName := provisionBaseName(labID, generation)
	adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/compute/v2/servers/detail":
			writeJSON(t, response, http.StatusOK, map[string]any{"servers": []any{
				map[string]any{"id": "server-candidate", "name": baseName + "-workspace", "status": "ACTIVE", "metadata": map[string]string{"labbit_lab_instance_id": labID, "labbit_generation": "3", "labbit_vm_key": "workspace"}},
				map[string]any{"id": "server-other", "name": "unrelated", "status": "ACTIVE", "metadata": map[string]string{"labbit_lab_instance_id": "other", "labbit_generation": "3"}},
			}, "servers_links": []any{}})
		case "/network/v2.0/networks":
			writeJSON(t, response, http.StatusOK, map[string]any{"networks": []any{map[string]any{"id": "network-candidate", "name": baseName + "-network", "status": "ACTIVE"}}, "networks_links": []any{}})
		case "/network/v2.0/subnets":
			writeJSON(t, response, http.StatusOK, map[string]any{"subnets": []any{map[string]any{"id": "subnet-candidate", "name": baseName + "-subnet"}}, "subnets_links": []any{}})
		case "/network/v2.0/routers":
			writeJSON(t, response, http.StatusOK, map[string]any{"routers": []any{map[string]any{"id": "router-candidate", "name": baseName + "-router", "status": "ACTIVE"}}, "routers_links": []any{}})
		case "/network/v2.0/security-groups":
			name := request.URL.Query().Get("name")
			switch name {
			case baseName + "-lab-sg":
				writeJSON(t, response, http.StatusOK, map[string]any{"security_groups": []any{map[string]any{
					"id": "sg-lab-candidate", "name": name, "security_group_rules": []any{map[string]any{"id": "rule-lab-candidate", "security_group_id": "sg-lab-candidate", "description": "Allow traffic inside this Lab network"}},
				}}, "security_groups_links": []any{}})
			case baseName + "-management-sg":
				writeJSON(t, response, http.StatusOK, map[string]any{"security_groups": []any{map[string]any{
					"id": "sg-management-candidate", "name": name, "security_group_rules": []any{map[string]any{"id": "rule-ssh-candidate", "security_group_id": "sg-management-candidate", "description": "Allow Connector Management SSH"}},
				}}, "security_groups_links": []any{}})
			default:
				t.Fatalf("unexpected security group query: %s", request.URL.String())
			}
		case "/network/v2.0/ports":
			writeJSON(t, response, http.StatusOK, map[string]any{"ports": []any{
				map[string]any{"id": "port-known", "name": baseName + "-workspace-lab", "status": "ACTIVE"},
				map[string]any{"id": "port-candidate", "name": baseName + "-workspace-management", "status": "DOWN"},
				map[string]any{"id": "port-other", "name": "unrelated", "status": "ACTIVE"},
			}, "ports_links": []any{}})
		default:
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.String())
		}
	}))

	result, err := adapter.Reconcile(context.Background(), coreprovider.ReconcileRequest{
		Correlation:        coreprovider.Correlation{OperationID: "reconcile-3", LabInstanceID: labID, Generation: generation},
		KnownResources:     []coreprovider.ResourceRef{},
		DiscoverCandidates: true,
	})
	if err != nil || result.Error != nil || len(result.Observations) != 10 {
		t.Fatalf("Reconcile() = %+v, %v", result, err)
	}
	for _, observation := range result.Observations {
		if observation.Source != coreprovider.SourceDiscoveredCandidate || !observation.Exists || observation.Generation != generation || strings.Contains(observation.ProviderID, "other") {
			t.Fatalf("candidate observation = %+v", observation)
		}
	}
}

func TestReconcileExcludesKnownIDFromCandidates(t *testing.T) {
	labID := "known-candidate-lab"
	baseName := provisionBaseName(labID, 1)
	adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/network/v2.0/networks/network-known":
			writeJSON(t, response, http.StatusOK, map[string]any{"network": map[string]any{"id": "network-known", "name": baseName + "-network", "status": "ACTIVE"}})
		case "/compute/v2/servers/detail":
			writeJSON(t, response, http.StatusOK, map[string]any{"servers": []any{}, "servers_links": []any{}})
		case "/network/v2.0/networks":
			writeJSON(t, response, http.StatusOK, map[string]any{"networks": []any{map[string]any{"id": "network-known", "name": baseName + "-network", "status": "ACTIVE"}}, "networks_links": []any{}})
		case "/network/v2.0/subnets":
			writeJSON(t, response, http.StatusOK, map[string]any{"subnets": []any{}, "subnets_links": []any{}})
		case "/network/v2.0/routers":
			writeJSON(t, response, http.StatusOK, map[string]any{"routers": []any{}, "routers_links": []any{}})
		case "/network/v2.0/security-groups":
			writeJSON(t, response, http.StatusOK, map[string]any{"security_groups": []any{}, "security_groups_links": []any{}})
		case "/network/v2.0/ports":
			writeJSON(t, response, http.StatusOK, map[string]any{"ports": []any{}, "ports_links": []any{}})
		default:
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.String())
		}
	}))

	result, err := adapter.Reconcile(context.Background(), coreprovider.ReconcileRequest{
		Correlation:        coreprovider.Correlation{OperationID: "reconcile-4", LabInstanceID: labID, Generation: 1},
		KnownResources:     []coreprovider.ResourceRef{{ResourceType: coreprovider.ResourceTypeNetwork, ProviderID: "network-known", Generation: 1}},
		DiscoverCandidates: true,
	})
	if err != nil || len(result.Observations) != 1 || result.Observations[0].Source != coreprovider.SourceKnownResource {
		t.Fatalf("Reconcile() = %+v, %v", result, err)
	}
}
