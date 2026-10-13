package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Operation is a durable request record. Trace and errors are internal persistence metadata.
type Operation struct {
	ID, OrganizationID, RequestedByUserID  uuid.UUID
	Type, Status                           string
	LabExecutionID, LabInstanceID          *uuid.UUID
	RequestID                              string
	IdempotencyKeyHash, RequestFingerprint []byte
	Stage, ErrorCode, ErrorMessage         string
	CreatedAt, UpdatedAt                   time.Time
	StartedAt, FinishedAt                  *time.Time
	Traceparent, Tracestate                string
}

// NewOperation contains only initial registration fields; status is always PENDING.
type NewOperation struct {
	ID, OrganizationID, RequestedByUserID  uuid.UUID
	Type                                   string
	LabExecutionID, LabInstanceID          *uuid.UUID
	RequestID                              string
	IdempotencyKeyHash, RequestFingerprint []byte
	CreatedAt                              time.Time
	Traceparent, Tracestate                string
}

type OperationItem struct {
	ID, OrganizationID, OperationID, LabInstanceID uuid.UUID
	Generation                                     int64
	Status, Stage, ErrorCode, ErrorMessage         string
	CreatedAt                                      time.Time
	StartedAt, FinishedAt                          *time.Time
}

type NewOperationItem struct {
	ID, OrganizationID, OperationID, LabInstanceID uuid.UUID
	Generation                                     int64
	CreatedAt                                      time.Time
}

// OperationTarget carries only authoritative relationships needed for read authorization.
type OperationTarget struct {
	OrganizationID, ClassID uuid.UUID
	OwnerUserID             *uuid.UUID // instance target only
}

// OperationRepository is used inside Transactor for registration. A failed registration
// must escape the callback so all business rows and items roll back together.
type OperationRepository interface {
	// InsertOperation returns false ONLY on the existing idempotency unique key.
	// ON CONFLICT waits for a concurrent owner to commit/rollback; it never overwrites metadata.
	InsertOperation(context.Context, NewOperation) (bool, error)
	OperationByIdempotency(context.Context, uuid.UUID, string, []byte) (Operation, error)
	OperationByID(context.Context, uuid.UUID) (Operation, error)
	// CreateOperationItem rejects items outside the target or its authoritative generation.
	// PROVISION/CLEANUP use the current generation; RESET uses current+1 (Connector README §11).
	// Use inside Transactor: FOR SHARE holds the LabInstance generation stable until commit/rollback.
	// It does not activate RESET's new generation or change ProviderResource history.
	CreateOperationItem(context.Context, NewOperationItem) error
	OperationItems(context.Context, uuid.UUID) ([]OperationItem, error)
	OperationTarget(context.Context, Operation) (OperationTarget, error)
}
