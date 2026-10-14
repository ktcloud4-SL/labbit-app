package terminal

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"golang.org/x/crypto/ssh"
)

func managedWaitWorkers() int {
	buf := make([]byte, 4<<20)
	n := runtime.Stack(buf, true)
	count := 0
	for _, stack := range strings.Split(string(buf[:n]), "\n\n") {
		if strings.Contains(stack, "terminal.newSSHSessionPTY.func1") {
			count++
		}
	}
	return count
}

// Use the production VMAccess setup worker and real SSH requests. Cancellation
// races with a successful shell reply and initial stdout; an abandoned pipe
// must release its Wait worker without retiring a sibling's shared transport.
func TestManagedPTYCanceledSetupReleasesOutputWorkers(t *testing.T) {
	baseline := managedWaitWorkers()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(key, "generated-test-fixture")
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	type gate struct {
		phase            string
		entered, release chan struct{}
	}
	gates := make(chan gate, 1)
	serverDone := make(chan struct{})
	var peerMu sync.Mutex
	var peer *ssh.ServerConn
	stopPeer := func() {
		peerMu.Lock()
		defer peerMu.Unlock()
		if peer != nil {
			_ = peer.Close()
		}
	}
	t.Cleanup(stopPeer)
	go func() {
		defer close(serverDone)
		raw, err := listener.Accept()
		if err != nil {
			return
		}
		defer raw.Close()
		cfg := &ssh.ServerConfig{NoClientAuth: true}
		cfg.AddHostKey(signer)
		sc, channels, requests, err := ssh.NewServerConn(raw, cfg)
		if err != nil {
			return
		}
		peerMu.Lock()
		peer = sc
		peerMu.Unlock()
		defer sc.Close()
		go ssh.DiscardRequests(requests)
		var workers sync.WaitGroup
		defer workers.Wait()
		for incoming := range channels {
			var current *gate
			select {
			case g := <-gates:
				current = &g
			default:
			}
			// Bind this channel's gate before ACK permits the caller to enqueue
			// a gate for the next channel. A probe has no PTY requests to signal.
			ch, reqs, err := incoming.Accept()
			if err != nil {
				continue
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer ch.Close()
				for req := range reqs {
					if current != nil && req.Type == current.phase {
						close(current.entered)
						<-current.release
					}
					if req.WantReply {
						_ = req.Reply(true, nil)
					}
					if req.Type == "shell" {
						if current != nil {
							// Output is intentionally unread if setup was abandoned.
							_, _ = ch.Write(make([]byte, 64<<10))
						}
						_, _ = io.Copy(ch, ch)
						return
					}
				}
			}()
		}
	}()
	target := vmaccess.Target{LabInstanceID: "cancel-lab", Generation: 1, VMKey: "workspace", ServerID: "server-1"}
	access := vmaccess.NewManager(unreadManagerSSHResolver{endpoint: vmaccess.Endpoint{ProviderConnectionID: "fixture", Address: listener.Addr().String(), Username: "fixture", PrivateKeyFile: keyFile, HostKeyCallback: ssh.FixedHostKey(signer.PublicKey())}}, vmaccess.Config{SetupTimeout: time.Second, IdleTimeout: time.Minute})
	t.Cleanup(func() { _ = access.Close() })
	sibling, err := NewManagedSSHPTY(context.Background(), access, target, 80, 24)
	if err != nil {
		t.Fatal("sibling setup failed")
	}
	t.Cleanup(func() { _ = sibling.Close() })
	for _, phase := range []string{"pty-req", "shell"} {
		t.Run(phase, func(t *testing.T) {
			for trial := 0; trial < 24; trial++ {
				g := gate{phase: phase, entered: make(chan struct{}), release: make(chan struct{})}
				var once sync.Once
				release := func() { once.Do(func() { close(g.release) }) }
				t.Cleanup(release)
				gates <- g
				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)
				type result struct {
					pty PTYChannel
					err error
				}
				done := make(chan result, 1)
				go func() { p, err := NewManagedSSHPTY(ctx, access, target, 80, 24); done <- result{p, err} }()
				select {
				case <-g.entered:
				case <-time.After(2 * time.Second):
					t.Fatal("setup did not reach gated request")
				}
				if trial%2 == 0 {
					cancel()
					release()
				} else {
					release()
					cancel()
				}
				select {
				case r := <-done:
					if r.err != nil && r.pty != nil {
						t.Fatal("canceled setup transferred a PTY")
					}
					if r.err == nil && r.pty == nil {
						t.Fatal("successful setup lost PTY ownership")
					}
					if r.pty != nil {
						_ = r.pty.Close()
					}
				case <-time.After(2 * time.Second):
					t.Fatal("canceled setup did not return")
				}
				// A new channel also waits for the previous bounded setup worker.
				probe, err := access.OpenSession(context.Background(), target)
				if err != nil {
					t.Fatal("late result stranded setup worker or retired shared transport")
				}
				_ = probe.Close()
			}
		})
	}
	// The original PTY must remain usable over the same SSH connection.
	readDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 5)
		_, err := io.ReadFull(sibling, buf)
		if err == nil && string(buf) != "alive" {
			err = io.ErrUnexpectedEOF
		}
		readDone <- err
	}()
	if _, err := sibling.Write([]byte("alive")); err != nil {
		t.Fatal("sibling write failed")
	}
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatal("sibling output failed")
		}
	case <-time.After(time.Second):
		t.Fatal("sibling output stalled")
	}
	_ = sibling.Close()
	_ = access.Close()
	stopPeer()
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("SSH fixture workers remained")
	}
	deadline := time.Now().Add(2 * time.Second)
	for managedWaitWorkers() > baseline {
		if time.Now().After(deadline) {
			t.Fatal("abandoned PTY output/Wait worker leaked after shared transport closed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
