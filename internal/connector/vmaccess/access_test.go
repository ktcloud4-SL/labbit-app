package vmaccess

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

type resolverFunc func(context.Context, Target) (Endpoint, error)

func (f resolverFunc) ResolveVMAccess(ctx context.Context, target Target) (Endpoint, error) {
	return f(ctx, target)
}

type sshPeer struct {
	endpoint     Endpoint
	listener     net.Listener
	accepted     atomic.Int64
	active       atomic.Int64
	mu           sync.Mutex
	connections  []net.Conn
	stallOpen    atomic.Bool
	stallRequest atomic.Bool
	openCount    atomic.Int64
	heldOpen     chan ssh.NewChannel
	heldRequest  chan string
	forwarded    chan tcpRequest
	rejectTCP    atomic.Int64
}

type tcpRequest struct {
	Host       string
	Port       uint32
	OriginHost string
	OriginPort uint32
}

func newSSHPeer(t *testing.T) *sshPeer {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.MarshalPrivateKey(private, "unit-test-only")
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "client-key")
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(key), 0600); err != nil {
		t.Fatal(err)
	}
	signer, _ := ssh.NewSignerFromKey(private)
	_, hostPrivate, _ := ed25519.GenerateKey(rand.Reader)
	host, _ := ssh.NewSignerFromKey(hostPrivate)
	config := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, public ssh.PublicKey) (*ssh.Permissions, error) {
		if bytes.Equal(public.Marshal(), signer.PublicKey().Marshal()) {
			return nil, nil
		}
		return nil, errors.New("raw authentication detail must remain private")
	}}
	config.AddHostKey(host)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &sshPeer{listener: listener, heldOpen: make(chan ssh.NewChannel, 64), heldRequest: make(chan string, 64), forwarded: make(chan tcpRequest, 64), endpoint: Endpoint{ProviderConnectionID: "provider-1", Address: listener.Addr().String(), Username: "test-user", PrivateKeyFile: keyFile, HostKeyCallback: ssh.FixedHostKey(host.PublicKey())}}
	t.Cleanup(func() { _ = listener.Close(); p.disconnect() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			p.mu.Lock()
			p.connections = append(p.connections, conn)
			p.mu.Unlock()
			go func() {
				defer conn.Close()
				cc, channels, requests, err := ssh.NewServerConn(conn, config)
				if err != nil {
					return
				}
				p.accepted.Add(1)
				p.active.Add(1)
				defer p.active.Add(-1)
				defer cc.Close()
				go ssh.DiscardRequests(requests)
				for next := range channels {
					p.openCount.Add(1)
					if p.stallOpen.Load() {
						p.heldOpen <- next
						continue
					}
					if next.ChannelType() == "direct-tcpip" {
						var target tcpRequest
						if err := ssh.Unmarshal(next.ExtraData(), &target); err != nil {
							_ = next.Reject(ssh.ConnectionFailed, "invalid target")
							continue
						}
						p.forwarded <- target
						if mode := p.rejectTCP.Load(); mode != 0 {
							reason, message := ssh.ConnectionFailed, "connect failed: Connection refused (private detail)"
							if mode == 2 {
								reason, message = ssh.Prohibited, "private forwarding policy detail"
							}
							_ = next.Reject(reason, message)
							continue
						}
						ch, reqs, err := next.Accept()
						if err != nil {
							return
						}
						go ssh.DiscardRequests(reqs)
						go func() { defer ch.Close(); _, _ = io.Copy(ch, ch) }()
						continue
					}
					channel, requests, err := next.Accept()
					if err != nil {
						return
					}
					go func() {
						defer channel.Close()
						for request := range requests {
							if p.stallRequest.Load() {
								p.heldRequest <- request.Type
								continue
							}
							_ = request.Reply(true, nil)
							if request.Type == "shell" || request.Type == "subsystem" {
								_, _ = io.Copy(channel, channel)
								return
							}
						}
						_, _ = io.Copy(io.Discard, channel)
					}()
				}
			}()
		}
	}()
	return p
}

func (p *sshPeer) disconnect() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, conn := range p.connections {
		_ = conn.Close()
	}
}

