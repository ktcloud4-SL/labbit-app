package terminal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
	"github.com/ktcloud4-SL/labbit-app/internal/server/valkeyowner"
)

type recordingRelay struct {
	expected []realtime.Expected
	forgot   []string
}

func (r *recordingRelay) Expect(e realtime.Expected) error {
	r.expected = append(r.expected, e)
	return nil
}

func (r *recordingRelay) Activate(string, time.Time) error {
	return nil
}

func (r *recordingRelay) Forget(id string) {
	r.forgot = append(r.forgot, id)
}

func (r *recordingRelay) Terminate(string, realtime.End) {}

type recordingConnectors struct {
	sentOpen   []connector.TerminalOpen
	forgotOpen []string
}

func (c *recordingConnectors) SendTerminalOpen(_ context.Context, open connector.TerminalOpen) (connector.SentMessage, error) {
	c.sentOpen = append(c.sentOpen, open)
	return connector.SentMessage{}, nil
}

func (c *recordingConnectors) SendTerminalClose(context.Context, connector.TerminalClose) (connector.SentMessage, error) {
	return connector.SentMessage{}, nil
}

func (c *recordingConnectors) ForgetTerminalOpen(_ uuid.UUID, sessionID string) bool {
	c.forgotOpen = append(c.forgotOpen, sessionID)
	return true
}

type failingOwnerLocator struct {
	registerErr error
	registered  []string
	released    []string
}

func (f *failingOwnerLocator) RegisterTerminal(_ context.Context, sessionID string, _ uuid.UUID, _ int64, _ time.Duration) error {
	f.registered = append(f.registered, sessionID)
	return f.registerErr
}

func (f *failingOwnerLocator) ReleaseTerminal(_ context.Context, sessionID string) error {
	f.released = append(f.released, sessionID)
	return nil
}

func (f *failingOwnerLocator) LookupConnector(context.Context, uuid.UUID) (*valkeyowner.ConnectorOwnerRecord, error) {
	return nil, nil
}

func (f *failingOwnerLocator) PodID() string {
	return "pod-unit-test"
}

func TestCreateFailsClosedOnTerminalOwnerRegistrationFailure(t *testing.T) {
	store := newStore()
	store.servers["vk-web"] = []repository.ProviderServer{
		{ID: uuid.New(), ProviderID: "srv-web-0", LifecycleStatus: ProviderResourcePresent},
	}

	relay := &recordingRelay{}
	connectors := &recordingConnectors{}
	locator := &failingOwnerLocator{
		registerErr: valkeyowner.ErrLocatorUnavailable,
	}

	svc, err := NewService(Options{
		Store:        store,
		Auth:         unusedAuth{},
		Connectors:   connectors,
		Relay:        relay,
		OwnerLocator: locator,
		Clock:        realtime.SystemClock{},
	})
	if err != nil {
		t.Fatalf("NewService err = %v", err)
	}

	created, err := svc.Create(t.Context(), owner, CreateInput{
		LabInstanceID: labID.String(),
		TargetVMKey:   "vk-web",
		Cols:          []byte("80"),
		Rows:          []byte("24"),
		RequestID:     "req-fail-locator",
	})

	// 1. Must fail-closed with ErrUnavailable
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("svc.Create err = %v, want ErrUnavailable", err)
	}
	// 2. No session or token returned (no false-success 201 data)
	if created.ID != uuid.Nil || len(created.Token) != 0 {
		t.Fatalf("svc.Create returned non-zero Created: %+v", created)
	}

	// 3. TERMINAL_OPEN command must NEVER have been sent to Connector
	if len(connectors.sentOpen) != 0 {
		t.Fatalf("SendTerminalOpen was called %d times; expected 0", len(connectors.sentOpen))
	}

	// 4. Relay correlation was cleaned via abort path
	if len(relay.expected) != 1 {
		t.Fatalf("expected 1 relay registration, got %d", len(relay.expected))
	}
	if len(relay.forgot) != 1 || relay.forgot[0] != relay.expected[0].TerminalSessionID {
		t.Fatalf("relay.Forget was not called for registered session: expected %v, got %v", relay.expected, relay.forgot)
	}

	// 5. Connectors pending was cleaned via abort path
	if len(connectors.forgotOpen) != 1 || connectors.forgotOpen[0] != relay.expected[0].TerminalSessionID {
		t.Fatalf("ForgetTerminalOpen was not called: got %v", connectors.forgotOpen)
	}

	// 6. DB session record was transitioned to ENDED (EndReasonOpenFailed)
	if !store.called("CreateTerminalSession") {
		t.Fatalf("CreateTerminalSession was not called in store")
	}
	if !store.called("EndTerminalSession") {
		t.Fatalf("EndTerminalSession was not called to mark session failed in store")
	}

	// 7. Locator release was called exactly once in abortCreate (no duplicate cleanup)
	if len(locator.released) != 1 {
		t.Fatalf("ownerLocator.ReleaseTerminal call count = %d, want exactly 1", len(locator.released))
	}
}
