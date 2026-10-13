//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/operation"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal/terminaltest"
)

func generationInput(f *terminaltest.Fixture, kind string, generation int64) operation.Registration {
	in := operationInput(f, "generation-key-dummy")
	in.Type = kind
	in.Items = []operation.Item{{LabInstanceID: f.LabInstanceID, Generation: generation}}
	if kind == "RESET" {
		in.LabExecutionID = nil
		in.LabInstanceID = &f.LabInstanceID
	}
	return in
}

func TestOperationItemGenerationMatchesMutation(t *testing.T) {
	for _, tt := range []struct {
		kind           string
		current, valid int64
		rejected       []int64
	}{
		{"PROVISION", 1, 1, []int64{0, 2}},
		{"CLEANUP", 2, 2, []int64{1, 3}},
		{"RESET", 1, 2, []int64{1, 3}},
		{"RESET", 2, 3, []int64{1, 2, 4}},
	} {
		t.Run(fmt.Sprintf("%s-current-%d", tt.kind, tt.current), func(t *testing.T) {
			db, f := operationFixture(t)
			conn := postgrestest.Connect(t, db.dsn)
			terminaltest.Exec(t, conn, `UPDATE lab_instances SET generation=$2 WHERE id=$1`, f.LabInstanceID, tt.current)
			// RESET targets the new generation, while tracked resources remain in the previous one.
			if tt.current > 1 {
				f.AddResource(t, conn, f.LabInstanceID, tt.current, "SERVER", "workspace", "PRESENT")
			}
			before := countRows(t, db, `SELECT count(*) FROM provider_resources WHERE lab_instance_id=$1 AND generation=$2`, f.LabInstanceID, tt.current)
			if before == 0 {
				t.Fatal("missing previous/current resource fixture")
			}
			for _, generation := range tt.rejected {
				in := generationInput(f, tt.kind, generation)
				// Call the persistence primitive directly as well, so generation=0 is not rejected only by Application validation.
				err := db.store.WithinTransaction(t.Context(), func(ctx context.Context, r repository.Repositories) error {
					hash, _ := operation.HashKey(in.IdempotencyKey)
					fp := in.Fingerprint
					id := uuid.New()
					inserted, err := r.InsertOperation(ctx, repository.NewOperation{ID: id, OrganizationID: in.OrganizationID, RequestedByUserID: in.RequestedByUserID, Type: in.Type, LabExecutionID: in.LabExecutionID, LabInstanceID: in.LabInstanceID, IdempotencyKeyHash: hash, RequestFingerprint: fp[:], CreatedAt: at(1)})
					if err != nil {
						return err
					}
					if !inserted {
						return errors.New("unexpected replay")
					}
					return r.CreateOperationItem(ctx, repository.NewOperationItem{ID: uuid.New(), OrganizationID: f.OrganizationID, OperationID: id, LabInstanceID: f.LabInstanceID, Generation: generation, CreatedAt: at(1)})
				})
				if !errors.Is(err, repository.ErrConstraintViolation) {
					t.Fatalf("%s current=%d supplied=%d error=%v", tt.kind, tt.current, generation, err)
				}
				operationCounts(t, db, 0, 0)
			}
			result, err := registerOperation(t.Context(), db, generationInput(f, tt.kind, tt.valid), at(1))
			if err != nil {
				t.Fatal(err)
			}
			items, err := db.store.OperationItems(t.Context(), result.Operation.ID)
			if err != nil || len(items) != 1 || items[0].Generation != tt.valid {
				t.Fatalf("persisted item=%+v %v", items, err)
			}
			instance, err := db.store.LabInstanceByID(t.Context(), f.LabInstanceID)
			if err != nil || instance.Generation != tt.current {
				t.Fatalf("registration changed active generation: %+v %v", instance, err)
			}
			if after := countRows(t, db, `SELECT count(*) FROM provider_resources WHERE lab_instance_id=$1 AND generation=$2`, f.LabInstanceID, tt.current); after != before {
				t.Fatal("registration changed ProviderResource history")
			}
			operationCounts(t, db, 1, 1)
		})
	}
}

func TestOperationGenerationRechecksConcurrentWriter(t *testing.T) {
	for _, kind := range []string{"PROVISION", "CLEANUP", "RESET"} {
		for _, commit := range []bool{true, false} {
			t.Run(kind+map[bool]string{true: "-commit", false: "-rollback"}[commit], func(t *testing.T) {
				db, f := operationFixture(t)
				ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
				defer cancel()
				writer, err := db.pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = writer.Rollback(context.Background()) }()
				if _, err := writer.Exec(ctx, `UPDATE lab_instances SET generation=2 WHERE id=$1`, f.LabInstanceID); err != nil {
					t.Fatal(err)
				}
				supplied := int64(1)
				if kind == "RESET" {
					supplied = 2
				}
				done := make(chan error, 1)
				go func() { _, err := registerOperation(ctx, db, generationInput(f, kind, supplied), at(1)); done <- err }()
				waitForLockWait(t, db.pool)
				select {
				case err := <-done:
					t.Fatalf("registration skipped generation lock: %v", err)
				default:
				}
				if commit {
					err = writer.Commit(ctx)
				} else {
					err = writer.Rollback(ctx)
				}
				if err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-done:
					if commit {
						if !errors.Is(err, repository.ErrConstraintViolation) {
							t.Fatalf("stale generation accepted after concurrent commit: %v", err)
						}
						operationCounts(t, db, 0, 0)
					} else {
						if err != nil {
							t.Fatalf("rollback should preserve valid generation: %v", err)
						}
						operationCounts(t, db, 1, 1)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			})
		}
	}
}

func TestOperationGenerationLockHeldThroughTransaction(t *testing.T) {
	for _, kind := range []string{"PROVISION", "CLEANUP", "RESET"} {
		t.Run(kind, func(t *testing.T) {
			db, f := operationFixture(t)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			locked, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			registered := make(chan error, 1)
			supplied := int64(1)
			if kind == "RESET" {
				supplied = 2
			}
			go func() {
				registered <- db.store.WithinTransaction(ctx, func(ctx context.Context, r repository.Repositories) error {
					if _, err := operation.Register(ctx, r, generationInput(f, kind, supplied), at(1)); err != nil {
						return err
					}
					close(locked)
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				})
			}()
			select {
			case <-locked:
			case err := <-registered:
				t.Fatalf("registration failed: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			updated := make(chan error, 1)
			go func() {
				_, err := db.pool.Exec(ctx, `UPDATE lab_instances SET generation=2 WHERE id=$1`, f.LabInstanceID)
				updated <- err
			}()
			waitForLockWait(t, db.pool)
			select {
			case err := <-updated:
				t.Fatalf("generation changed before registration commit: %v", err)
			default:
			}
			unblock()
			for _, done := range []<-chan error{registered, updated} {
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			operationCounts(t, db, 1, 1)
		})
	}
}
