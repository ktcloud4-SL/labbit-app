package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

var _ repository.ReconciliationStore = (*Store)(nil)

func (s *Store) ClaimReconciliation(ctx context.Context, owner uuid.UUID, duration time.Duration) (repository.OperationItemClaim, error) {
	if owner == uuid.Nil || duration < time.Microsecond {
		return repository.OperationItemClaim{}, invalidProvision()
	}
	var c repository.OperationItemClaim
	err := s.inTransaction(ctx, func(ctx context.Context, q queries) error {
		err := q.db.QueryRow(ctx, `SELECT i.id,i.operation_id FROM operation_items i
 JOIN operations o ON o.id=i.operation_id AND o.organization_id=i.organization_id
 WHERE i.status IN ('RUNNING','RECONCILING') AND i.finished_at IS NULL
 AND (i.lease_expires_at IS NULL OR i.lease_expires_at<=clock_timestamp())
 AND o.status IN ('RUNNING','RECONCILING') AND o.finished_at IS NULL
 AND o.operation_type IN ('PROVISION','RESET','CLEANUP')
 ORDER BY i.lease_expires_at NULLS FIRST,i.created_at,i.id LIMIT 1 FOR UPDATE OF i,o SKIP LOCKED`).Scan(&c.Item.ID, &c.Item.OperationID)
		if err != nil {
			return normalize("ClaimReconciliation", err)
		}
		c.Lease = repository.OperationItemLease{ItemID: c.Item.ID, Owner: owner}
		err = q.db.QueryRow(ctx, `UPDATE operation_items SET status='RECONCILING',stage='RECONCILING',lease_owner=$2,
 lease_expires_at=clock_timestamp()+$3::bigint*interval '1 microsecond' WHERE id=$1 RETURNING lease_expires_at`, c.Item.ID, owner.String(), duration.Microseconds()).Scan(&c.ExpiresAt)
		if err != nil {
			return normalize("ClaimReconciliation", err)
		}
		var at time.Time
		if err = q.db.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&at); err != nil {
			return normalize("ClaimReconciliation", err)
		}
		if err = q.aggregateProvision(ctx, c.Item.OperationID, at); err != nil {
			return err
		}
		c.Operation, err = q.OperationByID(ctx, c.Item.OperationID)
		c.Item.Status = "RECONCILING"
		return err
	})
	if err != nil {
		return repository.OperationItemClaim{}, err
	}
	return c, nil
}

func (s *Store) RenewReconciliation(ctx context.Context, lease repository.OperationItemLease, duration time.Duration) (time.Time, error) {
	if duration < time.Microsecond {
		return time.Time{}, invalidProvision()
	}
	var at time.Time
	err := s.inTransaction(ctx, func(ctx context.Context, q queries) error {
		if _, err := q.lockReconciliation(ctx, lease); err != nil {
			return err
		}
		err := q.db.QueryRow(ctx, `UPDATE operation_items SET lease_expires_at=clock_timestamp()+$3::bigint*interval '1 microsecond'
 WHERE id=$1 AND lease_owner=$2 AND lease_expires_at>clock_timestamp() RETURNING lease_expires_at`, lease.ItemID, lease.Owner.String(), duration.Microseconds()).Scan(&at)
		if errors.Is(normalize("RenewReconciliation", err), repository.ErrNotFound) {
			return repository.ErrOperationLeaseLost
		}
		return normalize("RenewReconciliation", err)
	})
	return at, err
}

