//go:build integration

package httpapi_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	migrationfiles "github.com/ktcloud4-SL/labbit-app/db/migrations"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/auth"
	"github.com/ktcloud4-SL/labbit-app/internal/server/bootstrap"
	"github.com/ktcloud4-SL/labbit-app/internal/server/httpapi"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

const (
	trustedOrigin = "https://labbit.test"

	// 테스트용 Password 원문이다. 실제 Credential이 아니다.
	password = "test-http-integration-password"
)

var baseTime = time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

// stack은 실제 PostgreSQL, 실제 Argon2id, 실제 auth.Service 위의 HTTP handler다.
type stack struct {
	handler http.Handler
	pool    *pgxpool.Pool
	clock   *clock
	logs    *bytes.Buffer
	userID  uuid.UUID
	orgID   uuid.UUID
}

func newStack(t *testing.T) *stack {
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
	s := &stack{pool: pool, clock: &clock{now: baseTime}, logs: &bytes.Buffer{}, userID: uuid.New(), orgID: uuid.New()}
	err = bootstrap.Run(t.Context(), store, bootstrap.Spec{
		Organization: bootstrap.Organization{ID: s.orgID, Name: "HTTP Org"},
		Users: []bootstrap.User{{
			ID: s.userID, Username: "alice", PasswordHash: hash, OrganizationRole: repository.OrganizationRoleAdmin,
		}},
	})
	if err != nil {
		t.Fatalf("bootstrap.Run() error = %v", err)
	}

	s.handler, err = httpapi.New(httpapi.Options{
		Auth:         auth.NewService(store, auth.Argon2id{}, s.clock.Now),
		PublicOrigin: trustedOrigin,
		Logger:       slog.New(slog.NewJSONHandler(s.logs, nil)),
	})
	if err != nil {
		t.Fatalf("httpapi.New() error = %v", err)
	}
	return s
}

func (s *stack) send(method, path, body string, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, trustedOrigin+path, strings.NewReader(body))
	for _, mod := range mods {
		mod(req)
	}
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	return rec
}

func withHeader(key, value string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set(key, value) }
}

func withCookie(value string) func(*http.Request) {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "__Host-labbit-session", Value: value}) }
}

func (s *stack) login(username, pass string, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"username": username, "password": pass})
	base := []func(*http.Request){withHeader("Origin", trustedOrigin), withHeader("Content-Type", "application/json")}
	return s.send(http.MethodPost, "/api/v1/auth/login", string(body), append(base, mods...)...)
}

func (s *stack) logout(token string) *httptest.ResponseRecorder {
	return s.send(http.MethodPost, "/api/v1/auth/logout", "", withHeader("Origin", trustedOrigin), withCookie(token))
}

func (s *stack) me(token string) *httptest.ResponseRecorder {
	return s.send(http.MethodGet, "/api/v1/me", "", withCookie(token))
}

func (s *stack) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func (s *stack) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := s.pool.Exec(t.Context(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// sessionCookie는 응답의 세션 Cookie를 반환한다.
func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == "__Host-labbit-session" {
			return cookie
		}
	}
	t.Fatalf("Set-Cookie에 세션 Cookie가 없습니다: %v", rec.Header().Values("Set-Cookie"))
	return nil
}

