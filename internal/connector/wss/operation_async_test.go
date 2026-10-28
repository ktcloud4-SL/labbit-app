package wss

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"

	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/terminal"
)

func awaitOperationEvent[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Control/Provider event")
	}
	var zero T
	return zero
}

func cleanupCommand(message, operation, lab string) protocol.OperationCommandMessage {
	return protocol.OperationCommandMessage{
		BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeOperationCommand, MessageID: message, SentAt: time.Now().UTC(), OperationID: operation, LabInstanceID: lab, Generation: 1, RequestID: "request-" + message, TraceParent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", TraceState: "vendor=value"},
		Payload:      protocol.OperationCommandPayload{MutationType: protocol.MutationTypeCleanup, ProviderResources: []protocol.ProviderResourceRef{}},
	}
}

func assertOperationCorrelation(t *testing.T, got, command protocol.BaseEnvelope) {
	t.Helper()
	if got.ReplyToMessageID != command.MessageID || got.OperationID != command.OperationID || got.LabInstanceID != command.LabInstanceID || got.Generation != command.Generation || got.RequestID != command.RequestID || got.TraceParent != command.TraceParent || got.TraceState != command.TraceState {
		t.Fatalf("lost command correlation: got %+v, command %+v", got, command)
	}
}

// Exercise the real Control reader and serialized Client writer, as well as
// Terminal Data attach. The mutation stays held through OPEN and CLOSE.
func TestControlTerminalLifecycleWhileProviderMutationIsBlocked(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls, ptys atomic.Int32
	h := NewHandler(&provider.MockProvider{CleanupFunc: func(ctx context.Context, _ provider.CleanupRequest) (provider.OperationResult, error) {
		calls.Add(1)
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return provider.OperationResult{ProviderResources: []provider.ResourceResult{{ResourceRef: provider.ResourceRef{ResourceType: "SERVER", ProviderID: "uncertain-server", Generation: 1}}}}, errors.New("sensitive Provider error")
	}}, nil)
	relay := mock.NewTerminalRelay()
	defer relay.Close()
	mgr := terminal.NewSessionManager(time.Second, nil)
	defer mgr.Shutdown(protocol.TerminalReasonSessionClosed)
	h.SetTerminalManager(mgr, func(string, string, int, int) (terminal.PTYChannel, error) {
		ptys.Add(1)
		return terminal.NewMockEchoPTY(80, 24), nil
	}, terminal.DataWSSClientConfig{EndpointURL: relay.URL(), AllowInsecure: true, DialTimeout: time.Second})
	saas := mock.NewMockSaaS("test-credential")
	defer saas.Close()
	messages := make(chan []byte, 20)
	saas.OnMsgReceived = func(raw []byte) {
		var env protocol.BaseEnvelope
		_ = json.Unmarshal(raw, &env)
		if env.Type != protocol.MessageTypeHello && env.Type != protocol.MessageTypeHeartbeat {
			messages <- raw
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	supervisor := NewSupervisor(Config{BaseURL: saas.URL(), Credential: "test-credential", AllowInsecure: true}, h, NewDefaultBackoffPolicy())
	connected := make(chan struct{})
	supervisor.SetOnConnected(func(*protocol.HelloAckPayload) { close(connected) })
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	defer func() { cancel(); awaitOperationEvent(t, done) }()
	awaitOperationEvent(t, connected)
	send := func(msg any) {
		t.Helper()
		if err := saas.SendRaw(msg); err != nil {
			t.Fatal(err)
		}
	}
	command := cleanupCommand("held", "operation", "mutating-lab")
	send(command)
	var ack protocol.OperationAckMessage
	if err := json.Unmarshal(awaitOperationEvent(t, messages), &ack); err != nil {
		t.Fatal(err)
	}
	if ack.Type != protocol.MessageTypeOperationAck || !ack.Payload.Accepted {
		t.Fatalf("not admitted: %+v", ack)
	}
	assertOperationCorrelation(t, ack.BaseEnvelope, command.BaseEnvelope)
	awaitOperationEvent(t, entered)
	open := protocol.TerminalOpenMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeTerminalOpen, MessageID: "open", SentAt: time.Now().UTC(), TerminalSessionID: "ready-terminal", LabInstanceID: "ready-lab", Generation: 1}, Payload: protocol.TerminalOpenPayload{TargetVmKey: "workspace", ProviderServerID: "ready-server", Cols: 80, Rows: 24}}
	for i := 0; i < 2; i++ {
		send(open)
		var result protocol.TerminalOpenResultMessage
		if err := json.Unmarshal(awaitOperationEvent(t, messages), &result); err != nil {
			t.Fatal(err)
		}
		if result.Type != protocol.MessageTypeTerminalOpenResult || result.ReplyToMessageID != open.MessageID || result.Payload.Outcome != protocol.OutcomeSucceeded {
			t.Fatalf("OPEN blocked/failed: %+v", result)
		}
	}
	if ptys.Load() != 1 {
		t.Fatalf("duplicate OPEN created %d PTYs", ptys.Load())
	}
	session, exists := mgr.GetSession(open.TerminalSessionID)
	if !exists {
		t.Fatal("Terminal session missing")
	}
	closeMsg := protocol.TerminalCloseMessage{BaseEnvelope: open.BaseEnvelope, Payload: protocol.TerminalClosePayload{Reason: protocol.TerminalReasonSessionClosed}}
	closeMsg.Type, closeMsg.MessageID = protocol.MessageTypeTerminalClose, "close"
	send(closeMsg)
	var ended protocol.TerminalEndedMessage
	if err := json.Unmarshal(awaitOperationEvent(t, messages), &ended); err != nil {
		t.Fatal(err)
	}
	if ended.Type != protocol.MessageTypeTerminalEnded || ended.TerminalSessionID != open.TerminalSessionID || session.GetStatus() != terminal.StatusClosed {
		t.Fatalf("CLOSE blocked/failed: %+v", ended)
	}
	send(closeMsg)
	// A subsequent OPEN is a read-loop barrier after duplicate CLOSE; it must
	// fail without resurrecting the closed PTY or emitting a second ENDED.
	send(open)
	var reopened protocol.TerminalOpenResultMessage
	if err := json.Unmarshal(awaitOperationEvent(t, messages), &reopened); err != nil {
		t.Fatal(err)
	}
	if reopened.Type != protocol.MessageTypeTerminalOpenResult || reopened.Payload.Outcome != protocol.OutcomeFailed || ptys.Load() != 1 {
		t.Fatalf("closed Terminal resurrected: %+v", reopened)
	}
	// A duplicate delivery and a different Item of the same parent Operation
	// cannot launch extra mutations while the execution slot is occupied.
	for _, lab := range []string{"mutating-lab", "another-item"} {
		next := cleanupCommand("busy-"+lab, command.OperationID, lab)
		send(next)
		if err := json.Unmarshal(awaitOperationEvent(t, messages), &ack); err != nil {
			t.Fatal(err)
		}
		if ack.Type != protocol.MessageTypeOperationAck || ack.Payload.Accepted || ack.Payload.Error == nil {
			t.Fatalf("busy work was accepted: %+v", ack)
		}
		assertOperationCorrelation(t, ack.BaseEnvelope, next.BaseEnvelope)
	}
	if calls.Load() != 1 {
		t.Fatalf("mutation calls=%d, want 1", calls.Load())
	}
	close(release)
	var result protocol.OperationResultMessage
	if err := json.Unmarshal(awaitOperationEvent(t, messages), &result); err != nil {
		t.Fatal(err)
	}
	if result.Type != protocol.MessageTypeOperationResult || result.Payload.Outcome != protocol.OutcomeUnknown || len(result.Payload.ProviderResources) != 1 || result.Payload.ProviderResources[0].ProviderID != "uncertain-server" {
		t.Fatalf("UNKNOWN changed: %+v", result)
	}
	assertOperationCorrelation(t, result.BaseEnvelope, command.BaseEnvelope)
}

