// Package auth는 Local Account 로그인, Browser Session 발급·인증·종료 use case다.
//
// 구체 기준은 docs/backend/auth-session.md다. Session 유효성, 계정 disabled 여부, username 존재 여부를
// 숨기는 규칙은 Repository가 아니라 이 package가 판단한다. HTTP status, Cookie, Problem Details는 알지 못한다.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

const (
	// SessionLifetime은 v0.1 server-side absolute lifetime이다. 활동으로 연장하지 않는다.
	SessionLifetime = 8 * time.Hour

	// sessionTokenBytes는 CSPRNG로 생성하는 raw Session token의 길이다.
	sessionTokenBytes = 32
)

var (
	// ErrInvalidCredentials는 잘못된 username/password를 나타낸다. 존재하지 않는 username,
	// Password 불일치, disabled 계정을 의도적으로 구분하지 않는다.
	ErrInvalidCredentials = errors.New("auth: 잘못된 credential")
	// ErrUnauthenticated는 요청이 유효한 Session을 제시하지 않았음을 나타낸다.
	ErrUnauthenticated = errors.New("auth: 인증되지 않음")
)

const redacted = "[REDACTED]"

// SessionToken은 Browser Cookie로 전달하는 opaque token이다. Cookie 외에는 저장·log·trace 대상이 아니므로
// 실수로 출력되지 않도록 값을 가린다. Cookie를 만드는 경계에서만 string(token)으로 변환한다.
type SessionToken string

func (SessionToken) String() string               { return redacted }
func (SessionToken) GoString() string             { return redacted }
func (SessionToken) LogValue() slog.Value         { return slog.StringValue(redacted) }
func (SessionToken) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// Store는 Service가 사용하는 persistence 경계다. postgres.Store가 구현한다.
type Store interface {
	repository.IdentityRepository
	repository.Transactor
}

// Service는 인증 use case다.
type Service struct {
	store    Store
	verifier PasswordVerifier
	now      func() time.Time
}

// NewService의 now가 nil이면 time.Now를 사용한다.
func NewService(store Store, verifier PasswordVerifier, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, verifier: verifier, now: now}
}

// LoginInput의 PresentedToken은 요청이 함께 보낸 기존 Session Cookie 값이다. 없으면 빈 값이다.
type LoginInput struct {
	Username       string
	Password       string
	PresentedToken SessionToken
}

// Principal은 인증에 성공한 현재 사용자와 그 Session이다. 인증 성공은 resource authorization이 아니다.
type Principal struct {
	SessionID uuid.UUID
	User      repository.User
}

// Login은 Password를 검증하고 새 Session을 발급한다.
//
// username이 없어도 dummy Argon2id PHC로 검증을 정확히 한 번 수행하므로 존재 여부가 hash 수행 유무로 드러나지 않는다.
// 요청이 기존 Session을 제시했다면 그 Session만 revoke하고, 다른 Browser의 Session은 건드리지 않는다.
func (s *Service) Login(ctx context.Context, in LoginInput) (SessionToken, error) {
	var (
		account repository.LocalAccount
		found   bool
	)
	// PostgreSQL text는 NUL을 저장할 수 없으므로 이런 username은 존재할 수 없다.
	// 인증 전 입력이 저장소 오류가 되지 않도록 없는 username과 똑같이 dummy 검증 경로로 보낸다.
	if !strings.ContainsRune(in.Username, 0) {
		var err error
		account, err = s.store.LocalAccountByUsername(ctx, in.Username)
		switch {
		case err == nil:
			found = true
		case !errors.Is(err, repository.ErrNotFound):
			return "", fmt.Errorf("auth: 로그인 계정 조회: %w", err)
		}
	}

	hash := dummyPasswordHash
	if found {
		hash = account.PasswordHash
	}
	matched, err := s.verifier.Verify(hash, in.Password)
	if err != nil {
		return "", fmt.Errorf("auth: password 검증: %w", err)
	}
	if !found || !matched || account.User.DisabledAt != nil {
		return "", ErrInvalidCredentials
	}

	token, digest := newSessionToken()
	now := s.now()
	session := repository.AuthSession{
		ID:        uuid.New(),
		UserID:    account.User.ID,
		TokenHash: digest[:],
		CreatedAt: now,
		ExpiresAt: now.Add(SessionLifetime),
	}

	// 기존 Session revoke와 새 Session 저장은 함께 성공하거나 함께 실패해야 한다.
	err = s.store.WithinTransaction(ctx, func(ctx context.Context, repos repository.Repositories) error {
		if presented, ok := tokenDigest(in.PresentedToken); ok {
			old, err := repos.AuthSessionByTokenHash(ctx, presented[:])
			switch {
			case err == nil:
				if err := repos.RevokeAuthSession(ctx, old.Session.ID, now); err != nil {
					return err
				}
			case !errors.Is(err, repository.ErrNotFound):
				return err
			}
		}
		return repos.CreateAuthSession(ctx, session)
	})
	if err != nil {
		return "", fmt.Errorf("auth: Session 발급: %w", err)
	}
	return token, nil
}

