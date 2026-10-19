package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

func (q queries) CreateConnectorEnrollmentToken(ctx context.Context, t repository.EnrollmentToken) error {
	_, err := q.db.Exec(ctx, `INSERT INTO connector_enrollment_tokens
 (id, token_hash, organization_id, connector_name, issued_by, created_at, expires_at)
 VALUES ($1,$2,$3,$4,$5,$6,$7)`, t.ID, t.TokenHash, t.OrganizationID, t.Name, t.IssuedBy, t.CreatedAt, t.ExpiresAt)
	return normalize("CreateConnectorEnrollmentToken", err)
}

const enrollmentColumns = `id, token_hash, organization_id, connector_name, issued_by, created_at, expires_at, consumed_at, revoked_at, connector_id`

func scanEnrollment(row pgx.Row) (repository.EnrollmentToken, error) {
	var t repository.EnrollmentToken
	err := row.Scan(&t.ID, &t.TokenHash, &t.OrganizationID, &t.Name, &t.IssuedBy, &t.CreatedAt, &t.ExpiresAt, &t.ConsumedAt, &t.RevokedAt, &t.ConnectorID)
	return t, err
}

func (q queries) ConnectorEnrollmentTokenByHashForUpdate(ctx context.Context, hash []byte) (repository.EnrollmentToken, error) {
	t, err := scanEnrollment(q.db.QueryRow(ctx, `SELECT `+enrollmentColumns+` FROM connector_enrollment_tokens WHERE token_hash=$1 FOR UPDATE`, hash))
	return t, normalize("ConnectorEnrollmentTokenByHashForUpdate", err)
}

func (q queries) ConnectorEnrollmentTokenForUpdate(ctx context.Context, org, id uuid.UUID) (repository.EnrollmentToken, error) {
	t, err := scanEnrollment(q.db.QueryRow(ctx, `SELECT `+enrollmentColumns+` FROM connector_enrollment_tokens WHERE organization_id=$1 AND id=$2 FOR UPDATE`, org, id))
	return t, normalize("ConnectorEnrollmentTokenForUpdate", err)
}

func (q queries) RevokeConnectorEnrollmentToken(ctx context.Context, id uuid.UUID, at time.Time) error {
	_, err := q.db.Exec(ctx, `UPDATE connector_enrollment_tokens SET revoked_at=COALESCE(revoked_at,$2) WHERE id=$1 AND consumed_at IS NULL`, id, at)
	return normalize("RevokeConnectorEnrollmentToken", err)
}

func (q queries) CreateEnrolledConnector(ctx context.Context, id, org uuid.UUID, name string, at time.Time, credentialID uuid.UUID, hash []byte) error {
	_, err := q.db.Exec(ctx, `INSERT INTO connectors (id,organization_id,name,created_at) VALUES ($1,$2,$3,$4)`, id, org, name, at)
	if err != nil {
		return normalize("CreateEnrolledConnector", err)
	}
	_, err = q.db.Exec(ctx, `INSERT INTO connector_credentials (id,connector_id,credential_hash,created_at) VALUES ($1,$2,$3,$4)`, credentialID, id, hash, at)
	return normalize("CreateEnrolledConnectorCredential", err)
}

func (q queries) ConsumeConnectorEnrollmentToken(ctx context.Context, id, connectorID uuid.UUID, at time.Time) error {
	tag, err := q.db.Exec(ctx, `UPDATE connector_enrollment_tokens SET consumed_at=$3,connector_id=$2
 WHERE id=$1 AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at>$3`, id, connectorID, at)
	if err != nil {
		return normalize("ConsumeConnectorEnrollmentToken", err)
	}
	if tag.RowsAffected() != 1 {
		return repository.ErrConflict
	}
	return nil
}

func (q queries) ConnectorsByOrganization(ctx context.Context, org uuid.UUID) ([]repository.ManagedConnector, error) {
	rows, err := q.db.Query(ctx, `SELECT id,name,created_at,revoked_at,last_seen_at,last_connector_version,last_runtime_id
 FROM connectors WHERE organization_id=$1 ORDER BY created_at DESC,id`, org)
	if err != nil {
		return nil, normalize("ConnectorsByOrganization", err)
	}
	defer rows.Close()
	out := make([]repository.ManagedConnector, 0)
	for rows.Next() {
		var c repository.ManagedConnector
		if err := rows.Scan(&c.ID, &c.Name, &c.CreatedAt, &c.RevokedAt, &c.LastSeenAt, &c.LastConnectorVersion, &c.LastRuntimeID); err != nil {
			return nil, normalize("ConnectorsByOrganization", err)
		}
		out = append(out, c)
	}
	return out, normalize("ConnectorsByOrganization", rows.Err())
}

func (q queries) RevokeManagedConnector(ctx context.Context, org, id uuid.UUID, at time.Time) error {
	// UPDATE locks the parent before all child credentials, matching heartbeat/HELLO.
	tag, err := q.db.Exec(ctx, `UPDATE connectors SET revoked_at=COALESCE(revoked_at,$3) WHERE organization_id=$1 AND id=$2`, org, id, at)
	if err != nil {
		return normalize("RevokeManagedConnector", err)
	}
	if tag.RowsAffected() != 1 {
		return repository.ErrNotFound
	}
	_, err = q.db.Exec(ctx, `UPDATE connector_credentials SET revoked_at=COALESCE(revoked_at,$2) WHERE connector_id=$1`, id, at)
	return normalize("RevokeManagedConnectorCredentials", err)
}

func (s *Store) RecordConnectorHello(ctx context.Context, id, credentialID uuid.UUID, version, runtimeID string) (bool, error) {
	var recorded bool
	err := s.inTransaction(ctx, func(ctx context.Context, q queries) error {
		active, err := q.lockActiveConnectorCredential(ctx, id, credentialID)
		if err != nil || !active {
			return err
		}
		_, err = q.db.Exec(ctx, `UPDATE connectors SET last_connector_version=$2,last_runtime_id=$3 WHERE id=$1`, id, version, runtimeID)
		recorded = err == nil
		return normalize("RecordConnectorHello", err)
	})
	if err != nil {
		return false, err
	}
	return recorded, nil
}
