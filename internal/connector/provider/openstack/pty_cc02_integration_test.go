package openstackprovider

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/terminal"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
	"golang.org/x/crypto/ssh"
)

// Real VM bytes are asserted in memory only. Control evidence has lifecycle
// metadata; Terminal content, private endpoints and raw SSH errors are not logged.
func TestOpenStackPTYCC02Integration(t *testing.T) {
	if os.Getenv("LABBIT_OPENSTACK_PTY_CC02_TEST") != "1" {
		t.Skip("set LABBIT_OPENSTACK_PTY_CC02_TEST=1 for real local OpenStack PTY/CC-02 acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
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
	keyPair := integrationKeyPairName(t, ctx, a, keyFile)
	allowedCIDR := environmentOrDefault("LABBIT_OPENSTACK_TEST_SSH_CIDR", "172.16.8.1/32")
	managementSG := integrationManagementSecurityGroup(t, ctx, a, allowedCIDR)
	a.provision = normalizedProvisionConfig(ProvisionConfig{
		ProviderConnectionID: "local-integration", ProjectID: integrationProjectID(t, a),
		ManagementNetworkID: management.ID, ManagementSecurityGroupID: managementSG,
		KeyPairName: keyPair, SSHAllowedCIDR: allowedCIDR,
		LabSubnetCIDR: environmentOrDefault("LABBIT_OPENSTACK_TEST_LAB_CIDR", "198.20.0.0/24"),
		SSHUsername:   a.provision.SSHUsername, SSHPrivateKeyFile: keyFile,
		SSHKnownHostsFile: t.TempDir() + "/known_hosts", PollInterval: 2 * time.Second,
	})
	labID := "lbt148-" + integrationRunID(t)
	var tracked []coreprovider.ResourceResult
	t.Cleanup(func() {
		cleanupM2Resources(t, a, tracked)
		t.Logf("Tracked resource absence verified after cleanup: distinctResources=%d", len(tracked))
	})
	result, err := a.Provision(ctx, coreprovider.ProvisionRequest{
		Correlation:      coreprovider.Correlation{OperationID: labID + "-provision", LabInstanceID: labID, Generation: 1},
		CreationSnapshot: coreprovider.CreationSnapshot{ProviderConnectionID: "local-integration", WorkspaceVMKey: "workspace", VMs: []coreprovider.VMSpec{{VMKey: "workspace", Role: "WORKSPACE", ImageID: image.ID, FlavorID: flavor.ID, FlavorSpec: coreprovider.FlavorSpec{VCPUs: flavor.VCPUs, RAMMiB: flavor.RAMMiB, DiskGiB: flavor.DiskGiB}}}},
	})
	tracked = append(tracked, result.ProviderResources...)
	if err != nil || result.Outcome != coreprovider.OutcomeSucceeded {
		t.Fatalf("Provision failed: outcome=%s", result.Outcome)
	}
	t.Log("OP-01 Provision ACTIVE and pinned authenticated SSH ready")
	target := vmaccess.Target{LabInstanceID: labID, Generation: 1, VMKey: "workspace", ServerID: providerResourceID(t, result.ProviderResources, coreprovider.ResourceTypeServer, "workspace")}
	access := vmaccess.NewManager(a, vmaccess.Config{IdleTimeout: time.Second})
	defer access.Close()
	// Shortened only for this harness; production retains its 60-second grace.
	sessions := terminal.NewSessionManager(3*time.Second, nil)
	defer sessions.CloseAll(protocol.TerminalReasonServiceRestart)
	saas := mock.NewMockSaaS("pty-cc02-test-only-token")
	defer saas.Close()
	control := wss.NewClient(wss.Config{BaseURL: saas.URL(), Credential: "pty-cc02-test-only-token", RuntimeID: "pty-cc02-runtime", AllowInsecure: true})
	if err := control.Dial(ctx); err != nil {
		t.Fatal("Control dial failed")
	}
	defer control.Close()
	if _, err := control.SendHello(ctx); err != nil {
		t.Fatal("Control HELLO failed")
	}
	handler := wss.NewHandler(a, control)
	handler.SetVMPTYFactory(func(ctx context.Context, target vmaccess.Target, cols, rows int) (terminal.PTYChannel, error) {
		return terminal.NewManagedSSHPTY(ctx, access, target, cols, rows)
	})
	listenCtx, stopListen := context.WithCancel(ctx)
	listenDone := make(chan error, 1)
	go func() { listenDone <- handler.Listen(listenCtx, control.Conn()) }()
	defer func() {
		stopListen()
		_ = control.Close()
		select {
		case <-listenDone:
		case <-time.After(time.Second):
			t.Error("Control listener did not stop")
		}
	}()
	relayA, relayB := mock.NewTerminalRelay(), mock.NewTerminalRelay()
	defer relayA.Close()
	defer relayB.Close()
	for _, relay := range []*mock.TerminalRelay{relayA, relayB} {
		relay.SetAuthValidator(func(r *http.Request) int {
			if r.Header.Get("Authorization") != "Bearer pty-cc02-test-only-token" {
				return http.StatusUnauthorized
			}
			return http.StatusOK
		})
	}
	open := func(id string, relay *mock.TerminalRelay, cols, rows int) *terminal.Session {
		t.Helper()
		handler.SetTerminalManager(sessions, nil, terminal.DataWSSClientConfig{EndpointURL: relay.URL(), Credential: "pty-cc02-test-only-token", RuntimeID: "pty-cc02-runtime", AllowInsecure: true, DialTimeout: 2 * time.Second})
		request := protocol.TerminalOpenMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeTerminalOpen, MessageID: id + "-open", SentAt: time.Now().UTC(), TerminalSessionID: id, LabInstanceID: labID, Generation: 1}, Payload: protocol.TerminalOpenPayload{TargetVmKey: target.VMKey, ProviderServerID: target.ServerID, Cols: cols, Rows: rows}}
		if err := saas.SendRaw(request); err != nil {
			t.Fatal("TERMINAL_OPEN send failed")
		}
		var opened protocol.TerminalOpenResultMessage
		if err := json.Unmarshal(waitPTYControl(t, saas, protocol.MessageTypeTerminalOpenResult, id, id+"-open"), &opened); err != nil || opened.Payload.Outcome != protocol.OutcomeSucceeded {
			t.Fatal("TERMINAL_OPEN did not succeed after attach")
		}
		if _, err := relay.WaitForAttach(2 * time.Second); err != nil {
			t.Fatal("Data ATTACH missing")
		}
		session, exists := sessions.GetSession(id)
		if !exists {
			t.Fatal("TerminalSession missing")
		}
		return session
	}
	first := open("pty-a", relayA, 80, 24)
	second := open("pty-b", relayB, 100, 30)
	ptyCommandMarker(t, relayA, "printf 'LBT148-SIZE:%s\\n' \"$(stty size)\"\n", "LBT148-SIZE:24 80")
	ptyCommandMarker(t, relayB, "printf 'LBT148-SIZE:%s\\n' \"$(stty size)\"\n", "LBT148-SIZE:30 100")
	if err := relayA.SendResize(120, 40); err != nil {
		t.Fatal("Data RESIZE send failed")
	}
	ptyCommandMarker(t, relayA, "printf 'LBT148-SIZE:%s\\n' \"$(stty size)\"\n", "LBT148-SIZE:40 120")
	connection := realSSHConnection(t, ctx, access, target)
	if connection != realSSHConnection(t, ctx, access, target) {
		t.Fatal("PTY channels did not share VM transport")
	}
	// Repeated active OPEN preserves both the PTY and its Data WSS.
	activeConn := first.GetDataConn()
	duplicate := protocol.TerminalOpenMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeTerminalOpen, MessageID: "duplicate-open", SentAt: time.Now().UTC(), TerminalSessionID: first.SessionID, LabInstanceID: labID, Generation: 1}, Payload: protocol.TerminalOpenPayload{TargetVmKey: target.VMKey, ProviderServerID: target.ServerID, Cols: 80, Rows: 24}}
	if err := saas.SendRaw(duplicate); err != nil {
		t.Fatal(err)
	}
	var duplicateResult protocol.TerminalOpenResultMessage
	_ = json.Unmarshal(waitPTYControl(t, saas, protocol.MessageTypeTerminalOpenResult, first.SessionID, "duplicate-open"), &duplicateResult)
	if duplicateResult.Payload.Outcome != protocol.OutcomeSucceeded || first.GetDataConn() != activeConn || sessions.ActiveCount() != 2 {
		t.Fatal("duplicate OPEN replaced a channel")
	}
	t.Log("Control OPEN/Data ATTACH, real PTY INPUT/OUTPUT, initial size, RESIZE, two independent PTYs and duplicate OPEN PASS")
	file := realOpenSession(t, ctx, access, target)
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
		t.Fatal("independent SFTP setup failed")
	}
	if _, err := fileIn.Write([]byte{0, 0, 0, 5, 1, 0, 0, 0, 3}); err != nil {
		t.Fatal("SFTP INIT failed")
	}
	if packet := readSFTPPacket(t, fileOut, file); len(packet) < 5 || packet[0] != 2 {
		t.Fatal("SFTP VERSION missing")
	}
	closeRequest := protocol.TerminalDataCloseMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeTerminalDataClose, MessageID: "data-close", SentAt: time.Now().UTC(), TerminalSessionID: first.SessionID, LabInstanceID: labID, Generation: 1}, Payload: protocol.TerminalDataClosePayload{Reason: protocol.TerminalReasonSessionClosed}}
	raw, _ := json.Marshal(closeRequest)
	if err := relayA.SendText(raw); err != nil {
		t.Fatal("Data CLOSE send failed")
	}
	assertPTYEnded(t, saas, relayA, first.SessionID, protocol.TerminalReasonSessionClosed, nil)
	if _, err := fileIn.Write([]byte{0, 0, 0, 10, 16, 0, 0, 0, 1, 0, 0, 0, 1, '.'}); err != nil {
		t.Fatal("SFTP after PTY close failed")
	}
	if packet := readSFTPPacket(t, fileOut, file); len(packet) < 5 || packet[0] != 104 || binary.BigEndian.Uint32(packet[1:5]) != 1 {
		t.Fatal("PTY close affected SFTP channel")
	}
	_ = file.Close()
	ptyCommandMarker(t, relayB, "LBT148_STATE=kept; printf 'LBT148-%s\\n' 'INDEPENDENT'\n", "LBT148-INDEPENDENT")
	t.Log("Data CLOSE and Control/Data ENDED release one PTY; other PTY and SFTP remain usable PASS")
	relayB.DisconnectConnection()
	if _, err := relayB.WaitForAttach(3 * time.Second); err != nil {
		t.Fatal("automatic Data reconnect missing")
	}
	ptyCommandMarker(t, relayB, "printf 'LBT148-STATE:%s\\n' \"$LBT148_STATE\"\n", "LBT148-STATE:kept")
	if current, ok := sessions.GetSession(second.SessionID); !ok || current != second || connection != realSSHConnection(t, ctx, access, target) {
		t.Fatal("Data reconnect replaced shell or transport")
	}
	t.Log("Automatic Data reconnect within grace preserves shell state, TerminalSession and shared SSH transport PASS; historyAvailable=false")
	ptyCommandMarker(t, relayB, "printf 'LBT148-%s\\n' 'FINAL'; exit 7\n", "LBT148-FINAL")
	seven := 7
	assertPTYEnded(t, saas, relayB, second.SessionID, protocol.TerminalReasonPtyExited, &seven)
	t.Log("Final output before remote shell exit, exitCode=7 and Control/Data ENDED PASS")
	grace := open("pty-grace", relayA, 80, 24)
	relayA.SetAuthValidator(func(*http.Request) int { return http.StatusServiceUnavailable })
	relayA.DisconnectConnection()
	var graceEnded protocol.TerminalEndedMessage
	_ = json.Unmarshal(waitPTYControl(t, saas, protocol.MessageTypeTerminalEnded, grace.SessionID, ""), &graceEnded)
	if graceEnded.Payload.Reason != protocol.TerminalReasonGraceTimeout || grace.GetStatus() != terminal.StatusClosed {
		t.Fatal("grace did not release PTY")
	}
	duplicate.TerminalSessionID = grace.SessionID
	duplicate.MessageID = "reopen-ended"
	if err := saas.SendRaw(duplicate); err != nil {
		t.Fatal(err)
	}
	var rejected protocol.TerminalOpenResultMessage
	_ = json.Unmarshal(waitPTYControl(t, saas, protocol.MessageTypeTerminalOpenResult, grace.SessionID, "reopen-ended"), &rejected)
	if rejected.Payload.Outcome != protocol.OutcomeFailed || rejected.Payload.Error == nil || rejected.Payload.Error.Code != protocol.TerminalErrInvalidSession {
		t.Fatal("ended TerminalSession resurrected")
	}
	t.Log("Shortened 3-second harness grace expires, releases PTY and rejects delayed OPEN PASS; production grace=60s")
	last := open("pty-disconnect", relayB, 80, 24)
	ptyCommandMarker(t, relayB, "printf 'LBT148-%s\\n' 'BEFORE-DISCONNECT'\n", "LBT148-BEFORE-DISCONNECT")
	_ = access.Close()
	assertPTYEnded(t, saas, relayB, last.SessionID, protocol.TerminalReasonSSHDisconnected, nil)
	if sessions.ActiveCount() != 0 {
		t.Fatal("TerminalSession or PTY remains after termination")
	}
	t.Log("SSH disconnect produces safe Control/Data ENDED, all PTYs released and no automatic SSH retry PASS")
}

