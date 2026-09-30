package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// 테스트용 Password 원문이다. 실제 Credential이 아니다.
const (
	correctPassword = "test-correct-password"
	wrongPassword   = "test-wrong-password"
)

var baseTime = time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)

// fakeClock은 테스트가 시각을 직접 조작하는 clock이다.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

// fakeVerifier는 Verify 호출을 기록한다. hash가 accountHash일 때만 correctPassword를 일치로 본다.
type fakeVerifier struct {
	accountHash repository.PasswordHash
	calls       []repository.PasswordHash
	err         error
}

func (v *fakeVerifier) Verify(hash repository.PasswordHash, password string) (bool, error) {
	v.calls = append(v.calls, hash)
	if v.err != nil {
		return false, v.err
	}
	return hash == v.accountHash && password == correctPassword, nil
}

// fakeStore는 Service가 사용하는 identity query와 transaction만 구현한 in-memory Store다.
type fakeStore struct {
	users    map[uuid.UUID]*repository.User
	accounts map[string]fakeAccount
	sessions map[uuid.UUID]repository.AuthSession

	lookups        int // AuthSessionByTokenHash 호출 수
	accountLookups int // LocalAccountByUsername 호출 수
	lookupErr      error
	createErr      error
}

type fakeAccount struct {
	userID uuid.UUID
	hash   repository.PasswordHash
}

var _ Store = (*fakeStore)(nil)

func (f *fakeStore) LocalAccountByUsername(_ context.Context, username string) (repository.LocalAccount, error) {
	f.accountLookups++
	if f.lookupErr != nil {
		return repository.LocalAccount{}, f.lookupErr
	}
	account, ok := f.accounts[username]
	if !ok {
		return repository.LocalAccount{}, repository.ErrNotFound
	}
	return repository.LocalAccount{User: *f.users[account.userID], PasswordHash: account.hash}, nil
}

func (f *fakeStore) UserByID(_ context.Context, id uuid.UUID) (repository.User, error) {
	user, ok := f.users[id]
	if !ok {
		return repository.User{}, repository.ErrNotFound
	}
	return *user, nil
}

func (f *fakeStore) CreateAuthSession(_ context.Context, session repository.AuthSession) error {
	if f.createErr != nil {
		return f.createErr
	}
	for _, existing := range f.sessions {
		if bytes.Equal(existing.TokenHash, session.TokenHash) {
			return repository.ErrConflict
		}
	}
	f.sessions[session.ID] = session
	return nil
}

func (f *fakeStore) AuthSessionByTokenHash(_ context.Context, tokenHash []byte) (repository.AuthSessionWithUser, error) {
	f.lookups++
	for _, session := range f.sessions {
		if bytes.Equal(session.TokenHash, tokenHash) {
			return repository.AuthSessionWithUser{Session: session, User: *f.users[session.UserID]}, nil
		}
	}
	return repository.AuthSessionWithUser{}, repository.ErrNotFound
}

// RevokeAuthSession은 실제 Repository처럼 처음 기록한 revoked_at을 유지한다.
func (f *fakeStore) RevokeAuthSession(_ context.Context, id uuid.UUID, revokedAt time.Time) error {
	session, ok := f.sessions[id]
	if !ok {
		return repository.ErrNotFound
	}
	if session.RevokedAt == nil {
		session.RevokedAt = &revokedAt
		f.sessions[id] = session
	}
	return nil
}

// WithinTransaction은 fn 오류 시 session 변경을 되돌린다. Repositories 중 사용하지 않는 method는
// nil embedded interface라 호출하면 즉시 panic해 test 설정 오류로 드러난다.
func (f *fakeStore) WithinTransaction(ctx context.Context, fn func(context.Context, repository.Repositories) error) error {
	snapshot := make(map[uuid.UUID]repository.AuthSession, len(f.sessions))
	for id, session := range f.sessions {
		snapshot[id] = session
	}
	if err := fn(ctx, txRepos{store: f}); err != nil {
		f.sessions = snapshot
		return err
	}
	return nil
}

// txRepos는 transaction callback에 전달하는 Repositories다. identity method만 fakeStore에 위임한다.
type txRepos struct {
	repository.Repositories
	store *fakeStore
}

func (r txRepos) LocalAccountByUsername(ctx context.Context, username string) (repository.LocalAccount, error) {
	return r.store.LocalAccountByUsername(ctx, username)
}

func (r txRepos) UserByID(ctx context.Context, id uuid.UUID) (repository.User, error) {
	return r.store.UserByID(ctx, id)
}

