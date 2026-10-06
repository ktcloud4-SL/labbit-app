package preview

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// ---- Browser 인증: bootstrap → exchange → Cookie ----

func TestBootstrapPageIsStaticAndLocksDownItsScript(t *testing.T) {
	e := newEnv(t)
	s := e.activate()

	resp, body := e.do(s.Expected, http.MethodGet, BootstrapPath, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}
	if got := resp.Header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q", got)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", got)
	}

	// CSP는 inline script를 이 페이지의 script hash로만 허용한다(test는 hash를 독립적으로 다시 계산한다).
	start := strings.Index(body, "<script>")
	end := strings.Index(body, "</script>")
	if start < 0 || end < start {
		t.Fatalf("script를 찾지 못함: %s", body)
	}
	sum := sha256.Sum256([]byte(body[start+len("<script>") : end]))
	wantHash := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	csp := resp.Header.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src " + wantHash, "connect-src 'self'", "base-uri 'none'", "form-action 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q에 %q가 없음", csp, want)
		}
	}
	if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
		t.Errorf("CSP가 unsafe 값을 허용함: %q", csp)
	}

	// 모든 PreviewSession에 같은 정적 HTML이며 credential이나 ID를 담지 않는다. fragment는 서버가 받지 않으므로 script가 location.hash로 읽는다.
	for _, secret := range []string{s.Credential, s.SessionID, s.OwnerID, s.ProviderServerID, s.TargetVMKey} {
		if strings.Contains(body, secret) {
			t.Errorf("bootstrap 페이지가 %q를 담음", secret)
		}
	}
	for _, want := range []string{"location.hash", "history.replaceState", "/__labbit/exchange", "credentials: 'same-origin'", "location.replace('/')"} {
		if !strings.Contains(body, want) {
			t.Errorf("bootstrap script에 %q가 없음", want)
		}
	}
	// credential을 URL query나 path로 옮기지 않는다.
	if strings.Contains(body, "?credential") || strings.Contains(body, "location.search") {
		t.Error("bootstrap script가 credential을 query로 다룸")
	}

	if resp, _ := e.do(s.Expected, http.MethodHead, BootstrapPath, ""); resp.StatusCode != http.StatusOK {
		t.Errorf("HEAD status = %d", resp.StatusCode)
	}
	resp, _ = e.do(s.Expected, http.MethodPost, BootstrapPath, "x")
	if resp.StatusCode != http.StatusMethodNotAllowed || resp.Header.Get("Allow") != "GET, HEAD" {
		t.Errorf("POST status = %d Allow = %q", resp.StatusCode, resp.Header.Get("Allow"))
	}
}

func TestBootstrapPageIsNotServedForUnknownOrInactiveSessions(t *testing.T) {
	e := newEnv(t)
	unknown := e.expected()
	if resp, _ := e.do(unknown, http.MethodGet, BootstrapPath, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("알 수 없는 PreviewSession status = %d, want 401", resp.StatusCode)
	}
	pending := e.expect()
	if resp, _ := e.do(pending, http.MethodGet, BootstrapPath, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("활성화 전 PreviewSession status = %d, want 401", resp.StatusCode)
	}
}

func TestExchangeIssuesAHostOnlyHttpOnlyCookieAndTheCredentialIsSingleUse(t *testing.T) {
	e := newEnv(t)
	s := e.activate()

	resp, body := e.exchange(s.Expected, s.Credential)
	if resp.StatusCode != http.StatusNoContent || body != "" {
		t.Fatalf("exchange status = %d body = %q", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}
	cookies := resp.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("Set-Cookie %d개, want 1개", len(cookies))
	}
	c := cookies[0]
	if c.Name != "labbit-preview" || c.Path != "/" || c.Domain != "" || !c.HttpOnly || c.Secure || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie = %+v", c)
	}
	if len(c.Value) != 43 || c.Value == s.Credential {
		t.Fatalf("cookie token = %q: bootstrap credential과 다른 새 CSPRNG token이어야 함", c.Value)
	}
	info, _ := e.gw.Info(s.SessionID)
	if !c.Expires.Equal(info.ExpiresAt.Truncate(time.Second)) {
		t.Errorf("cookie 만료 = %v, want PreviewSession 만료 %v", c.Expires, info.ExpiresAt)
	}

	// 같은 credential은 한 번만 성공한다. 두 번째는 이유를 구분하지 않는 401이다.
	resp2, _ := e.exchange(s.Expected, s.Credential)
	if resp2.StatusCode != http.StatusUnauthorized || len(resp2.Cookies()) != 0 {
		t.Fatalf("재사용 status = %d cookies = %v", resp2.StatusCode, resp2.Cookies())
	}
	// 첫 Cookie는 계속 유효하다.
	if resp, body := e.get(s.Expected, c, "/"); resp.StatusCode != http.StatusOK || body != "hello from workspace" {
		t.Fatalf("Cookie로 요청 = %d %q", resp.StatusCode, body)
	}

	// 서버에는 digest만 있다. 원문은 저장하지 않는다.
	sess := e.gw.lookup(s.SessionID)
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.bootstrapDigest != sha256.Sum256([]byte(s.Credential)) || sess.cookieDigest != sha256.Sum256([]byte(c.Value)) {
		t.Fatal("session이 token의 SHA-256 digest를 갖고 있지 않음")
	}
}

// HTTPS Preview Origin에서는 __Host- prefix와 Secure다.
func TestSecureOriginUsesTheHostPrefixedSecureCookie(t *testing.T) {
	https, err := ParseOriginTemplate("https://{sessionId}.preview.test", true)
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, func(o *Options) { o.Origin = https })
	s := e.activate()

	resp, _ := e.exchange(s.Expected, s.Credential)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	c := resp.Cookies()[0]
	if c.Name != "__Host-labbit-preview" || !c.Secure || !c.HttpOnly || c.Path != "/" || c.Domain != "" || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie = %+v", c)
	}
	line := resp.Header.Get("Set-Cookie")
	if strings.Contains(strings.ToLower(line), "domain") {
		t.Errorf("Domain attribute가 있음: %q", line)
	}
	// 개발용 이름의 Cookie는 이 Origin에서 인증이 아니다.
	if resp, _ := e.get(s.Expected, &http.Cookie{Name: "labbit-preview", Value: c.Value}, "/"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("다른 이름 Cookie status = %d, want 401", resp.StatusCode)
	}
	if resp, body := e.get(s.Expected, c, "/"); resp.StatusCode != http.StatusOK {
		t.Errorf("정상 Cookie status = %d %s", resp.StatusCode, body)
	}
}

