package workspacefile

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// 경로 상한이다. Linux의 PATH_MAX와 NAME_MAX이며 contracts/http/openapi.yaml의 path parameter와 같다.
const (
	maxPathBytes    = 4096
	maxSegmentBytes = 255
)

// ErrInvalidPath는 File API 경로 규칙을 위반했음이다. 경로는 조용히 고쳐 쓰지 않고 거절한다.
var ErrInvalidPath = errors.New("workspacefile: 올바르지 않은 경로")

// 거절 사유다. 경로 원문을 담지 않는 고정 분류이며 log와 test에서 쓴다.
const (
	reasonEmpty            = "empty"
	reasonTooLong          = "too_long"
	reasonInvalidUTF8      = "invalid_utf8"
	reasonControlCharacter = "control_character"
	reasonBackslash        = "backslash"
	reasonAbsolute         = "absolute"
	reasonTrailingSep      = "trailing_separator"
	reasonEmptySegment     = "empty_segment"
	reasonDotSegment       = "dot_segment"
	reasonSegmentTooLong   = "segment_too_long"
	reasonPercentEscape    = "percent_escape"
)

// PathError는 ErrInvalidPath의 사유다. 경로 원문은 포함하지 않는다.
type PathError struct {
	Reason string
}

func (e *PathError) Error() string { return fmt.Sprintf("%v: %s", ErrInvalidPath, e.Reason) }

// Is는 errors.Is(err, ErrInvalidPath)를 만족시킨다.
func (e *PathError) Is(target error) bool { return target == ErrInvalidPath }

// Path는 Workspace root 기준의 canonical 상대 POSIX 경로다. 경로 규칙의 단일 구현이며 이 package 밖에서는 만들 수 없다.
// Transport adapter는 Path만 받으므로 handler, repository, Connector adapter가 서로 다른 정규화 규칙을 만들 수 없다.
//
// canonical 규칙(contracts/http/openapi.yaml의 path parameter):
//   - 빈 문자열은 Workspace root이며 디렉터리(Tree)에서만 허용한다.
//   - '/'로 시작하거나 끝나지 않고, 빈 segment, "." 또는 ".." segment가 없다.
//   - 백슬래시와 제어 문자(U+0000-U+001F, U+007F)가 없고 유효한 UTF-8이다.
//   - percent-escape 형태('%'와 두 개의 16진수)가 없다. HTTP layer가 query를 한 번 decode한 값을 받으므로 남아 있는 percent-escape는
//     이중 인코딩과 구분할 수 없다. 이 package는 어떤 경우에도 다시 decode하지 않는다.
//   - 전체 4096 byte, segment 하나 255 byte를 넘지 않는다.
//
// 거절한 경로를 고쳐 쓰지 않는다. 그래서 canonical Path의 문자열은 요청한 값과 같다.
type Path struct {
	value string
}

// String은 canonical 경로다. root는 빈 문자열이다.
func (p Path) String() string { return p.value }

// IsRoot는 Workspace root인지 알려 준다.
func (p Path) IsRoot() bool { return p.value == "" }

// ParseDirectory는 디렉터리 경로를 검증한다. 빈 문자열은 Workspace root다.
func ParseDirectory(raw string) (Path, error) {
	if raw == "" {
		return Path{}, nil
	}
	return canonicalize(raw)
}

// ParseFile은 파일 경로를 검증한다. Workspace root는 파일이 아니므로 빈 문자열을 거절한다.
func ParseFile(raw string) (Path, error) {
	if raw == "" {
		return Path{}, &PathError{Reason: reasonEmpty}
	}
	return canonicalize(raw)
}

// child는 디렉터리 dir의 항목 name의 경로다. name이 경로 규칙으로 표현할 수 없는 값이면(구분자 포함, "." 등) ok가 false다.
func (p Path) child(name string) (Path, bool) {
	if name == "" || strings.Contains(name, "/") {
		return Path{}, false
	}
	joined := name
	if p.value != "" {
		joined = p.value + "/" + name
	}
	c, err := canonicalize(joined)
	if err != nil {
		return Path{}, false
	}
	return c, true
}

func canonicalize(raw string) (Path, error) {
	if raw == "" {
		return Path{}, &PathError{Reason: reasonEmpty}
	}
	if len(raw) > maxPathBytes {
		return Path{}, &PathError{Reason: reasonTooLong}
	}
	if !utf8.ValidString(raw) {
		return Path{}, &PathError{Reason: reasonInvalidUTF8}
	}
	for i := 0; i < len(raw); i++ {
		switch c := raw[i]; {
		case c < 0x20 || c == 0x7f:
			return Path{}, &PathError{Reason: reasonControlCharacter}
		case c == '\\':
			return Path{}, &PathError{Reason: reasonBackslash}
		case c == '%' && i+2 < len(raw) && isHex(raw[i+1]) && isHex(raw[i+2]):
			return Path{}, &PathError{Reason: reasonPercentEscape}
		}
	}
	if raw[0] == '/' {
		return Path{}, &PathError{Reason: reasonAbsolute}
	}
	if raw[len(raw)-1] == '/' {
		return Path{}, &PathError{Reason: reasonTrailingSep}
	}
	for _, segment := range strings.Split(raw, "/") {
		switch {
		case segment == "":
			return Path{}, &PathError{Reason: reasonEmptySegment}
		case segment == "." || segment == "..":
			return Path{}, &PathError{Reason: reasonDotSegment}
		case len(segment) > maxSegmentBytes:
			return Path{}, &PathError{Reason: reasonSegmentTooLong}
		}
	}
	return Path{value: raw}, nil
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}
