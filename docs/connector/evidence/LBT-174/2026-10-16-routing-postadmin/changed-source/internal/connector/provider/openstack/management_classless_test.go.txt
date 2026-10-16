package openstackprovider

import (
	"context"
	"encoding/json"
	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManagementDHCPClasslessRouteUsesValidatedSubnet(t *testing.T) {
	for _, subnet := range []struct{ cidr, ssh, address string }{{"172.16.8.0/24", "172.16.8.1/32", "172.16.8.200"}, {"10.80.0.0/24", "10.80.0.1/32", "10.80.0.200"}} {
		t.Run(subnet.cidr, func(t *testing.T) {
			fake := &m2OpenStackFake{t: t}
			seen, correct := false, false
			a := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/subnets/management-subnet") {
					writeJSON(t, w, 200, invalidManagementSubnet("cidr", subnet.cidr))
					return
				}
				if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/ports") {
					b, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
						return
					}
					r.Body = io.NopCloser(strings.NewReader(string(b)))
					var body struct {
						Port struct {
							NetworkID string                 `json:"network_id"`
							Options   []managementDHCPOption `json:"extra_dhcp_opts"`
						} `json:"port"`
					}
					if err := json.Unmarshal(b, &body); err != nil {
						t.Error(err)
						return
					}
					if body.Port.NetworkID == "management-network" {
						seen = true
						for _, opt := range body.Port.Options {
							if opt.Name == "classless-static-route" && opt.Value != nil && *opt.Value == subnet.cidr+",0.0.0.0" && opt.Version == 4 {
								correct = true
							}
						}
					}
				}
				rr := httptest.NewRecorder()
				fake.ServeHTTP(rr, r)
				if strings.Contains(r.URL.Path, "/ports") {
					var body map[string]any
					if json.Unmarshal(rr.Body.Bytes(), &body) == nil {
						if port, ok := body["port"].(map[string]any); ok && port["network_id"] == "management-network" {
							port["fixed_ips"] = []map[string]string{{"subnet_id": "management-subnet", "ip_address": subnet.address}}
							writeJSON(t, w, rr.Code, body)
							return
						}
					}
				}
				for k, v := range rr.Header() {
					w.Header()[k] = v
				}
				w.WriteHeader(rr.Code)
				_, _ = w.Write([]byte(strings.ReplaceAll(rr.Body.String(), "172.16.8.1/32", subnet.ssh)))
			}))
			a.provision = testProvisionConfig(t)
			a.provision.SSHAllowedCIDR = subnet.ssh
			a.sshProbe = func(context.Context, string) error { return nil }
			a.startupProbe = func(context.Context, string, string) error { return nil }
			snapshot := validSnapshot()
			result, err := a.Provision(context.Background(), coreprovider.ProvisionRequest{Correlation: coreprovider.Correlation{OperationID: "classless-policy", LabInstanceID: "classless-policy", Generation: 1}, CreationSnapshot: snapshot})
			if !seen || !correct || err != nil || result.Outcome != coreprovider.OutcomeSucceeded || fake.userData != snapshot.StartupScript.Content {
				t.Fatalf("explicit classless route not preserved: seen=%t correct=%t outcome=%s err=%v", seen, correct, result.Outcome, err)
			}
		})
	}
}

func TestManagementPortRejectsAddressOutsideOriginalSubnet(t *testing.T) {
	fake := &m2OpenStackFake{t: t}
	changed := false
	a := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fake.serverCreates > 0 && r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/ports/port-management") {
			rr := httptest.NewRecorder()
			fake.ServeHTTP(rr, r)
			var body map[string]any
			_ = json.Unmarshal(rr.Body.Bytes(), &body)
			body["port"].(map[string]any)["fixed_ips"] = []map[string]string{{"subnet_id": "management-subnet", "ip_address": "172.16.9.200"}}
			changed = true
			writeJSON(t, w, rr.Code, body)
			return
		}
		fake.ServeHTTP(w, r)
	}))
	a.provision = testProvisionConfig(t)
	probed := false
	a.sshProbe = func(context.Context, string) error { probed = true; return nil }
	a.startupProbe = func(context.Context, string, string) error { return nil }
	result, err := a.Provision(context.Background(), coreprovider.ProvisionRequest{Correlation: coreprovider.Correlation{OperationID: "address-drift", LabInstanceID: "address-drift", Generation: 1}, CreationSnapshot: validSnapshot()})
	if !changed || err != nil || result.Outcome == coreprovider.OutcomeSucceeded || probed {
		t.Fatalf("outside address accepted: changed=%t probed=%t outcome=%s err=%v", changed, probed, result.Outcome, err)
	}
	retained := false
	for _, r := range result.ProviderResources {
		if r.ProviderID == "port-management" {
			retained = true
		}
	}
	if !retained {
		t.Fatal("known owned port lost")
	}
}

