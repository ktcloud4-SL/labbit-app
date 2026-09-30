package httpapi

import (
	"net/http"

	"github.com/ktcloud4-SL/labbit-app/internal/server/auth"
)

// sessionCookieName은 OpenAPI sessionCookie security scheme의 Cookie 이름이다.
const sessionCookieName = "__Host-labbit-session"

// newSessionCookie는 production 및 production-like HTTPS 계약의 Cookie다.
// Secure, HttpOnly, SameSite=Lax, Path=/, Domain 없음, Max-Age/Expires 없음(non-persistent).
// 개발 편의를 위해 속성을 약화하는 설정은 두지 않는다.
func newSessionCookie(token auth.SessionToken) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    string(token),
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

// clearedSessionCookie는 같은 scope에서 Cookie를 비우고 Max-Age=0을 적용한다.
func clearedSessionCookie() *http.Cookie {
	cookie := newSessionCookie("")
	cookie.MaxAge = -1 // net/http는 음수를 "Max-Age=0"으로 기록한다.
	return cookie
}

// presentedToken은 요청이 제시한 Session Cookie 값이다. 없거나 비어 있으면 false다.
func presentedToken(r *http.Request) (auth.SessionToken, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return "", false
	}
	return auth.SessionToken(cookie.Value), true
}
