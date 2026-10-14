package openstackprovider

import (
	"context"
	"errors"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"
)

func TestTerminalTargetRejectsInvalidInputsBeforeCloudLookup(t *testing.T) {
	a := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("invalid target reached cloud: %s", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	a.provision.ProjectID, a.provision.ManagementNetworkID = "project-1", "management"
	for _, target := range [][2]string{{"", "server-1"}, {"workspace", ""}, {"  ", "server-1"}} {
		if address, err := a.ResolveServerAddress(context.Background(), target[0], target[1]); err == nil || address != "" {
			t.Fatalf("invalid target accepted: address=%q err=%v", address, err)
		}
	}
	var absent *Adapter
	if address, err := absent.ResolveServerAddress(context.Background(), "workspace", "server-1"); err == nil || address != "" {
		t.Fatalf("nil Provider accepted: address=%q err=%v", address, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.ResolveServerAddress(ctx, "workspace", "server-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled lookup: %v", err)
	}
	if _, err := a.SSHHostKeyCallback(ctx, "server-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled pin load: %v", err)
	}
	if _, err := absent.SSHHostKeyCallback(context.Background(), "server-1"); err == nil {
		t.Fatal("nil Provider accepted for host-key verification")
	}
	if _, err := a.SSHHostKeyCallback(context.Background(), ""); err == nil {
		t.Fatal("empty server ID accepted for host-key verification")
	}
}

func TestWorkspaceDesignationFailsClosed(t *testing.T) {
	for _, name := range []string{"workspace", "worker", "legacy-missing", "wrong-vm"} {
		t.Run(name, func(t *testing.T) {
			metadata := map[string]string{"labbit_vm_key": "workspace", "labbit_workspace_vm_key": "workspace"}
			switch name {
			case "worker":
				metadata["labbit_workspace_vm_key"] = "other"
			case "legacy-missing":
				delete(metadata, "labbit_workspace_vm_key")
			case "wrong-vm":
				metadata["labbit_vm_key"] = "other"
			}
			a := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/compute/v2/servers/server-1" {
					t.Fatal("Workspace validation mutated resources or resolved a different server")
				}
				writeJSON(t, w, 200, map[string]any{"server": servers.Server{ID: "server-1", Metadata: metadata}})
			}))
			err := a.ValidateWorkspace(context.Background(), vmaccess.Target{LabInstanceID: "lab-1", Generation: 1, VMKey: "workspace", ServerID: "server-1"})
			if name == "workspace" && err != nil {
				t.Fatal("designated Workspace rejected")
			}
			if name != "workspace" && !errors.Is(err, vmaccess.ErrInvalidTarget) {
				t.Fatal("undesignated Workspace accepted")
			}
		})
	}
}

func TestVMAccessRejectsMismatchedEnvelopeBeforeManagementLookup(t *testing.T) {
	for _, kind := range []string{"other-lab", "stale-generation", "future-generation"} {
		t.Run(kind, func(t *testing.T) {
			a := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/compute/v2/servers/server-1" {
					t.Errorf("mismatched envelope reached NIC lookup: %s", r.URL.Path)
					w.WriteHeader(500)
					return
				}
				writeJSON(t, w, 200, map[string]any{"server": servers.Server{ID: "server-1", TenantID: "project-1", Status: "ACTIVE", Name: provisionBaseName("lab-1", 2) + "-workspace", Metadata: map[string]string{"labbit_lab_instance_id": "lab-1", "labbit_generation": "2", "labbit_operation_id": "op-1", "labbit_vm_key": "workspace"}}})
			}))
			a.provision.ProjectID, a.provision.ManagementNetworkID = "project-1", "management"
			target := vmaccess.Target{LabInstanceID: "lab-1", Generation: 2, VMKey: "workspace", ServerID: "server-1"}
			switch kind {
			case "other-lab":
				target.LabInstanceID = "lab-2"
			case "stale-generation":
				target.Generation = 1
			case "future-generation":
				target.Generation = 3
			}
			if endpoint, err := a.ResolveVMAccess(context.Background(), target); !errors.Is(err, vmaccess.ErrInvalidTarget) || endpoint.Address != "" {
				t.Fatalf("mismatched envelope accepted: %v", err)
			}
		})
	}
}

