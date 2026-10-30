package openstackprovider

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"golang.org/x/crypto/ssh"
)

// Real public Provider calls against loopback HTTP, with only SSH readiness
// probes faked. No OpenStack credentials, cloud VM or network is used.
func TestKTCloudCIDRPublicLifecycleReturnsCompleteTopology(t *testing.T) {
	seed := pooledKTAdapter(t, nil)
	key := testPrivateKey("cidr-lifecycle")
	pub, err := ssh.NewPublicKey(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	tiers := map[string]map[string]any{"connector-tier": {"networkId": "connector-tier", "networkName": "connector", "refId": "connector-ref", "cidr": "172.25.0.0/24", "status": "ACTIVE", "shared": false}}
	policies := map[string]any{}
	var server map[string]any
	var managementName string
	var sequence int
	volumePresent := false
	deleteOrder := []string{}
	a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case path == "/compute/v2/nsm/network" && r.Method == "GET":
			data := []any{}
			for _, tier := range tiers {
				data = append(data, tier)
			}
			writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]int{"total": len(data), "offset": 0}, "data": data})
		case path == "/compute/v2/nsm/network" && r.Method == "POST":
			var body struct {
				Name   string            `json:"name"`
				Detail map[string]string `json:"detail"`
			}
			decodeJSON(t, r, &body)
			sequence++
			id := fmt.Sprintf("tier-%d", sequence)
			cidr := strings.TrimSuffix(body.Detail["gatewayIp"], ".1") + ".0/24"
			tiers[id] = map[string]any{"networkId": id, "networkName": body.Name, "refId": id + "-ref", "cidr": cidr, "status": "ACTIVE", "shared": false}
			if strings.Contains(body.Name, "management") || cidr == "172.25.241.0/24" {
				managementName = body.Name
			}
			writeJSON(t, w, 201, map[string]any{"httpStatus": 201, "data": map[string]string{"networkId": id}})
		case path == "/compute/v2/nsm/firewall/policy" && r.Method == "GET":
			data := []any{}
			for _, policy := range policies {
				data = append(data, policy)
			}
			writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]int{"total": len(data), "offset": 0}, "data": data})
		case path == "/compute/v2/nsm/firewall/policy" && r.Method == "POST":
			var spec ktCloudFirewallSpec
			decodeJSON(t, r, &spec)
			policies["policy"] = ktCloudFirewallPolicy{ID: "policy", Action: "accept", Status: "enable", Comment: spec.Comment, NAT: "disable", Source: []ktCloudInterface{{ID: spec.Source[0]}}, Destination: []ktCloudInterface{{ID: spec.Destination[0]}}, SourceIPs: []ktCloudAddress{{Name: spec.SourceIPs[0]}}, DestinationIPs: []ktCloudAddress{{Name: "all"}}, Services: []ktCloudService{{Protocol: "TCP", Start: "22", End: "22"}}}
			writeJSON(t, w, 202, map[string]any{"httpStatus": 202, "jobId": "job"})
		case path == "/compute/v2/nsm/job/status/job":
			writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "jobStatus": "SUCCESS", "jobId": "job"})
		case path == "/compute/v2/servers" && r.Method == "POST":
			var body struct {
				Server map[string]any `json:"server"`
			}
			decodeJSON(t, r, &body)
			server = map[string]any{"id": "server", "name": body.Server["name"], "status": "ACTIVE", "tenant_id": "fixture-project", "metadata": body.Server["metadata"], "os-extended-volumes:volumes_attached": []any{map[string]string{"id": "root"}}, "addresses": map[string]any{managementName: []any{map[string]string{"addr": "172.25.241.10", "OS-EXT-IPS:type": "fixed"}}}}
			volumePresent = true
			writeJSON(t, w, 202, map[string]any{"server": server})
		case r.Method == "DELETE":
			deleteOrder = append(deleteOrder, path)
			switch {
			case path == "/compute/v2/servers/server":
				server = nil
			case path == "/compute/v2/volume/volumes/root":
				volumePresent = false
			case path == "/compute/v2/nsm/firewall/policy/policy":
				delete(policies, "policy")
			case strings.HasPrefix(path, "/compute/v2/nsm/network/"):
				delete(tiers, strings.TrimPrefix(path, "/compute/v2/nsm/network/"))
			default:
				t.Errorf("unexpected deletion %s", path)
			}
			w.WriteHeader(204)
		case path == "/compute/v2/servers/server":
			if server == nil {
				w.WriteHeader(404)
			} else {
				writeJSON(t, w, 200, map[string]any{"server": server})
			}
		case path == "/compute/v2/servers/detail":
			data := []any{}
			if server != nil {
				data = append(data, server)
			}
			writeJSON(t, w, 200, map[string]any{"servers": data})
		case path == "/compute/v2/volume/volumes/root":
			if !volumePresent {
				w.WriteHeader(404)
				return
			}
			attachments := []any{}
			if server != nil {
				attachments = append(attachments, map[string]string{"server_id": "server"})
			}
			writeJSON(t, w, 200, map[string]any{"volume": map[string]any{"id": "root", "status": "available", "size": 50, "volume_image_metadata": map[string]string{"image_id": "image-ubuntu"}, "attachments": attachments}})
		case path == "/compute/v2/os-keypairs/key":
			writeJSON(t, w, 200, map[string]any{"keypair": map[string]any{"name": "key", "public_key": strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))}})
		case path == "/image/v2/images/image-ubuntu":
			writeJSON(t, w, 200, map[string]any{"id": "image-ubuntu", "name": "Ubuntu", "status": "active", "min_disk": 50})
		case path == "/compute/v2/flavors/flavor-small":
			writeJSON(t, w, 200, map[string]any{"flavor": map[string]any{"id": "flavor-small", "vcpus": 2, "ram": 4096, "disk": 0}})
		case path == "/compute/v2/os-quota-sets/fixture-project/detail":
			writeJSON(t, w, 200, map[string]any{"quota_set": map[string]any{"instances": map[string]int{"limit": 100}, "cores": map[string]int{"limit": 100}, "ram": map[string]int{"limit": 100000}}})
		default:
			t.Errorf("unexpected fixture request %s %s", r.Method, path)
			w.WriteHeader(500)
		}
	})
	a.provision = seed.provision
	a.provision.SSHPrivateKeyFile = writeTestPrivateKey(t, "cidr-lifecycle")
	a.sshProbe = func(context.Context, string) error { return nil }
	a.startupProbe = func(context.Context, string, string) error { return nil }
	if err := a.provider.SetTokenAndAuthResult(cidrTestAuth{}); err != nil {
		t.Fatal(err)
	}
	snapshot := validSnapshot()
	snapshot.ProviderConnectionID = "local-provider"
	snapshot.InternetOutbound = false
	snapshot.StartupScript = nil
	snapshot.VMs[0].FlavorSpec = coreprovider.FlavorSpec{VCPUs: 2, RAMMiB: 4096, DiskGiB: 0}
	for _, owner := range []string{"old", "new"} {
		correlation := cidrCorrelation(owner, 1)
		result, err := a.Provision(context.Background(), coreprovider.ProvisionRequest{Correlation: correlation, CreationSnapshot: snapshot})
		if err != nil || result.Outcome != coreprovider.OutcomeSucceeded || len(result.ProviderResources) != 5 {
			t.Fatalf("public Provision %s: %+v safeError=%+v err=%v", owner, result, result.Error, err)
		}
		pair, err := a.loadCIDRAssignment(owner)
		if err != nil || pair.Lab != "172.25.240.0/24" {
			t.Fatal("public Provision did not reuse first pair")
		}
		var completion ktCIDRCompletion
		if readCIDRFile(filepath.Join(a.provision.KTCloudCIDRStateDir, completionName(0, 1)), &completion) != nil || len(completion.Resources) != 5 {
			t.Fatal("full topology not durable")
		}
		refs := cidrResultRefs(result.ProviderResources)
		before := len(deleteOrder)
		result, err = a.Cleanup(context.Background(), coreprovider.CleanupRequest{Correlation: correlation, ProviderResources: refs})
		if err != nil || result.Outcome != coreprovider.OutcomeSucceeded || len(deleteOrder)-before != 5 {
			t.Fatalf("public Cleanup %s: %+v err=%v", owner, result, err)
		}
		if _, err := a.loadCIDRAssignment(owner); err == nil {
			t.Fatal("reservation not returned")
		}
		expected := []string{"/compute/v2/servers/server", "/compute/v2/volume/volumes/root", "/compute/v2/nsm/firewall/policy/policy"}
		if !reflect.DeepEqual(deleteOrder[before:before+3], expected) {
			t.Fatal("cleanup order changed")
		}
		if len(tiers) != 1 || tiers["connector-tier"] == nil {
			t.Fatal("shared connector tier changed")
		}
	}
}