func TestExchangeRejectsBadRequestsWithoutBurningTheCredential(t *testing.T) {
	e := newEnv(t)
	s := e.activate()
	other := e.activate()

	post := func(x Expected, body string, mods ...func(*http.Request)) *http.Response {
		resp, _ := e.do(x, http.MethodPost, ExchangePath, body, mods...)
		return resp
	}
	jsonHeaders := func(r *http.Request) {
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", e.origin(s.Expected))
	}
	good, _ := json.Marshal(map[string]string{"credential": s.Credential})

	cases := []struct {
		name string
		resp *http.Response
		want int
	}{
		{"Origin 없음", post(s.Expected, string(good), func(r *http.Request) { r.Header.Set("Content-Type", "application/json") }), http.StatusForbidden},
		{"다른 Origin", post(s.Expected, string(good), func(r *http.Request) {
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", "https://labbit.example.com")
		}), http.StatusForbidden},
		{"다른 PreviewSession의 Origin", post(s.Expected, string(good), func(r *http.Request) {
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", e.origin(other.Expected))
		}), http.StatusForbidden},
		{"Origin이 둘", post(s.Expected, string(good), func(r *http.Request) {
			r.Header.Set("Content-Type", "application/json")
			r.Header.Add("Origin", e.origin(s.Expected))
			r.Header.Add("Origin", e.origin(s.Expected))
		}), http.StatusForbidden},
		{"Content-Type 없음", post(s.Expected, string(good), func(r *http.Request) { r.Header.Set("Origin", e.origin(s.Expected)) }), http.StatusBadRequest},
		{"form 본문", post(s.Expected, "credential="+s.Credential, func(r *http.Request) {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("Origin", e.origin(s.Expected))
		}), http.StatusBadRequest},
		{"JSON이 아님", post(s.Expected, "not json", jsonHeaders), http.StatusBadRequest},
		{"credential 없음", post(s.Expected, `{}`, jsonHeaders), http.StatusBadRequest},
		{"credential이 빈 문자열", post(s.Expected, `{"credential":""}`, jsonHeaders), http.StatusBadRequest},
		{"credential이 문자열이 아님", post(s.Expected, `{"credential":7}`, jsonHeaders), http.StatusBadRequest},
		{"추가 field", post(s.Expected, `{"credential":"`+s.Credential+`","extra":1}`, jsonHeaders), http.StatusBadRequest},
		{"JSON 값이 둘", post(s.Expected, string(good)+string(good), jsonHeaders), http.StatusBadRequest},
		{"너무 큰 본문", post(s.Expected, `{"credential":"`+strings.Repeat("a", 8192)+`"}`, jsonHeaders), http.StatusBadRequest},
		{"알 수 없는 credential", post(s.Expected, `{"credential":"`+strings.Repeat("A", 43)+`"}`, jsonHeaders), http.StatusUnauthorized},
		{"형식이 틀린 credential", post(s.Expected, `{"credential":"short"}`, jsonHeaders), http.StatusUnauthorized},
		{"다른 PreviewSession의 credential", post(s.Expected, `{"credential":"`+other.Credential+`"}`, jsonHeaders), http.StatusUnauthorized},
		{"GET", func() *http.Response { r, _ := e.do(s.Expected, http.MethodGet, ExchangePath, ""); return r }(), http.StatusMethodNotAllowed},
	}
	for _, tc := range cases {
		if tc.resp.StatusCode != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.name, tc.resp.StatusCode, tc.want)
		}
		if len(tc.resp.Cookies()) != 0 {
			t.Errorf("%s: 실패한 교환이 Cookie를 발급함", tc.name)
		}
	}

	// 위의 어떤 실패도 credential을 소모하지 않는다. 올바른 교환은 여전히 성공한다.
	if resp, _ := e.exchange(s.Expected, s.Credential); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("정상 exchange status = %d, want 204", resp.StatusCode)
	}
	// 알 수 없는 PreviewSession host는 401이다.
	unknown := e.expected()
	if resp, _ := e.exchange(unknown, s.Credential); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("알 수 없는 PreviewSession exchange status = %d, want 401", resp.StatusCode)
	}
}

func TestBootstrapCredentialExpiresBeforeTheSessionDoes(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.BootstrapTTL = 10 * time.Second })
	s := e.activate()
	e.clock.Advance(11 * time.Second)
	resp, _ := e.exchange(s.Expected, s.Credential)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("만료된 credential status = %d, want 401", resp.StatusCode)
	}
	// PreviewSession은 살아 있지만 Cookie가 없으니 요청은 인증되지 않는다.
	if info, _ := e.gw.Info(s.SessionID); info.Ended {
		t.Fatal("bootstrap credential 만료가 PreviewSession을 끝냄")
	}
	if resp, _ := e.get(s.Expected, nil, "/"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Cookie 없는 요청 status = %d", resp.StatusCode)
	}
}

// bootstrap credential은 PreviewSession TTL보다 오래 유효하지 않다.
func TestBootstrapCredentialNeverOutlivesTheSession(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.BootstrapTTL = time.Hour })
	s := e.activate(func(x *Expected) { x.TTL = 30 * time.Second })
	sess := e.gw.lookup(s.SessionID)
	sess.mu.Lock()
	bootstrapExpires, expiresAt := sess.bootstrapExpires, sess.expiresAt
	sess.mu.Unlock()
	if bootstrapExpires.After(expiresAt) {
		t.Fatalf("bootstrap 만료 %v가 PreviewSession 만료 %v보다 늦음", bootstrapExpires, expiresAt)
	}
}

