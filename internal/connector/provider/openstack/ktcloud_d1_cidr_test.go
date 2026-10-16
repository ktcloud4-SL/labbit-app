package openstackprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/tokens"
	"golang.org/x/crypto/ssh"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

const testCIDRPairs = `[{"lab":"172.25.240.0/24","management":"172.25.241.0/24"},{"lab":"172.25.242.0/24","management":"172.25.243.0/24"}]`

func pooledKTAdapter(t *testing.T, tiers []ktCloudTier) *Adapter {
	t.Helper()
	a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/compute/v2/nsm/network" {
			if r.URL.Path != "/compute/v2/nsm/unavailable/network" {
				t.Errorf("unexpected cloud request: %s %s", r.Method, r.URL.Path)
			}
			w.WriteHeader(500)
			return
		}
		// Marshal explicit sharing flags, as actual NSM responses do.
		data := []map[string]any{}
		for _, tier := range tiers {
			data = append(data, map[string]any{"networkId": tier.ID, "refId": tier.RefID, "cidr": tier.CIDR, "networkName": tier.Name, "shared": tier.Shared, "status": tier.Status})
		}
		writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]any{"total": len(data), "offset": 0}, "data": data})
	})
	a.provision.SSHKnownHostsFile = filepath.Join(t.TempDir(), "pins")
	a.provision.KTCloudCIDRPairs = testCIDRPairs
	a.provision.KTCloudCIDRStateDir = t.TempDir()
	if err := os.Chmod(a.provision.KTCloudCIDRStateDir, 0700); err != nil {
		t.Fatal(err)
	}
	return a
}
func cidrCorrelation(instance string, generation int64) coreprovider.Correlation {
	return coreprovider.Correlation{LabInstanceID: instance, Generation: generation, OperationID: "operation"}
}
func claimCIDR(t *testing.T, a *Adapter, instance string, generation int64, reset bool) ktCIDRPair {
	t.Helper()
	var selected ktCIDRPair
	result, err := a.withCIDRScope(context.Background(), cidrCorrelation(instance, generation), reset, func(scoped *Adapter) (coreprovider.OperationResult, error) {
		selected = ktCIDRPair{Lab: scoped.provision.LabSubnetCIDR, Management: scoped.provision.KTCloudManagementCIDR}
		if err := scoped.startCIDRMutation(cidrCorrelation(instance, generation)); err != nil {
			t.Fatal(err)
		}
		return coreprovider.OperationResult{Outcome: coreprovider.OutcomeSucceeded}, nil
	})
	if err != nil || result.Outcome != coreprovider.OutcomeSucceeded {
		t.Fatalf("claim: %v %s", err, result.Outcome)
	}
	return selected
}
func TestKTCloudCIDRPoolIsolationResetAndRestart(t *testing.T) {
	a := pooledKTAdapter(t, nil)
	original := a.provision
	teacher := claimCIDR(t, a, "teacher", 1, false)
	student := claimCIDR(t, a, "student", 1, false)
	if teacher == student || teacher.Lab == student.Management || teacher.Management == student.Lab {
		t.Fatal("instances overlap")
	}
	// A new adapter/process reads persistent assignments; generation is not the key.
	restarted := pooledKTAdapter(t, nil)
	restarted.provision = a.provision
	replacement := claimCIDR(t, restarted, "student", 2, true)
	if replacement != student || a.provision != original {
		t.Fatal("reset changed CIDR or request mutated shared configuration")
	}
	runs := 0
	hold := func(*Adapter) (coreprovider.OperationResult, error) {
		runs++
		return coreprovider.OperationResult{}, nil
	}
	for _, tc := range []struct {
		instance   string
		generation int64
		reset      bool
	}{{"student", 2, true}, {"student", 1, false}, {"third", 1, false}, {"missing", 2, true}} {
		result, _ := restarted.withCIDRScope(context.Background(), cidrCorrelation(tc.instance, tc.generation), tc.reset, hold)
		if result.Outcome != coreprovider.OutcomeUnknown {
			t.Fatal("duplicate, exhausted, or missing assignment did not HOLD")
		}
	}
	if runs != 0 {
		t.Fatal("unsafe mutation permitted")
	}
}
func TestKTCloudCIDRPoolSharedOverlapAndUncertainInventory(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tiers []ktCloudTier
		want  string
	}{
		{"skip shared", []ktCloudTier{{ID: "shared", RefID: "ref", CIDR: "172.25.240.0/23", Shared: true}}, "172.25.242.0/24"},
		{"all overlap", []ktCloudTier{{ID: "shared", RefID: "ref", CIDR: "172.25.240.0/22"}}, ""},
		{"unknown CIDR", []ktCloudTier{{ID: "unknown", RefID: "ref"}}, ""},
		{"pending unmapped Tier", []ktCloudTier{{ID: "pending"}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := pooledKTAdapter(t, tc.tiers)
			selected := ""
			result, _ := a.withCIDRScope(context.Background(), cidrCorrelation("student", 1), false, func(scoped *Adapter) (coreprovider.OperationResult, error) {
				selected = scoped.provision.LabSubnetCIDR
				return coreprovider.OperationResult{Outcome: coreprovider.OutcomeSucceeded}, nil
			})
			if selected != tc.want || (tc.want == "" && result.Outcome != coreprovider.OutcomeUnknown) {
				t.Fatalf("selected %q, outcome %s", selected, result.Outcome)
			}
		})
	}
	a := pooledKTAdapter(t, nil)
	a.ktNetwork.client.Endpoint += "unavailable/"
	result, _ := a.withCIDRScope(context.Background(), cidrCorrelation("student", 1), false, func(*Adapter) (coreprovider.OperationResult, error) {
		t.Fatal("uncertain inventory accepted")
		return coreprovider.OperationResult{}, nil
	})
	if result.Outcome != coreprovider.OutcomeUnknown {
		t.Fatal("lookup uncertainty did not HOLD")
	}
}
func TestKTCloudCIDRPoolConcurrentAndUnknownMutation(t *testing.T) {
	a := pooledKTAdapter(t, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.withCIDRScope(context.Background(), cidrCorrelation("student", 1), false, func(scoped *Adapter) (coreprovider.OperationResult, error) {
			if err := scoped.startCIDRMutation(cidrCorrelation("student", 1)); err != nil {
				t.Error(err)
			}
			close(entered)
			<-release
			return unknownResult(nil, "lost create response"), nil
		})
	}()
	<-entered
	other := *a
	result, _ := other.withCIDRScope(context.Background(), cidrCorrelation("teacher", 1), false, func(*Adapter) (coreprovider.OperationResult, error) {
		t.Error("concurrent mutation entered")
		return coreprovider.OperationResult{}, nil
	})
	if result.Outcome != coreprovider.OutcomeUnknown {
		t.Error("concurrent operation did not HOLD")
	}
	close(release)
	wg.Wait()
	// Missing provider inventory after a lost response cannot release the slot or intent.
	result, _ = other.withCIDRScope(context.Background(), cidrCorrelation("student", 1), false, func(*Adapter) (coreprovider.OperationResult, error) {
		t.Error("blind generation retry permitted")
		return coreprovider.OperationResult{}, nil
	})
	if result.Outcome != coreprovider.OutcomeUnknown {
		t.Fatal("lost result did not HOLD")
	}
	pair := claimCIDR(t, &other, "teacher", 1, false)
	if pair.Lab != "172.25.242.0/24" {
		t.Fatal("unknown slot was recycled")
	}
}
func TestKTCloudCIDRPoolConfigurationAndCorruptionFailClosed(t *testing.T) {
	for _, kind := range []string{"overlap", "noncanonical", "public", "partial", "pool changed", "symlink", "corrupt slot", "stale lock"} {
		t.Run(kind, func(t *testing.T) {
			a := pooledKTAdapter(t, nil)
			if kind == "pool changed" || kind == "corrupt slot" {
				claimCIDR(t, a, "teacher", 1, false)
			}
			switch kind {
			case "overlap":
				a.provision.KTCloudCIDRPairs = `[{"lab":"172.25.240.0/24","management":"172.25.240.0/24"}]`
			case "noncanonical":
				a.provision.KTCloudCIDRPairs = `[{"lab":"172.25.240.1/24","management":"172.25.241.0/24"}]`
			case "public":
				a.provision.KTCloudCIDRPairs = `[{"lab":"8.8.8.0/24","management":"172.25.241.0/24"}]`
			case "partial":
				a.provision.KTCloudCIDRStateDir = ""
			case "pool changed":
				a.provision.KTCloudCIDRPairs = `[{"lab":"172.25.244.0/24","management":"172.25.245.0/24"}]`
			case "symlink":
				dir := a.provision.KTCloudCIDRStateDir
				link := filepath.Join(t.TempDir(), "link")
				if err := os.Symlink(dir, link); err != nil {
					t.Fatal(err)
				}
				a.provision.KTCloudCIDRStateDir = link
			case "corrupt slot":
				if err := os.WriteFile(filepath.Join(a.provision.KTCloudCIDRStateDir, slotName(0)), []byte(`{`), 0600); err != nil {
					t.Fatal(err)
				}
			case "stale lock":
				if err := writeCIDRFile(a.provision.KTCloudCIDRStateDir, "LOCK", cidrCorrelation("crashed", 1)); err != nil {
					t.Fatal(err)
				}
			}
			result, _ := a.withCIDRScope(context.Background(), cidrCorrelation("student", 1), false, func(*Adapter) (coreprovider.OperationResult, error) {
				t.Error("unsafe configuration entered lifecycle")
				return coreprovider.OperationResult{}, nil
			})
			if result.Outcome != coreprovider.OutcomeUnknown {
				t.Fatal("not HOLD")
			}
		})
	}
}

