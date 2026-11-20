package connectorwss

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
)

func TestReleaseWaitsForRequestedCloseFrameAndSealsLateRequests(t *testing.T) {
	fake := &fakeWS{controlEntered: make(chan struct{}), controlGate: make(chan struct{})}
	cc := newControlConn(fake)
	cc.closeFor(connector.CloseRevoked)
	<-fake.controlEntered
	released := make(chan struct{})
	go func() { cc.release(); close(released) }()
	requireStillRunning(t, released, "release during close-frame write")
	close(fake.controlGate)
	<-released
	cc.closeFor(connector.CloseReplaced)
	cc.close(websocket.CloseGoingAway, "late shutdown")
	requireEvents(t, fake, wsEvent{"control", closeCredentialRevoked})
}

func TestConcurrentShutdownAndRetireShareOneCloseAttempt(t *testing.T) {
	fake := &fakeWS{}
	cc := newControlConn(fake)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() { cc.closeFor(connector.CloseRevoked) })
		wg.Go(func() { cc.close(websocket.CloseGoingAway, "shutdown") })
	}
	wg.Wait()
	cc.release()
	events := fake.recorded()
	if len(events) != 1 || events[0].kind != "control" {
		t.Fatalf("close attempts = %+v", events)
	}
	if events[0].code != closeCredentialRevoked && events[0].code != websocket.CloseGoingAway {
		t.Fatalf("unexpected reason: %+v", events)
	}
}

// The existing OwnerLocator port gates the real transport after HELLO_ACK and
// before MarkReady. No sleeps or readiness changes hide that retirement window.
type gatedCloseRegistrar struct {
	fakeRegistrar
	once             sync.Once
	entered, proceed chan struct{}
}

func (g *gatedCloseRegistrar) RegisterConnector(ctx context.Context, id uuid.UUID, epoch int64, ttl time.Duration, onLost ...func()) (int64, error) {
	g.once.Do(func() { close(g.entered); <-g.proceed })
	return g.fakeRegistrar.RegisterConnector(ctx, id, epoch, ttl, onLost...)
}

func TestRetirementAfterAckBeforeReadyPreservesCloseCodeAndNewSession(t *testing.T) {
	for _, replace := range []bool{false, true} {
		name := "revoke"
		if replace {
			name = "replace"
		}
		t.Run(name, func(t *testing.T) {
			g := &gatedCloseRegistrar{entered: make(chan struct{}), proceed: make(chan struct{})}
			var unblock sync.Once
			t.Cleanup(func() { unblock.Do(func() { close(g.proceed) }) })
			h := newHarness(t, func(opts *Options) { opts.OwnerLocator = g })
			first := h.establish()
			<-g.entered
			old := h.waitRegistered()
			want := closeCredentialRevoked
			var second *peer
			if replace {
				// Upgrade replaces the old registration before the new HELLO.
				second = h.upgradeWith(testCredential)
				want = closeReplaced
			} else if !h.registry.RevokeConnector(h.principal.ConnectorID) {
				t.Fatal("nothing revoked")
			}
			unblock.Do(func() { close(g.proceed) })
			if got := first.waitClosed(5 * time.Second); got.Code != want {
				t.Fatalf("close=%d want=%d", got.Code, want)
			}
			if replace {
				second.hello()
				waitFor(t, "new session ready", func() bool {
					current, ok := h.registry.Current(h.principal.ConnectorID)
					return ok && current.ID != old.ID && h.registry.WithReadyRoute(current.ConnectorID, func(connector.Session, connector.Route) error { return nil }) == nil
				})
				if second.closedNow() {
					t.Fatal("old cleanup closed new connection")
				}
			} else {
				registryReleased(t, h)
			}
		})
	}
}
