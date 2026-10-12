package terminal

import (
	"crypto/ed25519"
	"crypto/rand"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"golang.org/x/crypto/ssh"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type unreadSSHReadGate struct {
	net.Conn
	pause  atomic.Bool
	resume chan struct{}
}

func (c *unreadSSHReadGate) Read(p []byte) (int, error) {
	if c.pause.Load() {
		<-c.resume
	}
	return c.Conn.Read(p)
}

type unreadSSHTCPWrite struct {
	net.Conn
	active  atomic.Int64
	entered atomic.Int64
}

type resizeEnteredPTY struct {
	PTYChannel
	entered chan struct{}
}

func (p *resizeEnteredPTY) Resize(cols, rows int) error {
	close(p.entered)
	return p.PTYChannel.Resize(cols, rows)
}

func (c *unreadSSHTCPWrite) Write(p []byte) (int, error) {
	c.active.Add(1)
	c.entered.Store(time.Now().UnixNano())
	defer c.active.Add(-1)
	return c.Conn.Write(p)
}

// The peer and generated keys are synthetic. Transport.Write itself is neither
// delayed nor replaced: an unread kernel TCP receive buffer causes backpressure.
func newUnreadSSHPTY(t *testing.T, standalone bool) (*SSHPTY, *unreadSSHTCPWrite) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal("key fixture")
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal("signer fixture")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("listener fixture")
	}
	t.Cleanup(func() { _ = listener.Close() })
	paused := make(chan struct{})
	resume := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(resume) }) })
	go func() {
		raw, err := listener.Accept()
		if err != nil {
			return
		}
		defer raw.Close()
		_ = raw.(*net.TCPConn).SetReadBuffer(1024)
		gated := &unreadSSHReadGate{Conn: raw, resume: resume}
		cfg := &ssh.ServerConfig{NoClientAuth: true}
		cfg.AddHostKey(signer)
		sc, incoming, requests, err := ssh.NewServerConn(gated, cfg)
		if err != nil {
			return
		}
		defer sc.Close()
		go ssh.DiscardRequests(requests)
		ch, reqs, err := (<-incoming).Accept()
		if err != nil {
			return
		}
		defer ch.Close()
		for req := range reqs {
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
			if req.Type == "shell" {
				gated.pause.Store(true)
				close(paused)
				<-resume
				return
			}
		}
	}()
	raw, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal("dial fixture")
	}
	t.Cleanup(func() { _ = raw.Close() })
	_ = raw.(*net.TCPConn).SetWriteBuffer(1024)
	observed := &unreadSSHTCPWrite{Conn: raw}
	cc, chs, reqs, err := ssh.NewClientConn(observed, listener.Addr().String(), &ssh.ClientConfig{User: "fixture", HostKeyCallback: ssh.FixedHostKey(signer.PublicKey())})
	if err != nil {
		t.Fatal("handshake fixture")
	}
	client := ssh.NewClient(cc, chs, reqs)
	t.Cleanup(func() { _ = client.Close() })
	ss, err := client.NewSession()
	if err != nil {
		t.Fatal("channel fixture")
	}
	// Managed factory delegates channel ownership to its manager callback. This
	// fixture callback records/release semantics only and does no transport write.
	var owner *ssh.Client
	closeChannel := func() error { return nil }
	if standalone {
		owner, closeChannel = client, nil
	}
	pty, err := newSSHSessionPTY(ss, owner, closeChannel, 80, 24)
	if err != nil {
		t.Fatal("pty fixture")
	}
	<-paused
	wrote := make(chan struct{})
	go func() { defer close(wrote); _, _ = pty.Write(make([]byte, 16<<20)) }()
	deadline := time.Now().Add(4 * time.Second)
	for observed.active.Load() != 1 || time.Since(time.Unix(0, observed.entered.Load())) < 200*time.Millisecond {
		if time.Now().After(deadline) {
			t.Fatal("SSH transport did not reach actual sustained TCP write backpressure")
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Cleanup(func() {
		_ = raw.Close()
		select {
		case <-wrote:
		case <-time.After(time.Second):
			t.Error("fixture input worker did not release")
		}
	})
	return pty, observed
}

func TestSSHPTYCloseWithUnreadPeer(t *testing.T) {
	for _, standalone := range []bool{false, true} {
		name := "managed"
		if standalone {
			name = "standalone"
		}
		t.Run(name, func(t *testing.T) {
			pty, transport := newUnreadSSHPTY(t, standalone)
			closed := make(chan struct{})
			go func() { _ = pty.Close(); close(closed) }()
			select {
			case <-closed:
				t.Log("actualTCPBackpressure=true managedPTYCloseReturned=true")
			case <-time.After(750 * time.Millisecond):
				t.Error("managed PTY close blocked by unread SSH transport; runtime cannot reach shared manager Close")
				_ = transport.Close()
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Error("fixture close did not release after transport close")
			}
		})
	}
}

func TestSessionShutdownWithUnreadSSHResize(t *testing.T) {
	pty, transport := newUnreadSSHPTY(t, false)
	observedPTY := &resizeEnteredPTY{PTYChannel: pty, entered: make(chan struct{})}
	manager := NewSessionManager(time.Minute, nil)
	session, _, err := manager.GetOrCreateSession(protocol.TerminalOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server-1"}, protocol.BaseEnvelope{TerminalSessionID: "audit-resize", LabInstanceID: "lab-1", Generation: 1}, func() (PTYChannel, error) { return observedPTY, nil })
	if err != nil {
		t.Fatal(err)
	}
	resizing := make(chan struct{})
	go func() { _ = session.Resize(81, 25); close(resizing) }()
	select {
	case <-observedPTY.entered:
	case <-time.After(time.Second):
		t.Fatal("resize did not enter SSH PTY")
	}
	select {
	case <-resizing:
		t.Fatal("resize unexpectedly completed against unread SSH peer")
	case <-time.After(200 * time.Millisecond):
	}
	shutdown := make(chan struct{})
	go func() { manager.Shutdown(protocol.TerminalReasonServiceRestart); close(shutdown) }()
	select {
	case <-shutdown:
		t.Log("actualTCPBackpressure=true resizePending=true shutdownReturned=true")
	case <-time.After(750 * time.Millisecond):
		t.Error("Terminal shutdown blocked acquiring state locks held by unread SSH window-change")
		_ = transport.Close()
	}
	// runControl can reach the shared manager's socket close only after Terminal
	// Shutdown returns. This peer fixture models that subsequent ownership step.
	_ = transport.Close()
	select {
	case <-resizing:
	case <-time.After(time.Second):
		t.Error("resize did not release after transport close")
	}
	select {
	case <-shutdown:
	case <-time.After(time.Second):
		t.Error("shutdown did not release after transport close")
	}
}
