package preview

import (
	"crypto/sha256"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNewTokenIsRandomAndDigestOnly(t *testing.T) {
	seen := map[string]bool{}
	for range 64 {
		raw, digest := newToken()
		if len(raw) != 43 || strings.ContainsAny(raw, "+/=") {
			t.Fatalf("token = %q, want padding 없는 Base64URL 43자(32 bytes)", raw)
		}
		if seen[raw] {
			t.Fatalf("token이 중복됨: %q", raw)
		}
		seen[raw] = true
		if sha256.Sum256([]byte(raw)) != digest {
			t.Fatal("digest가 token의 SHA-256이 아님")
		}
		if got, ok := tokenDigest(raw); !ok || got != digest {
			t.Fatalf("tokenDigest = %v, %v", got, ok)
		}
	}
}

func TestTokenDigestRejectsMalformedTokens(t *testing.T) {
	raw, _ := newToken()

	// 32 bytes는 43자로 표현되고 마지막 문자는 하위 2 bit가 0이어야 canonical이다. 하위 bit가 다른 문자로 바꾸면 같은 byte를 다른 문자열로
	// 표현한 non-canonical token이 된다. 하나의 token이 여러 표기로 통하지 않게 거절해야 한다.
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	index := strings.IndexByte(alphabet, raw[len(raw)-1])
	if index < 0 || index%4 != 0 {
		t.Fatalf("마지막 문자 %q가 canonical이 아님", raw[len(raw)-1:])
	}
	nonCanonical := raw[:len(raw)-1] + string(alphabet[index+1])

	for name, bad := range map[string]string{
		"빈 문자열":            "",
		"너무 짧음":            raw[:42],
		"너무 김":             raw + "A",
		"표준 Base64 문자":     raw[:42] + "+",
		"padding":          raw[:42] + "=",
		"공백":               " " + raw[1:],
		"non-canonical 표기": nonCanonical,
	} {
		if _, ok := tokenDigest(bad); ok {
			t.Errorf("%s: tokenDigest(%q)가 통과함", name, bad)
		}
	}
	if _, ok := tokenDigest(raw); !ok {
		t.Fatal("정상 token을 거절함")
	}
}

func TestStripLabbitCookiesKeepsApplicationCookies(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string // 빈 문자열이면 header가 없어야 함
	}{
		{"Labbit Cookie만", []string{"__Host-labbit-preview=secret"}, ""},
		{"둘 다 Labbit Cookie", []string{"__Host-labbit-preview=a; __Host-labbit-session=b"}, ""},
		{"application Cookie와 섞임", []string{"theme=dark; __Host-labbit-preview=secret; lang=ko"}, "theme=dark; lang=ko"},
		{"prefix 없는 개발용 이름", []string{"labbit-preview=a; labbit-session=b; app=1"}, "app=1"},
		{"여러 header", []string{"a=1", "__Host-labbit-session=s; b=2"}, "a=1; b=2"},
		{"이름만 비슷한 application Cookie", []string{"labbit-preview-x=1; my-labbit-session=2"}, "labbit-preview-x=1; my-labbit-session=2"},
		{"Cookie 없음", nil, ""},
	}
	for _, tc := range cases {
		h := http.Header{}
		for _, v := range tc.in {
			h.Add("Cookie", v)
		}
		stripLabbitCookies(h)
		if got := h.Get("Cookie"); got != tc.want {
			t.Errorf("%s: Cookie = %q, want %q", tc.name, got, tc.want)
		}
		if tc.want == "" && len(h.Values("Cookie")) != 0 {
			t.Errorf("%s: 남는 Cookie가 없는데 header가 남음", tc.name)
		}
	}
}

