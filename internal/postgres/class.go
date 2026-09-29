package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

func (q queries) ClassByID(ctx context.Context, id uuid.UUID) (repository.Class, error) {
	const op = "ClassByID"

	var class repository.Class
	err := q.db.QueryRow(ctx,
		`SELECT id, organization_id, name, created_at FROM classes WHERE id = $1`,
		id,
	).Scan(&class.ID, &class.OrganizationID, &class.Name, &class.CreatedAt)
	if err != nil {
		return repository.Class{}, normalize(op, err)
	}
	return class, nil
}

func (q queries) ClassMembership(ctx context.Context, classID, userID uuid.UUID) (repository.ClassMembership, error) {
	const op = "ClassMembership"

	var membership repository.ClassMembership
	err := q.db.QueryRow(ctx,
		`SELECT organization_id, class_id, user_id, role, created_at
		 FROM class_memberships
		 WHERE class_id = $1 AND user_id = $2`,
		classID, userID,
	).Scan(&membership.OrganizationID, &membership.ClassID, &membership.UserID, &membership.Role, &membership.CreatedAt)
	if err != nil {
		return repository.ClassMembership{}, normalize(op, err)
	}
	return membership, nil
}

func (q queries) ClassesByUser(ctx context.Context, userID uuid.UUID) ([]repository.ClassWithRole, error) {
	const op = "ClassesByUser"

	rows, err := q.db.Query(ctx,
		`SELECT c.id, c.organization_id, c.name, c.created_at, m.role
		 FROM class_memberships m
		 JOIN classes c ON c.id = m.class_id
		 WHERE m.user_id = $1
		 ORDER BY c.name, c.id`,
		userID,
	)
	if err != nil {
		return nil, normalize(op, err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (repository.ClassWithRole, error) {
		var item repository.ClassWithRole
		err := row.Scan(&item.Class.ID, &item.Class.OrganizationID, &item.Class.Name, &item.Class.CreatedAt, &item.Role)
		return item, err
	})
	if err != nil {
		return nil, normalize(op, err)
	}
	return items, nil
}
