//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/operationrecovery"
	"github.com/ktcloud4-SL/labbit-app/internal/server/labexecution"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

func failureRecoveryFixture(t *testing.T) (*provisionDB, operationrecovery.Plan, repository.ProvisionDispatch) {
	t.Helper()
	p := newProvisionDB(t)
	if err := p.store.WithinTransaction(t.Context(), func(ctx context.Context, r repository.Repositories) error {
		current, err := r.LabSpecForUpdate(ctx, p.f.LabSpecID)
		if err != nil {
			return err
		}
		_, ok, err := r.UpdateLabSpec(ctx, repository.LabSpecUpdate{ID: p.f.LabSpecID, OrganizationID: p.f.OrganizationID, ExpectedRevision: current.Revision, LabSpecDefinition: repository.LabSpecDefinition{Name: "Recovery fixture", WorkspaceRole: "solo", WorkspaceInstanceIndex: 0, VMRoles: []repository.NewLabSpecVMRole{{ID: uuid.New(), Role: "solo", ImageMappingID: p.f.ImageMappingID, FlavorMappingID: p.f.FlavorMappingID, VMCount: 1}}}})
		if !ok {
			return errors.New("fixture update failed")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_, err := p.svc.Provision(t.Context(), p.user(t, p.f.InstructorID), p.input("failed-recovery", p.f.StudentAID))
	if err != nil {
		t.Fatal(err)
	}
	d := prepareProvision(t, p)
	sibling := prepareProvision(t, p)
	if err = p.store.WriteProvision(t.Context(), repository.ProvisionUpdate{Dispatch: d, Status: "RECONCILING", Stage: "RECONCILING", ErrorCode: "provision_response_unconfirmed"}); err != nil {
		t.Fatal(err)
	}
	if err = p.store.WriteProvision(t.Context(), repository.ProvisionUpdate{Dispatch: sibling, Status: "FAILED", Stage: "FAILED", ErrorCode: "fixture_failed"}); err != nil {
		t.Fatal(err)
	}
	claim, err := p.store.ClaimReconciliation(t.Context(), uuid.New(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	rd, err := p.store.PrepareReconciliation(t.Context(), claim.Lease)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.store.WriteReconciliation(t.Context(), repository.ReconciliationUpdate{Dispatch: rd, RetryDelay: time.Second}); err != nil {
		t.Fatal(err)
	}
	target, err := p.store.FailedProvisionRecoveryTarget(t.Context(), d.Lease.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	req := operationrecovery.Request{OperationID: target.OperationID, InstanceID: target.InstanceID, ConnectionID: target.ConnectionID, Generation: 1, ProjectID: "fixture-project", VMKey: target.VMKey, Nonce: operationrecovery.SHA256([]byte("fixture-nonce"))}
	e := operationrecovery.Evidence{Request: req, Outcome: "FAILED", LedgerSHA256: operationrecovery.SHA256([]byte("fixture-ledger")), ReceiptSHA256: operationrecovery.SHA256([]byte("fixture-receipt")), ObservedAt: time.Now().UTC()}
	for name, kind := range map[string]string{"lab-tier": "KT_TIER", target.VMKey + ":management-tier": "KT_TIER", target.VMKey + ":management-ssh": "KT_FIREWALL_POLICY", target.VMKey: "SERVER", target.VMKey + ":root-volume": "VOLUME"} {
		e.Resources = append(e.Resources, operationrecovery.Resource{Type: kind, ProviderID: uuid.NewString(), LogicalName: name, Generation: 1, ObservedState: "PRESENT"})
	}
	plan, err := operationrecovery.Validate(target, req, e, "SHA256:"+strings.Repeat("a", 43), "fixture-operator", "LBT-197", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return p, plan, sibling
}

func TestFailedProvisionRecoveryAtomicAndOfficialCleanup(t *testing.T) {
	p, plan, _ := failureRecoveryFixture(t)
	if _, err := p.svc.Cleanup(t.Context(), p.user(t, p.f.InstructorID), labexecution.CleanupInput{LabExecutionID: plan.Target.ExecutionID.String(), IdempotencyKey: "before-recover"}); err == nil {
		t.Fatal("HOLD allowed Cleanup")
	}
	if err := p.store.RecoverFailedProvision(t.Context(), plan, plan.Digest()); err != nil {
		t.Fatal(err)
	}
	var resources, audits int
	var item, instance, parent string
	if err := p.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM provider_resources WHERE lab_instance_id=$1),
 (SELECT count(*) FROM failed_provision_recoveries WHERE operation_item_id=$2),
 (SELECT status FROM operation_items WHERE id=$2),(SELECT status FROM lab_instances WHERE id=$1),
 (SELECT status FROM operations WHERE id=$3)`, plan.Target.InstanceID, plan.Target.ItemID, plan.Target.OperationID).Scan(&resources, &audits, &item, &instance, &parent); err != nil {
		t.Fatal(err)
	}
	if resources != 5 || audits != 1 || item != "FAILED" || instance != "ERROR" || parent != "FAILED" {
		t.Fatalf("resources/audit/status %d %d %s %s %s", resources, audits, item, instance, parent)
	}
	if err := p.store.RecoverFailedProvision(t.Context(), plan, plan.Digest()); err == nil {
		t.Fatal("replay restored twice")
	}
	if _, err := p.svc.Cleanup(t.Context(), p.user(t, p.f.InstructorID), labexecution.CleanupInput{LabExecutionID: plan.Target.ExecutionID.String(), IdempotencyKey: "after-recover"}); err != nil {
		t.Fatal(err)
	}
}

func TestFailedProvisionRecoveryRejectsStaleLeaseOwnershipAndPlan(t *testing.T) {
	for _, scenario := range []string{"wrong hash", "snapshot changed", "connection changed", "active lease", "foreign resource", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			p, plan, sibling := failureRecoveryFixture(t)
			digest := plan.Digest()
			ctx := t.Context()
			switch scenario {
			case "wrong hash":
				digest = operationrecovery.SHA256([]byte("wrong"))
			case "snapshot changed":
				plan.Target.SnapshotSHA256 = operationrecovery.SHA256([]byte("stale"))
				digest = plan.Digest()
			case "connection changed":
				plan.Target.ConnectionID = uuid.New()
				digest = plan.Digest()
			case "active lease":
				_, err := p.pool.Exec(ctx, `UPDATE operation_items SET lease_owner=$2,lease_expires_at=clock_timestamp()+interval '1 minute' WHERE id=$1`, plan.Target.ItemID, uuid.NewString())
				if err != nil {
					t.Fatal(err)
				}
			case "foreign resource":
				r := plan.Resources[len(plan.Resources)-1]
				_, err := p.pool.Exec(ctx, `INSERT INTO provider_resources(id,organization_id,lab_instance_id,provider_connection_id,generation,resource_type,logical_name,provider_id,lifecycle_status,created_by_operation_item_id) VALUES($1,$2,$3,$4,1,$5,$6,$7,'PRESENT',$8)`, uuid.New(), sibling.OrganizationID, sibling.LabInstanceID, sibling.ProviderConnectionID, r.Type, r.LogicalName, r.ProviderID, sibling.Lease.ItemID)
				if err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if err := p.store.RecoverFailedProvision(ctx, plan, digest); err == nil {
				t.Fatal("unsafe recovery accepted")
			}
			var refs, audits int
			var status string
			if err := p.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM provider_resources WHERE lab_instance_id=$1),(SELECT count(*) FROM failed_provision_recoveries WHERE operation_item_id=$2),(SELECT status FROM operation_items WHERE id=$2)`, plan.Target.InstanceID, plan.Target.ItemID).Scan(&refs, &audits, &status); err != nil {
				t.Fatal(err)
			}
			if refs != 0 || audits != 0 || status != "RECONCILING" {
				t.Fatal("rejected recovery leaked a partial write")
			}
		})
	}
}

func TestFailedProvisionRecoveryConcurrentOperatorRunsOnce(t *testing.T) {
	p, plan, _ := failureRecoveryFixture(t)
	out := make(chan error, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { <-start; out <- p.store.RecoverFailedProvision(t.Context(), plan, plan.Digest()) })
	}
	close(start)
	wg.Wait()
	close(out)
	success := 0
	for err := range out {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("successful recovery count=%d", success)
	}
}
