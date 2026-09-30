//go:build integration

package postgres_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
)

// lastSeenAt은 connectors.last_seen_at을 읽는다. 기록된 적이 없으면 nil이다.
func lastSeenAt(t *testing.T, db *testDB, connectorID uuid.UUID) *time.Time {
	t.Helper()
	var seen *time.Time
	if err := db.pool.QueryRow(t.Context(), `SELECT last_seen_at FROM connectors WHERE id = $1`, connectorID).Scan(&seen); err != nil {
		t.Fatalf("last_seen_at 조회: %v", err)
	}
	return seen
}

func requireLastSeenAt(t *testing.T, db *testDB, connectorID uuid.UUID, want *time.Time) {
	t.Helper()
	got := lastSeenAt(t, db, connectorID)
	switch {
	case want == nil && got != nil:
		t.Fatalf("last_seen_at = %v, want NULL", *got)
	case want != nil && (got == nil || !got.Equal(*want)):
		t.Fatalf("last_seen_at = %v, want %v", got, *want)
	}
}

func ptr[T any](v T) *T { return &v }

// heartbeat 기록은 Credential이 그 Connector의 것이고 둘 다 revoke되지 않았을 때만 last_seen_at을 갱신한다.
func TestRecordConnectorHeartbeat(t *testing.T) {
	db := newTestDB(t)
	org := bootstrapSample(t, db, 1).OrganizationID
	revokedAt := at(9)

	activeConnector, activeCredential := uuid.New(), uuid.New()
	otherConnector, otherCredential := uuid.New(), uuid.New()
	revokedCredConnector, revokedCredential := uuid.New(), uuid.New()
	revokedConnector, credentialOfRevokedConnector := uuid.New(), uuid.New()
	seedConnector(t, db, org, activeConnector, activeCredential, "test-hb-active", nil, nil)
	seedConnector(t, db, org, otherConnector, otherCredential, "test-hb-other", nil, nil)
	seedConnector(t, db, org, revokedCredConnector, revokedCredential, "test-hb-revoked-credential", nil, &revokedAt)
	seedConnector(t, db, org, revokedConnector, credentialOfRevokedConnector, "test-hb-revoked-connector", &revokedAt, nil)

	record := func(connectorID, credentialID uuid.UUID, seenAt time.Time) bool {
		t.Helper()
		recorded, err := db.store.RecordConnectorHeartbeat(t.Context(), connectorID, credentialID, seenAt)
		if err != nil {
			t.Fatalf("RecordConnectorHeartbeat() error = %v", err)
		}
		return recorded
	}

	t.Run("active Credential은 전달한 서버 시각으로 last_seen_at을 갱신", func(t *testing.T) {
		requireLastSeenAt(t, db, activeConnector, nil)
		if !record(activeConnector, activeCredential, at(10)) {
			t.Fatal("RecordConnectorHeartbeat() = false, want true")
		}
		requireLastSeenAt(t, db, activeConnector, ptr(at(10)))

		if !record(activeConnector, activeCredential, at(11)) {
			t.Fatal("두 번째 RecordConnectorHeartbeat() = false, want true")
		}
		requireLastSeenAt(t, db, activeConnector, ptr(at(11)))
	})

	t.Run("다른 Connector의 last_seen_at은 바뀌지 않음", func(t *testing.T) {
		requireLastSeenAt(t, db, otherConnector, nil)
	})

	t.Run("이번 기록은 last_seen_at 외의 metadata를 건드리지 않음", func(t *testing.T) {
		if n := countRows(t, db, `SELECT count(*) FROM connectors WHERE id = $1 AND last_runtime_id IS NULL AND last_connector_version IS NULL AND revoked_at IS NULL`, activeConnector); n != 1 {
			t.Fatal("last_runtime_id, last_connector_version 또는 revoked_at이 바뀜")
		}
		if n := countRows(t, db, `SELECT count(*) FROM connector_credentials WHERE id = $1 AND last_used_at IS NULL AND revoked_at IS NULL`, activeCredential); n != 1 {
			t.Fatal("connector_credentials의 last_used_at 또는 revoked_at이 바뀜")
		}
	})

	t.Run("revoke된 Credential은 갱신하지 않음", func(t *testing.T) {
		if record(revokedCredConnector, revokedCredential, at(12)) {
			t.Fatal("revoke된 Credential의 heartbeat가 기록됨")
		}
		requireLastSeenAt(t, db, revokedCredConnector, nil)
	})

	t.Run("revoke된 Connector는 갱신하지 않음", func(t *testing.T) {
		if record(revokedConnector, credentialOfRevokedConnector, at(12)) {
			t.Fatal("revoke된 Connector의 heartbeat가 기록됨")
		}
		requireLastSeenAt(t, db, revokedConnector, nil)
	})

	t.Run("다른 Connector의 Credential 조합은 갱신하지 않음", func(t *testing.T) {
		before := *lastSeenAt(t, db, activeConnector)
		if record(activeConnector, otherCredential, at(13)) {
			t.Fatal("다른 Connector의 Credential로 heartbeat가 기록됨")
		}
		if record(otherConnector, activeCredential, at(13)) {
			t.Fatal("다른 Connector의 Credential로 heartbeat가 기록됨")
		}
		requireLastSeenAt(t, db, activeConnector, &before)
		requireLastSeenAt(t, db, otherConnector, nil)
	})

	t.Run("없는 Connector나 Credential은 오류 없이 false", func(t *testing.T) {
		if record(uuid.New(), activeCredential, at(13)) || record(activeConnector, uuid.New(), at(13)) || record(uuid.New(), uuid.New(), at(13)) {
			t.Fatal("존재하지 않는 조합의 heartbeat가 기록됨")
		}
	})

	t.Run("revoke 이후에는 같은 조합도 갱신하지 않고 마지막 값을 유지", func(t *testing.T) {
		before := *lastSeenAt(t, db, activeConnector)
		mustExec(t, db, `UPDATE connector_credentials SET revoked_at = now() WHERE id = $1`, activeCredential)
		for i := range 5 {
			if record(activeConnector, activeCredential, at(14+i)) {
				t.Fatal("revoke된 Credential의 heartbeat가 기록됨")
			}
		}
		requireLastSeenAt(t, db, activeConnector, &before)

		// Connector revoke도 마찬가지다.
		mustExec(t, db, `UPDATE connector_credentials SET revoked_at = NULL WHERE id = $1`, activeCredential)
		if !record(activeConnector, activeCredential, at(20)) {
			t.Fatal("Credential revoke를 되돌린 뒤 heartbeat가 기록되지 않음")
		}
		mustExec(t, db, `UPDATE connectors SET revoked_at = now() WHERE id = $1`, activeConnector)
		if record(activeConnector, activeCredential, at(21)) {
			t.Fatal("revoke된 Connector의 heartbeat가 기록됨")
		}
		requireLastSeenAt(t, db, activeConnector, ptr(at(20)))
	})
}

