package app

import (
	"context"
	"os"
	"strings"

	"github.com/ktcloud4-SL/rabbit-app/internal/observability"
)

// Run은 Connector 프로세스의 최소 lifecycle만 제공한다.
// Control WSS, Provider Adapter, SSH/PTY는 각 개발 Story에서 별도 package로 추가한다.
func Run(ctx context.Context) error {
	environment := envOrDefault("LABBIT_ENVIRONMENT", "development")
	logLevel := envOrDefault("LABBIT_LOG_LEVEL", "info")
	logger := observability.NewJSONLogger("labbit-connector", "bootstrap", environment, logLevel)

	logger.Info("Labbit Connector 스켈레톤 시작",
		"connector_id", strings.TrimSpace(os.Getenv("LABBIT_CONNECTOR_ID")),
	)

	// Connector는 public inbound listener를 열지 않는다.
	// 실제 구현은 고객망에서 SaaS 443으로 outbound WSS를 생성한다.
	<-ctx.Done()
	logger.Info("Connector 종료 신호 수신")
	return nil
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