func waitPTYControl(t *testing.T, saas *mock.MockSaaS, kind, id, reply string) []byte {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, raw := range saas.ReceivedMessages() {
			var env protocol.BaseEnvelope
			if json.Unmarshal(raw, &env) == nil && env.Type == kind && env.TerminalSessionID == id && (reply == "" || env.ReplyToMessageID == reply) {
				return raw
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Control lifecycle missing: type=%s", kind)
	return nil
}

func ptyCommandMarker(t *testing.T, relay *mock.TerminalRelay, command, marker string) {
	t.Helper()
	if err := relay.SendBinary([]byte(command)); err != nil {
		t.Fatal("Terminal INPUT send failed")
	}
	deadline := time.Now().Add(10 * time.Second)
	var collected []byte
	for time.Now().Before(deadline) && len(collected) < 65536 {
		output, err := relay.ReadBinary(time.Second)
		if err != nil {
			continue
		}
		collected = append(collected, output...)
		if strings.Contains(string(collected), marker) {
			return
		}
	}
	t.Fatal("Terminal OUTPUT assertion failed (content withheld)")
}

func assertPTYEnded(t *testing.T, saas *mock.MockSaaS, relay *mock.TerminalRelay, id, reason string, code *int) {
	t.Helper()
	raw, err := relay.ReadText(3 * time.Second)
	if err != nil {
		t.Fatal("Data ENDED missing")
	}
	var data protocol.TerminalDataEndedMessage
	var control protocol.TerminalEndedMessage
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal("Data ENDED malformed")
	}
	if err := json.Unmarshal(waitPTYControl(t, saas, protocol.MessageTypeTerminalEnded, id, ""), &control); err != nil {
		t.Fatal("Control ENDED malformed")
	}
	if data.Type != protocol.MessageTypeTerminalDataEnded || data.TerminalSessionID != id || data.Payload.Reason != reason || control.Payload.Reason != reason {
		t.Fatal("Control/Data termination classification mismatch")
	}
	if code != nil && (data.Payload.ExitCode == nil || control.Payload.ExitCode == nil || *data.Payload.ExitCode != *code || *control.Payload.ExitCode != *code) {
		t.Fatal("Control/Data exit code mismatch")
	}
	if reason == protocol.TerminalReasonSSHDisconnected && (data.Payload.Error == nil || control.Payload.Error == nil) {
		t.Fatal("SSH disconnect error missing")
	}
}
