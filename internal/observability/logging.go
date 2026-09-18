package observability

import (
	"log/slog"
	"os"
	"strings"
)

// NewJSONLogger는 Runtime Contract의 stdout/stderr JSON 로그 형식을 위한 최소 로거를 만든다.
// 실제 correlation field는 각 요청/작업/세션의 식별자를 알게 된 시점에 With로 추가한다.
func NewJSONLogger(service, component, environment, level string) *slog.Logger {
	var slogLevel slog.Level
	switch strings.ToLower(level) {
	case "debug":
		slogLevel = slog.LevelDebug
	case "warn", "warning":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slogLevel})
	return slog.New(handler).With(
		"service", service,
		"component", component,
		"version", "dev",
		"environment", environment,
	)
}
