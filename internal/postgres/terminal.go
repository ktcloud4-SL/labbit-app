package postgres

import (
	"context"
	"encoding/json"
	"errors"
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

// snapshotVM은 creation_snapshots.snapshot의 vms[] 원소 중 Repository가 읽는 field다. 나머지(imageId, flavorId 등)는 읽지 않는다.
// instanceIndex는 누락(nil)과 0을 구분하기 위해 pointer다.
type snapshotVM struct {
	VMKey         string `json:"vmKey"`
	Role          string `json:"role"`
	InstanceIndex *int64 `json:"instanceIndex"`
}

func (q queries) CreationSnapshotTargets(ctx context.Context, labInstanceID uuid.UUID) (repository.CreationSnapshotTargets, error) {
	const op = "CreationSnapshotTargets"

	// 필요한 두 key만 jsonb로 꺼낸다. startupScript 같은 나머지 snapshot은 DB 밖으로 읽어 오지 않는다.
	// pgx는 jsonb를 string으로 scan하면 JSON 원문(따옴표 포함)을 주므로 raw로 받아 직접 decode한다.
	var rawVMs, rawWorkspace []byte
	err := q.db.QueryRow(ctx,
		`SELECT cs.snapshot -> 'vms', cs.snapshot -> 'workspaceVmKey'
		 FROM lab_instances li
		 JOIN creation_snapshots cs ON cs.organization_id = li.organization_id AND cs.lab_execution_id = li.lab_execution_id
		 WHERE li.id = $1`,
		labInstanceID,
	).Scan(&rawVMs, &rawWorkspace)
	if err != nil {
		return repository.CreationSnapshotTargets{}, normalize(op, err)
	}

	// 읽을 수 없는 shape의 오류에는 snapshot의 값을 담지 않는다. 어느 field인지만 남긴다.
	unreadable := func(field string) error {
		return &repository.Error{Kind: repository.KindInternal, Op: op, Cause: errors.New(field + "를 projection 타입으로 읽을 수 없음")}
	}

	// SQL NULL(key 없음)과 JSON null은 빈 값이다. 의미 검증은 Application이 한다.
	var workspaceVMKey *string
	if len(rawWorkspace) > 0 {
		if err := json.Unmarshal(rawWorkspace, &workspaceVMKey); err != nil {
			return repository.CreationSnapshotTargets{}, unreadable("workspaceVmKey")
		}
	}
	var vms []snapshotVM
	if len(rawVMs) > 0 {
		if err := json.Unmarshal(rawVMs, &vms); err != nil {
			return repository.CreationSnapshotTargets{}, unreadable("vms")
		}
	}

	targets := repository.CreationSnapshotTargets{VMs: make([]repository.SnapshotVM, 0, len(vms))}
	if workspaceVMKey != nil {
		targets.WorkspaceVMKey = *workspaceVMKey
	}
	for _, vm := range vms {
		if vm.InstanceIndex == nil {
			// 0과 누락을 구분하지 못한 채 0으로 해석하면 key는 있지만 shape가 틀린 snapshot이 유효해 보인다.
			return repository.CreationSnapshotTargets{}, unreadable("vms[].instanceIndex")
		}
		targets.VMs = append(targets.VMs, repository.SnapshotVM{VMKey: vm.VMKey, Role: vm.Role, InstanceIndex: *vm.InstanceIndex})
	}
	return targets, nil
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