func TestManagementDHCPPolicyRejectsInvalidCIDRBeforePortAPI(t *testing.T) {
	for _, cidr := range []string{"", "invalid", "::/64", "172.16.8.1/24", "0.0.0.0/0"} {
		t.Run(cidr, func(t *testing.T) {
			calls := 0
			a := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) }))
			result, _, err := a.EnsurePort(context.Background(), resourceIdentity(1, "workspace:management"), PortSpec{Name: "owned-management", NetworkID: "management-network", SubnetID: "management-subnet", SecurityGroupIDs: []string{"management-sg"}, SuppressManagementRoutes: true, ManagementSubnetCIDR: cidr})
			if err != ErrInvalidResourceSpec || result.ProviderID != "" || calls != 0 {
				t.Fatalf("invalid CIDR reached provider: calls=%d result=%+v err=%v", calls, result, err)
			}
		})
	}
}

func TestManagementPortRejectsPolicyDriftAfterNova(t *testing.T) {
	for _, kind := range []string{"empty 121", "default route", "other subnet", "null", "missing"} {
		t.Run(kind, func(t *testing.T) {
			fake := &m2OpenStackFake{t: t}
			changed := false
			a := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if fake.serverCreates > 0 && r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/ports/port-management") {
					rr := httptest.NewRecorder()
					fake.ServeHTTP(rr, r)
					var body map[string]any
					_ = json.Unmarshal(rr.Body.Bytes(), &body)
					var value any = ""
					switch kind {
					case "default route":
						value = "0.0.0.0/0,172.16.8.2"
					case "other subnet":
						value = "172.16.9.0/24,0.0.0.0"
					case "null":
						value = nil
					}
					opts := []map[string]any{{"opt_name": "router", "opt_value": "", "ip_version": 4}, {"opt_name": "classless-static-route", "opt_value": value, "ip_version": 4}}
					if kind == "missing" {
						opts = opts[:1]
					}
					body["port"].(map[string]any)["extra_dhcp_opts"] = opts
					changed = true
					writeJSON(t, w, rr.Code, body)
					return
				}
				fake.ServeHTTP(w, r)
			}))
			a.provision = testProvisionConfig(t)
			probed := false
			a.sshProbe = func(context.Context, string) error { probed = true; return nil }
			a.startupProbe = func(context.Context, string, string) error { return nil }
			result, err := a.Provision(context.Background(), coreprovider.ProvisionRequest{Correlation: coreprovider.Correlation{OperationID: "policy-drift", LabInstanceID: "policy-drift", Generation: 1}, CreationSnapshot: validSnapshot()})
			if !changed || err != nil || result.Outcome == coreprovider.OutcomeSucceeded || probed {
				t.Fatalf("policy drift accepted: changed=%t probed=%t outcome=%s err=%v", changed, probed, result.Outcome, err)
			}
			retained := false
			for _, r := range result.ProviderResources {
				if r.ProviderID == "port-management" {
					retained = true
				}
			}
			if !retained {
				t.Fatal("known owned port lost")
			}
		})
	}
}

func TestManagementPortUsesValidatedObservation(t *testing.T) {
	for _, kind := range []string{"outside address", "foreign ID", "foreign network", "empty 121"} {
		t.Run(kind, func(t *testing.T) {
			fake := &m2OpenStackFake{t: t}
			postNovaGets := 0
			a := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if fake.serverCreates > 0 && r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/ports/port-management") {
					postNovaGets++
					if postNovaGets == 1 {
						rr := httptest.NewRecorder()
						fake.ServeHTTP(rr, r)
						var body map[string]any
						if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
							t.Error(err)
							return
						}
						port := body["port"].(map[string]any)
						switch kind {
						case "outside address":
							port["fixed_ips"] = []map[string]string{{"subnet_id": "management-subnet", "ip_address": "172.16.9.200"}}
						case "foreign ID":
							port["id"] = "foreign-port"
						case "foreign network":
							port["network_id"] = "foreign-network"
						case "empty 121":
							port["extra_dhcp_opts"] = []map[string]any{{"opt_name": "router", "opt_value": "", "ip_version": 4}, {"opt_name": "classless-static-route", "opt_value": "", "ip_version": 4}}
						}
						writeJSON(t, w, rr.Code, body)
						return
					}
				}
				fake.ServeHTTP(w, r)
			}))
			a.provision = testProvisionConfig(t)
			probed := ""
			a.sshProbe = func(_ context.Context, address string) error { probed = address; return nil }
			a.startupProbe = func(context.Context, string, string) error { return nil }
			result, err := a.Provision(context.Background(), coreprovider.ProvisionRequest{Correlation: coreprovider.Correlation{OperationID: "single-observation", LabInstanceID: "single-observation", Generation: 1}, CreationSnapshot: validSnapshot()})
			if postNovaGets < 1 || err != nil || result.Outcome == coreprovider.OutcomeSucceeded || probed != "" {
				t.Fatalf("unvalidated observation used: gets=%d probed=%q outcome=%s err=%v", postNovaGets, probed, result.Outcome, err)
			}
			retained := false
			for _, r := range result.ProviderResources {
				if r.ProviderID == "port-management" {
					retained = true
				}
				if r.ProviderID == "foreign-port" {
					t.Fatal("unowned identity entered cleanup ledger")
				}
			}
			if !retained {
				t.Fatal("known owned port lost")
			}
		})
	}
}
