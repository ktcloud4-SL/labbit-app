package terminal

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestDialSSHPTYStopsStalledHandshake(t *testing.T) {
	for _, mode := range []string{"cancel", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			accepted := make(chan net.Conn, 1)
			go func() {
				conn, err := listener.Accept()
				if err == nil {
					accepted <- conn
				}
			}()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			config := &ssh.ClientConfig{User: "test", HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 200 * time.Millisecond}
			if mode == "cancel" {
				config.Timeout = 5 * time.Second
			}
			done := make(chan error, 1)
			go func() {
				pty, err := DialSSHPTY(ctx, listener.Addr().String(), config, 80, 24)
				if pty != nil {
					_ = pty.Close()
				}
				done <- err
			}()
			var peer net.Conn
			select {
			case peer = <-accepted:
			case <-time.After(time.Second):
				t.Fatal("TCP connection not accepted")
			}
			defer peer.Close()
			if mode == "cancel" {
				cancel()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("stalled handshake succeeded")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("SSH handshake ignored cancellation/timeout")
			}
			_ = peer.SetReadDeadline(time.Now().Add(time.Second))
			if _, err := io.Copy(io.Discard, peer); err != nil {
				t.Fatalf("client socket remained open: %v", err)
			}
		})
	}
}

func TestDialSSHPTYSetupDeadlineDoesNotEndLiveSession(t *testing.T) {
	address, clientKey, _, hostKey, cleanup := startTestSSHServer(t)
	defer cleanup()
	pty, err := DialSSHPTY(context.Background(), address, &ssh.ClientConfig{
		User: "test", Auth: []ssh.AuthMethod{ssh.PublicKeys(clientKey)},
		HostKeyCallback: ssh.FixedHostKey(hostKey.PublicKey()), Timeout: time.Second,
	}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer pty.Close()
	// Outlive setup's deadline, then verify actual SSH echo still works.
	time.Sleep(1100 * time.Millisecond)
	if _, err := pty.Write([]byte("alive")); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 5)
		_, err := io.ReadFull(pty, buf)
		if err == nil && string(buf) != "alive" {
			err = io.ErrUnexpectedEOF
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("live PTY lost after setup timeout")
	}
}
