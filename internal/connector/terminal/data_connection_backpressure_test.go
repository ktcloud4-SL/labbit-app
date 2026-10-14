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

func unreadDataPair(t *testing.T) (*websocket.Conn, *websocket.Conn, *observedDataTCP) {
	t.Helper()
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
	t.Cleanup(server.Close)
	t.Cleanup(stopPeer)
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
	peer := <-peerCh
	t.Cleanup(func() { _ = peer.Close(); _ = client.Close() })
	return client, peer, observed
}

func TestSessionReleasesUnreadDataConnectionOnReplaceAndDetach(t *testing.T) {
	for _, operation := range []string{"replace", "detach"} {
		t.Run(operation, func(t *testing.T) {
			first, firstPeer, observed := unreadDataPair(t)
			pty := &streamingDataPTY{start: make(chan struct{}), closed: make(chan struct{})}
			t.Cleanup(func() { _ = pty.Close() })
			var callbacks atomic.Int64
			manager := NewSessionManager(time.Minute, func(*Session, string, *int, error) { callbacks.Add(1) })
			session, _, err := manager.GetOrCreateSession(protocol.TerminalOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server-1"}, protocol.BaseEnvelope{TerminalSessionID: "old-conn-session", LabInstanceID: "lab-1", Generation: 1}, func() (PTYChannel, error) { return pty, nil })
			if err != nil {
				t.Fatal(err)
			}
			if _, err := session.AttachDataConn(first); err != nil {
				t.Fatal(err)
			}
			close(pty.start)
			deadline := time.Now().Add(5 * time.Second)
			for observed.inWrite.Load() < 1 || time.Since(time.Unix(0, observed.enteredAt.Load())) < 200*time.Millisecond {
				if time.Now().After(deadline) {
					t.Fatal("old socket did not reach genuine TCP write backpressure")
				}
				time.Sleep(5 * time.Millisecond)
			}
			operationDone := make(chan struct{})
			var second *websocket.Conn
			if operation == "replace" {
				second, _, _ = unreadDataPair(t)
				go func() { _, _ = session.AttachDataConn(second); close(operationDone) }()
			} else {
				go func() { session.DetachDataConn(first); close(operationDone) }()
			}
			deadline = time.Now().Add(time.Second)
			for session.GetDataConn() != second {
				if time.Now().After(deadline) {
					t.Fatal("operation did not transfer old socket ownership")
				}
				time.Sleep(5 * time.Millisecond)
			}
			shutdownDone := make(chan struct{})
			go func() { manager.Shutdown(protocol.TerminalReasonServiceRestart); close(shutdownDone) }()
			operationBlocked, shutdownBlocked := false, false
			select {
			case <-operationDone:
			case <-time.After(750 * time.Millisecond):
				operationBlocked = true
			}
			select {
			case <-shutdownDone:
			case <-time.After(750 * time.Millisecond):
				shutdownBlocked = true
			}
			t.Logf("ownership transition=%s oldWriteInFlight=%d operationBlocked=%t shutdownBlocked=%t PTYClosed=%t callbacks=%d", operation, observed.inWrite.Load(), operationBlocked, shutdownBlocked, pty.wasClosed.Load(), callbacks.Load())
			// Always release only the fixture-owned peer, including expected red.
			_ = firstPeer.Close()
			select {
			case <-operationDone:
			case <-time.After(2 * time.Second):
				t.Fatal("old socket operation leaked after fixture peer close")
			}
			select {
			case <-shutdownDone:
			case <-time.After(2 * time.Second):
				t.Fatal("shutdown leaked after fixture peer close")
			}
			if operationBlocked || shutdownBlocked {
				t.Error("old Data socket was not closed independently of its blocked writer during replacement/detach")
			}
			if observed.inWrite.Load() != 0 || !pty.wasClosed.Load() || callbacks.Load() != 1 {
				t.Error("resource ownership not fully released")
			}
		})
	}
}

// Verify the recovered connection's consumer boundary before shutdown. The
// old unread socket must be released without ending its still-owned PTY.
func TestSessionRecoversFromUnreadDataConnectionWithoutEndingPTY(t *testing.T) {
	for _, operation := range []string{"replace", "detach-reattach"} {
		t.Run(operation, func(t *testing.T) {
			first, firstPeer, observed := unreadDataPair(t)
			second, secondPeer, _ := unreadDataPair(t)
			pty := &streamingDataPTY{start: make(chan struct{}), closed: make(chan struct{})}
			t.Cleanup(func() { _ = pty.Close() })
			var callbacks atomic.Int64
			manager := NewSessionManager(time.Minute, func(*Session, string, *int, error) { callbacks.Add(1) })
			session, _, err := manager.GetOrCreateSession(protocol.TerminalOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server-1"}, protocol.BaseEnvelope{TerminalSessionID: "recovery-session", LabInstanceID: "lab-1", Generation: 1}, func() (PTYChannel, error) { return pty, nil })
			if err != nil {
				t.Fatal(err)
			}
			if _, err := session.AttachDataConn(first); err != nil {
				t.Fatal(err)
			}
			close(pty.start)
			deadline := time.Now().Add(5 * time.Second)
			for observed.inWrite.Load() < 1 || time.Since(time.Unix(0, observed.enteredAt.Load())) < 200*time.Millisecond {
				if time.Now().After(deadline) {
					t.Fatal("old socket did not reach actual TCP backpressure")
				}
				time.Sleep(5 * time.Millisecond)
			}
			operationDone := make(chan struct{})
			if operation == "replace" {
				go func() { _, _ = session.AttachDataConn(second); close(operationDone) }()
			} else {
				go func() { session.DetachDataConn(first); close(operationDone) }()
			}
			blocked := false
			select {
			case <-operationDone:
			case <-time.After(750 * time.Millisecond):
				blocked = true
			}
			if blocked {
				_ = firstPeer.Close()
				select {
				case <-operationDone:
				case <-time.After(2 * time.Second):
					t.Fatal("operation did not release after fixture peer close")
				}
				_ = secondPeer.Close()
				manager.Shutdown(protocol.TerminalReasonServiceRestart)
				t.Fatal("recovery stalled behind the old connection writer")
			}
			deadline = time.Now().Add(time.Second)
			for observed.inWrite.Load() != 0 && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
			if observed.inWrite.Load() != 0 {
				t.Fatal("old writer was not released")
			}
			if pty.wasClosed.Load() || callbacks.Load() != 0 {
				t.Fatal("old socket closure ended the retained PTY")
			}
			resumed := false
			if operation == "detach-reattach" {
				if session.GetStatus() != StatusDetached || session.GetDataConn() != nil {
					t.Fatal("detached state incorrect")
				}
				// Remain inside grace while checking retained PTY ownership.
				time.Sleep(20 * time.Millisecond)
				if pty.wasClosed.Load() || callbacks.Load() != 0 {
					t.Fatal("PTY ended during detach grace")
				}
				resumed, err = session.AttachDataConn(second)
				if err != nil || !resumed {
					t.Fatal("same-session reattach did not resume")
				}
			}
			if session.GetStatus() != StatusActive || session.GetDataConn() != second {
				t.Fatal("new active connection not installed")
			}
			_ = secondPeer.SetReadDeadline(time.Now().Add(time.Second))
			messageType, output, err := secondPeer.ReadMessage()
			if err != nil || messageType != websocket.BinaryMessage || len(output) == 0 {
				t.Fatal("retained PTY output did not reach actual new WebSocket peer")
			}
			clear(output)
			t.Logf("recovery=%s actualNewPeerOutput=true oldWriterReleased=true PTYRetained=true resumed=%t", operation, resumed)
			manager.Shutdown(protocol.TerminalReasonServiceRestart)
			if !pty.wasClosed.Load() || callbacks.Load() != 1 {
				t.Fatal("explicit shutdown did not end retained PTY once")
			}
		})
	}
}
