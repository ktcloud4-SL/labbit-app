package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrSchemaIncompatible은 DB schema가 이 application build의 호환 범위를 벗어난 상태다.
var ErrSchemaIncompatible = errors.New("PostgreSQL schema가 이 application version과 호환되지 않습니다")

// Querier는 pgx Pool/Conn/Tx가 공통으로 제공하는 조회 경계다.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// CheckSchema는 이 build에 포함된 migration이 모두 같은 내용으로 적용되었는지 확인한다.
//
// Migration은 application rollout 전에 별도 단계로 실행되므로 rollout 중인 이전 version replica는
// 자신이 모르는 더 최신 migration을 만날 수 있다. append-only이며 이전 version과 호환되는
// migration 규칙을 전제로 더 최신 version 기록은 호환 범위로 허용한다.
// 연결·조회 실패는 ErrSchemaIncompatible이 아닌 오류로 반환한다.
func CheckSchema(ctx context.Context, q Querier, migrations []Migration) error {
	applied, err := readAppliedMigrations(ctx, q)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P01" {
			return fmt.Errorf("%w: labbit_schema_migrations가 없습니다", ErrSchemaIncompatible)
		}
		return err
	}

	appliedChecksums := make(map[int64]string, len(applied))
	for _, row := range applied {
		appliedChecksums[row.Version] = row.Checksum
	}
	for _, migration := range migrations {
		checksum, ok := appliedChecksums[migration.Version]
		if !ok {
			return fmt.Errorf("%w: %s 적용 기록이 없습니다", ErrSchemaIncompatible, migration.Name)
		}
		if checksum != migration.Checksum {
			return fmt.Errorf("%w: 적용된 %s의 내용이 현재 build와 다릅니다", ErrSchemaIncompatible, migration.Name)
		}
	}
	return nil
}
