// Package connector는 Connector Control WSS의 인증 use case와 connection 추적 골격이다.
//
// 외부 wire 계약은 contracts/connector/가 원본이다. 이 package는 WebSocket, HTTP status, close code를
// 알지 못하고, 제시된 Credential이 어떤 Connector의 것인지 결정하는 책임만 가진다.
// Connector identity는 항상 인증된 Credential에서 결정한다. HELLO payload 같은 메시지 안의 값은 identity가 아니다.
package connector

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// ErrUnauthenticated는 제시한 Credential로 Connector를 인증할 수 없음을 나타낸다.
// 존재하지 않음, Credential revoke, Connector revoke를 호출자에게 구분해서 전달하지 않는다.
var ErrUnauthenticated = errors.New("connector: 인증되지 않음")

const redacted = "[REDACTED]"

// Credential은 Connector가 Authorization: Bearer로 제시한 opaque Credential 원문이다.
// log, 오류 문자열, JSON에 실수로 출력되지 않도록 값을 가린다. digest를 계산하는 경계에서만 string(credential)로 변환한다.
type Credential string

func (Credential) String() string               { return redacted }
func (Credential) GoString() string             { return redacted }
func (Credential) LogValue() slog.Value         { return slog.StringValue(redacted) }
func (Credential) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// CredentialDigest는 connector_credentials.credential_hash에 저장하는 값이다.
//
// Credential은 시스템이 발급하는 고엔트로피 opaque Bearer token이므로, Browser Session token과 같이
// 원문 byte의 SHA-256 digest를 그대로 조회 key로 쓴다. Password처럼 느린 KDF나 별도 Secret은 필요하지 않다.
// 이 선택은 DB 저장 형식일 뿐 외부 wire 계약이 아니다. Credential을 발급하거나 fixture를 만드는 코드도
// 같은 함수를 사용해야 한다.
func CredentialDigest(credential string) [sha256.Size]byte {
	return sha256.Sum256([]byte(credential))
}

// Principal은 인증에 성공한 Connector다. ConnectorID가 이후 모든 판단의 identity 원본이다.
type Principal struct {
	ConnectorID    uuid.UUID
	OrganizationID uuid.UUID
	CredentialID   uuid.UUID
}

// Service는 Connector Credential 인증 use case다.
type Service struct {
	repo repository.ConnectorRepository
}

func NewService(repo repository.ConnectorRepository) *Service {
	return &Service{repo: repo}
}

// Authenticate는 Credential이 지금 사용 가능한 Connector의 것인지 판정한다.
// 유효하지 않은 모든 이유는 ErrUnauthenticated다. 저장소 장애는 인증 실패가 아니므로 그대로 오류로 반환한다.
func (s *Service) Authenticate(ctx context.Context, credential Credential) (Principal, error) {
	if credential == "" {
		return Principal{}, ErrUnauthenticated
	}
	digest := CredentialDigest(string(credential))
	found, err := s.repo.ConnectorCredentialByHash(ctx, digest[:])
	if errors.Is(err, repository.ErrNotFound) {
		return Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, fmt.Errorf("connector: Credential 조회: %w", err)
	}
	if found.Credential.RevokedAt != nil || found.Connector.RevokedAt != nil {
		return Principal{}, ErrUnauthenticated
	}
	return Principal{
		ConnectorID:    found.Connector.ID,
		OrganizationID: found.Connector.OrganizationID,
		CredentialID:   found.Credential.ID,
	}, nil
}
