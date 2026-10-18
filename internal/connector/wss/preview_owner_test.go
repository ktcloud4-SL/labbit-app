package wss

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/preview"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
)

type previewOwnerSender struct{ credential string }

func (s previewOwnerSender) DialCredential() string                 { return s.credential }
func (s previewOwnerSender) SendMessage(context.Context, any) error { return nil }

type previewOwnerForwarder struct {
	opened atomic.Int32
	peer   net.Conn
}

func (f *previewOwnerForwarder) DialTCP(context.Context, vmaccess.Target, int) (net.Conn, error) {
	f.opened.Add(1)
	a, b := net.Pipe()
	f.peer = b
	return a, nil
}

func TestPreviewOldControlEpochCannotUseNewCredential(t *testing.T) {
	observed := make(chan string, 1)
	upgrader := websocket.Upgrader{Subprotocols: []string{protocol.SubprotocolPreviewData}}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- r.Header.Get("Authorization")
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		var attach protocol.PreviewAttachMessage
		if ws.ReadJSON(&attach) != nil {
			return
		}
		_ = ws.WriteJSON(protocol.PreviewAttachedMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypePreviewAttached, MessageID: "ack", ReplyToMessageID: attach.MessageID, PreviewSessionID: attach.PreviewSessionID, LabInstanceID: attach.LabInstanceID, Generation: attach.Generation}, Payload: protocol.PreviewAttachedPayload{}})
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer gateway.Close()
	forwarder := &previewOwnerForwarder{}
	manager := preview.NewSessionManager(forwarder, nil)
	defer manager.CloseAll("cleanup")
	handler := NewHandler(&provider.MockProvider{}, previewOwnerSender{credential: "old-fixture-token"})
	handler.SetPreviewManager(manager, preview.DataWSSClientConfig{EndpointURL: strings.Replace(gateway.URL, "http", "ws", 1), AllowInsecure: true, RuntimeID: "runtime"})
	oldEpoch := handler.fileOwnerEpoch
	handler.SetSender(previewOwnerSender{credential: "new-fixture-token"})
	raw, _ := json.Marshal(protocol.PreviewOpenMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypePreviewOpen, MessageID: "old-control-open", SentAt: time.Now(), PreviewSessionID: "old-S", LabInstanceID: "lab", Generation: 1}, Payload: protocol.PreviewOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server", TargetPort: 3000}})
	err := handler.handleMessageWithFileOwner(context.Background(), raw, oldEpoch)
	if forwarder.peer != nil {
		defer forwarder.peer.Close()
	}
	if err == nil || forwarder.opened.Load() != 0 {
		credential := "no observed upgrade"
		select {
		case credential = <-observed:
		default:
		}
		t.Errorf("old Control epoch migrated to current owner: err=%v dialCount=%d authorization=%s", err, forwarder.opened.Load(), credential)
	}
}

type previewDialFunc func(context.Context, vmaccess.Target, int) (net.Conn, error)

func (f previewDialFunc) DialTCP(c context.Context, t vmaccess.Target, p int) (net.Conn, error) {
	return f(c, t, p)
}

