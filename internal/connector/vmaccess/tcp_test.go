package vmaccess

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// Models an SSH channel whose peer keeps the transport alive but never
// completes channel I/O or acknowledges CLOSE. Transport retirement ends it.
type unresponsiveTCP struct {
	net.Conn
	done    <-chan struct{}
	writing chan struct{}
}

func (c *unresponsiveTCP) Read([]byte) (int, error) { <-c.done; return 0, io.EOF }
func (c *unresponsiveTCP) Write([]byte) (int, error) {
	if c.writing != nil {
		close(c.writing)
	}
	<-c.done
	return 0, io.ErrClosedPipe
}
func (c *unresponsiveTCP) Close() error { return nil }

func TestTCPEOFDoesNotReleaseChannelBudgetBeforeSSHClose(t *testing.T) {
	p := newSSHPeer(t)
	m := testManager(t, p, Config{})
	pty := openSession(t, m, testTarget())
	in, out := echoChannel(t, pty, false)
	var transport *connection
	for range maxTCPChannels {
		c, err := m.acquire(context.Background(), testTarget())
		if err != nil {
			t.Fatal(err)
		}
		transport = c
		m.mu.Lock()
		c.tcpChannels++
		m.mu.Unlock()
		raw, peer := net.Pipe()
		stream := m.tcpStream(c, raw, c.done, 8080, func() { m.release(c) })
		_ = peer.Close() // Data EOF, but no SSH CLOSE until transport shutdown.
		if _, err := stream.Read(make([]byte, 1)); err == nil {
			t.Fatal("EOF did not terminate caller stream")
		}
	}
	if _, err := m.DialTCP(context.Background(), testTarget(), 8080); !errors.Is(err, ErrUnavailable) {
		t.Fatal("EOF-only channels escaped transport channel budget")
	}
	assertEcho(t, in, out)
	_ = m.Close()
	eventually(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return transport.tcpChannels == 0
	})
}

func TestTCPCloseUnblocksCallersAndBoundsNonCooperatingPeer(t *testing.T) {
	p := newSSHPeer(t)
	m := testManager(t, p, Config{IdleTimeout: 30 * time.Millisecond})
	pty := openSession(t, m, testTarget())
	in, out := echoChannel(t, pty, false)
	var transport *connection
	for range maxTCPChannels {
		c, err := m.acquire(context.Background(), testTarget())
		if err != nil {
			t.Fatal(err)
		}
		transport = c
		m.mu.Lock()
		c.tcpChannels++
		m.mu.Unlock()
		raw := &unresponsiveTCP{done: c.done, writing: make(chan struct{})}
		stream := m.tcpStream(c, raw, c.done, 8080, func() { m.release(c) })
		reading, writing := make(chan error, 1), make(chan error, 1)
		go func() { _, err := stream.Read(make([]byte, 1)); reading <- err }()
		go func() { _, err := stream.Write(bytes.Repeat([]byte{'x'}, 65536)); writing <- err }()
		select {
		case <-raw.writing:
		case <-time.After(time.Second):
			t.Fatal("raw write not started")
		}
		_ = stream.Close()
		for _, done := range []chan error{reading, writing} {
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("closed caller I/O succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("Close did not interrupt caller I/O")
			}
		}
	}
	if _, err := m.DialTCP(context.Background(), testTarget(), 8080); !errors.Is(err, ErrUnavailable) {
		t.Fatal("lingering pumps escaped per-transport budget")
	}
	assertEcho(t, in, out)
	if p.accepted.Load() != 1 {
		t.Fatal("budget exhaustion replaced sibling transport")
	}
	_ = pty.Close()
	eventually(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return transport.retired && transport.tcpChannels == 0
	})
	eventually(t, func() bool { return p.active.Load() == 0 })
}

func TestTCPSSHUnavailableAndHostPinFailureAreSafe(t *testing.T) {
	for _, mode := range []string{"pin", "offline"} {
		t.Run(mode, func(t *testing.T) {
			p := newSSHPeer(t)
			if mode == "pin" {
				p.endpoint.HostKeyCallback = func(string, net.Addr, ssh.PublicKey) error { return errors.New("private host key mismatch") }
			} else {
				_ = p.listener.Close()
			}
			m := testManager(t, p, Config{})
			if _, err := m.DialTCP(context.Background(), testTarget(), 8080); !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "private") {
				t.Fatalf("SSH failure not sanitized: %v", err)
			}
		})
	}
}

