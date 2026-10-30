package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/operationrecovery"
)

// FailedProvisionRecoveryTarget is read-only. A target cannot be obtained for
// running mutations, successful/finished items, Reset or a changed generation.
func (s *Store) FailedProvisionRecoveryTarget(ctx context.Context, item uuid.UUID) (operationrecovery.Target, error) {
	return s.queries.failedProvisionRecoveryTarget(ctx, item, false)
}

func (q queries) failedProvisionRecoveryTarget(ctx context.Context, item uuid.UUID, lock bool) (operationrecovery.Target, error) {
	var t operationrecovery.Target
	var snapshot []byte
	sql := `SELECT i.id,i.organization_id,i.operation_id,i.lab_instance_id,e.id,p.id,c.id,i.generation,s.snapshot
 FROM operation_items i
 JOIN operations o ON o.id=i.operation_id AND o.organization_id=i.organization_id
 JOIN lab_instances li ON li.id=i.lab_instance_id AND li.organization_id=i.organization_id AND li.generation=i.generation
 JOIN lab_executions e ON e.id=li.lab_execution_id AND e.organization_id=li.organization_id AND e.id=o.lab_execution_id
 JOIN creation_snapshots s ON s.lab_execution_id=e.id AND s.organization_id=e.organization_id AND s.schema_version=1
 JOIN provider_connections p ON p.id=s.provider_connection_id AND p.organization_id=s.organization_id
 JOIN connectors c ON c.id=p.connector_id AND c.organization_id=p.organization_id
 WHERE i.id=$1 AND i.generation=1 AND i.status='RECONCILING' AND i.stage='HOLD' AND i.finished_at IS NULL
 AND (i.lease_owner IS NULL OR i.lease_expires_at<=clock_timestamp())
 AND o.operation_type='PROVISION' AND o.status='RECONCILING' AND o.finished_at IS NULL
 AND li.status='PROVISIONING' AND e.finished_at IS NULL AND c.revoked_at IS NULL
 AND NOT EXISTS(SELECT 1 FROM operation_items sibling WHERE sibling.lab_instance_id=li.id AND sibling.id<>i.id AND sibling.status IN ('PENDING','RUNNING','RECONCILING'))`
	if lock {
		sql += ` FOR UPDATE OF i,o,li,e FOR SHARE OF s,p,c`
	}
	if err := q.db.QueryRow(ctx, sql, item).Scan(&t.ItemID, &t.OrganizationID, &t.OperationID, &t.InstanceID, &t.ExecutionID, &t.ConnectionID, &t.ConnectorID, &t.Generation, &snapshot); err != nil {
		return t, normalize("FailedProvisionRecoveryTarget", err)
	}
	var parsed struct {
		ProviderConnectionID string `json:"providerConnectionId"`
		WorkspaceVMKey       string `json:"workspaceVmKey"`
		VMs                  []struct {
			VMKey string `json:"vmKey"`
		} `json:"vms"`
	}
	if json.Unmarshal(snapshot, &parsed) != nil || parsed.ProviderConnectionID != t.ConnectionID.String() || len(parsed.VMs) != 1 || parsed.VMs[0].VMKey == "" || parsed.WorkspaceVMKey != parsed.VMs[0].VMKey {
		return t, operationrecovery.ErrUnsafe
	}
	t.SnapshotSHA256 = operationrecovery.SHA256(snapshot)
	t.VMKey = parsed.VMs[0].VMKey
	return t, nil
}

