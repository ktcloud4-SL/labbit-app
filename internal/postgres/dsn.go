package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrDSNNotConfigured는 DSN 입력이 하나도 설정되지 않은 상태다. 필요 여부는 호출자가 판단한다.
var ErrDSNNotConfigured = errors.New("LABBIT_DATABASE_DSN_FILE 또는 LABBIT_DATABASE_DSN이 필요합니다")

// DSN parser 오류는 connection string 일부를 포함할 수 있으므로 고정 문구만 반환한다.
var errInvalidDSN = errors.New("PostgreSQL DSN 형식이 올바르지 않습니다")

// LoadDSN은 Runtime Contract의 DSN 입력을 읽는다.
//
// LABBIT_DATABASE_DSN_FILE이 설정되면 LABBIT_DATABASE_DSN보다 우선한다.
// production에서는 file 입력이 필수이며 LABBIT_DATABASE_DSN은 local development escape hatch다.
// 반환값은 Secret이므로 로그·오류 메시지에 기록하지 않는다.
func LoadDSN(environment string) (string, error) {
	if path := strings.TrimSpace(os.Getenv("LABBIT_DATABASE_DSN_FILE")); path != "" {
		content, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("LABBIT_DATABASE_DSN_FILE을 읽을 수 없습니다: %w", err)
		}
		dsn := strings.TrimSpace(string(content))
		if dsn == "" {
			return "", errors.New("LABBIT_DATABASE_DSN_FILE이 비어 있습니다")
		}
		return dsn, nil
	}

	if environment == "production" {
		return "", errors.New("production에서는 LABBIT_DATABASE_DSN_FILE이 필요합니다")
	}
	if dsn := strings.TrimSpace(os.Getenv("LABBIT_DATABASE_DSN")); dsn != "" {
		return dsn, nil
	}
	return "", ErrDSNNotConfigured
}

// OpenPool은 pgx Pool을 만든다. 첫 사용 시점에 연결하므로 DB 일시 장애가 process 시작 실패가 되지 않고
// readiness로 드러난다.
func OpenPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errInvalidDSN
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("PostgreSQL pool 생성 실패: %w", err)
	}
	return pool, nil
}

// Connect는 migration runner처럼 하나의 session이 필요한 작업의 연결을 만든다.
func Connect(ctx context.Context, dsn string) (*pgx.Conn, error) {
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, errInvalidDSN
	}
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("PostgreSQL 연결 실패: %w", err)
	}
	return conn, nil
}