func TestKTCloudCIDRPoolManagementAddressUsesAssignment(t *testing.T) {
	a := pooledKTAdapter(t, nil)
	claimCIDR(t, a, "teacher", 1, false)
	pair := claimCIDR(t, a, "student", 1, false)
	b := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("address resolution mutated provider")
		}
		name := ktCloudManagementTierName("student", 1, "workspace")
		switch r.URL.Path {
		case "/compute/v2/servers/student-server":
			writeJSON(t, w, 200, map[string]any{"server": map[string]any{"id": "student-server", "tenant_id": "fixture-project", "status": "ACTIVE", "name": provisionBaseName("student", 1) + "-workspace", "metadata": map[string]string{"labbit_lab_instance_id": "student", "labbit_generation": "1", "labbit_operation_id": "op", "labbit_vm_key": "workspace"}, "addresses": map[string]any{name: []any{map[string]any{"addr": "172.25.243.10", "OS-EXT-IPS:type": "fixed"}}}}})
		case "/compute/v2/nsm/network":
			writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]int{"total": 1, "offset": 0}, "data": []any{map[string]any{"networkId": "student-management", "refId": "student-ref", "networkName": name, "cidr": pair.Management, "shared": false, "status": "ACTIVE"}}})
		default:
			w.WriteHeader(500)
		}
	})
	b.provision = a.provision
	address, err := b.ResolveServerAddress(context.Background(), "workspace", "student-server")
	if err != nil || address != "172.25.243.10" {
		t.Fatalf("assigned management address: %q %v", address, err)
	}
	if err := os.Remove(filepath.Join(a.provision.KTCloudCIDRStateDir, slotName(1))); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ResolveServerAddress(context.Background(), "workspace", "student-server"); err == nil {
		t.Fatal("missing assignment fell back to global management CIDR")
	}
}
func TestKTCloudCIDRPoolEnvironment(t *testing.T) {
	t.Setenv(EnvKTCloudCIDRPairs, testCIDRPairs)
	t.Setenv(EnvKTCloudCIDRStateDir, "/durable/pool")
	cfg := ConfigFromEnvironment().Provision
	var pairs []ktCIDRPair
	if json.Unmarshal([]byte(cfg.KTCloudCIDRPairs), &pairs) != nil || len(pairs) != 2 || cfg.KTCloudCIDRStateDir != "/durable/pool" {
		t.Fatal("pool deployment inputs not loaded")
	}
}