func TestOperationAdmissionACKFailureAndCompletionBoundary(t *testing.T) {
	entered, release := make(chan struct{}, 2), make(chan struct{})
	var calls atomic.Int32
	messages := make(chan any, 10)
	resultEntered, resultRelease := make(chan struct{}), make(chan struct{})
	h := NewHandler(&provider.MockProvider{CleanupFunc: func(ctx context.Context, _ provider.CleanupRequest) (provider.OperationResult, error) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return provider.OperationResult{Outcome: provider.OutcomeSucceeded}, nil
	}}, SendMessageFunc(func(_ context.Context, msg any) error {
		messages <- msg
		if _, ok := msg.(protocol.OperationResultMessage); ok {
			close(resultEntered)
			<-resultRelease
		}
		return nil
	}))
	raw, _ := json.Marshal(cleanupCommand("first", "parent", "first-item"))
	done := make(chan error, 1)
	go func() { done <- h.HandleMessage(context.Background(), raw) }()
	awaitOperationEvent(t, entered)
	if ack := awaitOperationEvent(t, messages).(protocol.OperationAckMessage); !ack.Payload.Accepted {
		t.Fatal("free slot rejected")
	}
	close(release)
	awaitOperationEvent(t, resultEntered)
	_ = awaitOperationEvent(t, messages).(protocol.OperationResultMessage)
	next := cleanupCommand("next", "parent", "second-item")
	nextRaw, _ := json.Marshal(next)
	if err := h.HandleMessage(context.Background(), nextRaw); err != nil {
		t.Fatal(err)
	}
	if ack := awaitOperationEvent(t, messages).(protocol.OperationAckMessage); ack.Payload.Accepted {
		t.Fatal("slot released before worker completed")
	}
	close(resultRelease)
	if err := awaitOperationEvent(t, done); err != nil {
		t.Fatal(err)
	}
	// Rejection did not record the parent Operation ID or replay it. A distinct
	// Item remains executable when capacity becomes available.
	h.SetSender(SendMessageFunc(func(_ context.Context, msg any) error { messages <- msg; return nil }))
	if err := h.HandleMessage(context.Background(), nextRaw); err != nil {
		t.Fatal(err)
	}
	if ack := awaitOperationEvent(t, messages).(protocol.OperationAckMessage); !ack.Payload.Accepted {
		t.Fatal("completed worker retained slot")
	}
	_ = awaitOperationEvent(t, messages).(protocol.OperationResultMessage)
	if calls.Load() != 2 {
		t.Fatalf("distinct Item calls=%d", calls.Load())
	}
	h.SetSender(SendMessageFunc(func(context.Context, any) error { return errors.New("ACK transport failure") }))
	if err := h.HandleMessage(context.Background(), raw); err == nil {
		t.Fatal("ACK write failure ignored")
	}
	if calls.Load() != 2 {
		t.Fatal("Provider called after failed ACK")
	}
	h.SetSender(SendMessageFunc(func(_ context.Context, msg any) error { messages <- msg; return nil }))
	if err := h.HandleMessage(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	if ack := awaitOperationEvent(t, messages).(protocol.OperationAckMessage); !ack.Payload.Accepted {
		t.Fatal("failed ACK leaked slot")
	}
	_ = awaitOperationEvent(t, messages).(protocol.OperationResultMessage)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.HandleMessage(canceled, raw); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled admission=%v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("unexpected Provider calls=%d", calls.Load())
	}
}

