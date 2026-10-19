package openstackprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

func TestManagementPortDecodesPolicy(t *testing.T) {
	var p managementPort
	if err := json.Unmarshal([]byte(`{"id":"owned-port","network_id":"management-network","extra_dhcp_opts":[{"opt_name":"router","opt_value":"","ip_version":4},{"opt_name":"classless-static-route","opt_value":"172.16.8.0/24,0.0.0.0","ip_version":4}]}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.ID != "owned-port" || !managementRoutesSuppressed(p.DHCPOptions, "172.16.8.0/24") {
		t.Fatalf("valid policy lost during SDK extension decode: id=%q optionCount=%d", p.ID, len(p.DHCPOptions))
	}
}

func TestManagementSubnetAffinity(t *testing.T) {
	for _, drift := range []bool{false, true} {
		name := "stable"
		if drift {
			name = "changed_after_Nova_creation"
		}
		t.Run(name, func(t *testing.T) {
			fake := &m2OpenStackFake{t: t}
			changedReads := 0
			a := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if drift && fake.serverCreates > 0 && r.Method == http.MethodGet && r.URL.Path == "/network/v2.0/ports/port-management" {
					rr := httptest.NewRecorder()
					fake.ServeHTTP(rr, r)
					var body map[string]any
					if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
						t.Fatal(err)
					}
					p := body["port"].(map[string]any)
					p["fixed_ips"] = []map[string]string{{"subnet_id": "different-management-subnet", "ip_address": "172.16.9.200"}}
					changedReads++
					writeJSON(t, w, rr.Code, body)
					return
				}
				fake.ServeHTTP(w, r)
			}))
			a.provision = testProvisionConfig(t)
			var probed string
			a.sshProbe = func(_ context.Context, address string) error { probed = address; return nil }
			a.startupProbe = func(context.Context, string, string) error { return nil }
			result, err := a.Provision(context.Background(), coreprovider.ProvisionRequest{
				Correlation:      coreprovider.Correlation{OperationID: "subnet-affinity", LabInstanceID: "subnet-affinity-lab", Generation: 1},
				CreationSnapshot: validSnapshot(),
			})
			if err != nil {
				t.Fatal(err)
			}
			if !drift {
				if result.Outcome != coreprovider.OutcomeSucceeded || probed != "172.16.8.200:22" {
					t.Fatalf("stable result=%s probed=%s", result.Outcome, probed)
				}
				return
			}
			t.Logf("observed changedReads=%d outcome=%s probed=%q resources=%d", changedReads, result.Outcome, probed, len(result.ProviderResources))
			if changedReads == 0 {
				t.Fatal("fault not reached")
			}
			if result.Outcome == coreprovider.OutcomeSucceeded || probed != "" {
				t.Fatalf("subnet drift accepted: outcome=%s probed=%s", result.Outcome, probed)
			}
			retained := false
			for _, r := range result.ProviderResources {
				if r.ProviderID == "port-management" {
					retained = true
				}
			}
			if !retained {
				t.Fatal("known port ID lost")
			}
		})
	}
}
