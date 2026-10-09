package vmaccess

import (
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
	for _, mode := range []string{"timeout", "cancel"} {
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
			if mode == "cancel" {
				cancel()
			}
			select {
			case err := <-done:
				want := context.DeadlineExceeded
				if mode == "cancel" {
					want = context.Canceled
				}
				if !errors.Is(err, want) {
					t.Fatalf("handshake failure=%v", err)
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
