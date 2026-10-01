package openstackprovider

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestRunSSHCommandCancellationClosesStalledHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	started := make(chan struct{})
	serverDone := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
		reader := bufio.NewReader(connection)
		if _, err := reader.ReadString('\n'); err != nil {
			serverDone <- err
			return
		}
		close(started)
		// Do not send a server version: the real SSH client must remain blocked
		// in its handshake until cancellation closes the TCP connection.
		_, err = io.Copy(io.Discard, reader)
		serverDone <- err
	}()

	adapter := sshCancellationAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	t.Cleanup(cancel)
	result := make(chan error, 1)
	go func() { result <- adapter.runSSHCommand(ctx, listener.Addr().String(), "test-server:22", "true") }()
	awaitSSHTestSignal(t, started, "client handshake")
	cancel()
	assertSSHTestCanceled(t, result)
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("cancellation did not close the peer socket cleanly: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("peer socket remained open after cancellation")
	}
}

func TestRunSSHCommandCancellationClosesStalledCommand(t *testing.T) {
	started := make(chan struct{})
	address, serverDone := stalledCommandSSHServer(t, started)
	adapter := sshCancellationAdapter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	t.Cleanup(cancel)
	result := make(chan error, 1)
	go func() { result <- adapter.runSSHCommand(ctx, address, "test-server:22", "cloud-init status --wait") }()
	awaitSSHTestSignal(t, started, "authenticated SSH exec")
	cancel()
	assertSSHTestCanceled(t, result)
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("authenticated SSH connection remained open after cancellation")
	}
}

func sshCancellationAdapter(t *testing.T) *Adapter {
	t.Helper()
	return &Adapter{provision: ProvisionConfig{
		SSHUsername:       "test-user",
		SSHPrivateKeyFile: writeTestPrivateKey(t, "ssh-cancellation"),
		SSHKnownHostsFile: filepath.Join(t.TempDir(), "known_hosts"),
	}}
}

func stalledCommandSSHServer(t *testing.T, started chan<- struct{}) (string, <-chan error) {
	t.Helper()
	signer, err := ssh.NewSignerFromKey(testPrivateKey("ssh-cancellation-server"))
	if err != nil {
		t.Fatal(err)
	}
	clientSigner, err := ssh.NewSignerFromKey(testPrivateKey("ssh-cancellation"))
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if string(key.Marshal()) != string(clientSigner.PublicKey().Marshal()) {
			return nil, fmt.Errorf("unexpected test SSH public key")
		}
		return nil, nil
	}}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	done := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
		server, channels, requests, err := ssh.NewServerConn(connection, config)
		if err != nil {
			done <- err
			return
		}
		defer server.Close()
		go ssh.DiscardRequests(requests)
		channelRequest, ok := <-channels
		if !ok {
			done <- fmt.Errorf("SSH connection ended before session creation")
			return
		}
		channel, channelRequests, err := channelRequest.Accept()
		if err != nil {
			done <- err
			return
		}
		defer channel.Close()
		request, ok := <-channelRequests
		if !ok || request.Type != "exec" {
			done <- fmt.Errorf("expected an SSH exec request")
			return
		}
		if err := request.Reply(true, nil); err != nil {
			done <- err
			return
		}
		close(started)
		// Never send exit-status or close the session before client cancellation.
		done <- server.Wait()
	}()
	return listener.Addr().String(), done
}

func awaitSSHTestSignal(t *testing.T, signal <-chan struct{}, stage string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("SSH test did not reach %s", stage)
	}
}

func assertSSHTestCanceled(t *testing.T, result <-chan error) {
	t.Helper()
	select {
	case err := <-result:
		if !errors.Is(err, ErrStartupNotReady) {
			t.Fatalf("canceled SSH command returned %v, want a readiness error", err)
		}
	case <-time.After(time.Second):
		t.Fatal("SSH command did not return promptly after cancellation")
	}
}
