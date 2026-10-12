package openstackprovider

// Test-only overlay: real local Keystone/Nova/Neutron/SSH, scoped HTTP faults.
import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/terminal"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
	"golang.org/x/crypto/ssh"
)

type additionalFaultTransport struct {
	upstream http.RoundTripper
	mode     string
	serverID string
	hits     atomic.Int64
}

func (f *additionalFaultTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	nova := r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/servers/"+f.serverID)
	neutron := r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/ports") && r.URL.Query().Get("device_id") == f.serverID
	if (f.mode == "nova-503" && nova) || (f.mode == "neutron-503" && neutron) {
		f.hits.Add(1)
		return &http.Response{StatusCode: 503, Status: "503 Service Unavailable", Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"error":"additional-audit-private-provider-detail"}`)), Request: r}, nil
	}
	return f.upstream.RoundTrip(r)
}

func TestLBT147AdditionalRealFailures(t *testing.T) {
	if os.Getenv("LABBIT_LBT147_ADDITIONAL_REAL_TEST") != "1" {
		t.Skip("explicit local cloud opt-in")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	a, err := New(ctx, ConfigFromEnvironment())
	if err != nil {
		t.Fatal("real provider initialization failed")
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
	image := exactImage(t, images, "ubuntu")
	flavor := exactFlavor(t, flavors, "m1.small")
	management := exactNetwork(t, networks, "sharednet1")
	key := integrationKeyPairName(t, ctx, a, a.provision.SSHPrivateKeyFile)
	group := integrationManagementSecurityGroup(t, ctx, a, "172.16.8.1/32")
	a.provision = normalizedProvisionConfig(ProvisionConfig{ProviderConnectionID: "local-additional-audit",
		ProjectID: integrationProjectID(t, a), ManagementNetworkID: management.ID, ManagementSecurityGroupID: group,
		KeyPairName: key, SSHAllowedCIDR: "172.16.8.1/32", LabSubnetCIDR: "198.24.0.0/24",
		SSHUsername: "ubuntu", SSHPrivateKeyFile: a.provision.SSHPrivateKeyFile,
		SSHKnownHostsFile: t.TempDir() + "/known_hosts", PollInterval: 2 * time.Second})
	lab := "lbt147-additional-" + integrationRunID(t)
	spec := coreprovider.VMSpec{VMKey: "workspace", Role: "WORKSPACE", ImageID: image.ID, FlavorID: flavor.ID,
		FlavorSpec: coreprovider.FlavorSpec{VCPUs: flavor.VCPUs, RAMMiB: flavor.RAMMiB, DiskGiB: flavor.DiskGiB}}
	worker := spec
	worker.VMKey, worker.Role = "worker", "WORKER"
	result, err := a.Provision(ctx, coreprovider.ProvisionRequest{Correlation: coreprovider.Correlation{
		OperationID: lab + "-provision", LabInstanceID: lab, Generation: 1},
		CreationSnapshot: coreprovider.CreationSnapshot{ProviderConnectionID: a.provision.ProviderConnectionID,
			WorkspaceVMKey: "workspace", VMs: []coreprovider.VMSpec{spec, worker}}})
	t.Cleanup(func() { cleanupM2Resources(t, a, result.ProviderResources) })
	if err != nil || result.Outcome != coreprovider.OutcomeSucceeded {
		t.Fatal("real two-VM provision failed")
	}
	workspace := vmaccess.Target{LabInstanceID: lab, Generation: 1, VMKey: "workspace",
		ServerID: providerResourceID(t, result.ProviderResources, coreprovider.ResourceTypeServer, "workspace")}
	other := workspace
	other.VMKey = "worker"
	other.ServerID = providerResourceID(t, result.ProviderResources, coreprovider.ResourceTypeServer, "worker")
	manager := vmaccess.NewManager(a, vmaccess.Config{SetupTimeout: 10 * time.Second, IdleTimeout: 80 * time.Millisecond})
	t.Cleanup(func() { _ = manager.Close() })
	// Nova ACTIVE does not assert cloud-init or SSH service readiness. Observe
	// successful pinned SSH before testing cancellation of an active channel.
	for _, target := range []vmaccess.Target{workspace, other} {
		readyCtx, stop := context.WithTimeout(ctx, 90*time.Second)
		attempts := 0
		for {
			attempts++
			s, err := manager.OpenSession(readyCtx, target)
			if err == nil {
				err = s.Setup(readyCtx, func(raw *ssh.Session) error { return raw.Run("true") })
				_ = s.Close()
			}
			if err == nil {
				t.Logf("Actual pinned SSH ready: vmKey=%s attempts=%d", target.VMKey, attempts)
				break
			}
			select {
			case <-readyCtx.Done():
				stop()
				t.Fatal("actual pinned SSH not ready within fixture readiness budget")
			case <-time.After(time.Second):
			}
		}
		stop()
	}

	t.Run("R3-actual-runtime-healthy-and-unread", func(t *testing.T) {
		metadata, err := json.Marshal(struct {
			Provision ProvisionConfig `json:"provision"`
			Target    vmaccess.Target `json:"target"`
		}{a.provision, workspace})
		if err != nil {
			t.Fatal("runtime metadata encoding failed")
		}
		metadataFile := filepath.Join(t.TempDir(), "runtime.json")
		if err := os.WriteFile(metadataFile, metadata, 0600); err != nil {
			t.Fatal("runtime metadata write failed")
		}
		outputDir := os.Getenv("LABBIT_ADDITIONAL_OUTPUT_DIR")
		overlay := os.Getenv("LABBIT_ADDITIONAL_APP_OVERLAY")
		if outputDir == "" || overlay == "" {
			t.Fatal("runtime child fixture not configured")
		}
		stdout, err := os.Create(filepath.Join(outputDir, "actual-runtime.jsonl"))
		if err != nil {
			t.Fatal("runtime child log unavailable")
		}
		defer stdout.Close()
		stderr, err := os.Create(filepath.Join(outputDir, "actual-runtime.stderr.txt"))
		if err != nil {
			t.Fatal("runtime child error log unavailable")
		}
		defer stderr.Close()
		childCtx, stop := context.WithTimeout(ctx, 2*time.Minute)
		defer stop()
		command := exec.CommandContext(childCtx, "go", "test", "-race", "-json", "-count=1", "-timeout=90s", "-overlay", overlay,
			"-run", "^TestIndependentAuditRunControlActualVMShutdown$", "./internal/connector/app")
		command.Env = append(os.Environ(), "LABBIT_ADDITIONAL_RUNTIME_METADATA_FILE="+metadataFile)
		command.Dir = os.Getenv("LABBIT_ADDITIONAL_REPO_DIR")
		if command.Dir == "" {
			t.Fatal("runtime child repository directory missing")
		}
		command.Stdout, command.Stderr = stdout, stderr
		if err := command.Run(); err != nil {
			t.Fatal("actual runtime healthy/unread shutdown failed; preserved child logs")
		}
		t.Log("Actual VM: production runControl/Keystone/target resolution/pinned SSH/PTY/Data WSS; healthy and unread peer shutdown, Control ENDED exactly once. Control/Data SaaS peers are mock; VM server-side SSH EOF not independently observed")
	})

	for _, kind := range []string{"cancel", "deadline"} {
		t.Run("R1-real-exec-"+kind, func(t *testing.T) {
			active, err := terminal.NewManagedSSHPTY(ctx, manager, workspace, 80, 24)
			if err != nil {
				t.Fatal("active real PTY failed")
			}
			defer active.Close()
			for i := 0; i < 10; i++ {
				b, err := manager.OpenSession(ctx, workspace)
				if err != nil {
					t.Fatal("real B channel open failed")
				}
				operationCtx, stop := context.WithTimeout(ctx, 120*time.Millisecond)
				if kind == "cancel" {
					stop()
					operationCtx, stop = context.WithCancel(ctx)
				}
				started := make(chan struct{})
				completed := make(chan error, 1)
				go func() {
					completed <- b.Setup(operationCtx, func(raw *ssh.Session) error {
						if err := raw.Start("sleep 3"); err != nil {
							return err
						}
						close(started)
						return raw.Wait()
					})
				}()
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					stop()
					t.Fatal("actual remote command did not start")
				}
				if kind == "cancel" {
					stop()
				}
				var setupErr error
				select {
				case setupErr = <-completed:
				case <-time.After(3 * time.Second):
					stop()
					t.Fatal("cancel did not release Setup waiter")
				}
				stop()
				_ = b.Close()
				want := context.DeadlineExceeded
				if kind == "cancel" {
					want = context.Canceled
				}
				if !errors.Is(setupErr, want) || !errors.Is(setupErr, vmaccess.ErrUnavailable) {
					t.Fatal("Setup lost safe context classification")
				}
				if _, err := active.Write([]byte("printf 'LBT147-%s\\n' 'PTY-OK'\n")); err != nil {
					t.Fatal("B cancellation killed active A write")
				}
				assertPTYMarker(t, active)
			}
			t.Log("Actual VM: 10 canceled/deadlined remote sleep waits, healthy A PTY IO preserved; this is not a stalled pty-req packet injection")
		})
	}

	for _, kind := range []string{"nova-503", "neutron-503", "wrong-generation", "wrong-vm", "unknown-server"} {
		t.Run("R2-real-provider-"+kind, func(t *testing.T) {
			original := a.provider.HTTPClient.Transport
			upstream := original
			if upstream == nil {
				upstream = http.DefaultTransport
			}
			fault := &additionalFaultTransport{upstream: upstream, mode: kind, serverID: workspace.ServerID}
			a.provider.HTTPClient.Transport = fault
			defer func() { a.provider.HTTPClient.Transport = original }()
			target := workspace
			invalid := !strings.HasSuffix(kind, "503")
			if kind == "wrong-generation" {
				target.Generation = 2
			}
			if kind == "wrong-vm" {
				target.VMKey = "not-workspace"
			}
			if kind == "unknown-server" {
				target.ServerID = "00000000-0000-0000-0000-000000000000"
			}
			m := vmaccess.NewManager(a, vmaccess.Config{})
			defer m.Close()
			terminals := terminal.NewSessionManager(time.Minute, nil)
			defer terminals.Shutdown(protocol.TerminalReasonServiceRestart)
			var messages []any
			h := wss.NewHandler(a, wss.SendMessageFunc(func(_ context.Context, msg any) error { messages = append(messages, msg); return nil }))
			h.SetTerminalManager(terminals, nil, terminal.DataWSSClientConfig{EndpointURL: "ws://127.0.0.1:1"})
			h.SetVMPTYFactory(func(callCtx context.Context, target vmaccess.Target, cols, rows int) (terminal.PTYChannel, error) {
				return terminal.NewManagedSSHPTY(callCtx, m, target, cols, rows)
			})
			request := protocol.TerminalOpenMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeTerminalOpen, MessageID: "additional-open", SentAt: time.Now().UTC(),
				TerminalSessionID: "additional-session", LabInstanceID: target.LabInstanceID, Generation: target.Generation},
				Payload: protocol.TerminalOpenPayload{TargetVmKey: target.VMKey, ProviderServerID: target.ServerID, Cols: 80, Rows: 24}}
			raw, _ := json.Marshal(request)
			if h.HandleMessage(ctx, raw) == nil || len(messages) != 1 {
				t.Fatal("provider failure did not return one failed Control response")
			}
			encoded, _ := json.Marshal(messages[0])
			var response protocol.TerminalOpenResultMessage
			_ = json.Unmarshal(encoded, &response)
			want := protocol.TerminalErrAttachFailed
			if invalid {
				want = protocol.TerminalErrInvalidSession
			}
			if response.Payload.Error == nil || response.Payload.Error.Code != want {
				t.Fatal("real Provider Control classification incorrect")
			}
			if strings.Contains(string(encoded), "additional-audit-private-provider-detail") {
				t.Fatal("raw provider error escaped")
			}
			if !invalid && fault.hits.Load() != 1 {
				t.Fatal("scoped API fault not hit exactly once")
			}
			t.Logf("Actual owned cloud target plus scoped fault/affinity: classification=%s injectedResponses=%d", want, fault.hits.Load())
		})
	}

	t.Run("real-host-key-mismatch-and-recovery", func(t *testing.T) {
		_, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		wrong, err := ssh.NewSignerFromKey(private)
		if err != nil {
			t.Fatal(err)
		}
		callback, err := a.SSHHostKeyCallback(ctx, workspace.ServerID)
		if err != nil {
			t.Fatal("enrolled pin unavailable")
		}
		endpoint, err := a.ResolveVMAccess(ctx, workspace)
		if err != nil {
			t.Fatal("real endpoint unavailable")
		}
		remote, err := net.ResolveTCPAddr("tcp", endpoint.Address)
		if err != nil {
			t.Fatal("real remote endpoint invalid")
		}
		if callback(endpoint.Address, remote, wrong.PublicKey()) == nil {
			t.Fatal("wrong host key accepted")
		}
		s := realOpenSession(t, ctx, manager, workspace)
		defer s.Close()
		if err := s.Setup(ctx, func(raw *ssh.Session) error { return raw.Run("true") }); err != nil {
			t.Fatal("valid pinned real server failed after mismatch")
		}
		t.Log("Actual enrolled provider/server pin rejects synthetic hostile key; valid real SSH still succeeds (no real key rotation)")
	})
	t.Run("two-VM-concurrent-SSH-reuse-and-cleanup", func(t *testing.T) {
		var wg sync.WaitGroup
		failures := make(chan struct{}, 2)
		for _, target := range []vmaccess.Target{workspace, other} {
			wg.Add(1)
			go func(target vmaccess.Target) {
				defer wg.Done()
				for i := 0; i < 30; i++ {
					s, err := manager.OpenSession(ctx, target)
					if err != nil {
						failures <- struct{}{}
						return
					}
					err = s.Setup(ctx, func(raw *ssh.Session) error { return raw.Run("true") })
					_ = s.Close()
					if err != nil {
						failures <- struct{}{}
						return
					}
				}
			}(target)
		}
		wg.Wait()
		close(failures)
		if len(failures) > 0 {
			t.Fatal("concurrent real VM channel work failed")
		}
		time.Sleep(200 * time.Millisecond)
		for _, target := range []vmaccess.Target{workspace, other} {
			_ = realSSHConnection(t, ctx, manager, target)
		}
		t.Log("Two actual VMs concurrently completed 30 open/exec/close cycles each, then idle retirement/new authenticated connection")
	})
	_ = manager.Close()
	t.Run("real-server-after-manager-shutdown", func(t *testing.T) {
		if _, err := manager.OpenSession(ctx, workspace); !errors.Is(err, vmaccess.ErrClosed) {
			t.Fatal("manager reopened after shutdown")
		}
		if _, err := servers.Get(ctx, a.compute, workspace.ServerID).Extract(); err != nil {
			t.Fatal("test-owned live server disappeared before owned cleanup")
		}
	})
}
