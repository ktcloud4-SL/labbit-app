package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ProvisionDispatch contains committed, immutable inputs, never a transaction.
// Snapshot contains resolved Provider IDs/script and must not enter logs or HTTP.
type ProvisionDispatch struct {
	Lease                                                      OperationItemLease
	OrganizationID, OperationID, LabInstanceID, LabExecutionID uuid.UUID
	ProviderConnectionID, ConnectorID                          uuid.UUID
	Generation                                                 int64
	Snapshot                                                   json.RawMessage
	MutationType, PreviousStatus                               string
	Resources                                                  []ProvisionResource
}

type ProvisionResource struct {
	Type, ProviderID, LogicalName, ObservedState string
	Generation                                   int64
}

type ProvisionUpdate struct {
	Dispatch                               ProvisionDispatch
	Status, Stage, ErrorCode, ErrorMessage string
	Resources                              []ProvisionResource
}

type ProvisionWorkerStore interface {
	OperationWorkerStore
	ClaimProvisionOperationItem(context.Context, uuid.UUID, time.Duration) (OperationItemClaim, error)
	// PrepareProvision activates dispatch exactly once under a live RUNNING lease,
	// checking the Operation/Instance/Execution/Snapshot/Connection relationships.
	PrepareProvision(context.Context, OperationItemLease) (ProvisionDispatch, error)
	// DeferUnsentProvision is only for a Router guarantee that no write occurred.
	// It releases a live DISPATCHING lease and atomically restores PENDING with a
	// durable not-before time. ACKed/resource-bearing/stale work cannot be deferred.
	DeferUnsentProvision(context.Context, ProvisionDispatch, time.Duration) error
	// WriteProvision updates resources, item and parent atomically. Correlation,
	// generation, connection affinity and live lease are rechecked after locking.
	WriteProvision(context.Context, ProvisionUpdate) error
}

// MutationWorkerStore shares claims, fencing and the dispatch/result value types
// with PROVISION. RESET/CLEANUP never reclaim uncertain RUNNING work.
type MutationWorkerStore interface {
	OperationWorkerStore
	PrepareMutation(context.Context, OperationItemLease) (ProvisionDispatch, error)
	DeferUnsentMutation(context.Context, ProvisionDispatch, time.Duration) error
	WriteMutation(context.Context, ProvisionUpdate) error
}
