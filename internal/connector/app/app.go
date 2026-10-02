package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/preview"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider/openstack"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/terminal"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
	"github.com/ktcloud4-SL/labbit-app/internal/observability"
	"golang.org/x/crypto/ssh"
)

// ConnectorApp 은 Connector 프로세스의 전역 런타임 컴포넌트 묶음입니다.
type ConnectorApp struct {
	Handler         *wss.Handler
	TerminalManager *terminal.SessionManager
	PTYFactory      wss.PTYFactoryFunc
	PreviewManager  *preview.SessionManager
}

// BuildConnector 는 환경변수 및 Provider를 바탕으로 실제 Production 컴포넌트(SessionManager, SSHPTY, TerminalDataWSS)를 Wiring합니다.
func BuildConnector(p provider.Provider, sender wss.MessageSender) (*ConnectorApp, error) {
	return buildConnector(p, sender, nil)
}

type serverHostKeyProvider interface {
	SSHHostKeyCallback(context.Context, string) (ssh.HostKeyCallback, error)
}

func buildConnector(p provider.Provider, sender wss.MessageSender, controlConfig *wss.Config) (*ConnectorApp, error) {
	environment := envOrDefault("LABBIT_ENVIRONMENT", envOrDefault("LABBIT_ENV", "development"))
	isProduction := environment == "production" || strings.ToLower(environment) == "prod"

	// 1. Runtime Contract SSOT: SaaS Base URL 및 Credential 로딩
	saasBaseURL := strings.TrimSpace(os.Getenv("LABBIT_SAAS_BASE_URL"))
	if controlConfig != nil {
		saasBaseURL = strings.TrimSpace(controlConfig.BaseURL)
	}
	if isProduction && saasBaseURL == "" {
		return nil, fmt.Errorf("LABBIT_SAAS_BASE_URL is required in production")
	}
	if saasBaseURL == "" {
		saasBaseURL = "http://localhost:8080"
	}

	var credential string
	credFile := strings.TrimSpace(os.Getenv("LABBIT_CONNECTOR_CREDENTIAL_FILE"))
	if controlConfig != nil {
		credFile = controlConfig.CredentialFile
		var err error
		credential, err = controlConfig.GetCredential()
		if err != nil {
			return nil, fmt.Errorf("invalid Connector credential configuration")
		}
	} else if credFile != "" {
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
	if controlConfig != nil {
		runtimeID = controlConfig.RuntimeID
	}

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
	if verifier, ok := p.(serverHostKeyProvider); ok {
		// Reuse the Provider lifecycle's per-server pin, not the current IP.
		// A terminal request must never enroll a previously unknown host key.
		ptyFactory = func(targetVmKey, serverID string, cols, rows int) (terminal.PTYChannel, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			callback, err := verifier.SSHHostKeyCallback(ctx, serverID)
			if err != nil || callback == nil {
				return nil, fmt.Errorf("Provider SSH host key could not be verified")
			}
			cfg := sshCfg
			cfg.HostKeyCallback = callback
			cfg.AllowInsecureHostKey = false
			pty, err := terminal.NewSSHPTYFactory(cfg)(targetVmKey, serverID, cols, rows)
			if err != nil {
				return nil, fmt.Errorf("Workspace VM SSH/PTY could not be opened")
			}
			return pty, nil
		}
	}

	sessionMgr := terminal.NewSessionManager(60*time.Second, nil)

	termCfg := terminal.DataWSSClientConfig{
		EndpointURL:    terminalRelayURL,
		Credential:     credential,
		CredentialFile: credFile,
		RuntimeID:      runtimeID,
		DialTimeout:    10 * time.Second,
		AllowInsecure:  !isProduction,
	}

	// 5. Preview Gateway Endpoint 및 SessionManager 배선
	previewGatewayURL := strings.TrimSpace(os.Getenv("LABBIT_PREVIEW_GATEWAY_URL"))
	if previewGatewayURL == "" {
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
		u.Path = "/connector/v1/preview-data"
		previewGatewayURL = u.String()
	}

	forwarder := preview.NewDirectTCPForwarder(addressResolver)
	previewMgr := preview.NewSessionManager(forwarder, nil)
	prevCfg := preview.DataWSSClientConfig{
		EndpointURL:    previewGatewayURL,
		Credential:     credential,
		CredentialFile: credFile,
		RuntimeID:      runtimeID,
		DialTimeout:    10 * time.Second,
		AllowInsecure:  !isProduction,
	}

	handler := wss.NewHandler(p, sender)
	handler.SetTerminalManager(sessionMgr, ptyFactory, termCfg)
	handler.SetPreviewManager(previewMgr, prevCfg)

	return &ConnectorApp{
		Handler:         handler,
		TerminalManager: sessionMgr,
		PTYFactory:      ptyFactory,
		PreviewManager:  previewMgr,
	}, nil
}

// Run 은 Connector 프로세스의 lifecycle과 Graceful Shutdown을 제공합니다.
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
	connectorApp, err := buildConnector(p, nil, &config)
	if err != nil {
		return err
	}
	defer connectorApp.TerminalManager.CloseAll("SERVICE_RESTARTING")
	if connectorApp.PreviewManager != nil {
		defer connectorApp.PreviewManager.CloseAll("SERVICE_RESTARTING")
	}
	handler := connectorApp.Handler
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

	err = supervisor.Run(ctx)
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