func TestPreviewControlCloseCancelsPendingOpenWithoutBlockingReader(t *testing.T) {
	saas := mock.NewMockSaaS("fixture-token")
	defer saas.Close()
	client := NewClient(Config{BaseURL: saas.URL(), Credential: "fixture-token", AllowInsecure: true})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if client.Dial(ctx) != nil {
		t.Fatal("Control dial failed")
	}
	defer client.Close()
	if _, err := client.SendHello(ctx); err != nil {
		t.Fatal(err)
	}
	entered, retired := make(chan struct{}), make(chan struct{})
	manager := preview.NewSessionManager(previewDialFunc(func(ctx context.Context, _ vmaccess.Target, _ int) (net.Conn, error) {
		close(entered)
		<-ctx.Done()
		close(retired)
		return nil, ctx.Err()
	}), nil)
	defer manager.Shutdown("cleanup")
	handler := NewHandler(&provider.MockProvider{}, client)
	handler.SetPreviewManager(manager, preview.DataWSSClientConfig{EndpointURL: "ws://127.0.0.1:1", AllowInsecure: true})
	done := make(chan error, 1)
	go func() { done <- handler.Listen(ctx, client.Conn()) }()
	defer func() {
		cancel()
		_ = client.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("Control reader stuck")
		}
	}()
	env := protocol.BaseEnvelope{Type: protocol.MessageTypePreviewOpen, MessageID: "OPEN", SentAt: time.Now(), PreviewSessionID: "S", LabInstanceID: "lab", Generation: 1}
	if saas.SendRaw(protocol.PreviewOpenMessage{BaseEnvelope: env, Payload: protocol.PreviewOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server", TargetPort: 3000}}) != nil {
		t.Fatal("OPEN send")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("pending open never started")
	}
	env.Type = protocol.MessageTypePreviewClose
	env.MessageID = "CLOSE"
	if saas.SendRaw(protocol.PreviewCloseMessage{BaseEnvelope: env, Payload: protocol.PreviewClosePayload{Reason: protocol.PreviewReasonSessionClosed}}) != nil {
		t.Fatal("CLOSE send")
	}
	select {
	case <-retired:
	case <-time.After(time.Second):
		t.Fatal("Control CLOSE blocked behind pending OPEN")
	}
	if manager.Count() != 0 {
		t.Fatal("pending logical session retained")
	}
}

