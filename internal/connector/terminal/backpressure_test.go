package terminal

import (
	"context"
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
)

// These wrappers observe actual loopback TCP writes; they do not delay, gate,
// or replace production writes. The peer deliberately stops consuming frames.
type observedDataTCP struct {
	net.Conn
	inWrite   atomic.Int64
	enteredAt atomic.Int64
}

func (c *observedDataTCP) Write(p []byte) (int, error) {
	c.enteredAt.Store(time.Now().UnixNano())
	c.inWrite.Add(1)
	defer c.inWrite.Add(-1)
	return c.Conn.Write(p)
}

type streamingDataPTY struct {
	start     chan struct{}
	closed    chan struct{}
	once      sync.Once
	reads     atomic.Int64
	wasClosed atomic.Bool
}

func (p *streamingDataPTY) Read(b []byte) (int, error) {
	select {
	case <-p.start:
	case <-p.closed:
		return 0, io.EOF
	}
	select {
	case <-p.closed:
		return 0, io.EOF
	default:
	}
	clear(b)
	p.reads.Add(1)
	return len(b), nil
}
func (*streamingDataPTY) Write(b []byte) (int, error) { return len(b), nil }
func (*streamingDataPTY) Resize(int, int) error       { return nil }
func (p *streamingDataPTY) Close() error {
	p.once.Do(func() { p.wasClosed.Store(true); close(p.closed) })
	return nil
}

func TestSessionShutdownWithUnreadDataPeer(t *testing.T) {
	peerCh := make(chan *websocket.Conn, 1)
	peerStop := make(chan struct{})
	var stopOnce sync.Once
	stopPeer := func() { stopOnce.Do(func() { close(peerStop) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer peer.Close()
		if tcp, ok := peer.UnderlyingConn().(*net.TCPConn); ok {
			_ = tcp.SetReadBuffer(1024)
		}
		peerCh <- peer
		<-peerStop // No ReadMessage after the genuine websocket handshake.
	}))
	defer server.Close()
	defer stopPeer()
	var observed *observedDataTCP
	dialer := websocket.Dialer{NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.SetWriteBuffer(1024)
		}
		observed = &observedDataTCP{Conn: conn}
		return observed, nil
	}}
	client, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	peer := <-peerCh
	defer peer.Close()
	pty := &streamingDataPTY{start: make(chan struct{}), closed: make(chan struct{})}
	defer pty.Close()
	var ended atomic.Int64
	manager := NewSessionManager(time.Minute, func(*Session, string, *int, error) { ended.Add(1) })
	session, _, err := manager.GetOrCreateSession(
		protocol.TerminalOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server-1", Cols: 80, Rows: 24},
		protocol.BaseEnvelope{TerminalSessionID: "unread-session", LabInstanceID: "lab-1", Generation: 1},
		func() (PTYChannel, error) { return pty, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AttachDataConn(client); err != nil {
		t.Fatal(err)
	}
	close(pty.start)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if observed.inWrite.Load() > 0 && time.Since(time.Unix(0, observed.enteredAt.Load())) > 200*time.Millisecond {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture did not reach genuine TCP write backpressure")
		}
		time.Sleep(10 * time.Millisecond)
	}
	reads := pty.reads.Load()
	shutdownDone := make(chan struct{})
	go func() { manager.Shutdown(protocol.TerminalReasonServiceRestart); close(shutdownDone) }()
	blocked := false
	select {
	case <-shutdownDone:
	case <-time.After(750 * time.Millisecond):
		blocked = true
		t.Logf("unread TCP peer: shutdown blocked; inFlightWrite=%d ptyReads=%d stableReads=%t PTYClosed=%t endedCallbacks=%d logicalStatus=%s activeRegistry=%d",
			observed.inWrite.Load(), reads, pty.reads.Load() == reads, pty.wasClosed.Load(), ended.Load(), session.GetStatus(), manager.ActiveCount())
	}
	// Force only this test-owned peer closed so a failing regression leaks no
	// sockets or goroutines. Passing shutdown must not depend on this cleanup.
	_ = peer.Close()
	stopPeer()
	select {
	case <-shutdownDone:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not recover after the test-owned peer was forcibly closed")
	}
	if !pty.wasClosed.Load() || ended.Load() != 1 {
		t.Fatalf("cleanup after peer close: PTYClosed=%t callbacks=%d", pty.wasClosed.Load(), ended.Load())
	}
	if blocked {
		t.Error("SERVICE_RESTARTING shutdown did not bound unread Data WSS; PTY cleanup and Control ENDED were delayed until external peer close")
	}
}
