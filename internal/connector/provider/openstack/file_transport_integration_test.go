package openstackprovider

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	fileclient "github.com/ktcloud4-SL/labbit-app/internal/connector/filetransport"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/terminal"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/workspacefile"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
	serverconnector "github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connectorwss"
	filebroker "github.com/ktcloud4-SL/labbit-app/internal/server/filetransport"
	serverfile "github.com/ktcloud4-SL/labbit-app/internal/server/workspacefile"
	"golang.org/x/crypto/ssh"
)

// This test-only composition imports the existing Backend consumer. Production
// Connector packages do not import Backend packages. Auth is a fixture; the
// Control handler/Router/Broker, Connector runtime and VM SSH/SFTP are real.
type fileIntegrationAuth struct {
	principal serverconnector.Principal
	revoked   atomic.Bool
}

const fileIntegrationCredential = "lbt150-fixture-only-credential"

func (a *fileIntegrationAuth) Authenticate(_ context.Context, credential serverconnector.Credential) (serverconnector.Principal, error) {
	if string(credential) != fileIntegrationCredential || a.revoked.Load() {
		return serverconnector.Principal{}, serverconnector.ErrUnauthenticated
	}
	return a.principal, nil
}
func (*fileIntegrationAuth) RecordHeartbeat(context.Context, serverconnector.Principal, time.Time) error {
	return nil
}

type fileIntegrationForwarder struct{ target serverconnector.FileSink }

func (f *fileIntegrationForwarder) HandleFileEvent(e serverconnector.FileEvent) {
	f.target.HandleFileEvent(e)
}

type fileIntegrationSender struct {
	client *wss.Client
	mu     sync.Mutex
	frames [][]byte
}

func (s *fileIntegrationSender) DialCredential() string { return s.client.DialCredential() }
func (s *fileIntegrationSender) SendMessage(ctx context.Context, message any) error {
	frame, err := json.Marshal(message)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.frames = append(s.frames, bytes.Clone(frame))
	s.mu.Unlock()
	return s.client.SendMessage(ctx, message)
}

