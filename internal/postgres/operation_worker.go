package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

var _ repository.OperationWorkerStore = (*Store)(nil)

func (s *Store) ClaimOperationItem(ctx context.Context, owner uuid.UUID, duration time.Duration) (repository.OperationItemClaim, error) {
	return s.claimOperationItem(ctx, owner, duration, "")
}

// ClaimProvisionOperationItem filters before locking/activation. Unsupported
// RESET/CLEANUP work remains PENDING for its own executor.
func (s *Store) ClaimProvisionOperationItem(ctx context.Context, owner uuid.UUID, duration time.Duration) (repository.OperationItemClaim, error) {
	return s.claimOperationItem(ctx, owner, duration, "PROVISION")
}

func (s *Store) claimOperationItem(ctx context.Context, owner uuid.UUID, duration time.Duration, operationType string) (repository.OperationItemClaim, error) {
	if owner == uuid.Nil || duration < time.Microsecond {
		return repository.OperationItemClaim{}, &repository.Error{Kind: repository.KindConstraintViolation, Op: "ClaimOperationItem"}
	}
	var claim repository.OperationItemClaim
	err := s.inTransaction(ctx, func(ctx context.Context, q queries) error {
		// Lock both lifecycle records so a parent transition cannot race the claim.
		// A concurrent claim may skip siblings while their parent is briefly locked;
		// no lock survives this method's commit.
		err := q.db.QueryRow(ctx, `SELECT i.id, i.organization_id, i.operation_id, i.lab_instance_id,
 i.generation, COALESCE(i.stage,''), COALESCE(i.error_code,''), COALESCE(i.error_message,''), i.created_at
 FROM operation_items i JOIN operations o ON o.id=i.operation_id AND o.organization_id=i.organization_id
 WHERE i.status='PENDING' AND i.finished_at IS NULL
 AND (i.dispatch_not_before IS NULL OR i.dispatch_not_before<=clock_timestamp())
 AND ($1::text='' OR o.operation_type=$1)
 AND o.status IN ('PENDING','RUNNING') AND o.finished_at IS NULL
 ORDER BY i.created_at,i.id LIMIT 1 FOR UPDATE OF i,o SKIP LOCKED`, operationType).Scan(
			&claim.Item.ID, &claim.Item.OrganizationID, &claim.Item.OperationID, &claim.Item.LabInstanceID,
			&claim.Item.Generation, &claim.Item.Stage, &claim.Item.ErrorCode, &claim.Item.ErrorMessage, &claim.Item.CreatedAt)
		if err != nil {
			return normalize("ClaimOperationItem", err)
		}
		claim.Lease = repository.OperationItemLease{ItemID: claim.Item.ID, Owner: owner}
		// Take wall-clock time after acquiring the locks, not transaction start time.
		var claimedAt time.Time
		err = q.db.QueryRow(ctx, `WITH timing AS MATERIALIZED (SELECT clock_timestamp() AS at)
 UPDATE operation_items SET status='RUNNING',lease_owner=$2,
 dispatch_not_before=NULL,
 lease_expires_at=timing.at + $3::bigint * interval '1 microsecond',
 started_at=COALESCE(started_at,timing.at)
 FROM timing WHERE id=$1 RETURNING started_at,lease_expires_at,timing.at`,
			claim.Item.ID, owner.String(), duration.Microseconds()).Scan(&claim.Item.StartedAt, &claim.ExpiresAt, &claimedAt)
		if err != nil {
			return normalize("ClaimOperationItem", err)
		}
		claim.Item.Status = "RUNNING"
		_, err = q.db.Exec(ctx, `UPDATE operations SET status='RUNNING',
 started_at=COALESCE(started_at,$2),updated_at=$2 WHERE id=$1`, claim.Item.OperationID, claimedAt)
		if err != nil {
			return normalize("ClaimOperationItem", err)
		}
		claim.Operation, err = q.OperationByID(ctx, claim.Item.OperationID)
		return err
	})
	if err != nil {
		// A failed/ambiguous commit must never authorize external execution.
		return repository.OperationItemClaim{}, err
	}
	return claim, nil
}

func (s *Store) RenewOperationItemLease(ctx context.Context, lease repository.OperationItemLease, duration time.Duration) (time.Time, error) {
	if lease.ItemID == uuid.Nil || lease.Owner == uuid.Nil || duration < time.Microsecond {
		return time.Time{}, &repository.Error{Kind: repository.KindConstraintViolation, Op: "RenewOperationItemLease"}
	}
	var expiresAt time.Time
	err := s.inTransaction(ctx, func(ctx context.Context, q queries) error {
		// Lock the parent as well as the item before checking time. In particular,
		// a renewal waiting behind another writer cannot resurrect an expired lease.
		err := q.db.QueryRow(ctx, `WITH owned AS MATERIALIZED (
 SELECT i.id FROM operation_items i
 JOIN operations o ON o.id=i.operation_id AND o.organization_id=i.organization_id
 WHERE i.id=$1 AND i.lease_owner=$2 AND i.status='RUNNING' AND i.finished_at IS NULL
 AND o.status='RUNNING' AND o.finished_at IS NULL
 FOR UPDATE OF i,o
 )
 UPDATE operation_items i SET lease_expires_at=clock_timestamp() + $3::bigint * interval '1 microsecond'
 FROM owned WHERE i.id=owned.id AND i.lease_expires_at > clock_timestamp()
 RETURNING i.lease_expires_at`, lease.ItemID, lease.Owner.String(), duration.Microseconds()).Scan(&expiresAt)
		if err != nil {
			return normalize("RenewOperationItemLease", err)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return time.Time{}, repository.ErrOperationLeaseLost
		}
		return time.Time{}, err
	}
	return expiresAt, nil
}
