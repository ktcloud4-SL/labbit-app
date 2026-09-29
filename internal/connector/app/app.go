package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider/openstack"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
	"github.com/ktcloud4-SL/labbit-app/internal/observability"
)

// Run starts the outbound Control WSS and connects Control messages to the
// customer-local OpenStack Provider. Provider authentication is lazy so a
// Provider outage does not incorrectly make the Connector appear OFFLINE.
func Run(ctx context.Context) error {
	environment := envOrDefault("LABBIT_ENVIRONMENT", "development")
	logLevel := envOrDefault("LABBIT_LOG_LEVEL", "info")
	logger := observability.NewJSONLogger("labbit-connector", "bootstrap", environment, logLevel)
	connectorID := strings.TrimSpace(os.Getenv("LABBIT_CONNECTOR_ID"))

	config, err := controlConfigFromEnvironment()
	if err != nil {
		return err
	}
	providerConnectionID := strings.TrimSpace(os.Getenv(openstackprovider.EnvProviderConnection))
	lazy := newLazyProvider(providerConnectionID, func(factoryContext context.Context) (runtimeProvider, error) {
		return openstackprovider.New(factoryContext, openstackprovider.ConfigFromEnvironment())
	})

	logger.Info("Labbit Connector 시작", "connector_id", connectorID)
	return runControl(ctx, config, lazy, connectorID, logger)
}

func controlConfigFromEnvironment() (wss.Config, error) {
	connectorID := strings.TrimSpace(os.Getenv("LABBIT_CONNECTOR_ID"))
	if connectorID == "" {
		return wss.Config{}, fmt.Errorf("LABBIT_CONNECTOR_ID is required")
	}
	config := wss.Config{
		BaseURL:        strings.TrimSpace(os.Getenv("LABBIT_SAAS_BASE_URL")),
		CredentialFile: strings.TrimSpace(os.Getenv("LABBIT_CONNECTOR_CREDENTIAL_FILE")),
		RuntimeID:      uuid.NewString(),
	}
	if _, err := config.ResolveEndpoint(); err != nil {
		return wss.Config{}, fmt.Errorf("invalid Connector Control endpoint configuration: %w", err)
	}
	if _, err := config.GetCredential(); err != nil {
		return wss.Config{}, fmt.Errorf("invalid Connector credential configuration: %w", err)
	}
	return config, nil
}

func runControl(ctx context.Context, config wss.Config, p runtimeProvider, connectorID string, logger *slog.Logger) error {
	handler := wss.NewHandler(p, nil)
	handler.SetOnError(func(error) {
		logger.Warn("Connector Control 메시지 처리 실패", "connector_id", connectorID)
	})
	supervisor := wss.NewSupervisor(config, handler, wss.NewDefaultBackoffPolicy())
	supervisor.SetOnConnected(func(ack *protocol.HelloAckPayload) {
		logger.Info("Connector Control WSS 연결 완료",
			"connector_id", connectorID,
			"heartbeat_interval_seconds", ack.HeartbeatIntervalSeconds,
			"offline_timeout_seconds", ack.OfflineTimeoutSeconds,
		)
	})
	supervisor.SetOnDisconnected(func(error) {
		logger.Warn("Connector Control WSS 연결 종료; 재연결 대기", "connector_id", connectorID)
	})

	err := supervisor.Run(ctx)
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		logger.Info("Connector 종료 신호 수신", "connector_id", connectorID)
		return nil
	}
	return err
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
