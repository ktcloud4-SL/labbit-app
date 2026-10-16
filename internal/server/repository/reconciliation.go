package repository

import (
	"context"
	"github.com/google/uuid"
	"time"
)

// ReconciliationStore claims observation work only. It never authorizes a
// mutation replay, including when the original RUNNING lease has expired.
type ReconciliationStore interface {
	ClaimReconciliation(context.Context, uuid.UUID, time.Duration) (OperationItemClaim, error)
	RenewReconciliation(context.Context, OperationItemLease, time.Duration) (time.Time, error)
	PrepareReconciliation(context.Context, OperationItemLease) (ProvisionDispatch, error)
	WriteReconciliation(context.Context, ReconciliationUpdate) error
}

type ResourceObservation struct {
	ProvisionResource
	Exists bool
	Source string
}

type ReconciliationUpdate struct {
	Dispatch     ProvisionDispatch
	Observations []ResourceObservation
	QueryFailed  bool
	RetryDelay   time.Duration
}
