//go:build integration

package app

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/auth"
	"github.com/ktcloud4-SL/labbit-app/internal/server/bootstrap"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// startServer가 LABBIT_PUBLIC_ORIGIN으로 주입하는 값이다. 실제 listener의 Host(127.0.0.1:<port>)와 다르다.
const publicOrigin = "https://labbit.test"

// 테스트용 Password 원문이다. 실제 Credential이 아니다.
const e2ePassword = "test-e2e-password"

// 실제 서버 process 경로(Config → Store → auth.Service → handler → listener)에서
// Login → /me → Logout → /me 401이 동작한다.
func TestServerLoginMeLogoutFlow(t *testing.T) {
	dsn := postgrestest.NewDatabase(t)
	postgrestest.Migrate(t, dsn, loadEmbeddedMigrations(t))
	userID := seedUser(t, dsn)

	admin, application := startServerWithApplication(t, "development", "api", dsn, "")
	waitForStatus(t, admin+"/readyz", http.StatusOK)

	login := func(password string, origin string) response {
		return send(t, http.MethodPost, application+"/api/v1/auth/login", loginJSON("alice", password), map[string]string{"Origin": origin})
	}

	// Cookie 없이 /me는 401이다.
	if got := send(t, http.MethodGet, application+"/api/v1/me", "", nil); got.status != http.StatusUnauthorized {
		t.Fatalf("Cookie 없는 /me = %d, want 401", got.status)
	}

	// 서버의 실제 Host origin은 trusted origin이 아니므로 올바른 credential이어도 거절한다.
	if got := login(e2ePassword, application); got.status != http.StatusForbidden {
		t.Fatalf("Host origin으로 보낸 login = %d, want 403", got.status)
	}

	// 잘못된 Password는 401이며 Cookie를 발급하지 않는다.
	if got := login("test-wrong-password", publicOrigin); got.status != http.StatusUnauthorized || len(got.header.Values("Set-Cookie")) != 0 {
		t.Fatalf("wrong password login = %d, Set-Cookie=%v, want 401 without Cookie", got.status, got.header.Values("Set-Cookie"))
	}

	// Login
	got := login(e2ePassword, publicOrigin)
	if got.status != http.StatusNoContent {
		t.Fatalf("login = %d, want 204: %s", got.status, got.body)
	}
	cookie := got.sessionCookie(t)
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.Domain != "" || cookie.MaxAge != 0 || !cookie.Expires.IsZero() {
		t.Fatalf("Cookie 계약 위반: %+v", cookie)
	}
	session := map[string]string{"Cookie": cookie.Name + "=" + cookie.Value}

	// /me
	got = send(t, http.MethodGet, application+"/api/v1/me", "", session)
	if got.status != http.StatusOK {
		t.Fatalf("/me = %d, want 200: %s", got.status, got.body)
	}
	var me struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	if err := json.Unmarshal([]byte(got.body), &me); err != nil || me.ID != userID.String() || me.Username != "alice" {
		t.Fatalf("/me = %s (%v)", got.body, err)
	}

	// Logout
	got = send(t, http.MethodPost, application+"/api/v1/auth/logout", "", map[string]string{"Origin": publicOrigin, "Cookie": session["Cookie"]})
	if got.status != http.StatusNoContent {
		t.Fatalf("logout = %d, want 204: %s", got.status, got.body)
	}
	if cleared := got.sessionCookie(t); cleared.Value != "" || cleared.MaxAge != -1 {
		t.Fatalf("logout Cookie = %+v", cleared)
	}

	// 같은 Cookie로 /me는 401이다.
	if got := send(t, http.MethodGet, application+"/api/v1/me", "", session); got.status != http.StatusUnauthorized {
		t.Fatalf("logout 뒤 /me = %d, want 401", got.status)
	}
}

// api role이 아닌 process는 Auth endpoint를 열지 않는다.
func TestNonAPIRoleDoesNotServeAuthEndpoints(t *testing.T) {
	admin, application := startServerWithApplication(t, "development", "realtime", "", "")
	waitForStatus(t, admin+"/readyz", http.StatusOK)

	got := send(t, http.MethodGet, application+"/api/v1/me", "", nil)

	if got.status != http.StatusNotFound {
		t.Fatalf("realtime role /api/v1/me = %d, want 404", got.status)
	}
}

func seedUser(t *testing.T, dsn string) uuid.UUID {
	t.Helper()
	pool, err := postgres.OpenPool(t.Context(), dsn)
	if err != nil {
		t.Fatalf("OpenPool() error = %v", err)
	}
	t.Cleanup(pool.Close)

	hash, err := auth.HashPassword(e2ePassword)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	userID := uuid.New()
	err = bootstrap.Run(t.Context(), postgres.NewStore(pool), bootstrap.Spec{
		Organization: bootstrap.Organization{ID: uuid.New(), Name: "E2E Org"},
		Users: []bootstrap.User{{
			ID: userID, Username: "alice", PasswordHash: hash, OrganizationRole: repository.OrganizationRoleMember,
		}},
	})
	if err != nil {
		t.Fatalf("bootstrap.Run() error = %v", err)
	}
	return userID
}

func loginJSON(username, password string) string {
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	return string(body)
}

type response struct {
	status int
	header http.Header
	body   string
}

func (r response) sessionCookie(t *testing.T) *http.Cookie {
	t.Helper()
	for _, cookie := range (&http.Response{Header: r.header}).Cookies() {
		if cookie.Name == "__Host-labbit-session" {
			return cookie
		}
	}
	t.Fatalf("Set-Cookie에 세션 Cookie가 없습니다: %v", r.header.Values("Set-Cookie"))
	return nil
}

// send는 실제 listener로 request를 보낸다. Cookie jar를 쓰지 않아 Cookie를 직접 다룬다.
func send(t *testing.T, method, url, body string, headers map[string]string) response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := probeClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response{status: resp.StatusCode, header: resp.Header, body: string(payload)}
}
