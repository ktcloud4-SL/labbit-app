package vmaccess

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestLastCallerDeadlineCauseReachesFinalDialDiagnostic(t *testing.T) {
	for _, cause := range []error{errSetupTimeout, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			p := newSSHPeer(t)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			endpoint := p.endpoint
			endpoint.Address = listener.Addr().String()
			m := NewManager(resolverFunc(func(context.Context, Target) (Endpoint, error) { return endpoint, nil }), Config{SetupTimeout: time.Second})
			defer m.Close()
			observed := make(chan FailureInfo, 4)
			m.SetFailureObserver(func(_ Target, info FailureInfo) { observed <- info })
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			done := make(chan error, 1)
			go func() { _, err := m.OpenSession(ctx, testTarget()); done <- err }()
			peer, err := listener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			_ = peer.SetReadDeadline(time.Now().Add(time.Second))
			if _, err := bufio.NewReader(peer).ReadString('\n'); err != nil {
				t.Fatal(err)
			}
			// CancelCause has Err()==Canceled even though its cause is a deadline.
			cancel(cause)
			select {
			case err := <-done:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("deadline reclassified: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("caller did not return")
			}
			info := awaitHandshakeFailure(t, observed)
			want := "request_deadline"
			if cause == errSetupTimeout {
				want = "setup_deadline"
			}
			if !info.Timeout || info.ContextEnd != want {
				t.Fatalf("final diagnostic lost deadline cause: %+v", info)
			}
		})
	}
}

type joinedDialContext struct {
	context.Context
	once   sync.Once
	joined chan struct{}
}

func TestLastCancelReturnsWhileHostKeyCheckBlocked(t *testing.T) {
	p := newSSHPeer(t)
	endpoint := p.endpoint
	original := endpoint.HostKeyCallback
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	endpoint.HostKeyCallback = func(host string, remote net.Addr, key ssh.PublicKey) error {
		close(entered)
		<-release
		return original(host, remote, key)
	}
	m := NewManager(resolverFunc(func(context.Context, Target) (Endpoint, error) { return endpoint, nil }), Config{SetupTimeout: 100 * time.Millisecond})
	defer m.Close()
	observed := make(chan FailureInfo, 4)
	m.SetFailureObserver(func(_ Target, info FailureInfo) { observed <- info })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := m.OpenSession(ctx, testTarget()); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("host verification not reached")
	}
	cancel()
	select {
	case err := <-done:
		info, ok := Diagnostic(err)
		if !errors.Is(err, context.Canceled) || !ok || info.SSHPhase != SSHHostVerification {
			t.Fatalf("cancel checkpoint=%+v error=%v", info, err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("canceled caller waited for blocked callback")
	}
	m.mu.Lock()
	remaining := len(m.entries)
	m.mu.Unlock()
	if remaining != 0 {
		t.Fatal("canceled transport remained cached")
	}
	unblock()
	// The eventual SSH failure must still be observed after caller return.
	info := awaitHandshakeFailure(t, observed)
	if info.ContextEnd != "request_canceled" {
		t.Fatalf("worker final cause=%+v", info)
	}
}

func (c *joinedDialContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.joined) })
	return c.Context.Done()
}

func TestCancelFirstDialCallerKeepsSurvivingWaiter(t *testing.T) {
	p := newSSHPeer(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	endpoint := p.endpoint
	endpoint.Address = listener.Addr().String()
	m := NewManager(resolverFunc(func(context.Context, Target) (Endpoint, error) { return endpoint, nil }), Config{SetupTimeout: time.Second})
	defer m.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	go func() {
		c, e := m.acquire(ctx, testTarget())
		if c != nil {
			m.release(c)
		}
		first <- e
	}()
	downstream, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer downstream.Close()
	upstream, err := net.Dial("tcp", p.endpoint.Address)
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() { _, _ = io.Copy(upstream, downstream) }()
	// Hold the server's first response, so both callers join one pending dial.
	secondCtx := &joinedDialContext{Context: context.Background(), joined: make(chan struct{})}
	second := make(chan *connection, 1)
	secondErr := make(chan error, 1)
	go func() { c, e := m.acquire(secondCtx, testTarget()); second <- c; secondErr <- e }()
	select {
	case <-secondCtx.joined:
	case <-time.After(time.Second):
		t.Fatal("second caller did not join")
	}
	cancel()
	select {
	case e := <-first:
		if !errors.Is(e, context.Canceled) {
			t.Fatalf("first caller: %v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled first caller did not return")
	}
	go func() { _, _ = io.Copy(downstream, upstream) }()
	select {
	case e := <-secondErr:
		c := <-second
		if e != nil || c == nil {
			t.Fatalf("surviving caller lost shared dial: %v", e)
		}
		defer m.release(c)
		session, e := c.client.NewSession()
		if e != nil {
			t.Fatal(e)
		}
		_ = session.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("surviving caller did not connect")
	}
	if p.accepted.Load() != 1 {
		t.Fatal("shared dial created duplicate transports")
	}
}
