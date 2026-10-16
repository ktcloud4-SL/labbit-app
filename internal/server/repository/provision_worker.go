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
	// WriteProvision updates resources, item and parent atomically. Correlation,
	// generation, connection affinity and live lease are rechecked after locking.
	WriteProvision(context.Context, ProvisionUpdate) error
}
