// labbit-migrate는 application rollout 전에 별도 단계로 SQL Migration을 적용한다.
// labbit-server는 startup 시 Migration을 실행하지 않는다.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	migrationfiles "github.com/ktcloud4-SL/labbit-app/db/migrations"
	"github.com/ktcloud4-SL/labbit-app/internal/observability"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
)

const usage = "사용법: labbit-migrate up"

func main() {
	if len(os.Args) != 2 || os.Args[1] != "up" {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}

	environment := strings.TrimSpace(os.Getenv("LABBIT_ENVIRONMENT"))
	logEnvironment := environment
	if logEnvironment == "" {
		logEnvironment = "unknown"
	}
	logger := observability.NewJSONLogger("labbit-migrate", "migration", logEnvironment, os.Getenv("LABBIT_LOG_LEVEL"))

	if err := run(environment, logger); err != nil {
		logger.Error("migration 실패", "error", err.Error())
		os.Exit(1)
	}
}

func run(environment string, logger *slog.Logger) error {
	if environment == "" {
		return errors.New("LABBIT_ENVIRONMENT가 필요합니다")
	}
	dsn, err := postgres.LoadDSN(environment)
	if err != nil {
		return err
	}
	migrations, err := postgres.LoadMigrations(migrationfiles.Files)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	conn, err := postgres.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())

	applied, err := postgres.Migrate(ctx, conn, migrations, logger)
	if err != nil {
		return err
	}
	logger.Info("migration 완료",
		"applied_count", len(applied),
		"latest_known_version", migrations[len(migrations)-1].Version,
	)
	return nil
}
