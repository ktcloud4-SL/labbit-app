// Package repository는 Application이 사용하는 persistence port를 정의한다.
//
// Application은 이 package의 interface, record, typed error만 알고 SQL/pgx를 알지 않는다.
// PostgreSQL 구현은 internal/postgres가 제공하며 이 package는 pgx에 의존하지 않는다.
//
// Repository는 전달받은 값을 저장·조회하고 DB 오류를 정규화하는 책임만 가진다.
// Session 유효성, Class 접근 권한, 403/404 같은 정책은 Application이 판단한다.
package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// IdentityRepository는 로그인과 Session 검증이 사용하는 query다.
type IdentityRepository interface {
	// LocalAccountByUsername은 username이 정확히 일치하는 계정을 반환한다. 없으면 ErrNotFound다.
	// disabled 계정도 그대로 반환하며 로그인 허용 여부는 Application이 판단한다.
	LocalAccountByUsername(ctx context.Context, username string) (LocalAccount, error)
	// UserByID는 ID로 User를 반환한다. 없으면 ErrNotFound다.
	UserByID(ctx context.Context, id uuid.UUID) (User, error)

	// CreateAuthSession은 전달받은 Session을 그대로 저장한다.
	// 저장된 token_hash가 이미 있으면 ErrConflict, expires_at이 created_at 이하이면 ErrConstraintViolation이다.
	CreateAuthSession(ctx context.Context, session AuthSession) error
	// AuthSessionByTokenHash는 token digest로 Session과 User 상태를 반환한다. 없으면 ErrNotFound다.
	// revoke되었거나 만료된 Session도 필터링하지 않고 반환한다.
	AuthSessionByTokenHash(ctx context.Context, tokenHash []byte) (AuthSessionWithUser, error)
	// RevokeAuthSession은 revoked_at을 기록한다. 이미 revoke된 Session은 처음 기록한 시각을 유지하고 성공한다.
	// Session이 없으면 ErrNotFound다.
	RevokeAuthSession(ctx context.Context, id uuid.UUID, revokedAt time.Time) error
}

// ClassRepository는 Class와 ClassMembership 조회다. 접근 가능 여부는 판단하지 않는다.
type ClassRepository interface {
	// ClassByID는 Organization과 무관하게 ID로 Class를 반환한다. 없으면 ErrNotFound다.
	ClassByID(ctx context.Context, id uuid.UUID) (Class, error)
	// ClassMembership은 (class, user) 참여 관계를 반환한다. 없으면 ErrNotFound다.
	ClassMembership(ctx context.Context, classID, userID uuid.UUID) (ClassMembership, error)
	// ClassesByUser는 사용자가 ClassMembership으로 참여 중인 Class를 이름, ID 순서로 반환한다.
	// 참여 중인 Class가 없으면 빈 목록이며 오류가 아니다.
	ClassesByUser(ctx context.Context, userID uuid.UUID) ([]ClassWithRole, error)
}

// BootstrapRepository는 trusted operator Bootstrap이 사용하는 INSERT다.
// 중복은 ErrConflict, 존재하지 않거나 다른 Organization의 참조와 잘못된 값은 ErrConstraintViolation이다.
type BootstrapRepository interface {
	CreateOrganization(ctx context.Context, organization NewOrganization) error
	CreateUser(ctx context.Context, user NewUser) error
	CreateLocalAccount(ctx context.Context, account NewLocalAccount) error
	CreateClass(ctx context.Context, class NewClass) error
	CreateClassMembership(ctx context.Context, membership NewClassMembership) error
}

