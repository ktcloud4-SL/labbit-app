package openstackprovider

import (
	"context"
	"net/http"
	"strings"
	"testing"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

func TestCleanupDeletesExactIDsInDependencyOrderAndTreats404AsDeleted(t *testing.T) {
	var deletes []string
	adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodDelete {
			deletes = append(deletes, request.URL.Path)
			if strings.HasSuffix(request.URL.Path, "/already-absent") {
				http.NotFound(response, request)
				return
			}
			response.WriteHeader(http.StatusNoContent)
			return
		}
		if request.Method == http.MethodGet {
			http.NotFound(response, request)
			return
		}
		t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
	}))
	adapter.provision = testProvisionConfig()

	result, err := adapter.Cleanup(context.Background(), coreprovider.CleanupRequest{
		Correlation: coreprovider.Correlation{OperationID: "cleanup-1", LabInstanceID: "lab-1", Generation: 1},
		ProviderResources: []coreprovider.ResourceRef{
			{ResourceType: coreprovider.ResourceTypeNetwork, ProviderID: "network-1", Generation: 1},
			{ResourceType: coreprovider.ResourceTypeSecurityGroup, ProviderID: "already-absent", Generation: 1},
			{ResourceType: coreprovider.ResourceTypeSubnet, ProviderID: "subnet-1", Generation: 1},
			{ResourceType: coreprovider.ResourceTypeSecurityRule, ProviderID: "rule-1", Generation: 1},
			{ResourceType: coreprovider.ResourceTypePort, ProviderID: "port-1", Generation: 1},
			{ResourceType: coreprovider.ResourceTypeServer, ProviderID: "server-1", Generation: 1},
		},
	})
	if err != nil || result.Outcome != coreprovider.OutcomeSucceeded || result.Error != nil {
		t.Fatalf("Cleanup() = %+v, %v", result, err)
	}
	want := []string{
		"/compute/v2/servers/server-1",
		"/network/v2.0/ports/port-1",
		"/network/v2.0/security-group-rules/rule-1",
		"/network/v2.0/subnets/subnet-1",
		"/network/v2.0/security-groups/already-absent",
		"/network/v2.0/networks/network-1",
	}
	if strings.Join(deletes, "\n") != strings.Join(want, "\n") {
		t.Fatalf("delete order = %#v, want %#v", deletes, want)
	}
	for _, resource := range result.ProviderResources {
		if resource.ObservedState != stateDeleted {
			t.Fatalf("cleanup resource = %+v", resource)
		}
	}
}

func TestCleanupDetachesRouterInterfaceBeforeDelete(t *testing.T) {
	var requests []string
	adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.Method+" "+request.URL.Path)
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/network/v2.0/ports":
			writeJSON(t, response, http.StatusOK, map[string]any{"ports": []any{map[string]any{
				"id": "router-interface", "device_id": "router-1", "device_owner": "network:router_interface", "fixed_ips": []any{map[string]any{"subnet_id": "subnet-1", "ip_address": "198.19.0.1"}},
			}}, "ports_links": []any{}})
		case request.Method == http.MethodPut && request.URL.Path == "/network/v2.0/routers/router-1/remove_router_interface":
			writeJSON(t, response, http.StatusOK, map[string]any{"id": "router-1", "port_id": "router-interface", "subnet_id": "subnet-1"})
		case request.Method == http.MethodDelete && request.URL.Path == "/network/v2.0/routers/router-1":
			response.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodGet && request.URL.Path == "/network/v2.0/routers/router-1":
			http.NotFound(response, request)
		default:
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.String())
		}
	}))
	adapter.provision = testProvisionConfig()
	result, err := adapter.Cleanup(context.Background(), coreprovider.CleanupRequest{
		Correlation:       coreprovider.Correlation{OperationID: "cleanup-router", LabInstanceID: "lab", Generation: 1},
		ProviderResources: []coreprovider.ResourceRef{{ResourceType: coreprovider.ResourceTypeRouter, ProviderID: "router-1", Generation: 1}},
	})
	if err != nil || result.Outcome != coreprovider.OutcomeSucceeded {
		t.Fatalf("Cleanup() = %+v, %v", result, err)
	}
	want := []string{
		"GET /network/v2.0/ports",
		"PUT /network/v2.0/routers/router-1/remove_router_interface",
		"DELETE /network/v2.0/routers/router-1",
		"GET /network/v2.0/routers/router-1",
	}
	if strings.Join(requests, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests = %#v, want %#v", requests, want)
	}
}

