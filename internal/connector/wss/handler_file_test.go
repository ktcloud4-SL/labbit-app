package wss

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/filetransport"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/workspacefile"
)

type fileControlSender struct {
	credential string
	frames     chan []byte
}

func (s *fileControlSender) DialCredential() string { return s.credential }
func (s *fileControlSender) SendMessage(_ context.Context, msg any) error {
	b, e := json.Marshal(msg)
	if e == nil {
		s.frames <- b
	}
	return e
}

type handlerFiles struct{}

func (handlerFiles) Tree(context.Context, vmaccess.Target, string) ([]workspacefile.Entry, error) {
	return nil, nil
}
func (handlerFiles) Read(context.Context, vmaccess.Target, string, int64) (workspacefile.File, error) {
	return workspacefile.File{Content: []byte("private-body"), Revision: "revision"}, nil
}
func (handlerFiles) Save(context.Context, vmaccess.Target, string, string, []byte) (string, error) {
	return "revision", nil
}

func TestFileOpenCapturesControlSenderAndActualDialCredential(t *testing.T) {
	gate := make(chan struct{})
	attached := make(chan struct{}, 1)
	done := make(chan struct{}, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { done <- struct{}{} }()
		if r.Header.Get("Authorization") != "Bearer old-credential" {
			t.Error("File Data used a rotated/different Control credential")
			http.Error(w, "unauthorized", 401)
			return
		}
		up := websocket.Upgrader{Subprotocols: []string{protocol.SubprotocolFileData}}
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		ws.SetReadDeadline(time.Now().Add(2 * time.Second))
		var attach protocol.FileDataAttachMessage
		if ws.ReadJSON(&attach) != nil {
			return
		}
		attached <- struct{}{}
		<-gate
		base := protocol.BaseEnvelope{Type: protocol.MessageTypeFileDataAttached, MessageID: "attached", SentAt: time.Now().UTC(), ReplyToMessageID: attach.MessageID, FileRequestID: "file-1", LabInstanceID: "lab", Generation: 1}
		if ws.WriteJSON(struct {
			protocol.BaseEnvelope
			Payload any `json:"payload"`
		}{base, struct{}{}}) != nil {
			return
		}
		base.Type, base.MessageID, base.ReplyToMessageID = protocol.MessageTypeFileRead, "read", ""
		if ws.WriteJSON(struct {
			protocol.BaseEnvelope
			Payload any `json:"payload"`
		}{base, map[string]any{"path": "private-file", "maxBytes": 64}}) != nil {
			return
		}
		kind, _, err := ws.ReadMessage()
		if err != nil || kind != websocket.TextMessage {
			return
		}
		kind, b, err := ws.ReadMessage()
		if err != nil || kind != websocket.BinaryMessage || string(b) != "private-body" {
			t.Error("File body changed")
		}
		ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(1000, "done"), time.Now().Add(time.Second))
	}))
	defer server.Close()
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	m, err := filetransport.New(handlerFiles{}, filetransport.Config{EndpointURL: "wss" + strings.TrimPrefix(server.URL, "https"), RuntimeID: "runtime", Dialer: &websocket.Dialer{TLSClientConfig: &tls.Config{RootCAs: pool}}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Shutdown(context.Background())
	old := &fileControlSender{credential: "old-credential", frames: make(chan []byte, 4)}
	newSender := &fileControlSender{credential: "rotated-credential", frames: make(chan []byte, 4)}
	h := NewHandler(nil, old)
	h.SetFileManager(m, "fallback-credential")
	open := protocol.FileOpenMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeFileOpen, MessageID: "open", SentAt: time.Now().UTC(), FileRequestID: "file-1", LabInstanceID: "lab", Generation: 1}, Payload: protocol.FileOpenPayload{Operation: "READ", TargetVMKey: "workspace", ProviderServerID: "server"}}
	raw, _ := json.Marshal(open)
	if h.HandleMessage(context.Background(), raw) != nil {
		t.Fatal("valid OPEN rejected")
	}
	select {
	case <-attached:
	case <-time.After(2 * time.Second):
		t.Fatal("async File dial did not start")
	}
	close(gate)
	select {
	case b := <-old.frames:
		var result protocol.FileOpenResultMessage
		if json.Unmarshal(b, &result) != nil || result.Payload.Outcome != "SUCCEEDED" || result.ReplyToMessageID != "open" {
			t.Fatal("OPEN_RESULT correlation changed")
		}
		if bytes.Contains(b, []byte("private-file")) || bytes.Contains(b, []byte("private-body")) || bytes.Contains(b, []byte("credential")) {
			t.Fatal("File content/credential leaked into Control")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("old sender did not receive bound result")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Data exchange did not complete")
	}
	if len(newSender.frames) != 0 {
		t.Fatal("old request response reached new Control")
	}
	h.SetSender(newSender)
}

func TestInvalidFileControlAndUnconfiguredCapabilityFailSafely(t *testing.T) {
	sender := &fileControlSender{credential: "credential", frames: make(chan []byte, 8)}
	h := NewHandler(nil, sender)
	valid := map[string]any{"type": "FILE_OPEN", "messageId": "open", "sentAt": time.Now().UTC().Format(time.RFC3339Nano), "fileRequestId": "file", "labInstanceId": "lab", "generation": json.RawMessage("1.0"), "payload": map[string]any{"operation": "READ", "targetVmKey": "workspace", "providerServerId": "server"}}
	raw, _ := json.Marshal(valid)
	if h.HandleMessage(context.Background(), raw) != nil {
		t.Fatal("Schema integer rejected")
	}
	var result protocol.FileOpenResultMessage
	if json.Unmarshal(<-sender.frames, &result) != nil || result.Payload.Outcome != "FAILED" || result.Payload.Error.Code != "UNAVAILABLE" {
		t.Fatal("unconfigured File runtime did not fail safely")
	}
	for _, edit := range []func(map[string]any){func(v map[string]any) { v["FileRequestId"] = v["fileRequestId"]; delete(v, "fileRequestId") }, func(v map[string]any) { v["generation"] = json.RawMessage("1e999999999") }, func(v map[string]any) { v["sentAt"] = nil }, func(v map[string]any) { v["payload"] = map[string]any{"operation": "DELETE"} }} {
		var changed map[string]any
		json.Unmarshal(raw, &changed)
		edit(changed)
		data, _ := json.Marshal(changed)
		if h.HandleMessage(context.Background(), data) == nil {
			t.Fatal("invalid File Control accepted")
		}
		if len(sender.frames) != 0 {
			t.Fatal("invalid correlation got a fallback response")
		}
	}
}
