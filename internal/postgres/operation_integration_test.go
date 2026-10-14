//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/operation"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal/terminaltest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/tracecontext"
)

func operationFixture(t *testing.T) (*testDB, *terminaltest.Fixture) {
	t.Helper()
	db := newTestDB(t)
	conn := postgrestest.Connect(t, db.dsn)
	f := terminaltest.New(t, conn)
	return db, f
}
func operationInput(f *terminaltest.Fixture, key string) operation.Registration {
	return operation.Registration{OrganizationID: f.OrganizationID, RequestedByUserID: f.InstructorID, Type: "PROVISION", LabExecutionID: &f.LabExecutionID,
		IdempotencyKey: key, Fingerprint: operation.SemanticFingerprint([]byte("class/spec/revision/selected-students-canonical-dummy")), RequestID: "request-correlation-dummy",
		Items: []operation.Item{{LabInstanceID: f.LabInstanceID, Generation: 1}, {LabInstanceID: f.PeerLabInstanceID, Generation: 1}}}
}
func registerOperation(ctx context.Context, db *testDB, in operation.Registration, at time.Time) (operation.Result, error) {
	var result operation.Result
	err := db.store.WithinTransaction(ctx, func(ctx context.Context, r repository.Repositories) error {
		var err error
		result, err = operation.Register(ctx, r, in, at)
		return err
	})
	return result, err
}
func operationCounts(t *testing.T, db *testDB, operations, items int) {
	t.Helper()
	var o, i int
	if err := db.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM operations),(SELECT count(*) FROM operation_items)`).Scan(&o, &i); err != nil {
		t.Fatal(err)
	}
	if o != operations || i != items {
		t.Fatalf("rows=%d/%d want=%d/%d", o, i, operations, items)
	}
}
func TestOperationRegistrationPersistsItemsTraceAndUpdatedAt(t *testing.T) {
	db, f := operationFixture(t)
	in := operationInput(f, "operation-key-dummy")
	tc := tracecontext.Normalize("00-0123456789abcdef0123456789abcdef-0123456789abcdef-00", "vendor=opaque")
	first, err := registerOperation(tracecontext.NewContext(t.Context(), tc), db, in, at(1))
	if err != nil || first.Replayed {
		t.Fatalf("first=%+v %v", first, err)
	}
	operationCounts(t, db, 1, 2)
	saved, err := db.store.OperationByID(t.Context(), first.Operation.ID)
	if err != nil || saved.Status != "PENDING" || saved.Type != "PROVISION" || *saved.LabExecutionID != f.LabExecutionID || saved.RequestID != in.RequestID || saved.Traceparent != tc.Traceparent || saved.Tracestate != tc.Tracestate || !saved.CreatedAt.Equal(at(1)) || !saved.UpdatedAt.Equal(at(1)) || saved.StartedAt != nil || saved.FinishedAt != nil {
		t.Fatalf("persisted=%+v %v", saved, err)
	}
	items, err := db.store.OperationItems(t.Context(), saved.ID)
	if err != nil || len(items) != 2 {
		t.Fatalf("items=%+v %v", items, err)
	}
	for _, i := range items {
		if i.Status != "PENDING" || i.Generation != 1 || i.OperationID != saved.ID || i.OrganizationID != f.OrganizationID || !i.CreatedAt.Equal(at(1)) {
			t.Fatalf("item=%+v", i)
		}
	}
	replay, err := registerOperation(t.Context(), db, in, at(2))
	if err != nil || !replay.Replayed || replay.Operation.ID != saved.ID || replay.Operation.Traceparent != saved.Traceparent || replay.Operation.Tracestate != saved.Tracestate || !replay.Operation.UpdatedAt.Equal(saved.UpdatedAt) {
		t.Fatalf("replay=%+v %v", replay, err)
	}
	in.Fingerprint = operation.SemanticFingerprint([]byte("different semantic target"))
	if _, err := registerOperation(t.Context(), db, in, at(2)); !errors.Is(err, operation.ErrConflict) {
		t.Fatalf("conflict=%v", err)
	}
	operationCounts(t, db, 1, 2)
	// A future state writer owns updated_at in the same transaction; created_at never changes.
	if _, err := db.pool.Exec(t.Context(), `UPDATE operations SET status='RUNNING',stage='INITIALIZING',started_at=$2,updated_at=$2 WHERE id=$1`, saved.ID, at(3)); err != nil {
		t.Fatal(err)
	}
	changed, err := db.store.OperationByID(t.Context(), saved.ID)
	if err != nil || !changed.CreatedAt.Equal(at(1)) || !changed.UpdatedAt.Equal(at(3)) || changed.StartedAt == nil || !changed.StartedAt.Equal(at(3)) {
		t.Fatalf("timestamps=%+v %v", changed, err)
	}
	t.Log("PostgreSQL: durable operation + 2 items; trace flags/tracestate retained; replay/conflict and creation/update times verified")
}
func TestOperationRegistrationRollbackIncludesCallerBusinessRows(t *testing.T) {
	for _, failure := range []string{"item constraint", "caller failure"} {
		t.Run(failure, func(t *testing.T) {
			db, f := operationFixture(t)
			in := operationInput(f, "rollback-key-dummy")
			newUser := uuid.New()
			sentinel := errors.New("caller business failure")
			if failure == "item constraint" {
				in.Items[1].LabInstanceID = uuid.New()
			}
			err := db.store.WithinTransaction(t.Context(), func(ctx context.Context, r repository.Repositories) error {
				if err := r.CreateUser(ctx, repository.NewUser{ID: newUser, OrganizationID: f.OrganizationID, OrganizationRole: repository.OrganizationRoleMember}); err != nil {
					return err
				}
				if _, err := operation.Register(ctx, r, in, at(1)); err != nil {
					return err
				}
				return sentinel
			})
			if failure == "item constraint" && !errors.Is(err, repository.ErrConstraintViolation) {
				t.Fatalf("constraint=%v", err)
			}
			if failure == "caller failure" && !errors.Is(err, sentinel) {
				t.Fatalf("callback=%v", err)
			}
			operationCounts(t, db, 0, 0)
			if _, err := db.store.UserByID(t.Context(), newUser); !errors.Is(err, repository.ErrNotFound) {
				t.Fatalf("business row survived=%v", err)
			}
		})
	}
}
func TestOperationConcurrentReplayUsesUniqueConstraint(t *testing.T) {
	db, f := operationFixture(t)
	in := operationInput(f, "concurrent-key-dummy")
	const n = 12
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]operation.Result, n)
	errs := make([]error, n)
	for i := range n {
		wg.Go(func() { <-start; results[i], errs[i] = registerOperation(t.Context(), db, in, at(1)) })
	}
	close(start)
	wg.Wait()
	created := 0
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("request %d=%v", i, errs[i])
		}
		if results[i].Operation.ID != results[0].Operation.ID {
			t.Fatal("duplicate operation")
		}
		if !results[i].Replayed {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("new operations=%d", created)
	}
	operationCounts(t, db, 1, 2)
	// Another key must not bypass the active mutation unique index.
	in.IdempotencyKey = "another-key-dummy"
	if _, err := registerOperation(t.Context(), db, in, at(2)); !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("active mutation unique=%v", err)
	}
	operationCounts(t, db, 1, 2)
	t.Log("PostgreSQL: 12 concurrent requests -> one Operation, two Items, 11 replays; distinct key rejected while items active")
}
func TestOperationConcurrentDifferentSemanticsConflict(t *testing.T) {
	db, f := operationFixture(t)
	a := operationInput(f, "race-conflict-dummy")
	b := a
	b.Fingerprint = operation.SemanticFingerprint([]byte("different semantic payload"))
	inputs := []operation.Registration{a, b}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range inputs {
		wg.Go(func() { <-start; _, errs[i] = registerOperation(t.Context(), db, inputs[i], at(1)) })
	}
	close(start)
	wg.Wait()
	if !((errs[0] == nil && errors.Is(errs[1], operation.ErrConflict)) || (errs[1] == nil && errors.Is(errs[0], operation.ErrConflict))) {
		t.Fatalf("race errors=%v", errs)
	}
	operationCounts(t, db, 1, 2)
}
func TestOperationInvalidTraceIsNonFatal(t *testing.T) {
	db, f := operationFixture(t)
	in := operationInput(f, "invalid-trace-key-dummy")
	ctx := tracecontext.NewContext(t.Context(), tracecontext.Context{Traceparent: "invalid", Tracestate: "credential-dummy"})
	result, err := registerOperation(ctx, db, in, at(1))
	if err != nil {
		t.Fatal(err)
	}
	var tp, ts *string
	if err := db.pool.QueryRow(t.Context(), `SELECT traceparent,tracestate FROM operations WHERE id=$1`, result.Operation.ID).Scan(&tp, &ts); err != nil || tp != nil || ts != nil {
		t.Fatalf("invalid trace=%v/%v %v", tp, ts, err)
	}
}
func TestOperationUpdatedAtMigrationBackfillsExistingRows(t *testing.T) {
	for _, state := range []string{"PENDING", "RUNNING", "FAILED"} {
		t.Run(state, func(t *testing.T) {
			all := embeddedMigrations(t)
			dsn := postgrestest.NewDatabase(t)
			conn := postgrestest.Connect(t, dsn)
			postgrestest.Migrate(t, dsn, migrationsUpTo(all, 6))
			id := seedOperation(t, conn)
			var started, finished *time.Time
			expected := at(1)
			if state != "PENDING" {
				v := at(2)
				started = &v
				expected = v
			}
			if state == "FAILED" {
				v := at(3)
				finished = &v
				expected = v
			}
			if _, err := conn.Exec(t.Context(), `UPDATE operations SET created_at=$2,started_at=$3,finished_at=$4,status=$5 WHERE id=$1`, id, at(1), started, finished, state); err != nil {
				t.Fatal(err)
			}
			postgrestest.Migrate(t, dsn, all)
			var created, updated time.Time
			if err := conn.QueryRow(t.Context(), `SELECT created_at,updated_at FROM operations WHERE id=$1`, id).Scan(&created, &updated); err != nil || !created.Equal(at(1)) || !updated.Equal(expected) {
				t.Fatalf("backfill=%v/%v %v", created, updated, err)
			}
		})
	}
}

func TestOperationItemMustBelongToOperationTarget(t *testing.T) {
	db, f := operationFixture(t)
	conn := postgrestest.Connect(t, db.dsn)
	unrelated := f.AddLabInstanceWithSnapshot(t, conn, terminaltest.Snapshot("workspace", terminaltest.DefaultVMs()))
	in := operationInput(f, "wrong-target-key-dummy")
	in.Items[1].LabInstanceID = unrelated
	if _, err := registerOperation(t.Context(), db, in, at(1)); !errors.Is(err, repository.ErrConstraintViolation) {
		t.Fatalf("wrong execution item: %v", err)
	}
	operationCounts(t, db, 0, 0)
}

func TestOperationReplayRollsBackCallerAllocations(t *testing.T) {
	db, f := operationFixture(t)
	in := operationInput(f, "allocation-replay-dummy")
	first, err := registerOperation(t.Context(), db, in, at(1))
	if err != nil {
		t.Fatal(err)
	}
	allocated := uuid.New()
	replayed := errors.New("roll back replay allocations")
	err = db.store.WithinTransaction(t.Context(), func(ctx context.Context, r repository.Repositories) error {
		if err := r.CreateUser(ctx, repository.NewUser{ID: allocated, OrganizationID: f.OrganizationID, OrganizationRole: repository.OrganizationRoleMember}); err != nil {
			return err
		}
		result, err := operation.Register(ctx, r, in, at(2))
		if err != nil {
			return err
		}
		if !result.Replayed || result.Operation.ID != first.Operation.ID {
			return errors.New("expected existing operation")
		}
		return replayed
	})
	if !errors.Is(err, replayed) {
		t.Fatal(err)
	}
	if _, err := db.store.UserByID(t.Context(), allocated); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("replay allocation remained: %v", err)
	}
	operationCounts(t, db, 1, 2)
}

func TestOperationIdempotencyScopeIsUserAndType(t *testing.T) {
	db, f := operationFixture(t)
	in := operationInput(f, "scoped-key-dummy")
	provision, err := registerOperation(t.Context(), db, in, at(1))
	if err != nil {
		t.Fatal(err)
	}
	finish := func(id uuid.UUID) {
		t.Helper()
		if _, err := db.pool.Exec(t.Context(), `UPDATE operation_items SET status='SUCCEEDED',finished_at=$2 WHERE operation_id=$1`, id, at(2)); err != nil {
			t.Fatal(err)
		}
	}
	finish(provision.Operation.ID)
	// Same key/type, another authenticated requester has a separate unique scope.
	in.RequestedByUserID = f.OwnerID
	otherUser, err := registerOperation(t.Context(), db, in, at(2))
	if err != nil || otherUser.Replayed || otherUser.Operation.ID == provision.Operation.ID {
		t.Fatalf("user scope: %+v %v", otherUser, err)
	}
	finish(otherUser.Operation.ID)
	// Same user/key, another operation type has a separate unique scope.
	in.RequestedByUserID = f.InstructorID
	in.Type = "CLEANUP"
	in.Fingerprint = operation.SemanticFingerprint([]byte("cleanup-execution-dummy"))
	cleanup, err := registerOperation(t.Context(), db, in, at(2))
	if err != nil || cleanup.Replayed || cleanup.Operation.ID == provision.Operation.ID {
		t.Fatalf("type scope: %+v %v", cleanup, err)
	}
	operationCounts(t, db, 3, 6)
}
