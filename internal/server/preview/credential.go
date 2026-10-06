package preview

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
	"time"
)

// tokenBytes는 bootstrap credential과 Preview Cookie token의 CSPRNG byte 수다(openapi.yaml previewCookie: 32 bytes).
const tokenBytes = 32

// tokenLength는 padding 없는 canonical Base64URL로 표현한 token의 문자 수다.
var tokenLength = base64.RawURLEncoding.EncodedLen(tokenBytes)

// newToken은 CSPRNG가 만든 token 원문과 그 SHA-256 digest를 반환한다. 원문은 Browser에 한 번만 전달하며 서버에는 digest만 남긴다.
func newToken() (raw string, digest [sha256.Size]byte) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		// 운영체제 CSPRNG를 읽지 못하면 안전한 token을 만들 수 없다. 약한 값으로 대체하지 않는다.
		panic("preview: CSPRNG를 읽지 못함: " + err.Error())
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, sha256.Sum256([]byte(raw))
}

// tokenDigest는 제시된 token의 digest다. 형식(길이, canonical Base64URL)이 틀리면 ok가 false다. 형식은 비밀이 아니므로 먼저 거른다.
// 같은 길이의 digest끼리의 비교는 호출자가 constant time으로 한다.
func tokenDigest(presented string) (digest [sha256.Size]byte, ok bool) {
	if len(presented) != tokenLength {
		return digest, false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(presented)
	if err != nil || len(decoded) != tokenBytes {
		return digest, false
	}
	return sha256.Sum256([]byte(presented)), true
}

// Preview Cookie 이름이다(openapi.yaml previewCookie). HTTPS Preview Origin에서는 `__Host-` prefix로 Secure·Path=/·Domain 없음을 브라우저가 강제한다.
// 개발용 HTTP Preview Origin에서는 `__Host-` prefix를 쓸 수 없다.
const (
	secureCookieName = "__Host-labbit-preview"
	plainCookieName  = "labbit-preview"
)

// Workspace application으로 전달하지 않는 Labbit 소유 Cookie 이름이다. Preview Cookie와 로그인 Session Cookie(둘 다 prefix 유무)다.
var labbitCookieNames = map[string]struct{}{
	secureCookieName:        {},
	plainCookieName:         {},
	"__Host-labbit-session": {},
	"labbit-session":        {},
}

func (g *Gateway) cookieName() string {
	if g.origin.Secure() {
		return secureCookieName
	}
	return plainCookieName
}

// newCookie는 Preview 전용 Cookie다. HttpOnly, Path=/, Domain 없음(host-only), SameSite=Lax이고 HTTPS Preview Origin에서는 Secure다.
// 만료는 PreviewSession의 만료 시각이며 서버가 PreviewSession의 상태를 매 요청마다 확인한다.
func (g *Gateway) newCookie(token string, expiresAt, now time.Time) *http.Cookie {
	maxAge := int(expiresAt.Sub(now).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	return &http.Cookie{
		Name:     g.cookieName(),
		Value:    token,
		Path:     "/",
		Expires:  expiresAt.UTC(),
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   g.origin.Secure(),
		SameSite: http.SameSiteLaxMode,
	}
}

// presentedCookie는 요청이 제시한 Preview Cookie token이다. 없거나 둘 이상이면 false다.
func (g *Gateway) presentedCookie(r *http.Request) (string, bool) {
	name := g.cookieName()
	var value string
	count := 0
	for _, c := range r.Cookies() {
		if c.Name == name {
			value = c.Value
			count++
		}
	}
	return value, count == 1 && value != ""
}

// stripLabbitCookies는 Cookie header에서 Labbit 소유 Cookie(Preview Cookie, 로그인 Session Cookie)를 제거한다. Workspace application이 설정한
// 그 밖의 Cookie는 값을 바꾸지 않고 그대로 둔다. 남는 Cookie가 없으면 header를 지운다.
func stripLabbitCookies(h http.Header) {
	values := h.Values("Cookie")
	if len(values) == 0 {
		return
	}
	var kept []string
	for _, line := range values {
		for _, pair := range strings.Split(line, ";") {
			pair = strings.TrimSpace(pair)
			if pair == "" {
				continue
			}
			name, _, _ := strings.Cut(pair, "=")
			if _, owned := labbitCookieNames[strings.TrimSpace(name)]; owned {
				continue
			}
			kept = append(kept, pair)
		}
	}
	if len(kept) == 0 {
		h.Del("Cookie")
		return
	}
	h.Set("Cookie", strings.Join(kept, "; "))
}

// sanitizeSetCookies는 Workspace application의 Set-Cookie를 PreviewSession마다 host-only로 유지하도록 고친다.
// Domain attribute를 제거하고(상위 domain Cookie로 다른 PreviewSession Origin이나 본 서비스에 영향을 주지 못하게 한다),
// Labbit 소유 Cookie 이름을 덮어쓰려는 Set-Cookie는 버린다.
func sanitizeSetCookies(h http.Header) {
	values := h.Values("Set-Cookie")
	if len(values) == 0 {
		return
	}
	h.Del("Set-Cookie")
	for _, line := range values {
		parts := strings.Split(line, ";")
		name, _, _ := strings.Cut(strings.TrimSpace(parts[0]), "=")
		if _, owned := labbitCookieNames[strings.TrimSpace(name)]; owned {
			continue
		}
		kept := parts[:1]
		for _, attr := range parts[1:] {
			key, _, _ := strings.Cut(strings.TrimSpace(attr), "=")
			if strings.EqualFold(strings.TrimSpace(key), "domain") {
				continue
			}
			kept = append(kept, attr)
		}
		h.Add("Set-Cookie", strings.Join(kept, ";"))
	}
}
