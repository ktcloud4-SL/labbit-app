package vmaccess

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestHandshakeDiagnosticsSeparateVersionAndKeyExchange(t *testing.T) {
	for _, banner := range []bool{false, true} {
		t.Run(map[bool]string{false: "version", true: "key_exchange"}[banner], func(t *testing.T) {
			p := newSSHPeer(t)
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			endpoint := p.endpoint
			endpoint.Address = l.Addr().String()
			m := NewManager(resolverFunc(func(context.Context, Target) (Endpoint, error) { return endpoint, nil }), Config{SetupTimeout: 100 * time.Millisecond})
			defer m.Close()
			observed := make(chan FailureInfo, 4)
			m.SetFailureObserver(func(_ Target, info FailureInfo) { observed <- info })
			done := make(chan error, 1)
			go func() { _, err := m.OpenSession(context.Background(), testTarget()); done <- err }()
			c, err := l.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if banner {
				_, err = io.WriteString(c, "notice\r\nSS")
				if err == nil {
					_, err = io.WriteString(c, "H-2.0-fixture\r\n")
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				info, ok := Diagnostic(err)
				want := SSHVersionExchange
				if banner {
					want = SSHKeyExchange
				}
				if !ok || info.SSHPhase != want || !info.Timeout || info.SharedWaiter || info.CallerElapsedMS <= 0 {
					t.Fatalf("diagnostic=%+v, ok=%v", info, ok)
				}
				if info.ContextEnd != "setup_deadline" && info.ContextEnd != "deadline_timer_pending" {
					t.Fatalf("setup cause=%+v", info)
				}
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal(err)
				}
				final := awaitHandshakeFailure(t, observed)
				if final.SSHPhase != want || !final.Timeout {
					t.Fatalf("worker final diagnostic=%+v", final)
				}
			case <-time.After(time.Second):
				t.Fatal("handshake did not stop")
			}
		})
	}
}

func TestHandshakeDiagnosticsKeepRequestCancelSeparate(t *testing.T) {
	p := newSSHPeer(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	endpoint := p.endpoint
	endpoint.Address = l.Addr().String()
	m := NewManager(resolverFunc(func(context.Context, Target) (Endpoint, error) { return endpoint, nil }), Config{})
	defer m.Close()
	observed := make(chan FailureInfo, 4)
	var observer func(Target, FailureInfo)
	observer = func(target Target, info FailureInfo) {
		// Setter re-entry proves observers run outside the transport mutex.
		m.SetFailureObserver(observer)
		if target != testTarget() {
			t.Error("observer target differs")
		}
		observed <- info
	}
	m.SetFailureObserver(observer)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := m.OpenSession(ctx, testTarget()); done <- err }()
	c, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := bufio.NewReader(c).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		info, ok := Diagnostic(err)
		if !ok || info.ContextEnd != "request_canceled" || info.CallerContextEnd != "request_canceled" || info.Timeout || info.SSHPhase != SSHVersionExchange {
			t.Fatalf("diagnostic=%+v, ok=%v", info, ok)
		}
	case <-time.After(time.Second):
		t.Fatal("request cancellation did not stop dial")
	}
	info := awaitHandshakeFailure(t, observed)
	if info.SSHPhase != SSHVersionExchange || info.ContextEnd != "request_canceled" || !info.ContextClosedSocket {
		t.Fatalf("observer lost completed worker diagnostic: %+v", info)
	}
}

func awaitHandshakeFailure(t *testing.T, observed <-chan FailureInfo) FailureInfo {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case info := <-observed:
			if info.HandshakeReturnedError {
				return info
			}
		case <-timer.C:
			t.Fatal("completed handshake failure was not observed")
			return FailureInfo{}
		}
	}
}

func TestIdentificationObserverDoesNotConsumeOrRetainPeerText(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	c := &handshakeConn{Conn: left}
	c.phase.Store(uint32(SSHVersionExchange))
	input := "pre-auth fixture notice\r\nSSH-2.0-fixture\r\nopaque binary data"
	go func() { _, _ = io.WriteString(right, input); _ = right.Close() }()
	b, err := io.ReadAll(c)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != input || SSHPhase(c.phase.Load()) != SSHKeyExchange {
		t.Fatal("identification observer changed protocol bytes or failed to classify")
	}
	if strings.Contains(SSHPhase(255).String(), "fixture") {
		t.Fatal("external text in classification")
	}
}
