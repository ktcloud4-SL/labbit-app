package openstackprovider

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/operationrecovery"
)

func TestFailedRecoveryEvidenceRequiresBoundCompleteOwnedTopology(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux protected ledger ownership and fsync semantics")
	}
	for _, scenario := range []string{"valid", "succeeded", "wrong intent", "missing root", "symlink receipt", "wrong project", "wrong connection", "wrong server owner", "wrong root attachment", "shared tier", "wrong CIDR", "wrong policy", "lookup failed", "active LOCK"} {
		t.Run(scenario, func(t *testing.T) {
			req := operationrecovery.Request{OperationID: uuid.New(), InstanceID: uuid.New(), ConnectionID: uuid.New(), Generation: 1, ProjectID: "fixture-project", VMKey: "vm-1", Nonce: operationrecovery.SHA256([]byte("nonce"))}
			refs := []coreprovider.ResourceRef{{ResourceType: "KT_TIER", ProviderID: uuid.NewString(), Generation: 1, LogicalName: "lab-tier"}, {ResourceType: "KT_TIER", ProviderID: uuid.NewString(), Generation: 1, LogicalName: "vm-1:management-tier"}, {ResourceType: "KT_FIREWALL_POLICY", ProviderID: uuid.NewString(), Generation: 1, LogicalName: "vm-1:management-ssh"}, {ResourceType: "SERVER", ProviderID: uuid.NewString(), Generation: 1, LogicalName: "vm-1"}, {ResourceType: "VOLUME", ProviderID: uuid.NewString(), Generation: 1, LogicalName: "vm-1:root-volume"}}
			a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Errorf("Recovery attempted mutation %s", r.Method)
					w.WriteHeader(500)
					return
				}
				if scenario == "lookup failed" {
					w.WriteHeader(503)
					return
				}
				switch {
				case strings.HasSuffix(r.URL.Path, "/servers/"+refs[3].ProviderID):
					owner := req.InstanceID.String()
					if scenario == "wrong server owner" {
						owner = uuid.NewString()
					}
					writeJSON(t, w, 200, map[string]any{"server": map[string]any{"id": refs[3].ProviderID, "status": "ACTIVE", "tenant_id": "fixture-project", "metadata": map[string]string{"labbit_operation_id": req.OperationID.String(), "labbit_lab_instance_id": owner, "labbit_generation": "1", "labbit_vm_key": "vm-1"}, "os-extended-volumes:volumes_attached": []any{map[string]any{"id": refs[4].ProviderID}}}})
				case strings.HasSuffix(r.URL.Path, "/volumes/"+refs[4].ProviderID):
					server := refs[3].ProviderID
					if scenario == "wrong root attachment" {
						server = uuid.NewString()
					}
					writeJSON(t, w, 200, map[string]any{"volume": map[string]any{"id": refs[4].ProviderID, "status": "in-use", "attachments": []any{map[string]any{"server_id": server}}}})
				case strings.HasSuffix(r.URL.Path, "/nsm/network"):
					lab := "172.25.240.0/24"
					if scenario == "wrong CIDR" {
						lab = "172.25.248.0/24"
					}
					data := []any{map[string]any{"networkId": refs[0].ProviderID, "networkName": ktCloudLabTierName(req.InstanceID.String(), 1), "cidr": lab, "status": "ACTIVE", "shared": scenario == "shared tier"}, map[string]any{"networkId": refs[1].ProviderID, "networkName": ktCloudManagementTierName(req.InstanceID.String(), 1, "vm-1"), "cidr": "172.25.241.0/24", "status": "ACTIVE", "shared": false}}
					writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]any{"total": 2, "offset": 0}, "data": data})
				case strings.HasSuffix(r.URL.Path, "/nsm/firewall/policy"):
					comment := provisionBaseName(req.InstanceID.String(), 1) + ":vm-1:management-ssh"
					if scenario == "wrong policy" {
						comment = "foreign"
					}
					policy := ktCloudFirewallPolicy{ID: refs[2].ProviderID, Action: "accept", Status: "enable", Comment: comment, NAT: "disable", Source: []ktCloudInterface{{ID: "connector-tier"}}, Destination: []ktCloudInterface{{ID: refs[1].ProviderID}}, SourceIPs: []ktCloudAddress{{Name: "172.25.0.11/32"}}, DestinationIPs: []ktCloudAddress{{Name: "all"}}, Services: []ktCloudService{{Protocol: "TCP", Start: "22", End: "22"}}}
					writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]any{"total": 1, "offset": 0}, "data": []any{policy}})
				default:
					t.Errorf("unexpected Provider request %s", r.URL.Path)
					w.WriteHeader(500)
				}
			})
			a.provision.ProviderConnectionID = req.ConnectionID.String()
			a.provision.KTCloudCIDRPairs = testCIDRPairs
			a.provision.KTCloudCIDRStateDir = t.TempDir()
			dir := a.provision.KTCloudCIDRStateDir
			_ = os.Chmod(dir, 0700)
			if err := a.provider.SetTokenAndAuthResult(cidrTestAuth{}); err != nil {
				t.Fatal(err)
			}
			pool, _, err := a.cidrPool()
			if err != nil {
				t.Fatal(err)
			}
			corr := coreprovider.Correlation{OperationID: req.OperationID.String(), LabInstanceID: req.InstanceID.String(), Generation: 1}
			intent := corr
			if scenario == "wrong intent" {
				intent.OperationID = uuid.NewString()
			}
			outcome := coreprovider.OutcomeFailed
			if scenario == "succeeded" {
				outcome = coreprovider.OutcomeSucceeded
			}
			completionRefs := refs
			if scenario == "missing root" {
				completionRefs = refs[:4]
			}
			for name, value := range map[string]any{"pool.json": pool, slotName(0): ktCIDRAssignment{Instance: corr.LabInstanceID, Pair: pool.Pairs[0]}, intentName(0, 1): intent, completionName(0, 1): ktCIDRCompletion{Correlation: corr, Outcome: outcome, Resources: completionRefs}} {
				if err := writeCIDRFile(dir, name, value); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "symlink receipt" {
				path := filepath.Join(dir, completionName(0, 1))
				if err := os.Rename(path, path+".other"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".other", path); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "active LOCK" {
				if err := writeCIDRFile(dir, "LOCK", corr); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "wrong project" {
				req.ProjectID = "other"
			}
			if scenario == "wrong connection" {
				req.ConnectionID = uuid.New()
			}
			before, err := os.ReadFile(filepath.Join(dir, "pool.json"))
			if err != nil {
				t.Fatal(err)
			}
			evidence, err := a.FailedProvisionEvidence(t.Context(), req)
			if (err == nil) != (scenario == "valid") {
				t.Fatalf("scenario %s error=%v", scenario, err)
			}
			if err == nil {
				if evidence.Outcome != "FAILED" || len(evidence.Resources) != 5 || evidence.Request != req {
					t.Fatal("bad recovery proof")
				}
				if _, err = json.Marshal(evidence); err != nil {
					t.Fatal(err)
				}
			}
			after, _ := os.ReadFile(filepath.Join(dir, "pool.json"))
			if string(before) != string(after) {
				t.Fatal("evidence changed pool")
			}
			_, err = os.Lstat(filepath.Join(dir, "LOCK"))
			if scenario != "active LOCK" && !os.IsNotExist(err) {
				t.Fatal("evidence left a LOCK")
			}
		})
	}
}