func testTarget() Target {
	return Target{LabInstanceID: "lab-1", Generation: 1, VMKey: "workspace", ServerID: "server-1"}
}
func testManager(t *testing.T, p *sshPeer, config Config) *Manager {
	t.Helper()
	m := NewManager(resolverFunc(func(context.Context, Target) (Endpoint, error) { return p.endpoint, nil }), config)
	t.Cleanup(func() { _ = m.Close() })
	return m
}
func eventually(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatal("condition did not become true")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func openSession(t *testing.T, m *Manager, target Target) *Session {
	t.Helper()
	s, err := m.OpenSession(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func echoChannel(t *testing.T, s *Session, subsystem bool) (io.WriteCloser, io.Reader) {
	t.Helper()
	var in io.WriteCloser
	var out io.Reader
	err := s.Setup(context.Background(), func(raw *ssh.Session) error {
		var err error
		in, err = raw.StdinPipe()
		if err != nil {
			return err
		}
		out, err = raw.StdoutPipe()
		if err != nil {
			return err
		}
		if subsystem {
			return raw.RequestSubsystem("sftp")
		}
		if err := raw.RequestPty("xterm", 24, 80, nil); err != nil {
			return err
		}
		return raw.Shell()
	})
	if err != nil {
		t.Fatal(err)
	}
	return in, out
}
func assertEcho(t *testing.T, in io.Writer, out io.Reader) {
	t.Helper()
	if _, err := in.Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		b := make([]byte, 2)
		_, err := io.ReadFull(out, b)
		if err == nil && string(b) != "ok" {
			err = errors.New("wrong echo")
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("channel did not echo")
	}
}

func TestSharedTransportIndependentChannelsAndIdleExpiry(t *testing.T) {
	p := newSSHPeer(t)
	m := testManager(t, p, Config{IdleTimeout: 40 * time.Millisecond})
	pty := openSession(t, m, testTarget())
	file := openSession(t, m, testTarget())
	ptyIn, ptyOut := echoChannel(t, pty, false)
	fileIn, fileOut := echoChannel(t, file, true)
	assertEcho(t, ptyIn, ptyOut)
	_ = pty.Close()
	assertEcho(t, fileIn, fileOut)
	time.Sleep(70 * time.Millisecond)
	if p.accepted.Load() != 1 || p.active.Load() != 1 {
		t.Fatal("closing one channel retired the shared transport")
	}
	_ = file.Close()
	eventually(t, func() bool { return p.active.Load() == 0 })
	newSession := openSession(t, m, testTarget())
	if p.accepted.Load() != 2 {
		t.Fatal("idle transport was reused")
	}
	_ = newSession.Close()
}

func TestConcurrentChannelsShareOneHandshake(t *testing.T) {
	p := newSSHPeer(t)
	m := testManager(t, p, Config{})
	var wg sync.WaitGroup
	errors := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Go(func() {
			s, err := m.OpenSession(context.Background(), testTarget())
			if err == nil {
				_ = s.Close()
			}
			errors <- err
		})
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if p.accepted.Load() != 1 {
		t.Fatalf("handshakes=%d", p.accepted.Load())
	}
}

func TestGenerationAndTargetChangeRetireOldTransport(t *testing.T) {
	p := newSSHPeer(t)
	m := testManager(t, p, Config{})
	old := openSession(t, m, testTarget())
	in, out := echoChannel(t, old, false)
	assertEcho(t, in, out)
	next := testTarget()
	next.Generation = 2
	next.ServerID = "server-2"
	current := openSession(t, m, next)
	if p.accepted.Load() != 2 {
		t.Fatal("generation reused old transport")
	}
	if _, err := m.OpenSession(context.Background(), testTarget()); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("stale generation err=%v", err)
	}
	replacement := next
	replacement.ServerID = "server-3"
	_ = openSession(t, m, replacement)
	if p.accepted.Load() != 3 {
		t.Fatal("new target reused old transport")
	}
	_ = current.Close()
	m.InvalidateBefore("lab-1", 3)
	if _, err := m.OpenSession(context.Background(), replacement); !errors.Is(err, ErrInvalidTarget) {
		t.Fatal("cleanup accepted delayed old open")
	}
}

func TestDisconnectEvictsAndDoesNotReconnectUntilRequested(t *testing.T) {
	p := newSSHPeer(t)
	m := testManager(t, p, Config{})
	s := openSession(t, m, testTarget())
	p.disconnect()
	eventually(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return len(m.entries) == 0 })
	_ = s.Close()
	if p.accepted.Load() != 1 {
		t.Fatal("disconnect caused unsolicited reconnect")
	}
	_ = openSession(t, m, testTarget())
	if p.accepted.Load() != 2 {
		t.Fatal("dead transport was reused")
	}
}

func TestSetupContextIsDetachedAfterSuccessfulOpen(t *testing.T) {
	p := newSSHPeer(t)
	m := testManager(t, p, Config{})
	ctx, cancel := context.WithCancel(context.Background())
	s, err := m.OpenSession(ctx, testTarget())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cancel()
	in, out := echoChannel(t, s, false)
	assertEcho(t, in, out)
}

func TestStalledChannelOpenAndRequestCancellationCleanTransport(t *testing.T) {
	for _, phase := range []string{"open", "request"} {
		t.Run(phase, func(t *testing.T) {
			p := newSSHPeer(t)
			m := testManager(t, p, Config{SetupTimeout: 80 * time.Millisecond, IdleTimeout: 40 * time.Millisecond})
			var err error
			if phase == "open" {
				p.stallOpen.Store(true)
				_, err = m.OpenSession(context.Background(), testTarget())
			} else {
				s := openSession(t, m, testTarget())
				p.stallRequest.Store(true)
				err = s.Setup(context.Background(), func(raw *ssh.Session) error { return raw.RequestSubsystem("sftp") })
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("timeout error=%v", err)
			}
			eventually(t, func() bool { return p.active.Load() == 0 })
		})
	}
}

func TestAuthenticationHostKeyAndPrivateKeyFailuresAreSafe(t *testing.T) {
	for _, kind := range []string{"authentication", "host-key", "private-key", "unreachable", "resolver"} {
		t.Run(kind, func(t *testing.T) {
			p := newSSHPeer(t)
			endpoint := p.endpoint
			switch kind {
			case "authentication":
				other := newSSHPeer(t)
				endpoint.PrivateKeyFile = other.endpoint.PrivateKeyFile
			case "host-key":
				endpoint.HostKeyCallback = func(string, net.Addr, ssh.PublicKey) error { return errors.New("private raw host-key payload") }
			case "private-key":
				endpoint.PrivateKeyFile = filepath.Join(t.TempDir(), "private-path-missing")
			case "unreachable":
				_ = p.listener.Close()
			}
			m := NewManager(resolverFunc(func(context.Context, Target) (Endpoint, error) {
				if kind == "resolver" {
					return Endpoint{}, errors.New("private raw provider payload")
				}
				return endpoint, nil
			}), Config{})
			defer m.Close()
			_, err := m.OpenSession(context.Background(), testTarget())
			if err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "127.0.0.1") {
				t.Fatalf("unsafe failure: %v", err)
			}
			m.mu.Lock()
			remaining := len(m.entries)
			m.mu.Unlock()
			if remaining != 0 {
				t.Fatal("failed handshake remained cached")
			}
		})
	}
}

func TestHandshakeCancelWaiterCancelAndManagerShutdown(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	p := newSSHPeer(t)
	endpoint := p.endpoint
	endpoint.Address = listener.Addr().String()
	m := NewManager(resolverFunc(func(context.Context, Target) (Endpoint, error) { return endpoint, nil }), Config{})
	defer m.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner := make(chan error, 1)
	go func() { _, err := m.OpenSession(ctx, testTarget()); owner <- err }()
	peer, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	waitCtx, waitCancel := context.WithCancel(context.Background())
	waitCancel()
	if _, err := m.OpenSession(waitCtx, testTarget()); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter cancel=%v", err)
	}
	select {
	case err := <-owner:
		t.Fatalf("waiter canceled owner: %v", err)
	default:
	}
	_ = m.Close()
	select {
	case err := <-owner:
		if err == nil {
			t.Fatal("shutdown accepted handshake")
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown left handshake blocked")
	}
	if _, err := m.OpenSession(context.Background(), testTarget()); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed manager=%v", err)
	}
}
