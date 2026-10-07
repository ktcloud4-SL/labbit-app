package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/terminal"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
)

func TestControlConfigFromEnvironment_ValidatesControlInputs(t *testing.T) {
	t.Setenv("LABBIT_CONNECTOR_ID", "")
	t.Setenv("LABBIT_SAAS_BASE_URL", "https://saas.example.com")
	t.Setenv("LABBIT_CONNECTOR_CREDENTIAL_FILE", "unused")
	if _, err := controlConfigFromEnvironment(); err == nil {
		t.Fatal("missing LABBIT_CONNECTOR_ID was accepted")
	}

	credentialFile := filepath.Join(t.TempDir(), "connector-credential")
	if err := os.WriteFile(credentialFile, []byte("connector-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LABBIT_CONNECTOR_ID", "connector-1")
	t.Setenv("LABBIT_CONNECTOR_CREDENTIAL_FILE", credentialFile)
	config, err := controlConfigFromEnvironment()
	if err != nil {
		t.Fatalf("controlConfigFromEnvironment() error = %v", err)
	}
	if config.BaseURL != "https://saas.example.com" || config.CredentialFile != credentialFile || config.RuntimeID == "" {
		t.Fatalf("control config = %+v", config)
	}
}

func TestRunControl_KeepsWSSConnectedWhenProviderAuthenticationFails(t *testing.T) {
	mockSaaS := mock.NewMockSaaS("connector-token")
	defer mockSaaS.Close()
	attempts := 0
	lazy := newLazyProvider("provider-connection-1", func(context.Context) (runtimeProvider, error) {
		attempts++
		return nil, errors.New("keystone authentication failed")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	go func() {
		done <- runControl(ctx, wss.Config{
			BaseURL: mockSaaS.URL(), Credential: "connector-token", RuntimeID: "runtime-1", AllowInsecure: true,
		}, lazy, "connector-1", logger)
	}()

	waitForMessageType(t, mockSaaS, protocol.MessageTypeHello, 2*time.Second)
	request := protocol.ProviderRequestMessage{
		BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeProviderRequest, MessageID: "provider-request-1", SentAt: time.Now().UTC()},
		Payload: protocol.ProviderRequestPayload{
			RequestType: protocol.ProviderRequestValidateConnection, ProviderConnectionID: "provider-connection-1",
		},
	}
	if err := mockSaaS.SendRaw(request); err != nil {
		t.Fatalf("SendRaw() error = %v", err)
	}
	raw := waitForMessageType(t, mockSaaS, protocol.MessageTypeProviderResponse, 2*time.Second)
	var response protocol.ProviderResponseMessage
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if response.Payload.Outcome != protocol.OutcomeFailed || response.Payload.Error == nil || response.Payload.Error.Code != "ERR_INFRA_OPENSTACK" {
		t.Fatalf("PROVIDER_RESPONSE = %+v", response.Payload)
	}
	if mockSaaS.ActiveConn() == nil {
		t.Fatal("Control WSS disconnected after Provider authentication failure")
	}
	if attempts != 1 {
		t.Fatalf("Provider initialization attempts = %d, want 1", attempts)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runControl() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runControl did not stop after cancellation")
	}
}

func waitForMessageType(t *testing.T, mockSaaS *mock.MockSaaS, messageType string, timeout time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, raw := range mockSaaS.ReceivedMessages() {
			var envelope protocol.BaseEnvelope
			if json.Unmarshal(raw, &envelope) == nil && envelope.Type == messageType {
				return raw
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("message type %s was not received", messageType)
	return nil
}

type recordSender struct {
	sent []any
}

func (r *recordSender) SendMessage(ctx context.Context, msg any) error {
	r.sent = append(r.sent, msg)
	return nil
}

func TestBuildConnector_ProductionTerminalWiring(t *testing.T) {
	sender := &recordSender{}
	mockProv := &provider.MockProvider{}

	connectorApp, err := BuildConnector(mockProv, sender)
	if err != nil {
		t.Fatalf("BuildConnector failed: %v", err)
	}

	if connectorApp.Handler == nil {
		t.Fatal("expected Handler to be initialized, got nil")
	}
	if connectorApp.TerminalManager == nil {
		t.Fatal("expected TerminalManager to be initialized, got nil")
	}
	if connectorApp.PTYFactory == nil {
		t.Fatal("expected PTYFactory to be initialized, got nil")
	}

	// TERMINAL_OPEN 요청을 Handler로 전달했을 때
	// "terminal manager or PTY factory not configured" 오류가 발생하지 않음을 검증
	openMsg := protocol.TerminalOpenMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:              protocol.MessageTypeTerminalOpen,
			MessageID:         "msg-open-prod-wiring",
			SentAt:            time.Now().UTC(),
			TerminalSessionID: "sess-prod-wiring",
			LabInstanceID:     "inst-prod-wiring",
			Generation:        1,
		},
		Payload: protocol.TerminalOpenPayload{
			TargetVmKey:      "vm-01",
			ProviderServerID: "srv-01",
			Cols:             80,
			Rows:             24,
		},
	}
	raw, err := json.Marshal(openMsg)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	// handleMessage 실행 (dial은 실패하더라도 manager/factory 미설정 에러가 아님을 확인)
	_ = connectorApp.Handler.HandleMessage(context.Background(), raw)

	if len(sender.sent) == 0 {
		t.Fatal("expected response message to be sent")
	}

	res, ok := sender.sent[0].(protocol.TerminalOpenResultMessage)
	if !ok {
		t.Fatalf("expected TerminalOpenResultMessage, got %T", sender.sent[0])
	}

	if res.Payload.Error != nil && res.Payload.Error.Message == "terminal manager or PTY factory not configured" {
		t.Fatalf("production handler failed with unconfigured terminal manager or PTY factory")
	}
}

func TestConnectorApp_GracefulShutdown(t *testing.T) {
	sender := &recordSender{}
	mockProv := &provider.MockProvider{}

	connectorApp, err := BuildConnector(mockProv, sender)
	if err != nil {
		t.Fatalf("BuildConnector failed: %v", err)
	}

	// 세션 하나 수동 등록
	session, _, err := connectorApp.TerminalManager.GetOrCreateSession(
		protocol.TerminalOpenPayload{
			TargetVmKey:      "vm-shutdown",
			ProviderServerID: "srv-shutdown",
			Cols:             80,
			Rows:             24,
		},
		protocol.BaseEnvelope{
			TerminalSessionID: "sess-shutdown",
			LabInstanceID:     "inst-shutdown",
			Generation:        1,
		},
		func() (terminal.PTYChannel, error) {
			return terminal.NewBufferPTY(80, 24), nil
		},
	)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	if session.Status == terminal.StatusClosed {
		t.Fatal("expected session to be created/active")
	}

	// CloseAll 호출 (Connector 종료 시의 동작)
	connectorApp.TerminalManager.CloseAll("SERVICE_RESTARTING")

	if session.Status != terminal.StatusClosed {
		t.Fatalf("expected session to be CLOSED after shutdown, got: %s", session.Status)
	}
	if connectorApp.TerminalManager.ActiveCount() != 0 {
		t.Fatalf("expected active count 0, got %d", connectorApp.TerminalManager.ActiveCount())
	}
}

type bareProvider struct{}

func (b *bareProvider) Provision(ctx context.Context, req provider.ProvisionRequest) (provider.OperationResult, error) {
	return provider.OperationResult{}, nil
}
func (b *bareProvider) Reset(ctx context.Context, req provider.ResetRequest) (provider.OperationResult, error) {
	return provider.OperationResult{}, nil
}
func (b *bareProvider) Cleanup(ctx context.Context, req provider.CleanupRequest) (provider.OperationResult, error) {
	return provider.OperationResult{}, nil
}
func (b *bareProvider) Reconcile(ctx context.Context, req provider.ReconcileRequest) (provider.ReconcileResult, error) {
	return provider.ReconcileResult{}, nil
}

func TestBuildConnector_ProductionContract_Validation(t *testing.T) {
	sender := &recordSender{}

	// Create temp dir for credentials and known_hosts
	tmpDir := t.TempDir()
	credFile := filepath.Join(tmpDir, "connector.credential")
	if err := os.WriteFile(credFile, []byte("prod-secret-token"), 0600); err != nil {
		t.Fatalf("failed to write cred file: %v", err)
	}
	knownHostsFile := filepath.Join(tmpDir, "known_hosts")
	if err := os.WriteFile(knownHostsFile, []byte("# empty known hosts\n"), 0600); err != nil {
		t.Fatalf("failed to write known_hosts: %v", err)
	}

	// 1. LABBIT_ENV=production, missing LABBIT_SAAS_BASE_URL
	t.Setenv("LABBIT_ENV", "production")
	t.Setenv("LABBIT_SAAS_BASE_URL", "")
	t.Setenv("LABBIT_CONNECTOR_CREDENTIAL_FILE", credFile)
	t.Setenv("LABBIT_OPENSTACK_SSH_KNOWN_HOSTS_FILE", knownHostsFile)

	_, err := BuildConnector(&provider.MockProvider{}, sender)
	if err == nil || !strings.Contains(err.Error(), "LABBIT_SAAS_BASE_URL is required in production") {
		t.Fatalf("expected error for missing LABBIT_SAAS_BASE_URL in production, got: %v", err)
	}

	// 2. HTTP prohibited in production (TLS required)
	t.Setenv("LABBIT_SAAS_BASE_URL", "http://saas.example.com")
	_, err = BuildConnector(&provider.MockProvider{}, sender)
	if err == nil || !strings.Contains(err.Error(), "http scheme is prohibited in production") {
		t.Fatalf("expected error for http scheme in production, got: %v", err)
	}

	// 3. Missing credential in production
	t.Setenv("LABBIT_SAAS_BASE_URL", "https://saas.example.com")
	t.Setenv("LABBIT_CONNECTOR_CREDENTIAL_FILE", "")
	t.Setenv("LABBIT_CONNECTOR_CREDENTIAL", "")
	_, err = BuildConnector(&provider.MockProvider{}, sender)
	if err == nil || !strings.Contains(err.Error(), "connector credential is required in production") {
		t.Fatalf("expected error for missing credential in production, got: %v", err)
	}

	// 4. Missing known_hosts in production
	t.Setenv("LABBIT_CONNECTOR_CREDENTIAL_FILE", credFile)
	t.Setenv("LABBIT_OPENSTACK_SSH_KNOWN_HOSTS_FILE", "")
	_, err = BuildConnector(&provider.MockProvider{}, sender)
	if err == nil || !strings.Contains(err.Error(), "LABBIT_OPENSTACK_SSH_KNOWN_HOSTS_FILE is required in production") {
		t.Fatalf("expected error for missing known hosts in production, got: %v", err)
	}

	// 5. Provider without ServerAddressResolver in production
	t.Setenv("LABBIT_OPENSTACK_SSH_KNOWN_HOSTS_FILE", knownHostsFile)
	_, err = BuildConnector(&bareProvider{}, sender)
	if err == nil || !strings.Contains(err.Error(), "does not implement ServerAddressResolver") {
		t.Fatalf("expected error for provider missing ServerAddressResolver, got: %v", err)
	}

	// 6. Complete valid production configuration
	mockProv := &provider.MockProvider{}
	app, err := BuildConnector(mockProv, sender)
	if err != nil {
		t.Fatalf("expected BuildConnector to succeed with valid production config, got: %v", err)
	}
	if app == nil {
		t.Fatal("expected non-nil app")
	}
}

func TestBuildConnector_AddressResolver_Wiring(t *testing.T) {
	sender := &recordSender{}
	resolved := false
	mockProv := &provider.MockProvider{
		ResolveServerAddressFunc: func(ctx context.Context, targetVmKey, serverID string) (string, error) {
			resolved = true
			if targetVmKey != "vm-test-resolver" || serverID != "srv-test-resolver" {
				t.Errorf("unexpected args: targetVmKey=%s, serverID=%s", targetVmKey, serverID)
			}
			// Return dummy port unreachable address
			return "127.0.0.1:59998", nil
		},
	}

	app, err := BuildConnector(mockProv, sender)
	if err != nil {
		t.Fatalf("BuildConnector failed: %v", err)
	}

	// Invoking PTYFactory should call ResolveServerAddressFunc
	_, _ = app.PTYFactory("vm-test-resolver", "srv-test-resolver", 80, 24)
	if !resolved {
		t.Fatal("expected ResolveServerAddressFunc to be called during PTYFactory invocation")
	}
}
