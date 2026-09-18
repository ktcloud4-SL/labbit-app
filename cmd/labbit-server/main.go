package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ktcloud4-SL/rabbit-app/internal/server/app"
)

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "labbit-server 종료: %v\n", err)
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