func TestTCPSharedTransportExactDestinationAndIsolation(t *testing.T) {
	p := newSSHPeer(t)
	m := testManager(t, p, Config{IdleTimeout: 50 * time.Millisecond})
	pty := openSession(t, m, testTarget())
	file := openSession(t, m, testTarget())
	ptyIn, ptyOut := echoChannel(t, pty, false)
	fileIn, fileOut := echoChannel(t, file, true)
	ctx, cancel := context.WithCancel(context.Background())
	tcp, err := m.DialTCP(ctx, testTarget(), 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	cancel() // Setup context does not own an established stream.
	request := <-p.forwarded
	if request.Host != "127.0.0.1" || request.Port != 5000 {
		t.Fatal("wrong SSH destination")
	}
	assertEcho(t, tcp, tcp)
	if p.accepted.Load() != 1 {
		t.Fatal("TCP did not share PTY/SFTP transport")
	}
	_ = tcp.Close()
	_ = tcp.Close()
	assertEcho(t, ptyIn, ptyOut)
	assertEcho(t, fileIn, fileOut)
	other, err := m.DialTCP(context.Background(), testTarget(), 80)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	_ = pty.Close()
	_ = file.Close()
	assertEcho(t, other, other)
	_ = other.Close()
	eventually(t, func() bool { return p.active.Load() == 0 })
}

func TestTCPValidationAndSafeErrors(t *testing.T) {
	p := newSSHPeer(t)
	var resolved int
	m := NewManager(resolverFunc(func(_ context.Context, target Target) (Endpoint, error) {
		resolved++
		if target != testTarget() {
			return Endpoint{}, ErrInvalidTarget
		}
		return p.endpoint, nil
	}), Config{})
	defer m.Close()
	for _, port := range []int{-1, 0, 22, 65536} {
		if _, err := m.DialTCP(context.Background(), testTarget(), port); !errors.Is(err, ErrInvalidTarget) {
			t.Fatal("invalid port accepted")
		}
	}
	if resolved != 0 {
		t.Fatal("rejected port reached resolver")
	}
	for _, target := range []Target{
		{LabInstanceID: "other", Generation: 1, VMKey: "workspace", ServerID: "server-1"},
		{LabInstanceID: "lab-1", Generation: 2, VMKey: "workspace", ServerID: "server-1"},
		{LabInstanceID: "lab-1", Generation: 1, VMKey: "other", ServerID: "server-1"},
		{LabInstanceID: "lab-1", Generation: 1, VMKey: "workspace", ServerID: "other"},
	} {
		if _, err := m.DialTCP(context.Background(), target, 8080); !errors.Is(err, ErrInvalidTarget) {
			t.Fatal("wrong target accepted")
		}
	}
	for mode, expected := range map[int64]error{1: ErrConnectionRefused, 2: ErrUnavailable} {
		p.rejectTCP.Store(mode)
		_, err := m.DialTCP(context.Background(), testTarget(), 8080)
		if !errors.Is(err, expected) || strings.Contains(err.Error(), "private") {
			t.Fatalf("unsafe or incorrect error: %v", err)
		}
	}
	p.rejectTCP.Store(0)
	tcp, err := m.DialTCP(context.Background(), testTarget(), 8080)
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	m.InvalidateBefore("lab-1", 2)
	if _, err := m.DialTCP(context.Background(), testTarget(), 8080); !errors.Is(err, ErrInvalidTarget) {
		t.Fatal("stale generation accepted")
	}
	if _, err := tcp.Read(make([]byte, 1)); err == nil {
		t.Fatal("invalidated stream still active")
	}
}

func TestTCPOpenTimeoutLateChannelAndSiblingIsolation(t *testing.T) {
	p := newSSHPeer(t)
	m := testManager(t, p, Config{SetupTimeout: 40 * time.Millisecond, IdleTimeout: 50 * time.Millisecond})
	pty := openSession(t, m, testTarget())
	in, out := echoChannel(t, pty, false)
	p.stallOpen.Store(true)
	_, err := m.DialTCP(context.Background(), testTarget(), 8080)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout not classified: %v", err)
	}
	late := <-p.heldOpen
	m.mu.Lock()
	pending := m.entries[testTarget()].tcpChannels
	m.mu.Unlock()
	if pending != 1 {
		t.Fatal("timed-out in-flight open lost its transport slot")
	}
	for range 4 {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		_, err := m.DialTCP(ctx, testTarget(), 8080)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("waiter timeout: %v", err)
		}
	}
	if p.openCount.Load() != 2 {
		t.Fatal("canceled waiters started unbounded channel workers")
	}
	assertEcho(t, in, out)
	p.stallOpen.Store(false)
	channel, requests, err := late.Accept()
	if err != nil {
		t.Fatal(err)
	}
	go ssh.DiscardRequests(requests)
	closed := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, channel); _ = channel.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("late successful channel leaked")
	}
	eventually(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.entries[testTarget()].tcpChannels == 0
	})
	assertEcho(t, in, out)
	tcp, err := m.DialTCP(context.Background(), testTarget(), 8080)
	if err != nil {
		t.Fatal(err)
	}
	assertEcho(t, tcp, tcp)
	_ = tcp.Close()
	_ = pty.Close()
	eventually(t, func() bool { return p.active.Load() == 0 })
}

func TestTCPCancelAndShutdownPendingOpen(t *testing.T) {
	p := newSSHPeer(t)
	m := testManager(t, p, Config{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.DialTCP(ctx, testTarget(), 8080); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled dial: %v", err)
	}
	p.stallOpen.Store(true)
	done := make(chan error, 1)
	go func() { _, err := m.DialTCP(context.Background(), testTarget(), 8080); done <- err }()
	select {
	case <-p.heldOpen:
	case <-time.After(time.Second):
		t.Fatal("open not started")
	}
	_ = m.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("shutdown dial succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not unblock dial")
	}
	eventually(t, func() bool { return p.active.Load() == 0 })
}
