package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"golang.org/x/crypto/ssh"
)

type lifecycleResolver func(context.Context, vmaccess.Target) (vmaccess.Endpoint, error)

func (r lifecycleResolver) ResolveVMAccess(ctx context.Context, target vmaccess.Target) (vmaccess.Endpoint, error) {
	return r(ctx, target)
}

// This local SSH peer exercises transport closure and surviving channels;
// the Provider mutation result is mocked, without contacting a cloud.
func lifecycleEndpoint(t *testing.T) vmaccess.Endpoint {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(key, "unit-test-only")
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "ssh-key")
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, public ssh.PublicKey) (*ssh.Permissions, error) {
		if ssh.FingerprintSHA256(public) != ssh.FingerprintSHA256(signer.PublicKey()) {
			return nil, ssh.ErrNoAuth
		}
		return nil, nil
	}}
	config.AddHostKey(signer)
	var mu sync.Mutex
	var connections []net.Conn
	var workers sync.WaitGroup
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections = append(connections, conn)
			mu.Unlock()
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer conn.Close()
				server, channels, requests, err := ssh.NewServerConn(conn, config)
				if err != nil {
					return
				}
				defer server.Close()
				go ssh.DiscardRequests(requests)
				for incoming := range channels {
					channel, requests, err := incoming.Accept()
					if err != nil {
						return
					}
					workers.Add(1)
					go func() {
						defer workers.Done()
						defer channel.Close()
						for request := range requests {
							_ = request.Reply(true, nil)
							if request.Type == "shell" {
								_, _ = io.Copy(channel, channel)
								return
							}
						}
					}()
				}
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-acceptDone
		mu.Lock()
		for _, conn := range connections {
			_ = conn.Close()
		}
		mu.Unlock()
		workers.Wait()
	})
	return vmaccess.Endpoint{ProviderConnectionID: "provider-1", Address: listener.Addr().String(), Username: "test-user", PrivateKeyFile: keyFile, HostKeyCallback: ssh.FixedHostKey(signer.PublicKey())}
}

func failedLifecycleResult(states ...string) provider.OperationResult {
	result := provider.OperationResult{Outcome: provider.OutcomeFailed}
	for index, state := range states {
		id := "server-" + strconv.Itoa(index+1)
		name := "worker-" + strconv.Itoa(index+1)
		if index == 1 {
			name = "workspace"
		}
		generation := int64(1)
		if index > 0 && state == "ACTIVE" {
			generation = 2
		}
		result.ProviderResources = append(result.ProviderResources, provider.ResourceResult{
			ResourceRef:   provider.ResourceRef{ResourceType: provider.ResourceTypeServer, ProviderID: id, LogicalName: name, Generation: generation},
			ObservedState: state,
		})
	}
	return result
}

func applyFailedLifecycle(t *testing.T, manager *vmaccess.Manager, mutation string, result provider.OperationResult) {
	t.Helper()
	p := &accessLifecycleProvider{Provider: &provider.MockProvider{
		ResetFunc:   func(context.Context, provider.ResetRequest) (provider.OperationResult, error) { return result, nil },
		CleanupFunc: func(context.Context, provider.CleanupRequest) (provider.OperationResult, error) { return result, nil },
	}, manager: manager}
	var err error
	if mutation == "reset" {
		_, err = p.Reset(context.Background(), provider.ResetRequest{Correlation: provider.Correlation{LabInstanceID: "lab-1", Generation: 2}})
	} else {
		_, err = p.Cleanup(context.Background(), provider.CleanupRequest{Correlation: provider.Correlation{LabInstanceID: "lab-1", Generation: 1}})
	}
	if err != nil {
		t.Fatal(err)
	}
}

