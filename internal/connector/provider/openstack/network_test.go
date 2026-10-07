package openstackprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

func TestEnsureNetworkRejectsExistingExactNameWithoutClaimingOwnership(t *testing.T) {
	posts := 0
	adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/network/v2.0/networks" {
			http.NotFound(response, request)
			return
		}
		if request.Method == http.MethodPost {
			posts++
		}
		if request.URL.Query().Get("name") != "lab-network" {
			t.Errorf("name query = %q", request.URL.Query().Get("name"))
		}
		writeJSON(t, response, http.StatusOK, map[string]any{
			"networks": []map[string]any{
				{"id": "network-1", "name": "lab-network", "status": "ACTIVE", "shared": false, "subnets": []any{}},
			},
			"networks_links": []any{},
		})
	}))

	result, err := adapter.EnsureNetwork(context.Background(), ResourceIdentity{
		Generation:  3,
		LogicalName: "lab-network",
	}, NetworkSpec{Name: "lab-network"})
	if !errors.Is(err, ErrResourceOwnership) || posts != 0 || result.ProviderID != "" {
		t.Fatalf("unexpected result: %+v, posts=%d", result, posts)
	}
}

func TestEnsureNetworkTreatsRequestTimeoutAsUnknownMutation(t *testing.T) {
	for _, test := range []struct {
		name       string
		status     int
		isRejected bool
	}{
		{name: "request timeout", status: http.StatusRequestTimeout, isRejected: false},
		{name: "bad request", status: http.StatusBadRequest, isRejected: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				switch request.Method {
				case http.MethodGet:
					writeJSON(t, response, http.StatusOK, map[string]any{"networks": []any{}, "networks_links": []any{}})
				case http.MethodPost:
					http.Error(response, "provider response must not escape", test.status)
				default:
					response.WriteHeader(http.StatusMethodNotAllowed)
				}
			}))
			_, err := adapter.EnsureNetwork(context.Background(), ResourceIdentity{Generation: 1, LogicalName: "lab-network"}, NetworkSpec{Name: "lab-network"})
			if !errors.Is(err, ErrNetworkCreate) || errors.Is(err, ErrMutationRejected) != test.isRejected {
				t.Fatalf("EnsureNetwork() error = %v, rejected = %v", err, errors.Is(err, ErrMutationRejected))
			}
		})
	}
}

