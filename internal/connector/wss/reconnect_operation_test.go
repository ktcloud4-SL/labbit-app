package wss

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

// The second frame is already on the old socket when its session ends. Only
// the first, accepted command may finish; reconnect must not accept queued work.
func TestSessionCancelPreservesAcceptedOperationButStopsBufferedCommands(t *testing.T) {
	conn, written := operationTestSocket(t, 2)
	operationCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	readCtx, cancelRead := context.WithCancel(operationCtx)
	defer cancelRead()
	entered := make(chan context.Context, 1)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	var calls, responses atomic.Int32
	h := NewHandler(&provider.MockProvider{
		CleanupFunc: func(ctx context.Context, _ provider.CleanupRequest) (provider.OperationResult, error) {
			calls.Add(1)
			entered <- ctx
			select {
			case <-release:
			case <-ctx.Done():
			}
			return provider.OperationResult{Outcome: provider.OutcomeSucceeded}, nil
		},
	}, SendMessageFunc(func(ctx context.Context, _ interface{}) error {
		if err := ctx.Err(); err != nil {
			t.Errorf("accepted operation response context canceled: %v", err)
			return err
		}
		responses.Add(1)
		return nil
	}))
	done := make(chan error, 1)
	go func() { done <- h.listenWithOperationContext(readCtx, operationCtx, conn) }()
	var acceptedCtx context.Context
	select {
	case acceptedCtx = <-entered:
	case <-operationCtx.Done():
		t.Fatal("first command did not reach Provider")
	}
	select {
	case <-written:
	case <-operationCtx.Done():
		t.Fatal("server did not write both commands")
	}
	cancelRead()
	if err := acceptedCtx.Err(); err != nil {
		t.Fatalf("transport canceled accepted operation: %v", err)
	}
	close(release)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("listener error = %v, want context.Canceled", err)
		}
	case <-operationCtx.Done():
		t.Fatal("old listener did not stop after accepted operation finished")
	}
	if calls.Load() != 1 || responses.Load() != 2 {
		t.Fatalf("Provider calls=%d, ACK/RESULT responses=%d, want 1/2", calls.Load(), responses.Load())
	}
}

func TestSupervisorShutdownCancelsAcceptedOperationContext(t *testing.T) {
	conn, _ := operationTestSocket(t, 1)
	operationCtx, cancelOperation := context.WithCancel(context.Background())
	defer cancelOperation()
	readCtx, cancelRead := context.WithCancel(operationCtx)
	defer cancelRead()
	entered, canceled := make(chan struct{}), make(chan struct{})
	h := NewHandler(&provider.MockProvider{
		CleanupFunc: func(ctx context.Context, _ provider.CleanupRequest) (provider.OperationResult, error) {
			close(entered)
			<-ctx.Done()
			close(canceled)
			return provider.OperationResult{Outcome: provider.OutcomeUnknown}, nil
		},
	}, nil)
	done := make(chan error, 1)
	go func() { done <- h.listenWithOperationContext(readCtx, operationCtx, conn) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("command did not reach Provider")
	}
	cancelOperation()
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("Supervisor shutdown did not cancel accepted Provider operation")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("listener error = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("listener did not stop on application shutdown")
	}
}

func TestSupervisorPublishesBusinessSenderOnlyAfterHelloAck(t *testing.T) {
	helloRead, releaseAck, connected := make(chan struct{}), make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{
			Subprotocols: []string{protocol.SubprotocolControl},
			CheckOrigin:  func(*http.Request) bool { return true },
		}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var hello protocol.HelloMessage
		if err := conn.ReadJSON(&hello); err != nil {
			return
		}
		close(helloRead)
		<-releaseAck
		if err := conn.WriteJSON(protocol.HelloAckMessage{
			BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeHelloAck, MessageID: generateUUID(), ReplyToMessageID: hello.MessageID, SentAt: time.Now().UTC()},
			Payload:      protocol.HelloAckPayload{ServerTime: time.Now().UTC(), HeartbeatIntervalSeconds: 1, OfflineTimeoutSeconds: 3},
		}); err != nil {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	handler := NewHandler(&provider.MockProvider{}, nil)
	supervisor := NewSupervisor(Config{BaseURL: server.URL, Credential: "test-credential", AllowInsecure: true}, handler, NewDefaultBackoffPolicy())
	supervisor.SetOnConnected(func(*protocol.HelloAckPayload) { close(connected) })
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	defer func() {
		select {
		case <-releaseAck:
		default:
			close(releaseAck)
		}
		cancel()
	}()
	select {
	case <-helloRead:
	case <-ctx.Done():
		t.Fatal("HELLO not received")
	}
	if handler.Sender() != nil {
		t.Error("business sender published before HELLO_ACK")
	}
	close(releaseAck)
	select {
	case <-connected:
	case <-ctx.Done():
		t.Fatal("HELLO_ACK not accepted")
	}
	if handler.Sender() == nil {
		t.Error("business sender missing after HELLO_ACK")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Supervisor did not stop")
	}
	if handler.Sender() != nil {
		t.Error("business sender not cleared after shutdown")
	}
}

func operationTestSocket(t *testing.T, commands int) (*websocket.Conn, <-chan struct{}) {
	t.Helper()
	written, hold := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for i := 0; i < commands; i++ {
			err := conn.WriteJSON(protocol.OperationCommandMessage{
				BaseEnvelope: protocol.BaseEnvelope{
					Type: protocol.MessageTypeOperationCommand, MessageID: generateUUID(), SentAt: time.Now().UTC(),
					OperationID: generateUUID(), LabInstanceID: "lab-test", Generation: 1,
				},
				Payload: protocol.OperationCommandPayload{MutationType: protocol.MutationTypeCleanup, ProviderResources: []protocol.ProviderResourceRef{}},
			})
			if err != nil {
				return
			}
		}
		close(written)
		<-hold
	}))
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		close(hold)
		server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		close(hold)
		server.Close()
	})
	return conn, written
}
