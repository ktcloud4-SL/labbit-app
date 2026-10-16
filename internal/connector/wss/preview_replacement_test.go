package wss

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/preview"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
)

func TestPreviewDirectControlReplacementRetiresOldIdleSession(t *testing.T) {
	gateway := mock.NewPreviewGateway()
	defer gateway.Close()
	a, b := net.Pipe()
	defer b.Close()
	manager := preview.NewSessionManager(previewDialFunc(func(context.Context, vmaccess.Target, int) (net.Conn, error) { return a, nil }), nil)
	defer manager.Shutdown("cleanup")
	h := NewHandler(&provider.MockProvider{}, previewOwnerSender{"old"})
	h.SetPreviewManager(manager, preview.DataWSSClientConfig{EndpointURL: gateway.URL(), RuntimeID: "runtime", AllowInsecure: true})
	oldEpoch := h.fileOwnerEpoch
	raw, _ := json.Marshal(protocol.PreviewOpenMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypePreviewOpen, MessageID: "M1", SentAt: time.Now(), PreviewSessionID: "S", LabInstanceID: "lab", Generation: 1}, Payload: protocol.PreviewOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server", TargetPort: 3000}})
	if err := h.HandleMessage(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	session, ok := manager.GetSession("S")
	if !ok {
		t.Fatal("session missing")
	}
	if err := gateway.SendClose(websocket.CloseNormalClosure, ""); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for session.HasActiveTunnel() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if session.HasActiveTunnel() || !session.EverAttached() {
		t.Fatal("fixture did not reach attached-then-idle state")
	}
	// A new active authenticated connection directly replaces the old one;
	// this is not a plain EOF -> nil -> reconnect transition.
	h.SetSender(previewOwnerSender{"new"})
	h.onControlDisconnected(oldEpoch, &websocket.CloseError{Code: protocol.CloseSessionReplaced})
	if manager.Count() != 0 || !session.IsDestroyed() {
		t.Fatalf("old idle logical session survived explicit Control replacement: count=%d destroyed=%v", manager.Count(), session.IsDestroyed())
	}
	if h.previewOwner.Err() != nil {
		t.Fatal("new owner canceled")
	}
}
