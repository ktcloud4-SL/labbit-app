package postgres

import (
	"context"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// 아래 INSERT는 Schema Migration이 아닌 trusted operator Bootstrap이 사용한다.
// created_at은 DB default를 사용한다.

func (q queries) CreateOrganization(ctx context.Context, organization repository.NewOrganization) error {
	return q.exec(ctx, "CreateOrganization",
		`INSERT INTO organizations (id, name) VALUES ($1, $2)`,
		organization.ID, organization.Name,
	)
}

func (q queries) CreateUser(ctx context.Context, user repository.NewUser) error {
	return q.exec(ctx, "CreateUser",
		`INSERT INTO users (id, organization_id, organization_role) VALUES ($1, $2, $3)`,
		user.ID, user.OrganizationID, user.OrganizationRole,
	)
}

func (q queries) CreateLocalAccount(ctx context.Context, account repository.NewLocalAccount) error {
	return q.exec(ctx, "CreateLocalAccount",
		`INSERT INTO local_accounts (user_id, username, password_hash) VALUES ($1, $2, $3)`,
		// PasswordHash는 log에서 가려지는 타입이므로 저장 경계에서만 원문으로 변환한다.
		account.UserID, account.Username, string(account.PasswordHash),
	)
}

func (q queries) CreateClass(ctx context.Context, class repository.NewClass) error {
	return q.exec(ctx, "CreateClass",
		`INSERT INTO classes (id, organization_id, name) VALUES ($1, $2, $3)`,
		class.ID, class.OrganizationID, class.Name,
	)
}

func (q queries) CreateClassMembership(ctx context.Context, membership repository.NewClassMembership) error {
	return q.exec(ctx, "CreateClassMembership",
		`INSERT INTO class_memberships (organization_id, class_id, user_id, role) VALUES ($1, $2, $3, $4)`,
		membership.OrganizationID, membership.ClassID, membership.UserID, membership.Role,
	)
}
