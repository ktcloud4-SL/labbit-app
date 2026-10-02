package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/auth"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

const (
	trustedOrigin = "https://labbit.test"

	// 테스트용 Password 원문이다. 실제 Credential이 아니다.
	testPassword = "test-login-password"
)

// fakeAuth는 상태를 가진 Authenticator다. Application 판정 로직이 아니라 handler가 넘기는 값과
// 결과를 HTTP로 옮기는 방식만 검증하기 위한 것이다.
type fakeAuth struct {
	principal auth.Principal
	sessions  map[auth.SessionToken]auth.Principal

	logins        []auth.LoginInput
	authenticates int
	logouts       []uuid.UUID
	seq           int

	loginErr, authErr, logoutErr error
}

func newFakeAuth() *fakeAuth {
	return &fakeAuth{
		principal: auth.Principal{
			SessionID: uuid.New(),
			User: repository.User{
				ID:               uuid.New(),
				OrganizationID:   uuid.New(),
				OrganizationName: "Test Org",
				OrganizationRole: repository.OrganizationRoleAdmin,
				Username:         "alice",
			},
		},
		sessions: map[auth.SessionToken]auth.Principal{},
	}
}

func (f *fakeAuth) Login(_ context.Context, in auth.LoginInput) (auth.SessionToken, error) {
	f.logins = append(f.logins, in)
	if f.loginErr != nil {
		return "", f.loginErr
	}
	if in.Username != "alice" || in.Password != testPassword {
		return "", auth.ErrInvalidCredentials
	}
	delete(f.sessions, in.PresentedToken)
	f.seq++
	token := auth.SessionToken(fmt.Sprintf("issued-token-%d", f.seq))
	principal := f.principal
	principal.SessionID = uuid.New()
	f.sessions[token] = principal
	return token, nil
}

func (f *fakeAuth) Authenticate(_ context.Context, token auth.SessionToken) (auth.Principal, error) {
	f.authenticates++
	if f.authErr != nil {
		return auth.Principal{}, f.authErr
	}
	principal, ok := f.sessions[token]
	if !ok {
		return auth.Principal{}, auth.ErrUnauthenticated
	}
	return principal, nil
}

func (f *fakeAuth) Logout(_ context.Context, sessionID uuid.UUID) error {
	f.logouts = append(f.logouts, sessionID)
	if f.logoutErr != nil {
		return f.logoutErr
	}
	for token, principal := range f.sessions {
		if principal.SessionID == sessionID {
			delete(f.sessions, token)
			return nil
		}
	}
	return auth.ErrUnauthenticated
}

type harness struct {
	handler http.Handler
	auth    *fakeAuth
	classes *fakeClasses
	logs    *bytes.Buffer
}

func newHarness(t *testing.T, mods ...func(*Options)) *harness {
	t.Helper()
	fake := newFakeAuth()
	classes := &fakeClasses{}
	logs := &bytes.Buffer{}
	opts := Options{
		Auth:         fake,
		Classes:      classes,
		PublicOrigin: trustedOrigin,
		Logger:       slog.New(slog.NewJSONHandler(logs, nil)),
	}
	for _, mod := range mods {
		mod(&opts)
	}
	handler, err := New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return &harness{handler: handler, auth: fake, classes: classes, logs: logs}
}

// send는 request를 만들고 mods로 조정한 뒤 handler를 실행한다. 기본 Host는 trusted origin의 host다.
func (h *harness) send(method, path, body string, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, trustedOrigin+path, strings.NewReader(body))
	for _, mod := range mods {
		mod(req)
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec
}

func withHeader(key, value string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set(key, value) }
}

func withCookie(token auth.SessionToken) func(*http.Request) {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: string(token)}) }
}

func withoutSource(r *http.Request) {
	r.Header.Del("Origin")
	r.Header.Del("Referer")
}

func loginBody(username, password string) string {
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	return string(body)
}

// login은 유효한 Origin과 JSON Content-Type을 가진 login 요청을 보낸다.
func (h *harness) login(username, password string, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	base := []func(*http.Request){withHeader("Origin", trustedOrigin), withHeader("Content-Type", "application/json")}
	return h.send(http.MethodPost, "/api/v1/auth/login", loginBody(username, password), append(base, mods...)...)
}