func issuedToken(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if rec.Code != http.StatusNoContent {
		t.Fatalf("login status = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	return sessionCookie(t, rec).Value
}

func TestLoginMeLogoutFlowAgainstPostgres(t *testing.T) {
	s := newStack(t)

	// Login: 실제 Argon2id PHC 검증 뒤 Cookie가 발급되고 DB에는 digest만 저장된다.
	rec := s.login("alice", password)
	token := issuedToken(t, rec)
	cookie := sessionCookie(t, rec)
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.Domain != "" || cookie.MaxAge != 0 || !cookie.Expires.IsZero() {
		t.Fatalf("Cookie 계약 위반: %+v", cookie)
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(raw) != 32 {
		t.Fatalf("Cookie 값은 32 bytes의 base64url이어야 합니다: len=%d err=%v", len(raw), err)
	}
	digest := sha256.Sum256(raw)
	if n := s.count(t, `SELECT count(*) FROM auth_sessions WHERE token_hash = $1 AND user_id = $2 AND revoked_at IS NULL`, digest[:], s.userID); n != 1 {
		t.Fatalf("SHA-256(raw token) digest row = %d, want 1", n)
	}

	// /me: 실제 DB의 User와 Organization을 OpenAPI Me 형태로 반환한다.
	rec = s.me(token)
	if rec.Code != http.StatusOK {
		t.Fatalf("/me = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var me struct {
		ID               string `json:"id"`
		Username         string `json:"username"`
		OrganizationRole string `json:"organizationRole"`
		Organization     struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"organization"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	if me.ID != s.userID.String() || me.Username != "alice" || me.OrganizationRole != "ADMIN" || me.Organization.ID != s.orgID.String() || me.Organization.Name != "HTTP Org" {
		t.Fatalf("/me = %+v", me)
	}
	if body := rec.Body.String(); strings.Contains(body, "argon2id") || strings.Contains(body, token) {
		t.Fatalf("/me가 민감한 값을 포함합니다: %s", body)
	}

	// Logout: 현재 Session만 revoke하고 Cookie를 제거한다.
	rec = s.logout(token)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout = %d, want 204", rec.Code)
	}
	if cleared := sessionCookie(t, rec); cleared.Value != "" || cleared.MaxAge != -1 || !cleared.Secure || cleared.Path != "/" {
		t.Fatalf("logout Cookie = %+v", cleared)
	}
	if n := s.count(t, `SELECT count(*) FROM auth_sessions WHERE token_hash = $1 AND revoked_at IS NOT NULL`, digest[:]); n != 1 {
		t.Fatalf("revoked Session row = %d, want 1", n)
	}

	// 같은 Cookie로 /me는 401이고 stale Cookie를 제거한다.
	rec = s.me(token)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("/me after logout = %d, want 401", rec.Code)
	}
	if stale := sessionCookie(t, rec); stale.Value != "" || stale.MaxAge != -1 {
		t.Fatalf("stale Cookie = %+v", stale)
	}
}

func TestLogoutKeepsOtherBrowserSessionsAndLoginReplacesPresentedOverHTTP(t *testing.T) {
	s := newStack(t)
	browserA := issuedToken(t, s.login("alice", password))
	browserB := issuedToken(t, s.login("alice", password))

	// Browser A가 기존 Cookie를 제시하고 다시 로그인하면 A만 교체된다.
	replacement := issuedToken(t, s.login("alice", password, withCookie(browserA)))
	if replacement == browserA || replacement == browserB {
		t.Fatal("기존 token 값을 재사용하면 안 됩니다")
	}
	if rec := s.me(browserA); rec.Code != http.StatusUnauthorized {
		t.Fatalf("교체된 Session /me = %d, want 401", rec.Code)
	}
	if rec := s.me(browserB); rec.Code != http.StatusOK {
		t.Fatalf("다른 Browser Session /me = %d, want 200", rec.Code)
	}

	// 교체된 Browser가 Logout해도 다른 Browser Session은 유지된다.
	if rec := s.logout(replacement); rec.Code != http.StatusNoContent {
		t.Fatalf("logout = %d, want 204", rec.Code)
	}
	if rec := s.me(browserB); rec.Code != http.StatusOK {
		t.Fatalf("Logout 뒤 다른 Browser /me = %d, want 200", rec.Code)
	}
}

func TestInvalidSessionsAreRejectedOverHTTP(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, s *stack)
	}{
		{name: "expired after 8 hours", mutate: func(_ *testing.T, s *stack) { s.clock.now = baseTime.Add(auth.SessionLifetime) }},
		{name: "revoked", mutate: func(t *testing.T, s *stack) {
			s.exec(t, `UPDATE auth_sessions SET revoked_at = $1`, baseTime.Add(time.Minute))
		}},
		{name: "disabled user", mutate: func(t *testing.T, s *stack) {
			s.exec(t, `UPDATE users SET disabled_at = $1 WHERE id = $2`, baseTime.Add(time.Minute), s.userID)
		}},
		{name: "password changed after login", mutate: func(t *testing.T, s *stack) {
			s.exec(t, `UPDATE local_accounts SET password_changed_at = $1 WHERE user_id = $2`, baseTime.Add(time.Second), s.userID)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStack(t)
			token := issuedToken(t, s.login("alice", password))
			if rec := s.me(token); rec.Code != http.StatusOK {
				t.Fatalf("변경 전 /me = %d, want 200", rec.Code)
			}

			tt.mutate(t, s)

			for name, rec := range map[string]*httptest.ResponseRecorder{"/me": s.me(token), "logout": s.logout(token)} {
				if rec.Code != http.StatusUnauthorized {
					t.Fatalf("%s = %d, want 401", name, rec.Code)
				}
				if stale := sessionCookie(t, rec); stale.Value != "" || stale.MaxAge != -1 {
					t.Fatalf("%s가 stale Cookie를 제거하지 않았습니다: %+v", name, stale)
				}
			}
		})
	}
}

func TestRejectedLoginsLeaveNoSession(t *testing.T) {
	s := newStack(t)

	wrongPassword := s.login("alice", "test-wrong-password")
	unknownUser := s.login("nobody", password)
	crossOrigin := s.login("alice", password, withHeader("Origin", "https://evil.test"))
	noSource := s.login("alice", password, func(r *http.Request) { r.Header.Del("Origin") })

	for name, rec := range map[string]*httptest.ResponseRecorder{"wrong password": wrongPassword, "unknown username": unknownUser} {
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s = %d, want 401", name, rec.Code)
		}
		if values := rec.Header().Values("Set-Cookie"); len(values) != 0 {
			t.Fatalf("%s가 Cookie를 발급했습니다: %v", name, values)
		}
	}
	if strip(wrongPassword.Body.String()) != strip(unknownUser.Body.String()) {
		t.Fatalf("응답이 계정 존재 여부를 드러냅니다:\n%s\n%s", wrongPassword.Body, unknownUser.Body)
	}
	for name, rec := range map[string]*httptest.ResponseRecorder{"origin mismatch": crossOrigin, "no origin or referer": noSource} {
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s = %d, want 403", name, rec.Code)
		}
	}
	if n := s.count(t, `SELECT count(*) FROM auth_sessions`); n != 0 {
		t.Fatalf("거절된 로그인 뒤 auth_sessions = %d, want 0", n)
	}
}

