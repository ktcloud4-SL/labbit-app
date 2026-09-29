//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"

	migrationfiles "github.com/ktcloud4-SL/labbit-app/db/migrations"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
)

var discardLogger = slog.New(slog.DiscardHandler)

// productTables는 db/migrations/README.md의 000001~000005 table 목록이다.
var productTables = []string{
	"organizations", "users", "local_accounts", "auth_sessions", "classes", "class_memberships",
	"connectors", "connector_credentials", "provider_connections", "provider_image_mappings", "provider_flavor_mappings",
	"lab_specs", "lab_spec_vm_roles",
	"lab_executions", "creation_snapshots", "lab_instances", "operations", "operation_items", "provider_resources",
	"terminal_sessions", "live_sessions",
}

func TestMigrateFreshDatabaseAppliesAllMigrations(t *testing.T) {
	ctx := t.Context()
	all := embeddedMigrations(t)
	dsn := postgrestest.NewDatabase(t)
	conn := postgrestest.Connect(t, dsn)

	applied, err := postgres.Migrate(ctx, conn, all, discardLogger)
	if err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if len(applied) != len(all) {
		t.Fatalf("applied = %d, want %d", len(applied), len(all))
	}
	assertAppliedVersions(t, conn, 1, 2, 3, 4, 5, 6)
	for _, table := range productTables {
		if !tableExists(t, conn, table) {
			t.Fatalf("table %s was not created", table)
		}
	}
	assertTraceColumns(t, conn, true)
	if err := postgres.CheckSchema(ctx, conn, all); err != nil {
		t.Fatalf("CheckSchema() error = %v", err)
	}

	// 같은 migration을 다시 실행해도 아무것도 적용하지 않는다.
	again, err := postgres.Migrate(ctx, conn, all, discardLogger)
	if err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second Migrate() applied %d migrations, want 0", len(again))
	}
	assertAppliedVersions(t, conn, 1, 2, 3, 4, 5, 6)
}

func TestMigrateApplies000006AdditivelyOn000005Database(t *testing.T) {
	ctx := t.Context()
	all := embeddedMigrations(t)
	upTo5 := migrationsUpTo(all, 5)
	dsn := postgrestest.NewDatabase(t)
	conn := postgrestest.Connect(t, dsn)

	if _, err := postgres.Migrate(ctx, conn, upTo5, discardLogger); err != nil {
		t.Fatalf("Migrate(000001~000005) error = %v", err)
	}
	assertAppliedVersions(t, conn, 1, 2, 3, 4, 5)
	assertTraceColumns(t, conn, false)
	if err := postgres.CheckSchema(ctx, conn, all); !errors.Is(err, postgres.ErrSchemaIncompatible) {
		t.Fatalf("CheckSchema() on 000005 = %v, want ErrSchemaIncompatible", err)
	}

	operationID := seedOperation(t, conn)

	applied, err := postgres.Migrate(ctx, conn, all, discardLogger)
	if err != nil {
		t.Fatalf("Migrate(000006) error = %v", err)
	}
	if len(applied) != 1 || applied[0].Version != 6 {
		t.Fatalf("applied = %v, want only 000006", migrationNames(applied))
	}
	assertAppliedVersions(t, conn, 1, 2, 3, 4, 5, 6)
	assertTraceColumns(t, conn, true)

	// 기존 Operation row는 유지되고 새 Trace metadata는 NULL이다.
	var traceparent, tracestate *string
	if err := conn.QueryRow(ctx,
		"SELECT traceparent, tracestate FROM operations WHERE id = $1", operationID,
	).Scan(&traceparent, &tracestate); err != nil {
		t.Fatalf("existing operation row lookup failed: %v", err)
	}
	if traceparent != nil || tracestate != nil {
		t.Fatalf("existing row trace context = %v/%v, want NULL/NULL", traceparent, tracestate)
	}
	if err := postgres.CheckSchema(ctx, conn, all); err != nil {
		t.Fatalf("CheckSchema() after 000006 error = %v", err)
	}
}

