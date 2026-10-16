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

var _ repository.MutationWorkerStore = (*Store)(nil)

func invalidMutation() error {
	return &repository.Error{Kind: repository.KindConstraintViolation, Op: "Mutation"}
}

// Locks use the same ownership and post-lock clock checks as PROVISION. RESET
// targets current+1; the instance remains at current until deletion is proven.
func (q queries) lockMutation(ctx context.Context, lease repository.OperationItemLease) (repository.ProvisionDispatch, string, error) {
	d := repository.ProvisionDispatch{Lease: lease}
	var stage string
	if lease.ItemID == uuid.Nil || lease.Owner == uuid.Nil {
		return d, stage, repository.ErrOperationLeaseLost
	}
	err := q.db.QueryRow(ctx, `SELECT i.organization_id,i.operation_id,i.lab_instance_id,
 li.lab_execution_id,i.generation,s.provider_connection_id,p.connector_id,s.snapshot,o.operation_type,
 CASE WHEN COALESCE(i.stage,'') IN ('','DEFERRED') THEN li.status ELSE COALESCE(i.previous_instance_status,li.status) END,COALESCE(i.stage,'')
 FROM operation_items i
 JOIN operations o ON o.id=i.operation_id AND o.organization_id=i.organization_id
 JOIN lab_instances li ON li.id=i.lab_instance_id AND li.organization_id=i.organization_id
 JOIN lab_executions e ON e.id=li.lab_execution_id AND e.organization_id=i.organization_id
 JOIN creation_snapshots s ON s.lab_execution_id=e.id AND s.organization_id=e.organization_id AND s.schema_version=1
 JOIN provider_connections p ON p.id=s.provider_connection_id AND p.organization_id=s.organization_id
 JOIN connectors c ON c.id=p.connector_id AND c.organization_id=p.organization_id
 WHERE i.id=$1 AND i.lease_owner=$2 AND i.status='RUNNING' AND i.finished_at IS NULL
 AND o.status='RUNNING' AND o.finished_at IS NULL AND e.finished_at IS NULL AND c.revoked_at IS NULL
 AND ((o.operation_type='RESET' AND o.lab_instance_id=li.id AND i.generation=li.generation+1)
 OR (o.operation_type='CLEANUP' AND o.lab_execution_id=e.id AND i.generation=li.generation))
 FOR UPDATE OF i,o,li,e FOR SHARE OF s,p,c`, lease.ItemID, lease.Owner.String()).Scan(
		&d.OrganizationID, &d.OperationID, &d.LabInstanceID, &d.LabExecutionID, &d.Generation,
		&d.ProviderConnectionID, &d.ConnectorID, &d.Snapshot, &d.MutationType, &d.PreviousStatus, &stage)
	if err != nil {
		if errors.Is(normalize("Mutation", err), repository.ErrNotFound) {
			return d, stage, repository.ErrOperationLeaseLost
		}
		return d, stage, normalize("Mutation", err)
	}
	var live bool
	if err := q.db.QueryRow(ctx, `SELECT lease_expires_at>clock_timestamp() FROM operation_items WHERE id=$1`, lease.ItemID).Scan(&live); err != nil {
		return d, stage, normalize("Mutation", err)
	}
	if !live {
		return d, stage, repository.ErrOperationLeaseLost
	}
	var snapshotConnection struct {
		ProviderConnectionID string `json:"providerConnectionId"`
	}
	if json.Unmarshal(d.Snapshot, &snapshotConnection) != nil || snapshotConnection.ProviderConnectionID != d.ProviderConnectionID.String() {
		return d, stage, invalidMutation()
	}
	return d, stage, nil
}

func (q queries) mutationResources(ctx context.Context, d repository.ProvisionDispatch) ([]repository.ProvisionResource, error) {
	// RESET needs the full tracked previous set, including already absent entries.
	// CLEANUP covers residual resources across every generation of this instance.
	rows, err := q.db.Query(ctx, `SELECT resource_type,provider_id,COALESCE(logical_name,''),generation,COALESCE(observed_state,'')
 FROM provider_resources WHERE lab_instance_id=$1 AND organization_id=$2 AND provider_connection_id=$3
 AND (($4='RESET' AND generation=$5-1) OR ($4='CLEANUP' AND lifecycle_status NOT IN ('DELETED','MISSING')))
 ORDER BY resource_type,provider_id FOR UPDATE`, d.LabInstanceID, d.OrganizationID, d.ProviderConnectionID, d.MutationType, d.Generation)
	if err != nil {
		return nil, normalize("MutationResources", err)
	}
	defer rows.Close()
	refs := make([]repository.ProvisionResource, 0)
	for rows.Next() {
		var r repository.ProvisionResource
		if err := rows.Scan(&r.Type, &r.ProviderID, &r.LogicalName, &r.Generation, &r.ObservedState); err != nil {
			return nil, normalize("MutationResources", err)
		}
		refs = append(refs, r)
	}
	return refs, normalize("MutationResources", rows.Err())
}