func TestPreviewResultWriteSurvivesTunnelCancellation(t *testing.T) {
	gateway := mock.NewPreviewGateway()
	defer gateway.Close()
	a, b := net.Pipe()
	defer b.Close()
	manager := preview.NewSessionManager(previewDialFunc(func(context.Context, vmaccess.Target, int) (net.Conn, error) { return a, nil }), nil)
	defer manager.Shutdown("cleanup")
	writing, release := make(chan context.Context, 1), make(chan struct{})
	handler := NewHandler(&provider.MockProvider{}, SendMessageFunc(func(ctx context.Context, _ any) error {
		writing <- ctx
		<-release
		return ctx.Err()
	}))
	handler.SetPreviewManager(manager, preview.DataWSSClientConfig{EndpointURL: gateway.URL(), RuntimeID: "runtime", AllowInsecure: true})
	msg := protocol.PreviewOpenMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypePreviewOpen, MessageID: "M1", SentAt: time.Now(), PreviewSessionID: "S", LabInstanceID: "lab", Generation: 1}, Payload: protocol.PreviewOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server", TargetPort: 3000}}
	raw, _ := json.Marshal(msg)
	done := make(chan error, 1)
	go func() { done <- handler.HandleMessage(context.Background(), raw) }()
	var notification context.Context
	select {
	case notification = <-writing:
	case <-time.After(time.Second):
		t.Fatal("result not written")
	}
	_ = manager.CloseSession("S", protocol.PreviewReasonSessionClosed)
	if notification.Err() != nil {
		t.Error("tunnel cancellation canceled shared Control write")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPreviewAttachedTunnelControlDisconnect(t *testing.T) {
	for _, code := range []int{0, protocol.CloseCredentialRevoked, protocol.CloseSessionReplaced} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			gateway := mock.NewPreviewGateway()
			defer gateway.Close()
			tcp, peer := net.Pipe()
			defer peer.Close()
			pending, canceled := make(chan struct{}), make(chan struct{})
			manager := preview.NewSessionManager(previewDialFunc(func(ctx context.Context, target vmaccess.Target, _ int) (net.Conn, error) {
				if target.VMKey == "pending" {
					close(pending)
					<-ctx.Done()
					close(canceled)
					return nil, ctx.Err()
				}
				return tcp, nil
			}), nil)
			defer manager.Shutdown("cleanup")
			h := NewHandler(&provider.MockProvider{}, previewOwnerSender{"fixture-token"})
			h.SetPreviewManager(manager, preview.DataWSSClientConfig{EndpointURL: gateway.URL(), RuntimeID: "runtime", AllowInsecure: true})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			msg := protocol.PreviewOpenMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypePreviewOpen, MessageID: "OPEN", PreviewSessionID: "S", LabInstanceID: "lab", Generation: 1}, Payload: protocol.PreviewOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server", TargetPort: 3000}}
			raw, _ := json.Marshal(msg)
			if err := h.HandleMessage(ctx, raw); err != nil {
				t.Fatal(err)
			}
			session, ok := manager.GetSession("S")
			if !ok || !session.HasActiveTunnel() || !session.EverAttached() {
				t.Fatal("fixture did not attach")
			}
			request := "GET / HTTP/1.1\r\nHost: workspace\r\n\r\n"
			if err := gateway.SendBinary([]byte(request)); err != nil {
				t.Fatal(err)
			}
			_ = peer.SetDeadline(time.Now().Add(3 * time.Second))
			got := make([]byte, len(request))
			if _, err := io.ReadFull(peer, got); err != nil || string(got) != request {
				t.Fatalf("HTTP request: %q, %v", got, err)
			}
			stream := func(chunk string) {
				t.Helper()
				_ = peer.SetWriteDeadline(time.Now().Add(time.Second))
				if _, err := io.WriteString(peer, chunk); err != nil {
					t.Fatalf("HTTP response interrupted: %v", err)
				}
				var received []byte
				for len(received) < len(chunk) {
					frame, err := gateway.WaitForBinary(time.Second)
					if err != nil {
						t.Fatal(err)
					}
					received = append(received, frame...)
				}
				if string(received) != chunk {
					t.Fatalf("HTTP response: got %q, want %q", received, chunk)
				}
			}
			stream("HTTP/1.1 200 OK\r\nContent-Length: 6\r\n\r\nab")
			if code == 0 {
				msg.MessageID, msg.PreviewSessionID, msg.Payload.TargetVmKey = "PENDING", "pending", "pending"
				raw, _ = json.Marshal(msg)
				if err := h.handleMessageWithFileOwner(ctx, raw, h.fileOwnerEpoch, true); err != nil {
					t.Fatal(err)
				}
				select {
				case <-pending:
				case <-time.After(time.Second):
					t.Fatal("pending OPEN not started")
				}
				h.OnControlDisconnected(io.EOF)
				h.SetSender(nil)
				select {
				case <-canceled:
				case <-time.After(time.Second):
					t.Fatal("ordinary disconnect retained pending OPEN")
				}
				stream("cd") // The in-flight response continues while Control is offline.
				h.SetSender(previewOwnerSender{"fixture-token"})
				stream("ef")
				if current, ok := manager.GetSession("S"); !ok || current != session || !session.HasActiveTunnel() {
					t.Fatal("reconnect replaced the active tunnel")
				}
				closeRaw, _ := json.Marshal(protocol.PreviewCloseMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypePreviewClose, MessageID: "CLOSE", PreviewSessionID: "S", LabInstanceID: "lab", Generation: 1}, Payload: protocol.PreviewClosePayload{Reason: protocol.PreviewReasonSessionClosed}})
				if err := h.HandleMessage(ctx, closeRaw); err != nil {
					t.Fatal(err)
				}
			} else {
				h.OnControlDisconnected(&websocket.CloseError{Code: code})
				h.SetSender(nil)
			}
			if err := gateway.WaitForClose(time.Second); err != nil {
				t.Fatal("explicit termination retained Data WSS:", err)
			}
			_ = peer.SetReadDeadline(time.Now().Add(time.Second))
			if _, err := peer.Read(make([]byte, 1)); err != io.EOF {
				t.Fatalf("explicit termination did not close TCP: %v", err)
			}
			if !session.IsDestroyed() {
				t.Fatal("explicit termination retained logical session")
			}
		})
	}
}