// ConnectorRepository는 Connector Control WSS 인증과 연결 수명이 사용하는 query다.
// 각각 단일 statement이므로 Transaction 안의 Repositories에는 포함하지 않는다.
type ConnectorRepository interface {
	// ConnectorCredentialByHash는 credential digest로 Credential과 소유 Connector를 반환한다. 없으면 ErrNotFound다.
	// revoke된 Credential이나 Connector도 필터링하지 않고 반환하며 인증 허용 여부는 Application이 판단한다.
	ConnectorCredentialByHash(ctx context.Context, credentialHash []byte) (ConnectorCredentialWithConnector, error)
	// RecordConnectorHeartbeat는 connectors.last_seen_at을 seenAt으로 갱신하고 갱신했으면 true를 반환한다.
	// credentialID가 connectorID의 Credential이고 Credential과 Connector가 모두 revoke되지 않았을 때만 갱신하며,
	// 그렇지 않으면 아무것도 바꾸지 않고 false다. 구현은 Connector와 Credential row를 잠근 채 revoke 여부를
	// 확인하고 갱신하므로, commit되지 않은 revoke가 있으면 그 결과를 기다린다. revoke가 commit된 뒤에는 갱신되지 않고
	// rollback되면 갱신된다. 이 lock과 순서(connectors → connector_credentials)는 구현 계약이므로
	// 두 row를 함께 revoke하는 writer도 같은 순서를 지켜야 한다. seenAt은 Application이 정한 서버 수신 시각이다.
	RecordConnectorHeartbeat(ctx context.Context, connectorID, credentialID uuid.UUID, seenAt time.Time) (bool, error)
}

// TerminalRepository는 TerminalSession 생성·attach 권한 판정·lifecycle 전이가 사용하는 query와 조건부 UPDATE다.
// Terminal INPUT/OUTPUT, transcript, exit code를 저장하는 method는 없다.
type TerminalRepository interface {
	// LabInstanceForShare는 LabInstance를 FOR SHARE로 잠그고 반환한다. 없으면 ErrNotFound다.
	// transaction 안에서 사용한다. 잠금은 commit까지 유지되어 같은 row의 generation을 바꾸는 UPDATE(Reset)가 그때까지 기다린다.
	// 그래서 같은 transaction에서 만드는 TerminalSession은 읽은 generation과 항상 일치한다.
	LabInstanceForShare(ctx context.Context, id uuid.UUID) (LabInstance, error)
	// LabInstanceByID는 LabInstance를 잠그지 않고 반환한다. 없으면 ErrNotFound다.
	LabInstanceByID(ctx context.Context, id uuid.UUID) (LabInstance, error)
	// ConnectorIDForLabInstance는 LabInstance의 LabExecution CreationSnapshot이 가리키는 ProviderConnection의 Connector를 반환한다.
	// 그 관계가 하나라도 없으면 ErrNotFound다.
	ConnectorIDForLabInstance(ctx context.Context, labInstanceID uuid.UUID) (uuid.UUID, error)
	// CreationSnapshotTargets는 LabInstance의 LabExecution CreationSnapshot에서 vms[](vmKey, role, instanceIndex)와
	// workspaceVmKey만 읽어 반환한다. LabInstance나 CreationSnapshot이 없으면 ErrNotFound다.
	// 저장된 값을 해석하지 않고 그대로 반환하며(빈 vms, 빈 key, 중복 포함) 검증은 Application이 한다.
	// 필요한 field의 JSON 타입이 맞지 않아 projection으로 읽을 수 없으면 ErrInternal이다.
	// snapshot은 생성 후 변경되지 않으므로 최신 LabSpec이 아니라 그 LabExecution 시점의 값이다.
	CreationSnapshotTargets(ctx context.Context, labInstanceID uuid.UUID) (CreationSnapshotTargets, error)
	// ProviderServers는 LabInstance의 generation에 속한 SERVER ProviderResource 중 logical_name이 일치하는 row를 모든
	// lifecycle_status와 함께 반환한다. 없으면 빈 목록이며 오류가 아니다.
	ProviderServers(ctx context.Context, labInstanceID uuid.UUID, generation int64, logicalName string) ([]ProviderServer, error)

	// CreateTerminalSession은 OPENING TerminalSession을 저장한다. 같은 attach_token_hash가 이미 있으면 ErrConflict,
	// FK(ProviderResource가 그 LabInstance의 그 generation에 속함)나 Check를 위반하면 ErrConstraintViolation이다.
	CreateTerminalSession(ctx context.Context, session NewTerminalSession) error
	// TerminalSessionByID는 lifecycle과 무관하게 TerminalSession을 반환한다. 없으면 ErrNotFound다.
	TerminalSessionByID(ctx context.Context, id uuid.UUID) (TerminalSession, error)
	// UnendedTerminalSessionsByLabInstance는 LabInstance의 ENDED가 아닌 TerminalSession을 반환한다. 없으면 빈 목록이다.
	UnendedTerminalSessionsByLabInstance(ctx context.Context, labInstanceID uuid.UUID) ([]TerminalSession, error)

	// 아래 전이는 모두 조건부 UPDATE이며 조건을 만족해 갱신했으면 true, 조건을 만족하지 않아 아무것도 바꾸지 않았으면 false다.
	// 행이 없는 경우와 조건 불일치를 구분하지 않는다. ENDED는 어떤 전이로도 되살아나지 않는다.

	// MarkTerminalSessionOpened는 OPENING → DETACHED다. PTY가 준비되어 Browser attach를 기다리는 상태이며
	// detached_at과 grace_expires_at을 기록한다.
	MarkTerminalSessionOpened(ctx context.Context, id uuid.UUID, at, graceExpiresAt time.Time) (bool, error)
	// MarkTerminalSessionAttached는 DETACHED 또는 ACTIVE → ACTIVE다. attached_at을 at으로 기록하고
	// detached_at과 grace_expires_at을 비운다.
	MarkTerminalSessionAttached(ctx context.Context, id uuid.UUID, at time.Time) (bool, error)
	// MarkTerminalSessionDetached는 ACTIVE → DETACHED다. detached_at과 grace_expires_at을 기록한다.
	MarkTerminalSessionDetached(ctx context.Context, id uuid.UUID, at, graceExpiresAt time.Time) (bool, error)
	// EndTerminalSession은 ENDED가 아닌 모든 상태 → ENDED다. ended_at과 end_reason을 기록하고 grace_expires_at을 비운다.
	// 이미 ENDED면 처음 기록한 값을 유지하고 false다.
	EndTerminalSession(ctx context.Context, id uuid.UUID, endedAt time.Time, reason string) (bool, error)
}

