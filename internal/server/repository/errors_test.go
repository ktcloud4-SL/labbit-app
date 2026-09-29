package repository_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

var allSentinels = []error{
	repository.ErrNotFound,
	repository.ErrConflict,
	repository.ErrConstraintViolation,
	repository.ErrInternal,
}

func TestErrorMatchesOnlyItsOwnKind(t *testing.T) {
	tests := []struct {
		kind repository.Kind
		want error
	}{
		{repository.KindNotFound, repository.ErrNotFound},
		{repository.KindConflict, repository.ErrConflict},
		{repository.KindConstraintViolation, repository.ErrConstraintViolation},
		{repository.KindInternal, repository.ErrInternal},
	}
	for _, tt := range tests {
		t.Run(tt.kind.String(), func(t *testing.T) {
			// Application은 다른 계층이 감싼 오류에서도 의미를 판별할 수 있어야 한다.
			err := fmt.Errorf("use case: %w", &repository.Error{Kind: tt.kind, Op: "Op"})
			for _, sentinel := range allSentinels {
				if got, want := errors.Is(err, sentinel), sentinel == tt.want; got != want {
					t.Errorf("errors.Is(%v, %v) = %v, want %v", tt.kind, sentinel, got, want)
				}
			}
		})
	}
}

func TestErrorZeroValueIsInternal(t *testing.T) {
	if !errors.Is(&repository.Error{}, repository.ErrInternal) {
		t.Fatal("분류하지 못한 오류는 internal이어야 합니다")
	}
}

func TestErrorStringOmitsDatabaseDetails(t *testing.T) {
	err := &repository.Error{
		Kind:       repository.KindConflict,
		Op:         "CreateLocalAccount",
		SQLState:   "23505",
		Constraint: "local_accounts_username_key",
		Cause:      errors.New(`duplicate key value violates unique constraint "local_accounts_username_key"`),
	}

	if got, want := err.Error(), "repository: conflict: CreateLocalAccount"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	if errors.Unwrap(err) != nil {
		t.Fatal("원인 오류를 Unwrap하면 호출자가 PostgreSQL 오류 타입에 의존할 수 있습니다")
	}
}

func TestErrorExposesContextCauseOnly(t *testing.T) {
	err := &repository.Error{
		Kind:  repository.KindInternal,
		Op:    "ClassByID",
		Cause: fmt.Errorf("query: %w", context.DeadlineExceeded),
	}

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Error("요청 만료를 구분할 수 있어야 합니다")
	}
	if errors.Is(err, context.Canceled) {
		t.Error("취소되지 않은 요청이 Canceled로 판별되면 안 됩니다")
	}
	if errors.Is(&repository.Error{Kind: repository.KindInternal, Cause: errors.New("connection refused")}, context.DeadlineExceeded) {
		t.Error("context 오류가 아닌 원인이 DeadlineExceeded로 판별되면 안 됩니다")
	}
}

func TestErrorLogValue(t *testing.T) {
	tests := []struct {
		name      string
		err       *repository.Error
		wantCause bool
	}{
		{
			name: "internal은 진단용 원인을 남긴다",
			err: &repository.Error{
				Kind: repository.KindInternal, Op: "UserByID", SQLState: "57P01",
				Cause: errors.New("terminating connection"),
			},
			wantCause: true,
		},
		{
			name: "conflict는 원인 없이 진단 필드만 남긴다",
			err: &repository.Error{
				Kind: repository.KindConflict, Op: "CreateClass", SQLState: "23505", Constraint: "classes_pkey",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			slog.New(slog.NewJSONHandler(&buf, nil)).Error("repository 실패", "error", tt.err)

			var record struct {
				Error map[string]string `json:"error"`
			}
			if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
				t.Fatalf("log JSON 해석 실패: %v\n%s", err, buf.String())
			}
			if record.Error["kind"] != tt.err.Kind.String() || record.Error["op"] != tt.err.Op {
				t.Errorf("kind/op가 log에 없습니다: %v", record.Error)
			}
			if record.Error["sqlstate"] != tt.err.SQLState || record.Error["constraint"] != tt.err.Constraint {
				t.Errorf("진단 필드가 log에 없습니다: %v", record.Error)
			}
			if _, ok := record.Error["cause"]; ok != tt.wantCause {
				t.Errorf("cause 기록 여부 = %v, want %v", ok, tt.wantCause)
			}
		})
	}
}

func TestPasswordHashIsRedactedInEveryOutputPath(t *testing.T) {
	const phc = "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$c2VjcmV0LWhhc2g"
	account := repository.LocalAccount{
		User:         repository.User{Username: "alice"},
		PasswordHash: repository.PasswordHash(phc),
	}
	newAccount := repository.NewLocalAccount{Username: "alice", PasswordHash: repository.PasswordHash(phc)}

	var textLog, jsonLog bytes.Buffer
	slog.New(slog.NewTextHandler(&textLog, nil)).Info("account", "account", account, "new", newAccount, "hash", account.PasswordHash)
	slog.New(slog.NewJSONHandler(&jsonLog, nil)).Info("account", "account", account, "new", newAccount, "hash", account.PasswordHash)
	marshaled, err := json.Marshal(account)
	if err != nil {
		t.Fatal(err)
	}

	outputs := map[string]string{
		"%v":       fmt.Sprintf("%v", account),
		"%+v":      fmt.Sprintf("%+v", account),
		"%#v":      fmt.Sprintf("%#v", account),
		"%s":       fmt.Sprintf("%s", account.PasswordHash),
		"json":     string(marshaled),
		"slogText": textLog.String(),
		"slogJSON": jsonLog.String(),
	}
	for path, output := range outputs {
		if strings.Contains(output, "argon2id") || strings.Contains(output, "c2VjcmV0") {
			t.Errorf("%s 출력에 password hash가 노출되었습니다: %s", path, output)
		}
		if !strings.Contains(output, "REDACTED") {
			t.Errorf("%s 출력에 redaction 표시가 없습니다: %s", path, output)
		}
	}

	// 저장 경계에서는 원문이 필요하므로 명시적 변환으로만 접근한다.
	if string(account.PasswordHash) != phc {
		t.Fatal("명시적 변환은 원문을 반환해야 합니다")
	}
}
