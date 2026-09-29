package postgres

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

const (
	sqlStateUniqueViolation = "23505"
	// SQLSTATE class 23은 integrity constraint violation이다. FK(23503), Check(23514),
	// Not Null(23502) 등을 하나의 constraint violation 의미로 묶는다.
	sqlStateIntegrityClass = "23"
)

// normalize는 pgx/PostgreSQL 오류를 Application이 해석할 수 있는 repository.Error로 바꾼다.
//
// PostgreSQL 오류 문자열, SQLSTATE, constraint 이름은 Error() 문자열에 포함하지 않고 진단 필드로만 둔다.
// NotFound/Conflict/ConstraintViolation은 원인 오류를 보존하지 않는다. PgError.Detail은 실패한 row의 값을
// 포함할 수 있어 password_hash 같은 값이 log로 새지 않도록 KindInternal의 원인만 log 전용으로 남긴다.
func normalize(op string, err error) error {
	if err == nil {
		return nil
	}

	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return &repository.Error{Kind: repository.KindNotFound, Op: op}
	case errors.As(err, &pgErr):
		kind := repository.KindInternal
		switch {
		case pgErr.Code == sqlStateUniqueViolation:
			kind = repository.KindConflict
		case strings.HasPrefix(pgErr.Code, sqlStateIntegrityClass):
			kind = repository.KindConstraintViolation
		}
		e := &repository.Error{
			Kind:       kind,
			Op:         op,
			SQLState:   pgErr.Code,
			Constraint: pgErr.ConstraintName,
		}
		if kind == repository.KindInternal {
			e.Cause = err
		}
		return e
	default:
		return &repository.Error{Kind: repository.KindInternal, Op: op, Cause: err}
	}
}

// notFound는 UPDATE가 아무 row도 바꾸지 못한 경우처럼 DB가 오류를 내지 않는 Not Found다.
func notFound(op string) error {
	return &repository.Error{Kind: repository.KindNotFound, Op: op}
}
