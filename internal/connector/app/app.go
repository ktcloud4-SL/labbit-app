package app

import (
	"context"
	"fmt"
	"net/url"
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
	environment := envOrDefault("LABBIT_ENVIRONMENT", envOrDefault("LABBIT_ENV", "development"))
	isProduction := environment == "production" || strings.ToLower(environment) == "prod"

	// 1. Runtime Contract SSOT: SaaS Base URL 및 Credential 로딩
	saasBaseURL := strings.TrimSpace(os.Getenv("LABBIT_SAAS_BASE_URL"))
	if isProduction && saasBaseURL == "" {
		return nil, fmt.Errorf("LABBIT_SAAS_BASE_URL is required in production")
	}
	if saasBaseURL == "" {
		saasBaseURL = "http://localhost:8080"
	}

	var credential string
	credFile := strings.TrimSpace(os.Getenv("LABBIT_CONNECTOR_CREDENTIAL_FILE"))
	if credFile != "" {
		data, err := os.ReadFile(credFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read connector credential file: %w", err)
		}
		credential = strings.TrimSpace(string(data))
	} else if isProduction {
		// 프로덕션 모드에서는 Runtime Contract SSOT에 따라 반드시 LABBIT_CONNECTOR_CREDENTIAL_FILE 파일 주입만 허용
		return nil, fmt.Errorf("connector credential is required in production: set LABBIT_CONNECTOR_CREDENTIAL_FILE")
	} else if cred := strings.TrimSpace(os.Getenv("LABBIT_CONNECTOR_CREDENTIAL")); cred != "" {
		// 개발/테스트 환경에서 편의를 위해 제한적으로 리터럴 허용
		credential = cred
	} else {
		credential = "local-dev-credential"
	}

	// 2. Terminal Data WSS Endpoint 도출
	terminalRelayURL := strings.TrimSpace(os.Getenv("LABBIT_TERMINAL_RELAY_URL"))
	if terminalRelayURL == "" {
		u, err := url.Parse(saasBaseURL)
		if err != nil {
			return nil, fmt.Errorf("invalid LABBIT_SAAS_BASE_URL: %w", err)
		}
		switch u.Scheme {
		case "https":
			u.Scheme = "wss"
		case "http":
			if isProduction {
				return nil, fmt.Errorf("http scheme is prohibited in production: TLS is required")
			}
			u.Scheme = "ws"
		default:
			if !strings.HasPrefix(u.Scheme, "ws") {
				u.Scheme = "wss"
			}
		}
		u.Path = "/connector/v1/terminal-data"
		terminalRelayURL = u.String()
	}

	runtimeID := envOrDefault("LABBIT_CONNECTOR_RUNTIME_ID", envOrDefault("LABBIT_CONNECTOR_ID", "connector-runtime-01"))

	// 3. Provider 기반 Management Address Resolver 배선 (Reviewer 4번 지적 사항)
	var addressResolver func(ctx context.Context, targetVmKey, serverID string) (string, error)
	if res, ok := p.(provider.ServerAddressResolver); ok {
		addressResolver = func(ctx context.Context, targetVmKey, serverID string) (string, error) {
			return res.ResolveServerAddress(ctx, targetVmKey, serverID)
		}
	} else if isProduction {
		return nil, fmt.Errorf("provider %T does not implement ServerAddressResolver: required in production", p)
	} else {
		addressResolver = func(ctx context.Context, targetVmKey, serverID string) (string, error) {
			return "127.0.0.1", nil
		}
	}

	// 4. SSH PTY 설정 (Reviewer 1번 지적 사항: known_hosts 필수 및 InsecureIgnoreHostKey 제거)
	knownHostsFile := strings.TrimSpace(os.Getenv("LABBIT_OPENSTACK_SSH_KNOWN_HOSTS_FILE"))
	if isProduction && knownHostsFile == "" {
		return nil, fmt.Errorf("LABBIT_OPENSTACK_SSH_KNOWN_HOSTS_FILE is required in production")
	}

	sshCfg := terminal.SSHConfig{
		Username:             envOrDefault("LABBIT_OPENSTACK_SSH_USERNAME", "ubuntu"),
		PrivateKeyFile:       os.Getenv("LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE"),
		KnownHostsFile:       knownHostsFile,
		AllowInsecureHostKey: !isProduction,
		AddressResolver:      addressResolver,
	}
	ptyFactory := terminal.NewSSHPTYFactory(sshCfg)

	sessionMgr := terminal.NewSessionManager(60*time.Second, nil)

	termCfg := terminal.DataWSSClientConfig{
		EndpointURL:   terminalRelayURL,
		Credential:    credential,
		RuntimeID:     runtimeID,
		DialTimeout:   10 * time.Second,
		AllowInsecure: !isProduction,
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
// 주의: Run() 내의 MockProvider 사용은 LBT-82 OpenStack Provider 실제 배선 전 단계의 Skeleton/Bootstrap 경계입니다.
// 실제 프로덕션 구동 시에는 구체 OpenStack Provider 구현체가 주입되어야 합니다.
func Run(ctx context.Context) error {
	environment := envOrDefault("LABBIT_ENVIRONMENT", "development")
	logLevel := envOrDefault("LABBIT_LOG_LEVEL", "info")
	logger := observability.NewJSONLogger("labbit-connector", "bootstrap", environment, logLevel)

	logger.Info("Labbit Connector 스켈레톤 시작",
		"connector_id", strings.TrimSpace(os.Getenv("LABBIT_CONNECTOR_ID")),
	)

	// Production Wiring 초기화 (후속 Provider 배선 전까지 MockProvider 스켈레톤 사용)
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
