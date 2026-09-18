package observability

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// NewJSONLogger는 Runtime Contract의 stdout JSON 로그 형식을 위한 기본 로거를 만든다.
// 실제 correlation field는 각 요청/작업/세션의 식별자를 알게 된 시점에 With로 추가한다.
func NewJSONLogger(service, component, environment, level string) *slog.Logger {
	return NewJSONLoggerTo(os.Stdout, service, component, environment, level)
}

// NewJSONLoggerTo는 지정한 출력으로 Runtime Contract 형식의 JSON event를 기록한다.
// 정상 실행 로그는 stdout, 프로세스 시작 실패처럼 종료 직전의 진단 event는 stderr에 사용할 수 있다.
func NewJSONLoggerTo(output io.Writer, service, component, environment, level string) *slog.Logger {
	handler := slog.NewJSONHandler(output, &slog.HandlerOptions{
		Level: parseLevel(level),
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			// slog 기본 키(time/msg)를 Runtime Contract의 필드명(timestamp/message)에 맞춘다.
			switch attr.Key {
			case slog.TimeKey:
				attr.Key = "timestamp"
			case slog.MessageKey:
				attr.Key = "message"
			}
			return attr
		},
	})

	return slog.New(handler).With(
		"service", service,
		"component", component,
		"version", "dev",
		"environment", environment,
	)
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
