package connector

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// The fence is checked inside WithReadyRoute, not in a separate readiness
// precheck. A replacement must receive neither mutation nor reconcile traffic.
func TestRouterPinnedSessionRejectsReplacement(t *testing.T) {
	f := newRouterFixture(t)
	principal := Principal{ConnectorID: uuid.New(), CredentialID: uuid.New()}
	old, oldFrames := f.connect(principal)
	cmd := commandFor(principal.ConnectorID, corr("op-pinned", "lab-pinned", 1))
	cmd.SessionID = old.Session().ID
	replacement, replacementFrames := f.connect(principal)
	if _, err := f.router.SendOperationCommand(context.Background(), cmd); !errors.Is(err, ErrNotReady) {
		t.Fatalf("stale session: %v", err)
	}
	if _, err := f.router.SendReconcileRequest(context.Background(), ReconcileRequest{ConnectorID: principal.ConnectorID, SessionID: old.Session().ID, Correlation: cmd.Correlation}); !errors.Is(err, ErrNotReady) {
		t.Fatalf("stale reconcile: %v", err)
	}
	if oldFrames.count() != 0 || replacementFrames.count() != 0 {
		t.Fatal("stale command was sent")
	}
	if ops, recs := f.router.PendingCount(principal.ConnectorID); ops != 0 || recs != 0 {
		t.Fatal("stale pending was registered")
	}
	cmd.SessionID = replacement.Session().ID
	if _, err := f.router.SendOperationCommand(context.Background(), cmd); err != nil {
		t.Fatal(err)
	}
	if replacementFrames.count() != 1 {
		t.Fatal("exact current session not used")
	}
}
