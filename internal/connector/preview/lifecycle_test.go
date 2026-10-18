package preview

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
)

type lifecycleForwarder func(context.Context, vmaccess.Target, int) (net.Conn, error)

func (f lifecycleForwarder) DialTCP(ctx context.Context, target vmaccess.Target, port int) (net.Conn, error) {
	return f(ctx, target, port)
}

func TestPreviewOwnerCancellationAttachBoundary(t *testing.T) {
	for _, order := range []string{"attached", "pending", "canceling"} {
		t.Run(order, func(t *testing.T) {
			accepted, finish := make(chan *websocket.Conn, 1), make(chan struct{})
			upgrader := websocket.Upgrader{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ws, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer ws.Close()
				accepted <- ws
				<-finish
			}))
			defer server.Close()
			defer close(finish)
			ws, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http", "ws", 1), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ws.Close()
			remote := <-accepted
			tcp, peer := net.Pipe()
			defer peer.Close()
			manager := NewSessionManager(lifecycleForwarder(func(context.Context, vmaccess.Target, int) (net.Conn, error) { return tcp, nil }), nil)
			defer manager.Shutdown("cleanup")
			attempt, err := manager.PrepareOpen(context.Background(), protocol.PreviewOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server", TargetPort: 3000}, protocol.BaseEnvelope{MessageID: "OPEN", PreviewSessionID: "S", LabInstanceID: "lab", Generation: 1})
			if err != nil {
				t.Fatal(err)
			}
			session, _, err := attempt.Open(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if order != "attached" {
				if order == "canceling" {
					// Fix the interval after pending cancellation wins, before Close.
					attempt.tunnel.cancel()
				} else {
					attempt.CancelPending()
				}
				if err := session.AttachDataConn(ws, "OPEN"); !errors.Is(err, ErrSessionClosed) {
					t.Fatalf("canceled pending tunnel attached: %v", err)
				}
				attempt.CancelPending()
				if manager.Count() != 0 {
					t.Fatal("canceled initial attempt retained logical session")
				}
				return
			}
			if err := session.AttachDataConn(ws, "OPEN"); err != nil {
				t.Fatal(err)
			}
			attempt.CancelPending() // Also covers an already-started owner callback.
			if attempt.tunnel.ctx.Err() != nil || !session.HasActiveTunnel() {
				t.Fatal("pending cancellation closed attached tunnel")
			}
			request := "GET / HTTP/1.1\r\nHost: workspace\r\n\r\n"
			if err := remote.WriteMessage(websocket.BinaryMessage, []byte(request)); err != nil {
				t.Fatal(err)
			}
			_ = peer.SetReadDeadline(time.Now().Add(time.Second))
			got := make([]byte, len(request))
			if _, err := io.ReadFull(peer, got); err != nil || string(got) != request {
				t.Fatalf("attached HTTP request interrupted: %q, %v", got, err)
			}
		})
	}
}

func TestPreviewLifecyclePendingOpenRetiredByClose(t *testing.T) {
	for _, all := range []bool{false, true} {
		t.Run(map[bool]string{false: "CloseSession", true: "CloseAll"}[all], func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			a, b := net.Pipe()
			defer b.Close()
			mgr := NewSessionManager(lifecycleForwarder(func(context.Context, vmaccess.Target, int) (net.Conn, error) {
				close(entered)
				<-release
				return a, nil
			}), nil)
			defer mgr.CloseAll("test cleanup")
			result := make(chan error, 1)
			go func() {
				_, _, err := mgr.GetOrCreateSession(context.Background(), protocol.PreviewOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server", TargetPort: 3000}, protocol.BaseEnvelope{MessageID: "M1", PreviewSessionID: "S", LabInstanceID: "lab", Generation: 1})
				result <- err
			}()
			<-entered
			if all {
				mgr.CloseAll(protocol.PreviewReasonCredentialRevoked)
			} else {
				_ = mgr.CloseSession("S", protocol.PreviewReasonSessionClosed)
			}
			close(release)
			err := <-result
			if err == nil || mgr.Count() != 0 {
				t.Errorf("closed pending attempt resurrected: error=%v count=%d", err, mgr.Count())
			}
		})
	}
}

