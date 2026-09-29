package repository

import (
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// OrganizationRole은 users.organization_role 값이다. Class 역할과 독립적으로 판단한다.
type OrganizationRole string

const (
	OrganizationRoleAdmin  OrganizationRole = "ADMIN"
	OrganizationRoleMember OrganizationRole = "MEMBER"
)

func (r OrganizationRole) Valid() bool {
	return r == OrganizationRoleAdmin || r == OrganizationRoleMember
}

// ClassRole은 class_memberships.role 값이다.
type ClassRole string

const (
	ClassRoleInstructor ClassRole = "INSTRUCTOR"
	ClassRoleStudent    ClassRole = "STUDENT"
)

func (r ClassRole) Valid() bool {
	return r == ClassRoleInstructor || r == ClassRoleStudent
}

const redacted = "[REDACTED]"

// PasswordHash는 Argon2id PHC 문자열이다. log, 오류 문자열, JSON에 실수로 출력되지 않도록 값을 가린다.
// 저장·검증처럼 원문이 필요한 경계에서만 string(hash)로 변환한다.
type PasswordHash string

func (PasswordHash) String() string               { return redacted }
func (PasswordHash) GoString() string             { return redacted }
func (PasswordHash) LogValue() slog.Value         { return slog.StringValue(redacted) }
func (PasswordHash) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// User는 Organization/Class 관계와 /me 응답에 필요한 값을 담은 persistence record다.
// disabled 여부나 권한은 판정하지 않고 저장된 값만 전달한다.
type User struct {
	ID               uuid.UUID
	OrganizationID   uuid.UUID
	OrganizationName string
	OrganizationRole OrganizationRole
	// Username은 Local Account의 username이다. Local Account가 없으면 빈 문자열이다.
	Username   string
	DisabledAt *time.Time
	// PasswordChangedAt은 Local Account가 없거나 Password를 변경한 적이 없으면 nil이다.
	PasswordChangedAt *time.Time
}

// LocalAccount는 username으로 조회한 로그인 후보다. Password 검증은 Application 책임이다.
type LocalAccount struct {
	User         User
	PasswordHash PasswordHash
}

// AuthSession은 auth_sessions row다. 시각과 token digest는 Application이 정해 전달한다.
type AuthSession struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	TokenHash []byte
	CreatedAt time.Time
	ExpiresAt time.Time
	// LastSeenAt과 RevokedAt은 nil이면 기록이 없다는 뜻이다.
	LastSeenAt *time.Time
	RevokedAt  *time.Time
}

// AuthSessionWithUser는 Session 유효성 판정에 필요한 User 상태를 함께 담는다.
// revoke·만료·disabled·Password 변경 여부는 이 값을 보고 Application이 판정한다.
type AuthSessionWithUser struct {
	Session AuthSession
	User    User
}

type Class struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	Name           string
	CreatedAt      time.Time
}

type ClassMembership struct {
	OrganizationID uuid.UUID
	ClassID        uuid.UUID
	UserID         uuid.UUID
	Role           ClassRole
	CreatedAt      time.Time
}

// ClassWithRole은 사용자가 참여한 Class와 그 Class에서의 역할이다.
type ClassWithRole struct {
	Class Class
	Role  ClassRole
}

// 아래 New* 값은 trusted operator Bootstrap이 저장할 입력이다. created_at은 DB default를 사용한다.

type NewOrganization struct {
	ID   uuid.UUID
	Name string
}

type NewUser struct {
	ID               uuid.UUID
	OrganizationID   uuid.UUID
	OrganizationRole OrganizationRole
}

// NewLocalAccount의 PasswordHash는 이미 준비된 PHC 문자열이다. 이 계층은 해시를 만들거나 검증하지 않는다.
type NewLocalAccount struct {
	UserID       uuid.UUID
	Username     string
	PasswordHash PasswordHash
}

type NewClass struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	Name           string
}

type NewClassMembership struct {
	OrganizationID uuid.UUID
	ClassID        uuid.UUID
	UserID         uuid.UUID
	Role           ClassRole
}
