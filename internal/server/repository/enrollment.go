package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// EnrollmentToken carries lifecycle metadata and a digest, never the original Secret.
type EnrollmentToken struct {
	ID, OrganizationID, IssuedBy uuid.UUID
	TokenHash                    []byte
	Name                         string
	CreatedAt, ExpiresAt         time.Time
	ConsumedAt, RevokedAt        *time.Time
	ConnectorID                  *uuid.UUID
}

type ManagedConnector struct {
	ID                                  uuid.UUID
	Name                                string
	CreatedAt                           time.Time
	RevokedAt, LastSeenAt               *time.Time
	LastConnectorVersion, LastRuntimeID *string
}

// EnrollmentRepository methods that lock or change several rows run within Transactor.
// Revoke uses the same parent → child lock order as RecordConnectorHeartbeat.
type EnrollmentRepository interface {
	CreateConnectorEnrollmentToken(context.Context, EnrollmentToken) error
	ConnectorEnrollmentTokenByHashForUpdate(context.Context, []byte) (EnrollmentToken, error)
	ConnectorEnrollmentTokenForUpdate(context.Context, uuid.UUID, uuid.UUID) (EnrollmentToken, error)
	RevokeConnectorEnrollmentToken(context.Context, uuid.UUID, time.Time) error
	CreateEnrolledConnector(context.Context, uuid.UUID, uuid.UUID, string, time.Time, uuid.UUID, []byte) error
	ConsumeConnectorEnrollmentToken(context.Context, uuid.UUID, uuid.UUID, time.Time) error
	ConnectorsByOrganization(context.Context, uuid.UUID) ([]ManagedConnector, error)
	RevokeManagedConnector(context.Context, uuid.UUID, uuid.UUID, time.Time) error
}

// ConnectorHelloRepository authenticates both rows under the heartbeat lock order before
// recording HELLO metadata. The caller must also fence the current Registry connection.
type ConnectorHelloRepository interface {
	RecordConnectorHello(context.Context, uuid.UUID, uuid.UUID, string, string) (bool, error)
}
