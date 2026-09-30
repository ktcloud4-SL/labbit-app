//go:build integration

package auth_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	migrationfiles "github.com/ktcloud4-SL/labbit-app/db/migrations"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/auth"
	"github.com/ktcloud4-SL/labbit-app/internal/server/bootstrap"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// 테스트용 Password 원문이다. 실제 Credential이 아니다.
const (
	password      = "test-integration-password"
	wrongPassword = "test-integration-wrong"
)

var baseTime = time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// env는 migration이 적용된 폐기 가능한 PostgreSQL과, 실제 Argon2id PHC로 사전 생성한 사용자 위의 Service다.
type env struct {
	pool   *pgxpool.Pool
	store  *postgres.Store
	clock  *clock
	svc    *auth.Service
	userID uuid.UUID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dsn := postgrestest.NewDatabase(t)
	migrations, err := postgres.LoadMigrations(migrationfiles.Files)
	if err != nil {
		t.Fatalf("LoadMigrations() error = %v", err)
	}
	postgrestest.Migrate(t, dsn, migrations)

	pool, err := postgres.OpenPool(t.Context(), dsn)
	if err != nil {
		t.Fatalf("OpenPool() error = %v", err)
	}
	t.Cleanup(pool.Close)
	store := postgres.NewStore(pool)

	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	userID := uuid.New()
	err = bootstrap.Run(t.Context(), store, bootstrap.Spec{
		Organization: bootstrap.Organization{ID: uuid.New(), Name: "Integration Org"},
		Users: []bootstrap.User{{
			ID: userID, Username: "alice", PasswordHash: hash, OrganizationRole: repository.OrganizationRoleMember,
		}},
	})
	if err != nil {
		t.Fatalf("bootstrap.Run() error = %v", err)
	}

	clock := &clock{now: baseTime}
	return &env{
		pool: pool, store: store, clock: clock, userID: userID,
		svc: auth.NewService(store, auth.Argon2id{}, clock.Now),
	}
}

func (e *env) login(t *testing.T, presented auth.SessionToken) auth.SessionToken {
	t.Helper()
	token, err := e.svc.Login(t.Context(), auth.LoginInput{Username: "alice", Password: password, PresentedToken: presented})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	return token
}

