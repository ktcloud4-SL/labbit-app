package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// labInstanceSelect는 repository.LabInstance를 만드는 공통 projection이다. lab_instances를 li, lab_executions를 le로 별칭한다.
const labInstanceSelect = `SELECT li.id, li.organization_id, li.lab_execution_id, le.class_id, li.user_id, li.status, li.generation
	 FROM lab_instances li
	 JOIN lab_executions le ON le.organization_id = li.organization_id AND le.id = li.lab_execution_id
	 WHERE li.id = $1`

func scanLabInstance(row pgx.Row) (repository.LabInstance, error) {
	var lab repository.LabInstance
	err := row.Scan(&lab.ID, &lab.OrganizationID, &lab.LabExecutionID, &lab.ClassID, &lab.UserID, &lab.Status, &lab.Generation)
	return lab, err
}

func (q queries) LabInstanceForShare(ctx context.Context, id uuid.UUID) (repository.LabInstance, error) {
	const op = "LabInstanceForShare"

	// FOR SHARE OF li는 lab_instances row만 잠근다. generation을 바꾸는 UPDATE는 이 transaction이 끝날 때까지 기다린다.
	lab, err := scanLabInstance(q.db.QueryRow(ctx, labInstanceSelect+` FOR SHARE OF li`, id))
	if err != nil {
		return repository.LabInstance{}, normalize(op, err)
	}
	return lab, nil
}

func (q queries) LabInstanceByID(ctx context.Context, id uuid.UUID) (repository.LabInstance, error) {
	const op = "LabInstanceByID"

	lab, err := scanLabInstance(q.db.QueryRow(ctx, labInstanceSelect, id))
	if err != nil {
		return repository.LabInstance{}, normalize(op, err)
	}
	return lab, nil
}

func (q queries) ConnectorIDForLabInstance(ctx context.Context, labInstanceID uuid.UUID) (uuid.UUID, error) {
	const op = "ConnectorIDForLabInstance"

	var connectorID uuid.UUID
	err := q.db.QueryRow(ctx,
		`SELECT pc.connector_id
		 FROM lab_instances li
		 JOIN creation_snapshots cs ON cs.organization_id = li.organization_id AND cs.lab_execution_id = li.lab_execution_id
		 JOIN provider_connections pc ON pc.organization_id = cs.organization_id AND pc.id = cs.provider_connection_id
		 WHERE li.id = $1`,
		labInstanceID,
	).Scan(&connectorID)
	if err != nil {
		return uuid.UUID{}, normalize(op, err)
	}
	return connectorID, nil
}

func (q queries) ProviderServers(ctx context.Context, labInstanceID uuid.UUID, generation int64, logicalName string) ([]repository.ProviderServer, error) {
	const op = "ProviderServers"

	rows, err := q.db.Query(ctx,
		`SELECT id, provider_id, lifecycle_status
		 FROM provider_resources
		 WHERE lab_instance_id = $1 AND generation = $2 AND resource_type = 'SERVER' AND logical_name = $3
		 ORDER BY id`,
		labInstanceID, generation, logicalName,
	)
	if err != nil {
		return nil, normalize(op, err)
	}
	servers, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (repository.ProviderServer, error) {
		var server repository.ProviderServer
		err := row.Scan(&server.ID, &server.ProviderID, &server.LifecycleStatus)
		return server, err
	})
	if err != nil {
		return nil, normalize(op, err)
	}
	return servers, nil
}

func (q queries) CreateTerminalSession(ctx context.Context, s repository.NewTerminalSession) error {
	return q.exec(ctx, "CreateTerminalSession",
		`INSERT INTO terminal_sessions
		 (id, organization_id, lab_instance_id, user_id, provider_resource_id, generation, status, attach_token_hash, token_expires_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, 'OPENING', $7, $8, $9)`,
		s.ID, s.OrganizationID, s.LabInstanceID, s.UserID, s.ProviderResourceID, s.Generation, s.AttachTokenHash, s.TokenExpiresAt, s.CreatedAt,
	)
}