func TestCleanupUncertainDeleteStopsAndPreservesEveryTarget(t *testing.T) {
	calls := 0
	adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls++
		http.Error(response, "raw-provider-secret", http.StatusGatewayTimeout)
	}))
	adapter.provision = testProvisionConfig()

	result, err := adapter.Cleanup(context.Background(), coreprovider.CleanupRequest{
		Correlation: coreprovider.Correlation{OperationID: "cleanup-2", LabInstanceID: "lab-2", Generation: 1},
		ProviderResources: []coreprovider.ResourceRef{
			{ResourceType: coreprovider.ResourceTypePort, ProviderID: "port-1", Generation: 1},
			{ResourceType: coreprovider.ResourceTypeServer, ProviderID: "server-1", Generation: 1},
		},
	})
	if err != nil || calls != 1 || result.Outcome != coreprovider.OutcomeUnknown || len(result.ProviderResources) != 2 {
		t.Fatalf("Cleanup() calls=%d result=%+v err=%v", calls, result, err)
	}
	if result.ProviderResources[0].ResourceType != coreprovider.ResourceTypeServer || result.ProviderResources[0].ObservedState != stateDeleteUnknown || result.ProviderResources[1].ObservedState != stateDeleteNotAttempted {
		t.Fatalf("unexpected retained resources: %+v", result.ProviderResources)
	}
	if result.Error == nil || strings.Contains(strings.ToLower(result.Error.Message), "secret") {
		t.Fatalf("unsafe error: %+v", result.Error)
	}
}

func TestCleanupRejectsFutureGenerationBeforeMutation(t *testing.T) {
	calls := 0
	adapter := newTestAdapter(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))

	result, err := adapter.Cleanup(context.Background(), coreprovider.CleanupRequest{
		Correlation:       coreprovider.Correlation{OperationID: "cleanup-3", LabInstanceID: "lab-3", Generation: 2},
		ProviderResources: []coreprovider.ResourceRef{{ResourceType: coreprovider.ResourceTypeServer, ProviderID: "newer-server", Generation: 3}},
	})
	if err != nil || calls != 0 || result.Outcome != coreprovider.OutcomeFailed {
		t.Fatalf("Cleanup() calls=%d result=%+v err=%v", calls, result, err)
	}
}

func TestResetPreflightFailureDoesNotDeleteCurrentGeneration(t *testing.T) {
	deletes := 0
	adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodDelete {
			deletes++
		}
		http.NotFound(response, request)
	}))
	adapter.provision = testProvisionConfig()

	result, err := adapter.Reset(context.Background(), coreprovider.ResetRequest{
		Correlation:      coreprovider.Correlation{OperationID: "reset-1", LabInstanceID: "lab-1", Generation: 2},
		CreationSnapshot: validSnapshot(),
		ProviderResources: []coreprovider.ResourceRef{{
			ResourceType: coreprovider.ResourceTypeServer,
			ProviderID:   "old-server",
			Generation:   1,
		}},
	})
	if err != nil || deletes != 0 || result.Outcome != coreprovider.OutcomeFailed || len(result.ProviderResources) != 1 || result.ProviderResources[0].ObservedState != stateDeleteNotAttempted {
		t.Fatalf("Reset() deletes=%d result=%+v err=%v", deletes, result, err)
	}
}

