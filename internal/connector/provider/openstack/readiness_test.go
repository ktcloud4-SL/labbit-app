package openstackprovider

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestWorkspacePTYProof(t *testing.T) {
	for _, scenario := range []string{"shell roundtrip", "echo only", "PTY rejected", "shell rejected", "authentication rejected", "host pin mismatch", "stalled read"} {
		t.Run(scenario, func(t *testing.T) {
			adapter := sshCancellationAdapter(t)
			adapter.provision.ProviderConnectionID = "test-provider"
			address, hostKey := readinessSSHServer(t, scenario)
			if scenario == "host pin mismatch" {
				var err error
				hostKey, err = ssh.NewSignerFromKey(testPrivateKey("other-host"))
				if err != nil {
					t.Fatal(err)
				}
			}
			identity, err := sshHostKeyIdentity("test-provider", "server")
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(adapter.provision.SSHKnownHostsFile, []byte(knownhosts.Line([]string{identity}, hostKey.PublicKey())+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 350*time.Millisecond)
			defer cancel()
			started := time.Now()
			err = adapter.probeWorkspacePTY(ctx, address, "server")
			if scenario == "shell roundtrip" && err != nil {
				t.Fatalf("shell proof failed: %v", err)
			}
			if scenario != "shell roundtrip" && err == nil {
				t.Fatal("false PTY readiness")
			}
			if time.Since(started) > time.Second {
				t.Fatal("PTY probe did not respect cancellation")
			}
		})
	}
}

func readinessSSHServer(t *testing.T, scenario string) (string, ssh.Signer) {
	t.Helper()
	signer, err := ssh.NewSignerFromKey(testPrivateKey("readiness-host"))
	if err != nil {
		t.Fatal(err)
	}
	client, err := ssh.NewSignerFromKey(testPrivateKey("ssh-cancellation"))
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if scenario == "authentication rejected" || string(key.Marshal()) != string(client.PublicKey().Marshal()) {
			return nil, fmt.Errorf("authentication rejected")
		}
		return nil, nil
	}}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		server, channels, requests, err := ssh.NewServerConn(conn, config)
		if err != nil {
			return
		}
		defer server.Close()
		go ssh.DiscardRequests(requests)
		request, ok := <-channels
		if !ok {
			return
		}
		channel, reqs, err := request.Accept()
		if err != nil {
			return
		}
		defer channel.Close()
		for request := range reqs {
			switch request.Type {
			case "pty-req":
				_ = request.Reply(scenario != "PTY rejected", nil)
			case "shell":
				_ = request.Reply(scenario != "shell rejected", nil)
				if scenario == "shell rejected" {
					continue
				}
				if scenario == "stalled read" {
					_ = server.Wait()
					return
				}
				if scenario == "echo only" {
					_, _ = io.Copy(channel, channel)
					return
				}
				command := exec.Command("/bin/sh")
				command.Stdin = channel
				command.Stdout = channel
				command.Stderr = channel
				_ = command.Start()
				go func() {
					_ = server.Wait()
					if command.Process != nil {
						_ = command.Process.Kill()
					}
				}()
				_ = command.Wait()
				return
			}
		}
	}()
	return listener.Addr().String(), signer
}

func TestProvisionReadinessProofRequiresAllVMsAndWorkspacePTY(t *testing.T) {
	for _, scenario := range []string{"success", "PTY failed", "canceled", "worker SSH failed", "startup failed"} {
		t.Run(scenario, func(t *testing.T) {
			fake := &m2OpenStackFake{t: t}
			adapter := newTestAdapter(t, fake)
			adapter.provision = testProvisionConfig(t)
			adapter.provision.SSHReadyTimeout = 10 * time.Millisecond
			adapter.provision.StartupReadyTimeout = 10 * time.Millisecond
			adapter.sshProbe = func(_ context.Context, address string) error {
				if scenario == "worker SSH failed" && address == "172.16.8.201:22" {
					return ErrSSHNotReady
				}
				return nil
			}
			adapter.startupProbe = func(context.Context, string, string) error {
				if scenario == "startup failed" {
					return ErrStartupNotReady
				}
				return nil
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			adapter.ptyProbe = func(_ context.Context, address, id string) error {
				calls++
				if address != "172.16.8.200:22" || id != "server-workspace" {
					t.Errorf("wrong workspace PTY target")
				}
				if scenario == "PTY failed" {
					return ErrSSHNotReady
				}
				if scenario == "canceled" {
					cancel()
					return context.Canceled
				}
				return nil
			}
			request := multiVMNoScriptRequest()
			if scenario == "startup failed" {
				request.CreationSnapshot.StartupScript = validSnapshot().StartupScript
			}
			result, err := adapter.Provision(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "success" {
				if result.Outcome != coreprovider.OutcomeSucceeded || result.Readiness == nil || result.Readiness.ServerID != "server-workspace" || result.Readiness.Generation != request.Generation || calls != 1 {
					t.Fatalf("missing proof: %+v calls=%d", result, calls)
				}
			} else if result.Readiness != nil || result.Outcome == coreprovider.OutcomeSucceeded {
				t.Fatalf("false readiness: %+v", result)
			}
		})
	}
}
