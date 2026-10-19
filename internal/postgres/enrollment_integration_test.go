//go:build integration

package postgres_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

func enrollmentAdmin(s sample) repository.User {
	return repository.User{ID: s.AdminID, OrganizationID: s.OrganizationID, OrganizationRole: repository.OrganizationRoleAdmin}
}

func TestEnrollmentConcurrentConsumptionAndSecrets(t *testing.T) {
	db := newTestDB(t)
	user := enrollmentAdmin(bootstrapSample(t, db, 1))
	svc := connector.NewEnrollmentService(db.store, connector.NewRegistry(), nil)
	token, err := svc.Issue(t.Context(), user, "Linux connector")
	if err != nil {
		t.Fatal(err)
	}
	const requests = 16
	start := make(chan struct{})
	results := make(chan connector.EnrollmentCreated, requests)
	errs := make(chan error, requests)
	var wg sync.WaitGroup
	for range requests {
		wg.Go(func() {
			<-start
			out, err := svc.Enroll(context.Background(), token.Token)
			results <- out
			errs <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		} else if !errors.Is(err, connector.ErrInvalidEnrollmentToken) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("successes=%d", successes)
	}
	var created connector.EnrollmentCreated
	for result := range results {
		if result.ConnectorID != uuid.Nil {
			created = result
		} else if result.Credential != "" {
			t.Fatal("failed enrollment returned Credential")
		}
	}
	for _, table := range []string{"connectors", "connector_credentials"} {
		var n int
		if err := db.pool.QueryRow(t.Context(), `SELECT count(*) FROM `+table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s count=%d err=%v", table, n, err)
		}
	}
	var consumed bool
	if err := db.pool.QueryRow(t.Context(), `SELECT consumed_at IS NOT NULL AND connector_id=$2 FROM connector_enrollment_tokens WHERE id=$1`, token.ID, created.ConnectorID).Scan(&consumed); err != nil || !consumed {
		t.Fatal("consumption not linked")
	}
	for _, table := range []string{"connector_enrollment_tokens", "connectors", "connector_credentials"} {
		var json string
		if err := db.pool.QueryRow(t.Context(), `SELECT json_agg(t)::text FROM `+table+` t`).Scan(&json); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(json, string(token.Token)) || strings.Contains(json, string(created.Credential)) {
			t.Fatalf("raw secret persisted in %s", table)
		}
	}
	var hash []byte
	if err := db.pool.QueryRow(t.Context(), `SELECT token_hash FROM connector_enrollment_tokens WHERE id=$1`, token.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte(token.Token))
	if string(hash) != string(want[:]) {
		t.Fatal("token digest mismatch")
	}
	principal, err := connector.NewService(db.store).Authenticate(t.Context(), created.Credential)
	if err != nil || principal.ConnectorID != created.ConnectorID {
		t.Fatal("issued Credential does not authenticate")
	}
	if _, err := svc.Enroll(t.Context(), token.Token); !errors.Is(err, connector.ErrInvalidEnrollmentToken) {
		t.Fatal("replay accepted")
	}
}

func TestEnrollmentRollbackLeavesNoPartialIdentity(t *testing.T) {
	for _, deferred := range []bool{false, true} {
		t.Run(map[bool]string{false: "credential insert", true: "commit failure"}[deferred], func(t *testing.T) {
			db := newTestDB(t)
			user := enrollmentAdmin(bootstrapSample(t, db, 1))
			svc := connector.NewEnrollmentService(db.store, connector.NewRegistry(), nil)
			token, err := svc.Issue(t.Context(), user, "rollback")
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.pool.Exec(t.Context(), `CREATE FUNCTION reject_test_credential() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture failure'; END $$`)
			if err != nil {
				t.Fatal(err)
			}
			sql := `CREATE TRIGGER reject_test_credential BEFORE INSERT ON connector_credentials FOR EACH ROW EXECUTE FUNCTION reject_test_credential()`
			if deferred {
				sql = `CREATE CONSTRAINT TRIGGER reject_test_credential AFTER INSERT ON connector_credentials DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_test_credential()`
			}
			if _, err := db.pool.Exec(t.Context(), sql); err != nil {
				t.Fatal(err)
			}
			out, err := svc.Enroll(t.Context(), token.Token)
			if err == nil || out.Credential != "" || out.ConnectorID != uuid.Nil {
				t.Fatal("failed transaction returned identity")
			}
			var count int
			if err := db.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM connectors)+(SELECT count(*) FROM connector_credentials)+(SELECT count(*) FROM connector_enrollment_tokens WHERE consumed_at IS NOT NULL OR connector_id IS NOT NULL)`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("partial persistence count=%d err=%v", count, err)
			}
			if _, err := db.pool.Exec(t.Context(), `DROP TRIGGER reject_test_credential ON connector_credentials`); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.Enroll(t.Context(), token.Token); err != nil {
				t.Fatal("rollback consumed token")
			}
		})
	}
}

func TestEnrollmentExpiryCancellationRecoveryAndOrganization(t *testing.T) {
	db := newTestDB(t)
	user := enrollmentAdmin(bootstrapSample(t, db, 1))
	other := enrollmentAdmin(bootstrapSample(t, db, 2))
	now := time.Now().UTC().Truncate(time.Microsecond)
	svc := connector.NewEnrollmentService(db.store, connector.NewRegistry(), func() time.Time { return now })
	token, err := svc.Issue(t.Context(), user, "lost response")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Cancel(t.Context(), other, token.ID.String()); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("cross-org cancel allowed")
	}
	out, err := svc.Enroll(t.Context(), token.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Cancel(t.Context(), user, token.ID.String()); !errors.Is(err, connector.ErrEnrollmentTokenConsumed) {
		t.Fatal("consumed token cancelled")
	}
	list, err := svc.List(t.Context(), user)
	if err != nil || len(list.Items) != 1 || list.Items[0].ConnectionStatus != "NEVER_SEEN" {
		t.Fatal("lost response recovery list unavailable")
	}
	foreign, err := svc.List(t.Context(), other)
	if err != nil || len(foreign.Items) != 0 {
		t.Fatal("cross-org list leak")
	}
	if err := svc.Revoke(t.Context(), other, out.ConnectorID.String()); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("cross-org revoke allowed")
	}
	if err := svc.Revoke(t.Context(), user, out.ConnectorID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := connector.NewService(db.store).Authenticate(t.Context(), out.Credential); !errors.Is(err, connector.ErrUnauthenticated) {
		t.Fatal("revoked Credential authenticated")
	}
	list, err = svc.List(t.Context(), user)
	if err != nil || list.Items[0].ConnectionStatus != "REVOKED" {
		t.Fatal("revoked status missing")
	}
	replacement, err := svc.Issue(t.Context(), user, "replacement")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Enroll(t.Context(), replacement.Token); err != nil {
		t.Fatal(err)
	}
	cancelled, err := svc.Issue(t.Context(), user, "cancelled")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Cancel(t.Context(), user, cancelled.ID.String()); err != nil {
		t.Fatal(err)
	}
	if err := svc.Cancel(t.Context(), user, cancelled.ID.String()); err != nil {
		t.Fatal("cancel not idempotent")
	}
	if _, err := svc.Enroll(t.Context(), cancelled.Token); !errors.Is(err, connector.ErrInvalidEnrollmentToken) {
		t.Fatal("cancelled token consumed")
	}
	expired, err := svc.Issue(t.Context(), user, "expired")
	if err != nil {
		t.Fatal(err)
	}
	now = expired.ExpiresAt
	if _, err := svc.Enroll(t.Context(), expired.Token); !errors.Is(err, connector.ErrInvalidEnrollmentToken) {
		t.Fatal("expiry boundary accepted")
	}
}

func TestEnrollmentCancelRacesConsumption(t *testing.T) {
	db := newTestDB(t)
	user := enrollmentAdmin(bootstrapSample(t, db, 1))
	svc := connector.NewEnrollmentService(db.store, connector.NewRegistry(), nil)
	for range 8 {
		token, err := svc.Issue(t.Context(), user, "race")
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		enroll := make(chan error, 1)
		cancel := make(chan error, 1)
		go func() { <-start; _, err := svc.Enroll(context.Background(), token.Token); enroll <- err }()
		go func() { <-start; cancel <- svc.Cancel(context.Background(), user, token.ID.String()) }()
		close(start)
		e, c := <-enroll, <-cancel
		if !((e == nil && errors.Is(c, connector.ErrEnrollmentTokenConsumed)) || (errors.Is(e, connector.ErrInvalidEnrollmentToken) && c == nil)) {
			t.Fatalf("unexpected race result: %v / %v", e, c)
		}
	}
}

func TestHelloMetadataWaitsForRevokeAndPreservesHeartbeat(t *testing.T) {
	db := newTestDB(t)
	org := bootstrapSample(t, db, 1).OrganizationID
	for _, credentialOnly := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("credentialOnly=%v/commit=%v", credentialOnly, commit), func(t *testing.T) {
				id, cred := uuid.New(), uuid.New()
				seedConnector(t, db, org, id, cred, uuid.NewString(), nil, nil)
				tx, err := db.pool.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(context.Background())
				table, column, value := "connectors", "id", id
				if credentialOnly {
					table = "connector_credentials"
					value = cred
				}
				if _, err := tx.Exec(t.Context(), `UPDATE `+table+` SET revoked_at=now() WHERE `+column+`=$1`, value); err != nil {
					t.Fatal(err)
				}
				done := make(chan heartbeatResult, 1)
				go func() {
					ok, err := db.store.RecordConnectorHello(context.Background(), id, cred, "v-observed", "runtime-observed")
					done <- heartbeatResult{recorded: ok, err: err}
				}()
				requireBlocked(t, done)
				if commit {
					err = tx.Commit(t.Context())
				} else {
					err = tx.Rollback(t.Context())
				}
				if err != nil {
					t.Fatal(err)
				}
				result := awaitHeartbeat(t, done)
				if result.err != nil || result.recorded == commit {
					t.Fatalf("HELLO recorded=%v err=%v", result.recorded, result.err)
				}
				var version, runtime *string
				if err := db.pool.QueryRow(t.Context(), `SELECT last_connector_version,last_runtime_id FROM connectors WHERE id=$1`, id).Scan(&version, &runtime); err != nil {
					t.Fatal(err)
				}
				if commit && (version != nil || runtime != nil) {
					t.Fatal("revoked metadata updated")
				}
				if !commit && (version == nil || *version != "v-observed" || runtime == nil || *runtime != "runtime-observed") {
					t.Fatal("active metadata not recorded")
				}
				requireLastSeenAt(t, db, id, nil)
			})
		}
	}
}

func TestManagedRevokeRacesHeartbeatAndHello(t *testing.T) {
	db := newTestDB(t)
	user := enrollmentAdmin(bootstrapSample(t, db, 1))
	svc := connector.NewEnrollmentService(db.store, connector.NewRegistry(), nil)
	for range 8 {
		issued, err := svc.Issue(t.Context(), user, "concurrent lifecycle")
		if err != nil {
			t.Fatal(err)
		}
		out, err := svc.Enroll(t.Context(), issued.Token)
		if err != nil {
			t.Fatal(err)
		}
		principal, err := connector.NewService(db.store).Authenticate(t.Context(), out.Credential)
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		errs := make(chan error, 3)
		go func() {
			<-start
			_, err := db.store.RecordConnectorHeartbeat(context.Background(), out.ConnectorID, principal.CredentialID, time.Now())
			errs <- err
		}()
		go func() {
			<-start
			_, err := db.store.RecordConnectorHello(context.Background(), out.ConnectorID, principal.CredentialID, "v1", "runtime1")
			errs <- err
		}()
		go func() { <-start; errs <- svc.Revoke(context.Background(), user, out.ConnectorID.String()) }()
		close(start)
		for range 3 {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
		}
		before := lastSeenAt(t, db, out.ConnectorID)
		if recorded, err := db.store.RecordConnectorHeartbeat(t.Context(), out.ConnectorID, principal.CredentialID, time.Now()); err != nil || recorded {
			t.Fatal("post-revoke heartbeat recorded")
		}
		if recorded, err := db.store.RecordConnectorHello(t.Context(), out.ConnectorID, principal.CredentialID, "stale", "stale"); err != nil || recorded {
			t.Fatal("post-revoke HELLO recorded")
		}
		requireLastSeenAt(t, db, out.ConnectorID, before)
	}
}

func TestEnrollmentChecksExpiryAfterWaitingForTokenLock(t *testing.T) {
	db := newTestDB(t)
	user := enrollmentAdmin(bootstrapSample(t, db, 1))
	// Keep the clock race-free while moving it across the expiry boundary.
	var mu sync.Mutex
	now := time.Now().UTC().Truncate(time.Microsecond)
	svc := connector.NewEnrollmentService(db.store, connector.NewRegistry(), func() time.Time { mu.Lock(); defer mu.Unlock(); return now })
	token, err := svc.Issue(t.Context(), user, "expires while waiting")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(t.Context(), `SELECT id FROM connector_enrollment_tokens WHERE id=$1 FOR UPDATE`, token.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := svc.Enroll(context.Background(), token.Token); done <- err }()
	select {
	case <-done:
		t.Fatal("enrollment bypassed token lock")
	case <-time.After(100 * time.Millisecond):
	}
	mu.Lock()
	now = token.ExpiresAt
	mu.Unlock()
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, connector.ErrInvalidEnrollmentToken) {
		t.Fatal("enrollment used pre-lock time")
	}
	if n := countRows(t, db, `SELECT count(*) FROM connectors`); n != 0 {
		t.Fatal("expired identity persisted")
	}
}
