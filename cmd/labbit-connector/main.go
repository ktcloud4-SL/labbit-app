package main

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ktcloud4-SL/rabbit-app/internal/connector/app"
	"github.com/ktcloud4-SL/rabbit-app/internal/observability"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	if err := app.Run(ctx); err != nil {
		logStartupFailure(err)
		os.Exit(1)
	}
}

func logStartupFailure(err error) {
	environment := strings.TrimSpace(os.Getenv("LABBIT_ENVIRONMENT"))
	if environment == "" {
		// Connector도 시작 실패 시 일반 문자열 대신 구조화 JSON event를 남긴다.
		environment = "unknown"
	}

	logger := observability.NewJSONLoggerTo(
		os.Stderr,
		"labbit-connector",
		"bootstrap",
		environment,
		os.Getenv("LABBIT_LOG_LEVEL"),
	)
	logger.Error("Labbit Connector 시작 실패", "error", err.Error())
}
