package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

// migrationLockKey는 같은 database에서 동시에 실행된 runner를 직렬화한다.
// PostgreSQL advisory lock은 database 단위이므로 다른 database의 runner와 경쟁하지 않는다.
const migrationLockKey int64 = 0x6c61_6262_6974_6d67 // "labbitmg"

const trackingTableDDL = `CREATE TABLE IF NOT EXISTS labbit_schema_migrations (
    version bigint PRIMARY KEY,
    name text NOT NULL,
    checksum text NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now()
)`

// ErrMigrationState는 적용 이력이 현재 migration 파일과 맞지 않아 runner가 진행을 거부한 상태다.
// 자동 복구하지 않고 운영자가 원인을 확인해야 한다.
var ErrMigrationState = errors.New("migration 적용 이력이 현재 migration 파일과 맞지 않습니다")

type appliedMigration struct {
	Version  int64
	Checksum string
}

// Migrate는 아직 적용되지 않은 migration을 version 순서로 적용하고 이번 실행에서 적용한 목록을 반환한다.
//
// 각 migration은 적용 기록과 함께 하나의 transaction으로 commit한다. 실패한 migration은 기록 없이
// 모두 rollback되므로 원인을 고친 뒤 다시 실행하면 실패한 migration부터 이어서 적용한다.
// 실패 전에 commit된 migration은 반환 목록에 포함되지만 오류가 있으면 전체 실행은 실패다.
func Migrate(ctx context.Context, conn *pgx.Conn, migrations []Migration, logger *slog.Logger) ([]Migration, error) {
	if len(migrations) == 0 {
		return nil, errors.New("적용할 migration 정의가 없습니다")
	}

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockKey); err != nil {
		return nil, fmt.Errorf("migration lock 획득 실패: %w", err)
	}
	defer func() {
		// 연결이 끊긴 경우 session 종료와 함께 lock도 해제되므로 unlock 실패는 무시한다.
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", migrationLockKey)
	}()

	if _, err := conn.Exec(ctx, trackingTableDDL); err != nil {
		return nil, fmt.Errorf("migration 기록 table 준비 실패: %w", err)
	}

	applied, err := readAppliedMigrations(ctx, conn)
	if err != nil {
		return nil, err
	}
	pending, err := planPending(migrations, applied)
	if err != nil {
		return nil, err
	}

	var done []Migration
	for _, migration := range pending {
		started := time.Now()
		logger.Info("migration 적용 시작",
			"migration_version", migration.Version,
			"migration_name", migration.Name,
		)
		if err := applyMigration(ctx, conn, migration); err != nil {
			return done, err
		}
		logger.Info("migration 적용 완료",
			"migration_version", migration.Version,
			"migration_name", migration.Name,
			"duration_ms", time.Since(started).Milliseconds(),
		)
		done = append(done, migration)
	}
	return done, nil
}

func readAppliedMigrations(ctx context.Context, q Querier) ([]appliedMigration, error) {
	rows, err := q.Query(ctx, "SELECT version, checksum FROM labbit_schema_migrations ORDER BY version")
	if err != nil {
		return nil, fmt.Errorf("migration 적용 기록 조회 실패: %w", err)
	}
	applied, err := pgx.CollectRows(rows, pgx.RowToStructByPos[appliedMigration])
	if err != nil {
		return nil, fmt.Errorf("migration 적용 기록 조회 실패: %w", err)
	}
	return applied, nil
}

// planPending은 적용 기록과 migration 파일을 대조해 적용할 목록을 계산한다.
// 이미 적용된 파일의 변경이나 더 높은 version 뒤에 끼어든 미적용 migration은 진행하지 않는다.
// 이 build가 모르는 더 최신 version 기록은 rollback 배포 등에서 생길 수 있으므로 허용한다.
func planPending(migrations []Migration, applied []appliedMigration) ([]Migration, error) {
	appliedChecksums := make(map[int64]string, len(applied))
	var latestApplied int64
	for _, row := range applied {
		appliedChecksums[row.Version] = row.Checksum
		latestApplied = max(latestApplied, row.Version)
	}

	var pending []Migration
	for _, migration := range migrations {
		checksum, ok := appliedChecksums[migration.Version]
		if !ok {
			if migration.Version < latestApplied {
				return nil, fmt.Errorf("%w: 미적용 %s보다 높은 version이 이미 적용되었습니다(최고 version %d)",
					ErrMigrationState, migration.Name, latestApplied)
			}
			pending = append(pending, migration)
			continue
		}
		if checksum != migration.Checksum {
			return nil, fmt.Errorf("%w: 이미 적용된 %s의 내용이 변경되었습니다",
				ErrMigrationState, migration.Name)
		}
	}
	return pending, nil
}

func applyMigration(ctx context.Context, conn *pgx.Conn, migration Migration) (err error) {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("%s transaction 시작 실패: %w", migration.Name, err)
	}
	defer func() {
		if err == nil {
			return
		}
		// 실패한 migration의 DDL과 적용 기록을 함께 되돌린다. 연결이 끊긴 경우 서버가 rollback한다.
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()

	// 본문은 여러 statement이므로 simple protocol로 한 번에 실행한다.
	if err = conn.PgConn().Exec(ctx, migration.body).Close(); err != nil {
		return fmt.Errorf("%s 적용 실패: %w", migration.Name, err)
	}
	// 본문이 runner transaction을 끝냈다면 적용 기록을 같은 transaction에 남길 수 없으므로 실패시킨다.
	if conn.PgConn().TxStatus() != 'T' {
		return fmt.Errorf("%w: %s 실행 중 runner transaction이 종료되었습니다", ErrMigrationState, migration.Name)
	}
	if _, err = tx.Exec(ctx,
		"INSERT INTO labbit_schema_migrations (version, name, checksum) VALUES ($1, $2, $3)",
		migration.Version, migration.Name, migration.Checksum,
	); err != nil {
		return fmt.Errorf("%s 적용 기록 실패: %w", migration.Name, err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("%s commit 실패: %w", migration.Name, err)
	}
	return nil
}