func TestPreviewLifecycleStaleAttachCannotBindNewAttempt(t *testing.T) {
	seen, reply, finish := make(chan protocol.PreviewAttachMessage, 1), make(chan struct{}), make(chan struct{})
	upgrader := websocket.Upgrader{Subprotocols: []string{protocol.SubprotocolPreviewData}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		var attach protocol.PreviewAttachMessage
		if ws.ReadJSON(&attach) != nil {
			return
		}
		seen <- attach
		<-reply
		_ = ws.WriteJSON(protocol.PreviewAttachedMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypePreviewAttached, MessageID: "ack", ReplyToMessageID: attach.MessageID, PreviewSessionID: attach.PreviewSessionID, LabInstanceID: attach.LabInstanceID, Generation: attach.Generation}, Payload: protocol.PreviewAttachedPayload{}})
		<-finish
	}))
	defer server.Close()
	defer close(finish)
	a, b := net.Pipe()
	defer b.Close()
	session := NewPreviewSession("S", "lab", 1, "workspace", "server", 3000, nil, nil)
	defer session.Close("cleanup")
	_, _ = session.BindTunnel("M1", a)
	client := NewDataWSSClient(DataWSSClientConfig{EndpointURL: strings.Replace(server.URL, "http", "ws", 1), AllowInsecure: true, RuntimeID: "runtime"})
	done := make(chan error, 1)
	go func() { done <- client.DialAndAttach(context.Background(), session, "M1") }()
	attach := <-seen
	if attach.ReplyToMessageID != "M1" {
		t.Fatal("fixture correlation changed")
	}
	c, d := net.Pipe()
	defer d.Close()
	session.CloseAttempt("M1", protocol.PreviewReasonTargetClosed)
	if _, err := session.BindTunnel("M2", c); err != nil {
		t.Fatal(err)
	}
	close(reply)
	if err := <-done; err == nil {
		t.Errorf("M1 delayed ATTACHED accepted onto current M2 tunnel: current=%s attached=%v", session.CurrentAttemptID(), session.EverAttached())
	}
}

func TestPreviewLifecycleAttachReadCancellation(t *testing.T) {
	seen := make(chan *websocket.Conn, 1)
	finish := make(chan struct{})
	upgrader := websocket.Upgrader{Subprotocols: []string{protocol.SubprotocolPreviewData}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		var attach map[string]any
		if ws.ReadJSON(&attach) != nil {
			return
		}
		seen <- ws
		<-finish
	}))
	defer server.Close()
	defer close(finish)
	a, b := net.Pipe()
	defer b.Close()
	s := NewPreviewSession("S", "lab", 1, "workspace", "server", 3000, nil, nil)
	_, _ = s.BindTunnel("M1", a)
	defer s.Close("cleanup")
	client := NewDataWSSClient(DataWSSClientConfig{EndpointURL: strings.Replace(server.URL, "http", "ws", 1), AllowInsecure: true, RuntimeID: "runtime", AttachTimeout: 5 * time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.DialAndAttach(ctx, s, "M1") }()
	ws := <-seen
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("canceled attach remains blocked reading ATTACHED")
		_ = ws.Close()
		<-done
	}
}

type blockedWriteConn struct {
	net.Conn
	enabled         atomic.Bool
	entered, closed chan struct{}
	once, stop      sync.Once
}

