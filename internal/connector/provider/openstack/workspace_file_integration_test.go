package openstackprovider

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/terminal"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/workspacefile"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Disposable local OpenStack only. The unrestricted SFTP client below installs
// fixtures/helper; every acceptance operation uses the production rooted Service.
// File paths/bytes, private endpoints, keys and raw errors are never logged.
func TestOpenStackWorkspaceFileIntegration(t *testing.T) {
	if os.Getenv("LABBIT_OPENSTACK_WORKSPACE_FILE_TEST") != "1" {
		t.Skip("set LABBIT_OPENSTACK_WORKSPACE_FILE_TEST=1 for disposable local OpenStack acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	binary, err := os.ReadFile(os.Getenv("LABBIT_WORKSPACE_SFTP_HELPER_FILE"))
	if err != nil || len(binary) < 4 || string(binary[:4]) != "\x7fELF" {
		t.Fatal("Linux helper build is required")
	}
	a, err := New(ctx, ConfigFromEnvironment())
	if err != nil {
		t.Fatal("Provider initialization failed")
	}
	images, err := a.ListImages(ctx)
	if err != nil {
		t.Fatal("Image discovery failed")
	}
	flavors, err := a.ListFlavors(ctx)
	if err != nil {
		t.Fatal("Flavor discovery failed")
	}
	networks, err := a.ListNetworks(ctx)
	if err != nil {
		t.Fatal("Network discovery failed")
	}
	image := exactImage(t, images, environmentOrDefault("LABBIT_OPENSTACK_TEST_IMAGE", "ubuntu"))
	flavor := exactFlavor(t, flavors, environmentOrDefault("LABBIT_OPENSTACK_TEST_FLAVOR", "m1.small"))
	management := exactNetwork(t, networks, environmentOrDefault("LABBIT_OPENSTACK_TEST_MANAGEMENT_NETWORK", "sharednet1"))
	keyFile := a.provision.SSHPrivateKeyFile
	keypair := integrationKeyPairName(t, ctx, a, keyFile)
	allowed := environmentOrDefault("LABBIT_OPENSTACK_TEST_SSH_CIDR", "172.16.8.1/32")
	sg := integrationManagementSecurityGroup(t, ctx, a, allowed)
	a.provision = normalizedProvisionConfig(ProvisionConfig{ProviderConnectionID: "local-integration", ProjectID: integrationProjectID(t, a), ManagementNetworkID: management.ID, ManagementSecurityGroupID: sg, KeyPairName: keypair, SSHAllowedCIDR: allowed, LabSubnetCIDR: environmentOrDefault("LABBIT_OPENSTACK_TEST_LAB_CIDR", "198.20.0.0/24"), SSHUsername: a.provision.SSHUsername, SSHPrivateKeyFile: keyFile, SSHKnownHostsFile: t.TempDir() + "/known_hosts", PollInterval: 2 * time.Second})
	snapshot := coreprovider.CreationSnapshot{ProviderConnectionID: "local-integration", WorkspaceVMKey: "workspace", VMs: []coreprovider.VMSpec{{VMKey: "workspace", Role: "WORKSPACE", ImageID: image.ID, FlavorID: flavor.ID, FlavorSpec: coreprovider.FlavorSpec{VCPUs: flavor.VCPUs, RAMMiB: flavor.RAMMiB, DiskGiB: flavor.DiskGiB}}}}
	labID := "lbt149-" + integrationRunID(t)
	var tracked []coreprovider.ResourceResult
	t.Cleanup(func() {
		cleanupM2Resources(t, a, tracked)
		t.Logf("Tracked resource absence verified: resources=%d", len(tracked))
	})
	created, err := a.Provision(ctx, coreprovider.ProvisionRequest{Correlation: coreprovider.Correlation{OperationID: labID + "-provision", LabInstanceID: labID, Generation: 1}, CreationSnapshot: snapshot})
	tracked = append(tracked, created.ProviderResources...)
	if err != nil || created.Outcome != coreprovider.OutcomeSucceeded {
		t.Fatal("Disposable Workspace Provision failed")
	}
	target := vmaccess.Target{LabInstanceID: labID, Generation: 1, VMKey: "workspace", ServerID: providerResourceID(t, created.ProviderResources, coreprovider.ResourceTypeServer, "workspace")}
	access := vmaccess.NewManager(a, vmaccess.Config{IdleTimeout: time.Second})
	defer access.Close()
	fixture, session := workspaceFixtureSFTP(t, ctx, access, target)
	defer session.Close()
	defer fixture.Close()
	root := "/var/lib/labbit-workspaces/workspace"
	helper := "/usr/local/libexec/labbit-workspace-sftp"
	workspaceFixtureWrite(t, fixture, "/home/ubuntu/helper-upload", binary)
	workspaceFixtureExec(t, ctx, access, target, "sudo -n install -d -o root -g root -m 0755 /usr/local/libexec /var/lib/labbit-workspaces && sudo -n install -o root -g root -m 0755 /home/ubuntu/helper-upload /usr/local/libexec/labbit-workspace-sftp && sudo -n install -d -o ubuntu -g ubuntu -m 0750 /var/lib/labbit-workspaces/workspace && mkdir /var/lib/labbit-workspaces/workspace/dir")
	workspaceFixtureWrite(t, fixture, root+"/file", []byte("initial"))
	workspaceFixtureWrite(t, fixture, root+"/empty", nil)
	workspaceFixtureWrite(t, fixture, "/home/ubuntu/outside", []byte("outside-fixture"))
	if err = fixture.Symlink("/home/ubuntu/outside", root+"/link"); err != nil {
		t.Fatal("symlink fixture failed")
	}
	if err = fixture.Symlink("/home/ubuntu", root+"/escape"); err != nil {
		t.Fatal("ancestor symlink fixture failed")
	}
	workspaceFixtureExec(t, ctx, access, target, "sudo sh -c 'printf denied > /var/lib/labbit-workspaces/workspace/denied; chmod 000 /var/lib/labbit-workspaces/workspace/denied'; mkfifo /var/lib/labbit-workspaces/workspace/fifo")
	s, err := workspacefile.New(access, a, workspacefile.Config{Root: root, HelperPath: helper, Timeout: 3 * time.Second, MaxFileBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := s.Tree(ctx, target, "")
	if err != nil || len(entries) != 4 {
		t.Fatal("Tree regular/direct-child filtering failed")
	}
	read, err := s.Read(ctx, target, "file", 64)
	if err != nil || !bytes.Equal(read.Content, []byte("initial")) {
		t.Fatal("Read failed")
	}
	newRev, err := s.Save(ctx, target, "file", read.Revision, []byte("new"))
	if err != nil {
		t.Fatal("Save failed")
	}
	readBack, err := s.Read(ctx, target, "file", 64)
	if err != nil || string(readBack.Content) != "new" || readBack.Revision != newRev {
		t.Fatal("Read/Save/revision/truncation mismatch")
	}
	_, err = s.Save(ctx, target, "file", read.Revision, []byte("stale"))
	workspaceAssertError(t, err, workspacefile.ErrRevisionConflict)
	_, err = s.Save(ctx, target, "file", newRev, nil)
	if err != nil {
		t.Fatal("empty Save failed")
	}
	empty, err := s.Read(ctx, target, "file", 1)
	if err != nil || len(empty.Content) != 0 {
		t.Fatal("empty Read failed")
	}
	for _, p := range []string{"../outside", "/home/ubuntu/outside", "dir/../file", "link", "escape/outside"} {
		_, err = s.Read(ctx, target, p, 64)
		workspaceAssertError(t, err, workspacefile.ErrInvalidPath)
	}
	for _, p := range []string{"link", "escape/outside"} {
		_, err = s.Save(ctx, target, p, read.Revision, []byte("no"))
		workspaceAssertError(t, err, workspacefile.ErrInvalidPath)
	}
	_, err = s.Read(ctx, target, "denied", 64)
	workspaceAssertError(t, err, workspacefile.ErrPermissionDenied)
	_, err = s.Save(ctx, target, "denied", read.Revision, nil)
	workspaceAssertError(t, err, workspacefile.ErrPermissionDenied)
	_, err = s.Read(ctx, target, "dir", 64)
	workspaceAssertError(t, err, workspacefile.ErrNotAFile)
	_, err = s.Tree(ctx, target, "file")
	workspaceAssertError(t, err, workspacefile.ErrNotADirectory)
	_, err = s.Read(ctx, target, "fifo", 64)
	workspaceAssertError(t, err, workspacefile.ErrNotAFile)
	_, err = s.Save(ctx, target, "missing", read.Revision, nil)
	workspaceAssertError(t, err, workspacefile.ErrNotFound)
	if _, err = fixture.Stat(root + "/missing"); !os.IsNotExist(err) {
		t.Fatal("Save created missing file")
	}
	workspaceFixtureWrite(t, fixture, root+"/large", bytes.Repeat([]byte{'x'}, 65))
	_, err = s.Read(ctx, target, "large", 64)
	workspaceAssertError(t, err, workspacefile.ErrTooLarge)
	_, err = s.Save(ctx, target, "file", empty.Revision, bytes.Repeat([]byte{'x'}, 65))
	workspaceAssertError(t, err, workspacefile.ErrTooLarge)
	for _, bad := range []vmaccess.Target{{LabInstanceID: "wrong", Generation: 1, VMKey: target.VMKey, ServerID: target.ServerID}, {LabInstanceID: labID, Generation: 2, VMKey: target.VMKey, ServerID: target.ServerID}, {LabInstanceID: labID, Generation: 1, VMKey: "other", ServerID: target.ServerID}} {
		_, err = s.Read(ctx, bad, "file", 64)
		workspaceAssertError(t, err, workspacefile.ErrUnavailable)
	}
	t.Log("Real Tree/Read/Save, empty bytes, revision conflict, no create, size/permission/type/path/symlink and target failures PASS")
	// The same source's VM-side regression binary exercises Linux fd-root,
	// symlink swap, inode replacement, cross-helper flock and listing budgets.
	testBinary, err := os.ReadFile(os.Getenv("LABBIT_WORKSPACE_SFTP_TEST_BINARY"))
	if err != nil {
		t.Fatal("Linux helper regression binary required")
	}
	workspaceFixtureWrite(t, fixture, "/home/ubuntu/workspacefs-tests", testBinary)
	if fixture.Chmod("/home/ubuntu/workspacefs-tests", 0700) != nil {
		t.Fatal("VM regression installation failed")
	}
	probe := realOpenSession(t, ctx, access, target)
	var testOutput []byte
	if err = probe.Setup(ctx, func(raw *ssh.Session) error {
		var err error
		testOutput, err = raw.Output("/home/ubuntu/workspacefs-tests -test.v")
		return err
	}); err != nil {
		probe.Close()
		t.Fatal("VM-side Linux regression failed")
	}
	probe.Close()
	t.Log(string(testOutput))
	pty, err := terminal.NewManagedSSHPTY(ctx, access, target, 80, 24)
	if err != nil {
		t.Fatal("PTY failed")
	}
	defer pty.Close()
	connection := realSSHConnection(t, ctx, access, target)
	if _, err = s.Read(ctx, target, "file", 64); err != nil {
		t.Fatal("SFTP while PTY active failed")
	}
	request, cancelRequest := context.WithCancel(ctx)
	cancelRequest()
	_, err = s.Read(request, target, "file", 64)
	workspaceAssertError(t, err, context.Canceled)
	// An exec fixture accepts SSH but never serves SFTP: exercise active
	// initialization cancellation/timeout without changing the shared transport.
	stall := "/home/ubuntu/stall-workspace-sftp"
	workspaceFixtureWrite(t, fixture, stall, []byte("#!/bin/sh\nsleep 30\n"))
	if err = fixture.Chmod(stall, 0700); err != nil {
		t.Fatal("stall fixture failed")
	}
	stalled, err := workspacefile.New(access, a, workspacefile.Config{Root: root, HelperPath: stall, Timeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	_, err = stalled.Read(ctx, target, "file", 64)
	workspaceAssertError(t, err, context.DeadlineExceeded)
	if connection != realSSHConnection(t, ctx, access, target) {
		t.Fatal("SFTP timeout replaced PTY SSH transport")
	}
	if _, err = pty.Write([]byte("printf 'LBT147-%s\\n' 'PTY-OK'\n")); err != nil {
		t.Fatal("PTY input failed after SFTP timeout")
	}
	assertPTYMarker(t, pty)
	pty.Close()
	if _, err = s.Read(ctx, target, "file", 64); err != nil {
		t.Fatal("SFTP failed after PTY close")
	}
	outside, err := fixture.Open("/home/ubuntu/outside")
	if err != nil {
		t.Fatal("outside fixture verification failed")
	}
	outsideBody, err := io.ReadAll(outside)
	outside.Close()
	if err != nil || string(outsideBody) != "outside-fixture" {
		t.Fatal("outside file changed")
	}
	access.InvalidateBefore(labID, 2)
	_, err = s.Read(ctx, target, "file", 64)
	workspaceAssertError(t, err, workspacefile.ErrUnavailable)
	t.Log("Shared real SSH: SFTP timeout preserves PTY; PTY close preserves File; stale generation rejects; outside fixture unchanged PASS")
}
func workspaceFixtureSFTP(t *testing.T, ctx context.Context, m *vmaccess.Manager, target vmaccess.Target) (*sftp.Client, *vmaccess.Session) {
	t.Helper()
	session := realOpenSession(t, ctx, m, target)
	var in io.WriteCloser
	var out io.Reader
	if err := session.Setup(ctx, func(raw *ssh.Session) error {
		var err error
		in, err = raw.StdinPipe()
		if err != nil {
			return err
		}
		out, err = raw.StdoutPipe()
		if err != nil {
			return err
		}
		return raw.RequestSubsystem("sftp")
	}); err != nil {
		t.Fatal("fixture SFTP setup failed")
	}
	client, err := sftp.NewClientPipe(out, in)
	if err != nil {
		t.Fatal("fixture SFTP init failed")
	}
	return client, session
}
func workspaceFixtureWrite(t *testing.T, c *sftp.Client, p string, b []byte) {
	t.Helper()
	f, err := c.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		t.Fatal("fixture open failed")
	}
	_, err = f.Write(b)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatal("fixture write failed")
	}
}
func workspaceFixtureExec(t *testing.T, ctx context.Context, m *vmaccess.Manager, target vmaccess.Target, command string) {
	t.Helper()
	s := realOpenSession(t, ctx, m, target)
	defer s.Close()
	var output []byte
	if err := s.Setup(ctx, func(raw *ssh.Session) error { var e error; output, e = raw.CombinedOutput(command); return e }); err != nil {
		// Only fixed installation fixtures are executed here. Remove their paths
		// before reporting diagnostics; never print Provider/connection errors.
		diagnostic := strings.NewReplacer("/home/ubuntu", "<fixture>", "/usr/local/libexec", "<helper-dir>", "/var/lib/labbit-workspaces", "<root-dir>").Replace(string(output))
		if len(diagnostic) > 512 {
			diagnostic = diagnostic[:512]
		}
		t.Fatalf("fixture setup exec failed: %s", diagnostic)
	}
}
func workspaceAssertError(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("safe error=%v expected=%v", err, want)
	}
}