func TestMigrateFailureRollsBackFailedMigrationAndCanBeRetried(t *testing.T) {
	ctx := t.Context()
	all := embeddedMigrations(t)
	broken := loadMigrations(t, withExtraFile(t, "000007_it_probe.sql",
		"BEGIN;\nCREATE TABLE it_probe (id integer);\nSELECT 1 / 0;\nCOMMIT;\n"))
	fixed := loadMigrations(t, withExtraFile(t, "000007_it_probe.sql",
		"BEGIN;\nCREATE TABLE it_probe (id integer);\nCOMMIT;\n"))
	dsn := postgrestest.NewDatabase(t)
	conn := postgrestest.Connect(t, dsn)

	applied, err := postgres.Migrate(ctx, conn, broken, discardLogger)
	if err == nil || !strings.Contains(err.Error(), "000007_it_probe.sql") {
		t.Fatalf("Migrate() error = %v, want failure of 000007", err)
	}
	if len(applied) != len(all) {
		t.Fatalf("applied before failure = %v, want 000001~000006", migrationNames(applied))
	}
	// 실패한 migration의 DDL과 적용 기록은 함께 rollback된다.
	if tableExists(t, conn, "it_probe") {
		t.Fatal("failed migration left a partial table behind")
	}
	assertAppliedVersions(t, conn, 1, 2, 3, 4, 5, 6)
	if err := postgres.CheckSchema(ctx, conn, broken); !errors.Is(err, postgres.ErrSchemaIncompatible) {
		t.Fatalf("CheckSchema() after failure = %v, want ErrSchemaIncompatible", err)
	}

	// 원인을 고친 뒤 다시 실행하면 실패한 migration부터 이어서 적용한다.
	applied, err = postgres.Migrate(ctx, conn, fixed, discardLogger)
	if err != nil {
		t.Fatalf("retry Migrate() error = %v", err)
	}
	if len(applied) != 1 || applied[0].Version != 7 {
		t.Fatalf("retry applied = %v, want only 000007", migrationNames(applied))
	}
	if !tableExists(t, conn, "it_probe") {
		t.Fatal("retried migration did not create it_probe")
	}
	if err := postgres.CheckSchema(ctx, conn, fixed); err != nil {
		t.Fatalf("CheckSchema() after retry error = %v", err)
	}
}

func TestMigrateRejectsBodyThatEndsRunnerTransaction(t *testing.T) {
	ctx := t.Context()
	// 한 줄에 섞인 COMMIT은 파일 검사를 통과하므로 runner가 실행 후 transaction 상태로 막아야 한다.
	escaping := loadMigrations(t, withExtraFile(t, "000007_it_escape.sql",
		"BEGIN;\nSELECT 1; COMMIT; SELECT 2;\nCOMMIT;\n"))
	dsn := postgrestest.NewDatabase(t)
	conn := postgrestest.Connect(t, dsn)

	_, err := postgres.Migrate(ctx, conn, escaping, discardLogger)
	if !errors.Is(err, postgres.ErrMigrationState) {
		t.Fatalf("Migrate() error = %v, want ErrMigrationState", err)
	}
	assertAppliedVersions(t, conn, 1, 2, 3, 4, 5, 6)
}

func TestMigrateRefusesChangedAppliedMigration(t *testing.T) {
	ctx := t.Context()
	all := embeddedMigrations(t)
	dsn := postgrestest.NewDatabase(t)
	conn := postgrestest.Connect(t, dsn)
	if _, err := postgres.Migrate(ctx, conn, all, discardLogger); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	files := copyEmbeddedFiles(t)
	files["000001_identity_and_class.sql"].Data = append(files["000001_identity_and_class.sql"].Data, []byte("-- edited\n")...)
	changed := loadMigrations(t, files)

	if _, err := postgres.Migrate(ctx, conn, changed, discardLogger); !errors.Is(err, postgres.ErrMigrationState) {
		t.Fatalf("Migrate() with edited applied migration = %v, want ErrMigrationState", err)
	}
	if err := postgres.CheckSchema(ctx, conn, changed); !errors.Is(err, postgres.ErrSchemaIncompatible) {
		t.Fatalf("CheckSchema() with edited applied migration = %v, want ErrSchemaIncompatible", err)
	}
}

