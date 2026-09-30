package app

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/terminal"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
	"github.com/ktcloud4-SL/labbit-app/internal/observability"
)

// ConnectorApp 은 Connector 프로세스의 전역 런타임 컴포넌트 묶음입니다.
type ConnectorApp struct {
	Handler         *wss.Handler
	TerminalManager *terminal.SessionManager
	PTYFactory      wss.PTYFactoryFunc
}

// BuildConnector 는 환경변수 및 Provider를 바탕으로 실제 Production 컴포넌트(SessionManager, SSHPTY, TerminalDataWSS)를 Wiring합니다.
func BuildConnector(p provider.Provider, sender wss.MessageSender) (*ConnectorApp, error) {
	environment := envOrDefault("LABBIT_ENVIRONMENT", "development")
	runtimeID := envOrDefault("LABBIT_CONNECTOR_RUNTIME_ID", "connector-runtime-01")
	terminalRelayURL := envOrDefault("LABBIT_TERMINAL_RELAY_URL", "wss://localhost:8443/connector/v1/terminal-data")
	credential := envOrDefault("LABBIT_CONNECTOR_CREDENTIAL", "local-dev-credential")

	sessionMgr := terminal.NewSessionManager(60*time.Second, nil)

	sshCfg := terminal.SSHConfig{
		Username:        envOrDefault("LABBIT_OPENSTACK_SSH_USERNAME", "ubuntu"),
		PrivateKeyFile:  os.Getenv("LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE"),
		KnownHostsFile:  os.Getenv("LABBIT_OPENSTACK_SSH_KNOWN_HOSTS_FILE"),
		InsecureHostKey: environment != "production",
	}
	ptyFactory := terminal.NewSSHPTYFactory(sshCfg)

	termCfg := terminal.DataWSSClientConfig{
		EndpointURL: terminalRelayURL,
		Credential:  credential,
		RuntimeID:   runtimeID,
		DialTimeout: 10 * time.Second,
	}

	handler := wss.NewHandler(p, sender)
	handler.SetTerminalManager(sessionMgr, ptyFactory, termCfg)

	return &ConnectorApp{
		Handler:         handler,
		TerminalManager: sessionMgr,
		PTYFactory:      ptyFactory,
	}, nil
}

// Run 은 Connector 프로세스의 lifecycle과 Graceful Shutdown을 제공합니다.
func Run(ctx context.Context) error {
	environment := envOrDefault("LABBIT_ENVIRONMENT", "development")
	logLevel := envOrDefault("LABBIT_LOG_LEVEL", "info")
	logger := observability.NewJSONLogger("labbit-connector", "bootstrap", environment, logLevel)

	logger.Info("Labbit Connector 스켈레톤 시작",
		"connector_id", strings.TrimSpace(os.Getenv("LABBIT_CONNECTOR_ID")),
	)

	// Production Wiring 초기화
	connectorApp, err := BuildConnector(&provider.MockProvider{}, nil)
	if err != nil {
		return err
	}

	// Connector는 public inbound listener를 열지 않는다.
	// 실제 구현은 고객망에서 SaaS 443으로 outbound WSS를 생성한다.
	<-ctx.Done()
	logger.Info("Connector 종료 신호 수신, 활성 터미널 세션 정리 시작")

	if connectorApp != nil && connectorApp.TerminalManager != nil {
		connectorApp.TerminalManager.CloseAll("SERVICE_RESTARTING")
	}

	logger.Info("Connector 정상 종료 완료")
	return nil
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