func TestUnauthenticatedProxyRequestsAreRejected(t *testing.T) {
	e := newEnv(t)
	s := e.activate()
	cookie := e.login(s)
	other := e.activate()
	otherCookie := e.login(other)

	cases := []struct {
		name   string
		cookie *http.Cookie
		x      Expected
	}{
		{"Cookie 없음", nil, s.Expected},
		{"틀린 token", &http.Cookie{Name: "labbit-preview", Value: strings.Repeat("A", 43)}, s.Expected},
		{"형식이 틀린 token", &http.Cookie{Name: "labbit-preview", Value: "x"}, s.Expected},
		{"빈 token", &http.Cookie{Name: "labbit-preview", Value: ""}, s.Expected},
		{"bootstrap credential을 Cookie로 사용", &http.Cookie{Name: "labbit-preview", Value: s.Credential}, s.Expected},
		{"다른 PreviewSession의 Cookie", otherCookie, s.Expected},
		{"로그인 Session Cookie 이름", &http.Cookie{Name: "__Host-labbit-session", Value: cookie.Value}, s.Expected},
		{"알 수 없는 PreviewSession", cookie, e.expected()},
	}
	for _, tc := range cases {
		resp, body := e.get(tc.x, tc.cookie, "/")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", tc.name, resp.StatusCode)
			continue
		}
		var problem map[string]any
		if err := json.Unmarshal([]byte(body), &problem); err != nil || problem["code"] != "preview_unauthenticated" ||
			problem["status"] != float64(401) || problem["requestId"] == "" || problem["type"] != "about:blank" {
			t.Errorf("%s: problem = %s", tc.name, body)
		}
		if got := resp.Header.Get("Content-Type"); got != "application/problem+json" {
			t.Errorf("%s: Content-Type = %q", tc.name, got)
		}
		if got := resp.Header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q", tc.name, got)
		}
	}
	// 같은 이름 Cookie가 둘이면(cookie tossing) 어느 쪽도 신뢰하지 않는다.
	resp, _ := e.get(s.Expected, cookie, "/", func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: "labbit-preview", Value: strings.Repeat("B", 43)})
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("같은 이름 Cookie 둘 status = %d, want 401", resp.StatusCode)
	}
	// 인증되지 않은 요청은 Workspace application에 전달되지 않는다.
	for _, r := range e.app.all() {
		t.Errorf("인증 실패한 요청이 application에 도달함: %+v", r)
	}
}

// ---- proxy ----

