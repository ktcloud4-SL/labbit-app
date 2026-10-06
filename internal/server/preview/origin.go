package preview

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// sessionIDPlaceholder는 LABBIT_PREVIEW_ORIGIN_TEMPLATE에서 PreviewSession ID로 치환되는 자리다.
const sessionIDPlaceholder = "{sessionId}"

// OriginTemplate은 Preview Origin의 형식이다. host의 첫 label이 PreviewSession ID이고 나머지 host(와 선택적 port)는 고정이다.
// 예: https://{sessionId}.preview.example.com (예시이며 실제 hostname은 Platform이 정한다).
//
// SaaS 본 서비스 Origin과 PreviewSession마다 host가 다르므로 Cookie와 storage가 PreviewSession끼리, 그리고 본 서비스와 격리된다.
type OriginTemplate struct {
	scheme string
	// suffix는 첫 label 다음의 host이며 기본 port가 아니면 ":port"를 포함한다. 소문자다.
	suffix string
}

// IsZero는 template이 설정되지 않았음이다.
func (o OriginTemplate) IsZero() bool { return o.scheme == "" }

// Secure는 Preview Origin이 HTTPS인지다. Preview Cookie의 Secure와 이름을 정한다.
func (o OriginTemplate) Secure() bool { return o.scheme == "https" }

// Scheme은 "http" 또는 "https"다.
func (o OriginTemplate) Scheme() string { return o.scheme }

// String은 정규화한 template이다. 형식 확인용이며 비교 기준이다.
func (o OriginTemplate) String() string {
	if o.IsZero() {
		return ""
	}
	return o.scheme + "://" + sessionIDPlaceholder + "." + o.suffix
}

// Origin은 PreviewSession id의 Preview Origin("scheme://id.suffix")이다. Origin header와 정확히 비교할 수 있는 정규화 형태다.
func (o OriginTemplate) Origin(id string) string {
	return o.scheme + "://" + id + "." + o.suffix
}

// SessionID는 request Host header(Origin의 host[:port])가 이 template에 일치하면 PreviewSession ID를 반환한다.
// 첫 label은 DNS label이어야 하며 나머지 host는 정확히 일치해야 한다. 이 규칙이 Gateway로 갈 요청과 SaaS 본 서비스로 갈 요청을 가른다.
func (o OriginTemplate) SessionID(host string) (string, bool) {
	if o.IsZero() {
		return "", false
	}
	host = strings.ToLower(host)
	// Browser는 기본 port를 Host에 싣지 않지만 Proxy가 싣는 경우도 같은 Origin이다.
	if o.scheme == "https" && strings.HasSuffix(host, ":443") && !strings.HasSuffix(o.suffix, ":443") {
		host = strings.TrimSuffix(host, ":443")
	}
	if o.scheme == "http" && strings.HasSuffix(host, ":80") && !strings.HasSuffix(o.suffix, ":80") {
		host = strings.TrimSuffix(host, ":80")
	}
	id, ok := strings.CutSuffix(host, "."+o.suffix)
	if !ok || !validLabel(id) {
		return "", false
	}
	return id, true
}

// MatchesHost는 SessionID가 성공하는 host인지다.
func (o OriginTemplate) MatchesHost(host string) bool {
	_, ok := o.SessionID(host)
	return ok
}

// ConflictsWith는 SaaS 본 서비스 Origin(LABBIT_PUBLIC_ORIGIN, scheme://host[:port])의 host가 이 template에 일치해 본 서비스 요청이
// Preview Gateway로 가버리는지다. 본 서비스 Origin과 Preview Origin이 같으면 사용자 코드가 본 서비스 Origin에서 실행되므로 허용하지 않는다.
func (o OriginTemplate) ConflictsWith(publicOrigin string) bool {
	_, host, found := strings.Cut(publicOrigin, "://")
	if !found {
		return false
	}
	return o.MatchesHost(host)
}

var errInvalidTemplate = errors.New("절대 origin URL template(scheme + host + 선택적 port, path/query/fragment 없음)이며 host의 첫 label이 {sessionId}여야 합니다")

// ParseOriginTemplate은 LABBIT_PREVIEW_ORIGIN_TEMPLATE을 검증하고 정규화한다.
//
//   - http/https만 허용한다. requireHTTPS이면(production) https만 허용한다.
//   - {sessionId}는 host의 첫 label로 정확히 한 번 있어야 한다. scheme, port, path 등 다른 곳에 있으면 거절한다.
//   - path/query/fragment/userinfo를 허용하지 않는다.
//   - 나머지 host는 DNS 이름이어야 하며 IP literal은 거절한다(PreviewSession마다 host를 나눌 수 없다).
func ParseOriginTemplate(raw string, requireHTTPS bool) (OriginTemplate, error) {
	raw = strings.TrimSpace(raw)
	if strings.Count(raw, sessionIDPlaceholder) != 1 {
		return OriginTemplate{}, errInvalidTemplate
	}
	scheme, rest, found := strings.Cut(raw, "://")
	if !found {
		return OriginTemplate{}, errInvalidTemplate
	}
	scheme = strings.ToLower(scheme)
	if scheme != "http" && scheme != "https" {
		return OriginTemplate{}, errInvalidTemplate
	}
	if requireHTTPS && scheme != "https" {
		return OriginTemplate{}, errors.New("production에서는 https여야 합니다")
	}
	hostPort, ok := strings.CutPrefix(rest, sessionIDPlaceholder+".")
	if !ok || hostPort == "" || strings.ContainsAny(hostPort, "/?#@{}") {
		return OriginTemplate{}, errInvalidTemplate
	}

	u, err := url.Parse(scheme + "://" + hostPort)
	if err != nil || u.User != nil || u.Path != "" || u.Hostname() == "" {
		return OriginTemplate{}, errInvalidTemplate
	}
	host := strings.ToLower(u.Hostname())
	if net.ParseIP(host) != nil || strings.Contains(host, ":") || !validHostSuffix(host) {
		return OriginTemplate{}, errInvalidTemplate
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil || n == 0 {
			return OriginTemplate{}, errInvalidTemplate
		}
		port = strconv.FormatUint(n, 10)
	}
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	suffix := host
	if port != "" {
		suffix = net.JoinHostPort(host, port)
	}
	return OriginTemplate{scheme: scheme, suffix: suffix}, nil
}

// validHostSuffix는 host가 비어 있지 않은 DNS label들의 연결인지다.
func validHostSuffix(host string) bool {
	for _, label := range strings.Split(host, ".") {
		if !validLabel(label) {
			return false
		}
	}
	return true
}

// validLabel은 소문자 영숫자와 '-'로 이루어지고 '-'로 시작하거나 끝나지 않는 63자 이하의 DNS label인지다.
// PreviewSession ID가 host의 첫 label이므로 이 형식이어야 한다(canonical UUID 문자열이 이를 만족한다).
func validLabel(s string) bool {
	if s == "" || len(s) > 63 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}