// terminalSessionSelect는 repository.TerminalSession을 만드는 공통 projection이다.
const terminalSessionSelect = `SELECT id, organization_id, lab_instance_id, user_id, provider_resource_id, generation, status,
	attach_token_hash, token_expires_at, created_at, attached_at, detached_at, grace_expires_at, ended_at, COALESCE(end_reason, '')
	FROM terminal_sessions`

func scanTerminalSession(row pgx.Row) (repository.TerminalSession, error) {
	var s repository.TerminalSession
	err := row.Scan(&s.ID, &s.OrganizationID, &s.LabInstanceID, &s.UserID, &s.ProviderResourceID, &s.Generation, &s.Status,
		&s.AttachTokenHash, &s.TokenExpiresAt, &s.CreatedAt, &s.AttachedAt, &s.DetachedAt, &s.GraceExpiresAt, &s.EndedAt, &s.EndReason)
	return s, err
}

func (q queries) TerminalSessionByID(ctx context.Context, id uuid.UUID) (repository.TerminalSession, error) {
	const op = "TerminalSessionByID"

	session, err := scanTerminalSession(q.db.QueryRow(ctx, terminalSessionSelect+` WHERE id = $1`, id))
	if err != nil {
		return repository.TerminalSession{}, normalize(op, err)
	}
	return session, nil
}

func (q queries) UnendedTerminalSessionsByLabInstance(ctx context.Context, labInstanceID uuid.UUID) ([]repository.TerminalSession, error) {
	const op = "UnendedTerminalSessionsByLabInstance"

	rows, err := q.db.Query(ctx, terminalSessionSelect+` WHERE lab_instance_id = $1 AND status <> 'ENDED' ORDER BY created_at, id`, labInstanceID)
	if err != nil {
		return nil, normalize(op, err)
	}
	sessions, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (repository.TerminalSession, error) {
		return scanTerminalSession(row)
	})
	if err != nil {
		return nil, normalize(op, err)
	}
	return sessions, nil
}

// transition은 조건부 UPDATE 하나를 실행하고 행을 바꿨는지 반환한다. 조건 불일치는 오류가 아니다.
func (q queries) transition(ctx context.Context, op, sql string, args ...any) (bool, error) {
	tag, err := q.db.Exec(ctx, sql, args...)
	if err != nil {
		return false, normalize(op, err)
	}
	return tag.RowsAffected() == 1, nil
}

func (q queries) MarkTerminalSessionOpened(ctx context.Context, id uuid.UUID, at, graceExpiresAt time.Time) (bool, error) {
	return q.transition(ctx, "MarkTerminalSessionOpened",
		`UPDATE terminal_sessions SET status = 'DETACHED', detached_at = $2, grace_expires_at = $3
		 WHERE id = $1 AND status = 'OPENING'`,
		id, at, graceExpiresAt)
}

func (q queries) MarkTerminalSessionAttached(ctx context.Context, id uuid.UUID, at time.Time) (bool, error) {
	return q.transition(ctx, "MarkTerminalSessionAttached",
		`UPDATE terminal_sessions SET status = 'ACTIVE', attached_at = $2, detached_at = NULL, grace_expires_at = NULL
		 WHERE id = $1 AND status IN ('DETACHED', 'ACTIVE')`,
		id, at)
}

func (q queries) MarkTerminalSessionDetached(ctx context.Context, id uuid.UUID, at, graceExpiresAt time.Time) (bool, error) {
	return q.transition(ctx, "MarkTerminalSessionDetached",
		`UPDATE terminal_sessions SET status = 'DETACHED', detached_at = $2, grace_expires_at = $3
		 WHERE id = $1 AND status = 'ACTIVE'`,
		id, at, graceExpiresAt)
}

func (q queries) EndTerminalSession(ctx context.Context, id uuid.UUID, endedAt time.Time, reason string) (bool, error) {
	return q.transition(ctx, "EndTerminalSession",
		`UPDATE terminal_sessions SET status = 'ENDED', ended_at = $2, end_reason = $3, grace_expires_at = NULL
		 WHERE id = $1 AND status <> 'ENDED'`,
		id, endedAt, reason)
}
