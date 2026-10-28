package vmaccess

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

type pendingDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (c pendingDeadlineContext) Deadline() (time.Time, bool) { return c.deadline, true }

func TestSocketDeadlinePrecedesContextTimer(t *testing.T) {
	ctx := pendingDeadlineContext{Context: context.Background(), deadline: time.Now().Add(-time.Second)}
	err := safeError(ctx, ErrUnavailable)
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired socket deadline lost timeout classification: %v", err)
	}
	ctx.deadline = time.Now().Add(time.Minute)
	if err := safeError(ctx, ErrUnavailable); errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("future deadline classified as a timeout")
	}
}

func TestHandshakeTimeoutAndCancellationCloseSocket(t *testing.T) {
	for _, mode := range []string{"timeout", "cancel", "shutdown", "reset"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			p := newSSHPeer(t)
			endpoint := p.endpoint
			endpoint.Address = listener.Addr().String()
			m := NewManager(resolverFunc(func(context.Context, Target) (Endpoint, error) { return endpoint, nil }), Config{SetupTimeout: 150 * time.Millisecond})
			defer m.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := m.OpenSession(ctx, testTarget()); done <- err }()
			peer, err := listener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			_ = peer.SetReadDeadline(time.Now().Add(time.Second))
			if _, err := bufio.NewReader(peer).ReadString('\n'); err != nil {
				t.Fatalf("client did not start SSH handshake: %v", err)
			}
			switch mode {
			case "cancel":
				cancel()
			case "shutdown":
				_ = m.Close()
			case "reset":
				m.InvalidateBefore(testTarget().LabInstanceID, 2)
			}
			select {
			case err := <-done:
				want := context.DeadlineExceeded
				if mode != "timeout" {
					want = context.Canceled
				}
				if !errors.Is(err, want) {
					t.Fatalf("handshake failure=%v", err)
				}
				info, ok := Diagnostic(err)
				if !ok || info.Stage != StageHandshake || info.Timeout != (mode == "timeout") {
					t.Fatalf("handshake diagnostic lost: %+v, present=%v", info, ok)
				}
				if (mode == "timeout" || mode == "cancel") && (!errors.Is(err, ErrUnavailable) || err.Error() != errors.Join(ErrUnavailable, want).Error()) {
					t.Error("handshake failure lost safe classification or retained raw details")
				}
				if mode == "timeout" && info.ElapsedMS <= 0 {
					t.Fatalf("handshake elapsed time lost: %+v", info)
				}
			case <-time.After(time.Second):
				t.Fatal("handshake did not stop")
			}
			_ = peer.SetReadDeadline(time.Now().Add(time.Second))
			if _, err := io.Copy(io.Discard, peer); err != nil {
				t.Fatal("handshake socket was not closed")
			}
			m.mu.Lock()
			remaining := len(m.entries)
			m.mu.Unlock()
			if remaining != 0 {
				t.Fatal("failed handshake cached")
			}
		})
	}
}

func TestHandshakeTimeoutKeepsOtherVMChannelUsable(t *testing.T) {
	peer := newSSHPeer(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	badTarget := testTarget()
	badTarget.LabInstanceID = "unreachable-lab"
	badTarget.ServerID = "unreachable-server"
	m := NewManager(resolverFunc(func(_ context.Context, target Target) (Endpoint, error) {
		endpoint := peer.endpoint
		if target == badTarget {
			endpoint.Address = listener.Addr().String()
		}
		return endpoint, nil
	}), Config{SetupTimeout: 150 * time.Millisecond})
	defer m.Close()
	good := openSession(t, m, testTarget())
	defer good.Close()
	in, out := echoChannel(t, good, false)
	done := make(chan error, 1)
	go func() { _, err := m.OpenSession(context.Background(), badTarget); done <- err }()
	stalled, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer stalled.Close()
	select {
	case err := <-done:
		info, ok := Diagnostic(err)
		if !errors.Is(err, context.DeadlineExceeded) || !ok || info.Stage != StageHandshake || !info.Timeout {
			t.Fatalf("handshake timeout diagnostic = %+v, present=%v, error=%v", info, ok, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stalled VM did not time out")
	}
	assertEcho(t, in, out)
	m.mu.Lock()
	_, badCached := m.entries[badTarget]
	_, goodCached := m.entries[testTarget()]
	m.mu.Unlock()
	if badCached || !goodCached {
		t.Fatal("failed VM transport affected healthy VM cache")
	}
}

func TestResetDuringChannelSetupRejectsLateSuccess(t *testing.T) {
	p := newSSHPeer(t)
	m := testManager(t, p, Config{})
	s := openSession(t, m, testTarget())
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.Setup(context.Background(), func(*ssh.Session) error { close(started); <-release; return nil })
	}()
	<-started
	m.InvalidateBefore("lab-1", 2)
	close(release)
	if err := <-done; !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("setup accepted obsolete target: %v", err)
	}
}

func TestResetDuringResolutionRejectsLateTargetBeforeDial(t *testing.T) {
	p := newSSHPeer(t)
	started, release := make(chan struct{}), make(chan struct{})
	m := NewManager(resolverFunc(func(context.Context, Target) (Endpoint, error) { close(started); <-release; return p.endpoint, nil }), Config{})
	defer m.Close()
	done := make(chan error, 1)
	go func() { _, err := m.OpenSession(context.Background(), testTarget()); done <- err }()
	<-started
	m.InvalidateBefore("lab-1", 2)
	close(release)
	if err := <-done; !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("late target=%v", err)
	}
	if p.accepted.Load() != 0 {
		t.Fatal("invalidated target dialed SSH")
	}
}