func sessionCookieFrom(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	var found *http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			if found != nil {
				t.Fatal("Set-Cookie에 세션 Cookie가 둘 이상입니다")
			}
			found = cookie
		}
	}
	if found == nil {
		t.Fatalf("Set-Cookie에 %s가 없습니다: %v", sessionCookieName, rec.Header().Values("Set-Cookie"))
	}
	return found
}

func assertNoSetCookie(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if values := rec.Header().Values("Set-Cookie"); len(values) != 0 {
		t.Fatalf("Set-Cookie = %v, want none", values)
	}
}

func decodeProblem(t *testing.T, rec *httptest.ResponseRecorder) problemDetails {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", got)
	}
	var problem problemDetails
	decoder := json.NewDecoder(rec.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&problem); err != nil {
		t.Fatalf("Problem Details JSON: %v", err)
	}
	if problem.Status != rec.Code {
		t.Fatalf("problem.status = %d, response status = %d", problem.Status, rec.Code)
	}
	if problem.Type == "" || problem.Title == "" || problem.Code == "" {
		t.Fatalf("required Problem Details field가 비어 있습니다: %+v", problem)
	}
	if _, err := uuid.Parse(problem.RequestID); err != nil {
		t.Fatalf("requestId = %q, want generated correlation id", problem.RequestID)
	}
	return problem
}

func TestLoginMeLogoutFlow(t *testing.T) {
	h := newHarness(t)

	// Login
	rec := h.login("alice", testPassword)
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("login = %d %q, want 204 with empty body", rec.Code, rec.Body.String())
	}
	cookie := sessionCookieFrom(t, rec)
	session := auth.SessionToken(cookie.Value)

	// /me
	rec = h.send(http.MethodGet, "/api/v1/me", "", withCookie(session))
	if rec.Code != http.StatusOK {
		t.Fatalf("/me = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("/me Content-Type = %q", got)
	}
	var me map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	user := h.auth.principal.User
	want := map[string]any{
		"id":               user.ID.String(),
		"username":         "alice",
		"organization":     map[string]any{"id": user.OrganizationID.String(), "name": "Test Org"},
		"organizationRole": "ADMIN",
	}
	if fmt.Sprint(me) != fmt.Sprint(want) {
		t.Fatalf("/me body = %v, want %v", me, want)
	}

	// Logout
	rec = h.send(http.MethodPost, "/api/v1/auth/logout", "", withHeader("Origin", trustedOrigin), withCookie(session))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout = %d, want 204", rec.Code)
	}
	cleared := sessionCookieFrom(t, rec)
	if cleared.Value != "" || cleared.MaxAge != -1 {
		t.Fatalf("logout Set-Cookie = %+v, want empty value with Max-Age=0", cleared)
	}

	// 로그아웃한 Session으로 /me는 401이고 stale Cookie를 제거한다.
	rec = h.send(http.MethodGet, "/api/v1/me", "", withCookie(session))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("/me after logout = %d, want 401", rec.Code)
	}
	if problem := decodeProblem(t, rec); problem.Code != codeUnauthenticated {
		t.Fatalf("problem.code = %q", problem.Code)
	}
	if stale := sessionCookieFrom(t, rec); stale.MaxAge != -1 {
		t.Fatalf("stale Cookie가 제거되지 않았습니다: %+v", stale)
	}
}

func TestLoginIssuesContractCookie(t *testing.T) {
	h := newHarness(t)

	rec := h.login("alice", testPassword)

	raw := rec.Header().Values("Set-Cookie")
	if len(raw) != 1 {
		t.Fatalf("Set-Cookie = %v, want exactly one", raw)
	}
	cookie := sessionCookieFrom(t, rec)
	if cookie.Name != "__Host-labbit-session" || cookie.Value == "" {
		t.Fatalf("cookie = %+v", cookie)
	}
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
		t.Fatalf("cookie 속성 = %+v, want Secure; HttpOnly; SameSite=Lax; Path=/", cookie)
	}
	if cookie.Domain != "" {
		t.Fatalf("Domain = %q, __Host- Cookie는 Domain을 지정하면 안 됩니다", cookie.Domain)
	}
	if cookie.MaxAge != 0 || !cookie.Expires.IsZero() {
		t.Fatalf("Max-Age/Expires가 있으면 non-persistent Cookie가 아닙니다: %+v", cookie)
	}
	for _, attribute := range []string{"Max-Age", "Expires", "Domain"} {
		if strings.Contains(raw[0], attribute) {
			t.Fatalf("Set-Cookie %q에 %s가 있으면 안 됩니다", raw[0], attribute)
		}
	}
}