// Supply authenticated project affinity through the same SDK boundary as production.
type cidrTestAuth struct{}

func (cidrTestAuth) ExtractTokenID() (string, error) { return "test-token", nil }
func (cidrTestAuth) ExtractProject() (*tokens.Project, error) {
	return &tokens.Project{ID: "fixture-project"}, nil
}

func TestKTCloudCIDRPoolResetCreditsOnlyExactPreviousInstance(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(fmt.Sprint("foreign=", foreign), func(t *testing.T) {
			seed := pooledKTAdapter(t, nil)
			claimCIDR(t, seed, "teacher", 1, false)
			pair := claimCIDR(t, seed, "student", 1, false)
			keyFile := writeTestPrivateKey(t, "cidr-key")
			sshPublicKey, err := ssh.NewPublicKey(testPrivateKey("cidr-key").Public())
			if err != nil {
				t.Fatal(err)
			}
			publicKey := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPublicKey)))
			studentTier := map[string]any{"networkId": "student-tier", "refId": "student-ref", "networkName": ktCloudLabTierName("student", 1), "cidr": pair.Lab, "shared": false, "status": "ACTIVE"}
			managementTier := map[string]any{"networkId": "student-management", "refId": "student-management-ref", "networkName": ktCloudManagementTierName("student", 1, "workspace"), "cidr": pair.Management, "shared": false, "status": "ACTIVE"}
			teacherTier := map[string]any{"networkId": "teacher-tier", "refId": "teacher-ref", "networkName": ktCloudLabTierName("teacher", 1), "cidr": "172.25.240.0/24", "shared": false, "status": "ACTIVE"}
			a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Errorf("preflight mutated provider: %s", r.Method)
					w.WriteHeader(500)
					return
				}
				switch r.URL.Path {
				case "/compute/v2/nsm/network":
					writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]int{"total": 4, "offset": 0}, "data": []any{map[string]any{"networkId": "connector-tier", "refId": "connector-ref", "cidr": "172.25.0.0/24", "status": "ACTIVE"}, studentTier, managementTier, teacherTier}})
				case "/compute/v2/nsm/firewall/policy":
					writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]int{"total": 0, "offset": 0}, "data": []any{}})
				case "/compute/v2/servers/student-server":
					writeJSON(t, w, 200, map[string]any{"server": map[string]any{"id": "student-server", "tenant_id": "fixture-project", "status": "ACTIVE", "metadata": map[string]string{"labbit_lab_instance_id": "student", "labbit_generation": "1", "labbit_vm_key": "workspace"}, "flavor": map[string]any{"original_name": "2x4.itl", "vcpus": 2, "ram": 4096}}})
				case "/compute/v2/os-keypairs/key":
					writeJSON(t, w, 200, map[string]any{"keypair": map[string]any{"name": "key", "public_key": publicKey}})
				case "/image/v2/images/image-ubuntu":
					writeJSON(t, w, 200, map[string]any{"id": "image-ubuntu", "name": "Ubuntu", "status": "active", "min_disk": 50})
				case "/compute/v2/flavors/flavor-small":
					writeJSON(t, w, 200, map[string]any{"flavor": map[string]any{"id": "flavor-small", "vcpus": 2, "ram": 4096, "disk": 0}})
				case "/compute/v2/os-quota-sets/fixture-project/detail":
					writeJSON(t, w, 200, map[string]any{"quota_set": map[string]any{"instances": map[string]int{"limit": 2, "in_use": 2, "reserved": 0}, "cores": map[string]int{"limit": 4, "in_use": 4, "reserved": 0}, "ram": map[string]int{"limit": 8192, "in_use": 8192, "reserved": 0}}})
				default:
					t.Errorf("unexpected read %s", r.URL.Path)
					w.WriteHeader(500)
				}
			})
			a.provision = seed.provision
			a.provision.SSHPrivateKeyFile = keyFile
			if err := a.provider.SetTokenAndAuthResult(cidrTestAuth{}); err != nil {
				t.Fatal(err)
			}
			snapshot := validSnapshot()
			snapshot.ProviderConnectionID = "local-provider"
			snapshot.InternetOutbound = false
			snapshot.StartupScript = nil
			snapshot.VMs[0].FlavorSpec = coreprovider.FlavorSpec{VCPUs: 2, RAMMiB: 4096, DiskGiB: 0}
			refs := []coreprovider.ResourceRef{{ResourceType: coreprovider.ResourceTypeTier, ProviderID: "student-tier", Generation: 1, LogicalName: "lab-tier"}, {ResourceType: coreprovider.ResourceTypeTier, ProviderID: "student-management", Generation: 1, LogicalName: "workspace:management-tier"}, {ResourceType: coreprovider.ResourceTypeServer, ProviderID: "student-server", Generation: 1, LogicalName: "workspace"}}
			if foreign {
				refs[0].ProviderID = "teacher-tier"
			}
			request := coreprovider.ResetRequest{Correlation: cidrCorrelation("student", 2), CreationSnapshot: snapshot, ProviderResources: refs}
			_, err = a.withCIDRScope(context.Background(), request.Correlation, true, func(scoped *Adapter) (coreprovider.OperationResult, error) {
				err := scoped.validateCIDRResetOwnership(context.Background(), request, refs)
				if foreign {
					if err == nil {
						t.Fatal("instructor Tier granted student quota credit")
					}
					return coreprovider.OperationResult{}, nil
				}
				if err != nil {
					t.Fatal(err)
				}
				credit, err := scoped.quotaCreditForExistingReset(context.Background(), refs)
				if err != nil || credit.instances != 1 || credit.cores != 2 || credit.ramMiB != 4096 || len(credit.ktTiers) != 2 {
					t.Fatalf("quota credit: %+v %v", credit, err)
				}
				if _, ok := credit.ktTiers["teacher-tier"]; ok {
					t.Fatal("foreign Tier credited")
				}
				if err := scoped.preflightProvision(context.Background(), scoped.provision, snapshot, credit); err != nil {
					t.Fatalf("reset preflight did not use assigned pair and exact credit: %v", err)
				}
				if err := scoped.preflightProvision(context.Background(), scoped.provision, snapshot, quotaUsage{}); err == nil {
					t.Fatal("existing generation CIDRs ignored without exact credit")
				}
				return coreprovider.OperationResult{}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestKTCloudCIDRPoolTierCreateAndCleanupIsolation(t *testing.T) {
	seed := pooledKTAdapter(t, nil)
	claimCIDR(t, seed, "teacher", 1, false)
	tiers := []map[string]any{{"networkId": "teacher-tier", "refId": "teacher-ref", "networkName": ktCloudLabTierName("teacher", 1), "cidr": "172.25.240.0/24", "shared": false, "status": "ACTIVE"}}
	posts, deletes := 0, 0
	a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/compute/v2/nsm/network":
			writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]int{"total": len(tiers), "offset": 0}, "data": tiers})
		case r.Method == "POST" && r.URL.Path == "/compute/v2/nsm/network":
			posts++
			var body struct {
				Name   string            `json:"name"`
				Detail map[string]string `json:"detail"`
			}
			decodeJSON(t, r, &body)
			if body.Detail["gatewayIp"] != "172.25.242.1" {
				t.Error("Tier create used shared global CIDR")
			}
			tiers = append(tiers, map[string]any{"networkId": "student-tier", "refId": "student-ref", "networkName": body.Name, "cidr": "172.25.242.0/24", "shared": false, "status": "ACTIVE"})
			writeJSON(t, w, 201, map[string]any{"httpStatus": 201, "data": map[string]string{"networkId": "student-tier"}})
		case r.Method == "DELETE" && r.URL.Path == "/compute/v2/nsm/network/student-tier":
			deletes++
			tiers = tiers[:1]
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected mutation or read: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	a.provision = seed.provision
	var refs []coreprovider.ResourceRef
	result, _ := a.withCIDRScope(context.Background(), cidrCorrelation("student", 1), false, func(scoped *Adapter) (coreprovider.OperationResult, error) {
		if err := scoped.startCIDRMutation(cidrCorrelation("student", 1)); err != nil {
			t.Fatal(err)
		}
		request := coreprovider.ProvisionRequest{Correlation: cidrCorrelation("student", 1)}
		_, resources, err := scoped.prepareProvisionNetwork(context.Background(), request, scoped.provision)
		if err != nil || len(resources) != 1 {
			t.Fatalf("Tier creation: %v %+v", err, resources)
		}
		refs = append(refs, resources[0].ResourceRef)
		return coreprovider.OperationResult{Outcome: coreprovider.OutcomeSucceeded}, nil
	})
	if result.Outcome != coreprovider.OutcomeSucceeded {
		t.Fatal("create HOLD")
	}
	cleanup, err := a.Cleanup(context.Background(), coreprovider.CleanupRequest{Correlation: cidrCorrelation("student", 1), ProviderResources: refs})
	if err != nil || cleanup.Outcome != coreprovider.OutcomeSucceeded || posts != 1 || deletes != 1 || len(tiers) != 1 || tiers[0]["networkId"] != "teacher-tier" {
		t.Fatalf("cleanup crossed instance boundary: %+v %v", cleanup, err)
	}
	// Confirmed Cleanup never makes this pair available to a third instance.
	result, _ = a.withCIDRScope(context.Background(), cidrCorrelation("third", 1), false, func(*Adapter) (coreprovider.OperationResult, error) {
		t.Fatal("cleaned reservation automatically reused")
		return coreprovider.OperationResult{}, nil
	})
	if result.Outcome != coreprovider.OutcomeUnknown {
		t.Fatal("pool exhaustion did not HOLD")
	}
}

