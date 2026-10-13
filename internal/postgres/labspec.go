package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

func (q queries) ClassMembers(ctx context.Context, classID uuid.UUID) ([]repository.ClassMember, error) {
	const op = "ClassMembers"

	// Local Account가 없는 User도 Membership이므로 LEFT JOIN이다. INSTRUCTOR가 STUDENT보다 먼저 온다.
	rows, err := q.db.Query(ctx,
		`SELECT m.organization_id, m.user_id, coalesce(a.username, ''), m.role
		 FROM class_memberships m
		 LEFT JOIN local_accounts a ON a.user_id = m.user_id
		 WHERE m.class_id = $1
		 ORDER BY CASE m.role WHEN 'INSTRUCTOR' THEN 0 ELSE 1 END, coalesce(a.username, ''), m.user_id`,
		classID,
	)
	if err != nil {
		return nil, normalize(op, err)
	}
	members, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (repository.ClassMember, error) {
		var m repository.ClassMember
		err := row.Scan(&m.OrganizationID, &m.UserID, &m.Username, &m.Role)
		return m, err
	})
	if err != nil {
		return nil, normalize(op, err)
	}
	return members, nil
}

func (q queries) HasClassRole(ctx context.Context, userID uuid.UUID, role repository.ClassRole) (bool, error) {
	const op = "HasClassRole"

	var exists bool
	err := q.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM class_memberships WHERE user_id = $1 AND role = $2)`,
		userID, string(role),
	).Scan(&exists)
	if err != nil {
		return false, normalize(op, err)
	}
	return exists, nil
}

func (q queries) LabSpecSummariesByOwner(ctx context.Context, organizationID, ownerUserID uuid.UUID) ([]repository.LabSpecSummary, error) {
	const op = "LabSpecSummariesByOwner"

	rows, err := q.db.Query(ctx,
		`SELECT id, organization_id, owner_user_id, name, description
		 FROM lab_specs
		 WHERE organization_id = $1 AND owner_user_id = $2
		 ORDER BY name, id`,
		organizationID, ownerUserID,
	)
	if err != nil {
		return nil, normalize(op, err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (repository.LabSpecSummary, error) {
		var s repository.LabSpecSummary
		err := row.Scan(&s.ID, &s.OrganizationID, &s.OwnerUserID, &s.Name, &s.Description)
		return s, err
	})
	if err != nil {
		return nil, normalize(op, err)
	}
	return items, nil
}

const labSpecColumns = `s.id, s.organization_id, s.owner_user_id, s.name, s.description, s.revision, s.internet_outbound,
	s.startup_script, s.workspace_role, s.workspace_instance_index, s.created_at, s.updated_at`

// LabSpecByID는 본문과 VM Role을 statement 하나로 읽는다. PostgreSQL은 statement 하나를 하나의 snapshot으로 실행하므로
// 같은 commit의 본문(revision)과 VM Role만 함께 보인다. 본문과 VM Role을 별도 query로 읽으면 READ COMMITTED에서는 두 query
// 사이에 commit된 수정 때문에(transaction으로 묶어도 statement마다 snapshot이 새로 잡힘) 이전 revision과 새 VM Role이 섞일 수 있다.
func (q queries) LabSpecByID(ctx context.Context, id uuid.UUID) (repository.LabSpec, error) {
	const op = "LabSpecByID"

	rows, err := q.db.Query(ctx,
		`SELECT `+labSpecColumns+`,
			r.id, r.role, r.display_name, r.image_mapping_id, r.flavor_mapping_id, r.vm_count
		 FROM lab_specs s
		 LEFT JOIN lab_spec_vm_roles r ON r.lab_spec_id = s.id
		 WHERE s.id = $1
		 ORDER BY r.role`,
		id,
	)
	if err != nil {
		return repository.LabSpec{}, normalize(op, err)
	}
	defer rows.Close()

	var spec repository.LabSpec
	found := false
	for rows.Next() {
		var (
			s       repository.LabSpec
			roleID  *uuid.UUID
			role    repository.LabSpecVMRole
			roleStr *string
			image   *uuid.UUID
			flavor  *uuid.UUID
			count   *int64
		)
		if err := rows.Scan(&s.ID, &s.OrganizationID, &s.OwnerUserID, &s.Name, &s.Description, &s.Revision, &s.InternetOutbound,
			&s.StartupScript, &s.WorkspaceRole, &s.WorkspaceInstanceIndex, &s.CreatedAt, &s.UpdatedAt,
			&roleID, &roleStr, &role.DisplayName, &image, &flavor, &count); err != nil {
			return repository.LabSpec{}, normalize(op, err)
		}
		if !found {
			spec, found = s, true
		}
		// VM Role이 없는 LabSpec도 LEFT JOIN이므로 본문 row 하나가 온다. 판정(빈 VM Role은 모순)은 Application이 한다.
		if roleID != nil {
			role.ID, role.Role, role.ImageMappingID, role.FlavorMappingID, role.VMCount = *roleID, *roleStr, *image, *flavor, *count
			spec.VMRoles = append(spec.VMRoles, role)
		}
	}
	if err := rows.Err(); err != nil {
		return repository.LabSpec{}, normalize(op, err)
	}
	if !found {
		return repository.LabSpec{}, notFound(op)
	}
	return spec, nil
}

// LabSpecForUpdate는 먼저 본문 row를 FOR UPDATE로 잠그고 그 뒤 VM Role을 읽는다. VM Role을 바꾸는 쓰기(UpdateLabSpec)는 항상 같은
// transaction에서 본문 row를 먼저 UPDATE(잠금)하므로, 잠금을 얻은 뒤에 시작한 VM Role query는 잠금을 가진 동안 바뀌지 않는 마지막 commit
// 상태를 본다. FOR UPDATE를 outer join과 함께 쓰면 갱신된 본문을 다시 평가할 때 VM Role이 이전 row로 남을 수 있어 나누어 읽는다.
func (q queries) LabSpecForUpdate(ctx context.Context, id uuid.UUID) (repository.LabSpec, error) {
	const op = "LabSpecForUpdate"

	spec, err := scanLabSpec(q.db.QueryRow(ctx, `SELECT `+labSpecColumns+` FROM lab_specs s WHERE s.id = $1 FOR UPDATE`, id))
	if err != nil {
		return repository.LabSpec{}, normalize(op, err)
	}
	if spec.VMRoles, err = q.labSpecVMRoles(ctx, spec.ID); err != nil {
		return repository.LabSpec{}, normalize(op, err)
	}
	return spec, nil
}

func scanLabSpec(row pgx.Row) (repository.LabSpec, error) {
	var s repository.LabSpec
	err := row.Scan(&s.ID, &s.OrganizationID, &s.OwnerUserID, &s.Name, &s.Description, &s.Revision, &s.InternetOutbound,
		&s.StartupScript, &s.WorkspaceRole, &s.WorkspaceInstanceIndex, &s.CreatedAt, &s.UpdatedAt)
	return s, err
}

// labSpecVMRoles는 VM Role을 role 순서로 읽는다. lab_spec_vm_roles에는 입력 순서를 기록하는 column이 없다.
func (q queries) labSpecVMRoles(ctx context.Context, labSpecID uuid.UUID) ([]repository.LabSpecVMRole, error) {
	rows, err := q.db.Query(ctx,
		`SELECT id, role, display_name, image_mapping_id, flavor_mapping_id, vm_count
		 FROM lab_spec_vm_roles
		 WHERE lab_spec_id = $1
		 ORDER BY role`,
		labSpecID,
	)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (repository.LabSpecVMRole, error) {
		var r repository.LabSpecVMRole
		err := row.Scan(&r.ID, &r.Role, &r.DisplayName, &r.ImageMappingID, &r.FlavorMappingID, &r.VMCount)
		return r, err
	})
}

func (q queries) ImageMappingsForShare(ctx context.Context, organizationID uuid.UUID, ids []uuid.UUID) ([]repository.ProviderMapping, error) {
	return q.mappingsForShare(ctx, "ImageMappingsForShare",
		`SELECT id, organization_id, provider_connection_id, is_active
		 FROM provider_image_mappings
		 WHERE organization_id = $1 AND id = ANY($2::uuid[])
		 ORDER BY id
		 FOR SHARE`, organizationID, ids)
}

func (q queries) FlavorMappingsForShare(ctx context.Context, organizationID uuid.UUID, ids []uuid.UUID) ([]repository.ProviderMapping, error) {
	return q.mappingsForShare(ctx, "FlavorMappingsForShare",
		`SELECT id, organization_id, provider_connection_id, is_active
		 FROM provider_flavor_mappings
		 WHERE organization_id = $1 AND id = ANY($2::uuid[])
		 ORDER BY id
		 FOR SHARE`, organizationID, ids)
}

func (q queries) mappingsForShare(ctx context.Context, op, query string, organizationID uuid.UUID, ids []uuid.UUID) ([]repository.ProviderMapping, error) {
	texts := make([]string, len(ids))
	for i, id := range ids {
		texts[i] = id.String()
	}
	rows, err := q.db.Query(ctx, query, organizationID, texts)
	if err != nil {
		return nil, normalize(op, err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (repository.ProviderMapping, error) {
		var m repository.ProviderMapping
		err := row.Scan(&m.ID, &m.OrganizationID, &m.ProviderConnectionID, &m.Active)
		return m, err
	})
	if err != nil {
		return nil, normalize(op, err)
	}
	return items, nil
}

func (q queries) CreateLabSpec(ctx context.Context, spec repository.NewLabSpec) (repository.LabSpec, error) {
	const op = "CreateLabSpec"

	d := spec.LabSpecDefinition
	created, err := scanLabSpec(q.db.QueryRow(ctx,
		`INSERT INTO lab_specs (id, organization_id, owner_user_id, name, description, internet_outbound,
			startup_script, workspace_role, workspace_instance_index)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING id, organization_id, owner_user_id, name, description, revision, internet_outbound,
			startup_script, workspace_role, workspace_instance_index, created_at, updated_at`,
		spec.ID, spec.OrganizationID, spec.OwnerUserID, d.Name, d.Description, d.InternetOutbound,
		d.StartupScript, d.WorkspaceRole, d.WorkspaceInstanceIndex,
	))
	if err != nil {
		return repository.LabSpec{}, normalize(op, err)
	}
	if err := q.insertLabSpecVMRoles(ctx, spec.OrganizationID, spec.ID, d.VMRoles); err != nil {
		return repository.LabSpec{}, normalize(op, err)
	}
	if created.VMRoles, err = q.labSpecVMRoles(ctx, created.ID); err != nil {
		return repository.LabSpec{}, normalize(op, err)
	}
	return created, nil
}

func (q queries) UpdateLabSpec(ctx context.Context, update repository.LabSpecUpdate) (repository.LabSpec, bool, error) {
	const op = "UpdateLabSpec"

	d := update.LabSpecDefinition
	updated, err := scanLabSpec(q.db.QueryRow(ctx,
		`UPDATE lab_specs
		 SET name = $4, description = $5, internet_outbound = $6, startup_script = $7,
			workspace_role = $8, workspace_instance_index = $9, revision = revision + 1, updated_at = now()
		 WHERE id = $1 AND organization_id = $2 AND revision = $3
		 RETURNING id, organization_id, owner_user_id, name, description, revision, internet_outbound,
			startup_script, workspace_role, workspace_instance_index, created_at, updated_at`,
		update.ID, update.OrganizationID, update.ExpectedRevision, d.Name, d.Description, d.InternetOutbound,
		d.StartupScript, d.WorkspaceRole, d.WorkspaceInstanceIndex,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return repository.LabSpec{}, false, nil
	}
	if err != nil {
		return repository.LabSpec{}, false, normalize(op, err)
	}
	// VM Role은 외부에서 참조하는 row가 없으므로(CreationSnapshot은 resolve한 값을 따로 고정함) 교체한다.
	if _, err := q.db.Exec(ctx, `DELETE FROM lab_spec_vm_roles WHERE lab_spec_id = $1`, update.ID); err != nil {
		return repository.LabSpec{}, false, normalize(op, err)
	}
	if err := q.insertLabSpecVMRoles(ctx, update.OrganizationID, update.ID, d.VMRoles); err != nil {
		return repository.LabSpec{}, false, normalize(op, err)
	}
	if updated.VMRoles, err = q.labSpecVMRoles(ctx, updated.ID); err != nil {
		return repository.LabSpec{}, false, normalize(op, err)
	}
	return updated, true, nil
}

func (q queries) insertLabSpecVMRoles(ctx context.Context, organizationID, labSpecID uuid.UUID, roles []repository.NewLabSpecVMRole) error {
	for _, r := range roles {
		if _, err := q.db.Exec(ctx,
			`INSERT INTO lab_spec_vm_roles (id, organization_id, lab_spec_id, role, display_name,
				image_mapping_id, flavor_mapping_id, vm_count)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			r.ID, organizationID, labSpecID, r.Role, r.DisplayName, r.ImageMappingID, r.FlavorMappingID, r.VMCount,
		); err != nil {
			return err
		}
	}
	return nil
}
