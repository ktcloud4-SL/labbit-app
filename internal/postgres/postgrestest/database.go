// Package postgrestest는 PostgreSQL Integration Test용 폐기 가능한 database를 준비한다.
//
// 사람이 사용하는 개발 database 상태에 의존하지 않도록 LABBIT_TEST_DATABASE_DSN이 가리키는
// PostgreSQL server에 test마다 새 database를 만들고 test 종료 시 삭제한다.
package postgrestest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
)

// AdminDSNEnv는 CREATE DATABASE 권한이 있는 test PostgreSQL 연결 정보다.
const AdminDSNEnv = "LABBIT_TEST_DATABASE_DSN"

// DatabasePrefix는 test가 만든 database 이름의 prefix다. 이 prefix의 database만 삭제한다.
const DatabasePrefix = "labbit_it_"

// NewDatabase는 빈 database를 만들고 그 DSN을 반환한다.
// 명시적으로 실행한 Integration Test가 DB 없이 통과한 것처럼 보이지 않도록 설정이 없거나
// 연결할 수 없으면 skip하지 않고 실패한다.
func NewDatabase(t testing.TB) string {
	t.Helper()

	adminDSN := strings.TrimSpace(os.Getenv(AdminDSNEnv))
	if adminDSN == "" {
		t.Fatalf("%s 설정이 필요합니다. 로컬에서는 make dev-db-up 후 make go-integration-test로 실행합니다", AdminDSNEnv)
	}

	name := DatabasePrefix + randomSuffix(t)
	dsn, err := withDatabase(adminDSN, name)
	if err != nil {
		t.Fatalf("%s: %v", AdminDSNEnv, err)
	}

	execAdmin(t, adminDSN, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	t.Cleanup(func() {
		execAdmin(t, adminDSN, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	})
	return dsn
}

// Connect는 test 종료 시 닫히는 단일 연결을 만든다.
func Connect(t testing.TB, dsn string) *pgx.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := postgres.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("test database 연결 실패: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close(context.Background())
	})
	return conn
}

// Migrate는 migrations를 적용하고 이번에 적용한 목록을 반환한다.
func Migrate(t testing.TB, dsn string, migrations []postgres.Migration) []postgres.Migration {
	t.Helper()
	conn := Connect(t, dsn)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	applied, err := postgres.Migrate(ctx, conn, migrations, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("migration 실패: %v", err)
	}
	return applied
}

func execAdmin(t testing.TB, adminDSN, sql string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := postgres.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("%s 연결 실패: %v", AdminDSNEnv, err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, sql); err != nil {
		t.Fatalf("test database 준비/정리 실패: %v", err)
	}
}

// withDatabase는 admin DSN의 database만 바꾼다. URL과 key/value 형식을 모두 지원한다.
func withDatabase(dsn, database string) (string, error) {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		if err != nil {
			return "", errors.New("URL 형식이 올바르지 않습니다")
		}
		parsed.Path = "/" + database
		parsed.RawPath = ""
		return parsed.String(), nil
	}
	// key/value 형식에서는 뒤에 나온 같은 key가 앞의 값을 대체한다.
	return dsn + " dbname=" + database, nil
}

func randomSuffix(t testing.TB) string {
	t.Helper()
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(buf)
}