// All reconciliation writes use the same product/parent locks and post-lock DB
// clock fencing as the mutation writers. RESET may span both generations.
func (q queries) lockReconciliation(ctx context.Context, lease repository.OperationItemLease) (repository.ProvisionDispatch, error) {
	d := repository.ProvisionDispatch{Lease: lease}
	if lease.ItemID == uuid.Nil || lease.Owner == uuid.Nil {
		return d, repository.ErrOperationLeaseLost
	}
	err := q.db.QueryRow(ctx, `SELECT i.organization_id,i.operation_id,i.lab_instance_id,e.id,i.generation,
 s.provider_connection_id,p.connector_id,s.snapshot,o.operation_type
 FROM operation_items i JOIN operations o ON o.id=i.operation_id AND o.organization_id=i.organization_id
 JOIN lab_instances li ON li.id=i.lab_instance_id AND li.organization_id=i.organization_id
 JOIN lab_executions e ON e.id=li.lab_execution_id AND e.organization_id=i.organization_id
 JOIN creation_snapshots s ON s.lab_execution_id=e.id AND s.organization_id=e.organization_id AND s.schema_version=1
 JOIN provider_connections p ON p.id=s.provider_connection_id AND p.organization_id=s.organization_id
 JOIN connectors c ON c.id=p.connector_id AND c.organization_id=p.organization_id
 WHERE i.id=$1 AND i.lease_owner=$2 AND i.status='RECONCILING' AND i.finished_at IS NULL
 AND o.status IN ('RUNNING','RECONCILING') AND o.finished_at IS NULL AND e.finished_at IS NULL AND c.revoked_at IS NULL
 AND ((o.operation_type='PROVISION' AND o.lab_execution_id=e.id AND li.generation=i.generation)
 OR (o.operation_type='RESET' AND o.lab_instance_id=li.id AND i.generation=li.generation+1)
 OR (o.operation_type='CLEANUP' AND o.lab_execution_id=e.id AND i.generation=li.generation))
 FOR UPDATE OF i,o,li,e FOR SHARE OF s,p,c`, lease.ItemID, lease.Owner.String()).Scan(&d.OrganizationID, &d.OperationID, &d.LabInstanceID, &d.LabExecutionID, &d.Generation, &d.ProviderConnectionID, &d.ConnectorID, &d.Snapshot, &d.MutationType)
	if errors.Is(normalize("Reconciliation", err), repository.ErrNotFound) {
		return d, repository.ErrOperationLeaseLost
	}
	if err != nil {
		return d, normalize("Reconciliation", err)
	}
	var live bool
	if err = q.db.QueryRow(ctx, `SELECT lease_expires_at>clock_timestamp() FROM operation_items WHERE id=$1`, lease.ItemID).Scan(&live); err != nil {
		return d, normalize("Reconciliation", err)
	}
	if !live {
		return d, repository.ErrOperationLeaseLost
	}
	var snapshotConnection struct {
		ProviderConnectionID string `json:"providerConnectionId"`
	}
	if json.Unmarshal(d.Snapshot, &snapshotConnection) != nil || snapshotConnection.ProviderConnectionID != d.ProviderConnectionID.String() {
		return d, invalidProvision()
	}
	return d, nil
}

func (q queries) reconciliationResources(ctx context.Context, d repository.ProvisionDispatch) ([]repository.ProvisionResource, error) {
	// Include previously absent resources: a residual after CLEANUP must be observed.
	rows, err := q.db.Query(ctx, `SELECT resource_type,provider_id,COALESCE(logical_name,''),generation,COALESCE(observed_state,'')
 FROM provider_resources WHERE organization_id=$1 AND lab_instance_id=$2 AND provider_connection_id=$3 ORDER BY resource_type,provider_id FOR UPDATE`, d.OrganizationID, d.LabInstanceID, d.ProviderConnectionID)
	if err != nil {
		return nil, normalize("ReconciliationResources", err)
	}
	defer rows.Close()
	refs := make([]repository.ProvisionResource, 0)
	for rows.Next() {
		var r repository.ProvisionResource
		if err = rows.Scan(&r.Type, &r.ProviderID, &r.LogicalName, &r.Generation, &r.ObservedState); err != nil {
			return nil, normalize("ReconciliationResources", err)
		}
		refs = append(refs, r)
	}
	return refs, normalize("ReconciliationResources", rows.Err())
}
func (s *Store) PrepareReconciliation(ctx context.Context, lease repository.OperationItemLease) (repository.ProvisionDispatch, error) {
	var d repository.ProvisionDispatch
	err := s.inTransaction(ctx, func(ctx context.Context, q queries) error {
		var err error
		d, err = q.lockReconciliation(ctx, lease)
		if err != nil {
			return err
		}
		d.Resources, err = q.reconciliationResources(ctx, d)
		return err
	})
	if err != nil {
		return repository.ProvisionDispatch{}, err
	}
	return d, nil
}

