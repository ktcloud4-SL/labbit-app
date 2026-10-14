package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// LabSpecForShare는 LabSpecForUpdate와 같은 순서(본문 row 잠금 → VM Role 읽기)이며 잠금만 FOR SHARE다.
// LabSpec 수정(FOR UPDATE)은 이 잠금이 풀릴 때까지 기다리고, 진행 중인 수정이 있으면 이 query가 그 commit을 기다린 뒤 새 revision을 읽는다.
func (q queries) LabSpecForShare(ctx context.Context, id uuid.UUID) (repository.LabSpec, error) {
	const op = "LabSpecForShare"

	spec, err := scanLabSpec(q.db.QueryRow(ctx, `SELECT `+labSpecColumns+` FROM lab_specs s WHERE s.id = $1 FOR SHARE`, id))
	if err != nil {
		return repository.LabSpec{}, normalize(op, err)
	}
	if spec.VMRoles, err = q.labSpecVMRoles(ctx, spec.ID); err != nil {
		return repository.LabSpec{}, normalize(op, err)
	}
	return spec, nil
}

func (q queries) ResolvedImageMappingsForShare(ctx context.Context, organizationID uuid.UUID, ids []uuid.UUID) ([]repository.ResolvedImageMapping, error) {
	const op = "ResolvedImageMappingsForShare"

	rows, err := q.db.Query(ctx,
		`SELECT id, organization_id, provider_connection_id, is_active, provider_image_id
		 FROM provider_image_mappings
		 WHERE organization_id = $1 AND id = ANY($2::uuid[])
		 ORDER BY id
		 FOR SHARE`, organizationID, uuidTexts(ids))
	if err != nil {
		return nil, normalize(op, err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (repository.ResolvedImageMapping, error) {
		var m repository.ResolvedImageMapping
		err := row.Scan(&m.ID, &m.OrganizationID, &m.ProviderConnectionID, &m.Active, &m.ProviderImageID)
		return m, err
	})
	if err != nil {
		return nil, normalize(op, err)
	}
	return items, nil
}

func (q queries) ResolvedFlavorMappingsForShare(ctx context.Context, organizationID uuid.UUID, ids []uuid.UUID) ([]repository.ResolvedFlavorMapping, error) {
	const op = "ResolvedFlavorMappingsForShare"

	rows, err := q.db.Query(ctx,
		`SELECT id, organization_id, provider_connection_id, is_active, provider_flavor_id, vcpus, ram_mib, disk_gib
		 FROM provider_flavor_mappings
		 WHERE organization_id = $1 AND id = ANY($2::uuid[])
		 ORDER BY id
		 FOR SHARE`, organizationID, uuidTexts(ids))
	if err != nil {
		return nil, normalize(op, err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (repository.ResolvedFlavorMapping, error) {
		var m repository.ResolvedFlavorMapping
		err := row.Scan(&m.ID, &m.OrganizationID, &m.ProviderConnectionID, &m.Active, &m.ProviderFlavorID, &m.VCPUs, &m.RAMMiB, &m.DiskGiB)
		return m, err
	})
	if err != nil {
		return nil, normalize(op, err)
	}
	return items, nil
}

func (q queries) ClassMembershipsForShare(ctx context.Context, classID uuid.UUID, userIDs []uuid.UUID) ([]repository.ClassMembership, error) {
	const op = "ClassMembershipsForShare"

	rows, err := q.db.Query(ctx,
		`SELECT organization_id, class_id, user_id, role, created_at
		 FROM class_memberships
		 WHERE class_id = $1 AND user_id = ANY($2::uuid[])
		 ORDER BY user_id
		 FOR SHARE`, classID, uuidTexts(userIDs))
	if err != nil {
		return nil, normalize(op, err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (repository.ClassMembership, error) {
		var m repository.ClassMembership
		err := row.Scan(&m.OrganizationID, &m.ClassID, &m.UserID, &m.Role, &m.CreatedAt)
		return m, err
	})
	if err != nil {
		return nil, normalize(op, err)
	}
	return items, nil
}

func (q queries) CreateLabExecution(ctx context.Context, e repository.NewLabExecution) error {
	_, err := q.db.Exec(ctx,
		`INSERT INTO lab_executions (id, organization_id, class_id, lab_spec_id, instructor_user_id, status, started_at, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $7)`,
		e.ID, e.OrganizationID, e.ClassID, e.LabSpecID, e.InstructorUserID, e.Status, e.CreatedAt)
	return normalize("CreateLabExecution", err)
}

func (q queries) CreateCreationSnapshot(ctx context.Context, s repository.NewCreationSnapshot) error {
	// schema_version은 DB default(1)다. snapshot은 jsonb object Check가 형식을 마지막으로 확인한다.
	_, err := q.db.Exec(ctx,
		`INSERT INTO creation_snapshots (id, organization_id, lab_execution_id, source_lab_spec_id, source_lab_spec_revision,
			provider_connection_id, snapshot, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8)`,
		s.ID, s.OrganizationID, s.LabExecutionID, s.SourceLabSpecID, s.SourceLabSpecRevision, s.ProviderConnectionID, string(s.Snapshot), s.CreatedAt)
	return normalize("CreateCreationSnapshot", err)
}

func (q queries) CreateLabInstance(ctx context.Context, i repository.NewLabInstance) error {
	_, err := q.db.Exec(ctx,
		`INSERT INTO lab_instances (id, organization_id, lab_execution_id, user_id, participant_role, status, generation, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, 1, $7, $7)`,
		i.ID, i.OrganizationID, i.LabExecutionID, i.UserID, string(i.ParticipantRole), i.Status, i.CreatedAt)
	return normalize("CreateLabInstance", err)
}

// LabExecutionByID는 본문과 LabInstance를 statement 하나(하나의 snapshot)로 읽는다.
func (q queries) LabExecutionByID(ctx context.Context, id uuid.UUID) (repository.LabExecution, error) {
	const op = "LabExecutionByID"

	rows, err := q.db.Query(ctx,
		`SELECT e.id, e.organization_id, e.class_id, e.lab_spec_id, e.instructor_user_id, e.status, e.started_at, e.finished_at,
			i.id, i.organization_id, i.user_id, i.participant_role, i.status, i.generation
		 FROM lab_executions e
		 LEFT JOIN lab_instances i ON i.lab_execution_id = e.id AND i.organization_id = e.organization_id
		 WHERE e.id = $1
		 ORDER BY CASE i.participant_role WHEN 'INSTRUCTOR' THEN 0 ELSE 1 END, i.user_id`,
		id,
	)
	if err != nil {
		return repository.LabExecution{}, normalize(op, err)
	}
	defer rows.Close()

	var execution repository.LabExecution
	found := false
	for rows.Next() {
		var (
			e          repository.LabExecution
			instanceID *uuid.UUID
			orgID      *uuid.UUID
			userID     *uuid.UUID
			role       *string
			status     *string
			generation *int64
		)
		if err := rows.Scan(&e.ID, &e.OrganizationID, &e.ClassID, &e.LabSpecID, &e.InstructorUserID, &e.Status, &e.StartedAt, &e.FinishedAt,
			&instanceID, &orgID, &userID, &role, &status, &generation); err != nil {
			return repository.LabExecution{}, normalize(op, err)
		}
		if !found {
			execution, found = e, true
		}
		// LabInstance가 없는 LabExecution도 LEFT JOIN이므로 본문 row 하나가 온다. 판정(빈 목록은 모순)은 Application이 한다.
		if instanceID != nil {
			execution.LabInstances = append(execution.LabInstances, repository.LabInstanceSummary{
				ID: *instanceID, OrganizationID: *orgID, LabExecutionID: e.ID, UserID: *userID,
				ParticipantRole: repository.ClassRole(*role), Status: *status, Generation: *generation,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return repository.LabExecution{}, normalize(op, err)
	}
	if !found {
		return repository.LabExecution{}, notFound(op)
	}
	return execution, nil
}

func (q queries) ActiveLabExecutionByClass(ctx context.Context, classID uuid.UUID) (repository.LabExecutionSummary, error) {
	var s repository.LabExecutionSummary
	err := q.db.QueryRow(ctx,
		`SELECT id, status FROM lab_executions WHERE class_id = $1 AND finished_at IS NULL`, classID,
	).Scan(&s.ID, &s.Status)
	return s, normalize("ActiveLabExecutionByClass", err)
}

func (q queries) LabInstanceByExecutionUser(ctx context.Context, labExecutionID, userID uuid.UUID) (repository.LabInstanceSummary, error) {
	var i repository.LabInstanceSummary
	var role string
	err := q.db.QueryRow(ctx,
		`SELECT id, organization_id, lab_execution_id, user_id, participant_role, status, generation
		 FROM lab_instances WHERE lab_execution_id = $1 AND user_id = $2`, labExecutionID, userID,
	).Scan(&i.ID, &i.OrganizationID, &i.LabExecutionID, &i.UserID, &role, &i.Status, &i.Generation)
	i.ParticipantRole = repository.ClassRole(role)
	return i, normalize("LabInstanceByExecutionUser", err)
}

// uuidTexts는 uuid 목록을 PostgreSQL uuid[]로 cast할 text 배열로 바꾼다.
func uuidTexts(ids []uuid.UUID) []string {
	texts := make([]string, len(ids))
	for i, id := range ids {
		texts[i] = id.String()
	}
	return texts
}