func (r txRepos) CreateAuthSession(ctx context.Context, session repository.AuthSession) error {
	return r.store.CreateAuthSession(ctx, session)
}

func (r txRepos) AuthSessionByTokenHash(ctx context.Context, tokenHash []byte) (repository.AuthSessionWithUser, error) {
	return r.store.AuthSessionByTokenHash(ctx, tokenHash)
}

func (r txRepos) RevokeAuthSession(ctx context.Context, id uuid.UUID, revokedAt time.Time) error {
	return r.store.RevokeAuthSession(ctx, id, revokedAt)
}

type fixture struct {
	svc      *Service
	store    *fakeStore
	verifier *fakeVerifier
	clock    *fakeClock
	user     *repository.User
}

const accountHash repository.PasswordHash = "$argon2id$v=19$m=19456,t=2,p=1$YWNjb3VudC1zYWx0LTE2Ynl0$YWNjb3VudC1oYXNoLWZvci10ZXN0LW9ubHk"

func newFixture(t *testing.T) *fixture {
	t.Helper()
	user := &repository.User{
		ID:               uuid.New(),
		OrganizationID:   uuid.New(),
		OrganizationName: "Test Org",
		OrganizationRole: repository.OrganizationRoleMember,
		Username:         "alice",
	}
	store := &fakeStore{
		users:    map[uuid.UUID]*repository.User{user.ID: user},
		accounts: map[string]fakeAccount{"alice": {userID: user.ID, hash: accountHash}},
		sessions: map[uuid.UUID]repository.AuthSession{},
	}
	verifier := &fakeVerifier{accountHash: accountHash}
	clock := &fakeClock{now: baseTime}
	return &fixture{
		svc:      NewService(store, verifier, clock.Now),
		store:    store,
		verifier: verifier,
		clock:    clock,
		user:     user,
	}
}

// login은 alice로 로그인하고 발급된 token을 반환한다.
func (f *fixture) login(t *testing.T, presented SessionToken) SessionToken {
	t.Helper()
	token, err := f.svc.Login(t.Context(), LoginInput{Username: "alice", Password: correctPassword, PresentedToken: presented})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	return token
}

// seedSession은 임의 시각의 Session을 저장하고 Browser가 제시할 token을 반환한다.
func (f *fixture) seedSession(created, expires time.Time) (SessionToken, uuid.UUID) {
	token, digest := newSessionToken()
	id := uuid.New()
	f.store.sessions[id] = repository.AuthSession{
		ID: id, UserID: f.user.ID, TokenHash: digest[:], CreatedAt: created, ExpiresAt: expires,
	}
	return token, id
}

func (f *fixture) sessionByToken(t *testing.T, token SessionToken) repository.AuthSession {
	t.Helper()
	digest, ok := tokenDigest(token)
	if !ok {
		t.Fatal("token is not canonical")
	}
	for _, session := range f.store.sessions {
		if bytes.Equal(session.TokenHash, digest[:]) {
			return session
		}
	}
	t.Fatal("session not found for token")
	return repository.AuthSession{}
}

func TestLoginIssuesFreshSessionWithDigestOnly(t *testing.T) {
	f := newFixture(t)

	first := f.login(t, "")
	second := f.login(t, "")

	if first == second {
		t.Fatal("각 로그인은 새 token을 발급해야 합니다")
	}
	if len(f.store.sessions) != 2 {
		t.Fatalf("sessions = %d, want 2", len(f.store.sessions))
	}

	raw, err := base64.RawURLEncoding.Strict().DecodeString(string(first))
	if err != nil || len(raw) != 32 {
		t.Fatalf("token은 base64url(padding 없음)으로 인코딩한 32 bytes여야 합니다: len=%d err=%v", len(raw), err)
	}

	stored := f.sessionByToken(t, first)
	wantDigest := sha256.Sum256(raw)
	if !bytes.Equal(stored.TokenHash, wantDigest[:]) {
		t.Fatal("저장된 token_hash가 SHA-256(raw token)과 다릅니다")
	}
	for _, session := range f.store.sessions {
		if bytes.Equal(session.TokenHash, raw) || bytes.Equal(session.TokenHash, []byte(first)) {
			t.Fatal("raw token이 저장되었습니다")
		}
	}
	if stored.UserID != f.user.ID {
		t.Fatalf("session user = %v, want %v", stored.UserID, f.user.ID)
	}
	if !stored.CreatedAt.Equal(baseTime) || stored.ExpiresAt.Sub(stored.CreatedAt) != 8*time.Hour {
		t.Fatalf("session lifetime = %v (created %v), want absolute 8h", stored.ExpiresAt.Sub(stored.CreatedAt), stored.CreatedAt)
	}
	if stored.RevokedAt != nil {
		t.Fatal("새 Session은 revoke되어 있으면 안 됩니다")
	}
}