// Authenticate는 Browser가 제시한 token의 Session이 지금 유효한지 판정한다.
// 유효하지 않은 모든 이유는 ErrUnauthenticated이며 호출자에게 구분해서 전달하지 않는다.
func (s *Service) Authenticate(ctx context.Context, token SessionToken) (Principal, error) {
	digest, ok := tokenDigest(token)
	if !ok {
		return Principal{}, ErrUnauthenticated
	}
	found, err := s.store.AuthSessionByTokenHash(ctx, digest[:])
	if errors.Is(err, repository.ErrNotFound) {
		return Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, fmt.Errorf("auth: Session 조회: %w", err)
	}
	if !sessionUsable(found, s.now()) {
		return Principal{}, ErrUnauthenticated
	}
	return Principal{SessionID: found.Session.ID, User: found.User}, nil
}

// Logout은 현재 Session만 revoke한다. 이미 revoke된 Session은 처음 기록한 시각을 유지하고 성공한다.
func (s *Service) Logout(ctx context.Context, sessionID uuid.UUID) error {
	err := s.store.RevokeAuthSession(ctx, sessionID, s.now())
	if errors.Is(err, repository.ErrNotFound) {
		return ErrUnauthenticated
	}
	if err != nil {
		return fmt.Errorf("auth: Session revoke: %w", err)
	}
	return nil
}

// sessionUsable은 docs/backend/auth-session.md의 유효 Session 조건 중 token hash 일치를 제외한 나머지다.
func sessionUsable(found repository.AuthSessionWithUser, now time.Time) bool {
	session, user := found.Session, found.User
	switch {
	case session.RevokedAt != nil:
		return false
	case !session.ExpiresAt.After(now):
		return false
	case user.DisabledAt != nil:
		return false
	case user.PasswordChangedAt != nil && session.CreatedAt.Before(*user.PasswordChangedAt):
		return false
	}
	return true
}

// newSessionToken은 CSPRNG raw token 32 bytes를 만들고 Browser용 encoding과 저장용 digest를 함께 반환한다.
func newSessionToken() (SessionToken, [sha256.Size]byte) {
	var raw [sessionTokenBytes]byte
	// crypto/rand.Read는 실패하지 않고 process를 중단시키므로 반환 오류를 처리하지 않는다.
	_, _ = rand.Read(raw[:])
	return SessionToken(base64.RawURLEncoding.EncodeToString(raw[:])), sha256.Sum256(raw[:])
}

// tokenDigest는 Browser가 제시한 token을 raw 32 bytes로 되돌려 SHA-256 digest를 계산한다.
// 형식이 맞지 않거나 canonical encoding이 아니면 DB를 조회하지 않도록 false를 반환한다.
func tokenDigest(token SessionToken) ([sha256.Size]byte, bool) {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(string(token))
	if err != nil || len(raw) != sessionTokenBytes {
		return [sha256.Size]byte{}, false
	}
	return sha256.Sum256(raw), true
}
