package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
	"golang.org/x/crypto/ssh"
)

type pinnedTestProvider struct {
	*provider.MockProvider
	callback ssh.HostKeyCallback
	keyFile  string
}

func (p *pinnedTestProvider) ResolveVMAccess(ctx context.Context, target vmaccess.Target) (vmaccess.Endpoint, error) {
	if target.LabInstanceID != "lab-1" || target.Generation != 1 {
		return vmaccess.Endpoint{}, vmaccess.ErrInvalidTarget
	}
	address, err := p.ResolveServerAddress(ctx, target.VMKey, target.ServerID)
	if err != nil {
		return vmaccess.Endpoint{}, err
	}
	return vmaccess.Endpoint{ProviderConnectionID: "provider-1", Address: address, Username: "test-user", PrivateKeyFile: p.keyFile, HostKeyCallback: p.callback}, nil
}

func (p *pinnedTestProvider) SSHHostKeyCallback(_ context.Context, id string) (ssh.HostKeyCallback, error) {
	if id != "server-1" {
		return nil, errProviderInitialization
	}
	return p.callback, nil
}

func TestLatestMainControlProviderAndTerminalShareRuntime(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
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
	defer listener.Close()
	privateKeyBlock, err := ssh.MarshalPrivateKey(key, "unit-test-only")
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "ssh-key")
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(privateKeyBlock), 0600); err != nil {
		t.Fatal(err)
	}
	sshClosed := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			close(sshClosed)
			return
		}
		defer conn.Close()
		defer close(sshClosed)
		cfg := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, public ssh.PublicKey) (*ssh.Permissions, error) {
			if ssh.FingerprintSHA256(public) == ssh.FingerprintSHA256(signer.PublicKey()) {
				return nil, nil
			}
			return nil, ssh.ErrNoAuth
		}}
		cfg.AddHostKey(signer)
		server, channels, requests, err := ssh.NewServerConn(conn, cfg)
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
			go func() {
				defer channel.Close()
				for req := range requests {
					if req.WantReply {
						_ = req.Reply(true, nil)
					}
					if req.Type == "shell" {
						_, _ = io.Copy(channel, channel)
						return
					}
				}
			}()
		}
	}()
	saas := mock.NewMockSaaS("integration-token")
	defer saas.Close()
	relay := mock.NewTerminalRelay()
	defer relay.Close()
	relay.SetAuthValidator(func(r *http.Request) int {
		if r.Header.Get("Authorization") != "Bearer integration-token" {
			return http.StatusUnauthorized
		}
		return http.StatusOK
	})
	t.Setenv("LABBIT_ENVIRONMENT", "development")
	t.Setenv("LABBIT_TERMINAL_RELAY_URL", relay.URL())
	t.Setenv("LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE", "")
	t.Setenv("LABBIT_OPENSTACK_SSH_KNOWN_HOSTS_FILE", "")
	t.Setenv("LABBIT_CONNECTOR_RUNTIME_ID", "different-env-runtime-must-not-win")
	p := &pinnedTestProvider{MockProvider: &provider.MockProvider{
		ConnectionID:           "provider-1",
		ValidateConnectionFunc: func(context.Context) error { return nil },
		ResolveServerAddressFunc: func(_ context.Context, vm, id string) (string, error) {
			if vm != "workspace" || id != "server-1" {
				return "", errProviderInitialization
			}
			return listener.Addr().String(), nil
		},
	}, callback: ssh.FixedHostKey(signer.PublicKey()), keyFile: keyFile}
	lazy := newLazyProvider("provider-1", func(context.Context) (runtimeProvider, error) { return p, nil })
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runControl(ctx, wss.Config{BaseURL: saas.URL(), Credential: "integration-token", RuntimeID: "shared-runtime", AllowInsecure: true}, lazy, "connector-1", slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	var hello protocol.HelloMessage
	if err := json.Unmarshal(waitForMessageType(t, saas, protocol.MessageTypeHello, 2*time.Second), &hello); err != nil {
		t.Fatal(err)
	}
	if err := saas.SendRaw(protocol.ProviderRequestMessage{
		BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeProviderRequest, MessageID: "query", SentAt: time.Now().UTC()},
		Payload:      protocol.ProviderRequestPayload{RequestType: protocol.ProviderRequestValidateConnection, ProviderConnectionID: "provider-1"},
	}); err != nil {
		t.Fatal(err)
	}
	var query protocol.ProviderResponseMessage
	if err := json.Unmarshal(waitForMessageType(t, saas, protocol.MessageTypeProviderResponse, 2*time.Second), &query); err != nil {
		t.Fatal(err)
	}
	if query.Payload.Outcome != protocol.OutcomeSucceeded {
		t.Fatalf("Provider query failed: %+v", query.Payload)
	}
	// The complete Control target must cross the lazy Provider/SSH boundary.
	if err := saas.SendRaw(protocol.TerminalOpenMessage{
		BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeTerminalOpen, MessageID: "wrong-lab", SentAt: time.Now().UTC(), TerminalSessionID: "wrong-session", LabInstanceID: "other-lab", Generation: 1},
		Payload:      protocol.TerminalOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server-1", Cols: 80, Rows: 24},
	}); err != nil {
		t.Fatal(err)
	}
	var rejected protocol.TerminalOpenResultMessage
	if err := json.Unmarshal(waitForControlReply(t, saas, "wrong-lab"), &rejected); err != nil {
		t.Fatal(err)
	}
	if rejected.Payload.Outcome != protocol.OutcomeFailed || rejected.Payload.Error == nil || rejected.Payload.Error.Code != protocol.TerminalErrInvalidSession {
		t.Fatalf("wrong LabInstance accepted: %+v", rejected.Payload)
	}
	if err := saas.SendRaw(protocol.TerminalOpenMessage{
		BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeTerminalOpen, MessageID: "open", SentAt: time.Now().UTC(), TerminalSessionID: "session-1", LabInstanceID: "lab-1", Generation: 1},
		Payload:      protocol.TerminalOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server-1", Cols: 80, Rows: 24},
	}); err != nil {
		t.Fatal(err)
	}
	attach, err := relay.WaitForAttach(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if attach.Payload.RuntimeID != hello.Payload.RuntimeID || attach.Payload.RuntimeID != "shared-runtime" {
		t.Fatalf("Control/Data runtime split: %q %q", hello.Payload.RuntimeID, attach.Payload.RuntimeID)
	}
	var opened protocol.TerminalOpenResultMessage
	if err := json.Unmarshal(waitForControlReply(t, saas, "open"), &opened); err != nil {
		t.Fatal(err)
	}
	if opened.Payload.Outcome != protocol.OutcomeSucceeded {
		t.Fatalf("Terminal failed: %+v", opened.Payload)
	}
	if err := relay.SendBinary([]byte("echo")); err != nil {
		t.Fatal(err)
	}
	echo, err := relay.ReadBinary(2 * time.Second)
	if err != nil || string(echo) != "echo" {
		t.Fatalf("PTY echo=%q err=%v", echo, err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runtime did not shut down")
	}
	select {
	case <-sshClosed:
	case <-time.After(time.Second):
		t.Fatal("terminal SSH connection leaked on runtime shutdown")
	}
}

func waitForControlReply(t *testing.T, saas *mock.MockSaaS, replyTo string) []byte {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, raw := range saas.ReceivedMessages() {
			var env protocol.BaseEnvelope
			if json.Unmarshal(raw, &env) == nil && env.Type == protocol.MessageTypeTerminalOpenResult && env.ReplyToMessageID == replyTo {
				return raw
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("correlated Terminal reply was not received")
	return nil
}
