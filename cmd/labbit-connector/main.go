package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ktcloud4-SL/rabbit-app/internal/connector/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	if err := app.Run(ctx); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "labbit-connector 종료: %v\n", err)
		os.Exit(1)
	}
}
