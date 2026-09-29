package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
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
