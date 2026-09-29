package postgrestest

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// pgx의 실제 parsing 결과를 oracle로 사용해 임시 database가 선택되는지 확인한다.
func TestWithDatabaseSelectsGeneratedDatabase(t *testing.T) {
	const generated = DatabasePrefix + "0123456789abcdef"
	tests := []struct {
		name string
		dsn  string
	}{
		{
			name: "URL path only",
			dsn:  "postgres://labbit:dummy@127.0.0.1:5432/postgres?sslmode=disable&connect_timeout=7",
		},
		{
			name: "URL without path",
			dsn:  "postgres://labbit:dummy@127.0.0.1:5432?sslmode=disable&connect_timeout=7",
		},
		{
			name: "URL dbname query overrides path",
			dsn:  "postgres://labbit:dummy@127.0.0.1:5432/postgres?dbname=shared&sslmode=disable&connect_timeout=7",
		},
		{
			name: "URL database query overrides path",
			dsn:  "postgresql://labbit:dummy@127.0.0.1:5432/postgres?database=shared&sslmode=disable&connect_timeout=7",
		},
		{
			name: "URL repeated database queries",
			dsn:  "postgres://labbit:dummy@127.0.0.1:5432/postgres?dbname=a&database=b&dbname=c&sslmode=disable&connect_timeout=7",
		},
		{
			name: "key/value with existing dbname",
			dsn:  "host=127.0.0.1 port=5432 user=labbit password=dummy dbname=shared sslmode=disable connect_timeout=7",
		},
		{
			name: "key/value without dbname",
			dsn:  "host=127.0.0.1 port=5432 user=labbit password=dummy sslmode=disable connect_timeout=7",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dsn, err := withDatabase(tt.dsn, generated)
			if err != nil {
				t.Fatalf("withDatabase() error = %v", err)
			}
			config, err := pgx.ParseConfig(dsn)
			if err != nil {
				t.Fatalf("pgx.ParseConfig(generated DSN) error = %v", err)
			}
			if config.Database != generated {
				t.Fatalf("Database = %q, want %q (dsn %q)", config.Database, generated, dsn)
			}
			// database 선택 외의 연결 option은 유지한다.
			if config.Host != "127.0.0.1" || config.User != "labbit" || config.Password != "dummy" {
				t.Fatalf("connection target changed: host=%q user=%q", config.Host, config.User)
			}
			if config.ConnectTimeout != 7*time.Second {
				t.Fatalf("ConnectTimeout = %v, want 7s", config.ConnectTimeout)
			}
			if config.TLSConfig != nil {
				t.Fatal("sslmode=disable was not preserved")
			}
		})
	}
}