// Connector row를 revoke하는 transaction이 아직 commit되지 않은 동안 시작한 heartbeat 기록은 그 row lock을 기다리고,
// commit 뒤에는 조건을 다시 평가해 last_seen_at을 갱신하지 않는다(READ COMMITTED).
func TestRecordConnectorHeartbeatDoesNotWriteAfterConcurrentConnectorRevoke(t *testing.T) {
	db := newTestDB(t)
	org := bootstrapSample(t, db, 1).OrganizationID
	connectorID, credentialID := uuid.New(), uuid.New()
	seedConnector(t, db, org, connectorID, credentialID, "test-hb-race", nil, nil)

	tx, err := db.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), `UPDATE connectors SET revoked_at = now() WHERE id = $1`, connectorID); err != nil {
		t.Fatal(err)
	}

	type result struct {
		recorded bool
		err      error
	}
	done := make(chan result, 1)
	go func() {
		recorded, err := db.store.RecordConnectorHeartbeat(t.Context(), connectorID, credentialID, at(10))
		done <- result{recorded, err}
	}()

	// revoke가 commit되기 전에는 heartbeat 기록이 row lock에서 기다린다.
	select {
	case got := <-done:
		t.Fatalf("revoke transaction이 열려 있는데 heartbeat 기록이 끝남: %+v", got)
	case <-time.After(300 * time.Millisecond):
	}

	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil || got.recorded {
			t.Fatalf("revoke commit 뒤 heartbeat 기록 = %+v, want (false, nil)", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("revoke commit 뒤에도 heartbeat 기록이 끝나지 않음")
	}
	requireLastSeenAt(t, db, connectorID, nil)
}

// Application(connector.Service)까지 포함해 실제 PostgreSQL 데이터에서 heartbeat 기록 가능/불가 조건을 확인한다.
func TestConnectorServiceRecordsHeartbeatAgainstPostgreSQL(t *testing.T) {
	db := newTestDB(t)
	org := bootstrapSample(t, db, 1).OrganizationID
	connectorID, credentialID := uuid.New(), uuid.New()
	seedConnector(t, db, org, connectorID, credentialID, "test-svc-hb", nil, nil)
	principal := connector.Principal{ConnectorID: connectorID, OrganizationID: org, CredentialID: credentialID}
	service := connector.NewService(db.store)

	if err := service.RecordHeartbeat(t.Context(), principal, at(10)); err != nil {
		t.Fatalf("RecordHeartbeat() error = %v", err)
	}
	requireLastSeenAt(t, db, connectorID, ptr(at(10)))

	// 인증 뒤에 revoke되면 ErrUnauthenticated이고 last_seen_at은 그대로다.
	mustExec(t, db, `UPDATE connector_credentials SET revoked_at = now() WHERE id = $1`, credentialID)
	if err := service.RecordHeartbeat(t.Context(), principal, at(11)); !errors.Is(err, connector.ErrUnauthenticated) {
		t.Fatalf("revoke 뒤 RecordHeartbeat() error = %v, want ErrUnauthenticated", err)
	}
	requireLastSeenAt(t, db, connectorID, ptr(at(10)))
}
