package terminal

import (
	"context"
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

// Only this boundary case injects extra bytes into the final-frame write.
// It creates actual TCP backpressure with no concurrent Session writer and
// preserves the real TCP deadline implementation; no fake timeout/timer is used.
// These deliberately unread bytes are never parsed as a valid peer message.
type finalFrameTCP struct {
	net.Conn
	enabled atomic.Bool
	entered chan struct{}
	once    sync.Once
}

func (c *finalFrameTCP) Write(p []byte) (int, error) {
	if c.enabled.Load() {
		c.once.Do(func() { close(c.entered) })
		if _, err := c.Conn.Write(make([]byte, 16<<20)); err != nil {
			return 0, err
		}
	}
	return c.Conn.Write(p)
}

func TestSessionBoundsFinalDataEndedWrite(t *testing.T) {
	peerCh := make(chan *websocket.Conn, 1)
	stop := make(chan struct{})
	var stopOnce sync.Once
	stopPeer := func() { stopOnce.Do(func() { close(stop) }) }
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
		<-stop
	}))
	defer server.Close()
	defer stopPeer()
	var injected *finalFrameTCP
	dialer := websocket.Dialer{NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.SetWriteBuffer(1024)
		}
		injected = &finalFrameTCP{Conn: conn, entered: make(chan struct{})}
		return injected, nil
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
	var callbacks atomic.Int64
	manager := NewSessionManager(time.Minute, func(*Session, string, *int, error) { callbacks.Add(1) })
	session, _, err := manager.GetOrCreateSession(protocol.TerminalOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server-1"}, protocol.BaseEnvelope{TerminalSessionID: "final-frame-session", LabInstanceID: "lab-1", Generation: 1}, func() (PTYChannel, error) { return pty, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AttachDataConn(client); err != nil {
		t.Fatal(err)
	}
	// The PTY is still gated, so Close must acquire the free writer lock.
	injected.enabled.Store(true)
	done := make(chan struct{})
	started := time.Now()
	go func() { manager.Shutdown(protocol.TerminalReasonServiceRestart); close(done) }()
	select {
	case <-injected.entered:
	case <-time.After(time.Second):
		t.Fatal("final frame did not enter injected TCP write")
	}
	blocked := false
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		blocked = true
	}
	t.Logf("final-frame boundary: injected=true elapsedMilliseconds=%d shutdownBlocked=%t PTYClosed=%t callbacks=%d", time.Since(started).Milliseconds(), blocked, pty.wasClosed.Load(), callbacks.Load())
	_ = peer.Close()
	stopPeer()
	if blocked {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("final frame did not release after owned peer close")
		}
	}
	if !pty.wasClosed.Load() || callbacks.Load() != 1 {
		t.Error("PTY/Control cleanup not completed once")
	}
	if blocked {
		t.Error("final-frame WriteJSON lacked a transport deadline and blocked cleanup")
	}
}

func TestSessionFinalDataEndedWaitsForBriefWriter(t *testing.T) {
	peerCh := make(chan *websocket.Conn, 1)
	stop := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		peerCh <- peer
		<-stop
		_ = peer.Close()
	}))
	defer server.Close()
	defer close(stop)

	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	peer := <-peerCh

	session := &Session{
		SessionID:     "brief-writer-session",
		LabInstanceID: "lab-1",
		Generation:    1,
		Status:        StatusActive,
		DataConn:      client,
		detachChan:    make(chan struct{}),
	}
	session.writeMu.Lock()
	go func() {
		time.Sleep(20 * time.Millisecond)
		session.writeMu.Unlock()
	}()

	session.Close(protocol.TerminalReasonServiceRestart, nil, nil)
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	var ended protocol.TerminalDataEndedMessage
	if err := peer.ReadJSON(&ended); err != nil {
		t.Fatalf("brief healthy writer contention dropped Data ENDED: %v", err)
	}
	if ended.Type != protocol.MessageTypeTerminalDataEnded || ended.Payload.Reason != protocol.TerminalReasonServiceRestart {
		t.Fatalf("unexpected Data ENDED: %+v", ended)
	}
}
