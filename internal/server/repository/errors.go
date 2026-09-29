package repository

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// Kind는 Application이 구분해야 하는 Repository 오류 의미다. HTTP status를 결정하지 않는다.
type Kind int

const (
	// KindInternal은 zero value다. 분류하지 못한 오류는 안전하게 internal로 취급한다.
	KindInternal Kind = iota
	KindNotFound
	KindConflict
	KindConstraintViolation
)

func (k Kind) String() string {
	switch k {
	case KindNotFound:
		return "not found"
	case KindConflict:
		return "conflict"
	case KindConstraintViolation:
		return "constraint violation"
	default:
		return "internal"
	}
}

// errors.Is로 오류 의미를 판별하는 sentinel이다.
var (
	// ErrNotFound는 조회하거나 갱신할 row가 없다.
	ErrNotFound = sentinel("not found")
	// ErrConflict는 Unique 제약과 충돌했다. 경쟁 조건에서도 발생할 수 있는 정상 결과다.
	ErrConflict = sentinel("conflict")
	// ErrConstraintViolation은 FK, Check, Not Null 같은 무결성 제약을 위반했다.
	ErrConstraintViolation = sentinel("constraint violation")
	// ErrInternal은 연결 실패, 미분류 DB 오류처럼 위 의미에 해당하지 않는 실패다.
	ErrInternal = sentinel("internal error")
)

type sentinel string

func (s sentinel) Error() string { return "repository: " + string(s) }

// Error는 Repository가 Application에 반환하는 정규화된 오류다.
//
// Error() 문자열에는 PostgreSQL 오류 문자열, SQLSTATE, constraint 이름을 포함하지 않는다.
// 사용자 응답과 외부 계약에는 Kind만 사용하고, 나머지 필드는 운영 진단과 log 전용이다.
type Error struct {
	Kind Kind
	// Op는 실패한 Repository 작업 이름이다.
	Op string
	// SQLState와 Constraint는 진단용이다. 외부 HTTP/WSS 계약이나 사용자 응답에 노출하지 않는다.
	SQLState   string
	Constraint string
	// Cause는 KindInternal에만 보존하는 원인이며 log 전용이다. Unwrap하지 않으므로
	// errors.As로 PostgreSQL 오류 타입에 의존할 수 없다.
	Cause error
}

func (e *Error) Error() string {
	if e.Op == "" {
		return "repository: " + e.Kind.String()
	}
	return fmt.Sprintf("repository: %s: %s", e.Kind, e.Op)
}

// Is는 Kind에 대응하는 sentinel과 일치한다. 요청 취소·만료는 DB 오류가 아니므로
// 호출자가 구분할 수 있도록 Cause가 context 오류이면 그 의미도 함께 노출한다.
func (e *Error) Is(target error) bool {
	switch target {
	case ErrNotFound:
		return e.Kind == KindNotFound
	case ErrConflict:
		return e.Kind == KindConflict
	case ErrConstraintViolation:
		return e.Kind == KindConstraintViolation
	case ErrInternal:
		return e.Kind == KindInternal
	case context.Canceled, context.DeadlineExceeded:
		return errors.Is(e.Cause, target)
	}
	return false
}

// LogValue는 slog가 오류를 기록할 때 진단에 필요한 값만 남긴다.
func (e *Error) LogValue() slog.Value {
	attrs := []slog.Attr{
		slog.String("kind", e.Kind.String()),
		slog.String("op", e.Op),
	}
	if e.SQLState != "" {
		attrs = append(attrs, slog.String("sqlstate", e.SQLState))
	}
	if e.Constraint != "" {
		attrs = append(attrs, slog.String("constraint", e.Constraint))
	}
	if e.Cause != nil {
		attrs = append(attrs, slog.String("cause", e.Cause.Error()))
	}
	return slog.GroupValue(attrs...)
}
