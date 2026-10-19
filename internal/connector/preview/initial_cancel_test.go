package preview

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
)

func TestInitialCancellationDiscardsSessionAfterConcurrentClose(t *testing.T) {
	tcp, peer := net.Pipe()
	defer peer.Close()
	mgr := NewSessionManager(lifecycleForwarder(func(context.Context, vmaccess.Target, int) (net.Conn, error) {
		return tcp, nil
	}), nil)
	defer mgr.Shutdown("cleanup")
	a, err := mgr.PrepareOpen(context.Background(),
		protocol.PreviewOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server", TargetPort: 3000},
		protocol.BaseEnvelope{MessageID: "OPEN", PreviewSessionID: "S", LabInstanceID: "lab", Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Open(context.Background()); err != nil {
		t.Fatal(err)
	}

	callbackEntered, releaseCallback := make(chan struct{}), make(chan struct{})
	firstDone, cleanupDone := make(chan struct{}), make(chan struct{})
	original := a.tunnel.onClosed
	a.tunnel.onClosed = func(reason string, err error) {
		close(callbackEntered)
		<-releaseCallback
		original(reason, err)
	}
	go func() {
		defer close(firstDone)
		a.tunnel.Close(protocol.PreviewReasonSessionClosed)
	}()
	<-callbackEntered
	go func() {
		defer close(cleanupDone)
		mgr.CloseAttempt(a.session, "OPEN", protocol.PreviewReasonOpenFailed)
	}()
	// Keep state publication behind the attach-error cleanup. A correct Close
	// joins that publication rather than returning before the callback.
	select {
	case <-cleanupDone:
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseCallback)
	for _, done := range []<-chan struct{}{firstDone, cleanupDone} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("concurrent cancellation did not finish")
		}
	}
	if mgr.Count() != 0 {
		t.Fatalf("initial canceled session retained: count=%d active=%v attached=%v",
			mgr.Count(), a.session.HasActiveTunnel(), a.session.EverAttached())
	}
}
