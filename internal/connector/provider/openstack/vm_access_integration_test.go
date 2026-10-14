package openstackprovider

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/terminal"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"golang.org/x/crypto/ssh"
)

// Opt-in: creates two successive VM generations through OP-01, cleans only
// their tracked IDs, and verifies absence. No VM output or SSH raw errors log.
func TestOpenStackVMAccessIntegration(t *testing.T) {
	if os.Getenv("LABBIT_OPENSTACK_VM_ACCESS_TEST") != "1" {
		t.Skip("set LABBIT_OPENSTACK_VM_ACCESS_TEST=1 for real local OpenStack VM access acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	a, err := New(ctx, ConfigFromEnvironment())
	if err != nil {
		t.Fatalf("Provider initialization: %v", err)
	}
	imageItems, err := a.ListImages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	flavorItems, err := a.ListFlavors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	networkItems, err := a.ListNetworks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	image := exactImage(t, imageItems, environmentOrDefault("LABBIT_OPENSTACK_TEST_IMAGE", "ubuntu"))
	flavor := exactFlavor(t, flavorItems, environmentOrDefault("LABBIT_OPENSTACK_TEST_FLAVOR", "m1.small"))
	management := exactNetwork(t, networkItems, environmentOrDefault("LABBIT_OPENSTACK_TEST_MANAGEMENT_NETWORK", "sharednet1"))
	projectID := integrationProjectID(t, a)
	keyFile := a.provision.SSHPrivateKeyFile
	keyPair := integrationKeyPairName(t, ctx, a, keyFile)
	allowedCIDR := environmentOrDefault("LABBIT_OPENSTACK_TEST_SSH_CIDR", "172.16.8.1/32")
	managementSG := integrationManagementSecurityGroup(t, ctx, a, allowedCIDR)
	a.provision = normalizedProvisionConfig(ProvisionConfig{
		ProviderConnectionID: "local-integration", ProjectID: projectID,
		ManagementNetworkID: management.ID, ManagementSecurityGroupID: managementSG,
		KeyPairName: keyPair, SSHAllowedCIDR: allowedCIDR,
		LabSubnetCIDR: environmentOrDefault("LABBIT_OPENSTACK_TEST_LAB_CIDR", "198.20.0.0/24"),
		SSHUsername:   a.provision.SSHUsername, SSHPrivateKeyFile: keyFile,
		SSHKnownHostsFile: t.TempDir() + "/known_hosts", PollInterval: 2 * time.Second,
	})
	snapshot := coreprovider.CreationSnapshot{
		ProviderConnectionID: "local-integration", WorkspaceVMKey: "workspace",
		VMs: []coreprovider.VMSpec{{VMKey: "workspace", Role: "WORKSPACE", ImageID: image.ID, FlavorID: flavor.ID,
			FlavorSpec: coreprovider.FlavorSpec{VCPUs: flavor.VCPUs, RAMMiB: flavor.RAMMiB, DiskGiB: flavor.DiskGiB}}},
	}
	labID := "lbt147-" + integrationRunID(t)
	var tracked []coreprovider.ResourceResult
	t.Cleanup(func() {
		var distinct []coreprovider.ResourceResult
		seen := make(map[string]bool)
		for _, resource := range tracked {
			key := resource.ResourceType + ":" + resource.ProviderID
			if !seen[key] {
				seen[key] = true
				distinct = append(distinct, resource)
			}
		}
		cleanupM2Resources(t, a, distinct)
		t.Logf("Tracked resource absence verified after cleanup: generations=2 distinctResources=%d", len(distinct))
	})
	provision, err := a.Provision(ctx, coreprovider.ProvisionRequest{Correlation: coreprovider.Correlation{OperationID: labID + "-provision", LabInstanceID: labID, Generation: 1}, CreationSnapshot: snapshot})
	tracked = append(tracked, provision.ProviderResources...)
	if err != nil || provision.Outcome != coreprovider.OutcomeSucceeded {
		t.Fatalf("Provision failed: outcome=%s safeError=%+v", provision.Outcome, provision.Error)
	}
	t.Logf("OP-01 Provision gen1 ACTIVE and authenticated SSH ready: resources=%d", len(provision.ProviderResources))
	target := vmaccess.Target{LabInstanceID: labID, Generation: 1, VMKey: "workspace", ServerID: providerResourceID(t, provision.ProviderResources, coreprovider.ResourceTypeServer, "workspace")}
	m := vmaccess.NewManager(a, vmaccess.Config{IdleTimeout: 120 * time.Millisecond})
	t.Cleanup(func() { _ = m.Close() })
	for _, invalid := range []vmaccess.Target{
		{LabInstanceID: "other-lab", Generation: 1, VMKey: target.VMKey, ServerID: target.ServerID},
		{LabInstanceID: labID, Generation: 2, VMKey: target.VMKey, ServerID: target.ServerID},
		{LabInstanceID: labID, Generation: 1, VMKey: "other-vm", ServerID: target.ServerID},
	} {
		if _, err := m.OpenSession(ctx, invalid); !errors.Is(err, vmaccess.ErrInvalidTarget) {
			t.Fatal("mismatched real Provider target was accepted")
		}
	}
	t.Log("Wrong LabInstance, generation and VM key rejected before SSH")
	pty, err := terminal.NewManagedSSHPTY(ctx, m, target, 80, 24)
	if err != nil {
		t.Fatalf("managed PTY failed: %v", err)
	}
	defer pty.Close()
	if err := pty.Resize(100, 30); err != nil {
		t.Fatal("real PTY resize failed")
	}
	if _, err := pty.Write([]byte("printf 'LBT147-%s\\n' 'PTY-OK'\n")); err != nil {
		t.Fatal("PTY input failed")
	}
	assertPTYMarker(t, pty)
	file, err := m.OpenSession(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var fileIn io.WriteCloser
	var fileOut io.Reader
	if err := file.Setup(ctx, func(raw *ssh.Session) error {
		var err error
		fileIn, err = raw.StdinPipe()
		if err != nil {
			return err
		}
		fileOut, err = raw.StdoutPipe()
		if err != nil {
			return err
		}
		return raw.RequestSubsystem("sftp")
	}); err != nil {
		t.Fatal(err)
	}
	// Minimal SFTP wire probes only; production File operations remain LBT-149.
	if _, err := fileIn.Write([]byte{0, 0, 0, 5, 1, 0, 0, 0, 3}); err != nil {
		t.Fatal("SFTP INIT failed")
	}
	if packet := readSFTPPacket(t, fileOut, file); len(packet) < 5 || packet[0] != 2 || binary.BigEndian.Uint32(packet[1:5]) != 3 {
		t.Fatal("SFTP VERSION mismatch")
	}
	firstConnection := realSSHConnection(t, ctx, m, target)
	secondConnection := realSSHConnection(t, ctx, m, target)
	if firstConnection != secondConnection {
		t.Fatal("same VM channels did not share SSH transport")
	}
	_ = pty.Close()
	// SSH_FXP_REALPATH('.') proves the SFTP channel survives PTY closure.
	if _, err := fileIn.Write([]byte{0, 0, 0, 10, 16, 0, 0, 0, 1, 0, 0, 0, 1, '.'}); err != nil {
		t.Fatal("SFTP probe write failed")
	}
	if packet := readSFTPPacket(t, fileOut, file); len(packet) < 5 || packet[0] != 104 || binary.BigEndian.Uint32(packet[1:5]) != 1 {
		t.Fatal("SFTP channel did not survive PTY close")
	}
	_ = file.Close()
	time.Sleep(250 * time.Millisecond)
	thirdConnection := realSSHConnection(t, ctx, m, target)
	if thirdConnection == firstConnection {
		t.Fatal("idle SSH transport was not retired")
	}
	t.Log("Authenticated shared SSH, real PTY input/output/resize, independent SFTP channel and idle retirement PASS")
	old := realOpenSession(t, ctx, m, target)
	defer old.Close()
	reset, err := a.Reset(ctx, coreprovider.ResetRequest{Correlation: coreprovider.Correlation{OperationID: labID + "-reset", LabInstanceID: labID, Generation: 2}, CreationSnapshot: snapshot, ProviderResources: resourceRefs(provision.ProviderResources, 1)})
	tracked = append(tracked, reset.ProviderResources...)
	if err != nil || reset.Outcome != coreprovider.OutcomeSucceeded {
		t.Fatalf("Reset failed: outcome=%s safeError=%+v", reset.Outcome, reset.Error)
	}
	next := target
	next.Generation = 2
	next.ServerID = providerResourceID(t, resourceResultsForGeneration(reset.ProviderResources, 2), coreprovider.ResourceTypeServer, "workspace")
	_ = realSSHConnection(t, ctx, m, next)
	if _, err := m.OpenSession(ctx, target); !errors.Is(err, vmaccess.ErrInvalidTarget) {
		t.Fatal("old generation accepted after Reset")
	}
	t.Log("OP-01 Reset gen2, fresh SSH target and stale gen1 rejection PASS")
}

func realOpenSession(t *testing.T, ctx context.Context, m *vmaccess.Manager, target vmaccess.Target) *vmaccess.Session {
	t.Helper()
	s, err := m.OpenSession(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func realSSHConnection(t *testing.T, ctx context.Context, m *vmaccess.Manager, target vmaccess.Target) string {
	t.Helper()
	s := realOpenSession(t, ctx, m, target)
	defer s.Close()
	var output []byte
	if err := s.Setup(ctx, func(raw *ssh.Session) error {
		var err error
		output, err = raw.Output("printf '%s' \"$SSH_CONNECTION\"")
		return err
	}); err != nil {
		t.Fatal("authenticated exec failed")
	}
	if len(strings.Fields(string(output))) != 4 {
		t.Fatal("SSH connection identity unavailable")
	}
	return string(output) // Compared in memory only; private endpoints never logged.
}
func assertPTYMarker(t *testing.T, pty terminal.PTYChannel) {
	t.Helper()
	done := make(chan bool, 1)
	go func() {
		var collected []byte
		b := make([]byte, 1024)
		for len(collected) < 65536 {
			n, err := pty.Read(b)
			collected = append(collected, b[:n]...)
			if strings.Contains(string(collected), "LBT147-PTY-OK") {
				done <- true
				return
			}
			if err != nil {
				break
			}
		}
		done <- false
	}()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("PTY marker missing")
		}
	case <-time.After(10 * time.Second):
		_ = pty.Close()
		t.Fatal("PTY read timed out")
	}
}
func readSFTPPacket(t *testing.T, r io.Reader, session *vmaccess.Session) []byte {
	t.Helper()
	done := make(chan []byte, 1)
	go func() {
		var header [4]byte
		if _, err := io.ReadFull(r, header[:]); err != nil {
			done <- nil
			return
		}
		size := binary.BigEndian.Uint32(header[:])
		if size > 65536 {
			done <- nil
			return
		}
		packet := make([]byte, size)
		if _, err := io.ReadFull(r, packet); err != nil {
			done <- nil
			return
		}
		done <- packet
	}()
	select {
	case packet := <-done:
		return packet
	case <-time.After(10 * time.Second):
		_ = session.Close()
		t.Fatal("SFTP probe timed out")
		return nil
	}
}
