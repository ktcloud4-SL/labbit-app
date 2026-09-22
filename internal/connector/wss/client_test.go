package wss_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ktcloud4-SL/rabbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/rabbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/rabbit-app/internal/connector/wss"
)

func TestClient_DialAndHello_Success(t *testing.T) {
	// 1. Mock SaaS 기동 (Bearer 토큰 설정)
	validToken := "test-connector-secret-key-1234"
	mockSaaS := mock.NewMockSaaS(validToken)
	defer mockSaaS.Close()

	// 2. Client 설정 및 생성
	cfg := wss.Config{
		BaseURL:          mockSaaS.URL(),
		Credential:       validToken,
		ConnectorVersion: "0.1.0-test",
		Capabilities:     []string{"terminal.v1"},
	}
	client := wss.NewClient(cfg)
	defer client.Close()

	// 3. Dial (WSS 연결 + Bearer 인증 + Subprotocol 협상)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Dial(ctx); err != nil {
		t.Fatalf("Dial failed: %v", err)
	}

	if client.Conn() == nil {
		t.Fatal("expected non-nil connection after Dial")
	}

	// Subprotocol 검증
	if subproto := client.Conn().Subprotocol(); subproto != protocol.SubprotocolControl {
		t.Errorf("expected subprotocol %s, got %s", protocol.SubprotocolControl, subproto)
	}

	// 4. HELLO 전송 및 HELLO_ACK 수신 핸드셰이크
	ack, err := client.SendHello(ctx)
	if err != nil {
		t.Fatalf("SendHello failed: %v", err)
	}

	if ack == nil {
		t.Fatal("expected non-nil HelloAckPayload")
	}

	if ack.HeartbeatIntervalSeconds != 15 {
		t.Errorf("expected HeartbeatIntervalSeconds 15, got %d", ack.HeartbeatIntervalSeconds)
	}

	if ack.OfflineTimeoutSeconds != 45 {
		t.Errorf("expected OfflineTimeoutSeconds 45, got %d", ack.OfflineTimeoutSeconds)
	}
}

func TestClient_Dial_AuthFailure(t *testing.T) {
	mockSaaS := mock.NewMockSaaS("correct-token")
	defer mockSaaS.Close()

	cfg := wss.Config{
		BaseURL:    mockSaaS.URL(),
		Credential: "wrong-token-invalid",
	}
	client := wss.NewClient(cfg)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := client.Dial(ctx)
	if err == nil {
		t.Fatal("expected error on invalid credentials, but Dial succeeded")
	}
}

func TestClient_CredentialFile(t *testing.T) {
	token := "file-injected-token-5678"
	tmpDir := t.TempDir()
	credFile := filepath.Join(tmpDir, "connector_credential")
	if err := os.WriteFile(credFile, []byte(token+"\n"), 0600); err != nil {
		t.Fatalf("failed to write tmp credential file: %v", err)
	}

	mockSaaS := mock.NewMockSaaS(token)
	defer mockSaaS.Close()

	cfg := wss.Config{
		BaseURL:        mockSaaS.URL(),
		CredentialFile: credFile,
	}
	client := wss.NewClient(cfg)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Dial(ctx); err != nil {
		t.Fatalf("Dial with CredentialFile failed: %v", err)
	}

	ack, err := client.SendHello(ctx)
	if err != nil {
		t.Fatalf("SendHello failed: %v", err)
	}

	if ack.HeartbeatIntervalSeconds != 15 {
		t.Errorf("expected 15s heartbeat interval, got %d", ack.HeartbeatIntervalSeconds)
	}
}

func TestClient_ResolveEndpoint(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"http://saas.example.com", "ws://saas.example.com/connector/v1/control"},
		{"https://saas.example.com", "wss://saas.example.com/connector/v1/control"},
		{"https://saas.example.com/custom", "wss://saas.example.com/custom/connector/v1/control"},
		{"wss://saas.example.com/connector/v1/control", "wss://saas.example.com/connector/v1/control"},
	}

	for _, tc := range tests {
		cfg := wss.Config{BaseURL: tc.input}
		resolved, err := cfg.ResolveEndpoint()
		if err != nil {
			t.Errorf("ResolveEndpoint(%s) failed: %v", tc.input, err)
			continue
		}
		if resolved != tc.expected {
			t.Errorf("ResolveEndpoint(%s) = %s; want %s", tc.input, resolved, tc.expected)
		}
	}
}
