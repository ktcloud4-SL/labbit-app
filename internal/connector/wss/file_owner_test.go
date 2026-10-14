package wss

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/filetransport"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

// Gate after Handler has captured the old sender, before Manager admission.
// This models an ordinary scheduler pause at the production DialCredential call.
type reviewPausedSender struct {
	entered chan struct{}
	resume  chan struct{}
}

func (s *reviewPausedSender) DialCredential() string {
	close(s.entered)
	<-s.resume
	return "control-credential"
}
func (*reviewPausedSender) SendMessage(context.Context, any) error {
	return errors.New("old Control closed")
}

func TestReviewNoAdmissionAfterOldControlRetirement(t *testing.T) {
	dialed := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{Subprotocols: []string{protocol.SubprotocolFileData}}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		dialed <- struct{}{}
		c.SetReadDeadline(time.Now().Add(time.Second))
		c.ReadMessage()
	}))
	defer server.Close()
	m, err := filetransport.New(handlerFiles{}, filetransport.Config{EndpointURL: "ws" + strings.TrimPrefix(server.URL, "http"), RuntimeID: "runtime", AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Shutdown(context.Background())
	s := &reviewPausedSender{entered: make(chan struct{}), resume: make(chan struct{})}
	h := NewHandler(nil, s)
	h.SetFileManager(m, "fallback")
	open := protocol.FileOpenMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeFileOpen, MessageID: "open", SentAt: time.Now().UTC(), FileRequestID: "old-file", LabInstanceID: "lab", Generation: 1}, Payload: protocol.FileOpenPayload{Operation: "READ", TargetVMKey: "workspace", ProviderServerID: "server"}}
	raw, _ := json.Marshal(open)
	done := make(chan error, 1)
	go func() { done <- h.HandleMessage(context.Background(), raw) }()
	<-s.entered
	h.SetSender(nil)
	h.OnControlDisconnected(errors.New("heartbeat disconnected"))
	if m.Active() != 0 {
		t.Fatal("unexpected active worker before resume")
	}
	close(s.resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-dialed:
		t.Fatal("old Control OPEN created File Data WSS after retirement completed")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestFileOldOwnerCleanupPreservesNewOwnerAndRevocationUses4001(t *testing.T) {
	attached := make(chan struct{}, 1)
	closed := make(chan int, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{Subprotocols: []string{protocol.SubprotocolFileData}}
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		if _, _, err = ws.ReadMessage(); err != nil {
			return
		}
		attached <- struct{}{}
		_, _, err = ws.ReadMessage()
		var ce *websocket.CloseError
		code := 0
		if errors.As(err, &ce) {
			code = ce.Code
		}
		closed <- code
	}))
	defer server.Close()
	m, err := filetransport.New(handlerFiles{}, filetransport.Config{EndpointURL: "ws" + strings.TrimPrefix(server.URL, "http"), RuntimeID: "runtime", AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Shutdown(context.Background())
	sender := &fileControlSender{credential: "control-credential", frames: make(chan []byte, 4)}
	h := NewHandler(nil, sender)
	h.SetFileManager(m, "fallback")
	oldEpoch := h.fileOwnerEpoch
	oldOwner := h.fileOwner
	h.SetSender(sender)
	if oldOwner.Err() == nil {
		t.Fatal("Previous Control owner remained live")
	}
	open := protocol.FileOpenMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeFileOpen, MessageID: "open", SentAt: time.Now().UTC(), FileRequestID: "new-file", LabInstanceID: "lab", Generation: 1}, Payload: protocol.FileOpenPayload{Operation: "READ", TargetVMKey: "workspace", ProviderServerID: "server"}}
	raw, _ := json.Marshal(open)
	if h.HandleMessage(context.Background(), raw) != nil {
		t.Fatal("New owner request rejected")
	}
	select {
	case <-attached:
	case <-time.After(time.Second):
		t.Fatal("New owner did not attach")
	}
	h.onControlDisconnected(oldEpoch, errors.New("late old reader EOF"))
	if h.fileOwner.Err() != nil || m.Active() != 1 {
		t.Fatal("Old owner cleanup canceled new File request")
	}
	h.OnControlDisconnected(&websocket.CloseError{Code: protocol.CloseCredentialRevoked})
	select {
	case code := <-closed:
		if code != 4001 {
			t.Fatalf("Data revoke close=%d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("Data was not revoked")
	}
}
