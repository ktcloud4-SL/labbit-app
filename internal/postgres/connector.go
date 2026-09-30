package postgres

import (
	"context"

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
