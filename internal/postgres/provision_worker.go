package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

var _ repository.ProvisionWorkerStore = (*Store)(nil)

func invalidProvision() error {
	return &repository.Error{Kind: repository.KindConstraintViolation, Op: "Provision"}
}

// lockProvision uses the same item/parent fencing as Claim/Renew, also locking
// product relationships against generation changes and connection reassignment.
// Time is checked in a separate statement AFTER all row locks have been acquired.
func (q queries) lockProvision(ctx context.Context, lease repository.OperationItemLease) (repository.ProvisionDispatch, string, error) {
	d := repository.ProvisionDispatch{Lease: lease}
	if lease.ItemID == uuid.Nil || lease.Owner == uuid.Nil {
		return d, "", repository.ErrOperationLeaseLost
	}
	var stage string
	err := q.db.QueryRow(ctx, `SELECT i.organization_id,i.operation_id,i.lab_instance_id,
 li.lab_execution_id,i.generation,s.provider_connection_id,p.connector_id,s.snapshot,COALESCE(i.stage,'')
 FROM operation_items i
 JOIN operations o ON o.id=i.operation_id AND o.organization_id=i.organization_id
 JOIN lab_instances li ON li.id=i.lab_instance_id AND li.organization_id=i.organization_id AND li.generation=i.generation
 JOIN lab_executions e ON e.id=li.lab_execution_id AND e.organization_id=i.organization_id AND e.id=o.lab_execution_id
 JOIN creation_snapshots s ON s.lab_execution_id=e.id AND s.organization_id=e.organization_id AND s.schema_version=1
 JOIN provider_connections p ON p.id=s.provider_connection_id AND p.organization_id=s.organization_id
 JOIN connectors c ON c.id=p.connector_id AND c.organization_id=p.organization_id
 WHERE i.id=$1 AND i.lease_owner=$2 AND i.status='RUNNING' AND i.finished_at IS NULL
 AND o.operation_type='PROVISION' AND o.status='RUNNING' AND o.finished_at IS NULL
 AND e.finished_at IS NULL AND c.revoked_at IS NULL
 FOR UPDATE OF i,o,li,e FOR SHARE OF s,p,c`, lease.ItemID, lease.Owner.String()).Scan(
		&d.OrganizationID, &d.OperationID, &d.LabInstanceID, &d.LabExecutionID, &d.Generation,
		&d.ProviderConnectionID, &d.ConnectorID, &d.Snapshot, &stage)
	if err != nil {
		if errors.Is(normalize("Provision", err), repository.ErrNotFound) {
			return d, "", repository.ErrOperationLeaseLost
		}
		return d, "", normalize("Provision", err)
	}
	var live bool
	if err := q.db.QueryRow(ctx, `SELECT lease_expires_at > clock_timestamp() FROM operation_items WHERE id=$1`, lease.ItemID).Scan(&live); err != nil {
		return d, "", normalize("Provision", err)
	}
	if !live {
		return d, "", repository.ErrOperationLeaseLost
	}
	var snapshotConnection struct {
		ProviderConnectionID string `json:"providerConnectionId"`
	}
	if json.Unmarshal(d.Snapshot, &snapshotConnection) != nil || snapshotConnection.ProviderConnectionID != d.ProviderConnectionID.String() {
		return d, "", invalidProvision()
	}
	return d, stage, nil
}

func (s *Store) PrepareProvision(ctx context.Context, lease repository.OperationItemLease) (repository.ProvisionDispatch, error) {
	var dispatch repository.ProvisionDispatch
	err := s.inTransaction(ctx, func(ctx context.Context, q queries) error {
		d, stage, err := q.lockProvision(ctx, lease)
		if err != nil {
			return err
		}
		if (stage != "" && stage != "DEFERRED") || d.Generation != 1 {
			return invalidProvision()
		}
		var eligible bool
		err = q.db.QueryRow(ctx, `SELECT status='PENDING' AND NOT EXISTS (
 SELECT 1 FROM provider_resources WHERE lab_instance_id=$1)
 FROM lab_instances WHERE id=$1`, d.LabInstanceID).Scan(&eligible)
		if err != nil {
			return normalize("PrepareProvision", err)
		}
		if !eligible {
			return invalidProvision()
		}
		_, err = q.db.Exec(ctx, `UPDATE operation_items SET stage='DISPATCHING' WHERE id=$1`, lease.ItemID)
		if err != nil {
			return normalize("PrepareProvision", err)
		}
		_, err = q.db.Exec(ctx, `UPDATE lab_instances SET status='PROVISIONING',updated_at=clock_timestamp() WHERE id=$1`, d.LabInstanceID)
		dispatch = d
		return normalize("PrepareProvision", err)
	})
	if err != nil {
		return repository.ProvisionDispatch{}, err
	}
	return dispatch, nil
}