func (c *blockedWriteConn) Write(b []byte) (int, error) {
	if c.enabled.Load() {
		c.once.Do(func() { close(c.entered) })
		<-c.closed
		return 0, net.ErrClosed
	}
	return c.Conn.Write(b)
}
func (c *blockedWriteConn) Close() error {
	c.stop.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

func TestPreviewLifecycleCloseUnblocksBlockedWSSWriter(t *testing.T) {
	upgrader := websocket.Upgrader{Subprotocols: []string{protocol.SubprotocolPreviewData}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	var gate *blockedWriteConn
	dialer := websocket.Dialer{Subprotocols: []string{protocol.SubprotocolPreviewData}, NetDialContext: func(ctx context.Context, n, addr string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, n, addr)
		if err != nil {
			return nil, err
		}
		gate = &blockedWriteConn{Conn: c, entered: make(chan struct{}), closed: make(chan struct{})}
		return gate, nil
	}}
	ws, _, err := dialer.Dial(strings.Replace(server.URL, "http", "ws", 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	a, b := net.Pipe()
	defer b.Close()
	s := NewPreviewSession("S", "lab", 1, "workspace", "server", 3000, a, nil)
	gate.enabled.Store(true)
	if err := s.AttachDataConn(ws); err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = b.Write([]byte("fixture output")) }()
	<-gate.entered
	closed := make(chan struct{})
	go func() { s.Close("shutdown"); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Error("Close waits on data writeMu before closing underlying socket; blocked writer cannot be interrupted")
		_ = gate.Close()
		<-closed
	}
}

func TestPreviewLifecycleConcurrentDuplicateAndActiveAttemptIsolation(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	a, b := net.Pipe()
	defer b.Close()
	mgr := NewSessionManager(lifecycleForwarder(func(context.Context, vmaccess.Target, int) (net.Conn, error) {
		calls.Add(1)
		close(entered)
		<-release
		return a, nil
	}), nil)
	defer mgr.Shutdown("cleanup")
	env := protocol.BaseEnvelope{MessageID: "M1", PreviewSessionID: "S", LabInstanceID: "lab", Generation: 1}
	payload := protocol.PreviewOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server", TargetPort: 3000}
	type result struct {
		s     *PreviewSession
		retry bool
		err   error
	}
	results := make(chan result, 9)
	go func() {
		s, r, e := mgr.GetOrCreateSession(context.Background(), payload, env)
		results <- result{s, r, e}
	}()
	<-entered
	for range 8 {
		go func() {
			s, r, e := mgr.GetOrCreateSession(context.Background(), payload, env)
			results <- result{s, r, e}
		}()
	}
	other := env
	other.MessageID = "M2"
	if _, _, err := mgr.GetOrCreateSession(context.Background(), payload, other); err != ErrSessionConflict {
		t.Fatal("second active attempt was not rejected")
	}
	close(release)
	var session *PreviewSession
	originals := 0
	for range 9 {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		if session == nil {
			session = r.s
		}
		if r.s != session {
			t.Fatal("duplicate session split")
		}
		if !r.retry {
			originals++
		}
	}
	if calls.Load() != 1 || originals != 1 {
		t.Fatalf("duplicate TCP opens: calls=%d originals=%d", calls.Load(), originals)
	}
	session.CloseAttempt("M1", "finished")
	c, d := net.Pipe()
	defer d.Close()
	if _, err := session.BindTunnel("M2", c); err != nil {
		t.Fatal(err)
	}
	mgr.CloseAttempt(session, "M1", "stale failure")
	if !session.HasActiveTunnel() || session.CurrentAttemptID() != "M2" {
		t.Fatal("late failure closed newer tunnel")
	}
}

func TestPreviewLifecycleGenerationAndShutdownFencePending(t *testing.T) {
	var calls atomic.Int32
	var peersMu sync.Mutex
	var peers []net.Conn
	mgr := NewSessionManager(lifecycleForwarder(func(context.Context, vmaccess.Target, int) (net.Conn, error) {
		calls.Add(1)
		a, b := net.Pipe()
		peersMu.Lock()
		peers = append(peers, b)
		peersMu.Unlock()
		return a, nil
	}), nil)
	defer func() {
		mgr.Shutdown("cleanup")
		peersMu.Lock()
		defer peersMu.Unlock()
		for _, p := range peers {
			_ = p.Close()
		}
	}()
	payload := protocol.PreviewOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server", TargetPort: 3000}
	first := protocol.BaseEnvelope{MessageID: "A", PreviewSessionID: "S1", LabInstanceID: "lab", Generation: 1}
	sibling := first
	sibling.PreviewSessionID = "S2"
	sibling.LabInstanceID = "other"
	s, _, e := mgr.GetOrCreateSession(context.Background(), payload, first)
	if e != nil {
		t.Fatal(e)
	}
	other, _, e := mgr.GetOrCreateSession(context.Background(), payload, sibling)
	if e != nil {
		t.Fatal(e)
	}
	mgr.InvalidateBefore("lab", 2)
	if !s.IsDestroyed() || other.IsDestroyed() || !other.HasActiveTunnel() {
		t.Fatal("generation fence crossed Lab boundary")
	}
	if _, _, e = mgr.GetOrCreateSession(context.Background(), payload, first); e == nil {
		t.Fatal("stale generation reopened")
	}
	if calls.Load() != 2 {
		t.Fatal("stale generation reached forwarder")
	}
	fresh := first
	fresh.Generation = 2
	fresh.PreviewSessionID = "S3"
	if _, _, e = mgr.GetOrCreateSession(context.Background(), payload, fresh); e != nil {
		t.Fatal(e)
	}
	mgr.Shutdown("shutdown")
	fresh.PreviewSessionID = "S4"
	if _, _, e = mgr.GetOrCreateSession(context.Background(), payload, fresh); e == nil {
		t.Fatal("open admitted after shutdown")
	}
	if mgr.Count() != 0 || calls.Load() != 3 {
		t.Fatal("shutdown registry or dial count incorrect")
	}
}
