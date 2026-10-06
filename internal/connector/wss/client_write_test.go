package wss

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

func TestClient_SendMessage_ContextCancellationWhileWaitingForWriter(t *testing.T) {
	client := NewClient(Config{})
	<-client.writeGate
	defer func() { client.writeGate <- struct{}{} }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := client.SendMessage(ctx, map[string]string{"type": "HEARTBEAT"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SendMessage() error = %v, want context.Canceled", err)
	}
}

func TestClient_SendMessage_CanceledPreviousSessionDoesNotCloseCurrentConnection(t *testing.T) {
	const token = "test-canceled-session-token"
	server := mock.NewMockSaaS(token)
	defer server.Close()

	client := NewClient(Config{
		BaseURL:       server.URL(),
		Credential:    token,
		AllowInsecure: true,
	})
	defer client.Close()

	connectCtx, connectCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer connectCancel()
	if err := client.Dial(connectCtx); err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	if _, err := client.SendHello(connectCtx); err != nil {
		t.Fatalf("SendHello() error = %v", err)
	}
	currentConnection := client.Conn()

	oldSessionCtx, cancelOldSession := context.WithCancel(context.Background())
	cancelOldSession()
	for i := 0; i < 100; i++ {
		err := client.SendMessage(oldSessionCtx, map[string]string{"type": "LATE_RESULT"})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("SendMessage() iteration %d error = %v, want context.Canceled", i, err)
		}
		if client.Conn() != currentConnection {
			t.Fatalf("canceled previous-session write closed the current connection at iteration %d", i)
		}
	}

	if err := client.SendMessage(connectCtx, protocol.HeartbeatMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:      protocol.MessageTypeHeartbeat,
			MessageID: "current-session-heartbeat",
			SentAt:    time.Now().UTC(),
		},
		Payload: protocol.HeartbeatPayload{ObservedAt: time.Now().UTC()},
	}); err != nil {
		t.Fatalf("current-session SendMessage() error after canceled old-session writes = %v", err)
	}
}

func TestClient_SendMessage_WriteTimeoutClosesBlackholedConnection(t *testing.T) {
	client, releaseServer := newBlackholedClient(t, 100*time.Millisecond)
	defer releaseServer()

	startedAt := time.Now()
	// Bypass application sizing to exercise stalled transport I/O itself.
	err := client.writeMessage(context.Background(), websocket.TextMessage, []byte(strings.Repeat("x", 16<<20)))
	if err == nil {
		t.Fatal("SendMessage() succeeded against a server that does not read")
	}
	if elapsed := time.Since(startedAt); elapsed > 2*time.Second {
		t.Fatalf("SendMessage() took %s, want bounded failure within 2s", elapsed)
	}
	if client.Conn() != nil {
		t.Fatal("Conn() is non-nil after write timeout")
	}
}

func TestClient_CloseUnblocksStalledWrite(t *testing.T) {
	client, releaseServer := newBlackholedClient(t, 30*time.Second)
	defer releaseServer()

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- client.writeMessage(context.Background(), websocket.TextMessage, []byte(strings.Repeat("x", 16<<20)))
	}()

	select {
	case err := <-writeDone:
		t.Fatalf("SendMessage() returned before Close(), error = %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	closedAt := time.Now()
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if elapsed := time.Since(closedAt); elapsed > time.Second {
		t.Fatalf("Close() took %s, want less than 1s", elapsed)
	}

	select {
	case err := <-writeDone:
		if err == nil {
			t.Fatal("stalled SendMessage() returned nil after Close()")
		}
	case <-time.After(time.Second):
		t.Fatal("stalled SendMessage() did not return within 1s after Close()")
	}
}

func newBlackholedClient(t *testing.T, writeTimeout time.Duration) (*Client, func()) {
	t.Helper()

	upgrader := websocket.Upgrader{
		Subprotocols: []string{protocol.SubprotocolControl},
		CheckOrigin:  func(*http.Request) bool { return true },
	}
	connected := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		close(connected)
		<-release
		_ = conn.Close()
	}))

	client := NewClient(Config{
		BaseURL:       server.URL,
		Credential:    "test-token",
		WriteTimeout:  writeTimeout,
		AllowInsecure: true,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Dial(ctx); err != nil {
		close(release)
		server.Close()
		t.Fatalf("Dial() error = %v", err)
	}
	select {
	case <-connected:
	case <-ctx.Done():
		_ = client.Close()
		close(release)
		server.Close()
		t.Fatal("server did not accept websocket connection")
	}

	return client, func() {
		_ = client.Close()
		close(release)
		server.Close()
	}
}