func TestClearedCookieKeepsScopeAndSetsMaxAgeZero(t *testing.T) {
	h := newHarness(t)
	token := auth.SessionToken("issued")
	h.auth.sessions[token] = h.auth.principal

	rec := h.send(http.MethodPost, "/api/v1/auth/logout", "", withHeader("Origin", trustedOrigin), withCookie(token))

	raw := rec.Header().Get("Set-Cookie")
	for _, want := range []string{sessionCookieName + "=;", "Path=/", "Max-Age=0", "HttpOnly", "Secure", "SameSite=Lax"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("clear Set-Cookie %q에 %q가 없습니다", raw, want)
		}
	}
	if strings.Contains(raw, "Domain") {
		t.Fatalf("clear Set-Cookie %q에 Domain이 있으면 안 됩니다", raw)
	}
}

func TestLoginPassesPresentedSessionToApplication(t *testing.T) {
	h := newHarness(t)
	first := sessionCookieFrom(t, h.login("alice", testPassword))

	second := sessionCookieFrom(t, h.login("alice", testPassword, withCookie(auth.SessionToken(first.Value))))

	if second.Value == first.Value {
		t.Fatal("기존 Cookie 값을 재사용하면 안 됩니다")
	}
	if got := h.auth.logins[1].PresentedToken; got != auth.SessionToken(first.Value) {
		t.Fatalf("Application에 전달한 presented token = %q, want %q", string(got), first.Value)
	}
	if h.auth.logins[0].PresentedToken != "" {
		t.Fatal("Cookie가 없는 로그인은 presented token이 비어 있어야 합니다")
	}
}

func TestLoginInvalidCredentialsIsUniform401WithoutCookie(t *testing.T) {
	h := newHarness(t)

	wrongPassword := h.login("alice", "test-wrong-password")
	unknownUser := h.login("nobody", testPassword)

	for name, rec := range map[string]*httptest.ResponseRecorder{"wrong password": wrongPassword, "unknown username": unknownUser} {
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want 401", name, rec.Code)
		}
		assertNoSetCookie(t, rec)
	}
	a, b := decodeProblem(t, wrongPassword), decodeProblem(t, unknownUser)
	a.RequestID, b.RequestID = "", ""
	if a != b {
		t.Fatalf("응답이 계정 존재 여부에 따라 달라집니다:\n%+v\n%+v", a, b)
	}
	if a.Code != codeInvalidCredentials {
		t.Fatalf("problem.code = %q", a.Code)
	}
}

