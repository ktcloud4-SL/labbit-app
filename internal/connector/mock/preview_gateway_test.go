package mock

import (
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

func TestPreviewGatewayConcurrentFrames(t *testing.T) {
	gateway := NewPreviewGateway()
	defer gateway.Close()
	dialer := websocket.Dialer{Subprotocols: []string{protocol.SubprotocolPreviewData}}
	conn, _, err := dialer.Dial(gateway.URL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	attach := protocol.PreviewAttachMessage{BaseEnvelope: protocol.BaseEnvelope{
		Type: protocol.MessageTypePreviewAttach, MessageID: "attach", ReplyToMessageID: "open",
		SentAt: time.Now().UTC(), PreviewSessionID: "preview", LabInstanceID: "lab", Generation: 1,
	}}
	if err := conn.WriteJSON(attach); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	const writers = 32
	start := make(chan struct{})
	errors := make(chan error, writers)
	var done sync.WaitGroup
	for i := range writers {
		done.Go(func() {
			<-start
			if i%2 == 0 {
				errors <- gateway.SendBinary([]byte("fixture-binary"))
			} else {
				errors <- gateway.SendText([]byte("fixture-text"))
			}
		})
	}
	close(start)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	counts := map[int]int{}
	for range writers {
		kind, _, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		counts[kind]++
	}
	done.Wait()
	for range writers {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	if counts[websocket.BinaryMessage] != writers/2 || counts[websocket.TextMessage] != writers/2 {
		t.Fatal("concurrent fixture frames were lost or corrupted")
	}
}
