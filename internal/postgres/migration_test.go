package postgres

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	migrationfiles "github.com/ktcloud4-SL/labbit-app/db/migrations"
)

func TestLoadMigrationsReadsEmbeddedSSOTInVersionOrder(t *testing.T) {
	migrations, err := LoadMigrations(migrationfiles.Files)
	if err != nil {
		t.Fatalf("LoadMigrations() error = %v", err)
	}

	wantNames := []string{
		"000001_identity_and_class.sql",
		"000002_connector_and_provider.sql",
		"000003_lab_spec.sql",
		"000004_execution_operation_resource.sql",
		"000005_realtime_sessions.sql",
		"000006_operation_trace_context.sql",
	}
	if len(migrations) != len(wantNames) {
		t.Fatalf("migration count = %d, want %d", len(migrations), len(wantNames))
	}
	for i, migration := range migrations {
		if migration.Version != int64(i+1) || migration.Name != wantNames[i] {
			t.Fatalf("migration[%d] = %d %s, want %d %s", i, migration.Version, migration.Name, i+1, wantNames[i])
		}
		if len(migration.Checksum) != 64 {
			t.Fatalf("%s checksum = %q, want SHA-256 hex", migration.Name, migration.Checksum)
		}
		body := strings.TrimSpace(migration.body)
		if strings.HasPrefix(strings.ToUpper(body), "BEGIN;") || strings.HasSuffix(strings.ToUpper(body), "COMMIT;") {
			t.Fatalf("%s body still contains the transaction envelope", migration.Name)
		}
	}
}

func TestLoadMigrationsRejectsUnsafeFiles(t *testing.T) {
	valid := "BEGIN;\nCREATE TABLE a (id integer);\nCOMMIT;\n"
	tests := []struct {
		name  string
		files fstest.MapFS
		want  string
	}{
		{
			name:  "no files",
			files: fstest.MapFS{},
			want:  "migration 파일이 없습니다",
		},
		{
			name:  "invalid file name",
			files: fstest.MapFS{"init.sql": {Data: []byte(valid)}},
			want:  "이름 형식 오류",
		},
		{
			name: "duplicate version",
			files: fstest.MapFS{
				"000001_a.sql": {Data: []byte(valid)},
				"1_b.sql":      {Data: []byte(valid)},
			},
			want: "중복",
		},
		{
			name:  "missing BEGIN envelope",
			files: fstest.MapFS{"000001_a.sql": {Data: []byte("CREATE TABLE a (id integer);\nCOMMIT;\n")}},
			want:  "BEGIN; ... COMMIT;",
		},
		{
			name:  "missing COMMIT envelope",
			files: fstest.MapFS{"000001_a.sql": {Data: []byte("BEGIN;\nCREATE TABLE a (id integer);\n")}},
			want:  "BEGIN; ... COMMIT;",
		},
		{
			name:  "inner COMMIT",
			files: fstest.MapFS{"000001_a.sql": {Data: []byte("BEGIN;\nCREATE TABLE a (id integer);\ncommit;\nCREATE TABLE b (id integer);\nCOMMIT;\n")}},
			want:  "transaction 제어문",
		},
		{
			name:  "inner ROLLBACK",
			files: fstest.MapFS{"000001_a.sql": {Data: []byte("BEGIN;\nROLLBACK;\nCOMMIT;\n")}},
			want:  "transaction 제어문",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadMigrations(tt.files)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadMigrations() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestTransactionBodyKeepsPLpgSQLBlocks(t *testing.T) {
	content := `-- 설명
BEGIN;

CREATE FUNCTION f() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'x';
END;
$$;

COMMIT;
`
	body, err := transactionBody(content)
	if err != nil {
		t.Fatalf("transactionBody() error = %v", err)
	}
	if !strings.Contains(body, "BEGIN\n    RAISE") || !strings.Contains(body, "END;\n$$;") {
		t.Fatalf("PL/pgSQL block was not preserved: %q", body)
	}
}

func TestPlanPending(t *testing.T) {
	migrations := []Migration{
		{Version: 1, Name: "000001_a.sql", Checksum: "c1"},
		{Version: 2, Name: "000002_b.sql", Checksum: "c2"},
		{Version: 3, Name: "000003_c.sql", Checksum: "c3"},
	}

	tests := []struct {
		name        string
		applied     []appliedMigration
		wantPending []int64
		wantErr     bool
	}{
		{
			name:        "fresh database applies all",
			wantPending: []int64{1, 2, 3},
		},
		{
			name:        "continues after last applied",
			applied:     []appliedMigration{{1, "c1"}, {2, "c2"}},
			wantPending: []int64{3},
		},
		{
			name:    "all applied",
			applied: []appliedMigration{{1, "c1"}, {2, "c2"}, {3, "c3"}},
		},
		{
			name:    "newer unknown version is tolerated",
			applied: []appliedMigration{{1, "c1"}, {2, "c2"}, {3, "c3"}, {4, "c4"}},
		},
		{
			name:    "changed applied migration",
			applied: []appliedMigration{{1, "changed"}},
			wantErr: true,
		},
		{
			name:    "pending migration behind a higher applied version",
			applied: []appliedMigration{{1, "c1"}, {3, "c3"}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pending, err := planPending(migrations, tt.applied)
			if tt.wantErr {
				if !errors.Is(err, ErrMigrationState) {
					t.Fatalf("planPending() error = %v, want ErrMigrationState", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("planPending() error = %v", err)
			}
			var got []int64
			for _, migration := range pending {
				got = append(got, migration.Version)
			}
			if len(got) != len(tt.wantPending) {
				t.Fatalf("pending = %v, want %v", got, tt.wantPending)
			}
			for i := range got {
				if got[i] != tt.wantPending[i] {
					t.Fatalf("pending = %v, want %v", got, tt.wantPending)
				}
			}
		})
	}
}