func TestSanitizeSetCookiesKeepsCookiesHostOnly(t *testing.T) {
	h := http.Header{}
	h.Add("Set-Cookie", "session=abc; Path=/; Domain=.preview.example.com; HttpOnly")
	h.Add("Set-Cookie", "theme=dark; domain=example.com; Secure")
	h.Add("Set-Cookie", "plain=1; Path=/app")
	h.Add("Set-Cookie", "__Host-labbit-preview=evil; Path=/")
	h.Add("Set-Cookie", "labbit-session=evil; Path=/")
	sanitizeSetCookies(h)

	got := h.Values("Set-Cookie")
	if len(got) != 3 {
		t.Fatalf("Set-Cookie = %q, want 3개(Labbit 소유 이름을 덮어쓰는 2개는 제거)", got)
	}
	for _, line := range got {
		if strings.Contains(strings.ToLower(line), "domain") {
			t.Errorf("Domain attribute가 남음: %q", line)
		}
		if strings.Contains(line, "labbit") {
			t.Errorf("Labbit 소유 Cookie 이름이 남음: %q", line)
		}
	}
	if !strings.Contains(got[0], "HttpOnly") || !strings.Contains(got[0], "Path=/") || !strings.HasPrefix(got[0], "session=abc") {
		t.Errorf("다른 attribute를 바꿈: %q", got[0])
	}
	if !strings.Contains(got[1], "Secure") || !strings.HasPrefix(got[1], "theme=dark") {
		t.Errorf("다른 attribute를 바꿈: %q", got[1])
	}
}

// Preview Cookie 속성은 계약(openapi.yaml previewCookie)이다.
func TestPreviewCookieAttributes(t *testing.T) {
	now := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	expires := now.Add(30 * time.Minute)
	token, _ := newToken()

	secure, _ := ParseOriginTemplate("https://{sessionId}.preview.example.com", true)
	gw := &Gateway{origin: secure}
	c := gw.newCookie(token, expires, now)
	if c.Name != "__Host-labbit-preview" || c.Value != token || c.Path != "/" || c.Domain != "" || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("HTTPS cookie = %+v", c)
	}
	if !c.Expires.Equal(expires) || c.MaxAge != 1800 {
		t.Fatalf("만료 = %v / MaxAge %d, want PreviewSession 만료 시각", c.Expires, c.MaxAge)
	}
	line := c.String()
	for _, want := range []string{"HttpOnly", "Secure", "Path=/", "SameSite=Lax"} {
		if !strings.Contains(line, want) {
			t.Errorf("Set-Cookie %q에 %q가 없음", line, want)
		}
	}
	if strings.Contains(strings.ToLower(line), "domain") {
		t.Errorf("Domain attribute가 있음: %q", line)
	}

	plain, _ := ParseOriginTemplate("http://{sessionId}.localhost:8080", false)
	gw = &Gateway{origin: plain}
	c = gw.newCookie(token, expires, now)
	if c.Name != "labbit-preview" || c.Secure || !c.HttpOnly || c.Path != "/" || c.Domain != "" {
		t.Fatalf("개발용 HTTP cookie = %+v", c)
	}

	// 이미 만료 시각이 지난 값도 Max-Age가 0 이하(즉시 삭제)가 되지 않는다. 서버가 상태로 판정한다.
	if c := gw.newCookie(token, now.Add(-time.Hour), now); c.MaxAge < 1 {
		t.Fatalf("MaxAge = %d", c.MaxAge)
	}
}

func TestPresentedCookieNeedsExactlyOneValue(t *testing.T) {
	plain, _ := ParseOriginTemplate("http://{sessionId}.localhost:8080", false)
	gw := &Gateway{origin: plain}
	mk := func(cookies ...*http.Cookie) *http.Request {
		r := &http.Request{Header: http.Header{}, URL: &url.URL{}}
		for _, c := range cookies {
			r.AddCookie(c)
		}
		return r
	}
	if v, ok := gw.presentedCookie(mk(&http.Cookie{Name: "labbit-preview", Value: "tok"})); !ok || v != "tok" {
		t.Fatalf("정상 Cookie = %q, %v", v, ok)
	}
	if _, ok := gw.presentedCookie(mk()); ok {
		t.Fatal("Cookie 없음을 통과시킴")
	}
	if _, ok := gw.presentedCookie(mk(&http.Cookie{Name: "labbit-preview", Value: ""})); ok {
		t.Fatal("빈 Cookie를 통과시킴")
	}
	if _, ok := gw.presentedCookie(mk(&http.Cookie{Name: "labbit-preview", Value: "a"}, &http.Cookie{Name: "labbit-preview", Value: "b"})); ok {
		t.Fatal("같은 이름 Cookie가 둘인데 통과시킴(cookie tossing)")
	}
	if _, ok := gw.presentedCookie(mk(&http.Cookie{Name: "__Host-labbit-preview", Value: "tok"})); ok {
		t.Fatal("다른 환경의 이름을 통과시킴")
	}
}