func lifecycleShell(t *testing.T, manager *vmaccess.Manager, target vmaccess.Target) (io.WriteCloser, io.Reader) {
	t.Helper()
	session, err := manager.OpenSession(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	in, err := session.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := session.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Setup(context.Background(), func(raw *ssh.Session) error { return raw.Shell() }); err != nil {
		t.Fatal(err)
	}
	return in, out
}

func assertLifecycleEcho(t *testing.T, in io.Writer, out io.Reader) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		if _, err := in.Write([]byte("ping")); err != nil {
			done <- err
			return
		}
		buf := make([]byte, 4)
		_, err := io.ReadFull(out, buf)
		if err == nil && string(buf) != "ping" {
			err = errors.New("unexpected echo")
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("surviving channel failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("surviving channel stopped responding")
	}
}

func TestFailedLifecycleClosesOldChannelsAndPreservesPreflight(t *testing.T) {
	cases := []struct {
		name   string
		states []string
		fence  bool
	}{
		{"partial-delete", []string{"DELETED", "DELETE_FAILED", "DELETE_NOT_ATTEMPTED"}, true},
		{"first-delete-rejected", []string{"DELETE_FAILED", "DELETE_NOT_ATTEMPTED"}, true},
		{"replacement-failed", []string{"DELETED", "DELETED", "ACTIVE"}, true},
		{"validation-failed", nil, false},
		{"preflight-failed", []string{"DELETE_NOT_ATTEMPTED", "DELETE_NOT_ATTEMPTED"}, false},
	}
	for _, mutation := range []string{"reset", "cleanup"} {
		for _, tc := range cases {
			if mutation == "cleanup" && tc.name == "replacement-failed" {
				continue
			}
			t.Run(mutation+"/"+tc.name, func(t *testing.T) {
				endpoint := lifecycleEndpoint(t)
				manager := vmaccess.NewManager(lifecycleResolver(func(context.Context, vmaccess.Target) (vmaccess.Endpoint, error) { return endpoint, nil }), vmaccess.Config{})
				defer manager.Close()
				target := vmaccess.Target{LabInstanceID: "lab-1", Generation: 1, VMKey: "workspace", ServerID: "server-2"}
				in, out := lifecycleShell(t, manager, target)
				assertLifecycleEcho(t, in, out)
				otherTarget := target
				otherTarget.LabInstanceID = "lab-2"
				otherIn, otherOut := lifecycleShell(t, manager, otherTarget)
				applyFailedLifecycle(t, manager, mutation, failedLifecycleResult(tc.states...))
				if tc.fence {
					closed := make(chan error, 1)
					go func() { _, err := io.Copy(io.Discard, out); closed <- err }()
					select {
					case <-closed:
					case <-time.After(time.Second):
						t.Fatal("obsolete channel remained open after destructive failure")
					}
					if _, err := in.Write([]byte("stale")); err == nil {
						t.Fatal("obsolete channel still accepts input")
					}
				} else {
					assertLifecycleEcho(t, in, out)
				}
				reopened, err := manager.OpenSession(context.Background(), target)
				if reopened != nil {
					_ = reopened.Close()
				}
				if tc.fence && !errors.Is(err, vmaccess.ErrInvalidTarget) {
					t.Fatalf("obsolete generation reopened: %v", err)
				}
				if !tc.fence && err != nil {
					t.Fatalf("preflight failure fenced unchanged generation: %v", err)
				}
				assertLifecycleEcho(t, otherIn, otherOut)
			})
		}
	}
}

func TestFailedLifecycleRejectsDelayedOldResolutionAndSetup(t *testing.T) {
	for _, mutation := range []string{"reset", "cleanup"} {
		for _, stage := range []string{"resolve", "setup"} {
			t.Run(mutation+"/"+stage, func(t *testing.T) {
				endpoint := lifecycleEndpoint(t)
				entered, release := make(chan struct{}), make(chan struct{})
				var once sync.Once
				unblock := func() { once.Do(func() { close(release) }) }
				defer unblock()
				manager := vmaccess.NewManager(lifecycleResolver(func(context.Context, vmaccess.Target) (vmaccess.Endpoint, error) {
					if stage == "resolve" {
						close(entered)
						<-release
					}
					return endpoint, nil
				}), vmaccess.Config{})
				defer manager.Close()
				target := vmaccess.Target{LabInstanceID: "lab-1", Generation: 1, VMKey: "workspace", ServerID: "server-2"}
				done := make(chan error, 1)
				if stage == "resolve" {
					go func() {
						session, err := manager.OpenSession(context.Background(), target)
						if session != nil {
							_ = session.Close()
						}
						done <- err
					}()
				} else {
					session, err := manager.OpenSession(context.Background(), target)
					if err != nil {
						t.Fatal(err)
					}
					defer session.Close()
					go func() {
						done <- session.Setup(context.Background(), func(*ssh.Session) error { close(entered); <-release; return nil })
					}()
				}
				<-entered
				applyFailedLifecycle(t, manager, mutation, failedLifecycleResult("DELETED", "DELETE_FAILED"))
				unblock()
				select {
				case err := <-done:
					if !errors.Is(err, vmaccess.ErrInvalidTarget) {
						t.Fatalf("late obsolete %s accepted: %v", stage, err)
					}
				case <-time.After(time.Second):
					t.Fatal("obsolete setup did not finish")
				}
			})
		}
	}
}