func TestMigrateConcurrentRunnersApplyEachMigrationOnce(t *testing.T) {
	ctx := t.Context()
	all := embeddedMigrations(t)
	dsn := postgrestest.NewDatabase(t)
	conns := []*pgx.Conn{postgrestest.Connect(t, dsn), postgrestest.Connect(t, dsn)}

	var wg sync.WaitGroup
	results := make([][]postgres.Migration, len(conns))
	errs := make([]error, len(conns))
	for i, conn := range conns {
		wg.Go(func() {
			results[i], errs[i] = postgres.Migrate(ctx, conn, all, discardLogger)
		})
	}
	wg.Wait()

	total := 0
	for i := range conns {
		if errs[i] != nil {
			t.Fatalf("runner %d error = %v", i, errs[i])
		}
		total += len(results[i])
	}
	if total != len(all) {
		t.Fatalf("migrations applied across runners = %d, want %d", total, len(all))
	}
	assertAppliedVersions(t, conns[0], 1, 2, 3, 4, 5, 6)
}

func TestCheckSchema(t *testing.T) {
	ctx := t.Context()
	all := embeddedMigrations(t)

	t.Run("uninitialized database", func(t *testing.T) {
		conn := postgrestest.Connect(t, postgrestest.NewDatabase(t))
		if err := postgres.CheckSchema(ctx, conn, all); !errors.Is(err, postgres.ErrSchemaIncompatible) {
			t.Fatalf("CheckSchema() = %v, want ErrSchemaIncompatible", err)
		}
	})

	t.Run("newer migration from a later release is tolerated", func(t *testing.T) {
		conn := postgrestest.Connect(t, postgrestest.NewDatabase(t))
		newer := loadMigrations(t, withExtraFile(t, "000007_it_newer.sql",
			"BEGIN;\nCREATE TABLE it_newer (id integer);\nCOMMIT;\n"))
		if _, err := postgres.Migrate(ctx, conn, newer, discardLogger); err != nil {
			t.Fatalf("Migrate() error = %v", err)
		}

		// rollout 중인 이전 build는 자신이 아는 000001~000006이 모두 있으면 호환된다.
		if err := postgres.CheckSchema(ctx, conn, all); err != nil {
			t.Fatalf("CheckSchema() = %v, want nil", err)
		}
		applied, err := postgres.Migrate(ctx, conn, all, discardLogger)
		if err != nil || len(applied) != 0 {
			t.Fatalf("older runner Migrate() = %v, %v, want no-op", migrationNames(applied), err)
		}
	})
}

func embeddedMigrations(t *testing.T) []postgres.Migration {
	t.Helper()
	migrations, err := postgres.LoadMigrations(migrationfiles.Files)
	if err != nil {
		t.Fatalf("LoadMigrations() error = %v", err)
	}
	if len(migrations) != 6 {
		t.Fatalf("embedded migrations = %d, want 000001~000006", len(migrations))
	}
	return migrations
}

func loadMigrations(t *testing.T, files fstest.MapFS) []postgres.Migration {
	t.Helper()
	migrations, err := postgres.LoadMigrations(files)
	if err != nil {
		t.Fatalf("LoadMigrations() error = %v", err)
	}
	return migrations
}