func TestLoginRejectsMalformedRequests(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{name: "not json content type", contentType: "text/plain", body: loginBody("alice", testPassword)},
		{name: "form content type", contentType: "application/x-www-form-urlencoded", body: "username=alice&password=x"},
		{name: "missing content type", contentType: "", body: loginBody("alice", testPassword)},
		{name: "malformed json", contentType: "application/json", body: `{"username":`},
		{name: "empty body", contentType: "application/json", body: ""},
		{name: "not an object", contentType: "application/json", body: `"alice"`},
		{name: "unknown property", contentType: "application/json", body: `{"username":"alice","password":"x","admin":true}`},
		{name: "missing password", contentType: "application/json", body: `{"username":"alice"}`},
		{name: "missing username", contentType: "application/json", body: `{"password":"x"}`},
		{name: "empty username", contentType: "application/json", body: `{"username":"","password":"x"}`},
		{name: "empty password", contentType: "application/json", body: `{"username":"alice","password":""}`},
		{name: "wrong type", contentType: "application/json", body: `{"username":1,"password":"x"}`},
		{name: "trailing json value", contentType: "application/json", body: loginBody("alice", testPassword) + `{}`},
		{name: "oversized body", contentType: "application/json", body: `{"username":"alice","password":"` + strings.Repeat("x", maxLoginBodyBytes) + `"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)

			rec := h.send(http.MethodPost, "/api/v1/auth/login", tt.body,
				withHeader("Origin", trustedOrigin), withHeader("Content-Type", tt.contentType))

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if problem := decodeProblem(t, rec); problem.Code != codeInvalidRequest {
				t.Fatalf("problem.code = %q", problem.Code)
			}
			assertNoSetCookie(t, rec)
			if len(h.auth.logins) != 0 {
				t.Fatal("형식이 잘못된 요청이 Application까지 전달되었습니다")
			}
		})
	}
}

func TestLoginAcceptsJSONContentTypeWithCharset(t *testing.T) {
	h := newHarness(t)

	rec := h.send(http.MethodPost, "/api/v1/auth/login", loginBody("alice", testPassword),
		withHeader("Origin", trustedOrigin), withHeader("Content-Type", "application/json; charset=utf-8"))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
}

func TestUnsafeMethodsRequireMatchingSource(t *testing.T) {
	type source struct {
		name    string
		mod     func(*http.Request)
		allowed bool
	}
	sources := []source{
		{name: "origin exact match", mod: withHeader("Origin", trustedOrigin), allowed: true},
		{name: "referer fallback", mod: withHeader("Referer", trustedOrigin+"/login?next=%2F"), allowed: true},
		{name: "origin match wins over referer mismatch", mod: func(r *http.Request) {
			r.Header.Set("Origin", trustedOrigin)
			r.Header.Set("Referer", "https://evil.test/")
		}, allowed: true},
		{name: "origin mismatch", mod: withHeader("Origin", "https://evil.test")},
		{name: "origin scheme mismatch", mod: withHeader("Origin", "http://labbit.test")},
		{name: "origin port mismatch", mod: withHeader("Origin", "https://labbit.test:8443")},
		{name: "origin mismatch is not rescued by valid referer", mod: func(r *http.Request) {
			r.Header.Set("Origin", "https://evil.test")
			r.Header.Set("Referer", trustedOrigin+"/")
		}},
		{name: "null origin", mod: withHeader("Origin", "null")},
		{name: "malformed origin", mod: withHeader("Origin", "not a url")},
		{name: "referer mismatch", mod: withHeader("Referer", "https://evil.test/")},
		{name: "malformed referer", mod: withHeader("Referer", "://bad")},
		{name: "neither origin nor referer", mod: withoutSource},
		{name: "Host and X-Forwarded-Host are not trusted", mod: func(r *http.Request) {
			r.Host = "evil.test"
			r.Header.Set("X-Forwarded-Host", "evil.test")
			r.Header.Set("Origin", "https://evil.test")
		}},
	}

	endpoints := []struct {
		name string
		call func(h *harness, mod func(*http.Request)) *httptest.ResponseRecorder
	}{
		{name: "login", call: func(h *harness, mod func(*http.Request)) *httptest.ResponseRecorder {
			return h.send(http.MethodPost, "/api/v1/auth/login", loginBody("alice", testPassword), withHeader("Content-Type", "application/json"), mod)
		}},
		{name: "logout", call: func(h *harness, mod func(*http.Request)) *httptest.ResponseRecorder {
			return h.send(http.MethodPost, "/api/v1/auth/logout", "", withCookie("live-session"), mod)
		}},
	}

	for _, endpoint := range endpoints {
		for _, src := range sources {
			t.Run(endpoint.name+"/"+src.name, func(t *testing.T) {
				h := newHarness(t)
				h.auth.sessions["live-session"] = h.auth.principal

				rec := endpoint.call(h, src.mod)

				if src.allowed {
					if rec.Code != http.StatusNoContent {
						t.Fatalf("status = %d, want 204", rec.Code)
					}
					return
				}
				if rec.Code != http.StatusForbidden {
					t.Fatalf("status = %d, want 403", rec.Code)
				}
				if problem := decodeProblem(t, rec); problem.Code != codeCSRFRejected {
					t.Fatalf("problem.code = %q", problem.Code)
				}
				// 거절된 요청은 인증·Session 변경·Cookie 변경을 일으키면 안 된다.
				assertNoSetCookie(t, rec)
				if len(h.auth.logins) != 0 || h.auth.authenticates != 0 || len(h.auth.logouts) != 0 {
					t.Fatalf("거절된 요청이 Application에 도달했습니다: logins=%d authenticates=%d logouts=%d",
						len(h.auth.logins), h.auth.authenticates, len(h.auth.logouts))
				}
				if _, ok := h.auth.sessions["live-session"]; !ok {
					t.Fatal("거절된 Logout이 Session을 폐기했습니다")
				}
			})
		}
	}
}

func TestSourceCheckAppliesToEveryUnsafeMethod(t *testing.T) {
	h := newHarness(t)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		rec := h.send(method, "/api/v1/anything", "", withoutSource)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s without Origin/Referer = %d, want 403", method, rec.Code)
		}
	}
}

func TestSafeMethodsDoNotRequireSource(t *testing.T) {
	h := newHarness(t)
	h.auth.sessions["live-session"] = h.auth.principal

	rec := h.send(http.MethodGet, "/api/v1/me", "", withCookie("live-session"), withoutSource)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /me without Origin = %d, want 200", rec.Code)
	}
}

func TestProtectedEndpointsRequireSession(t *testing.T) {
	tests := []struct {
		name   string
		call   func(h *harness, mods ...func(*http.Request)) *httptest.ResponseRecorder
		cookie bool
	}{
		{name: "me", call: func(h *harness, mods ...func(*http.Request)) *httptest.ResponseRecorder {
			return h.send(http.MethodGet, "/api/v1/me", "", mods...)
		}},
		{name: "logout", call: func(h *harness, mods ...func(*http.Request)) *httptest.ResponseRecorder {
			return h.send(http.MethodPost, "/api/v1/auth/logout", "", append(mods, withHeader("Origin", trustedOrigin))...)
		}},
		{name: "list classes", call: func(h *harness, mods ...func(*http.Request)) *httptest.ResponseRecorder {
			return h.send(http.MethodGet, "/api/v1/classes", "", mods...)
		}},
		{name: "get class", call: func(h *harness, mods ...func(*http.Request)) *httptest.ResponseRecorder {
			return h.send(http.MethodGet, "/api/v1/classes/"+uuid.NewString(), "", mods...)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name+" without cookie", func(t *testing.T) {
			h := newHarness(t)

			rec := tt.call(h)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if problem := decodeProblem(t, rec); problem.Code != codeUnauthenticated {
				t.Fatalf("problem.code = %q", problem.Code)
			}
			assertNoSetCookie(t, rec)
			if h.auth.authenticates != 0 {
				t.Fatal("Cookie가 없는데 Session을 조회했습니다")
			}
			if h.classes.listCalls != 0 || len(h.classes.getCalls) != 0 {
				t.Fatal("인증되지 않은 요청이 Class use case를 호출했습니다")
			}
		})

		t.Run(tt.name+" with invalid cookie clears it", func(t *testing.T) {
			h := newHarness(t)

			rec := tt.call(h, withCookie("stale-or-forged"))

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if cleared := sessionCookieFrom(t, rec); cleared.Value != "" || cleared.MaxAge != -1 || !cleared.Secure || cleared.Path != "/" {
				t.Fatalf("stale Cookie 제거 형식 = %+v", cleared)
			}
		})

		t.Run(tt.name+" with empty cookie is treated as absent", func(t *testing.T) {
			h := newHarness(t)

			rec := tt.call(h, withCookie(""))

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			assertNoSetCookie(t, rec)
		})
	}
}

func TestLogoutRevokesOnlyTheCurrentSessionOverHTTP(t *testing.T) {
	h := newHarness(t)
	browserA := auth.SessionToken(sessionCookieFrom(t, h.login("alice", testPassword)).Value)
	browserB := auth.SessionToken(sessionCookieFrom(t, h.login("alice", testPassword)).Value)

	h.send(http.MethodPost, "/api/v1/auth/logout", "", withHeader("Origin", trustedOrigin), withCookie(browserA))

	if rec := h.send(http.MethodGet, "/api/v1/me", "", withCookie(browserA)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("logout한 Browser /me = %d, want 401", rec.Code)
	}
	if rec := h.send(http.MethodGet, "/api/v1/me", "", withCookie(browserB)); rec.Code != http.StatusOK {
		t.Fatalf("다른 Browser /me = %d, want 200", rec.Code)
	}
}

func TestAuthMiddlewareAttachesPrincipalToContext(t *testing.T) {
	h := newHarness(t)
	h.auth.sessions["live-session"] = h.auth.principal
	a := &api{auth: h.auth, origin: trustedOrigin, logger: slog.New(slog.DiscardHandler)}

	var got auth.Principal
	var attached bool
	protected := a.authenticated(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, attached = PrincipalFrom(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "live-session"})
	protected.ServeHTTP(rec, req)

	if !attached || got.User.ID != h.auth.principal.User.ID {
		t.Fatalf("PrincipalFrom() = (%+v, %v)", got, attached)
	}
	if _, ok := PrincipalFrom(context.Background()); ok {
		t.Fatal("인증하지 않은 context에 Principal이 있으면 안 됩니다")
	}
}

// 내부 오류(저장소 오류 문자열, SQLSTATE, constraint 이름, Secret)는 응답에 나가지 않는다.
func TestInternalErrorsAreNotExposed(t *testing.T) {
	const leak = `ERROR: duplicate key value violates unique constraint "uq_auth_sessions_token" (SQLSTATE 23505) password=hunter2`
	internal := &repository.Error{Kind: repository.KindInternal, Op: "CreateAuthSession", SQLState: "23505", Constraint: "uq_auth_sessions_token", Cause: errors.New(leak)}

	tests := []struct {
		name  string
		setup func(h *harness)
		call  func(h *harness) *httptest.ResponseRecorder
	}{
		{
			name:  "login",
			setup: func(h *harness) { h.auth.loginErr = fmt.Errorf("auth: Session 발급: %w", internal) },
			call:  func(h *harness) *httptest.ResponseRecorder { return h.login("alice", testPassword) },
		},
		{
			name:  "authenticate",
			setup: func(h *harness) { h.auth.authErr = fmt.Errorf("auth: Session 조회: %w", internal) },
			call: func(h *harness) *httptest.ResponseRecorder {
				return h.send(http.MethodGet, "/api/v1/me", "", withCookie("some-token"))
			},
		},
		{
			name: "logout",
			setup: func(h *harness) {
				h.auth.sessions["live-session"] = h.auth.principal
				h.auth.logoutErr = fmt.Errorf("auth: Session revoke: %w", internal)
			},
			call: func(h *harness) *httptest.ResponseRecorder {
				return h.send(http.MethodPost, "/api/v1/auth/logout", "", withHeader("Origin", trustedOrigin), withCookie("live-session"))
			},
		},
		{
			name:  "unclassified error",
			setup: func(h *harness) { h.auth.loginErr = errors.New(leak) },
			call:  func(h *harness) *httptest.ResponseRecorder { return h.login("alice", testPassword) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			tt.setup(h)

			rec := tt.call(h)

			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", rec.Code)
			}
			body := rec.Body.String()
			for _, secret := range []string{"duplicate key", "SQLSTATE", "23505", "uq_auth_sessions_token", "hunter2", "CreateAuthSession", "repository"} {
				if strings.Contains(body, secret) {
					t.Fatalf("응답이 내부 정보(%q)를 노출합니다: %s", secret, body)
				}
			}
			problem := decodeProblem(t, rec)
			if problem.Code != codeInternal {
				t.Fatalf("problem.code = %q", problem.Code)
			}
			assertNoSetCookie(t, rec)

			// 운영 진단을 위해 같은 requestId로 log에 남긴다.
			if !strings.Contains(h.logs.String(), problem.RequestID) {
				t.Fatalf("log에 requestId %q가 없습니다: %s", problem.RequestID, h.logs.String())
			}
		})
	}
}

// repository.Error.Cause와 오류 원문에는 driver/PostgreSQL 원문, Password hash, Session token 같은 값이 들어 있을 수 있다.
// 응답과 log 어디에도 나가면 안 되고, log에는 correlation과 분류 정보만 남아야 한다.
func TestInternalErrorLogKeepsClassificationButNeverRawCause(t *testing.T) {
	const (
		rawDetail = `password authentication failed for user "labbit" (SQLSTATE 28P01)`
		fakeHash  = "$argon2id$v=19$m=19456,t=2,p=1$ZmFrZS1zYWx0LWZvci10ZXN0$ZmFrZS1oYXNoLWZvci10ZXN0LW9ubHk"
		fakeToken = "fake-session-token-value-for-test-only-0123456789"
	)
	rawText := fmt.Sprintf("%s hash=%s token=%s", rawDetail, fakeHash, fakeToken)
	secrets := []string{rawDetail, "password authentication failed", fakeHash, "argon2id", fakeToken, "28P01"}

	repoErr := &repository.Error{
		Kind: repository.KindInternal, Op: "CreateAuthSession",
		SQLState: "23505", Constraint: "uq_auth_sessions_token_hash", Cause: errors.New(rawText),
	}

	variants := []struct {
		name          string
		err           error
		wantKind      string
		wantRepoOp    string
		wantSQLState  string
		wantConstrain string
	}{
		{name: "repository error", err: repoErr, wantKind: "internal", wantRepoOp: "CreateAuthSession", wantSQLState: "23505", wantConstrain: "uq_auth_sessions_token_hash"},
		{name: "wrapped repository error", err: fmt.Errorf("auth: Session 발급: %w", repoErr), wantKind: "internal", wantRepoOp: "CreateAuthSession", wantSQLState: "23505", wantConstrain: "uq_auth_sessions_token_hash"},
		{name: "unclassified error carrying secrets", err: errors.New(rawText), wantKind: "unclassified"},
		{name: "wrapped unclassified error", err: fmt.Errorf("auth: verify: %w", errors.New(rawText)), wantKind: "unclassified"},
		{name: "unusable stored password hash", err: fmt.Errorf("auth: password 검증: %w", auth.ErrMalformedPasswordHash), wantKind: "unusable_password_hash"},
		{name: "request context canceled", err: fmt.Errorf("auth: Session 조회: %w", context.Canceled), wantKind: "context"},
	}
	endpoints := []struct {
		operation string
		inject    func(h *harness, err error)
		call      func(h *harness) *httptest.ResponseRecorder
	}{
		{
			operation: "login",
			inject:    func(h *harness, err error) { h.auth.loginErr = err },
			call:      func(h *harness) *httptest.ResponseRecorder { return h.login("alice", testPassword) },
		},
		{
			operation: "authenticate",
			inject:    func(h *harness, err error) { h.auth.authErr = err },
			call: func(h *harness) *httptest.ResponseRecorder {
				return h.send(http.MethodGet, "/api/v1/me", "", withCookie(fakeToken))
			},
		},
		{
			operation: "logout",
			inject: func(h *harness, err error) {
				h.auth.sessions[fakeToken] = h.auth.principal
				h.auth.logoutErr = err
			},
			call: func(h *harness) *httptest.ResponseRecorder {
				return h.send(http.MethodPost, "/api/v1/auth/logout", "", withHeader("Origin", trustedOrigin), withCookie(fakeToken))
			},
		},
	}

	for _, endpoint := range endpoints {
		for _, variant := range variants {
			t.Run(endpoint.operation+"/"+variant.name, func(t *testing.T) {
				h := newHarness(t)
				endpoint.inject(h, variant.err)

				rec := endpoint.call(h)

				if rec.Code != http.StatusInternalServerError {
					t.Fatalf("status = %d, want 500", rec.Code)
				}
				problem := decodeProblem(t, rec)

				logs := h.logs.String()
				for _, secret := range secrets {
					if strings.Contains(rec.Body.String(), secret) {
						t.Fatalf("응답이 원문(%q)을 노출합니다", secret)
					}
					if strings.Contains(logs, secret) {
						t.Fatalf("log가 원문(%q)을 노출합니다: %s", secret, logs)
					}
				}

				// correlation과 분류 정보는 남는다.
				records := logRecords(t, h.logs)
				if len(records) != 1 {
					t.Fatalf("log record = %d, want 1: %s", len(records), logs)
				}
				record := records[0]
				want := map[string]string{
					"request_id":           problem.RequestID,
					"operation":            endpoint.operation,
					"error_kind":           variant.wantKind,
					"repository_operation": variant.wantRepoOp,
					"sqlstate":             variant.wantSQLState,
					"constraint":           variant.wantConstrain,
				}
				for key, value := range want {
					got, present := record[key]
					if value == "" {
						if present {
							t.Errorf("log field %q = %v, want absent", key, got)
						}
						continue
					}
					if got != value {
						t.Errorf("log field %q = %v, want %q", key, got, value)
					}
				}
				for _, key := range []string{"error", "cause", "repository"} {
					if _, present := record[key]; present {
						t.Errorf("log에 원문을 담을 수 있는 field %q가 있습니다: %v", key, record[key])
					}
				}
			})
		}
	}
}

// logRecords는 JSON handler가 기록한 줄마다 하나의 record로 읽는다.
func logRecords(t *testing.T, logs *bytes.Buffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("log line이 JSON이 아닙니다: %q (%v)", line, err)
		}
		records = append(records, record)
	}
	return records
}

// Password 원문과 Session token은 log에 남지 않는다.
func TestLogsDoNotContainPasswordOrSessionToken(t *testing.T) {
	h := newHarness(t)
	const presented = "presented-session-token-value"

	h.auth.loginErr = errors.New("boom")
	h.login("alice", testPassword, withCookie(presented))
	h.auth.loginErr = nil

	h.auth.authErr = errors.New("boom")
	h.send(http.MethodGet, "/api/v1/me", "", withCookie(presented))

	logs := h.logs.String()
	if logs == "" {
		t.Fatal("내부 오류가 log에 남아야 합니다")
	}
	for _, secret := range []string{testPassword, presented} {
		if strings.Contains(logs, secret) {
			t.Fatalf("log가 민감한 값(%q)을 포함합니다: %s", secret, logs)
		}
	}
}

func TestResponsesAreNotCacheable(t *testing.T) {
	h := newHarness(t)
	h.auth.sessions["live-session"] = h.auth.principal

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"login ok":       h.login("alice", testPassword),
		"login rejected": h.login("alice", "test-wrong-password"),
		"me ok":          h.send(http.MethodGet, "/api/v1/me", "", withCookie("live-session")),
		"me rejected":    h.send(http.MethodGet, "/api/v1/me", ""),
		"csrf rejected":  h.send(http.MethodPost, "/api/v1/auth/logout", "", withoutSource),
	} {
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s Cache-Control = %q, want no-store", name, got)
		}
	}
}

func TestEachRequestGetsItsOwnRequestID(t *testing.T) {
	h := newHarness(t)

	a := decodeProblem(t, h.send(http.MethodGet, "/api/v1/me", "", withHeader("X-Request-Id", "client-supplied")))
	b := decodeProblem(t, h.send(http.MethodGet, "/api/v1/me", ""))

	if a.RequestID == b.RequestID || a.RequestID == "client-supplied" {
		t.Fatalf("requestId는 요청마다 서버가 생성해야 합니다: %q, %q", a.RequestID, b.RequestID)
	}
}

func TestNewValidatesOptions(t *testing.T) {
	if _, err := New(Options{Classes: &fakeClasses{}, PublicOrigin: trustedOrigin}); err == nil {
		t.Error("Authenticator가 없으면 오류여야 합니다")
	}
	if _, err := New(Options{Auth: newFakeAuth(), PublicOrigin: trustedOrigin}); err == nil {
		t.Error("Classes가 없으면 오류여야 합니다")
	}
	for _, origin := range []string{"", "labbit.test", "https://labbit.test/app"} {
		if _, err := New(Options{Auth: newFakeAuth(), Classes: &fakeClasses{}, PublicOrigin: origin}); err == nil {
			t.Errorf("PublicOrigin %q는 오류여야 합니다", origin)
		}
	}
}