func sameProvisionDispatch(a, b repository.ProvisionDispatch) bool {
	return a.OrganizationID == b.OrganizationID && a.OperationID == b.OperationID && a.LabInstanceID == b.LabInstanceID &&
		a.LabExecutionID == b.LabExecutionID && a.Generation == b.Generation && a.ConnectorID == b.ConnectorID && a.ProviderConnectionID == b.ProviderConnectionID
}

func (s *Store) DeferUnsentProvision(ctx context.Context, expected repository.ProvisionDispatch, delay time.Duration) error {
	if delay < time.Microsecond {
		return invalidProvision()
	}
	return s.inTransaction(ctx, func(ctx context.Context, q queries) error {
		d, stage, err := q.lockProvision(ctx, expected.Lease)
		if err != nil {
			return err
		}
		if stage != "DISPATCHING" || !sameProvisionDispatch(d, expected) {
			return invalidProvision()
		}
		var eligible bool
		if err := q.db.QueryRow(ctx, `SELECT status='PROVISIONING' AND NOT EXISTS (
 SELECT 1 FROM provider_resources WHERE lab_instance_id=$1)
 FROM lab_instances WHERE id=$1`, d.LabInstanceID).Scan(&eligible); err != nil {
			return normalize("DeferUnsentProvision", err)
		}
		if !eligible {
			return invalidProvision()
		}
		var at time.Time
		// Locks are held and time is checked at the transition, as in the result
		// writer. Commit failure cannot authorize another external dispatch.
		err = q.db.QueryRow(ctx, `WITH timing AS MATERIALIZED (SELECT clock_timestamp() AS at)
 UPDATE operation_items SET status='PENDING',stage='DEFERRED',lease_owner=NULL,lease_expires_at=NULL,
 dispatch_not_before=timing.at+$2::bigint*interval '1 microsecond',error_code=NULL,error_message=NULL
 FROM timing WHERE id=$1 AND lease_expires_at>timing.at RETURNING timing.at`, d.Lease.ItemID, delay.Microseconds()).Scan(&at)
		if err != nil {
			if errors.Is(normalize("DeferUnsentProvision", err), repository.ErrNotFound) {
				return repository.ErrOperationLeaseLost
			}
			return normalize("DeferUnsentProvision", err)
		}
		_, err = q.db.Exec(ctx, `UPDATE lab_instances SET status='PENDING',updated_at=$2 WHERE id=$1`, d.LabInstanceID, at)
		if err != nil {
			return normalize("DeferUnsentProvision", err)
		}
		return q.aggregateProvision(ctx, d.OperationID, at)
	})
}