func (s *Store) WriteReconciliation(ctx context.Context, u repository.ReconciliationUpdate) error {
	if u.RetryDelay < time.Microsecond {
		return invalidProvision()
	}
	return s.inTransaction(ctx, func(ctx context.Context, q queries) error {
		d, err := q.lockReconciliation(ctx, u.Dispatch.Lease)
		if err != nil {
			return err
		}
		if !sameProvisionDispatch(d, u.Dispatch) || d.MutationType != u.Dispatch.MutationType {
			return invalidProvision()
		}
		refs, err := q.reconciliationResources(ctx, d)
		if err != nil {
			return err
		}
		// Require the prepared set to be unchanged. Ownership is never inferred from
		// provider names/tags or a response's generation alone.
		if len(refs) != len(u.Dispatch.Resources) {
			return invalidProvision()
		}
		tracked := make(map[[2]string]repository.ProvisionResource, len(refs))
		for i, r := range refs {
			if r != u.Dispatch.Resources[i] {
				return invalidProvision()
			}
			tracked[[2]string{r.Type, r.ProviderID}] = r
		}
		complete := !u.QueryFailed
		var unsafe bool
		if err = q.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_resources WHERE lab_instance_id=$1 AND
 (organization_id<>$2 OR provider_connection_id<>$3 OR generation>$4))`, d.LabInstanceID, d.OrganizationID, d.ProviderConnectionID, d.Generation).Scan(&unsafe); err != nil {
			return normalize("Reconciliation", err)
		}
		complete = complete && !unsafe
		seen := map[[2]string]bool{}
		valid := make([]repository.ResourceObservation, 0, len(u.Observations))
		// Validate the entire batch before persisting it. Contradictory/duplicate
		// observations cannot partially overwrite a prior confirmed state.
		for _, o := range u.Observations {
			key := [2]string{o.Type, o.ProviderID}
			r, known := tracked[key]
			if o.Source != "KNOWN_RESOURCE" || !known {
				complete = false
				continue
			}
			if seen[key] || o.Generation != r.Generation || o.LogicalName != r.LogicalName ||
				(o.Exists && (o.ObservedState == "" || o.ObservedState == "UNKNOWN" || absent(o.ObservedState))) ||
				(!o.Exists && o.ObservedState != "" && !absent(o.ObservedState)) {
				return invalidProvision()
			}
			seen[key] = true
			valid = append(valid, o)
			if o.Exists {
				complete = false
			}
		}
		if len(seen) != len(tracked) {
			complete = false
		}
		var at time.Time
		if err = q.db.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&at); err != nil {
			return normalize("Reconciliation", err)
		}
		if !u.QueryFailed && !unsafe {
			for _, o := range valid {
				state, lifecycle := o.ObservedState, "PRESENT"
				if !o.Exists {
					state = "MISSING"
					lifecycle = "MISSING"
				}
				_, err = q.db.Exec(ctx, `UPDATE provider_resources SET lifecycle_status=$6,observed_state=$7,last_observed_at=$8,updated_at=$8,deleted_at=CASE WHEN $6='PRESENT' THEN NULL ELSE deleted_at END
 WHERE organization_id=$1 AND lab_instance_id=$2 AND provider_connection_id=$3 AND resource_type=$4 AND provider_id=$5`, d.OrganizationID, d.LabInstanceID, d.ProviderConnectionID, o.Type, o.ProviderID, lifecycle, state, at)
				if err != nil {
					return normalize("ReconciliationResource", err)
				}
			}
		}
		// Existence observations do not prove creation completeness/startup readiness.
		// Only CLEANUP's complete absence proof can finish without another mutation.
		status, stage := "RECONCILING", "HOLD"
		var finished *time.Time
		if complete && d.MutationType == "CLEANUP" {
			status = "SUCCEEDED"
			stage = "COMPLETED"
			finished = &at
		}
		tag, err := q.db.Exec(ctx, `UPDATE operation_items SET status=$2,stage=$3,finished_at=$4,lease_owner=NULL,
 lease_expires_at=CASE WHEN $2='RECONCILING' THEN clock_timestamp()+$5::bigint*interval '1 microsecond' ELSE NULL END,
 error_code=CASE WHEN $2='RECONCILING' THEN 'reconciliation_hold' ELSE NULL END,
 error_message=CASE WHEN $2='RECONCILING' THEN 'Operation outcome requires reconciliation' ELSE NULL END
 WHERE id=$1 AND lease_expires_at>clock_timestamp()`, d.Lease.ItemID, status, stage, finished, u.RetryDelay.Microseconds())
		if err != nil {
			return normalize("Reconciliation", err)
		}
		if tag.RowsAffected() != 1 {
			return repository.ErrOperationLeaseLost
		}
		if status == "SUCCEEDED" {
			if _, err = q.db.Exec(ctx, `UPDATE lab_instances SET status='COMPLETED',updated_at=$2 WHERE id=$1`, d.LabInstanceID, at); err != nil {
				return normalize("Reconciliation", err)
			}
		}
		if d.MutationType == "CLEANUP" {
			return q.aggregateMutation(ctx, d, at)
		}
		return q.aggregateProvision(ctx, d.OperationID, at)
	})
}