func TestLoginIssuedTokenAuthenticates(t *testing.T) {
	f := newFixture(t)
	token := f.login(t, "")

	principal, err := f.svc.Authenticate(t.Context(), token)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if principal.User.ID != f.user.ID || principal.User.Username != "alice" || principal.SessionID != f.sessionByToken(t, token).ID {
		t.Fatalf("principal = %+v", principal)
	}
}

func TestLoginRejectsInvalidCredentialsWithoutDistinction(t *testing.T) {
	tests := []struct {
		name         string
		username     string
		password     string
		disable      bool
		wantHash     repository.PasswordHash
		wantVerifies int
	}{
		{name: "wrong password", username: "alice", password: wrongPassword, wantHash: accountHash, wantVerifies: 1},
		{name: "unknown username verifies dummy PHC", username: "nobody", password: correctPassword, wantHash: dummyPasswordHash, wantVerifies: 1},
		{name: "disabled user with correct password", username: "alice", password: correctPassword, disable: true, wantHash: accountHash, wantVerifies: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			if tt.disable {
				disabledAt := baseTime.Add(-time.Hour)
				f.user.DisabledAt = &disabledAt
			}

			token, err := f.svc.Login(t.Context(), LoginInput{Username: tt.username, Password: tt.password})

			if err != ErrInvalidCredentials {
				t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
			}
			if token != "" {
				t.Fatal("실패한 로그인은 token을 반환하면 안 됩니다")
			}
			if len(f.store.sessions) != 0 {
				t.Fatalf("sessions = %d, want 0", len(f.store.sessions))
			}
			if len(f.verifier.calls) != tt.wantVerifies || f.verifier.calls[0] != tt.wantHash {
				t.Fatalf("Argon2id verify calls = %v, want exactly one with the expected hash", f.verifier.calls)
			}
		})
	}
}

// PostgreSQL text에 저장할 수 없는 username은 존재하지 않는 username과 같은 경로다.
func TestLoginWithNULInUsernameSkipsLookupButStillVerifiesDummy(t *testing.T) {
	f := newFixture(t)

	_, err := f.svc.Login(t.Context(), LoginInput{Username: "ali\x00ce", Password: correctPassword})

	if err != ErrInvalidCredentials {
		t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
	}
	if f.store.accountLookups != 0 {
		t.Fatalf("저장할 수 없는 username으로 저장소를 %d번 조회했습니다", f.store.accountLookups)
	}
	if len(f.verifier.calls) != 1 || f.verifier.calls[0] != dummyPasswordHash {
		t.Fatalf("Argon2id verify calls = %v, want exactly one dummy PHC", f.verifier.calls)
	}
}

func TestLoginReplacesOnlyPresentedSession(t *testing.T) {
	f := newFixture(t)
	browserA := f.login(t, "")
	browserB := f.login(t, "")
	f.clock.Advance(time.Minute)

	replacement := f.login(t, browserA)

	if _, err := f.svc.Authenticate(t.Context(), browserA); err != ErrUnauthenticated {
		t.Fatalf("presented Session은 교체(revoke)되어야 합니다: %v", err)
	}
	if session := f.sessionByToken(t, browserA); session.RevokedAt == nil || !session.RevokedAt.Equal(f.clock.now) {
		t.Fatalf("presented Session revoked_at = %v, want %v", session.RevokedAt, f.clock.now)
	}
	if _, err := f.svc.Authenticate(t.Context(), browserB); err != nil {
		t.Fatalf("다른 Browser의 Session은 유지되어야 합니다: %v", err)
	}
	if _, err := f.svc.Authenticate(t.Context(), replacement); err != nil {
		t.Fatalf("새 Session은 유효해야 합니다: %v", err)
	}
	if replacement == browserA || replacement == browserB {
		t.Fatal("기존 token 값을 재사용하면 안 됩니다")
	}
}

func TestLoginWithUnusablePresentedTokenStillSucceeds(t *testing.T) {
	f := newFixture(t)
	unknown, _ := newSessionToken()

	for name, presented := range map[string]SessionToken{
		"malformed":        "not-a-token",
		"unknown":          unknown,
		"non-canonical":    SessionToken(strings.Repeat("A", 44)),
		"empty":            "",
		"padded base64url": SessionToken(base64.URLEncoding.EncodeToString(make([]byte, 32))),
	} {
		t.Run(name, func(t *testing.T) {
			before := len(f.store.sessions)
			f.login(t, presented)
			if len(f.store.sessions) != before+1 {
				t.Fatalf("sessions = %d, want %d", len(f.store.sessions), before+1)
			}
			for _, session := range f.store.sessions {
				if session.RevokedAt != nil {
					t.Fatal("제시된 Session이 없으면 아무 Session도 revoke하면 안 됩니다")
				}
			}
		})
	}
}

