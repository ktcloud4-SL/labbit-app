package httpapi

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// ParseOrigin은 LABBIT_PUBLIC_ORIGIN 값을 검증하고 비교용 형태("scheme://host[:port]")로 정규화한다.
// 형식은 runtime/contract.yaml을 따른다: http/https, host, 선택적 port만 허용하고 path/query/fragment는 허용하지 않는다.
func ParseOrigin(raw string) (string, error) {
	origin, err := normalizeOrigin(raw, false)
	if err != nil {
		return "", errors.New("절대 origin URL(scheme + host + 선택적 port, path/query/fragment 없음)이어야 합니다")
	}
	return origin, nil
}

// OriginMatches는 Browser WebSocket Upgrade의 Origin header 값 하나가 trusted origin(ParseOrigin 형식)과 정확히 일치하는지 판정한다.
// 같은 정규화를 쓰며 Referer로 대체하지 않는다. path, query, fragment가 있거나 malformed/null인 값은 일치하지 않는다.
func OriginMatches(trusted, origin string) bool {
	return matchesOrigin(trusted, origin, false)
}

// sourceAllowed는 unsafe method 요청의 source가 trusted origin과 정확히 일치하는지 판정한다.
//
//  1. Origin header가 있으면 그 값만 사용한다. 있는데 비교에 실패하면 Referer로 넘어가지 않는다.
//  2. Origin이 없으면 Referer의 origin을 사용한다.
//  3. 둘 다 없거나 malformed/null/중복이면 거절한다.
//
// request Host / X-Forwarded-Host는 읽지 않는다. trusted origin은 설정에서만 온다.
func sourceAllowed(trusted string, header http.Header) bool {
	if values := header.Values("Origin"); len(values) > 0 {
		return len(values) == 1 && matchesOrigin(trusted, values[0], false)
	}
	if values := header.Values("Referer"); len(values) > 0 {
		return len(values) == 1 && matchesOrigin(trusted, values[0], true)
	}
	return false
}

func matchesOrigin(trusted, raw string, allowPath bool) bool {
	got, err := normalizeOrigin(raw, allowPath)
	return err == nil && got == trusted
}

var errInvalidOrigin = errors.New("invalid origin")

// normalizeOrigin은 scheme, host, port를 소문자·기본 port 생략 형태로 정규화한다.
// allowPath가 false이면 Origin header와 설정 값처럼 path/query/fragment가 없는 순수 origin만 받는다.
// allowPath가 true이면 Referer처럼 path/query가 있는 URL에서 origin만 취한다.
func normalizeOrigin(raw string, allowPath bool) (string, error) {
	if raw == "" || (!allowPath && strings.ContainsAny(raw, "?#")) {
		return "", errInvalidOrigin
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.Hostname() == "" {
		return "", errInvalidOrigin
	}
	if !allowPath && u.Path != "" {
		return "", errInvalidOrigin
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errInvalidOrigin
	}
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]" // IPv6 literal
	}

	port := u.Port()
	if port != "" {
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil || n == 0 {
			return "", errInvalidOrigin
		}
		port = strconv.FormatUint(n, 10)
	}
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}

	if port == "" {
		return scheme + "://" + host, nil
	}
	return scheme + "://" + net.JoinHostPort(strings.Trim(host, "[]"), port), nil
}
