package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrOperationLeaseLost = errors.New("repository: operation item lease lost")

// OperationItemLease identifies one claim, rather than a process. Owners must never
// be reused for another claim, including claims made after a process restart.
type OperationItemLease struct {
	ItemID, Owner uuid.UUID
}

type OperationItemClaim struct {
	Operation Operation
	Item      OperationItem
	Lease     OperationItemLease
	ExpiresAt time.Time
}

// OperationWorkerStore methods return only after their own transaction commits.
// They are deliberately separate from transaction-bound Repositories: external
// execution must never receive an open claim transaction.
type OperationWorkerStore interface {
	// ClaimOperationItem claims at most one PENDING item of a PENDING/RUNNING
	// parent. ErrNotFound means no eligible unlocked item. RUNNING and RECONCILING
	// items are never reclaimed, even after lease expiry.
	ClaimOperationItem(context.Context, uuid.UUID, time.Duration) (OperationItemClaim, error)
	// RenewOperationItemLease requires the same owner, an unexpired lease and
	// RUNNING item/parent. It cannot revive expired ownership or change status.
	// PostgreSQL time is authoritative. Durations must be >= one microsecond.
	RenewOperationItemLease(context.Context, OperationItemLease, time.Duration) (time.Time, error)
}