func copyEmbeddedFiles(t *testing.T) fstest.MapFS {
	t.Helper()
	names, err := fs.Glob(migrationfiles.Files, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{}
	for _, name := range names {
		data, err := fs.ReadFile(migrationfiles.Files, name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: data}
	}
	return files
}

func withExtraFile(t *testing.T, name, content string) fstest.MapFS {
	t.Helper()
	files := copyEmbeddedFiles(t)
	files[name] = &fstest.MapFile{Data: []byte(content)}
	return files
}

func migrationsUpTo(migrations []postgres.Migration, version int64) []postgres.Migration {
	var selected []postgres.Migration
	for _, migration := range migrations {
		if migration.Version <= version {
			selected = append(selected, migration)
		}
	}
	return selected
}

func migrationNames(migrations []postgres.Migration) []string {
	names := make([]string, 0, len(migrations))
	for _, migration := range migrations {
		names = append(names, migration.Name)
	}
	return names
}

func assertAppliedVersions(t *testing.T, conn *pgx.Conn, want ...int64) {
	t.Helper()
	rows, err := conn.Query(context.Background(), "SELECT version FROM labbit_schema_migrations ORDER BY version")
	if err != nil {
		t.Fatalf("applied version lookup failed: %v", err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		t.Fatalf("applied version lookup failed: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("applied versions = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("applied versions = %v, want %v", got, want)
		}
	}
}

func tableExists(t *testing.T, conn *pgx.Conn, table string) bool {
	t.Helper()
	var exists bool
	if err := conn.QueryRow(context.Background(),
		"SELECT to_regclass($1) IS NOT NULL", "public."+table,
	).Scan(&exists); err != nil {
		t.Fatalf("table lookup failed: %v", err)
	}
	return exists
}

func assertTraceColumns(t *testing.T, conn *pgx.Conn, want bool) {
	t.Helper()
	rows, err := conn.Query(context.Background(), `
		SELECT column_name, is_nullable
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'operations'
		  AND column_name IN ('traceparent', 'tracestate')
		ORDER BY column_name`)
	if err != nil {
		t.Fatalf("column lookup failed: %v", err)
	}
	type column struct {
		Name     string
		Nullable string
	}
	columns, err := pgx.CollectRows(rows, pgx.RowToStructByPos[column])
	if err != nil {
		t.Fatalf("column lookup failed: %v", err)
	}
	if !want {
		if len(columns) != 0 {
			t.Fatalf("trace columns = %v, want none before 000006", columns)
		}
		return
	}
	if len(columns) != 2 {
		t.Fatalf("trace columns = %v, want traceparent and tracestate", columns)
	}
	for _, c := range columns {
		if c.Nullable != "YES" {
			t.Fatalf("column %s nullable = %s, want YES", c.Name, c.Nullable)
		}
	}
}

// seedOperation은 000005 schema에 기존 Operation row 하나를 만든다. ID는 test 전용 고정값이다.
func seedOperation(t *testing.T, conn *pgx.Conn) string {
	t.Helper()
	const (
		orgID       = "00000000-0000-4000-8000-000000000001"
		userID      = "00000000-0000-4000-8000-000000000002"
		classID     = "00000000-0000-4000-8000-000000000003"
		labSpecID   = "00000000-0000-4000-8000-000000000004"
		executionID = "00000000-0000-4000-8000-000000000005"
		operationID = "00000000-0000-4000-8000-000000000006"
	)
	statements := []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO organizations (id, name) VALUES ($1, 'it-org')", []any{orgID}},
		{"INSERT INTO users (id, organization_id, organization_role) VALUES ($1, $2, 'MEMBER')", []any{userID, orgID}},
		{"INSERT INTO classes (id, organization_id, name) VALUES ($1, $2, 'it-class')", []any{classID, orgID}},
		{`INSERT INTO lab_specs (id, organization_id, owner_user_id, name, internet_outbound, workspace_role, workspace_instance_index)
		  VALUES ($1, $2, $3, 'it-spec', false, 'web', 0)`, []any{labSpecID, orgID, userID}},
		{`INSERT INTO lab_executions (id, organization_id, class_id, lab_spec_id, instructor_user_id, status)
		  VALUES ($1, $2, $3, $4, $5, 'RUNNING')`, []any{executionID, orgID, classID, labSpecID, userID}},
		{`INSERT INTO operations (id, organization_id, operation_type, requested_by_user_id, lab_execution_id,
		                          idempotency_key_hash, request_fingerprint, status)
		  VALUES ($1, $2, 'PROVISION', $3, $4, '\x01', '\x02', 'PENDING')`, []any{operationID, orgID, userID, executionID}},
	}
	for _, statement := range statements {
		if _, err := conn.Exec(context.Background(), statement.sql, statement.args...); err != nil {
			t.Fatalf("seed failed: %v", err)
		}
	}
	return operationID
}