// Opt-in disposable local OpenStack acceptance. Paths/content/credentials and
// raw provider/SSH errors are checked only in memory, never included in logs.
func TestOpenStackFileTransportIntegration(t *testing.T) {
	if os.Getenv("LABBIT_OPENSTACK_FILE_TRANSPORT_TEST") != "1" {
		t.Skip("set LABBIT_OPENSTACK_FILE_TRANSPORT_TEST=1 for disposable local OpenStack File acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	binary, err := os.ReadFile(os.Getenv("LABBIT_WORKSPACE_SFTP_HELPER_FILE"))
	if err != nil || len(binary) < 4 || string(binary[:4]) != "\x7fELF" {
		t.Fatal("Linux helper build required")
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
	labID := uuid.New()
	var tracked []coreprovider.ResourceResult
	t.Cleanup(func() {
		cleanupM2Resources(t, a, tracked)
		t.Logf("Tracked resource absence verified: resources=%d", len(tracked))
	})
	created, err := a.Provision(ctx, coreprovider.ProvisionRequest{Correlation: coreprovider.Correlation{OperationID: "lbt150-" + integrationRunID(t), LabInstanceID: labID.String(), Generation: 1}, CreationSnapshot: coreprovider.CreationSnapshot{ProviderConnectionID: "local-integration", WorkspaceVMKey: "workspace", VMs: []coreprovider.VMSpec{{VMKey: "workspace", Role: "WORKSPACE", ImageID: image.ID, FlavorID: flavor.ID, FlavorSpec: coreprovider.FlavorSpec{VCPUs: flavor.VCPUs, RAMMiB: flavor.RAMMiB, DiskGiB: flavor.DiskGiB}}}}})
	tracked = append(tracked, created.ProviderResources...)
	if err != nil || created.Outcome != coreprovider.OutcomeSucceeded {
		t.Fatal("Disposable Workspace Provision failed")
	}
	vm := vmaccess.Target{LabInstanceID: labID.String(), Generation: 1, VMKey: "workspace", ServerID: providerResourceID(t, created.ProviderResources, coreprovider.ResourceTypeServer, "workspace")}
	access := vmaccess.NewManager(a, vmaccess.Config{IdleTimeout: time.Second})
	defer access.Close()
	fixture, session := workspaceFixtureSFTP(t, ctx, access, vm)
	defer session.Close()
	defer fixture.Close()
	root := "/var/lib/labbit-workspaces/workspace"
	helper := "/usr/local/libexec/labbit-workspace-sftp"
	workspaceFixtureWrite(t, fixture, "/home/ubuntu/helper-upload", binary)
	fileIntegrationInstall(t, ctx, access, vm)
	const secretPath = "private-path-fixture"
	initial := []byte("private-body-fixture")
	workspaceFixtureWrite(t, fixture, root+"/"+secretPath, initial)
	workspaceFixtureWrite(t, fixture, root+"/empty", nil)
	workspaceFixtureWrite(t, fixture, "/home/ubuntu/outside", []byte("outside-fixture"))
	if fixture.Symlink("/home/ubuntu/outside", root+"/link") != nil {
		t.Fatal("Symlink fixture failed")
	}
	service, err := workspacefile.New(access, a, workspacefile.Config{Root: root, HelperPath: helper, Timeout: 30 * time.Second, MaxFileBytes: 64})
	if err != nil {
		t.Fatal("File service configuration failed")
	}

	auth := &fileIntegrationAuth{principal: serverconnector.Principal{ConnectorID: uuid.New(), OrganizationID: uuid.New(), CredentialID: uuid.New()}}
	registry := serverconnector.NewRegistry()
	forwarder := &fileIntegrationForwarder{}
	router, err := serverconnector.NewRouter(serverconnector.RouterOptions{Registry: registry, FileSink: forwarder})
	if err != nil {
		t.Fatal("Router failed")
	}
	broker, err := filebroker.New(filebroker.Options{Control: router, Auth: auth, AttachTimeout: 5 * time.Second, OperationTimeout: 30 * time.Second})
	if err != nil {
		t.Fatal("Broker failed")
	}
	forwarder.target = broker
	registry.SetRevokeObserver(broker)
	controlHandler, err := connectorwss.New(connectorwss.Options{Auth: auth, Heartbeats: auth, Registry: registry, Router: router, OfflineTimeout: 10 * time.Minute})
	if err != nil {
		t.Fatal("Control handler failed")
	}
	controlServer := httptest.NewServer(controlHandler)
	dataMux := http.NewServeMux()
	dataMux.Handle("GET "+filebroker.DataPath, broker.Handler())
	dataServer := httptest.NewTLSServer(dataMux)
	defer controlServer.Close()
	defer dataServer.Close()
	defer controlHandler.Close()
	defer broker.Close()
	pool := x509.NewCertPool()
	pool.AddCert(dataServer.Certificate())
	fileConfig := fileclient.Config{EndpointURL: "wss" + strings.TrimPrefix(dataServer.URL, "https") + filebroker.DataPath, RuntimeID: "file-integration-runtime", Timeout: 30 * time.Second, MaxFileBytes: 64, Dialer: &websocket.Dialer{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
	manager, err := fileclient.New(service, fileConfig)
	if err != nil {
		t.Fatal("File manager failed")
	}
	defer func() {
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if manager.Shutdown(stop) != nil {
			t.Error("File workers did not drain")
		}
	}()
	control := wss.NewClient(wss.Config{BaseURL: "ws" + strings.TrimPrefix(controlServer.URL, "http"), Credential: fileIntegrationCredential, RuntimeID: fileConfig.RuntimeID, AllowInsecure: true, Capabilities: []string{protocol.CapabilityFileV1}})
	if control.Dial(ctx) != nil {
		t.Fatal("Control dial failed")
	}
	defer control.Close()
	if _, err = control.SendHello(ctx); err != nil {
		t.Fatal("Control HELLO failed")
	}
	sender := &fileIntegrationSender{client: control}
	handler := wss.NewHandler(a, sender)
	handler.SetFileManager(manager, "incorrect-fallback-credential")
	listenCtx, stopListen := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- handler.Listen(listenCtx, control.Conn()) }()
	defer func() {
		stopListen()
		_ = control.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("Control listener leaked")
		}
	}()
	target := serverfile.Target{LabInstanceID: labID, Generation: 1, ConnectorID: auth.principal.ConnectorID, WorkspaceVMKey: vm.VMKey, ProviderServerID: vm.ServerID}
	file, _ := serverfile.ParseFile(secretPath)
	emptyPath, _ := serverfile.ParseFile("empty")
	dir, _ := serverfile.ParseDirectory("")
	entries, err := broker.Tree(ctx, target, dir)
	if err != nil || len(entries) != 3 {
		t.Fatal("Backend -> Connector -> real SFTP Tree failed")
	}
	read, err := broker.Read(ctx, target, file, 65)
	if err != nil || !bytes.Equal(read.Content, initial) {
		t.Fatal("Real Read failed")
	}
	newBody := []byte{'n', 0, 'e', 255, 'w'}
	revision, err := broker.Save(ctx, target, file, read.Revision, newBody)
	if err != nil {
		t.Fatal("Real Save failed")
	}
	back, err := broker.Read(ctx, target, file, 64)
	if err != nil || !bytes.Equal(back.Content, newBody) || back.Revision != revision {
		t.Fatal("Binary/revision round trip failed")
	}
	_, err = broker.Save(ctx, target, file, read.Revision, initial)
	if !errors.Is(err, serverfile.ErrStaleRevision) {
		t.Fatal("Stale Save mapping failed")
	}
	revision, err = broker.Save(ctx, target, file, revision, nil)
	if err != nil {
		t.Fatal("Empty Save failed")
	}
	back, err = broker.Read(ctx, target, file, 1)
	if err != nil || len(back.Content) != 0 || back.Revision != revision {
		t.Fatal("Empty Binary Read failed")
	}
	if _, err = broker.Read(ctx, target, emptyPath, 1); err != nil {
		t.Fatal("Initial empty file Read failed")
	}
	missing, _ := serverfile.ParseFile("missing")
	_, err = broker.Read(ctx, target, missing, 64)
	if !errors.Is(err, serverfile.ErrPathNotFound) {
		t.Fatal("NOT_FOUND mapping failed")
	}
	link, _ := serverfile.ParseFile("link")
	_, err = broker.Read(ctx, target, link, 64)
	if !errors.Is(err, serverfile.ErrInvalidPath) {
		t.Fatal("Root escape mapping failed")
	}
	_, err = broker.Save(ctx, target, file, revision, bytes.Repeat([]byte{'x'}, 65))
	if !errors.Is(err, serverfile.ErrTooLarge) {
		t.Fatal("Save size guard failed")
	}
	for _, bad := range []serverfile.Target{{LabInstanceID: labID, Generation: 2, ConnectorID: target.ConnectorID, WorkspaceVMKey: vm.VMKey, ProviderServerID: vm.ServerID}, {LabInstanceID: labID, Generation: 1, ConnectorID: target.ConnectorID, WorkspaceVMKey: "other", ProviderServerID: vm.ServerID}} {
		_, err = broker.Read(ctx, bad, file, 64)
		if !errors.Is(err, serverfile.ErrTransportUnavailable) {
			t.Fatal("Stale generation/non-Workspace rejection failed")
		}
	}
	t.Log("Backend Router/Broker + production Connector + trusted TLS File Data + real VM rooted SFTP: Tree/Read/Save, binary/empty, revision, size, symlink and target guards PASS")
	pty, err := terminal.NewManagedSSHPTY(ctx, access, vm, 80, 24)
	if err != nil {
		t.Fatal("Shared PTY failed")
	}
	defer pty.Close()
	shared := realSSHConnection(t, ctx, access, vm)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		name := fmt.Sprintf("concurrent-%d", i)
		body := []byte(fmt.Sprintf("fixture-%d", i))
		workspaceFixtureWrite(t, fixture, root+"/"+name, body)
		path, _ := serverfile.ParseFile(name)
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := broker.Read(ctx, target, path, 64)
			if err != nil || !bytes.Equal(data.Content, body) {
				t.Errorf("Concurrent request failed: errorType=%T bodyMismatch=%t", err, !bytes.Equal(data.Content, body))
			}
		}()
	}
	wg.Wait()
	fileIntegrationIdle(t, broker, manager)
	// A VM-side helper accepts exec but delays SFTP initialization. Cancellation
	// is triggered only after its remote marker confirms the real channel started.
	stallPath := "/home/ubuntu/stall-workspace-sftp"
	workspaceFixtureWrite(t, fixture, stallPath, []byte("#!/bin/sh\nprintf ready > /home/ubuntu/stall-started\nsleep 30\n"))
	if fixture.Chmod(stallPath, 0700) != nil {
		t.Fatal("Stall fixture failed")
	}
	stalled, err := workspacefile.New(access, a, workspacefile.Config{Root: root, HelperPath: stallPath, Timeout: 5 * time.Second, MaxFileBytes: 64})
	if err != nil {
		t.Fatal("Stall service failed")
	}
	stallManager, err := fileclient.New(stalled, fileConfig)
	if err != nil {
		t.Fatal("Stall manager failed")
	}
	defer func() {
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if stallManager.Shutdown(stop) != nil {
			t.Error("Stalled File worker leaked")
		}
	}()
	handler.SetFileManager(stallManager, fileIntegrationCredential)
	for _, mode := range []string{"cancel", "timeout", "data_revoke"} {
		_ = fixture.Remove("/home/ubuntu/stall-started")
		request, stop := context.WithCancel(ctx)
		result := make(chan error, 1)
		go func() { _, err := broker.Read(request, target, file, 64); result <- err }()
		until := time.Now().Add(10 * time.Second)
		for {
			if _, err := fixture.Stat("/home/ubuntu/stall-started"); err == nil {
				break
			}
			if time.Now().After(until) {
				stop()
				t.Fatal("Real SFTP initialization did not start")
			}
			time.Sleep(10 * time.Millisecond)
		}
		switch mode {
		case "cancel":
			stop()
		case "data_revoke":
			broker.CredentialRevoked(auth.principal.CredentialID)
		}
		select {
		case err := <-result:
			if err == nil {
				t.Fatal("Interrupted File unexpectedly succeeded")
			}
		case <-time.After(8 * time.Second):
			stop()
			t.Fatal("Interrupted File did not terminate")
		}
		stop()
		fileIntegrationIdle(t, broker, stallManager)
		if shared != realSSHConnection(t, ctx, access, vm) {
			t.Fatal("File interruption replaced shared PTY transport")
		}
	}
	handler.SetFileManager(manager, fileIntegrationCredential)
	if _, err = broker.Read(ctx, target, file, 64); err != nil {
		t.Fatal("File failed after canceled requests")
	}
	if _, err = pty.Write([]byte("printf 'LBT147-%s\\n' 'PTY-OK'\n")); err != nil {
		t.Fatal("PTY failed after File termination")
	}
	assertPTYMarker(t, pty)
	fileIntegrationIdle(t, broker, manager)
	sender.mu.Lock()
	frames := append([][]byte(nil), sender.frames...)
	sender.mu.Unlock()
	for _, frame := range frames {
		for _, forbidden := range [][]byte{[]byte(secretPath), initial, newBody, []byte(fileIntegrationCredential), []byte(root)} {
			if len(forbidden) > 0 && bytes.Contains(frame, forbidden) {
				t.Fatal("Sensitive File data leaked into Control")
			}
		}
	}
	t.Log("Four independent Data requests with distinct content; real SFTP cancellation/timeout/Data revoke drain pending+workers and preserve shared PTY; Control secrecy PASS")
}

func fileIntegrationIdle(t *testing.T, b *filebroker.Broker, m *fileclient.Manager) {
	t.Helper()
	until := time.Now().Add(3 * time.Second)
	for (b.PendingCount() != 0 || m.Active() != 0) && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if b.PendingCount() != 0 || m.Active() != 0 {
		t.Fatal("Pending or File Data worker remained")
	}
}

func fileIntegrationInstall(t *testing.T, ctx context.Context, m *vmaccess.Manager, vm vmaccess.Target) {
	t.Helper()
	for i, command := range []string{
		"sudo -n install -d -o root -g root -m 0755 /usr/local/libexec /var/lib/labbit-workspaces && sudo -n install -o root -g root -m 0755 /home/ubuntu/helper-upload /usr/local/libexec/labbit-workspace-sftp",
		"sudo -n install -d -o ubuntu -g ubuntu -m 0750 /var/lib/labbit-workspaces/workspace && mkdir -p /var/lib/labbit-workspaces/workspace/dir",
	} {
		s := realOpenSession(t, ctx, m, vm)
		err := s.Setup(ctx, func(raw *ssh.Session) error { return raw.Run(command) })
		s.Close()
		if err != nil {
			var exit *ssh.ExitError
			status := -1
			if errors.As(err, &exit) {
				status = exit.ExitStatus()
			}
			t.Fatalf("Fixture install stage=%d errorType=%T exitStatus=%d canceled=%t", i+1, err, status, ctx.Err() != nil)
		}
	}
}