func TestLoginReplacementIsAtomic(t *testing.T) {
	f := newFixture(t)
	presented := f.login(t, "")
	f.store.createErr = &repository.Error{Kind: repository.KindInternal, Op: "CreateAuthSession", Cause: errors.New("driver detail")}

	token, err := f.svc.Login(t.Context(), LoginInput{Username: "alice", Password: correctPassword, PresentedToken: presented})

	if err == nil || token != "" {
		t.Fatalf("Login() = (%q, %v), want failure", token, err)
	}
	if errors.Is(err, ErrInvalidCredentials) || errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("저장소 실패를 인증 실패로 오인하면 안 됩니다: %v", err)
	}
	if session := f.sessionByToken(t, presented); session.RevokedAt != nil {
		t.Fatal("새 Session 저장이 실패하면 기존 Session revoke도 되돌려야 합니다")
	}
	if len(f.store.sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(f.store.sessions))
	}
}

func TestLoginInternalFailuresAreNotCredentialFailures(t *testing.T) {
	t.Run("account lookup failure does not verify", func(t *testing.T) {
		f := newFixture(t)
		f.store.lookupErr = &repository.Error{Kind: repository.KindInternal, Op: "LocalAccountByUsername"}

		_, err := f.svc.Login(t.Context(), LoginInput{Username: "alice", Password: correctPassword})

		if err == nil || errors.Is(err, ErrInvalidCredentials) || !errors.Is(err, repository.ErrInternal) {
			t.Fatalf("Login() error = %v, want wrapped repository internal error", err)
		}
		if len(f.verifier.calls) != 0 {
			t.Fatal("계정 조회가 실패했는데 dummy 판단으로 넘어가면 안 됩니다")
		}
	})

	t.Run("unusable stored PHC", func(t *testing.T) {
		f := newFixture(t)
		f.verifier.err = ErrMalformedPasswordHash

		_, err := f.svc.Login(t.Context(), LoginInput{Username: "alice", Password: correctPassword})

		if !errors.Is(err, ErrMalformedPasswordHash) || errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("Login() error = %v", err)
		}
		if len(f.store.sessions) != 0 {
			t.Fatal("검증하지 못한 로그인이 Session을 만들면 안 됩니다")
		}
	})
}

