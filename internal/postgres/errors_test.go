package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

func TestNormalize(t *testing.T) {
	// Detail은 실패한 row 값을 포함할 수 있으므로 어떤 경로로도 결과에 남으면 안 된다.
	pgError := func(code string) *pgconn.PgError {
		return &pgconn.PgError{
			Severity:       "ERROR",
			Code:           code,
			Message:        "raw postgres message",
			Detail:         "Failing row contains (secret-password-hash)",
			ConstraintName: "some_constraint",
		}
	}

	tests := []struct {
		name           string
		err            error
		want           repository.Kind
		wantSQLState   string
		wantConstraint string
		wantCause      bool
	}{
		{name: "no rows", err: pgx.ErrNoRows, want: repository.KindNotFound},
		{name: "wrapped no rows", err: fmt.Errorf("scan: %w", pgx.ErrNoRows), want: repository.KindNotFound},
		{name: "unique violation", err: pgError("23505"), want: repository.KindConflict, wantSQLState: "23505", wantConstraint: "some_constraint"},
		{name: "wrapped unique violation", err: fmt.Errorf("exec: %w", pgError("23505")), want: repository.KindConflict, wantSQLState: "23505", wantConstraint: "some_constraint"},
		{name: "foreign key violation", err: pgError("23503"), want: repository.KindConstraintViolation, wantSQLState: "23503", wantConstraint: "some_constraint"},
		{name: "check violation", err: pgError("23514"), want: repository.KindConstraintViolation, wantSQLState: "23514", wantConstraint: "some_constraint"},
		{name: "not null violation", err: pgError("23502"), want: repository.KindConstraintViolation, wantSQLState: "23502", wantConstraint: "some_constraint"},
		{name: "serialization failure", err: pgError("40001"), want: repository.KindInternal, wantSQLState: "40001", wantConstraint: "some_constraint", wantCause: true},
		{name: "aborted transaction", err: pgError("25P02"), want: repository.KindInternal, wantSQLState: "25P02", wantConstraint: "some_constraint", wantCause: true},
		{name: "connection error", err: errors.New("connection refused"), want: repository.KindInternal, wantCause: true},
		{name: "commit rolled back", err: pgx.ErrTxCommitRollback, want: repository.KindInternal, wantCause: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := normalize("TestOp", tt.err)

			var repoErr *repository.Error
			if !errors.As(err, &repoErr) {
				t.Fatalf("normalize() = %T, want *repository.Error", err)
			}
			if repoErr.Kind != tt.want || repoErr.Op != "TestOp" {
				t.Errorf("Kind/Op = %v/%q, want %v/%q", repoErr.Kind, repoErr.Op, tt.want, "TestOp")
			}
			if repoErr.SQLState != tt.wantSQLState || repoErr.Constraint != tt.wantConstraint {
				t.Errorf("SQLState/Constraint = %q/%q, want %q/%q", repoErr.SQLState, repoErr.Constraint, tt.wantSQLState, tt.wantConstraint)
			}
			if (repoErr.Cause != nil) != tt.wantCause {
				t.Errorf("Cause 보존 여부 = %v, want %v", repoErr.Cause != nil, tt.wantCause)
			}

			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) {
				t.Error("PostgreSQL 오류 타입이 호출자에게 노출되었습니다")
			}
			if errors.Is(err, pgx.ErrNoRows) {
				t.Error("pgx.ErrNoRows가 호출자에게 노출되었습니다")
			}
			for _, leaked := range []string{"raw postgres message", "secret-password-hash", "some_constraint", "SQLSTATE", "23505"} {
				if strings.Contains(err.Error(), leaked) {
					t.Errorf("오류 문자열에 %q가 노출되었습니다: %v", leaked, err)
				}
			}
		})
	}
}

func TestNormalizeNil(t *testing.T) {
	if err := normalize("TestOp", nil); err != nil {
		t.Fatalf("normalize(nil) = %v, want nil", err)
	}
}

func TestNormalizeKeepsContextErrorsDistinguishable(t *testing.T) {
	for _, target := range []error{context.Canceled, context.DeadlineExceeded} {
		err := normalize("TestOp", fmt.Errorf("query: %w", target))
		if !errors.Is(err, target) {
			t.Errorf("errors.Is(%v) = false, want true", target)
		}
		if !errors.Is(err, repository.ErrInternal) {
			t.Errorf("context 오류도 Repository 오류 분류는 internal이어야 합니다: %v", err)
		}
	}
}