// strip은 요청마다 달라지는 requestId를 제거해 Problem Details를 비교한다.
func strip(body string) string {
	var problem map[string]any
	if err := json.Unmarshal([]byte(body), &problem); err != nil {
		return body
	}
	delete(problem, "requestId")
	out, _ := json.Marshal(problem)
	return string(out)
}

// 실제 driver/PostgreSQL 오류 문자열은 응답과 log의 credential 어디에도 나가지 않는다.
func TestDatabaseFailureDoesNotLeakDriverErrorsOrCredentials(t *testing.T) {
	s := newStack(t)
	token := issuedToken(t, s.login("alice", password))
	s.pool.Close() // 이후 모든 query는 실제 driver 오류를 반환한다.

	// 실제 driver가 만든 Cause 원문을 직접 얻어, 이 문자열이 응답과 log 어디에도 없음을 아래에서 확인한다.
	_, err := postgres.NewStore(s.pool).LocalAccountByUsername(t.Context(), "alice")
	var repoErr *repository.Error
	if !errors.As(err, &repoErr) || repoErr.Cause == nil || repoErr.Cause.Error() == "" {
		t.Fatalf("실제 driver Cause를 얻지 못했습니다: %v", err)
	}
	rawCause := repoErr.Cause.Error()

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"login":  s.login("alice", password),
		"me":     s.me(token),
		"logout": s.logout(token),
	} {
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s = %d, want 500", name, rec.Code)
		}
		body := strings.ToLower(rec.Body.String())
		for _, leak := range []string{"pool", "pgx", "sqlstate", "auth_sessions", "local_accounts", "connection", "sql", "postgres", "closed"} {
			if strings.Contains(body, leak) {
				t.Fatalf("%s 응답이 내부 정보(%q)를 노출합니다: %s", name, leak, rec.Body.String())
			}
		}
		var problem map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || problem["code"] != "internal_error" || problem["status"] != float64(500) {
			t.Fatalf("%s Problem Details = %v (%v)", name, problem, err)
		}
		if values := rec.Header().Values("Set-Cookie"); len(values) != 0 {
			t.Fatalf("%s가 저장소 장애에서 Cookie를 변경했습니다: %v", name, values)
		}
	}

	logs := s.logs.String()
	if !strings.Contains(logs, "HTTP 요청 처리 실패") || !strings.Contains(logs, `"error_kind":"internal"`) {
		t.Fatalf("저장소 장애의 분류 정보가 log에 남아야 합니다: %q", logs)
	}
	for _, secret := range []string{password, token, rawCause} {
		if strings.Contains(logs, secret) {
			t.Fatalf("log가 민감한 값을 포함합니다: %s", logs)
		}
	}
}