func TestResetRejectsCurrentGenerationResourcesBeforePreflight(t *testing.T) {
	calls := 0
	adapter := newTestAdapter(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	adapter.provision = testProvisionConfig()

	result, err := adapter.Reset(context.Background(), coreprovider.ResetRequest{
		Correlation:      coreprovider.Correlation{OperationID: "reset-generation", LabInstanceID: "lab-generation", Generation: 2},
		CreationSnapshot: validSnapshot(),
		ProviderResources: []coreprovider.ResourceRef{{
			ResourceType: coreprovider.ResourceTypeServer,
			ProviderID:   "same-generation-server",
			Generation:   2,
		}},
	})
	if err != nil || calls != 0 || result.Outcome != coreprovider.OutcomeFailed {
		t.Fatalf("Reset() calls=%d result=%+v err=%v", calls, result, err)
	}
}

func TestResetCleansOldGenerationThenProvisionsNewGeneration(t *testing.T) {
	base := &m2OpenStackFake{t: t}
	fake := &m3ResetFake{base: base, deleted: make(map[string]bool)}
	adapter := newTestAdapter(t, fake)
	adapter.provision = testProvisionConfig()
	adapter.sshProbe = func(context.Context, string) error { return nil }
	adapter.startupProbe = func(context.Context, string) error { return nil }

	first, err := adapter.Provision(context.Background(), coreprovider.ProvisionRequest{
		Correlation:      coreprovider.Correlation{OperationID: "provision-1", LabInstanceID: "lab-instance-1", Generation: 1},
		CreationSnapshot: validSnapshot(),
	})
	if err != nil || first.Outcome != coreprovider.OutcomeSucceeded {
		t.Fatalf("initial Provision() = %+v, %v", first, err)
	}
	old := make([]coreprovider.ResourceRef, 0, len(first.ProviderResources))
	for _, resource := range first.ProviderResources {
		old = append(old, resource.ResourceRef)
	}

	reset, err := adapter.Reset(context.Background(), coreprovider.ResetRequest{
		Correlation:       coreprovider.Correlation{OperationID: "reset-2", LabInstanceID: "lab-instance-1", Generation: 2},
		CreationSnapshot:  validSnapshot(),
		ProviderResources: old,
	})
	if err != nil || reset.Outcome != coreprovider.OutcomeSucceeded || len(reset.ProviderResources) != 20 {
		t.Fatalf("Reset() = %+v, %v", reset, err)
	}
	for index, resource := range reset.ProviderResources {
		if index < 10 {
			if resource.Generation != 1 || resource.ObservedState != stateDeleted {
				t.Fatalf("old generation result = %+v", resource)
			}
		} else if resource.Generation != 2 || resource.ObservedState == stateDeleted {
			t.Fatalf("new generation result = %+v", resource)
		}
	}
	if len(fake.deleteOrder) != 10 || !strings.Contains(fake.deleteOrder[0], "/servers/") || !strings.Contains(fake.deleteOrder[len(fake.deleteOrder)-1], "/networks/") {
		t.Fatalf("delete order = %#v", fake.deleteOrder)
	}
}

type m3ResetFake struct {
	base        *m2OpenStackFake
	deleted     map[string]bool
	deleteOrder []string
}

func (f *m3ResetFake) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodDelete {
		f.deleted[request.URL.Path] = true
		f.deleteOrder = append(f.deleteOrder, request.URL.Path)
		response.WriteHeader(http.StatusNoContent)
		return
	}
	if request.Method == http.MethodGet && f.deleted[request.URL.Path] {
		http.NotFound(response, request)
		return
	}
	if request.Method == http.MethodPost {
		for _, path := range createdResourcePaths(request.URL.Path) {
			delete(f.deleted, path)
		}
	}
	f.base.ServeHTTP(response, request)
}

func createdResourcePaths(collectionPath string) []string {
	switch collectionPath {
	case "/network/v2.0/networks":
		return []string{"/network/v2.0/networks/network-lab"}
	case "/network/v2.0/subnets":
		return []string{"/network/v2.0/subnets/subnet-lab"}
	case "/network/v2.0/routers":
		return []string{"/network/v2.0/routers/router-lab"}
	case "/network/v2.0/security-groups":
		return []string{"/network/v2.0/security-groups/security-group-lab", "/network/v2.0/security-groups/security-group-management"}
	case "/network/v2.0/security-group-rules":
		return []string{"/network/v2.0/security-group-rules/rule-lab", "/network/v2.0/security-group-rules/rule-ssh"}
	case "/network/v2.0/ports":
		return []string{"/network/v2.0/ports/port-lab", "/network/v2.0/ports/port-management"}
	case "/compute/v2/servers":
		return []string{"/compute/v2/servers/server-workspace"}
	default:
		return nil
	}
}