func TestEnsureNetworkCreatesPrivateActiveResource(t *testing.T) {
	posts := 0
	adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/network/v2.0/networks" {
			http.NotFound(response, request)
			return
		}
		switch request.Method {
		case http.MethodGet:
			writeJSON(t, response, http.StatusOK, map[string]any{"networks": []any{}, "networks_links": []any{}})
		case http.MethodPost:
			posts++
			var body struct {
				Network struct {
					Name         string `json:"name"`
					Description  string `json:"description"`
					AdminStateUp bool   `json:"admin_state_up"`
					Shared       bool   `json:"shared"`
				} `json:"network"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Network.Name != "lab-network" || body.Network.Description != "managed by Labbit" || !body.Network.AdminStateUp || body.Network.Shared {
				t.Fatalf("unexpected create body: %+v", body.Network)
			}
			writeJSON(t, response, http.StatusCreated, map[string]any{
				"network": map[string]any{"id": "network-created", "name": body.Network.Name, "status": "BUILD", "shared": false, "subnets": []any{}},
			})
		default:
			response.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))

	result, err := adapter.EnsureNetwork(context.Background(), ResourceIdentity{
		Generation:  1,
		LogicalName: "lab-network",
	}, NetworkSpec{Name: "lab-network", Description: "managed by Labbit"})
	if err != nil {
		t.Fatalf("EnsureNetwork() error = %v", err)
	}
	if posts != 1 || result.ProviderID != "network-created" || result.ObservedState != "BUILD" {
		t.Fatalf("unexpected result: %+v, posts=%d", result, posts)
	}
}

func TestEnsureSubnetRejectsSameNameWithDifferentAddressing(t *testing.T) {
	posts := 0
	adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			posts++
		}
		writeJSON(t, response, http.StatusOK, map[string]any{
			"subnets": []map[string]any{
				{"id": "subnet-existing", "name": "lab-subnet", "network_id": "network-1", "cidr": "10.0.2.0/24", "gateway_ip": "10.0.2.1", "enable_dhcp": true},
			},
			"subnets_links": []any{},
		})
	}))

	result, err := adapter.EnsureSubnet(context.Background(), ResourceIdentity{
		Generation:  1,
		LogicalName: "lab-subnet",
	}, SubnetSpec{
		Name:       "lab-subnet",
		NetworkID:  "network-1",
		CIDR:       "10.0.1.0/24",
		EnableDHCP: true,
	})
	if !errors.Is(err, ErrSubnetConflict) || posts != 0 || result.ProviderID != "" {
		t.Fatalf("result = %+v, error = %v, posts=%d", result, err, posts)
	}
}

func TestEnsureSubnetCreatesIPv4Resource(t *testing.T) {
	posts := 0
	gateway := "10.0.1.1"
	adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/network/v2.0/subnets" {
			http.NotFound(response, request)
			return
		}
		switch request.Method {
		case http.MethodGet:
			writeJSON(t, response, http.StatusOK, map[string]any{"subnets": []any{}, "subnets_links": []any{}})
		case http.MethodPost:
			posts++
			var body map[string]map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			created := body["subnet"]
			if created["name"] != "lab-subnet" || created["network_id"] != "network-1" || created["cidr"] != "10.0.1.0/24" || created["gateway_ip"] != gateway || created["ip_version"] != float64(4) || created["enable_dhcp"] != true {
				t.Fatalf("unexpected create body: %+v", created)
			}
			writeJSON(t, response, http.StatusCreated, map[string]any{
				"subnet": map[string]any{"id": "subnet-created", "name": "lab-subnet", "network_id": "network-1", "cidr": "10.0.1.0/24", "gateway_ip": gateway, "enable_dhcp": true},
			})
		default:
			response.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))

	result, err := adapter.EnsureSubnet(context.Background(), ResourceIdentity{
		Generation:  4,
		LogicalName: "lab-subnet",
	}, SubnetSpec{
		Name:        "lab-subnet",
		Description: "Lab subnet",
		NetworkID:   "network-1",
		CIDR:        "10.0.1.0/24",
		GatewayIP:   &gateway,
		EnableDHCP:  true,
	})
	if err != nil {
		t.Fatalf("EnsureSubnet() error = %v", err)
	}
	if posts != 1 || result.ResourceType != coreprovider.ResourceTypeSubnet || result.ProviderID != "subnet-created" || result.Generation != 4 || result.ObservedState != "PRESENT" {
		t.Fatalf("unexpected result: %+v, posts=%d", result, posts)
	}
}

func TestEnsureSecurityGroupCreatesStatefulResource(t *testing.T) {
	posts := 0
	adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/network/v2.0/security-groups" {
			http.NotFound(response, request)
			return
		}
		switch request.Method {
		case http.MethodGet:
			writeJSON(t, response, http.StatusOK, map[string]any{"security_groups": []any{}, "security_groups_links": []any{}})
		case http.MethodPost:
			posts++
			var body map[string]map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			created := body["security_group"]
			if created["name"] != "lab-sg" || created["description"] != "Lab security group" || created["stateful"] != true {
				t.Fatalf("unexpected create body: %+v", created)
			}
			writeJSON(t, response, http.StatusCreated, map[string]any{
				"security_group": map[string]any{"id": "sg-created", "name": "lab-sg", "description": "Lab security group", "stateful": true, "security_group_rules": []any{}},
			})
		default:
			response.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))

	result, err := adapter.EnsureSecurityGroup(context.Background(), ResourceIdentity{
		Generation:  2,
		LogicalName: "lab-sg",
	}, SecurityGroupSpec{Name: "lab-sg", Description: "Lab security group"})
	if err != nil {
		t.Fatalf("EnsureSecurityGroup() error = %v", err)
	}
	if posts != 1 || result.ResourceType != coreprovider.ResourceTypeSecurityGroup || result.ProviderID != "sg-created" || result.Generation != 2 {
		t.Fatalf("unexpected result: %+v, posts=%d", result, posts)
	}
}

func TestEnsureResourcesRejectInvalidInputBeforeCallingOpenStack(t *testing.T) {
	calls := 0
	adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls++
		http.Error(response, "unexpected", http.StatusInternalServerError)
	}))

	if _, err := adapter.EnsureNetwork(context.Background(), ResourceIdentity{}, NetworkSpec{Name: "network"}); !errors.Is(err, ErrInvalidResourceSpec) {
		t.Fatalf("network error = %v", err)
	}
	if _, err := adapter.EnsureSubnet(context.Background(), ResourceIdentity{Generation: 1, LogicalName: "subnet"}, SubnetSpec{Name: "subnet", NetworkID: "network", CIDR: "not-a-cidr"}); !errors.Is(err, ErrInvalidResourceSpec) {
		t.Fatalf("subnet error = %v", err)
	}
	if _, err := adapter.EnsureSecurityGroup(context.Background(), ResourceIdentity{Generation: 1, LogicalName: "sg"}, SecurityGroupSpec{Name: "default"}); !errors.Is(err, ErrInvalidResourceSpec) {
		t.Fatalf("security group error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("OpenStack was called %d times for invalid input", calls)
	}
}
