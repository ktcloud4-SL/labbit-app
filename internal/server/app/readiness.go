package app

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"

	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
)

// databaseReadiness는 /readyz 요청마다 PostgreSQL 연결과 schema 호환성을 확인한다.
// Probe 주기마다 같은 실패를 반복 기록하지 않도록 상태가 바뀔 때만 로그를 남긴다.
type databaseReadiness struct {
	querier    postgres.Querier
	migrations []postgres.Migration
	logger     *slog.Logger
	failing    atomic.Bool
}

func (d *databaseReadiness) check(ctx context.Context) error {
	err := postgres.CheckSchema(ctx, d.querier, d.migrations)
	if err != nil {
		if !d.failing.Swap(true) {
			d.logger.Warn("PostgreSQL readiness 실패",
				"reason", databaseFailureReason(err),
				"error", err.Error(),
			)
		}
		return err
	}
	if d.failing.Swap(false) {
		d.logger.Info("PostgreSQL readiness 회복")
	}
	return nil
}

func databaseFailureReason(err error) string {
	if errors.Is(err, postgres.ErrSchemaIncompatible) {
		return "schema_incompatible"
	}
	return "database_unavailable"
}
