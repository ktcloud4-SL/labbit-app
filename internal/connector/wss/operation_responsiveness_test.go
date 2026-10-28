package wss

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/terminal"
)

func TestListenOpensReadyTerminalWhileAnotherVMOperationIsBlocked(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	relay := mock.NewTerminalRelay()
	defer relay.Close()
	result := make(chan protocol.TerminalOpenResultMessage, 1)
	h := NewHandler(&provider.MockProvider{CleanupFunc: func(ctx context.Context, _ provider.CleanupRequest) (provider.OperationResult, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return provider.OperationResult{Outcome: provider.OutcomeSucceeded}, nil
	}}, SendMessageFunc(func(_ context.Context, msg interface{}) error {
		if m, ok := msg.(protocol.TerminalOpenResultMessage); ok {
			result <- m
		}
		return nil
	}))
	manager := terminal.NewSessionManager(time.Second, nil)
	defer manager.CloseAll(protocol.TerminalReasonServiceRestart)
	h.SetTerminalManager(manager, func(_, _ string, cols, rows int) (terminal.PTYChannel, error) {
		return terminal.NewMockEchoPTY(cols, rows), nil
	}, terminal.DataWSSClientConfig{EndpointURL: relay.URL(), AllowInsecure: true, DialTimeout: time.Second})
	// A distinct VM is already READY while the Provider is still working on another.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if conn.WriteJSON(queuedCleanup("student")) != nil {
			return
		}
		select {
		case <-entered:
		case <-ctx.Done():
			return
		}
		_ = conn.WriteJSON(protocol.TerminalOpenMessage{
			BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeTerminalOpen, MessageID: "ready-terminal-open", SentAt: time.Now().UTC(), TerminalSessionID: "ready-terminal", LabInstanceID: "instructor", Generation: 1},
			Payload:      protocol.TerminalOpenPayload{TargetVmKey: "vm-1", ProviderServerID: "ready-server", Cols: 80, Rows: 24},
		})
		<-ctx.Done()
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	done := make(chan error, 1)
	go func() { done <- h.listenWithOperationContext(ctx, ctx, conn) }()
	select {
	case m := <-result:
		if m.Payload.Outcome != protocol.OutcomeSucceeded {
			t.Fatalf("terminal result: %+v", m.Payload)
		}
		if _, ok := manager.GetSession("ready-terminal"); !ok {
			t.Fatal("terminal was not attached")
		}
	case <-time.After(time.Second):
		t.Error("ready terminal was blocked by another VM's Provider work")
	}
	unblock()
	cancel()
	_ = conn.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("listener did not stop")
	}
}

func queuedCleanup(lab string) protocol.OperationCommandMessage {
	return protocol.OperationCommandMessage{
		BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeOperationCommand, MessageID: generateUUID(), SentAt: time.Now().UTC(), OperationID: generateUUID(), LabInstanceID: lab, Generation: 1},
		Payload:      protocol.OperationCommandPayload{MutationType: protocol.MutationTypeCleanup, ProviderResources: []protocol.ProviderResourceRef{}},
	}
}

func TestRetiredReaderWaitingForOperationSlotStopsWithoutCancelingAcceptedWork(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	entered, release, probe := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
	var calls atomic.Int32
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	sender := SendMessageFunc(func(_ context.Context, msg interface{}) error {
		if m, ok := msg.(protocol.ProtocolErrorMessage); ok && m.ReplyToMessageID == "reader-probe" {
			probe <- struct{}{}
		}
		return nil
	})
	h := NewHandler(&provider.MockProvider{CleanupFunc: func(ctx context.Context, _ provider.CleanupRequest) (provider.OperationResult, error) {
		if calls.Add(1) != 1 {
			t.Error("retired reader started another Provider call")
		}
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return provider.OperationResult{Outcome: provider.OutcomeSucceeded}, nil
	}}, sender)
	first, _ := controlFrames(t, ctx, []any{queuedCleanup("accepted")})
	firstDone := make(chan error, 1)
	go func() { firstDone <- h.listenWithOperationContext(ctx, ctx, first) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("first operation not accepted")
	}
	h.SetSender(sender)
	readCtx, retire := context.WithCancel(ctx)
	second, _ := controlFrames(t, ctx, []any{queuedCleanup("queued"), map[string]any{"type": "UNKNOWN", "messageId": "reader-probe"}})
	secondDone := make(chan error, 1)
	go func() { secondDone <- h.listenWithOperationContext(readCtx, ctx, second) }()
	select {
	case <-probe:
	case <-ctx.Done():
		t.Fatal("reader blocked behind Provider slot")
	}
	retire()
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("retired worker remained blocked on accepted work")
	}
	if calls.Load() != 1 {
		t.Fatal("queued command was accepted")
	}
	unblock()
	cancel()
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("accepted worker did not finish")
	}
}

func TestControlEOFDiscardsQueuedWorkAndPreservesAcceptedContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	entered, release := make(chan context.Context, 1), make(chan struct{})
	var calls atomic.Int32
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	h := NewHandler(&provider.MockProvider{CleanupFunc: func(ctx context.Context, _ provider.CleanupRequest) (provider.OperationResult, error) {
		calls.Add(1)
		entered <- ctx
		select {
		case <-release:
		case <-ctx.Done():
		}
		return provider.OperationResult{Outcome: provider.OutcomeSucceeded}, nil
	}}, nil)
	conn, peer := controlFrames(t, ctx, []any{queuedCleanup("accepted"), queuedCleanup("discarded")})
	done := make(chan error, 1)
	go func() { done <- h.listenWithOperationContext(ctx, ctx, conn) }()
	var accepted context.Context
	select {
	case accepted = <-entered:
	case <-ctx.Done():
		t.Fatal("operation not accepted")
	}
	select {
	case p := <-peer:
		_ = p.Close()
	case <-ctx.Done():
		t.Fatal("frames not written")
	}
	// EOF must retire the queue while preserving the accepted operation context.
	select {
	case <-accepted.Done():
		t.Fatal("EOF canceled accepted work")
	case <-time.After(30 * time.Millisecond):
	}
	unblock()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("reader failed to stop after EOF")
	}
	if calls.Load() != 1 {
		t.Fatalf("Provider calls=%d, expected only accepted command", calls.Load())
	}
}

func controlFrames(t *testing.T, ctx context.Context, frames []any) (*websocket.Conn, <-chan *websocket.Conn) {
	t.Helper()
	peer := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for _, frame := range frames {
			if conn.WriteJSON(frame) != nil {
				return
			}
		}
		peer <- conn
		<-ctx.Done()
	}))
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(); server.Close() })
	return conn, peer
}
