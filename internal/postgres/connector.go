package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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

// RecordConnectorHeartbeat는 짧은 transaction 하나에서 Connector와 Credential row를 lock하며 revoke 여부를 다시 확인한 뒤에만
// last_seen_at을 갱신한다. 계약은 repository.ConnectorRepository를 따른다.
//
// UPDATE ... FROM connector_credentials는 Credential row를 잠그지 않는 plain read라서, 아직 commit되지 않은 Credential revoke를
// 기다리지 않고 이전 commit 상태를 근거로 갱신할 수 있다. 그래서 두 row를 모두 명시적으로 잠근다.
//
// lock 순서는 connectors → connector_credentials(FK의 parent → child)다. Connector와 Credential을 한 transaction에서 함께
// revoke하는 writer도 같은 순서를 지켜야 deadlock이 생기지 않는다.
//   - connectors는 FOR NO KEY UPDATE: 같은 row를 UPDATE하는 Connector revoke와 다른 heartbeat를 직렬화한다.
//     FK를 검사하는 FOR KEY SHARE(Credential 생성 등)와는 충돌하지 않는다.
//   - connector_credentials는 FOR SHARE: Credential revoke UPDATE(FOR NO KEY UPDATE 수준)와 충돌하므로 revoke가 commit될 때까지
//     기다렸다가 조건을 다시 평가한다. revoke가 rollback되면 active로 보고 진행한다. 다른 heartbeat와는 서로 막지 않는다.
func (s *Store) RecordConnectorHeartbeat(ctx context.Context, connectorID, credentialID uuid.UUID, seenAt time.Time) (bool, error) {
	var recorded bool
	err := s.inTransaction(ctx, func(ctx context.Context, q queries) error {
		var err error
		recorded, err = q.recordConnectorHeartbeat(ctx, connectorID, credentialID, seenAt)
		return err
	})
	if err != nil {
		return false, err
	}
	return recorded, nil
}

// recordConnectorHeartbeat는 transaction 안에서 실행한다. 조건을 만족하지 않으면 아무것도 바꾸지 않고 false다.
func (q queries) recordConnectorHeartbeat(ctx context.Context, connectorID, credentialID uuid.UUID, seenAt time.Time) (bool, error) {
	const op = "RecordConnectorHeartbeat"

	var locked int
	// 1. Connector: revoke 중인 transaction이 있으면 끝날 때까지 기다린 뒤 revoked_at을 다시 평가한다.
	err := q.db.QueryRow(ctx,
		`SELECT 1 FROM connectors WHERE id = $1 AND revoked_at IS NULL FOR NO KEY UPDATE`,
		connectorID,
	).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, normalize(op, err)
	}

	// 2. Credential: 그 Connector의 Credential이어야 하고 revoke되지 않았어야 한다.
	err = q.db.QueryRow(ctx,
		`SELECT 1 FROM connector_credentials WHERE id = $1 AND connector_id = $2 AND revoked_at IS NULL FOR SHARE`,
		credentialID, connectorID,
	).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, normalize(op, err)
	}

	// 3. 두 row가 모두 active임을 lock한 채 확인했다.
	tag, err := q.db.Exec(ctx, `UPDATE connectors SET last_seen_at = $2 WHERE id = $1`, connectorID, seenAt)
	if err != nil {
		return false, normalize(op, err)
	}
	return tag.RowsAffected() == 1, nil
}
