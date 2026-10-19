package preview

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
)

type handoffConn struct {
	net.Conn
	entered, release chan struct{}
	once             sync.Once
	releaseOnce      sync.Once
	closed           atomic.Bool
}

func (c *handoffConn) releaseRetirement() { c.releaseOnce.Do(func() { close(c.release) }) }

func (c *handoffConn) Close() error {
	c.once.Do(func() {
		close(c.entered)
		<-c.release
		_ = c.Conn.Close()
		c.closed.Store(true)
	})
	return nil
}

func waitHandoff(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("tunnel handoff did not finish")
	}
}

// Keep both real WebSocket endpoints open while tests control TCP retirement.
func handoffSocket(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
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
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(finish) })
	ws, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http", "ws", 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	return ws, <-accepted
}

func handoffFixture(t *testing.T) (*SessionManager, *OpenAttempt, *handoffConn, *atomic.Int32, *websocket.Conn) {
	t.Helper()
	ws, remote := handoffSocket(t)
	tcp, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	old := &handoffConn{Conn: tcp, entered: make(chan struct{}), release: make(chan struct{})}
	calls := &atomic.Int32{}
	mgr := NewSessionManager(lifecycleForwarder(func(context.Context, vmaccess.Target, int) (net.Conn, error) {
		calls.Add(1)
		if calls.Load() == 1 {
			return old, nil
		}
		if !old.closed.Load() {
			t.Error("new TCP dial preceded old TCP close")
		}
		a, b := net.Pipe()
		t.Cleanup(func() { _ = b.Close() })
		return a, nil
	}), nil)
	t.Cleanup(func() { mgr.Shutdown("cleanup") })
	t.Cleanup(old.releaseRetirement)
	a, err := mgr.PrepareOpen(context.Background(),
		protocol.PreviewOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server", TargetPort: 3000},
		protocol.BaseEnvelope{MessageID: "M1", PreviewSessionID: "S", LabInstanceID: "lab", Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.session.AttachDataConn(ws, "M1"); err != nil {
		t.Fatal(err)
	}
	return mgr, a, old, calls, remote
}

func nextHandoff(t *testing.T, mgr *SessionManager, id string) *OpenAttempt {
	t.Helper()
	a, err := mgr.PrepareOpen(context.Background(),
		protocol.PreviewOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server", TargetPort: 3000},
		protocol.BaseEnvelope{MessageID: id, PreviewSessionID: "S", LabInstanceID: "lab", Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestPreviewHandoffRetiresBeforeDial(t *testing.T) {
	mgr, _, old, calls, _ := handoffFixture(t)
	a := nextHandoff(t, mgr, "M2")
	duplicate := nextHandoff(t, mgr, "M2")
	if _, err := mgr.PrepareOpen(context.Background(), a.payload,
		protocol.BaseEnvelope{MessageID: "M3", PreviewSessionID: "S", LabInstanceID: "lab", Generation: 1}); !errors.Is(err, ErrSessionConflict) {
		t.Fatalf("overlapping pending OPEN accepted: %v", err)
	}
	result, duplicateResult := make(chan error, 1), make(chan error, 1)
	go func() { _, _, err := a.Open(context.Background()); result <- err }()
	go func() { _, _, err := duplicate.Open(context.Background()); duplicateResult <- err }()
	waitHandoff(t, old.entered)
	if calls.Load() != 1 {
		t.Fatalf("dialed during predecessor retirement: %d", calls.Load())
	}
	old.releaseRetirement()
	for _, ch := range []<-chan error{result, duplicateResult} {
		select {
		case err := <-ch:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("OPEN remained blocked")
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("duplicate dial count=%d", calls.Load())
	}
}

func TestPreviewHandoffCancellationBeforeWorker(t *testing.T) {
	for _, mode := range []string{"owner", "caller", "explicit"} {
		t.Run(mode, func(t *testing.T) {
			mgr, _, old, calls, _ := handoffFixture(t)
			a := nextHandoff(t, mgr, "M2")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			if mode == "caller" {
				cancel()
				close(done)
			} else {
				go func() {
					defer close(done)
					if mode == "owner" {
						a.CancelPending()
					} else {
						_ = mgr.CloseMatchingSession("S", "lab", 1, protocol.PreviewReasonLabReset)
					}
				}()
				waitHandoff(t, old.entered)
			}
			old.releaseRetirement()
			waitHandoff(t, done)
			if _, _, err := a.Open(ctx); err == nil {
				t.Fatal("retired attempt reopened")
			}
			if calls.Load() != 1 || !old.closed.Load() {
				t.Fatalf("cancellation leaked predecessor or dialed: calls=%d closed=%v", calls.Load(), old.closed.Load())
			}
		})
	}
}

func TestPreviewHandoffTrustCloseRetiresPending(t *testing.T) {
	for _, code := range []int{4001, 4002} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			mgr, first, old, calls, remote := handoffFixture(t)
			a := nextHandoff(t, mgr, "M2")
			if err := remote.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, protocol.PreviewReasonCredentialRevoked), time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			waitHandoff(t, old.entered)
			old.releaseRetirement()
			deadline := time.After(2 * time.Second)
			for !first.session.IsDestroyed() || mgr.Count() != 0 {
				select {
				case <-deadline:
					t.Fatal("trust close did not retire logical session")
				default:
					time.Sleep(time.Millisecond)
				}
			}
			if _, _, err := a.Open(context.Background()); err == nil {
				t.Fatal("trust-closed pending attempt reopened")
			}
			if calls.Load() != 1 {
				t.Fatalf("trust close allowed new dial: %d", calls.Load())
			}
		})
	}
}

func TestPreviewBindTunnelRetiresAttachedPredecessor(t *testing.T) {
	_, first, old, _, _ := handoffFixture(t)
	tcp, peer := net.Pipe()
	defer peer.Close()
	result := make(chan error, 1)
	go func() { _, err := first.session.BindTunnel("M2", tcp); result <- err }()
	waitHandoff(t, old.entered)
	old.releaseRetirement()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("direct bind remained blocked")
	}
	if !old.closed.Load() {
		t.Fatal("new TCP bound before predecessor closed")
	}
}