func (s *Store) PrepareMutation(ctx context.Context, lease repository.OperationItemLease) (repository.ProvisionDispatch, error) {
	var dispatch repository.ProvisionDispatch
	err := s.inTransaction(ctx, func(ctx context.Context, q queries) error {
		d, stage, err := q.lockMutation(ctx, lease)
		if err != nil {
			return err
		}
		if stage != "" && stage != "DEFERRED" {
			return invalidMutation()
		}
		d.Resources, err = q.mutationResources(ctx, d)
		if err != nil {
			return err
		}
		if d.MutationType == "RESET" {
			if len(d.Resources) == 0 {
				return invalidMutation()
			}
			for _, r := range d.Resources {
				if strings.TrimSpace(r.LogicalName) == "" {
					return invalidMutation()
				}
			}
		}
		// Never omit an unresolved resource on another connection or unexpected
		// generation. It needs reconciliation rather than a destructive partial set.
		var unsafe bool
		err = q.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_resources WHERE lab_instance_id=$1
 AND lifecycle_status NOT IN ('DELETED','MISSING') AND
 (provider_connection_id<>$2 OR generation>$3 OR ($4='RESET' AND generation<>$3-1)))`, d.LabInstanceID, d.ProviderConnectionID, d.Generation, d.MutationType).Scan(&unsafe)
		if err != nil {
			return normalize("PrepareMutation", err)
		}
		if unsafe {
			return invalidMutation()
		}
		tag, err := q.db.Exec(ctx, `UPDATE operation_items SET stage='DISPATCHING',previous_instance_status=$2 WHERE id=$1 AND lease_expires_at>clock_timestamp()`, lease.ItemID, d.PreviousStatus)
		if err != nil {
			return normalize("PrepareMutation", err)
		}
		if tag.RowsAffected() != 1 {
			return repository.ErrOperationLeaseLost
		}
		status := "RESETTING"
		if d.MutationType == "CLEANUP" {
			status = "CLEANING_UP"
		}
		if _, err = q.db.Exec(ctx, `UPDATE lab_instances SET status=$2,updated_at=clock_timestamp() WHERE id=$1`, d.LabInstanceID, status); err != nil {
			return normalize("PrepareMutation", err)
		}
		if d.MutationType == "CLEANUP" {
			if _, err = q.db.Exec(ctx, `UPDATE lab_executions SET status='CLEANING_UP',updated_at=clock_timestamp() WHERE id=$1`, d.LabExecutionID); err != nil {
				return normalize("PrepareMutation", err)
			}
		}
		dispatch = d
		return nil
	})
	if err != nil {
		return repository.ProvisionDispatch{}, err
	}
	return dispatch, nil
}

func (s *Store) DeferUnsentMutation(ctx context.Context, expected repository.ProvisionDispatch, delay time.Duration) error {
	if delay < time.Microsecond {
		return invalidMutation()
	}
	return s.inTransaction(ctx, func(ctx context.Context, q queries) error {
		d, stage, err := q.lockMutation(ctx, expected.Lease)
		if err != nil {
			return err
		}
		if stage != "DISPATCHING" || !sameProvisionDispatch(d, expected) || d.MutationType != expected.MutationType {
			return invalidMutation()
		}
		tag, err := q.db.Exec(ctx, `UPDATE operation_items SET status='PENDING',stage='DEFERRED',lease_owner=NULL,lease_expires_at=NULL,
 dispatch_not_before=clock_timestamp()+$2::bigint*interval '1 microsecond' WHERE id=$1 AND lease_expires_at>clock_timestamp()`, d.Lease.ItemID, delay.Microseconds())
		if err != nil {
			return normalize("DeferMutation", err)
		}
		if tag.RowsAffected() != 1 {
			return repository.ErrOperationLeaseLost
		}
		if _, err = q.db.Exec(ctx, `UPDATE lab_instances SET status=$2,updated_at=clock_timestamp() WHERE id=$1`, d.LabInstanceID, d.PreviousStatus); err != nil {
			return normalize("DeferMutation", err)
		}
		var at time.Time
		if err := q.db.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&at); err != nil {
			return normalize("DeferMutation", err)
		}
		return q.aggregateMutation(ctx, d, at)
	})
}

func absent(state string) bool { return state == "DELETED" || state == "ABSENT" || state == "MISSING" }

func (s *Store) WriteMutation(ctx context.Context, u repository.ProvisionUpdate) error {
	if u.Status != "RUNNING" && u.Status != "SUCCEEDED" && u.Status != "FAILED" && u.Status != "RECONCILING" {
		return invalidMutation()
	}
	return s.inTransaction(ctx, func(ctx context.Context, q queries) error {
		d, stage, err := q.lockMutation(ctx, u.Dispatch.Lease)
		if err != nil {
			return err
		}
		if stage == "" || stage == "DEFERRED" || !sameProvisionDispatch(d, u.Dispatch) || d.MutationType != u.Dispatch.MutationType {
			return invalidMutation()
		}
		var unsafe bool
		if err := q.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_resources WHERE lab_instance_id=$1
 AND lifecycle_status NOT IN ('DELETED','MISSING') AND (provider_connection_id<>$2 OR generation>$3))`, d.LabInstanceID, d.ProviderConnectionID, d.Generation).Scan(&unsafe); err != nil {
			return normalize("WriteMutation", err)
		}
		if unsafe {
			return invalidMutation()
		}
		refs, err := q.mutationResources(ctx, d)
		if err != nil {
			return err
		}
		var at time.Time
		if err = q.db.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&at); err != nil {
			return normalize("WriteMutation", err)
		}
		tracked := make(map[[2]string]repository.ProvisionResource)
		for _, r := range refs {
			tracked[[2]string{r.Type, r.ProviderID}] = r
		}
		seen := make(map[[2]string]bool)
		allAbsent := true
		unchanged := true
		newCount := 0
		for _, r := range u.Resources {
			key := [2]string{r.Type, r.ProviderID}
			if strings.TrimSpace(r.Type) == "" || strings.TrimSpace(r.ProviderID) == "" || seen[key] {
				return invalidMutation()
			}
			seen[key] = true
			old, known := tracked[key]
			if known {
				if r.Generation != old.Generation || r.LogicalName != old.LogicalName {
					return invalidMutation()
				}
				if !absent(r.ObservedState) {
					allAbsent = false
				}
				if r.ObservedState == "DELETE_NOT_ATTEMPTED" {
					continue
				}
				unchanged = false
				lifecycle := "PRESENT"
				var deleted *time.Time
				if absent(r.ObservedState) {
					lifecycle = "MISSING"
					if r.ObservedState == "DELETED" {
						lifecycle = "DELETED"
						deleted = &at
					}
				}
				_, err = q.db.Exec(ctx, `UPDATE provider_resources SET lifecycle_status=$4,observed_state=$5,last_observed_at=$6,
 deleted_at=$7,updated_at=$6,cleanup_error_code=NULLIF($8,'') WHERE lab_instance_id=$1 AND provider_connection_id=$2 AND resource_type=$3 AND provider_id=$9`, d.LabInstanceID, d.ProviderConnectionID, r.Type, lifecycle, r.ObservedState, at, deleted, u.ErrorCode, r.ProviderID)
			} else {
				// CLEANUP cannot adopt discovered IDs. RESET can record only its new
				// generation; exact ID collisions retain the original owner's history.
				if d.MutationType != "RESET" || r.Generation != d.Generation {
					return invalidMutation()
				}
				unchanged = false
				newCount++
				lifecycle := "PRESENT"
				var deleted *time.Time
				if absent(r.ObservedState) {
					lifecycle = "MISSING"
					if r.ObservedState == "DELETED" {
						lifecycle = "DELETED"
						deleted = &at
					}
				}
				tag, e := q.db.Exec(ctx, `INSERT INTO provider_resources
 (id,organization_id,lab_instance_id,provider_connection_id,generation,resource_type,logical_name,provider_id,lifecycle_status,observed_state,created_by_operation_item_id,last_observed_at,deleted_at,created_at,updated_at)
 VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,NULLIF($10,''),$11,$12,$13,$12,$12)
 ON CONFLICT (provider_connection_id,resource_type,provider_id) DO NOTHING`, uuid.New(), d.OrganizationID, d.LabInstanceID, d.ProviderConnectionID, r.Generation, r.Type, r.LogicalName, r.ProviderID, lifecycle, r.ObservedState, d.Lease.ItemID, at, deleted)
				err = e
				if err == nil && tag.RowsAffected() != 1 {
					return invalidMutation()
				}
			}
			if err != nil {
				return normalize("MutationResource", err)
			}
		}
		for key := range tracked {
			if !seen[key] {
				allAbsent = false
			}
		}
		// Outcome alone cannot attest absence. Keep incomplete successes durable
		// for LBT-179; no additional mutation or reconciliation is performed here.
		if u.Status == "SUCCEEDED" && (!allAbsent || (d.MutationType == "RESET" && newCount == 0)) {
			u.Status = "RECONCILING"
			u.Stage = "RECONCILING"
			u.ErrorCode = "mutation_result_incomplete"
			u.ErrorMessage = "Resource confirmation requires reconciliation"
		}
		if d.MutationType == "RESET" && newCount > 0 && !allAbsent {
			u.Status = "RECONCILING"
			u.Stage = "RECONCILING"
			u.ErrorCode = "mutation_result_incomplete"
			u.ErrorMessage = "Resource confirmation requires reconciliation"
		}
		if d.MutationType == "CLEANUP" && u.Status == "SUCCEEDED" {
			u.Stage = "COMPLETED"
		}
		var finished *time.Time
		if u.Status == "SUCCEEDED" || u.Status == "FAILED" {
			finished = &at
		}
		tag, err := q.db.Exec(ctx, `UPDATE operation_items SET status=$2,stage=NULLIF($3,''),error_code=NULLIF($4,''),error_message=NULLIF($5,''),finished_at=$6,
 lease_owner=CASE WHEN $2='RUNNING' THEN lease_owner ELSE NULL END,lease_expires_at=CASE WHEN $2='RUNNING' THEN lease_expires_at ELSE NULL END
 WHERE id=$1 AND lease_expires_at>clock_timestamp()`, d.Lease.ItemID, u.Status, u.Stage, u.ErrorCode, u.ErrorMessage, finished)
		if err != nil {
			return normalize("WriteMutation", err)
		}
		if tag.RowsAffected() != 1 {
			return repository.ErrOperationLeaseLost
		}
		status := "ERROR"
		generation := d.Generation
		if d.MutationType == "RESET" {
			generation--
			switch {
			case u.Status == "RUNNING" || u.Status == "RECONCILING":
				status = "RESETTING"
			case u.Stage == "REJECTED" || (u.Status == "FAILED" && unchanged):
				status = d.PreviousStatus
			case allAbsent && newCount > 0:
				generation = d.Generation
				status = "PROVISIONING"
				if u.Status == "FAILED" {
					status = "ERROR"
				}
			}
		} else {
			if u.Status == "SUCCEEDED" {
				status = "COMPLETED"
				u.Stage = "COMPLETED"
			} else if u.Status == "RUNNING" || u.Status == "RECONCILING" {
				status = "CLEANING_UP"
			}
		}
		if _, err = q.db.Exec(ctx, `UPDATE lab_instances SET status=$2,generation=$3,updated_at=$4 WHERE id=$1`, d.LabInstanceID, status, generation, at); err != nil {
			return normalize("MutationInstance", err)
		}
		return q.aggregateMutation(ctx, d, at)
	})
}

