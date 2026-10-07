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

// Connector는 connectors row 중 인증 판정에 필요한 값이다. revoke 여부는 판정하지 않고 저장된 값만 전달한다.
type Connector struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	RevokedAt      *time.Time
}

// ConnectorCredential은 connector_credentials row 중 인증 판정에 필요한 값이다.
// credential digest는 조회 key로만 쓰이므로 반환하지 않는다.
type ConnectorCredential struct {
	ID          uuid.UUID
	ConnectorID uuid.UUID
	RevokedAt   *time.Time
}

// ConnectorCredentialWithConnector는 Credential과 그 소유 Connector의 revoke 상태를 함께 담는다.
type ConnectorCredentialWithConnector struct {
	Credential ConnectorCredential
	Connector  Connector
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

// TerminalSessionStatus는 terminal_sessions.status 값이다.
type TerminalSessionStatus string

const (
	TerminalSessionOpening  TerminalSessionStatus = "OPENING"
	TerminalSessionActive   TerminalSessionStatus = "ACTIVE"
	TerminalSessionDetached TerminalSessionStatus = "DETACHED"
	TerminalSessionEnded    TerminalSessionStatus = "ENDED"
)

// LabInstance는 Terminal 권한 판정에 필요한 lab_instances row와 그 LabExecution의 Class다.
// status와 generation의 의미 판정은 Application이 한다.
type LabInstance struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	LabExecutionID uuid.UUID
	ClassID        uuid.UUID
	UserID         uuid.UUID
	Status         string
	Generation     int64
}

// SnapshotVM은 immutable CreationSnapshot의 vms[] 한 원소 중 Terminal target 판정에 필요한 field다.
// imageId, flavorId, flavorSpec 같은 resolve된 Provider 정보는 Repository 밖으로 나오지 않는다.
type SnapshotVM struct {
	VMKey         string
	Role          string
	InstanceIndex int64
}

// CreationSnapshotTargets는 LabInstance의 LabExecution에 고정된 immutable resolved CreationSnapshot에서 읽은 Terminal target projection이다.
// 저장된 값을 그대로 담으며 의미 검증(빈 값, 중복, workspaceVmKey가 vms에 있는지)은 Application이 한다.
type CreationSnapshotTargets struct {
	WorkspaceVMKey string
	VMs            []SnapshotVM
}

// ProviderServer는 provider_resources 중 resource_type이 SERVER인 row다.
// LifecycleStatus가 PRESENT인지 판단하는 것은 Application이다.
type ProviderServer struct {
	ID              uuid.UUID
	ProviderID      string
	LifecycleStatus string
}

// TerminalSession은 terminal_sessions row다. raw attach token은 저장하지 않으므로 digest만 담는다.
// Terminal INPUT/OUTPUT, transcript, exit code는 저장하지 않는다.
type TerminalSession struct {
	ID                 uuid.UUID
	OrganizationID     uuid.UUID
	LabInstanceID      uuid.UUID
	UserID             uuid.UUID
	ProviderResourceID uuid.UUID
	Generation         int64
	Status             TerminalSessionStatus
	AttachTokenHash    []byte
	TokenExpiresAt     time.Time
	CreatedAt          time.Time
	// 아래 시각은 nil이면 기록이 없다는 뜻이다.
	AttachedAt     *time.Time
	DetachedAt     *time.Time
	GraceExpiresAt *time.Time
	EndedAt        *time.Time
	// EndReason은 ENDED가 아니면 비어 있다.
	EndReason string
}

// NewTerminalSession은 OPENING으로 저장할 TerminalSession이다. 시각은 Application이 정해 전달한다.
type NewTerminalSession struct {
	ID                 uuid.UUID
	OrganizationID     uuid.UUID
	LabInstanceID      uuid.UUID
	UserID             uuid.UUID
	ProviderResourceID uuid.UUID
	Generation         int64
	AttachTokenHash    []byte
	TokenExpiresAt     time.Time
	CreatedAt          time.Time
}

// LiveSession은 live_sessions row다.
// Terminal/Live OUTPUT, transcript, queue 내용은 저장하지 않는다.
type LiveSession struct {
	ID                      uuid.UUID
	OrganizationID          uuid.UUID
	ClassID                 uuid.UUID
	SourceTerminalSessionID uuid.UUID
	InstructorUserID        uuid.UUID
	CreatedAt               time.Time
	// EndedAt은 nil이면 진행 중인 세션이다.
	EndedAt   *time.Time
	EndReason string
}

// NewLiveSession은 새로 생성할 LiveSession이다. 시각은 Application이 정해 전달한다.
type NewLiveSession struct {
	ID                      uuid.UUID
	OrganizationID          uuid.UUID
	ClassID                 uuid.UUID
	SourceTerminalSessionID uuid.UUID
	InstructorUserID        uuid.UUID
	CreatedAt               time.Time
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
