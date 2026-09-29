//go:build integration

package postgrestest

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	migrationfiles "github.com/ktcloud4-SL/labbit-app/db/migrations"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
)

// admin DSN이 공유 database를 query parameter로 선택하고 있어도 test는 임시 database만 변경한다.
func TestNewDatabaseDoesNotMutateDatabaseSelectedByAdminDSN(t *testing.T) {
	sharedDSN := NewDatabase(t)
	shared := databaseName(t, sharedDSN)

	for _, key := range []string{"dbname", "database"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(AdminDSNEnv, selectDatabase(t, os.Getenv(AdminDSNEnv), key, shared))

			dsn := NewDatabase(t)
			temporary := databaseName(t, dsn)
			if temporary == shared || !strings.HasPrefix(temporary, DatabasePrefix) {
				t.Fatalf("temporary database = %q, shared = %q", temporary, shared)
			}
			migrations, err := postgres.LoadMigrations(migrationfiles.Files)
			if err != nil {
				t.Fatal(err)
			}
			Migrate(t, dsn, migrations)

			if !hasMigrationTable(t, dsn) {
				t.Fatal("temporary database was not migrated")
			}
			if hasMigrationTable(t, sharedDSN) {
				t.Fatalf("shared database %s was migrated through %s query parameter", shared, key)
			}
		})
	}
}

// selectDatabase는 admin DSN에 database 선택 parameter를 덧붙인 입력을 만든다.
func selectDatabase(t *testing.T, dsn, key, database string) string {
	t.Helper()
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		query := parsed.Query()
		query.Set(key, database)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return dsn + " dbname=" + database
}

// databaseName은 실제 연결된 database 이름을 server에서 확인한다.
func databaseName(t *testing.T, dsn string) string {
	t.Helper()
	var name string
	if err := Connect(t, dsn).QueryRow(context.Background(), "SELECT current_database()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	return name
}

func hasMigrationTable(t *testing.T, dsn string) bool {
	t.Helper()
	var exists bool
	if err := Connect(t, dsn).QueryRow(context.Background(),
		"SELECT to_regclass('public.labbit_schema_migrations') IS NOT NULL",
	).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	return exists
}
