package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

func (q queries) ConnectorCredentialByHash(ctx context.Context, credentialHash []byte) (repository.ConnectorCredentialWithConnector, error) {
	const op = "ConnectorCredentialByHash"

	var found repository.ConnectorCredentialWithConnector
	credential, connector := &found.Credential, &found.Connector
	err := q.db.QueryRow(ctx,
		`SELECT cc.id, cc.connector_id, cc.revoked_at, c.id, c.organization_id, c.revoked_at
		 FROM connector_credentials cc JOIN connectors c ON c.id = cc.connector_id
		 WHERE cc.credential_hash = $1`,
		credentialHash,
	).Scan(&credential.ID, &credential.ConnectorID, &credential.RevokedAt, &connector.ID, &connector.OrganizationID, &connector.RevokedAt)
	if err != nil {
		return repository.ConnectorCredentialWithConnector{}, normalize(op, err)
	}
	return found, nil
}

func (q queries) RecordConnectorHeartbeat(ctx context.Context, connectorID, credentialID uuid.UUID, seenAt time.Time) (bool, error) {
	// Credential이 그 Connector의 것이고 둘 다 revoke되지 않은 경우에만 갱신하는 단일 statement다.
	// READ COMMITTED에서 동시에 revoke가 commit되면 대기 후 조건을 다시 평가하므로 revoke 이후에는 갱신되지 않는다.
	tag, err := q.db.Exec(ctx,
		`UPDATE connectors c SET last_seen_at = $3
		 FROM connector_credentials cc
		 WHERE c.id = $1 AND cc.id = $2 AND cc.connector_id = c.id
		   AND c.revoked_at IS NULL AND cc.revoked_at IS NULL`,
		connectorID, credentialID, seenAt,
	)
	if err != nil {
		return false, normalize("RecordConnectorHeartbeat", err)
	}
	return tag.RowsAffected() == 1, nil
}
