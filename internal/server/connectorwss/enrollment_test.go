package connectorwss

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
)

type pausedAuth struct {
	Authenticator
	pauseAt          int32
	calls            atomic.Int32
	reached, release chan struct{}
}

func (a *pausedAuth) Authenticate(ctx context.Context, c connector.Credential) (connector.Principal, error) {
	p, err := a.Authenticator.Authenticate(ctx, c)
	if a.calls.Add(1) == a.pauseAt {
		close(a.reached)
		<-a.release
	}
	return p, err
}

// Force a revoke after a successful database authentication but before its
// result reaches the transport, both before and after Registry registration.
func TestControlRevokeRacesAuthenticationAndRegistration(t *testing.T) {
	for _, pauseAt := range []int32{1, 2} {
		t.Run(map[int32]string{1: "before registration", 2: "after registration"}[pauseAt], func(t *testing.T) {
			var paused *pausedAuth
			h := newHarness(t, func(o *Options) {
				paused = &pausedAuth{Authenticator: o.Auth, pauseAt: pauseAt, reached: make(chan struct{}), release: make(chan struct{})}
				o.Auth = paused
			})
			// Dial before first authentication completes; never call testing.Fatal in the goroutine.
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				<-paused.reached
				h.auth.failAll(connector.ErrUnauthenticated)
				h.registry.RevokeConnector(h.principal.ConnectorID)
				close(paused.release)
			}()
			conn := h.connect()
			<-finished
			_, closed := readUntilClose(t, conn)
			if closed.Code != closeCredentialRevoked {
				t.Fatalf("close=%d want4001", closed.Code)
			}
			waitFor(t, "Registry release", func() bool { _, ok := h.registry.Current(h.principal.ConnectorID); return !ok })
		})
	}
}

type recordedHello struct {
	principal        connector.Principal
	version, runtime string
}
type helloRecorder struct {
	records chan recordedHello
	err     error
}

func (h *helloRecorder) RecordHello(_ context.Context, p connector.Principal, v, r string) error {
	h.records <- recordedHello{p, v, r}
	return h.err
}

func TestHelloMetadataUsesAuthenticatedCurrentConnection(t *testing.T) {
	records := &helloRecorder{records: make(chan recordedHello, 4)}
	h := newHarness(t, func(o *Options) { o.Hellos = records })
	conn := h.connect()
	hello := validHello()
	hello["connectorId"] = "untrusted-claim"
	payloadOf(hello)["connectorVersion"] = "observed-version"
	payloadOf(hello)["runtimeId"] = "observed-runtime"
	sendJSON(t, conn, hello)
	if ack := readJSON(t, conn); ack["type"] != "HELLO_ACK" {
		t.Fatal("no HELLO_ACK")
	}
	record := <-records.records
	if record.principal != h.principal || record.version != "observed-version" || record.runtime != "observed-runtime" {
		t.Fatal("HELLO metadata lost authenticated identity")
	}
	// A replaced connection cannot persist another HELLO or heartbeat.
	second := h.connect()
	sendJSON(t, second, validHello())
	if ack := readJSON(t, second); ack["type"] != "HELLO_ACK" {
		t.Fatal("replacement not ready")
	}
	<-records.records
	_, closed := readUntilClose(t, conn)
	if closed.Code != 4002 {
		t.Fatal("old connection not replaced")
	}
	if len(records.records) != 0 {
		t.Fatal("stale HELLO persisted")
	}
}

func TestRevokedHelloCannotBecomeReady(t *testing.T) {
	records := &helloRecorder{records: make(chan recordedHello, 1), err: connector.ErrUnauthenticated}
	h := newHarness(t, func(o *Options) { o.Hellos = records })
	conn := h.connect()
	sendJSON(t, conn, validHello())
	frames, closed := readUntilClose(t, conn)
	if closed.Code != closeCredentialRevoked || len(frames) != 0 {
		t.Fatal("revoked HELLO acknowledged")
	}
	if err := h.registry.WithReadyRoute(h.principal.ConnectorID, func(connector.Session, connector.Route) error { return nil }); err == nil {
		t.Fatal("revoked route ready")
	}
}
