//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

func pendingProvision(t *testing.T) (*provisionDB, uuid.UUID) {
	t.Helper()
	p := newProvisionDB(t)
	a, err := p.svc.Provision(t.Context(), p.user(t, p.f.InstructorID), p.input("worker-provision", p.f.StudentAID, p.f.StudentBID))
	if err != nil {
		t.Fatal(err)
	}
	return p, a.OperationID
}

func prepareProvision(t *testing.T, p *provisionDB) repository.ProvisionDispatch {
	t.Helper()
	c, err := p.store.ClaimProvisionOperationItem(t.Context(), uuid.New(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	d, err := p.store.PrepareProvision(t.Context(), c.Lease)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func provisionUpdate(d repository.ProvisionDispatch, status, id string) repository.ProvisionUpdate {
	return repository.ProvisionUpdate{Dispatch: d, Status: status, Stage: "VERIFYING_READINESS", Resources: []repository.ProvisionResource{
		{Type: "SERVER", ProviderID: id, LogicalName: "vm-1", Generation: d.Generation, ObservedState: "ACTIVE"},
	}}
}

func TestProvisionWriterAggregatesIndependentItemsAndPreservesExactResources(t *testing.T) {
	p, op := pendingProvision(t)
	first := prepareProvision(t, p)
	// An accepted ACK is a progress write, never completion.
	if err := p.store.WriteProvision(t.Context(), repository.ProvisionUpdate{Dispatch: first, Status: "RUNNING", Stage: "PROVISIONING"}); err != nil {
		t.Fatal(err)
	}
	parent, err := p.store.OperationByID(t.Context(), op)
	if err != nil || parent.Status != "RUNNING" || parent.FinishedAt != nil {
		t.Fatalf("ACK parent=%+v err=%v", parent, err)
	}
	if err := p.store.WriteProvision(t.Context(), provisionUpdate(first, "SUCCEEDED", "exact-provider-ID/one")); err != nil {
		t.Fatal(err)
	}
	second := prepareProvision(t, p)
	u := provisionUpdate(second, "FAILED", "partial-provider-ID/two")
	u.ErrorCode = "PROVISION_FAILED"
	u.ErrorMessage = "Safe failure summary"
	if err := p.store.WriteProvision(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	parent, _ = p.store.OperationByID(t.Context(), op)
	if parent.Status != "RUNNING" || parent.FinishedAt != nil {
		t.Fatalf("pending sibling blocked: %+v", parent)
	}
	third := prepareProvision(t, p)
	if err := p.store.WriteProvision(t.Context(), provisionUpdate(third, "SUCCEEDED", "exact-provider-ID/three")); err != nil {
		t.Fatal(err)
	}
	parent, _ = p.store.OperationByID(t.Context(), op)
	if parent.Status != "FAILED" || parent.FinishedAt == nil || parent.ErrorCode != "PROVISION_FAILED" {
		t.Fatalf("aggregate=%+v", parent)
	}
	var resources, successful, failed, ready int
	err = p.pool.QueryRow(t.Context(), `SELECT
 (SELECT count(*) FROM provider_resources WHERE organization_id=$1 AND provider_connection_id=$2 AND generation=1
 AND lifecycle_status='PRESENT' AND observed_state='ACTIVE' AND created_by_operation_item_id IS NOT NULL),
 (SELECT count(*) FROM lab_instances WHERE status='PROVISIONING'),
 (SELECT count(*) FROM lab_instances WHERE status='ERROR'),
 (SELECT count(*) FROM lab_instances WHERE status='READY')`, p.f.OrganizationID, p.f.ProviderConnectionID).Scan(&resources, &successful, &failed, &ready)
	if err != nil || resources != 3 || successful != 2 || failed != 1 || ready != 0 {
		t.Fatalf("resources/states=%d/%d/%d/%d %v", resources, successful, failed, ready, err)
	}
	var exact string
	if err := p.pool.QueryRow(t.Context(), `SELECT provider_id FROM provider_resources WHERE created_by_operation_item_id=$1`, first.Lease.ItemID).Scan(&exact); err != nil || exact != "exact-provider-ID/one" {
		t.Fatalf("exact ID=%q %v", exact, err)
	}
	if err := p.store.WriteProvision(t.Context(), provisionUpdate(first, "FAILED", "late-resource")); !errors.Is(err, repository.ErrOperationLeaseLost) {
		t.Fatalf("late writer=%v", err)
	}
}

func TestProvisionUnknownSiblingDoesNotBlockUnstartedOrRunningItems(t *testing.T) {
	p, op := pendingProvision(t)
	first := prepareProvision(t, p)
	u := provisionUpdate(first, "RECONCILING", "known-before-timeout")
	u.Stage = "RECONCILING"
	u.ErrorCode = "OUTCOME_UNKNOWN"
	if err := p.store.WriteProvision(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	second := prepareProvision(t, p)
	third := prepareProvision(t, p)
	if _, err := p.store.RenewOperationItemLease(t.Context(), second.Lease, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := p.store.WriteProvision(t.Context(), provisionUpdate(second, "SUCCEEDED", "second")); err != nil {
		t.Fatal(err)
	}
	if err := p.store.WriteProvision(t.Context(), provisionUpdate(third, "FAILED", "third")); err != nil {
		t.Fatal(err)
	}
	parent, _ := p.store.OperationByID(t.Context(), op)
	if parent.Status != "RECONCILING" || parent.FinishedAt != nil {
		t.Fatalf("unknown aggregate=%+v", parent)
	}
	if _, err := p.store.ClaimProvisionOperationItem(t.Context(), uuid.New(), time.Minute); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("blind reclaim=%v", err)
	}
}

func TestProvisionWriterFencesLeaseStateAndCorrelation(t *testing.T) {
	for _, scenario := range []string{"owner", "expired", "item state", "parent state", "instance generation", "connector", "operation", "instance", "generation", "connection", "organization", "execution", "resource generation"} {
		t.Run(scenario, func(t *testing.T) {
			p, _ := pendingProvision(t)
			d := prepareProvision(t, p)
			u := provisionUpdate(d, "SUCCEEDED", "must-not-persist")
			switch scenario {
			case "owner":
				u.Dispatch.Lease.Owner = uuid.New()
			case "expired":
				mustExec(t, p.testDB, `UPDATE operation_items SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, d.Lease.ItemID)
			case "item state":
				mustExec(t, p.testDB, `UPDATE operation_items SET status='RECONCILING' WHERE id=$1`, d.Lease.ItemID)
			case "parent state":
				mustExec(t, p.testDB, `UPDATE operations SET status='RECONCILING' WHERE id=$1`, d.OperationID)
			case "instance generation":
				mustExec(t, p.testDB, `UPDATE lab_instances SET generation=2 WHERE id=$1`, d.LabInstanceID)
			case "connector":
				u.Dispatch.ConnectorID = uuid.New()
			case "operation":
				u.Dispatch.OperationID = uuid.New()
			case "instance":
				u.Dispatch.LabInstanceID = uuid.New()
			case "generation":
				u.Dispatch.Generation = 2
			case "connection":
				u.Dispatch.ProviderConnectionID = uuid.New()
			case "organization":
				u.Dispatch.OrganizationID = uuid.New()
			case "execution":
				u.Dispatch.LabExecutionID = uuid.New()
			case "resource generation":
				u.Resources[0].Generation = 2
			}
			if err := p.store.WriteProvision(t.Context(), u); err == nil {
				t.Fatal("stale/mismatched writer accepted")
			}
			if got := countRows(t, p.testDB, `SELECT count(*) FROM provider_resources`); got != 0 {
				t.Fatalf("resources=%d", got)
			}
			if got := countRows(t, p.testDB, `SELECT count(*) FROM operation_items WHERE status='PENDING'`); got != 2 {
				t.Fatalf("other items changed: %d", got)
			}
		})
	}
}

func TestProvisionWriterChecksExpiryAfterLockWait(t *testing.T) {
	p, _ := pendingProvision(t)
	d := prepareProvision(t, p)
	tx, err := p.holder.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	_, err = tx.Exec(t.Context(), `UPDATE operation_items SET lease_expires_at=clock_timestamp()+interval '500 milliseconds' WHERE id=$1`, d.Lease.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- p.store.WriteProvision(t.Context(), provisionUpdate(d, "SUCCEEDED", "expired-lock-wait"))
	}()
	p.waitForLockWaiters(t, 1)
	_, err = tx.Exec(t.Context(), `SELECT pg_sleep(GREATEST(0,extract(epoch FROM lease_expires_at-clock_timestamp()))+0.05) FROM operation_items WHERE id=$1`, d.Lease.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, repository.ErrOperationLeaseLost) {
		t.Fatalf("expired writer=%v", err)
	}
	if n := countRows(t, p.testDB, `SELECT count(*) FROM provider_resources`); n != 0 {
		t.Fatalf("resources=%d", n)
	}
}

func TestProvisionResourceOwnershipConflictRollsBackWholeResult(t *testing.T) {
	p, _ := pendingProvision(t)
	a, b := prepareProvision(t, p), prepareProvision(t, p)
	if err := p.store.WriteProvision(t.Context(), provisionUpdate(a, "SUCCEEDED", "already-owned")); err != nil {
		t.Fatal(err)
	}
	u := provisionUpdate(b, "SUCCEEDED", "fresh-before-conflict")
	u.Resources = append(u.Resources, repository.ProvisionResource{Type: "SERVER", ProviderID: "already-owned", Generation: 1, LogicalName: "vm-1", ObservedState: "ACTIVE"})
	if err := p.store.WriteProvision(t.Context(), u); !errors.Is(err, repository.ErrConstraintViolation) {
		t.Fatalf("ownership conflict=%v", err)
	}
	if n := countRows(t, p.testDB, `SELECT count(*) FROM provider_resources`); n != 1 {
		t.Fatalf("partial result survived: %d", n)
	}
	items, _ := p.store.OperationItems(t.Context(), b.OperationID)
	for _, i := range items {
		if i.ID == b.Lease.ItemID && i.Status != "RUNNING" {
			t.Fatalf("conflicting item=%+v", i)
		}
	}
}

func TestProvisionPrepareIsOnceOnlyAndDoesNotReinterpretSnapshot(t *testing.T) {
	p, _ := pendingProvision(t)
	mustExec(t, p.testDB, `UPDATE provider_image_mappings SET provider_image_id='new-image',is_active=false WHERE id=$1`, p.f.ImageMappingID)
	mustExec(t, p.testDB, `UPDATE lab_specs SET startup_script='new-script',revision=revision+1`)
	d := prepareProvision(t, p)
	if _, err := p.store.PrepareProvision(t.Context(), d.Lease); !errors.Is(err, repository.ErrConstraintViolation) {
		t.Fatalf("second activation=%v", err)
	}
	var image, script string
	if err := p.pool.QueryRow(t.Context(), `SELECT snapshot->'vms'->0->>'imageId',snapshot->'startupScript'->>'content' FROM creation_snapshots`).Scan(&image, &script); err != nil {
		t.Fatal(err)
	}
	if image == "new-image" || script == "new-script" {
		t.Fatal("snapshot reinterpreted")
	}
}

func TestProvisionClaimLeavesResetCleanupPending(t *testing.T) {
	for _, kind := range []string{"RESET", "CLEANUP"} {
		t.Run(kind, func(t *testing.T) {
			p, op := pendingProvision(t)
			if kind == "RESET" {
				var li uuid.UUID
				if err := p.pool.QueryRow(t.Context(), `SELECT lab_instance_id FROM operation_items ORDER BY id LIMIT 1`).Scan(&li); err != nil {
					t.Fatal(err)
				}
				mustExec(t, p.testDB, `UPDATE operations SET operation_type='RESET',lab_instance_id=$2 WHERE id=$1`, op, li)
			} else {
				mustExec(t, p.testDB, `UPDATE operations SET operation_type='CLEANUP' WHERE id=$1`, op)
			}
			if _, err := p.store.ClaimProvisionOperationItem(t.Context(), uuid.New(), time.Minute); !errors.Is(err, repository.ErrNotFound) {
				t.Fatalf("unsupported claim=%v", err)
			}
			if n := countRows(t, p.testDB, `SELECT count(*) FROM operation_items WHERE status='PENDING' AND lease_owner IS NULL`); n != 3 {
				t.Fatalf("pending=%d", n)
			}
		})
	}
}