func TestAuthenticateSessionValidity(t *testing.T) {
	created := baseTime
	expires := created.Add(SessionLifetime)

	tests := []struct {
		name    string
		now     time.Time
		mutate  func(user *repository.User, session *repository.AuthSession)
		wantErr error
	}{
		{name: "valid", now: created.Add(time.Hour)},
		{name: "last instant before expiry", now: expires.Add(-time.Nanosecond)},
		{name: "expired exactly at expires_at", now: expires, wantErr: ErrUnauthenticated},
		{name: "expired", now: expires.Add(time.Second), wantErr: ErrUnauthenticated},
		{
			name: "revoked", now: created.Add(time.Hour), wantErr: ErrUnauthenticated,
			mutate: func(_ *repository.User, session *repository.AuthSession) {
				revokedAt := created.Add(time.Minute)
				session.RevokedAt = &revokedAt
			},
		},
		{
			name: "disabled user", now: created.Add(time.Hour), wantErr: ErrUnauthenticated,
			mutate: func(user *repository.User, _ *repository.AuthSession) {
				disabledAt := created.Add(time.Minute)
				user.DisabledAt = &disabledAt
			},
		},
		{
			name: "password changed after session was created", now: created.Add(time.Hour), wantErr: ErrUnauthenticated,
			mutate: func(user *repository.User, _ *repository.AuthSession) {
				changedAt := created.Add(time.Minute)
				user.PasswordChangedAt = &changedAt
			},
		},
		{
			name: "password changed at the same instant is still valid", now: created.Add(time.Hour),
			mutate: func(user *repository.User, _ *repository.AuthSession) {
				changedAt := created
				user.PasswordChangedAt = &changedAt
			},
		},
		{
			name: "password changed before session was created", now: created.Add(time.Hour),
			mutate: func(user *repository.User, _ *repository.AuthSession) {
				changedAt := created.Add(-time.Hour)
				user.PasswordChangedAt = &changedAt
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			token, id := f.seedSession(created, expires)
			if tt.mutate != nil {
				session := f.store.sessions[id]
				tt.mutate(f.user, &session)
				f.store.sessions[id] = session
			}
			f.clock.now = tt.now

			principal, err := f.svc.Authenticate(t.Context(), token)

			if err != tt.wantErr {
				t.Fatalf("Authenticate() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && principal.User.ID != f.user.ID {
				t.Fatalf("principal user = %v", principal.User.ID)
			}
		})
	}
}

func TestAuthenticateRejectsUnknownAndMalformedTokens(t *testing.T) {
	f := newFixture(t)
	f.login(t, "")
	unknown, _ := newSessionToken()

	if _, err := f.svc.Authenticate(t.Context(), unknown); err != ErrUnauthenticated {
		t.Fatalf("unknown token error = %v", err)
	}

	lookups := f.store.lookups
	for _, token := range []SessionToken{"", "short", SessionToken(strings.Repeat("!", 43)), SessionToken(strings.Repeat("A", 44))} {
		if _, err := f.svc.Authenticate(t.Context(), token); err != ErrUnauthenticated {
			t.Fatalf("Authenticate(%d chars) error = %v", len(token), err)
		}
	}
	if f.store.lookups != lookups {
		t.Fatal("형식이 잘못된 token은 저장소를 조회하기 전에 거절해야 합니다")
	}
}

func TestAuthenticateInternalFailureIsNotUnauthenticated(t *testing.T) {
	f := newFixture(t)
	token := f.login(t, "")
	svc := NewService(&failingLookupStore{fakeStore: f.store}, f.verifier, f.clock.Now)

	_, err := svc.Authenticate(t.Context(), token)

	if err == nil || errors.Is(err, ErrUnauthenticated) || !errors.Is(err, repository.ErrInternal) {
		t.Fatalf("Authenticate() error = %v, want wrapped repository internal error", err)
	}
}

type failingLookupStore struct{ *fakeStore }

func (s *failingLookupStore) AuthSessionByTokenHash(context.Context, []byte) (repository.AuthSessionWithUser, error) {
	return repository.AuthSessionWithUser{}, &repository.Error{Kind: repository.KindInternal, Op: "AuthSessionByTokenHash"}
}

func TestLogoutRevokesOnlyCurrentSession(t *testing.T) {
	f := newFixture(t)
	browserA := f.login(t, "")
	browserB := f.login(t, "")
	sessionA := f.sessionByToken(t, browserA)

	f.clock.Advance(time.Minute)
	if err := f.svc.Logout(t.Context(), sessionA.ID); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	firstRevokedAt := f.clock.now

	if _, err := f.svc.Authenticate(t.Context(), browserA); err != ErrUnauthenticated {
		t.Fatalf("로그아웃한 Session은 인증되면 안 됩니다: %v", err)
	}
	if _, err := f.svc.Authenticate(t.Context(), browserB); err != nil {
		t.Fatalf("다른 Browser Session은 유지되어야 합니다: %v", err)
	}

	// 중복 Logout은 성공하며 처음 기록한 revoked_at을 유지한다.
	f.clock.Advance(time.Minute)
	if err := f.svc.Logout(t.Context(), sessionA.ID); err != nil {
		t.Fatalf("중복 Logout error = %v", err)
	}
	if revokedAt := f.sessionByToken(t, browserA).RevokedAt; revokedAt == nil || !revokedAt.Equal(firstRevokedAt) {
		t.Fatalf("revoked_at = %v, want first revoke time %v", revokedAt, firstRevokedAt)
	}
}

func TestLogoutUnknownSessionIsUnauthenticated(t *testing.T) {
	f := newFixture(t)

	if err := f.svc.Logout(t.Context(), uuid.New()); err != ErrUnauthenticated {
		t.Fatalf("Logout() error = %v, want ErrUnauthenticated", err)
	}
}

func TestSessionTokenIsRedactedWhenFormatted(t *testing.T) {
	f := newFixture(t)
	token := f.login(t, "")
	raw := string(token)

	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("login", "token", token)

	for name, got := range map[string]string{
		"%v":   fmt.Sprintf("%v", token),
		"%+v":  fmt.Sprintf("%+v", token),
		"%#v":  fmt.Sprintf("%#v", token),
		"%s":   fmt.Sprintf("%s", token),
		"slog": logs.String(),
	} {
		if strings.Contains(got, raw) {
			t.Errorf("%s output exposes the raw Session token: %q", name, got)
		}
	}
}