// LiveSessionRepository는 LiveSession 생성·조회·종료가 사용하는 query와 조건부 UPDATE다.
// Terminal/Live OUTPUT, transcript, queue 내용을 저장하는 method는 없다.
type LiveSessionRepository interface {
	// LiveSessionByID는 ID로 LiveSession을 반환한다. 없으면 ErrNotFound다.
	LiveSessionByID(ctx context.Context, id uuid.UUID) (LiveSession, error)
	// ActiveLiveSessionByClass는 Class의 active(ended_at IS NULL) LiveSession을 반환한다. 없으면 ErrNotFound다.
	ActiveLiveSessionByClass(ctx context.Context, classID uuid.UUID) (LiveSession, error)
	// CreateLiveSession은 LiveSession을 저장한다. Class에 이미 active LiveSession이 있거나
	// 같은 ID가 이미 있으면 ErrConflict, FK 위반은 ErrConstraintViolation이다.
	CreateLiveSession(ctx context.Context, session NewLiveSession) error
	// EndLiveSession은 active LiveSession을 ended_at과 end_reason으로 종료한다.
	// 조건을 만족해 종료했으면 true, 이미 종료되었거나 없으면 false다.
	EndLiveSession(ctx context.Context, id uuid.UUID, endedAt time.Time, reason string) (bool, error)
	// EndActiveLiveSessionBySourceTerminal은 sourceTerminalSessionID를 source로 하는 active LiveSession을 종료한다.
	// 조건을 만족해 종료했으면 true, 없으면 false다.
	EndActiveLiveSessionBySourceTerminal(ctx context.Context, sourceTerminalSessionID uuid.UUID, endedAt time.Time, reason string) (bool, error)
}

// Repositories는 하나의 DB session에서 사용할 수 있는 Repository 모음이다.
type Repositories interface {
	IdentityRepository
	ClassRepository
	BootstrapRepository
	TerminalRepository
	LiveSessionRepository
}

// Transactor는 Application이 원자성 범위를 결정하는 경계다.
//
// fn이 nil을 반환하면 commit하고, 오류를 반환하거나 panic하면 rollback한다. fn의 오류는 그대로 반환하며
// commit 실패는 성공으로 처리하지 않는다. commit 결과를 확인할 수 없는 실패도 오류로 반환하므로
// 재시도 안전성은 호출자가 Idempotency로 보장한다.
//
// fn에 전달된 Repositories는 이 transaction 안에서만 사용한다. 중첩 transaction과 자동 retry는 제공하지 않으며
// Connector/OpenStack 같은 외부 I/O는 transaction 안에서 수행하지 않는다.
type Transactor interface {
	WithinTransaction(ctx context.Context, fn func(ctx context.Context, repos Repositories) error) error
}