func (s *Store) WriteProvision(ctx context.Context, u repository.ProvisionUpdate) error {
	if u.Status != "RUNNING" && u.Status != "SUCCEEDED" && u.Status != "FAILED" && u.Status != "RECONCILING" {
		return invalidProvision()
	}
	return s.inTransaction(ctx, func(ctx context.Context, q queries) error {
		d, stage, err := q.lockProvision(ctx, u.Dispatch.Lease)
		if err != nil {
			return err
		}
		if stage == "" || !sameProvisionDispatch(d, u.Dispatch) {
			return invalidProvision()
		}
		var at time.Time
		if err := q.db.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&at); err != nil {
			return normalize("WriteProvision", err)
		}
		seen := make(map[[2]string]bool)
		for _, r := range u.Resources {
			key := [2]string{r.Type, r.ProviderID}
			if r.Generation != d.Generation || strings.TrimSpace(r.Type) == "" || strings.TrimSpace(r.ProviderID) == "" || seen[key] {
				return invalidProvision()
			}
			seen[key] = true
			lifecycle := "PRESENT"
			var deletedAt *time.Time
			switch r.ObservedState {
			case "DELETED":
				lifecycle = "DELETED"
				deletedAt = &at
			case "MISSING", "ABSENT":
				lifecycle = "MISSING"
			}
			// An exact ID already owned by another instance/generation/item cannot
			// be adopted. A mismatch rolls back every resource and state write.
			tag, err := q.db.Exec(ctx, `INSERT INTO provider_resources
 (id,organization_id,lab_instance_id,provider_connection_id,generation,resource_type,logical_name,provider_id,
 lifecycle_status,observed_state,created_by_operation_item_id,last_observed_at,deleted_at,created_at,updated_at)
 VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,NULLIF($10,''),$11,$12,$13,$12,$12)
 ON CONFLICT (provider_connection_id,resource_type,provider_id) DO UPDATE
 SET lifecycle_status=EXCLUDED.lifecycle_status,observed_state=EXCLUDED.observed_state,
 last_observed_at=EXCLUDED.last_observed_at,deleted_at=EXCLUDED.deleted_at,updated_at=EXCLUDED.updated_at
 WHERE provider_resources.organization_id=EXCLUDED.organization_id
 AND provider_resources.lab_instance_id=EXCLUDED.lab_instance_id AND provider_resources.generation=EXCLUDED.generation
 AND provider_resources.created_by_operation_item_id=EXCLUDED.created_by_operation_item_id
 AND provider_resources.logical_name IS NOT DISTINCT FROM EXCLUDED.logical_name`, uuid.New(), d.OrganizationID, d.LabInstanceID,
				d.ProviderConnectionID, d.Generation, r.Type, r.LogicalName, r.ProviderID, lifecycle, r.ObservedState, d.Lease.ItemID, at, deletedAt)
			if err != nil {
				return normalize("WriteProvisionResource", err)
			}
			if tag.RowsAffected() != 1 {
				return invalidProvision()
			}
		}
		ready := validReadiness(d, u)
		if u.Readiness != nil && !ready {
			u.Status = "RECONCILING"
			u.Stage = "RECONCILING"
			u.ErrorCode = "readiness_attestation_invalid"
			u.ErrorMessage = "Workspace readiness requires reconciliation"
		}
		if ready {
			u.Stage = "READY"
		}
		var finishedAt *time.Time
		if u.Status == "SUCCEEDED" || u.Status == "FAILED" {
			finishedAt = &at
		}
		// Recheck expiry immediately at the state write too: inserting a large
		// resource set must not let the transaction outlive its authorization.
		tag, err := q.db.Exec(ctx, `UPDATE operation_items SET status=$2,stage=NULLIF($3,''),error_code=NULLIF($4,''),
 error_message=NULLIF($5,''),finished_at=$6,
 lease_owner=CASE WHEN $2='RUNNING' THEN lease_owner ELSE NULL END,
 lease_expires_at=CASE WHEN $2='RUNNING' THEN lease_expires_at ELSE NULL END
 WHERE id=$1 AND lease_expires_at>clock_timestamp()`, d.Lease.ItemID, u.Status, u.Stage, u.ErrorCode, u.ErrorMessage, finishedAt)
		if err != nil {
			return normalize("WriteProvision", err)
		}
		if tag.RowsAffected() != 1 {
			return repository.ErrOperationLeaseLost
		}
		if u.Status == "FAILED" {
			_, err = q.db.Exec(ctx, `UPDATE lab_instances SET status='ERROR',updated_at=$2 WHERE id=$1`, d.LabInstanceID, at)
			if err != nil {
				return normalize("WriteProvisionInstance", err)
			}
		}
		if ready {
			if _, err = q.db.Exec(ctx, `UPDATE lab_instances SET status='READY',updated_at=$2 WHERE id=$1`, d.LabInstanceID, at); err != nil {
				return normalize("WriteProvisionReady", err)
			}
		}
		if err = q.aggregateReadiness(ctx, d.LabExecutionID, at); err != nil {
			return err
		}
		return q.aggregateProvision(ctx, d.OperationID, at)
	})
}

func (q queries) aggregateProvision(ctx context.Context, id uuid.UUID, at time.Time) error {
	// The parent lock serializes sibling writers. Pending/running siblings keep
	// executing even when another sibling fails or needs reconciliation.
	_, err := q.db.Exec(ctx, `WITH aggregate AS (
 SELECT CASE WHEN bool_and(status='PENDING') THEN 'PENDING'
 WHEN bool_or(status IN ('PENDING','RUNNING')) THEN 'RUNNING'
 WHEN bool_or(status='RECONCILING') THEN 'RECONCILING'
 WHEN bool_or(status='FAILED') THEN 'FAILED' ELSE 'SUCCEEDED' END AS status
 FROM operation_items WHERE operation_id=$1
 ), failure AS (
 SELECT error_code,error_message FROM operation_items
 WHERE operation_id=$1 AND status IN ('FAILED','RECONCILING') ORDER BY created_at,id LIMIT 1
 ) UPDATE operations SET status=a.status,updated_at=$2::timestamptz,
 finished_at=CASE WHEN a.status IN ('SUCCEEDED','FAILED') THEN $2::timestamptz ELSE NULL END,
 stage=CASE WHEN a.status='SUCCEEDED' THEN CASE WHEN NOT EXISTS(SELECT 1 FROM operation_items WHERE operation_id=$1 AND stage IS DISTINCT FROM 'READY') THEN 'READY' ELSE 'VERIFYING_READINESS' END
 WHEN a.status='PENDING' THEN 'DEFERRED'
 WHEN a.status='RECONCILING' THEN 'RECONCILING' WHEN a.status='FAILED' THEN 'FAILED' ELSE 'PROVISIONING' END,
 error_code=(SELECT error_code FROM failure),error_message=(SELECT error_message FROM failure)
 FROM aggregate a WHERE operations.id=$1`, id, at)
	return normalize("AggregateProvision", err)
}