// RecoverFailedProvision is the dedicated operator writer. It never changes a
// HOLD to RUNNING or replays a mutation. All checks and writes share one tx.
func (s *Store) RecoverFailedProvision(ctx context.Context, plan operationrecovery.Plan, expectedDigest string) error {
	if expectedDigest == "" || plan.Digest() != expectedDigest || operationrecovery.ValidateResources(plan.Target.VMKey, plan.Target.Generation, plan.Resources) != nil {
		return operationrecovery.ErrUnsafe
	}
	return s.inTransaction(ctx, func(ctx context.Context, q queries) error {
		// Bound lock contention. An operator must not hold this tx while doing SSH
		// or Provider I/O; all fresh external evidence is gathered before entry.
		if _, err := q.db.Exec(ctx, `SET LOCAL lock_timeout='3s'`); err != nil {
			return normalize("RecoverFailedProvision", err)
		}
		t, err := q.failedProvisionRecoveryTarget(ctx, plan.Target.ItemID, true)
		if err != nil {
			return err
		}
		if t != plan.Target {
			return operationrecovery.ErrUnsafe
		}
		// The exact original item must have lost its entire result, not merely a
		// subset that this narrowly supported path could overwrite or discard.
		var tracked bool
		if err = q.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_resources WHERE lab_instance_id=$1)`, t.InstanceID).Scan(&tracked); err != nil {
			return normalize("RecoverFailedProvision", err)
		}
		if tracked {
			return operationrecovery.ErrUnsafe
		}
		owner := uuid.New().String()
		tag, err := q.db.Exec(ctx, `UPDATE operation_items SET lease_owner=$2,lease_expires_at=clock_timestamp()+interval '30 seconds'
 WHERE id=$1 AND status='RECONCILING' AND stage='HOLD' AND (lease_owner IS NULL OR lease_expires_at<=clock_timestamp())`, t.ItemID, owner)
		if err != nil {
			return normalize("RecoverFailedProvision", err)
		}
		if tag.RowsAffected() != 1 {
			return operationrecovery.ErrUnsafe
		}
		var at time.Time
		if err = q.db.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&at); err != nil {
			return normalize("RecoverFailedProvision", err)
		}
		for _, r := range plan.Resources {
			// Unique affinity rejects ownership conflicts atomically, including
			// writes by another instance racing this restoration.
			_, err = q.db.Exec(ctx, `INSERT INTO provider_resources
 (id,organization_id,lab_instance_id,provider_connection_id,generation,resource_type,logical_name,provider_id,lifecycle_status,observed_state,created_by_operation_item_id,last_observed_at,created_at,updated_at)
 VALUES($1,$2,$3,$4,1,$5,$6,$7,'PRESENT',$8,$9,$10,$10,$10)`, uuid.New(), t.OrganizationID, t.InstanceID, t.ConnectionID, r.Type, r.LogicalName, r.ProviderID, r.ObservedState, t.ItemID, at)
			if err != nil {
				return normalize("RecoverFailedProvisionResource", err)
			}
		}
		_, err = q.db.Exec(ctx, `INSERT INTO failed_provision_recoveries
 (operation_item_id,organization_id,provider_connection_id,connector_id,plan_sha256,receipt_sha256,ledger_sha256,ssh_host_key,operator_label,reason,recovered_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, t.ItemID, t.OrganizationID, t.ConnectionID, t.ConnectorID, expectedDigest, plan.ReceiptSHA256, plan.LedgerSHA256, plan.SSHHostKey, plan.Operator, plan.Reason, at)
		if err != nil {
			return normalize("RecoverFailedProvisionAudit", err)
		}
		tag, err = q.db.Exec(ctx, `UPDATE operation_items SET status='FAILED',stage='RECOVERED_FAILED',error_code='provision_failed_recovered',
 error_message='Original Connector failure restored by trusted operator',finished_at=$3,lease_owner=NULL,lease_expires_at=NULL
 WHERE id=$1 AND lease_owner=$2 AND lease_expires_at>clock_timestamp() AND status='RECONCILING'`, t.ItemID, owner, at)
		if err != nil {
			return normalize("RecoverFailedProvision", err)
		}
		if tag.RowsAffected() != 1 {
			return operationrecovery.ErrUnsafe
		}
		if _, err = q.db.Exec(ctx, `UPDATE lab_instances SET status='ERROR',updated_at=$2 WHERE id=$1 AND generation=1 AND status='PROVISIONING'`, t.InstanceID, at); err != nil {
			return normalize("RecoverFailedProvision", err)
		}
		if err = q.aggregateReadiness(ctx, t.ExecutionID, at); err != nil {
			return err
		}
		return q.aggregateProvision(ctx, t.OperationID, at)
	})
}