func TestProxyForwardsGetAndPostAndPassesApplicationResponsesThrough(t *testing.T) {
	e := newEnv(t)
	s := e.activate()
	cookie := e.login(s)

	e.app.setHandler(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/missing":
			w.Header().Set("X-App", "not-found")
			http.Error(w, "app says no", http.StatusNotFound)
		case "/boom":
			http.Error(w, "app crashed", http.StatusInternalServerError)
		case "/echo":
			body, _ := io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-App", "echo")
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"method":%q,"query":%q,"body":%q,"ct":%q}`, r.Method, r.URL.RawQuery, string(body), r.Header.Get("Content-Type"))
		default:
			_, _ = io.WriteString(w, "index")
		}
	})

	resp, body := e.get(s.Expected, cookie, "/")
	if resp.StatusCode != http.StatusOK || body != "index" {
		t.Fatalf("GET / = %d %q", resp.StatusCode, body)
	}

	resp, body = e.do(s.Expected, http.MethodPost, "/echo?a=1&b=two%20words", `{"name":"x"}`, func(r *http.Request) {
		r.AddCookie(cookie)
		r.Header.Set("Content-Type", "application/json")
	})
	var echoed map[string]string
	if resp.StatusCode != http.StatusCreated || json.Unmarshal([]byte(body), &echoed) != nil ||
		echoed["method"] != "POST" || echoed["query"] != "a=1&b=two%20words" || echoed["body"] != `{"name":"x"}` || echoed["ct"] != "application/json" {
		t.Fatalf("POST /echo = %d %s", resp.StatusCode, body)
	}
	if resp.Header.Get("X-App") != "echo" || resp.Header.Get("Content-Type") != "application/json" {
		t.Errorf("application 응답 header가 전달되지 않음: %v", resp.Header)
	}

	// application의 404와 500은 그대로 전달한다. Gateway의 오류(Problem Details)로 바꾸지 않는다.
	resp, body = e.get(s.Expected, cookie, "/missing")
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(body, "app says no") || resp.Header.Get("X-App") != "not-found" {
		t.Errorf("application 404 = %d %q %v", resp.StatusCode, body, resp.Header)
	}
	if resp.Header.Get("Content-Type") == "application/problem+json" {
		t.Error("application의 404를 Gateway Problem으로 바꿈")
	}
	resp, body = e.get(s.Expected, cookie, "/boom")
	if resp.StatusCode != http.StatusInternalServerError || !strings.Contains(body, "app crashed") {
		t.Errorf("application 500 = %d %q", resp.StatusCode, body)
	}

	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
		resp, _ := e.do(s.Expected, method, "/echo", "", func(r *http.Request) { r.AddCookie(cookie) })
		if resp.StatusCode != http.StatusCreated {
			t.Errorf("%s status = %d", method, resp.StatusCode)
		}
	}
	if resp, _ := e.do(s.Expected, http.MethodHead, "/", "", func(r *http.Request) { r.AddCookie(cookie) }); resp.StatusCode != http.StatusOK {
		t.Errorf("HEAD status = %d", resp.StatusCode)
	}
}

func TestProxyKeepsThePreviewHostAndStripsLabbitCredentials(t *testing.T) {
	e := newEnv(t)
	s := e.activate()
	cookie := e.login(s)

	const sessionCookieMarker = "SAAS-SESSION-COOKIE-MARKER-5c1a"
	resp, _ := e.get(s.Expected, cookie, "/path?x=1", func(r *http.Request) {
		// 요청이 Preview Cookie, 로그인 Session Cookie(이름이 둘 다), application Cookie를 함께 보낸다.
		r.Header.Set("Cookie", cookie.Name+"="+cookie.Value+"; theme=dark; __Host-labbit-session="+sessionCookieMarker+"; labbit-session="+sessionCookieMarker+"; lang=ko")
		r.Header.Set("Authorization", "Bearer app-own-token")
		r.Header.Set("X-Forwarded-For", "203.0.113.9")
		r.Header.Set("X-Forwarded-Host", "attacker.example")
		r.Header.Set("Forwarded", "for=203.0.113.9")
		r.Header.Set("Proxy-Authorization", "Basic cHJveHk=")
		r.Header.Set("Te", "trailers")
		r.Header.Set("X-App-Header", "kept")
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	reqs := e.app.all()
	if len(reqs) != 1 {
		t.Fatalf("application이 받은 요청 %d개", len(reqs))
	}
	got := reqs[0]
	if got.Host != e.host(s.Expected) {
		t.Errorf("Host = %q, want Preview Origin의 host %q", got.Host, e.host(s.Expected))
	}
	if got.URI != "/path?x=1" {
		t.Errorf("request URI = %q", got.URI)
	}
	if got.Header.Get("X-Forwarded-Host") != e.host(s.Expected) || got.Header.Get("X-Forwarded-Proto") != "http" {
		t.Errorf("X-Forwarded-* = %q / %q", got.Header.Get("X-Forwarded-Host"), got.Header.Get("X-Forwarded-Proto"))
	}
	if got.Header.Get("X-Forwarded-For") != "" || got.Header.Get("Forwarded") != "" {
		t.Errorf("client가 보낸 전달 header를 믿고 넘김: %v", got.Header)
	}
	if got.Header.Get("Proxy-Authorization") != "" {
		t.Error("hop-by-hop Proxy-Authorization을 전달함")
	}
	// Labbit 소유 credential은 어떤 형태로도 application에 전달하지 않는다. application Cookie와 Authorization은 application의 것이다.
	if cookies := got.Header.Get("Cookie"); cookies != "theme=dark; lang=ko" {
		t.Errorf("Cookie = %q, want application Cookie만", cookies)
	}
	if got.Header.Get("Authorization") != "Bearer app-own-token" || got.Header.Get("X-App-Header") != "kept" {
		t.Errorf("application header를 바꿈: %v", got.Header)
	}
	dump := fmt.Sprintf("%+v", got)
	for _, secret := range []string{cookie.Value, sessionCookieMarker, s.Credential, "labbit-preview", "labbit-session"} {
		if strings.Contains(dump, secret) {
			t.Errorf("application이 받은 요청에 %q가 있음: %s", secret, dump)
		}
	}
}

func TestProxySanitizesApplicationSetCookie(t *testing.T) {
	e := newEnv(t)
	s := e.activate()
	cookie := e.login(s)
	e.app.setHandler(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Set-Cookie", "app=1; Path=/; Domain=.preview.test; HttpOnly")
		w.Header().Add("Set-Cookie", "labbit-preview=evil; Path=/")
		w.Header().Add("Set-Cookie", "__Host-labbit-session=evil; Path=/; Secure")
		w.Header().Add("Set-Cookie", "kept=2; Path=/x")
		_, _ = io.WriteString(w, "ok")
	})
	resp, _ := e.get(s.Expected, cookie, "/")
	lines := resp.Header.Values("Set-Cookie")
	if len(lines) != 2 {
		t.Fatalf("Set-Cookie = %q, want 2개", lines)
	}
	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), "domain") || strings.Contains(line, "labbit") {
			t.Errorf("Set-Cookie %q가 정리되지 않음", line)
		}
	}
}

func TestProxyRejectsUpgradeAndUnsupportedMethodsAndNeverForwardsThem(t *testing.T) {
	e := newEnv(t)
	s := e.activate()
	cookie := e.login(s)

	resp, body := e.get(s.Expected, cookie, "/ws", func(r *http.Request) {
		r.Header.Set("Connection", "Upgrade")
		r.Header.Set("Upgrade", "websocket")
	})
	if resp.StatusCode != http.StatusNotImplemented || !strings.Contains(body, `"preview_upgrade_unsupported"`) {
		t.Errorf("Upgrade = %d %s", resp.StatusCode, body)
	}
	resp, _ = e.get(s.Expected, cookie, "/ws", func(r *http.Request) { r.Header.Set("Connection", "keep-alive, Upgrade") })
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("Connection: keep-alive, Upgrade status = %d", resp.StatusCode)
	}
	resp, body = e.do(s.Expected, "TRACE", "/", "", func(r *http.Request) { r.AddCookie(cookie) })
	if resp.StatusCode != http.StatusMethodNotAllowed || !strings.Contains(body, `"method_not_allowed"`) {
		t.Errorf("TRACE = %d %s", resp.StatusCode, body)
	}
	if reqs := e.app.all(); len(reqs) != 0 {
		t.Errorf("거절해야 할 요청이 application에 도달함: %+v", reqs)
	}
	// 거절이 tunnel을 끊지 않는다.
	if resp, _ := e.get(s.Expected, cookie, "/"); resp.StatusCode != http.StatusOK {
		t.Errorf("거절 뒤 정상 요청 status = %d", resp.StatusCode)
	}
}

func TestReservedPrefixIsHandledByTheGatewayAndNeverForwarded(t *testing.T) {
	e := newEnv(t)
	s := e.activate()
	cookie := e.login(s)

	for _, path := range []string{"/__labbit", "/__labbit/", "/__labbit/unknown", "/__labbit/bootstrap/extra", "/__labbit/exchange/x"} {
		resp, body := e.get(s.Expected, cookie, path)
		if resp.StatusCode != http.StatusNotFound || !strings.Contains(body, `"not_found"`) {
			t.Errorf("%s = %d %s", path, resp.StatusCode, body)
		}
	}
	// prefix만 비슷한 경로는 application의 경로다.
	if resp, _ := e.get(s.Expected, cookie, "/__labbitx"); resp.StatusCode != http.StatusOK {
		t.Errorf("/__labbitx status = %d", resp.StatusCode)
	}
	for _, r := range e.app.all() {
		if strings.HasPrefix(r.URI, "/__labbit/") || r.URI == "/__labbit" {
			t.Errorf("예약 경로가 application에 전달됨: %s", r.URI)
		}
	}
}

func TestHostThatIsNotAPreviewOriginIsNotServed(t *testing.T) {
	e := newEnv(t)
	s := e.activate()
	cookie := e.login(s)
	for _, host := range []string{"labbit.example.com", "preview.test", "a.b." + e.host(s.Expected), "evil.test"} {
		resp, _ := e.get(s.Expected, cookie, "/", func(r *http.Request) { r.Host = host })
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("Host %q status = %d, want 404", host, resp.StatusCode)
		}
	}
	if reqs := e.app.all(); len(reqs) != 0 {
		t.Errorf("Preview Origin이 아닌 host의 요청이 application에 도달함: %+v", reqs)
	}
}

// 한 PreviewSession의 tunnel은 TCP 연결 하나다. 동시에 들어온 요청은 순서대로 그 연결을 쓰고 응답이 섞이지 않는다.
func TestConcurrentRequestsShareOneTunnelWithoutMixingResponses(t *testing.T) {
	e := newEnv(t)
	s := e.activate()
	cookie := e.login(s)
	e.app.setHandler(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		_, _ = io.WriteString(w, "path="+r.URL.Path)
	})

	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path := fmt.Sprintf("/item-%d", i)
			resp, body := e.get(s.Expected, cookie, path)
			if resp.StatusCode != http.StatusOK || body != "path="+path {
				t.Errorf("%s = %d %q", path, resp.StatusCode, body)
			}
		}()
	}
	wg.Wait()
	if info, _ := e.gw.Info(s.SessionID); info.Ended {
		t.Fatal("동시 요청이 PreviewSession을 끝냄")
	}
}

// Browser가 요청 하나를 중단해도 PreviewSession의 tunnel은 유지된다. 그렇지 않으면 페이지 이동마다 PreviewSession이 끝난다.
func TestClientAbortDoesNotTearDownTheTunnel(t *testing.T) {
	e := newEnv(t)
	s := e.activate()
	cookie := e.login(s)
	release := make(chan struct{})
	e.app.setHandler(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stream" {
			_, _ = io.WriteString(w, "plain")
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		chunk := make([]byte, 32<<10)
		for range 8 {
			_, _ = w.Write(chunk)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		<-release
		_, _ = w.Write(chunk)
	})

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.originServer.URL+"/stream", nil)
	req.Host = e.host(s.Expected)
	req.AddCookie(cookie)
	resp, err := (&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(resp.Body, make([]byte, 1024)); err != nil {
		t.Fatal(err)
	}
	// 응답을 읽는 도중 Browser가 요청을 중단한다.
	cancel()
	resp.Body.Close()
	close(release)

	// tunnel이 그대로이므로 다음 요청이 성공한다.
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, body := e.get(s.Expected, cookie, "/next")
		if resp.StatusCode == http.StatusOK && body == "plain" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("요청 중단 뒤 다음 요청 = %d %q", resp.StatusCode, body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if info, _ := e.gw.Info(s.SessionID); info.Ended {
		t.Fatal("Browser의 요청 중단이 PreviewSession을 끝냄")
	}
}

// ---- 오류 의미 ----

func TestTunnelLossDuringARequestMapsTo502AndEndsTheSession(t *testing.T) {
	e := newEnv(t)
	s := e.activate()
	cookie := e.login(s)
	started := make(chan struct{})
	e.app.setHandler(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		// Connector가 tunnel을 끊으면 fake application의 연결도 닫혀 요청 context가 끝난다.
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})

	type result struct {
		status int
		body   string
	}
	done := make(chan result, 1)
	go func() {
		resp, body := e.get(s.Expected, cookie, "/slow")
		done <- result{resp.StatusCode, body}
	}()
	<-started
	// Connector가 Workspace VM과의 연결을 잃고 Data WSS를 끊는다.
	s.Peer.stop()

	select {
	case r := <-done:
		if r.status != http.StatusBadGateway || !strings.Contains(r.body, `"preview_tunnel_closed"`) {
			t.Fatalf("tunnel 손실 = %d %s", r.status, r.body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("tunnel 손실 뒤에도 요청이 끝나지 않음")
	}
	eventually(t, "Lifecycle 종료 통지", func() bool { return len(e.lifecyle.all()) == 1 })
	got := e.lifecyle.all()[0]
	// Connector가 이미 아는 종료이므로 PREVIEW_CLOSE를 보낼 필요가 없다.
	if got.Reason != EndTunnelClosed || got.NotifyConnector {
		t.Fatalf("통지 = %+v", got)
	}
	if resp, _ := e.get(s.Expected, cookie, "/"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("tunnel 종료 뒤 요청 status = %d, want 401", resp.StatusCode)
	}
}

// application이 응답 뒤 TCP 연결을 닫으면 tunnel이 끝나고 PreviewSession도 끝난다(MVP: tunnel 하나 = TCP 연결 하나).
func TestApplicationClosingTheConnectionEndsTheSession(t *testing.T) {
	e := newEnv(t)
	s := e.activate()
	cookie := e.login(s)
	e.app.setHandler(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		_, _ = io.WriteString(w, "last response")
	})

	resp, body := e.get(s.Expected, cookie, "/")
	if resp.StatusCode != http.StatusOK || body != "last response" {
		t.Fatalf("첫 응답 = %d %q", resp.StatusCode, body)
	}
	eventually(t, "PreviewSession 종료", func() bool { info, _ := e.gw.Info(s.SessionID); return info.Ended })
	if info, _ := e.gw.Info(s.SessionID); info.EndReason != EndTunnelClosed {
		t.Fatalf("종료 사유 = %q", info.EndReason)
	}
	if resp, _ := e.get(s.Expected, cookie, "/"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("종료 뒤 요청 status = %d, want 401", resp.StatusCode)
	}
}

func TestUpstreamTimeoutMapsTo504(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.UpstreamTimeout = 200 * time.Millisecond })
	s := e.activate()
	cookie := e.login(s)
	e.app.setHandler(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	})

	resp, body := e.get(s.Expected, cookie, "/hang")
	if resp.StatusCode != http.StatusGatewayTimeout || !strings.Contains(body, `"preview_upstream_timeout"`) {
		t.Fatalf("응답 지연 = %d %s", resp.StatusCode, body)
	}
}

func TestProxyErrorClassification(t *testing.T) {
	e := newEnv(t)
	s := e.activate()
	sess := e.gw.lookup(s.SessionID)

	cases := []struct {
		name string
		err  error
		want int
		code string
	}{
		{"tunnel 닫힘", errTunnelClosed, http.StatusBadGateway, "preview_tunnel_closed"},
		{"EOF", io.EOF, http.StatusBadGateway, "preview_tunnel_closed"},
		{"예상치 못한 EOF", io.ErrUnexpectedEOF, http.StatusBadGateway, "preview_tunnel_closed"},
		{"닫힌 pipe", io.ErrClosedPipe, http.StatusBadGateway, "preview_tunnel_closed"},
		{"시간 초과", timeoutError{}, http.StatusGatewayTimeout, "preview_upstream_timeout"},
		{"해석할 수 없는 응답", errors.New("malformed HTTP response \"SECRET-PATH-/private\""), http.StatusBadGateway, "preview_upstream_error"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/private/path?token=SECRET-QUERY", nil)
		e.gw.proxyError(sess, rec, req, tc.err)
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.name, rec.Code, tc.want)
		}
		var problem map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || problem["code"] != tc.code {
			t.Errorf("%s: problem = %s", tc.name, rec.Body.String())
		}
		for _, leak := range []string{"SECRET-PATH", "SECRET-QUERY", "malformed"} {
			if strings.Contains(rec.Body.String(), leak) {
				t.Errorf("%s: 응답에 오류 원문/경로(%s)가 있음: %s", tc.name, leak, rec.Body.String())
			}
		}
	}
	if strings.Contains(e.logs.String(), "SECRET-PATH") || strings.Contains(e.logs.String(), "SECRET-QUERY") {
		t.Errorf("log에 오류 원문/경로가 남음: %s", e.logs.String())
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// ---- PreviewSession lifecycle ----

func TestExplicitTerminateInvalidatesTheCookieAndClosesTheTunnel(t *testing.T) {
	e := newEnv(t)
	s := e.activate()
	cookie := e.login(s)
	if resp, _ := e.get(s.Expected, cookie, "/"); resp.StatusCode != http.StatusOK {
		t.Fatalf("종료 전 요청 status = %d", resp.StatusCode)
	}

	if !e.gw.Terminate(s.SessionID, End{Reason: EndSessionClosed, NotifyConnector: true}) {
		t.Fatal("Terminate()가 false")
	}
	if e.gw.Terminate(s.SessionID, End{Reason: EndSessionClosed, NotifyConnector: true}) {
		t.Fatal("두 번째 Terminate()가 true(멱등이어야 함)")
	}
	if resp, _ := e.get(s.Expected, cookie, "/"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("종료 뒤 Cookie 요청 status = %d, want 401", resp.StatusCode)
	}
	if resp, _ := e.exchange(s.Expected, s.Credential); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("종료 뒤 exchange status = %d, want 401", resp.StatusCode)
	}
	if resp, _ := e.do(s.Expected, http.MethodGet, BootstrapPath, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("종료 뒤 bootstrap status = %d, want 401", resp.StatusCode)
	}
	if code := s.Peer.closeCode(t); code != websocket.CloseNormalClosure {
		t.Fatalf("Connector가 받은 close code = %d, want 1000", code)
	}

	notices := e.lifecyle.all()
	if len(notices) != 1 {
		t.Fatalf("Lifecycle 통지 %d개, want 1개(멱등)", len(notices))
	}
	if n := notices[0]; n.SessionID != s.SessionID || n.Reason != EndSessionClosed || !n.NotifyConnector ||
		n.ConnectorID != e.connectorID || n.LabInstanceID != "lab-1" || n.Generation != 3 || n.RequestID != "request-1" {
		t.Fatalf("통지 = %+v", n)
	}
	info, ok := e.gw.Info(s.SessionID)
	if !ok || !info.Ended || info.EndReason != EndSessionClosed || info.OwnerID != "user-1" {
		t.Fatalf("Info = %+v, %v", info, ok)
	}
}

func TestSessionExpiryInvalidatesAuthenticationAndNotifiesTheConnector(t *testing.T) {
	e := newEnv(t)
	s := e.activate(func(x *Expected) { x.TTL = 10 * time.Minute })
	cookie := e.login(s)

	e.clock.Advance(9 * time.Minute)
	if resp, _ := e.get(s.Expected, cookie, "/"); resp.StatusCode != http.StatusOK {
		t.Fatalf("만료 전 요청 status = %d", resp.StatusCode)
	}
	e.clock.Advance(2 * time.Minute)
	if resp, _ := e.get(s.Expected, cookie, "/"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("만료 뒤 요청 status = %d, want 401", resp.StatusCode)
	}
	notices := e.lifecyle.all()
	if len(notices) != 1 || notices[0].Reason != EndSessionExpired || !notices[0].NotifyConnector {
		t.Fatalf("통지 = %+v", notices)
	}
	if code := s.Peer.closeCode(t); code != websocket.CloseNormalClosure {
		t.Fatalf("close code = %d", code)
	}
}

// TTL은 Activate한 시점부터 흐른다. 생성에 걸린 시간이 TTL을 깎지 않는다.
func TestSessionTTLStartsAtActivation(t *testing.T) {
	e := newEnv(t)
	x := e.expect(func(x *Expected) { x.TTL = 10 * time.Minute })
	e.attachAndServe(x)
	attached, _, _ := e.gw.Pending(x.SessionID)
	<-attached
	e.clock.Advance(time.Hour) // attach 뒤 오래 기다려도 Activate 전에는 만료가 시작되지 않는다.
	act, err := e.gw.Activate(x.SessionID)
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if want := e.clock.Now().Add(10 * time.Minute); !act.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want Activate 시각 + TTL(%v)", act.ExpiresAt, want)
	}
	if !strings.HasPrefix(act.URL, "http://"+x.SessionID+".preview.test"+BootstrapPath+"#") {
		t.Fatalf("URL = %q", act.URL)
	}
	// URL에는 VM IP/port, Provider Server ID, Connector ID가 없다.
	for _, hidden := range []string{x.ProviderServerID, x.ConnectorID.String(), x.TargetVMKey, "5173", x.LabInstanceID} {
		if strings.Contains(act.URL, hidden) {
			t.Errorf("URL이 %q를 노출함: %s", hidden, act.URL)
		}
	}
}

func TestEndedSessionsLeaveATombstoneForTheTTLThenDisappear(t *testing.T) {
	e := newEnv(t)
	s := e.activate(func(x *Expected) { x.TTL = 10 * time.Minute })
	e.gw.Terminate(s.SessionID, End{Reason: EndSessionClosed, NotifyConnector: true})

	info, ok := e.gw.Info(s.SessionID)
	if !ok || !info.Ended {
		t.Fatalf("종료 직후 Info = %+v, %v", info, ok)
	}
	e.clock.Advance(11 * time.Minute)
	if _, ok := e.gw.Info(s.SessionID); ok {
		t.Fatal("보존 기간이 지난 tombstone이 남음")
	}
}

func TestActivateStateMachine(t *testing.T) {
	e := newEnv(t)
	if _, err := e.gw.Activate("no-such-session"); !errors.Is(err, ErrUnknownSession) {
		t.Errorf("알 수 없는 PreviewSession Activate() error = %v", err)
	}
	pending := e.expect()
	if _, err := e.gw.Activate(pending.SessionID); !errors.Is(err, ErrNotAttached) {
		t.Errorf("attach 전 Activate() error = %v, want ErrNotAttached", err)
	}
	s := e.activate()
	if _, err := e.gw.Activate(s.SessionID); !errors.Is(err, ErrAlreadyActive) {
		t.Errorf("두 번째 Activate() error = %v, want ErrAlreadyActive", err)
	}
	e.gw.Terminate(s.SessionID, End{Reason: EndSessionClosed})
	if _, err := e.gw.Activate(s.SessionID); !errors.Is(err, ErrSessionEnded) {
		t.Errorf("종료 뒤 Activate() error = %v, want ErrSessionEnded", err)
	}
	// 끝난 PreviewSession에는 Bind도 할 수 없다.
	if err := e.gw.Bind(s.SessionID, Binding{ConnectorID: e.connectorID}); !errors.Is(err, ErrSessionEnded) {
		t.Errorf("종료 뒤 Bind() error = %v", err)
	}
}

func TestExpectValidatesItsInput(t *testing.T) {
	e := newEnv(t)
	valid := e.expected()
	cases := map[string]func(*Expected){
		"SessionID 없음":         func(x *Expected) { x.SessionID = "" },
		"SessionID가 label이 아님": func(x *Expected) { x.SessionID = "A.B" },
		"OwnerID 없음":           func(x *Expected) { x.OwnerID = "" },
		"OrganizationID 없음":    func(x *Expected) { x.OrganizationID = "" },
		"ConnectorID 없음":       func(x *Expected) { x.ConnectorID = uuid.Nil },
		"LabInstanceID 없음":     func(x *Expected) { x.LabInstanceID = "" },
		"generation 0":         func(x *Expected) { x.Generation = 0 },
		"targetVmKey 없음":       func(x *Expected) { x.TargetVMKey = "" },
		"providerServerId 없음":  func(x *Expected) { x.ProviderServerID = "" },
		"port 0":               func(x *Expected) { x.TargetPort = 0 },
		"port 65536":           func(x *Expected) { x.TargetPort = 65536 },
		"TTL 0":                func(x *Expected) { x.TTL = 0 },
		"TTL 음수":               func(x *Expected) { x.TTL = -time.Second },
	}
	for name, mutate := range cases {
		x := valid
		x.SessionID = uuid.NewString()
		mutate(&x)
		if err := e.gw.Expect(x); !errors.Is(err, ErrInvalidSession) {
			t.Errorf("%s: Expect() error = %v, want ErrInvalidSession", name, err)
		}
	}
	if err := e.gw.Expect(valid); err != nil {
		t.Fatal(err)
	}
	if err := e.gw.Expect(valid); !errors.Is(err, ErrDuplicateSession) {
		t.Errorf("중복 Expect() error = %v, want ErrDuplicateSession", err)
	}
	e.gw.Close()
	again := valid
	again.SessionID = uuid.NewString()
	if err := e.gw.Expect(again); !errors.Is(err, ErrClosed) {
		t.Errorf("종료 뒤 Expect() error = %v, want ErrClosed", err)
	}
}

func TestSessionsForLabListsOnlyLiveSessionsOfThatLab(t *testing.T) {
	e := newEnv(t)
	a := e.activate()
	b := e.activate()
	c := e.activate(func(x *Expected) { x.LabInstanceID = "lab-2" })
	e.gw.Terminate(b.SessionID, End{Reason: EndSessionClosed})

	got := e.gw.SessionsForLab("lab-1")
	if len(got) != 1 || got[0] != a.SessionID {
		t.Fatalf("SessionsForLab(lab-1) = %v, want [%s]", got, a.SessionID)
	}
	if got := e.gw.SessionsForLab("lab-2"); len(got) != 1 || got[0] != c.SessionID {
		t.Fatalf("SessionsForLab(lab-2) = %v", got)
	}
	if got := e.gw.SessionsForLab("lab-9"); len(got) != 0 {
		t.Fatalf("SessionsForLab(lab-9) = %v", got)
	}
}

// 활성화하지 않은 PreviewSession을 Forget하면 attach된 Data WSS도 닫고 흔적을 남기지 않는다.
func TestForgetClosesAnAttachedButInactiveTunnel(t *testing.T) {
	e := newEnv(t)
	x := e.expect()
	peer := e.attachAndServe(x)
	attached, _, _ := e.gw.Pending(x.SessionID)
	<-attached

	e.gw.Forget(x.SessionID)
	if code := peer.closeCode(t); code != websocket.CloseNormalClosure {
		t.Fatalf("close code = %d", code)
	}
	if _, ok := e.gw.Info(x.SessionID); ok {
		t.Fatal("Forget한 PreviewSession이 남음")
	}
	if got := e.lifecyle.all(); len(got) != 0 {
		t.Fatalf("Lifecycle 통지 = %+v, want 없음", got)
	}
	e.gw.Forget(x.SessionID) // 없어도 안전하다.
}

// ---- trust: Credential revoke, Control Session 교체, 종료 ----

func TestCredentialRevokeEndsBoundSessionsAndClosesTheirTunnelsWith4001(t *testing.T) {
	e := newEnv(t)
	active := e.activate()
	pending := e.expect() // attach를 기다리는 PreviewSession도 정리된다.
	_, pendingEnded, _ := e.gw.Pending(pending.SessionID)

	otherCredential := uuid.New()
	survivor := e.expected()
	if err := e.gw.Expect(survivor); err != nil {
		t.Fatal(err)
	}
	if err := e.gw.Bind(survivor.SessionID, Binding{ConnectorID: e.connectorID, ControlSessionID: uuid.New(), CredentialID: otherCredential}); err != nil {
		t.Fatal(err)
	}

	e.gw.CredentialRevoked(e.credentialID)

	if code := active.Peer.closeCode(t); code != 4001 {
		t.Fatalf("revoke된 tunnel close code = %d, want 4001", code)
	}
	select {
	case <-pendingEnded:
	case <-time.After(5 * time.Second):
		t.Fatal("attach를 기다리던 PreviewSession이 정리되지 않음")
	}
	for _, id := range []string{active.SessionID, pending.SessionID} {
		info, ok := e.gw.Info(id)
		if ok && !info.Ended {
			t.Errorf("%s가 끝나지 않음", id)
		}
	}
	if resp, _ := e.get(active.Expected, nil, "/"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("revoke 뒤 요청 status = %d", resp.StatusCode)
	}
	// 다른 Credential에 묶인 PreviewSession은 영향을 받지 않는다.
	if info, ok := e.gw.Info(survivor.SessionID); !ok || info.Ended {
		t.Errorf("다른 Credential의 PreviewSession이 끝남: %+v", info)
	}

	notices := e.lifecyle.all()
	if len(notices) != 1 || notices[0].SessionID != active.SessionID || notices[0].Reason != EndCredentialRevoked || notices[0].NotifyConnector {
		t.Fatalf("Lifecycle 통지 = %+v (활성 PreviewSession 하나, Connector에 알리지 않음)", notices)
	}

	// revoke된 Credential이 새 PreviewSession을 완료시킬 수도 없다.
	x := e.expect()
	e.gw.CredentialRevoked(e.credentialID)
	if _, ended, ok := e.gw.Pending(x.SessionID); ok {
		select {
		case <-ended:
		case <-time.After(5 * time.Second):
			t.Fatal("새 PreviewSession이 정리되지 않음")
		}
	}
}

func TestConnectorRevokeEndsEveryTunnelOfThatConnector(t *testing.T) {
	e := newEnv(t)
	a := e.activate()
	b := e.activate()
	e.gw.ConnectorRevoked(e.connectorID)
	for _, p := range []*connector{a.Peer, b.Peer} {
		if code := p.closeCode(t); code != 4001 {
			t.Errorf("close code = %d, want 4001", code)
		}
	}
	e.gw.ConnectorRevoked(uuid.New()) // 다른 Connector는 영향이 없고 오류도 아니다.
}

// Control Session이 교체되면 이전 Control Session으로 PREVIEW_OPEN을 보낸 PreviewSession의 trust가 끝난다.
// Connector ID가 같다는 이유로 이전 Data WSS를 다시 신뢰하지 않는다.
func TestControlSessionReplacementEndsTheSessionsBoundToTheOldControlSession(t *testing.T) {
	e := newEnv(t)
	old := e.activate() // e.controlID에 묶임
	oldPending := e.expect()
	_, oldPendingEnded, _ := e.gw.Pending(oldPending.SessionID)

	// 새 Control Session에 묶인 PreviewSession이다. 이전 Control Session의 교체는 이것에 영향이 없다.
	newControl := uuid.New()
	fresh := e.expected()
	if err := e.gw.Expect(fresh); err != nil {
		t.Fatal(err)
	}
	if err := e.gw.Bind(fresh.SessionID, Binding{ConnectorID: e.connectorID, ControlSessionID: newControl, CredentialID: e.credentialID}); err != nil {
		t.Fatal(err)
	}

	e.gw.ControlSessionEnded(e.controlID, true)

	if code := old.Peer.closeCode(t); code != 4002 {
		t.Fatalf("교체된 Control Session의 tunnel close code = %d, want 4002", code)
	}
	select {
	case <-oldPendingEnded:
	case <-time.After(5 * time.Second):
		t.Fatal("이전 Control Session에서 attach를 기다리던 PreviewSession이 정리되지 않음")
	}
	if info, ok := e.gw.Info(fresh.SessionID); !ok || info.Ended {
		t.Fatalf("새 Control Session의 PreviewSession이 끝남: %+v", info)
	}
	notices := e.lifecyle.all()
	if len(notices) != 1 || notices[0].Reason != EndControlReplaced || notices[0].NotifyConnector || notices[0].ControlSessionID != e.controlID {
		t.Fatalf("통지 = %+v", notices)
	}

	// 새 Control Session의 PreviewSession은 새 Data WSS로 정상 attach한다.
	e.attachAndServe(fresh)
	attached, _, _ := e.gw.Pending(fresh.SessionID)
	select {
	case <-attached:
	case <-time.After(5 * time.Second):
		t.Fatal("새 Control Session의 PreviewSession이 attach되지 않음")
	}
}

func TestControlSessionRevokeClosesTunnelsWith4001(t *testing.T) {
	e := newEnv(t)
	s := e.activate()
	e.gw.ControlSessionEnded(e.controlID, false)
	if code := s.Peer.closeCode(t); code != 4001 {
		t.Fatalf("close code = %d, want 4001", code)
	}
	e.gw.ControlSessionEnded(uuid.New(), true) // 알 수 없는 Control Session은 아무것도 하지 않는다.
	if got := e.lifecyle.all(); len(got) != 1 || got[0].Reason != EndCredentialRevoked {
		t.Fatalf("통지 = %+v", got)
	}
}

func TestShutdownEndsEverySessionAndRejectsNewWork(t *testing.T) {
	e := newEnv(t)
	a := e.activate()
	cookie := e.login(a)
	b := e.activate()
	pending := e.expect()
	_, pendingEnded, _ := e.gw.Pending(pending.SessionID)

	e.gw.Close()
	e.gw.Close() // 여러 번 호출해도 안전하다.

	for _, p := range []*connector{a.Peer, b.Peer} {
		if code := p.closeCode(t); code != websocket.CloseGoingAway {
			t.Errorf("close code = %d, want 1001", code)
		}
	}
	select {
	case <-pendingEnded:
	default:
		t.Error("pending PreviewSession이 끝나지 않음")
	}
	notices := e.lifecyle.all()
	if len(notices) != 2 {
		t.Fatalf("Lifecycle 통지 %d개, want 2개(활성 PreviewSession만)", len(notices))
	}
	for _, n := range notices {
		if n.Reason != EndServiceRestarting || !n.NotifyConnector {
			t.Errorf("통지 = %+v", n)
		}
	}
	resp, body := e.get(a.Expected, cookie, "/")
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, `"preview_unavailable"`) {
		t.Errorf("종료 중 요청 = %d %s", resp.StatusCode, body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.gw.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

// ---- 민감정보 ----

// 경로, query, header, Cookie, 본문은 log에 남지 않는다. Preview 본문은 tunnel에만 있다.
func TestPreviewContentNeverReachesLogsOrLifecycle(t *testing.T) {
	e := newEnv(t)
	s := e.activate()
	cookie := e.login(s)

	const (
		pathMarker   = "PATH-MARKER-3f9a"
		queryMarker  = "QUERY-MARKER-71d0"
		headerMarker = "HEADER-MARKER-b2c8"
		cookieMark   = "APP-COOKIE-MARKER-44e1"
		reqBodyMark  = "REQUEST-BODY-MARKER-9e07"
		respBodyMark = "RESPONSE-BODY-MARKER-58ab"
		authMarker   = "AUTH-MARKER-c6d3"
	)
	e.app.setHandler(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("X-Marker", respBodyMark)
		_, _ = io.WriteString(w, respBodyMark)
	})
	resp, body := e.do(s.Expected, http.MethodPost, "/"+pathMarker+"?q="+queryMarker, reqBodyMark, func(r *http.Request) {
		r.AddCookie(cookie)
		r.AddCookie(&http.Cookie{Name: "app", Value: cookieMark})
		r.Header.Set("X-Custom", headerMarker)
		r.Header.Set("Authorization", "Bearer "+authMarker)
	})
	if resp.StatusCode != http.StatusOK || body != respBodyMark {
		t.Fatalf("응답 = %d %q", resp.StatusCode, body)
	}
	// 실패 경로도 같다.
	e.get(s.Expected, nil, "/"+pathMarker+"?q="+queryMarker)
	e.get(s.Expected, &http.Cookie{Name: cookie.Name, Value: strings.Repeat("Z", 43)}, "/"+pathMarker)
	e.gw.Terminate(s.SessionID, End{Reason: EndSessionClosed, NotifyConnector: true})

	logs := e.logs.String()
	for _, marker := range []string{pathMarker, queryMarker, headerMarker, cookieMark, reqBodyMark, respBodyMark, authMarker, cookie.Value, s.Credential} {
		if strings.Contains(logs, marker) {
			t.Errorf("log에 %q가 남음", marker)
		}
	}
	if strings.Contains(fmt.Sprintf("%+v", e.lifecyle.all()), pathMarker) {
		t.Error("Lifecycle 통지에 경로가 있음")
	}
	// 상관관계 정보는 남는다.
	for _, want := range []string{"preview_session_id", s.SessionID, "request_id"} {
		if !strings.Contains(logs, want) {
			t.Errorf("log에 %q가 없음", want)
		}
	}
}
