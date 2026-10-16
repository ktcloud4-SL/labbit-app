package openstackprovider

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/preview"
	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/terminal"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"golang.org/x/crypto/ssh"
)

// Opt-in disposable LOCAL OpenStack test. HTTP listens only inside the VM;
// the sole access path is production SSHForwarder/direct-tcpip. No Gateway,
// SaaS or KT Cloud acceptance is implied. Teardown uses exact tracked IDs.
func TestOpenStackPreviewForwardingIntegration(t *testing.T) {
	if os.Getenv("LABBIT_OPENSTACK_PREVIEW_TEST") != "1" {
		t.Skip("set LABBIT_OPENSTACK_PREVIEW_TEST=1 for disposable local OpenStack forwarding acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 14*time.Minute)
	defer cancel()
	a, err := New(ctx, ConfigFromEnvironment())
	if err != nil {
		t.Fatal("Provider initialization failed")
	}
	images, err := a.ListImages(ctx)
	if err != nil {
		t.Fatal("image discovery failed")
	}
	flavors, err := a.ListFlavors(ctx)
	if err != nil {
		t.Fatal("flavor discovery failed")
	}
	networks, err := a.ListNetworks(ctx)
	if err != nil {
		t.Fatal("network discovery failed")
	}
	image := exactImage(t, images, environmentOrDefault("LABBIT_OPENSTACK_TEST_IMAGE", "ubuntu"))
	flavor := exactFlavor(t, flavors, environmentOrDefault("LABBIT_OPENSTACK_TEST_FLAVOR", "m1.small"))
	management := exactNetwork(t, networks, environmentOrDefault("LABBIT_OPENSTACK_TEST_MANAGEMENT_NETWORK", "sharednet1"))
	keyFile := a.provision.SSHPrivateKeyFile
	keypair := integrationKeyPairName(t, ctx, a, keyFile)
	allowed := environmentOrDefault("LABBIT_OPENSTACK_TEST_SSH_CIDR", "172.16.8.1/32")
	sg := integrationManagementSecurityGroup(t, ctx, a, allowed)
	a.provision = normalizedProvisionConfig(ProvisionConfig{
		ProviderConnectionID: "local-integration", ProjectID: integrationProjectID(t, a),
		ManagementNetworkID: management.ID, ManagementSecurityGroupID: sg,
		KeyPairName: keypair, SSHAllowedCIDR: allowed,
		LabSubnetCIDR: environmentOrDefault("LABBIT_OPENSTACK_TEST_LAB_CIDR", "198.20.0.0/24"),
		SSHUsername:   a.provision.SSHUsername, SSHPrivateKeyFile: keyFile,
		SSHKnownHostsFile: t.TempDir() + "/known_hosts", PollInterval: 2 * time.Second,
	})
	snapshot := coreprovider.CreationSnapshot{ProviderConnectionID: "local-integration", WorkspaceVMKey: "workspace",
		VMs: []coreprovider.VMSpec{{VMKey: "workspace", Role: "WORKSPACE", ImageID: image.ID, FlavorID: flavor.ID,
			FlavorSpec: coreprovider.FlavorSpec{VCPUs: flavor.VCPUs, RAMMiB: flavor.RAMMiB, DiskGiB: flavor.DiskGiB}}}}
	labID := "lbt173-" + integrationRunID(t)
	var tracked []coreprovider.ResourceResult
	t.Cleanup(func() {
		cleanupM2Resources(t, a, tracked)
		t.Logf("Exact tracked VM/network/port/SG absence verified after cleanup: resources=%d", len(tracked))
	})
	created, err := a.Provision(ctx, coreprovider.ProvisionRequest{
		Correlation: coreprovider.Correlation{OperationID: labID + "-provision", LabInstanceID: labID, Generation: 1}, CreationSnapshot: snapshot,
	})
	tracked = append(tracked, created.ProviderResources...)
	if err != nil || created.Outcome != coreprovider.OutcomeSucceeded {
		t.Fatal("disposable Workspace Provision failed")
	}
	t.Log("Local OpenStack Workspace ACTIVE, authenticated SSH ready and host key enrolled")
	target := vmaccess.Target{LabInstanceID: labID, Generation: 1, VMKey: "workspace", ServerID: providerResourceID(t, created.ProviderResources, coreprovider.ResourceTypeServer, "workspace")}
	access := vmaccess.NewManager(a, vmaccess.Config{IdleTimeout: 100 * time.Millisecond})
	defer access.Close()
	forwarder := &preview.SSHForwarder{Access: access, Validator: a}
	server := realOpenSession(t, ctx, access, target)
	defer server.Close()
	var stdout io.Reader
	if err := server.Setup(ctx, func(raw *ssh.Session) error {
		var err error
		stdout, err = raw.StdoutPipe()
		if err != nil {
			return err
		}
		raw.Stderr = io.Discard
		return raw.Start("exec python3 -u -c 'from http.server import BaseHTTPRequestHandler, HTTPServer; exec(\"class Handler(BaseHTTPRequestHandler):\\n def do_GET(self):\\n  body=b\\\"LBT173-HTTP-OK\\\"; self.send_response(200); self.send_header(\\\"Content-Length\\\",str(len(body))); self.end_headers(); self.wfile.write(body)\\n def log_message(self,*args): pass\"); server=HTTPServer((\"127.0.0.1\",3000),Handler); print(\"READY\",flush=True); server.serve_forever()'")
	}); err != nil {
		t.Fatal("HTTP fixture setup failed")
	}
	ready := make(chan bool, 1)
	go func() {
		line, err := bufio.NewReader(stdout).ReadString('\n')
		ready <- err == nil && line == "READY\n"
	}()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("HTTP fixture did not start")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("HTTP fixture startup timed out")
	}
	pty, err := terminal.NewManagedSSHPTY(ctx, access, target, 80, 24)
	if err != nil {
		t.Fatal("sibling PTY setup failed")
	}
	defer pty.Close()
	sftp, fileSession := workspaceFixtureSFTP(t, ctx, access, target)
	defer fileSession.Close()
	defer sftp.Close()
	identity := realSSHConnection(t, ctx, access, target)
	for range 2 {
		conn, err := forwarder.DialTCP(ctx, target, 3000)
		if err != nil {
			t.Fatalf("SSH forwarding failed: %v", err)
		}
		func() {
			defer conn.Close()
			// Bound a stalled fixture response through the managed stream Close.
			stop := time.AfterFunc(10*time.Second, func() { _ = conn.Close() })
			defer stop.Stop()
			if _, err := fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: fixture\r\nConnection: close\r\n\r\n"); err != nil {
				t.Fatal("forwarded HTTP write failed")
			}
			response, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatal("forwarded HTTP read failed")
			}
			defer response.Body.Close()
			body, err := io.ReadAll(io.LimitReader(response.Body, 128))
			if err != nil || response.StatusCode != 200 || string(body) != "LBT173-HTTP-OK" {
				t.Fatal("forwarded HTTP response mismatch")
			}
		}()
	}
	if identity != realSSHConnection(t, ctx, access, target) {
		t.Fatal("Preview replaced shared SSH transport")
	}
	if _, err := pty.Write([]byte("printf 'LBT147-%s\\n' 'PTY-OK'\n")); err != nil {
		t.Fatal("sibling PTY write failed")
	}
	assertPTYMarker(t, pty)
	if _, err := sftp.Stat("."); err != nil {
		t.Fatal("sibling SFTP broken after Preview close")
	}
	t.Log("SSH direct-tcpip loopback:3000 HTTP 200 exact body twice; same SSH transport; PTY and SFTP survive Preview close PASS")
	for _, bad := range []vmaccess.Target{
		{LabInstanceID: "wrong", Generation: 1, VMKey: target.VMKey, ServerID: target.ServerID},
		{LabInstanceID: labID, Generation: 2, VMKey: target.VMKey, ServerID: target.ServerID},
		{LabInstanceID: labID, Generation: 1, VMKey: "other", ServerID: target.ServerID},
		{LabInstanceID: labID, Generation: 1, VMKey: target.VMKey, ServerID: "missing-server"},
	} {
		if _, err := forwarder.DialTCP(ctx, bad, 3000); !errors.Is(err, preview.ErrVMUnreachable) {
			t.Fatal("invalid real Provider target accepted")
		}
	}
	for _, port := range []int{-1, 0, 22, 65536} {
		if _, err := forwarder.DialTCP(ctx, target, port); !errors.Is(err, preview.ErrPortRejected) {
			t.Fatal("unsafe Preview port accepted")
		}
	}
	if _, err := forwarder.DialTCP(ctx, target, 3001); !errors.Is(err, preview.ErrAppNotRunning) {
		t.Fatal("closed application port not classified")
	}
	t.Log("Wrong LabInstance/generation/VM/server rejected; invalid/22 ports rejected; real closed app port -> APP_NOT_RUNNING PASS")
	// Actual reset supplies a positive older generation and a replaced Server ID.
	_ = sftp.Close()
	_ = fileSession.Close()
	_ = pty.Close()
	_ = server.Close()
	access.InvalidateBefore(labID, 2)
	reset, err := a.Reset(ctx, coreprovider.ResetRequest{Correlation: coreprovider.Correlation{OperationID: labID + "-reset", LabInstanceID: labID, Generation: 2}, CreationSnapshot: snapshot, ProviderResources: resourceRefs(created.ProviderResources, 1)})
	tracked = append(tracked, reset.ProviderResources...)
	if err != nil || reset.Outcome != coreprovider.OutcomeSucceeded {
		t.Fatal("disposable Workspace Reset failed")
	}
	if _, err := forwarder.DialTCP(ctx, target, 3000); !errors.Is(err, preview.ErrVMUnreachable) {
		t.Fatal("old generation target accepted after Reset")
	}
	newTarget := vmaccess.Target{LabInstanceID: labID, Generation: 2, VMKey: "workspace", ServerID: providerResourceID(t, resourceResultsForGeneration(reset.ProviderResources, 2), coreprovider.ResourceTypeServer, "workspace")}
	if _, err := forwarder.DialTCP(ctx, newTarget, 3000); !errors.Is(err, preview.ErrAppNotRunning) {
		t.Fatal("new VM was not independently reached")
	}
	t.Log("Real Reset gen1->gen2: stale target rejected; new VM fresh SSH and closed application port confirmed PASS")
}