// Accepted work outlives the old Control connection. Its single execution
// slot spans reconnect, but its result is neither stored nor replayed. A late
// result may use only a business sender published after HELLO_ACK.
func TestReconnectOperationAdmissionAndLateResult(t *testing.T) {
	for _, finishBeforeAck := range []bool{true, false} {
		name := "after HELLO_ACK"
		if finishBeforeAck {
			name = "during HELLO"
		}
		t.Run(name, func(t *testing.T) {
			type session struct {
				conn  *websocket.Conn
				hello protocol.HelloMessage
			}
			sessions := make(chan session, 2)
			messages := make(chan []byte, 10)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{Subprotocols: []string{protocol.SubprotocolControl}, CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				var hello protocol.HelloMessage
				if err := conn.ReadJSON(&hello); err != nil {
					return
				}
				sessions <- session{conn, hello}
				for {
					_, raw, err := conn.ReadMessage()
					if err != nil {
						return
					}
					var env protocol.BaseEnvelope
					_ = json.Unmarshal(raw, &env)
					if env.Type != protocol.MessageTypeHeartbeat {
						messages <- raw
					}
				}
			}))
			defer server.Close()
			entered, release := make(chan context.Context, 1), make(chan struct{})
			var calls atomic.Int32
			h := NewHandler(&provider.MockProvider{CleanupFunc: func(ctx context.Context, _ provider.CleanupRequest) (provider.OperationResult, error) {
				if calls.Add(1) == 1 {
					entered <- ctx
					select {
					case <-release:
					case <-ctx.Done():
					}
				}
				return provider.OperationResult{Outcome: provider.OutcomeSucceeded}, nil
			}}, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			supervisor := NewSupervisor(Config{BaseURL: server.URL, Credential: "test-credential", AllowInsecure: true}, h, BackoffPolicy{InitialInterval: time.Millisecond, MaxInterval: time.Millisecond, Multiplier: 1})
			connected := make(chan struct{}, 2)
			supervisor.SetOnConnected(func(*protocol.HelloAckPayload) { connected <- struct{}{} })
			done := make(chan error, 1)
			go func() { done <- supervisor.Run(ctx) }()
			defer func() { cancel(); awaitOperationEvent(t, done) }()
			handshake := func(s session) {
				t.Helper()
				if s.hello.Type != protocol.MessageTypeHello {
					t.Fatal("business message preceded HELLO")
				}
				if err := s.conn.WriteJSON(protocol.HelloAckMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeHelloAck, MessageID: generateUUID(), ReplyToMessageID: s.hello.MessageID, SentAt: time.Now().UTC()}, Payload: protocol.HelloAckPayload{ServerTime: time.Now().UTC(), HeartbeatIntervalSeconds: 15, OfflineTimeoutSeconds: 45}}); err != nil {
					t.Fatal(err)
				}
				awaitOperationEvent(t, connected)
			}
			first := awaitOperationEvent(t, sessions)
			handshake(first)
			command := cleanupCommand("old-command", "parent", "first-item")
			if err := first.conn.WriteJSON(command); err != nil {
				t.Fatal(err)
			}
			var ack protocol.OperationAckMessage
			if err := json.Unmarshal(awaitOperationEvent(t, messages), &ack); err != nil {
				t.Fatal(err)
			}
			if !ack.Payload.Accepted {
				t.Fatal("first command rejected")
			}
			acceptedCtx := awaitOperationEvent(t, entered)
			_ = first.conn.Close()
			next := awaitOperationEvent(t, sessions)
			if acceptedCtx.Err() != nil {
				t.Fatal("disconnect canceled accepted work")
			}
			if h.Sender() != nil {
				t.Fatal("business sender published before new HELLO_ACK")
			}
			if finishBeforeAck {
				close(release)
				// Wait for the whole admitted task, including its result send attempt,
				// to relinquish capacity while the new handshake is deliberately held.
				select {
				case h.operationSlot <- struct{}{}:
				case <-time.After(5 * time.Second):
					t.Fatal("old worker did not finish during HELLO")
				}
				<-h.operationSlot
				handshake(next)
			} else {
				handshake(next)
				busy := cleanupCommand("busy-new-socket", "parent", "second-item")
				if err := next.conn.WriteJSON(busy); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(awaitOperationEvent(t, messages), &ack); err != nil {
					t.Fatal(err)
				}
				if ack.Type != protocol.MessageTypeOperationAck || ack.Payload.Accepted {
					t.Fatal("reconnect admitted concurrent mutation")
				}
				assertOperationCorrelation(t, ack.BaseEnvelope, busy.BaseEnvelope)
				close(release)
				var result protocol.OperationResultMessage
				if err := json.Unmarshal(awaitOperationEvent(t, messages), &result); err != nil {
					t.Fatal(err)
				}
				if result.Type != protocol.MessageTypeOperationResult || result.Payload.Outcome != protocol.OutcomeSucceeded {
					t.Fatalf("late result=%+v", result)
				}
				assertOperationCorrelation(t, result.BaseEnvelope, command.BaseEnvelope)
				select {
				case h.operationSlot <- struct{}{}:
				case <-time.After(5 * time.Second):
					t.Fatal("old worker retained slot")
				}
				<-h.operationSlot
			}
			distinct := cleanupCommand("distinct-new-socket", "parent", "second-item")
			if err := next.conn.WriteJSON(distinct); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(awaitOperationEvent(t, messages), &ack); err != nil {
				t.Fatal(err)
			}
			if ack.Type != protocol.MessageTypeOperationAck || !ack.Payload.Accepted {
				t.Fatalf("distinct Item not admitted/no-replay broken: %+v", ack)
			}
			assertOperationCorrelation(t, ack.BaseEnvelope, distinct.BaseEnvelope)
			var result protocol.OperationResultMessage
			if err := json.Unmarshal(awaitOperationEvent(t, messages), &result); err != nil {
				t.Fatal(err)
			}
			assertOperationCorrelation(t, result.BaseEnvelope, distinct.BaseEnvelope)
			if result.Type != protocol.MessageTypeOperationResult || calls.Load() != 2 {
				t.Fatalf("calls=%d result=%+v", calls.Load(), result)
			}
		})
	}
}

func TestRetiredControlOperationCannotUseReplacementSender(t *testing.T) {
	var calls, sends atomic.Int32
	h := NewHandler(&provider.MockProvider{CleanupFunc: func(context.Context, provider.CleanupRequest) (provider.OperationResult, error) {
		calls.Add(1)
		return provider.OperationResult{Outcome: provider.OutcomeSucceeded}, nil
	}}, nil)
	oldEpoch := h.fileOwnerEpoch
	h.SetSender(SendMessageFunc(func(context.Context, any) error { sends.Add(1); return nil }))
	raw, _ := json.Marshal(cleanupCommand("buffered-old-command", "operation", "lab"))
	if err := h.handleMessageWithFileOwner(context.Background(), raw, oldEpoch, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("retired Control admission=%v", err)
	}
	if calls.Load() != 0 || sends.Load() != 0 {
		t.Fatalf("retired command reached Provider/new sender: %d/%d", calls.Load(), sends.Load())
	}
}