func (e *env) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func (e *env) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := e.pool.Exec(t.Context(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// revokedAt은 token의 Session row revoked_at을 읽는다. 없으면 nil이다.
func (e *env) revokedAt(t *testing.T, token auth.SessionToken) *time.Time {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(string(token))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	var revoked *time.Time
	if err := e.pool.QueryRow(t.Context(), `SELECT revoked_at FROM auth_sessions WHERE token_hash = $1`, digest[:]).Scan(&revoked); err != nil {
		t.Fatalf("Session row를 찾지 못했습니다: %v", err)
	}
	return revoked
}

func TestLoginVerifiesRealArgon2idPHCAndStoresOnlyTheTokenDigest(t *testing.T) {
	e := newEnv(t)

	// 실제 PostgreSQL의 실제 PHC로 검증한다. 틀린 Password와 없는 username은 같은 오류다.
	if _, err := e.svc.Login(t.Context(), auth.LoginInput{Username: "alice", Password: wrongPassword}); err != auth.ErrInvalidCredentials {
		t.Fatalf("wrong password error = %v, want ErrInvalidCredentials", err)
	}
	if _, err := e.svc.Login(t.Context(), auth.LoginInput{Username: "nobody", Password: password}); err != auth.ErrInvalidCredentials {
		t.Fatalf("unknown username error = %v, want ErrInvalidCredentials", err)
	}
	if n := e.count(t, `SELECT count(*) FROM auth_sessions`); n != 0 {
		t.Fatalf("실패한 로그인 뒤 auth_sessions = %d, want 0", n)
	}

	token := e.login(t, "")

	raw, err := base64.RawURLEncoding.Strict().DecodeString(string(token))
	if err != nil || len(raw) != 32 {
		t.Fatalf("token은 32 bytes의 base64url(padding 없음)이어야 합니다: len=%d err=%v", len(raw), err)
	}
	digest := sha256.Sum256(raw)

	var (
		storedHash        []byte
		userID            uuid.UUID
		createdAt         time.Time
		expiresAt         time.Time
		lifetimeIsEightHr bool
		revokedAt         *time.Time
	)
	err = e.pool.QueryRow(t.Context(),
		`SELECT token_hash, user_id, created_at, expires_at, expires_at - created_at = interval '8 hours', revoked_at FROM auth_sessions`,
	).Scan(&storedHash, &userID, &createdAt, &expiresAt, &lifetimeIsEightHr, &revokedAt)
	if err != nil {
		t.Fatalf("Session row 조회: %v", err)
	}
	if !bytes.Equal(storedHash, digest[:]) {
		t.Fatal("DB token_hash가 SHA-256(raw token)과 다릅니다")
	}
	if userID != e.userID || !createdAt.Equal(baseTime) || !lifetimeIsEightHr || revokedAt != nil {
		t.Fatalf("session = user %v created %v expires %v (8h=%v) revoked %v", userID, createdAt, expiresAt, lifetimeIsEightHr, revokedAt)
	}

	// raw token이 어떤 컬럼에도, 어떤 형태로도 저장되지 않는다.
	if n := e.count(t, `SELECT count(*) FROM auth_sessions WHERE token_hash IN ($1, $2)`, raw, []byte(token)); n != 0 {
		t.Fatalf("raw token 또는 그 encoding이 token_hash로 저장되었습니다")
	}
	if n := e.count(t, `SELECT count(*) FROM auth_sessions s WHERE s::text LIKE '%' || $1 || '%'`, string(token)); n != 0 {
		t.Fatal("Session row 어딘가에 encoded token 문자열이 저장되었습니다")
	}
}

// PostgreSQL text는 NUL을 저장하거나 비교할 수 없다. 이런 username도 존재하지 않는 username과 같은
// 401 의미여야 하며, 인증 전 요청이 저장소 오류(500)를 일으키면 안 된다.
func TestLoginWithUsernameThatPostgresCannotStoreIsInvalidCredentials(t *testing.T) {
	e := newEnv(t)

	_, err := e.svc.Login(t.Context(), auth.LoginInput{Username: "ali\x00ce", Password: password})

	if err != auth.ErrInvalidCredentials {
		t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
	}
}

func TestAuthenticateReturnsUserFromRealRepository(t *testing.T) {
	e := newEnv(t)
	token := e.login(t, "")

	principal, err := e.svc.Authenticate(t.Context(), token)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	user := principal.User
	if user.ID != e.userID || user.Username != "alice" || user.OrganizationName != "Integration Org" || user.OrganizationRole != repository.OrganizationRoleMember {
		t.Fatalf("principal user = %+v", user)
	}
	if principal.SessionID == uuid.Nil {
		t.Fatal("principal.SessionID가 비어 있습니다")
	}

	unknown := auth.SessionToken(base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	if _, err := e.svc.Authenticate(t.Context(), unknown); err != auth.ErrUnauthenticated {
		t.Fatalf("unknown token error = %v, want ErrUnauthenticated", err)
	}
}

func TestSessionValidityBoundariesAgainstRealRepository(t *testing.T) {
	t.Run("absolute 8 hour expiry", func(t *testing.T) {
		e := newEnv(t)
		token := e.login(t, "")

		e.clock.Advance(auth.SessionLifetime - time.Microsecond)
		if _, err := e.svc.Authenticate(t.Context(), token); err != nil {
			t.Fatalf("만료 직전 Authenticate() error = %v", err)
		}
		e.clock.Advance(time.Microsecond)
		if _, err := e.svc.Authenticate(t.Context(), token); err != auth.ErrUnauthenticated {
			t.Fatalf("expires_at 시점 Authenticate() error = %v, want ErrUnauthenticated", err)
		}
	})

	t.Run("activity does not extend the session", func(t *testing.T) {
		e := newEnv(t)
		token := e.login(t, "")

		e.clock.Advance(7 * time.Hour)
		if _, err := e.svc.Authenticate(t.Context(), token); err != nil {
			t.Fatalf("Authenticate() error = %v", err)
		}
		e.clock.Advance(time.Hour)
		if _, err := e.svc.Authenticate(t.Context(), token); err != auth.ErrUnauthenticated {
			t.Fatalf("최초 발급 8시간 뒤에는 활동과 무관하게 만료되어야 합니다: %v", err)
		}
	})

	t.Run("revoked", func(t *testing.T) {
		e := newEnv(t)
		token := e.login(t, "")
		e.exec(t, `UPDATE auth_sessions SET revoked_at = $1`, baseTime.Add(time.Minute))

		if _, err := e.svc.Authenticate(t.Context(), token); err != auth.ErrUnauthenticated {
			t.Fatalf("Authenticate() error = %v, want ErrUnauthenticated", err)
		}
	})

	t.Run("disabled user", func(t *testing.T) {
		e := newEnv(t)
		token := e.login(t, "")
		e.exec(t, `UPDATE users SET disabled_at = $1 WHERE id = $2`, baseTime.Add(time.Minute), e.userID)

		if _, err := e.svc.Authenticate(t.Context(), token); err != auth.ErrUnauthenticated {
			t.Fatalf("기존 Session Authenticate() error = %v, want ErrUnauthenticated", err)
		}
		if _, err := e.svc.Login(t.Context(), auth.LoginInput{Username: "alice", Password: password}); err != auth.ErrInvalidCredentials {
			t.Fatalf("disabled 계정 Login() error = %v, want ErrInvalidCredentials", err)
		}
	})

	t.Run("password changed after the session was created", func(t *testing.T) {
		e := newEnv(t)
		token := e.login(t, "")
		e.exec(t, `UPDATE local_accounts SET password_changed_at = $1 WHERE user_id = $2`, baseTime.Add(time.Second), e.userID)

		if _, err := e.svc.Authenticate(t.Context(), token); err != auth.ErrUnauthenticated {
			t.Fatalf("Authenticate() error = %v, want ErrUnauthenticated", err)
		}
	})

	t.Run("password changed at the same instant or earlier keeps the session", func(t *testing.T) {
		e := newEnv(t)
		token := e.login(t, "")

		for _, changedAt := range []time.Time{baseTime, baseTime.Add(-time.Hour)} {
			e.exec(t, `UPDATE local_accounts SET password_changed_at = $1 WHERE user_id = $2`, changedAt, e.userID)
			if _, err := e.svc.Authenticate(t.Context(), token); err != nil {
				t.Fatalf("password_changed_at = %v일 때 Authenticate() error = %v", changedAt, err)
			}
		}
	})

	t.Run("session created after a password change is valid", func(t *testing.T) {
		e := newEnv(t)
		e.exec(t, `UPDATE local_accounts SET password_changed_at = $1 WHERE user_id = $2`, baseTime.Add(-time.Minute), e.userID)

		token := e.login(t, "")

		if _, err := e.svc.Authenticate(t.Context(), token); err != nil {
			t.Fatalf("Authenticate() error = %v", err)
		}
	})
}

func TestLogoutRevokesOnlyTheCurrentSessionInPostgres(t *testing.T) {
	e := newEnv(t)
	browserA := e.login(t, "")
	browserB := e.login(t, "")
	principalA, err := e.svc.Authenticate(t.Context(), browserA)
	if err != nil {
		t.Fatal(err)
	}

	e.clock.Advance(time.Minute)
	if err := e.svc.Logout(t.Context(), principalA.SessionID); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	firstRevoke := e.clock.Now()

	if revoked := e.revokedAt(t, browserA); revoked == nil || !revoked.Equal(firstRevoke) {
		t.Fatalf("Browser A revoked_at = %v, want %v", revoked, firstRevoke)
	}
	if revoked := e.revokedAt(t, browserB); revoked != nil {
		t.Fatalf("Browser B가 revoke되었습니다: %v", revoked)
	}
	if _, err := e.svc.Authenticate(t.Context(), browserA); err != auth.ErrUnauthenticated {
		t.Fatalf("Browser A Authenticate() error = %v, want ErrUnauthenticated", err)
	}
	if _, err := e.svc.Authenticate(t.Context(), browserB); err != nil {
		t.Fatalf("Browser B Authenticate() error = %v", err)
	}

	// 중복 Logout은 성공하며 처음 기록한 revoked_at을 유지한다.
	e.clock.Advance(time.Minute)
	if err := e.svc.Logout(t.Context(), principalA.SessionID); err != nil {
		t.Fatalf("중복 Logout() error = %v", err)
	}
	if revoked := e.revokedAt(t, browserA); revoked == nil || !revoked.Equal(firstRevoke) {
		t.Fatalf("중복 Logout 뒤 revoked_at = %v, want first %v", revoked, firstRevoke)
	}

	if err := e.svc.Logout(t.Context(), uuid.New()); err != auth.ErrUnauthenticated {
		t.Fatalf("없는 Session Logout() error = %v, want ErrUnauthenticated", err)
	}
}

func TestLoginReplacesOnlyThePresentedSessionInPostgres(t *testing.T) {
	e := newEnv(t)
	browserA := e.login(t, "")
	browserB := e.login(t, "")
	e.clock.Advance(time.Minute)

	replacement := e.login(t, browserA)

	if revoked := e.revokedAt(t, browserA); revoked == nil || !revoked.Equal(e.clock.Now()) {
		t.Fatalf("presented Session revoked_at = %v, want %v", revoked, e.clock.Now())
	}
	if revoked := e.revokedAt(t, browserB); revoked != nil {
		t.Fatalf("다른 Browser Session이 revoke되었습니다: %v", revoked)
	}
	if revoked := e.revokedAt(t, replacement); revoked != nil {
		t.Fatalf("새 Session이 revoke되어 있습니다: %v", revoked)
	}
	if replacement == browserA || replacement == browserB {
		t.Fatal("기존 token 값을 재사용하면 안 됩니다")
	}
	if n := e.count(t, `SELECT count(*) FROM auth_sessions`); n != 3 {
		t.Fatalf("auth_sessions = %d, want 3", n)
	}
	if n := e.count(t, `SELECT count(*) FROM auth_sessions WHERE revoked_at IS NULL`); n != 2 {
		t.Fatalf("활성 Session = %d, want 2", n)
	}
}

// 기존 Session revoke와 새 Session 저장은 하나의 transaction이다. 새 Session 저장이 실패하면
// 이미 실행한 revoke도 실제 PostgreSQL에서 rollback되어야 한다.
func TestLoginReplacementRollsBackWhenNewSessionCannotBeStored(t *testing.T) {
	e := newEnv(t)
	presented := e.login(t, "")
	failing := auth.NewService(failCreateStore{Store: e.store}, auth.Argon2id{}, e.clock.Now)

	token, err := failing.Login(t.Context(), auth.LoginInput{Username: "alice", Password: password, PresentedToken: presented})

	if err == nil || token != "" {
		t.Fatalf("Login() = (%q, %v), want failure", token, err)
	}
	if errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("저장소 실패를 credential 실패로 오인했습니다: %v", err)
	}
	if revoked := e.revokedAt(t, presented); revoked != nil {
		t.Fatalf("새 Session 저장 실패 뒤 presented Session이 revoke된 채 남았습니다: %v", revoked)
	}
	if n := e.count(t, `SELECT count(*) FROM auth_sessions`); n != 1 {
		t.Fatalf("auth_sessions = %d, want 1", n)
	}
	if _, err := e.svc.Authenticate(t.Context(), presented); err != nil {
		t.Fatalf("presented Session은 계속 유효해야 합니다: %v", err)
	}
}

// failCreateStore는 transaction 안의 CreateAuthSession만 실패시킨다. 그 전에 실행한 revoke는 실제 DB에 남는다.
type failCreateStore struct{ *postgres.Store }

func (s failCreateStore) WithinTransaction(ctx context.Context, fn func(context.Context, repository.Repositories) error) error {
	return s.Store.WithinTransaction(ctx, func(ctx context.Context, repos repository.Repositories) error {
		return fn(ctx, failingCreate{Repositories: repos})
	})
}

type failingCreate struct{ repository.Repositories }

func (failingCreate) CreateAuthSession(context.Context, repository.AuthSession) error {
	return &repository.Error{Kind: repository.KindInternal, Op: "CreateAuthSession", Cause: errors.New("injected failure")}
}

func TestConcurrentLoginsPresentingTheSameSession(t *testing.T) {
	e := newEnv(t)
	presented := e.login(t, "")

	const logins = 5
	tokens := make([]auth.SessionToken, logins)
	errs := make([]error, logins)
	var wg sync.WaitGroup
	for i := range logins {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tokens[i], errs[i] = e.svc.Login(t.Context(), auth.LoginInput{Username: "alice", Password: password, PresentedToken: presented})
		}()
	}
	wg.Wait()

	seen := map[auth.SessionToken]bool{}
	for i := range logins {
		if errs[i] != nil {
			t.Fatalf("login %d error = %v", i, errs[i])
		}
		if seen[tokens[i]] {
			t.Fatal("동시 로그인이 같은 token을 발급했습니다")
		}
		seen[tokens[i]] = true
		if _, err := e.svc.Authenticate(t.Context(), tokens[i]); err != nil {
			t.Fatalf("login %d의 Session이 유효하지 않습니다: %v", i, err)
		}
	}
	if revoked := e.revokedAt(t, presented); revoked == nil {
		t.Fatal("presented Session이 revoke되지 않았습니다")
	}
	if n := e.count(t, `SELECT count(*) FROM auth_sessions WHERE revoked_at IS NULL`); n != logins {
		t.Fatalf("활성 Session = %d, want %d", n, logins)
	}
}