func TestKTCloudVMAccessPreservesTargetAffinityAndManagementTier(t *testing.T) {
	for _, tc := range []struct {
		name       string
		generation int64
		wantErr    error
		wantReads  int
	}{
		{name: "valid", generation: 2, wantReads: 1},
		{name: "stale-generation", generation: 1, wantErr: vmaccess.ErrInvalidTarget},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const labID, vmKey = "lab-1", "workspace"
			tierName := ktCloudManagementTierName(labID, 2, vmKey)
			tierReads := 0
			a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Fatalf("VM access mutated resources: %s %s", r.Method, r.URL.Path)
				}
				switch r.URL.Path {
				case "/compute/v2/servers/server-1":
					writeJSON(t, w, http.StatusOK, map[string]any{"server": map[string]any{
						"id": "server-1", "tenant_id": "fixture-project", "status": "ACTIVE",
						"name":      provisionBaseName(labID, 2) + "-" + vmKey,
						"metadata":  map[string]string{"labbit_lab_instance_id": labID, "labbit_generation": "2", "labbit_operation_id": "op-1", "labbit_vm_key": vmKey},
						"addresses": map[string]any{tierName: []any{map[string]any{"addr": "172.25.241.10", "OS-EXT-IPS:type": "fixed"}}},
					}})
				case "/compute/v2/nsm/network":
					tierReads++
					writeJSON(t, w, http.StatusOK, map[string]any{"httpStatus": 200, "pagination": map[string]any{"total": 1, "offset": 0}, "data": []any{map[string]any{
						"networkId": "management-id", "refId": "management-ref", "networkName": tierName,
						"cidr": "172.25.241.0/24", "status": "ACTIVE", "shared": false,
					}}})
				default:
					t.Fatalf("unexpected VM access lookup: %s", r.URL.Path)
				}
			})
			pins := filepath.Join(t.TempDir(), "known_hosts")
			if err := os.WriteFile(pins, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			a.provision.SSHKnownHostsFile = pins
			target := vmaccess.Target{LabInstanceID: labID, Generation: tc.generation, VMKey: vmKey, ServerID: "server-1"}
			endpoint, err := a.ResolveVMAccess(context.Background(), target)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) || endpoint.Address != "" {
					t.Fatalf("unsafe target result: endpoint=%+v err=%v", endpoint, err)
				}
			} else if err != nil || endpoint.Address != "172.25.241.10:22" || endpoint.ProviderConnectionID != "local-provider" || endpoint.HostKeyCallback == nil {
				t.Fatalf("valid KT VM access endpoint=%+v err=%v", endpoint, err)
			}
			if tierReads != tc.wantReads {
				t.Fatalf("management Tier reads=%d want=%d", tierReads, tc.wantReads)
			}
		})
	}
}

func TestResolveServerAddressManagementOnly(t *testing.T) {
	for _, name := range []string{"valid", "wrong-vm", "wrong-project", "missing-metadata", "wrong-name", "not-active", "no-port", "two-ports", "lab-port", "other-server-port", "other-project-port", "no-ip", "two-ips"} {
		t.Run(name, func(t *testing.T) {
			server := servers.Server{ID: "server-1", TenantID: "project-1", Status: "ACTIVE", Name: provisionBaseName("lab-1", 1) + "-workspace", Metadata: map[string]string{
				"labbit_lab_instance_id": "lab-1", "labbit_generation": "1", "labbit_operation_id": "op-1", "labbit_vm_key": "workspace",
			}}
			port := ports.Port{ID: "port-1", DeviceID: "server-1", DeviceOwner: "compute:nova", TenantID: "project-1", NetworkID: "management", FixedIPs: []ports.IP{{IPAddress: "192.0.2.10"}}}
			portList := []ports.Port{port}
			switch name {
			case "wrong-vm":
				server.Metadata["labbit_vm_key"] = "worker"
			case "wrong-project":
				server.TenantID = "other"
			case "missing-metadata":
				server.Metadata = nil
			case "wrong-name":
				server.Name = "unowned-server"
			case "not-active":
				server.Status = "BUILD"
			case "no-port":
				portList = nil
			case "two-ports":
				portList = append(portList, port)
			case "lab-port":
				portList[0].NetworkID = "lab"
			case "other-server-port":
				portList[0].DeviceID = "other-server"
			case "other-project-port":
				portList[0].TenantID = "other"
			case "no-ip":
				portList[0].FixedIPs = nil
			case "two-ips":
				portList[0].FixedIPs = append(portList[0].FixedIPs, ports.IP{IPAddress: "192.0.2.11"})
			}
			a := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/compute/v2/servers/server-1":
					writeJSON(t, w, 200, map[string]any{"server": server})
				case "/network/v2.0/ports":
					if r.URL.Query().Get("device_id") != "server-1" || r.URL.Query().Get("network_id") != "management" {
						t.Error("management filter missing")
					}
					writeJSON(t, w, 200, map[string]any{"ports": portList})
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			a.provision.ProjectID, a.provision.ManagementNetworkID = "project-1", "management"
			address, err := a.ResolveServerAddress(context.Background(), "workspace", "server-1")
			if name == "valid" {
				if err != nil || address != "192.0.2.10" {
					t.Fatalf("address=%q err=%v", address, err)
				}
			} else if err == nil || address != "" {
				t.Fatalf("invalid target accepted: %q %v", address, err)
			}
		})
	}
}

func TestTerminalHostKeyUsesExistingServerPin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	key := testHostPublicKey(t)
	identity, _ := sshHostKeyIdentity("provider-1", "server-1")
	remote := &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 22}
	enroll, err := trustOnFirstUseCallback(path, identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := enroll("192.0.2.10:22", remote, key); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	a := &Adapter{provision: ProvisionConfig{ProviderConnectionID: "provider-1", SSHKnownHostsFile: path}}
	verify, err := a.SSHHostKeyCallback(context.Background(), "server-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := verify("192.0.2.10:22", remote, key); err != nil {
		t.Fatalf("lifecycle pin rejected: %v", err)
	}
	if err := verify("192.0.2.10:22", remote, testHostPublicKey(t)); err == nil {
		t.Fatal("changed key accepted")
	}
	unknown, err := a.SSHHostKeyCallback(context.Background(), "server-2")
	if err != nil {
		t.Fatal(err)
	}
	if err := unknown("192.0.2.10:22", remote, key); err == nil {
		t.Fatal("IP reuse accepted an unpinned server")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("terminal access enrolled a new key")
	}
}
