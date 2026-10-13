package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

func (q queries) InsertOperation(ctx context.Context, o repository.NewOperation) (bool, error) {
	tag, err := q.db.Exec(ctx, `INSERT INTO operations
 (id, organization_id, operation_type, requested_by_user_id, request_id, lab_execution_id, lab_instance_id,
 idempotency_key_hash, request_fingerprint, status, created_at, updated_at, traceparent, tracestate)
 VALUES ($1,$2,$3,$4,NULLIF($5,''),$6,$7,$8,$9,'PENDING',$10,$10,NULLIF($11,''),NULLIF($12,''))
 ON CONFLICT (requested_by_user_id, operation_type, idempotency_key_hash) DO NOTHING`,
		o.ID, o.OrganizationID, o.Type, o.RequestedByUserID, o.RequestID, o.LabExecutionID, o.LabInstanceID,
		o.IdempotencyKeyHash, o.RequestFingerprint, o.CreatedAt, o.Traceparent, o.Tracestate)
	if err != nil {
		return false, normalize("InsertOperation", err)
	}
	return tag.RowsAffected() == 1, nil
}

const operationColumns = `id, organization_id, operation_type, requested_by_user_id,
 COALESCE(request_id,''), lab_execution_id, lab_instance_id, idempotency_key_hash, request_fingerprint,
 status, COALESCE(stage,''), COALESCE(error_code,''), COALESCE(error_message,''),
 created_at, updated_at, started_at, finished_at, COALESCE(traceparent,''), COALESCE(tracestate,'')`

func scanOperation(row pgx.Row, op string) (repository.Operation, error) {
	var o repository.Operation
	err := row.Scan(&o.ID, &o.OrganizationID, &o.Type, &o.RequestedByUserID, &o.RequestID, &o.LabExecutionID, &o.LabInstanceID,
		&o.IdempotencyKeyHash, &o.RequestFingerprint, &o.Status, &o.Stage, &o.ErrorCode, &o.ErrorMessage,
		&o.CreatedAt, &o.UpdatedAt, &o.StartedAt, &o.FinishedAt, &o.Traceparent, &o.Tracestate)
	if err != nil {
		return repository.Operation{}, normalize(op, err)
	}
	return o, nil
}
func (q queries) OperationByID(ctx context.Context, id uuid.UUID) (repository.Operation, error) {
	return scanOperation(q.db.QueryRow(ctx, `SELECT `+operationColumns+` FROM operations WHERE id=$1`, id), "OperationByID")
}
func (q queries) OperationByIdempotency(ctx context.Context, user uuid.UUID, kind string, hash []byte) (repository.Operation, error) {
	return scanOperation(q.db.QueryRow(ctx, `SELECT `+operationColumns+` FROM operations
 WHERE requested_by_user_id=$1 AND operation_type=$2 AND idempotency_key_hash=$3`, user, kind, hash), "OperationByIdempotency")
}
func (q queries) CreateOperationItem(ctx context.Context, i repository.NewOperationItem) error {
	tag, err := q.db.Exec(ctx, `WITH target AS MATERIALIZED (
 SELECT o.operation_type, li.generation
 FROM operations o JOIN lab_instances li ON li.organization_id=o.organization_id
 WHERE o.id=$3 AND o.organization_id=$2 AND li.id=$4
 AND ((o.operation_type='RESET' AND o.lab_instance_id=li.id)
 OR (o.operation_type IN ('PROVISION','CLEANUP') AND o.lab_execution_id=li.lab_execution_id))
 FOR SHARE OF li
 )
 INSERT INTO operation_items
 (id,organization_id,operation_id,lab_instance_id,generation,status,created_at)
 SELECT $1,$2,$3,$4,$5::bigint::integer,'PENDING',$6 FROM target
 WHERE $5::bigint = CASE WHEN operation_type='RESET' THEN generation::bigint + 1 ELSE generation END`,
		i.ID, i.OrganizationID, i.OperationID, i.LabInstanceID, i.Generation, i.CreatedAt)
	if err != nil {
		return normalize("CreateOperationItem", err)
	}
	if tag.RowsAffected() != 1 {
		return &repository.Error{Kind: repository.KindConstraintViolation, Op: "CreateOperationItem"}
	}
	return nil
}
func (q queries) OperationItems(ctx context.Context, id uuid.UUID) ([]repository.OperationItem, error) {
	rows, err := q.db.Query(ctx, `SELECT id,organization_id,operation_id,lab_instance_id,generation,status,
 COALESCE(stage,''),COALESCE(error_code,''),COALESCE(error_message,''),created_at,started_at,finished_at
 FROM operation_items WHERE operation_id=$1 ORDER BY lab_instance_id`, id)
	if err != nil {
		return nil, normalize("OperationItems", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (repository.OperationItem, error) {
		var i repository.OperationItem
		err := row.Scan(&i.ID, &i.OrganizationID, &i.OperationID, &i.LabInstanceID, &i.Generation, &i.Status, &i.Stage, &i.ErrorCode, &i.ErrorMessage, &i.CreatedAt, &i.StartedAt, &i.FinishedAt)
		return i, err
	})
	return items, normalize("OperationItems", err)
}
func (q queries) OperationTarget(ctx context.Context, o repository.Operation) (repository.OperationTarget, error) {
	var target repository.OperationTarget
	var err error
	if o.Type == "RESET" {
		err = q.db.QueryRow(ctx, `SELECT i.organization_id,e.class_id,i.user_id FROM lab_instances i
 JOIN lab_executions e ON e.id=i.lab_execution_id AND e.organization_id=i.organization_id
 WHERE i.id=$1 AND i.organization_id=$2`, o.LabInstanceID, o.OrganizationID).Scan(&target.OrganizationID, &target.ClassID, &target.OwnerUserID)
	} else {
		err = q.db.QueryRow(ctx, `SELECT organization_id,class_id FROM lab_executions
 WHERE id=$1 AND organization_id=$2`, o.LabExecutionID, o.OrganizationID).Scan(&target.OrganizationID, &target.ClassID)
	}
	return target, normalize("OperationTarget", err)
}
