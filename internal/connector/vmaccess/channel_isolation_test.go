package vmaccess

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestCanceledChannelLeavesActivePTYUsable(t *testing.T) {
	for _, phase := range []string{"open", "pty", "sftp"} {
		for _, mode := range []string{"timeout", "cancel"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				p := newSSHPeer(t)
				m := testManager(t, p, Config{SetupTimeout: time.Second, IdleTimeout: 40 * time.Millisecond})
				a := openSession(t, m, testTarget())
				in, out := echoChannel(t, a, false)
				assertEcho(t, in, out)
				ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
				defer cancel()
				done := make(chan error, 1)
				if phase == "open" {
					p.stallOpen.Store(true)
					go func() {
						b, err := m.OpenSession(ctx, testTarget())
						if b != nil {
							_ = b.Close()
						}
						done <- err
					}()
					select {
					case <-p.heldOpen:
					case <-time.After(time.Second):
						t.Fatal("B channel open did not reach SSH peer")
					}
				} else {
					b := openSession(t, m, testTarget())
					p.stallRequest.Store(true)
					go func() {
						done <- b.Setup(ctx, func(raw *ssh.Session) error {
							if phase == "pty" {
								return raw.RequestPty("xterm", 24, 80, nil)
							}
							return raw.RequestSubsystem("sftp")
						})
					}()
					select {
					case <-p.heldRequest:
					case <-time.After(time.Second):
						t.Fatal("B request did not reach SSH peer")
					}
				}
				if mode == "cancel" {
					cancel()
				}
				want := context.DeadlineExceeded
				if mode == "cancel" {
					want = context.Canceled
				}
				select {
				case err := <-done:
					if !errors.Is(err, want) || !errors.Is(err, ErrUnavailable) {
						t.Fatalf("B cancellation=%v", err)
					}
				case <-time.After(time.Second):
					t.Fatal("B cancellation did not return")
				}
				assertEcho(t, in, out)
				if p.accepted.Load() != 1 || p.active.Load() != 1 {
					t.Fatal("B cancellation replaced or closed A transport")
				}
				_ = a.Close()
				eventually(t, func() bool { return p.active.Load() == 0 })
			})
		}
	}
}

func TestCanceledOpenClosesLateChannelAndReusesTransport(t *testing.T) {
	p := newSSHPeer(t)
	m := testManager(t, p, Config{})
	a := openSession(t, m, testTarget())
	in, out := echoChannel(t, a, false)
	p.stallOpen.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := m.OpenSession(ctx, testTarget()); done <- err }()
	var pending ssh.NewChannel
	select {
	case pending = <-p.heldOpen:
	case <-time.After(time.Second):
		t.Fatal("open did not reach peer")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel blocked")
	}
	late, requests, err := pending.Accept()
	if err != nil {
		t.Fatalf("canceled B destroyed shared transport: %v", err)
	}
	defer late.Close()
	go ssh.DiscardRequests(requests)
	closed := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, late); closed <- err }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("late B channel leaked")
	}
	p.stallOpen.Store(false)
	b := openSession(t, m, testTarget())
	bIn, bOut := echoChannel(t, b, true)
	assertEcho(t, in, out)
	assertEcho(t, bIn, bOut)
	if p.accepted.Load() != 1 {
		t.Fatal("late response required reconnect")
	}
}

func TestStalledOpenDoesNotAccumulateCanceledSSHRequests(t *testing.T) {
	p := newSSHPeer(t)
	m := testManager(t, p, Config{})
	a := openSession(t, m, testTarget())
	in, out := echoChannel(t, a, false)
	p.stallOpen.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := m.OpenSession(ctx, testTarget()); done <- err }()
	select {
	case <-p.heldOpen:
	case <-time.After(time.Second):
		t.Fatal("open did not reach peer")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancel blocked")
	}
	var wg sync.WaitGroup
	for range 24 {
		wg.Go(func() {
			waitCtx, waitCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer waitCancel()
			if s, err := m.OpenSession(waitCtx, testTarget()); !errors.Is(err, context.DeadlineExceeded) {
				if s != nil {
					_ = s.Close()
				}
				t.Errorf("waiter=%v", err)
			}
		})
	}
	wg.Wait()
	if p.openCount.Load() != 2 {
		t.Fatalf("canceled waiters sent additional contextless opens: %d", p.openCount.Load())
	}
	assertEcho(t, in, out)
	_ = m.Close()
	eventually(t, func() bool { return p.active.Load() == 0 })
}
