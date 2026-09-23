package main

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ktcloud4-SL/labbit-app/internal/observability"
	"github.com/ktcloud4-SL/labbit-app/internal/server/app"
)

func main() {
	if err := run(); err != nil {
		logStartupFailure(err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := app.LoadConfig()
	if err != nil {
		return err
	}

	// Kubernetes 종료와 로컬 Ctrl+C를 같은 graceful shutdown 경로로 처리한다.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	return app.Run(ctx, cfg)
}

func logStartupFailure(err error) {
	environment := strings.TrimSpace(os.Getenv("LABBIT_ENVIRONMENT"))
	if environment == "" {
		// 환경 설정 자체가 실패한 경우에도 Runtime Contract의 필수 필드를 유지한다.
		environment = "unknown"
	}

	logger := observability.NewJSONLoggerTo(
		os.Stderr,
		"labbit-server",
		"bootstrap",
		environment,
		os.Getenv("LABBIT_LOG_LEVEL"),
	)
	logger.Error("Labbit 서버 시작 실패", "error", err.Error())
}
