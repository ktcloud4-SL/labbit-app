package openstackprovider

import (
	"context"
	"errors"
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