func TestPreviewAttachedTunnelDuringOpenResult(t *testing.T) {
	for _, name := range []string{"send_failure", "eof_send_success", "eof_send_failure", "timeout", "4001", "4002", "close", "reset", "cleanup", "shutdown", "caller_cancel"} {
		t.Run(name, func(t *testing.T) {
			gateway := mock.NewPreviewGateway()
			defer gateway.Close()
			tcp, peer := net.Pipe()
			defer peer.Close()
			manager := preview.NewSessionManager(previewDialFunc(func(context.Context, vmaccess.Target, int) (net.Conn, error) { return tcp, nil }), nil)
			defer manager.Shutdown("cleanup")
			writing, release := make(chan struct{}), make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			h := NewHandler(&provider.MockProvider{}, SendMessageFunc(func(ctx context.Context, _ any) error {
				close(writing)
				<-release
				if name == "timeout" {
					<-ctx.Done()
					return ctx.Err()
				}
				if name == "eof_send_success" {
					return nil
				}
				return io.ErrClosedPipe
			}))
			h.SetPreviewManager(manager, preview.DataWSSClientConfig{EndpointURL: gateway.URL(), RuntimeID: "runtime", AllowInsecure: true})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			msg := protocol.PreviewOpenMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypePreviewOpen, MessageID: "OPEN", PreviewSessionID: "S", LabInstanceID: "lab", Generation: 1}, Payload: protocol.PreviewOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server", TargetPort: 3000}}
			raw, _ := json.Marshal(msg)
			done := make(chan error, 1)
			go func() { done <- h.HandleMessage(ctx, raw) }()
			select {
			case <-writing:
			case <-time.After(3 * time.Second):
				t.Fatal("OPEN_RESULT write not reached")
			}
			session, ok := manager.GetSession("S")
			if !ok || !session.EverAttached() || !session.HasActiveTunnel() {
				t.Fatal("result write began without attached tunnel")
			}
			request := "GET / HTTP/1.1\r\nHost: workspace\r\n\r\n"
			if err := gateway.SendBinary([]byte(request)); err != nil {
				t.Fatal(err)
			}
			_ = peer.SetReadDeadline(time.Now().Add(3 * time.Second))
			got := make([]byte, len(request))
			if _, err := io.ReadFull(peer, got); err != nil || string(got) != request {
				t.Fatalf("HTTP request: %q, %v", got, err)
			}
			stream := func(chunk string) {
				t.Helper()
				_ = peer.SetWriteDeadline(time.Now().Add(time.Second))
				if _, err := io.WriteString(peer, chunk); err != nil {
					t.Fatalf("attached HTTP response interrupted: %v", err)
				}
				var received []byte
				for len(received) < len(chunk) {
					frame, err := gateway.WaitForBinary(time.Second)
					if err != nil {
						t.Fatal(err)
					}
					received = append(received, frame...)
				}
				if string(received) != chunk {
					t.Fatalf("HTTP response: %q, want %q", received, chunk)
				}
			}
			stream("HTTP/1.1 200 OK\r\nContent-Length: 6\r\n\r\nab")
			retired := true
			switch name {
			case "4001", "4002":
				code := protocol.CloseCredentialRevoked
				if name == "4002" {
					code = protocol.CloseSessionReplaced
				}
				h.OnControlDisconnected(&websocket.CloseError{Code: code})
				h.SetSender(nil)
			case "close":
				closeRaw, _ := json.Marshal(protocol.PreviewCloseMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypePreviewClose, MessageID: "CLOSE", PreviewSessionID: "S", LabInstanceID: "lab", Generation: 1}, Payload: protocol.PreviewClosePayload{Reason: protocol.PreviewReasonSessionClosed}})
				if err := h.HandleMessage(ctx, closeRaw); err != nil {
					t.Fatal(err)
				}
			case "reset":
				manager.InvalidateBefore("lab", 2)
			case "cleanup":
				manager.CloseAll(protocol.PreviewReasonLabCleanup)
			case "shutdown":
				manager.Shutdown(protocol.PreviewReasonServiceRestart)
			case "caller_cancel":
				cancel()
			default:
				retired = false
				if strings.HasPrefix(name, "eof_") {
					h.OnControlDisconnected(io.EOF)
					h.SetSender(nil)
				}
				stream("cd") // OPEN_RESULT remains blocked throughout the disconnect.
			}
			if retired {
				if err := gateway.WaitForClose(time.Second); err != nil {
					t.Fatal("explicit termination waited for result write:", err)
				}
				_ = peer.SetReadDeadline(time.Now().Add(time.Second))
				if _, err := peer.Read(make([]byte, 1)); err != io.EOF {
					t.Fatalf("explicit termination retained TCP: %v", err)
				}
			}
			close(release)
			select {
			case err := <-done:
				if (err == nil) != (name == "eof_send_success") {
					t.Fatalf("result delivery error: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("result writer did not finish")
			}
			if !retired {
				if strings.HasPrefix(name, "eof_") {
					h.SetSender(previewOwnerSender{"fixture-token"})
				}
				stream("ef")
				if current, ok := manager.GetSession("S"); !ok || current != session || !session.HasActiveTunnel() {
					t.Fatal("result delivery replaced attached tunnel")
				}
			}
		})
	}
}

