//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/operation"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

func pendingOperation(t *testing.T, itemCount int) (*testDB, operation.Result) {
	t.Helper()
	db, f := operationFixture(t)
	in := operationInput(f, "worker-key-dummy")
	in.Items = in.Items[:itemCount]
	result, err := registerOperation(t.Context(), db, in, at(1))
	if err != nil {
		t.Fatal(err)
	}
	return db, result
}

func claimItem(t *testing.T, db *testDB) repository.OperationItemClaim {
	t.Helper()
	claim, err := db.store.ClaimOperationItem(t.Context(), uuid.New(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return claim
}

func TestOperationItemConcurrentClaimIsExclusive(t *testing.T) {
	db, result := pendingOperation(t, 1)
	const n = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	claims := make([]repository.OperationItemClaim, n)
	errs := make([]error, n)
	for i := range n {
		wg.Go(func() {
			<-start
			claims[i], errs[i] = db.store.ClaimOperationItem(t.Context(), uuid.New(), time.Minute)
		})
	}
	close(start)
	wg.Wait()
	winners := 0
	for i := range n {
		if errors.Is(errs[i], repository.ErrNotFound) {
			continue
		}
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		winners++
		c := claims[i]
		if c.Operation.ID != result.Operation.ID || c.Item.Status != "RUNNING" || c.Lease.Owner == uuid.Nil || c.Item.StartedAt == nil || !c.ExpiresAt.After(*c.Item.StartedAt) {
			t.Fatalf("claim=%+v", c)
		}
	}
	if winners != 1 {
		t.Fatalf("claim winners=%d", winners)
	}
	t.Log("PostgreSQL: 8 simultaneous claimers, exactly one committed owner")
}

func TestOperationItemClaimSkipsLockedItem(t *testing.T) {
	db, result := pendingOperation(t, 2)
	conn := postgrestest.Connect(t, db.dsn)
	tx, err := conn.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var locked uuid.UUID
	if err := tx.QueryRow(t.Context(), `SELECT id FROM operation_items ORDER BY created_at,id LIMIT 1 FOR UPDATE`).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	first, err := db.store.ClaimOperationItem(ctx, uuid.New(), time.Minute)
	if err != nil || first.Item.ID == locked {
		t.Fatalf("skip locked claim=%+v err=%v", first, err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	second := claimItem(t, db)
	if second.Item.ID != locked || second.Lease.Owner == first.Lease.Owner {
		t.Fatalf("second=%+v", second)
	}
	parent, err := db.store.OperationByID(t.Context(), result.Operation.ID)
	if err != nil || parent.Status != "RUNNING" || parent.StartedAt == nil || !parent.StartedAt.Equal(*first.Item.StartedAt) || !parent.UpdatedAt.Equal(*second.Item.StartedAt) {
		t.Fatalf("parent=%+v err=%v", parent, err)
	}
	t.Log("PostgreSQL: locked first item skipped; sibling claimed independently; parent first-start retained")
}

func TestOperationWorkerCommitsBeforeExternalExecution(t *testing.T) {
	db, result := pendingOperation(t, 2)
	conn := postgrestest.Connect(t, db.dsn)
	callbackCount := 0
	worker, err := operation.NewWorker(operation.WorkerOptions{
		Store: db.store, LeaseDuration: time.Minute,
		Execute: func(ctx context.Context, claim repository.OperationItemClaim) error {
			callbackCount++
			// A separate connection sees durable RUNNING/lease metadata and can
			// immediately lock both records while the external callback is active.
			tx, err := conn.Begin(ctx)
			if err != nil {
				return err
			}
			defer tx.Rollback(context.Background())
			var status, owner, parentStatus string
			var expires, started, parentStarted time.Time
			err = tx.QueryRow(ctx, `SELECT i.status,i.lease_owner,i.lease_expires_at,i.started_at,o.status,o.started_at
 FROM operation_items i JOIN operations o ON o.id=i.operation_id
 WHERE i.id=$1 FOR UPDATE OF i,o NOWAIT`, claim.Item.ID).Scan(&status, &owner, &expires, &started, &parentStatus, &parentStarted)
			if err != nil {
				return err
			}
			if status != "RUNNING" || parentStatus != "RUNNING" || owner != claim.Lease.Owner.String() || !expires.Equal(claim.ExpiresAt) || !started.Equal(*claim.Item.StartedAt) {
				t.Errorf("uncommitted/inconsistent claim: %s/%s %s", status, parentStatus, owner)
			}
			if err := tx.Commit(ctx); err != nil {
				return err
			}
			if callbackCount == 1 {
				// The first item can still be executing while another worker claims
				// its sibling. Parent start time remains the first claim's time.
				other := claimItem(t, db)
				if other.Item.ID == claim.Item.ID || !other.Operation.StartedAt.Equal(parentStarted) {
					t.Errorf("sibling claim=%+v", other)
				}
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := worker.RunOne(t.Context())
	if err != nil || !claimed || callbackCount != 1 {
		t.Fatalf("run=%v err=%v callbacks=%d", claimed, err, callbackCount)
	}
	items, err := db.store.OperationItems(t.Context(), result.Operation.ID)
	if err != nil || len(items) != 2 || items[0].Status != "RUNNING" || items[1].Status != "RUNNING" {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	t.Log("PostgreSQL: external callback sees committed lease; separate connection obtains item + parent NOWAIT locks; sibling claim succeeds during callback")
}

func TestOperationItemLeaseFencesStaleWriters(t *testing.T) {
	for _, change := range []string{"wrong owner", "expired", "replaced owner", "item reconciling", "item succeeded", "item pending", "parent reconciling", "parent failed"} {
		t.Run(change, func(t *testing.T) {
			db, result := pendingOperation(t, 1)
			claim := claimItem(t, db)
			// A valid owner can renew and the returned expiry is durable.
			expires, err := db.store.RenewOperationItemLease(t.Context(), claim.Lease, 2*time.Minute)
			if err != nil || !expires.After(claim.ExpiresAt) {
				t.Fatalf("renew=%v err=%v", expires, err)
			}
			switch change {
			case "wrong owner":
				claim.Lease.Owner = uuid.New()
			case "expired":
				mustExec(t, db, `UPDATE operation_items SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, claim.Item.ID)
			case "replaced owner":
				mustExec(t, db, `UPDATE operation_items SET lease_owner=$2 WHERE id=$1`, claim.Item.ID, uuid.New().String())
			case "item reconciling", "item succeeded", "item pending":
				status := map[string]string{"item reconciling": "RECONCILING", "item succeeded": "SUCCEEDED", "item pending": "PENDING"}[change]
				mustExec(t, db, `UPDATE operation_items SET status=$2 WHERE id=$1`, claim.Item.ID, status)
			case "parent reconciling", "parent failed":
				status := map[string]string{"parent reconciling": "RECONCILING", "parent failed": "FAILED"}[change]
				mustExec(t, db, `UPDATE operations SET status=$2,updated_at=clock_timestamp() WHERE id=$1`, result.Operation.ID, status)
			}
			var before, after string
			if err := db.pool.QueryRow(t.Context(), `SELECT row_to_json(i)::text FROM operation_items i WHERE id=$1`, claim.Item.ID).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if _, err := db.store.RenewOperationItemLease(t.Context(), claim.Lease, time.Minute); !errors.Is(err, repository.ErrOperationLeaseLost) {
				t.Fatalf("stale renewal=%v", err)
			}
			if err := db.pool.QueryRow(t.Context(), `SELECT row_to_json(i)::text FROM operation_items i WHERE id=$1`, claim.Item.ID).Scan(&after); err != nil || before != after {
				t.Fatalf("stale writer changed row: %v", err)
			}
		})
	}
}

func TestOperationItemLeaseChecksTimeAfterLockWait(t *testing.T) {
	db, _ := pendingOperation(t, 1)
	claim := claimItem(t, db)
	conn := postgrestest.Connect(t, db.dsn)
	tx, err := conn.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(t.Context(), `UPDATE operation_items SET lease_expires_at=clock_timestamp()+interval '2 seconds' WHERE id=$1`, claim.Item.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	go func() {
		_, err := db.store.RenewOperationItemLease(ctx, claim.Lease, time.Minute)
		done <- err
	}()
	waitForLockWait(t, db.pool)
	// The renewal transaction began while the lease was live, then the lease
	// expires while that transaction waits. now()/transaction_timestamp() would
	// incorrectly allow renewal after the lock becomes available.
	var waitingBeforeExpiry bool
	if err := tx.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity a, operation_items i
 WHERE a.datname=current_database() AND a.wait_event_type='Lock'
 AND a.xact_start < i.lease_expires_at AND i.id=$1)`, claim.Item.ID).Scan(&waitingBeforeExpiry); err != nil || !waitingBeforeExpiry {
		t.Fatalf("renewal did not start before expiry: %v", err)
	}
	if _, err := tx.Exec(t.Context(), `SELECT pg_sleep(GREATEST(0,extract(epoch FROM lease_expires_at-clock_timestamp()))+0.05)
 FROM operation_items WHERE id=$1`, claim.Item.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, repository.ErrOperationLeaseLost) {
		t.Fatalf("expired while waiting=%v", err)
	}
}

func TestOperationWorkerRestartNeverReclaimsRunningItems(t *testing.T) {
	db, _ := pendingOperation(t, 2)
	for range 2 {
		claimItem(t, db)
	}
	mustExec(t, db, `UPDATE operation_items SET lease_expires_at=clock_timestamp()-interval '1 hour'`)
	// A new Store/pool and Worker model process restart without in-memory ownership.
	pool, err := postgres.OpenPool(t.Context(), db.dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	worker, err := operation.NewWorker(operation.WorkerOptions{Store: postgres.NewStore(pool), LeaseDuration: time.Minute,
		Execute: func(context.Context, repository.OperationItemClaim) error {
			t.Error("expired RUNNING item was executed again")
			return nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := worker.RunOne(t.Context()); err != nil || claimed {
		t.Fatalf("restart claim=%v err=%v", claimed, err)
	}
	if got := countRows(t, db, `SELECT count(*) FROM operation_items WHERE status='RUNNING'`); got != 2 {
		t.Fatalf("RUNNING items=%d", got)
	}
	t.Log("PostgreSQL: expired RUNNING items survive new pool/Store/Worker; no callback or PENDING reset")
}

func TestOperationWorkerExecutionFailurePreservesRunningItem(t *testing.T) {
	for _, scenario := range []string{"unknown outcome", "process cancellation"} {
		t.Run(scenario, func(t *testing.T) {
			db, _ := pendingOperation(t, 1)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			outcome := errors.New("external outcome unknown")
			calls := 0
			worker, err := operation.NewWorker(operation.WorkerOptions{Store: db.store, LeaseDuration: time.Minute,
				Execute: func(context.Context, repository.OperationItemClaim) error {
					calls++
					if scenario == "process cancellation" {
						cancel()
						return ctx.Err()
					}
					return outcome
				}})
			if err != nil {
				t.Fatal(err)
			}
			claimed, err := worker.RunOne(ctx)
			wantErr := outcome
			if scenario == "process cancellation" {
				wantErr = context.Canceled
			}
			if !claimed || !errors.Is(err, wantErr) {
				t.Fatalf("execution=%v/%v", claimed, err)
			}
			if got := countRows(t, db, `SELECT count(*) FROM operation_items
 WHERE status='RUNNING' AND lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL AND finished_at IS NULL`); got != 1 {
				t.Fatalf("durable RUNNING items=%d", got)
			}
			if claimed, err := worker.RunOne(t.Context()); claimed || err != nil || calls != 1 {
				t.Fatalf("blind retry=%v/%v callbacks=%d", claimed, err, calls)
			}
		})
	}
}

func TestOperationClaimRollbackIsAtomic(t *testing.T) {
	db, _ := pendingOperation(t, 1)
	// Reject the parent write after the item has been updated. Both must roll back.
	mustExec(t, db, `CREATE FUNCTION reject_worker_parent() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN RAISE EXCEPTION 'test parent write rejection'; END; $$;
 CREATE TRIGGER reject_worker_parent BEFORE UPDATE ON operations FOR EACH ROW EXECUTE FUNCTION reject_worker_parent()`)
	if claim, err := db.store.ClaimOperationItem(t.Context(), uuid.New(), time.Minute); err == nil || claim.Item.ID != uuid.Nil {
		t.Fatalf("failed claim authorized execution: %+v %v", claim, err)
	}
	if got := countRows(t, db, `SELECT count(*) FROM operation_items WHERE status='PENDING' AND lease_owner IS NULL AND lease_expires_at IS NULL AND started_at IS NULL`); got != 1 {
		t.Fatalf("rollback pending items=%d", got)
	}
	if got := countRows(t, db, `SELECT count(*) FROM operations WHERE status='PENDING' AND started_at IS NULL AND updated_at=created_at`); got != 1 {
		t.Fatalf("rollback pending operations=%d", got)
	}
}

func TestOperationClaimDoesNotOverrideParentLifecycle(t *testing.T) {
	for _, status := range []string{"RECONCILING", "SUCCEEDED", "FAILED"} {
		t.Run(status, func(t *testing.T) {
			db, result := pendingOperation(t, 1)
			mustExec(t, db, `UPDATE operations SET status=$2,updated_at=clock_timestamp() WHERE id=$1`, result.Operation.ID, status)
			if _, err := db.store.ClaimOperationItem(t.Context(), uuid.New(), time.Minute); !errors.Is(err, repository.ErrNotFound) {
				t.Fatalf("inactive parent claim=%v", err)
			}
		})
	}
}
