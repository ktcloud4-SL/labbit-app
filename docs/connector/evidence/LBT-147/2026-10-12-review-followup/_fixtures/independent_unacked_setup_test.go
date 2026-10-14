package vmaccess

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// The external dependency overlay makes server-side B ignore channel-close.
// Thus the B RequestPty worker cannot finish until the VM transport is retired.
func TestIndependentReviewUnacknowledgedCloseKeepsOneWorkerUntilIdle(t *testing.T) {
	p := newSSHPeer(t)
	m := testManager(t, p, Config{IdleTimeout: 30 * time.Millisecond})
	a := openSession(t, m, testTarget())
	in, out := echoChannel(t, a, false)
	b := openSession(t, m, testTarget())
	p.stallRequest.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- b.Setup(ctx, func(raw *ssh.Session) error { return raw.RequestPty("xterm", 24, 80, nil) })
	}()
	select {
	case <-p.heldRequest:
	case <-time.After(time.Second):
		t.Fatal("B setup not received")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("setup cancel=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("setup cancel blocked")
	}
	assertEcho(t, in, out)
	for range 12 {
		waitCtx, waitCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		channel, err := m.OpenSession(waitCtx, testTarget())
		waitCancel()
		if channel != nil {
			_ = channel.Close()
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("pending setup waiter=%v", err)
		}
	}
	if p.openCount.Load() != 2 {
		t.Fatalf("unacknowledged close spawned new SSH requests: %d", p.openCount.Load())
	}
	assertEcho(t, in, out)
	_ = a.Close()
	eventually(t, func() bool { return p.active.Load() == 0 })
	p.stallRequest.Store(false)
	next := openSession(t, m, testTarget())
	nextIn, nextOut := echoChannel(t, next, false)
	assertEcho(t, nextIn, nextOut)
	if p.accepted.Load() != 2 {
		t.Fatal("idle retirement did not allow a fresh transport")
	}
}
