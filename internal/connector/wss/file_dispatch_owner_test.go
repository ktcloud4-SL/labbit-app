package wss

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/filetransport"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

func TestReviewLateOldFrameNeverBindsNewControlOwner(t *testing.T) {
	dialed := make(chan string, 1)
	dataServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{Subprotocols: []string{protocol.SubprotocolFileData}}
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		dialed <- r.Header.Get("Authorization")
		ws.SetReadDeadline(time.Now().Add(time.Second))
		ws.ReadMessage()
	}))
	defer dataServer.Close()
	controlPeer := make(chan *websocket.Conn, 1)
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{}
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		controlPeer <- ws
	}))
	defer controlServer.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(controlServer.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	peer := <-controlPeer
	defer peer.Close()
	m, err := filetransport.New(handlerFiles{}, filetransport.Config{EndpointURL: "ws" + strings.TrimPrefix(dataServer.URL, "http"), RuntimeID: "runtime", AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Shutdown(context.Background())
	oldSender := &fileControlSender{credential: "old-credential", frames: make(chan []byte, 4)}
	newSender := &fileControlSender{credential: "new-credential", frames: make(chan []byte, 4)}
	h := NewHandler(nil, oldSender)
	h.SetFileManager(m, "fallback")
	readCtx, cancelRead := context.WithCancel(context.Background())
	defer cancelRead()
	done := make(chan error, 1)
	go func() { done <- h.listenWithOperationContext(readCtx, context.Background(), conn) }()
	// Let the listener capture the old owner epoch and enter ReadMessage.
	time.Sleep(30 * time.Millisecond)
	// A pending writer prevents the File handler's new RLock while its message
	// is decoded. This is a deterministic scheduling gate, without source hooks.
	h.mu.RLock()
	switched := make(chan struct{})
	go func() { h.SetSender(newSender); close(switched) }()
	time.Sleep(30 * time.Millisecond)
	open := protocol.FileOpenMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeFileOpen, MessageID: "old-open", SentAt: time.Now().UTC(), FileRequestID: "old-frame", LabInstanceID: "lab", Generation: 1}, Payload: protocol.FileOpenPayload{Operation: "READ", TargetVMKey: "workspace", ProviderServerID: "server"}}
	raw, _ := json.Marshal(open)
	if err := peer.WriteMessage(websocket.TextMessage, raw); err != nil {
		h.mu.RUnlock()
		t.Fatal(err)
	}
	// The old reader has passed readCtx.Err and is blocked at handler admission.
	time.Sleep(30 * time.Millisecond)
	cancelRead()
	h.mu.RUnlock()
	<-switched
	select {
	case auth := <-dialed:
		t.Fatalf("old Control frame admitted using new owner credential: %s", auth)
	case <-time.After(300 * time.Millisecond):
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("old reader did not drain")
	}
}
