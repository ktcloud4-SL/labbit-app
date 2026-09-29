package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// userColumns/userJoins는 repository.User를 만드는 공통 projection이다.
// Local Account가 없는 User도 조회할 수 있도록 local_accounts는 LEFT JOIN한다.
// 호출하는 query는 users를 u로 별칭한다.
const (
	userColumns = `u.id, u.organization_id, o.name, u.organization_role, COALESCE(a.username, ''), u.disabled_at, a.password_changed_at`
	userJoins   = `JOIN organizations o ON o.id = u.organization_id LEFT JOIN local_accounts a ON a.user_id = u.id`
)

// userDest는 userColumns 순서의 Scan 대상이다.
func userDest(u *repository.User) []any {
	return []any{&u.ID, &u.OrganizationID, &u.OrganizationName, &u.OrganizationRole, &u.Username, &u.DisabledAt, &u.PasswordChangedAt}
}

func (q queries) LocalAccountByUsername(ctx context.Context, username string) (repository.LocalAccount, error) {
	const op = "LocalAccountByUsername"

	var (
		account repository.LocalAccount
		hash    string
	)
	err := q.db.QueryRow(ctx,
		`SELECT `+userColumns+`, a.password_hash
		 FROM users u `+userJoins+`
		 WHERE a.username = $1`,
		username,
	).Scan(append(userDest(&account.User), &hash)...)
	if err != nil {
		return repository.LocalAccount{}, normalize(op, err)
	}
	account.PasswordHash = repository.PasswordHash(hash)
	return account, nil
}

func (q queries) UserByID(ctx context.Context, id uuid.UUID) (repository.User, error) {
	const op = "UserByID"

	var user repository.User
	err := q.db.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users u `+userJoins+` WHERE u.id = $1`,
		id,
	).Scan(userDest(&user)...)
	if err != nil {
		return repository.User{}, normalize(op, err)
	}
	return user, nil
}

func (q queries) CreateAuthSession(ctx context.Context, session repository.AuthSession) error {
	return q.exec(ctx, "CreateAuthSession",
		`INSERT INTO auth_sessions (id, user_id, token_hash, created_at, expires_at, last_seen_at, revoked_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		session.ID, session.UserID, session.TokenHash, session.CreatedAt, session.ExpiresAt, session.LastSeenAt, session.RevokedAt,
	)
}

func (q queries) AuthSessionByTokenHash(ctx context.Context, tokenHash []byte) (repository.AuthSessionWithUser, error) {
	const op = "AuthSessionByTokenHash"

	var found repository.AuthSessionWithUser
	session := &found.Session
	dest := append(
		[]any{&session.ID, &session.UserID, &session.TokenHash, &session.CreatedAt, &session.ExpiresAt, &session.LastSeenAt, &session.RevokedAt},
		userDest(&found.User)...,
	)
	err := q.db.QueryRow(ctx,
		`SELECT s.id, s.user_id, s.token_hash, s.created_at, s.expires_at, s.last_seen_at, s.revoked_at, `+userColumns+`
		 FROM auth_sessions s JOIN users u ON u.id = s.user_id `+userJoins+`
		 WHERE s.token_hash = $1`,
		tokenHash,
	).Scan(dest...)
	if err != nil {
		return repository.AuthSessionWithUser{}, normalize(op, err)
	}
	return found, nil
}

func (q queries) RevokeAuthSession(ctx context.Context, id uuid.UUID, revokedAt time.Time) error {
	const op = "RevokeAuthSession"

	// COALESCE로 처음 기록한 revoked_at을 유지해 중복 Logout이 안전하게 성공한다.
	tag, err := q.db.Exec(ctx,
		`UPDATE auth_sessions SET revoked_at = COALESCE(revoked_at, $2) WHERE id = $1`,
		id, revokedAt,
	)
	if err != nil {
		return normalize(op, err)
	}
	if tag.RowsAffected() == 0 {
		return notFound(op)
	}
	return nil
}

// exec는 결과 row가 필요 없는 statement를 실행하고 오류를 정규화한다.
func (q queries) exec(ctx context.Context, op, sql string, args ...any) error {
	_, err := q.db.Exec(ctx, sql, args...)
	return normalize(op, err)
}