func TestPreviewRetirementDoesNotCancelReplacementOwner(t *testing.T) {
	for _, code := range []int{protocol.CloseCredentialRevoked, protocol.CloseSessionReplaced} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			manager := preview.NewSessionManager(nil, nil)
			defer manager.Shutdown("cleanup")
			handler := NewHandler(&provider.MockProvider{}, previewOwnerSender{"old"})
			handler.SetPreviewManager(manager, preview.DataWSSClientConfig{})
			oldEpoch := handler.fileOwnerEpoch
			env := protocol.BaseEnvelope{MessageID: "old", PreviewSessionID: "old", LabInstanceID: "lab", Generation: 1}
			payload := protocol.PreviewOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server", TargetPort: 3000}
			if _, err := manager.PrepareOpen(context.Background(), payload, env); err != nil {
				t.Fatal(err)
			}
			closing, release := make(chan struct{}), make(chan struct{})
			manager.SetOnEnded(func(s *preview.PreviewSession, _ string, _ error) {
				if s.SessionID == "old" {
					close(closing)
					<-release
				}
			})
			retired := make(chan struct{})
			go func() { handler.onControlDisconnected(oldEpoch, &websocket.CloseError{Code: code}); close(retired) }()
			select {
			case <-closing:
			case <-time.After(time.Second):
				t.Fatal("old session not retired")
			}
			replaced := make(chan struct{})
			go func() { handler.SetSender(previewOwnerSender{"new"}); close(replaced) }()
			select {
			case <-replaced:
			case <-time.After(time.Second):
				close(release)
				t.Fatal("retirement callback held Control authority lock")
			}
			env.MessageID = "new"
			env.PreviewSessionID = "new"
			if _, err := manager.PrepareOpen(context.Background(), payload, env); err != nil {
				t.Fatal(err)
			}
			handler.onControlDisconnected(oldEpoch, &websocket.CloseError{Code: code})
			close(release)
			<-retired
			if handler.previewOwner.Err() != nil || manager.Count() != 1 {
				t.Fatal("old retirement affected replacement owner/session")
			}
		})
	}
}

func TestPreviewWorkerBoundAndRetirementRelease(t *testing.T) {
	manager := preview.NewSessionManager(previewDialFunc(func(ctx context.Context, _ vmaccess.Target, _ int) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}), nil)
	defer manager.Shutdown("cleanup")
	results := make(chan protocol.PreviewOpenResultMessage, 128)
	handler := NewHandler(&provider.MockProvider{}, SendMessageFunc(func(_ context.Context, msg any) error {
		if r, ok := msg.(protocol.PreviewOpenResultMessage); ok {
			results <- r
		}
		return nil
	}))
	handler.SetPreviewManager(manager, preview.DataWSSClientConfig{EndpointURL: "ws://127.0.0.1:1", AllowInsecure: true})
	epoch := handler.fileOwnerEpoch
	for n := 0; n < cap(handler.previewOpenSlots)+1; n++ {
		id := fmt.Sprintf("open-%d", n)
		raw, _ := json.Marshal(protocol.PreviewOpenMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypePreviewOpen, MessageID: id, PreviewSessionID: id, LabInstanceID: "lab", Generation: 1}, Payload: protocol.PreviewOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server", TargetPort: 3000}})
		_ = handler.handleMessageWithFileOwner(context.Background(), raw, epoch, true)
	}
	select {
	case r := <-results:
		if r.Payload.Error == nil || r.Payload.Error.Code != protocol.PreviewErrorCodeUnavailable {
			t.Fatal("worker bound did not fail safely")
		}
	case <-time.After(time.Second):
		t.Fatal("Control reader blocked on worker admission")
	}
	if manager.Count() != cap(handler.previewOpenSlots) {
		t.Fatal("unbounded pending opens")
	}
	handler.onControlDisconnected(epoch, &websocket.CloseError{Code: protocol.CloseCredentialRevoked})
	deadline := time.Now().Add(time.Second)
	for len(handler.previewOpenSlots) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if manager.Count() != 0 || len(handler.previewOpenSlots) != 0 {
		t.Fatal("retirement retained pending worker/session")
	}
}