func TestKTCloudCIDRPoolPublicProvisionLostCreateCannotRepeat(t *testing.T) {
	seed := pooledKTAdapter(t, nil)
	claimCIDR(t, seed, "teacher", 1, false)
	keyFile := writeTestPrivateKey(t, "public-cidr-key")
	pub, err := ssh.NewPublicKey(testPrivateKey("public-cidr-key").Public())
	if err != nil {
		t.Fatal(err)
	}
	posts := 0
	a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/compute/v2/nsm/network":
			if r.Method == "POST" {
				posts++
				var body struct {
					Detail map[string]string `json:"detail"`
				}
				decodeJSON(t, r, &body)
				if body.Detail["gatewayIp"] != "172.25.242.1" {
					t.Error("Provision used wrong assigned Lab CIDR")
				}
				w.WriteHeader(503)
				return // Create may have reached NSM; receipt was lost.
			}
			writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]int{"total": 1, "offset": 0}, "data": []any{map[string]any{"networkId": "connector-tier", "refId": "connector-ref", "cidr": "172.25.0.0/24", "status": "ACTIVE"}}})
		case "/compute/v2/nsm/firewall/policy":
			writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]int{"total": 0, "offset": 0}, "data": []any{}})
		case "/compute/v2/os-keypairs/key":
			writeJSON(t, w, 200, map[string]any{"keypair": map[string]any{"name": "key", "public_key": strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))}})
		case "/image/v2/images/image-ubuntu":
			writeJSON(t, w, 200, map[string]any{"id": "image-ubuntu", "name": "Ubuntu", "status": "active", "min_disk": 50})
		case "/compute/v2/flavors/flavor-small":
			writeJSON(t, w, 200, map[string]any{"flavor": map[string]any{"id": "flavor-small", "vcpus": 2, "ram": 4096, "disk": 0}})
		case "/compute/v2/os-quota-sets/fixture-project/detail":
			writeJSON(t, w, 200, map[string]any{"quota_set": map[string]any{"instances": map[string]int{"limit": 100}, "cores": map[string]int{"limit": 100}, "ram": map[string]int{"limit": 100000}}})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	a.provision = seed.provision
	a.provision.SSHPrivateKeyFile = keyFile
	if err := a.provider.SetTokenAndAuthResult(cidrTestAuth{}); err != nil {
		t.Fatal(err)
	}
	snapshot := validSnapshot()
	snapshot.ProviderConnectionID = "local-provider"
	snapshot.InternetOutbound = false
	snapshot.StartupScript = nil
	snapshot.VMs[0].FlavorSpec = coreprovider.FlavorSpec{VCPUs: 2, RAMMiB: 4096, DiskGiB: 0}
	request := coreprovider.ProvisionRequest{Correlation: cidrCorrelation("student", 1), CreationSnapshot: snapshot}
	for i := 0; i < 2; i++ {
		result, err := a.Provision(context.Background(), request)
		if err != nil || result.Outcome != coreprovider.OutcomeUnknown {
			t.Fatalf("uncertain create: %+v %v", result, err)
		}
	}
	if posts != 1 {
		t.Fatalf("blind create retry count %d", posts)
	}
}

func TestKTCloudCIDRPoolResetPreflightRejectsUnmappedInventory(t *testing.T) {
	seed := pooledKTAdapter(t, nil)
	claimCIDR(t, seed, "student", 1, false)
	a := pooledKTAdapter(t, []ktCloudTier{{ID: "pending-tier"}})
	a.provision = seed.provision
	if err := a.provider.SetTokenAndAuthResult(cidrTestAuth{}); err != nil {
		t.Fatal(err)
	}
	a.withCIDRScope(context.Background(), cidrCorrelation("student", 2), true, func(scoped *Adapter) (coreprovider.OperationResult, error) {
		if err := scoped.preflightProvision(context.Background(), scoped.provision, validSnapshot(), quotaUsage{}); err == nil {
			t.Error("RESET may delete old generation despite uncertain inventory")
		}
		return coreprovider.OperationResult{}, nil
	})
}
