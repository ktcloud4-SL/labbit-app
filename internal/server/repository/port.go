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
	// 그렇지 않으면 아무것도 바꾸지 않고 false다. 판정과 갱신은 하나의 statement라서 revoke와 경쟁해도
	// revoke가 반영된 뒤에는 갱신되지 않는다. seenAt은 Application이 정한 서버 수신 시각이다.
	RecordConnectorHeartbeat(ctx context.Context, connectorID, credentialID uuid.UUID, seenAt time.Time) (bool, error)
}

// Repositories는 하나의 DB session에서 사용할 수 있는 Repository 모음이다.
type Repositories interface {
	IdentityRepository
	ClassRepository
	BootstrapRepository
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