func (q queries) aggregateMutation(ctx context.Context, d repository.ProvisionDispatch, at time.Time) error {
	if err := q.aggregateProvision(ctx, d.OperationID, at); err != nil {
		return err
	}
	_, err := q.db.Exec(ctx, `UPDATE operations SET stage=CASE
 WHEN status='SUCCEEDED' AND operation_type='CLEANUP' THEN 'COMPLETED'
 WHEN status='RUNNING' THEN CASE WHEN operation_type='RESET' THEN 'RESETTING' ELSE 'CLEANING_UP' END ELSE stage END
 WHERE id=$1`, d.OperationID)
	if err != nil {
		return normalize("AggregateMutation", err)
	}
	if d.MutationType == "CLEANUP" {
		// The registered set must cover the execution, and every resource across
		// its instances/generations must be confirmed absent before releasing it.
		_, err = q.db.Exec(ctx, `WITH completion AS (SELECT
  NOT EXISTS(SELECT 1 FROM operation_items WHERE operation_id=$1 AND status<>'SUCCEEDED')
  AND NOT EXISTS(SELECT 1 FROM lab_instances li WHERE li.lab_execution_id=$2 AND NOT EXISTS
   (SELECT 1 FROM operation_items i WHERE i.operation_id=$1 AND i.lab_instance_id=li.id))
  AND NOT EXISTS(SELECT 1 FROM provider_resources r JOIN lab_instances li ON li.id=r.lab_instance_id
   WHERE li.lab_execution_id=$2 AND r.lifecycle_status NOT IN ('DELETED','MISSING')) AS done)
 UPDATE lab_executions SET status=CASE WHEN completion.done THEN 'COMPLETED'
 WHEN EXISTS(SELECT 1 FROM operation_items WHERE operation_id=$1 AND status='FAILED') THEN 'ERROR' ELSE 'CLEANING_UP' END,
 finished_at=CASE WHEN completion.done THEN $3::timestamptz ELSE NULL END,updated_at=$3 FROM completion WHERE id=$2`, d.OperationID, d.LabExecutionID, at)
	}
	return normalize("AggregateMutation", err)
}
