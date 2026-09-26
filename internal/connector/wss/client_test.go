package wss_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
)

func TestClient_DialAndHello_Success(t *testing.T) {
	// 1. Mock SaaS 기동 (Bearer 토큰 설정)
	validToken := "test-connector-secret-key-1234"
	mockSaaS := mock.NewMockSaaS(validToken)
	defer mockSaaS.Close()

	// 2. Client 설정 및 생성 (Mock SaaS loopback 테스트용 AllowInsecure 활성화)
	cfg := wss.Config{
		BaseURL:          mockSaaS.URL(),
		Credential:       validToken,
		ConnectorVersion: "0.1.0-test",
		Capabilities:     []string{"terminal.v1"},
		AllowInsecure:    true,
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
		BaseURL:       mockSaaS.URL(),
		Credential:    "wrong-token-invalid",
		AllowInsecure: true,
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
		AllowInsecure:  true,
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
		name          string
		input         string
		allowInsecure bool
		expected      string
		expectError   bool
	}{
		{
			name:        "Runtime https URL resolves to wss",
			input:       "https://saas.example.com",
			expected:    "wss://saas.example.com/connector/v1/control",
			expectError: false,
		},
		{
			name:        "Runtime custom path https URL",
			input:       "https://saas.example.com/custom",
			expected:    "wss://saas.example.com/custom/connector/v1/control",
			expectError: false,
		},
		{
			name:        "Runtime direct wss URL",
			input:       "wss://saas.example.com/connector/v1/control",
			expected:    "wss://saas.example.com/connector/v1/control",
			expectError: false,
		},
		{
			name:        "Runtime plaintext http is blocked without AllowInsecure",
			input:       "http://saas.example.com",
			expectError: true,
		},
		{
			name:        "Runtime plaintext ws is blocked without AllowInsecure",
			input:       "ws://saas.example.com",
			expectError: true,
		},
		{
			name:          "Non-loopback plaintext http is blocked even with AllowInsecure",
			input:         "http://saas.example.com",
			allowInsecure: true,
			expectError:   true,
		},
		{
			name:        "ws://localhost is blocked in runtime without AllowInsecure",
			input:       "ws://localhost:8080",
			expectError: true,
		},
		{
			name:          "ws://localhost is allowed with AllowInsecure (test-only boundary)",
			input:         "ws://localhost:8080",
			allowInsecure: true,
			expected:      "ws://localhost:8080/connector/v1/control",
			expectError:   false,
		},
		{
			name:          "http://127.0.0.1 is allowed with AllowInsecure (test-only boundary)",
			input:         "http://127.0.0.1:9090",
			allowInsecure: true,
			expected:      "ws://127.0.0.1:9090/connector/v1/control",
			expectError:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := wss.Config{
				BaseURL:       tc.input,
				AllowInsecure: tc.allowInsecure,
			}
			resolved, err := cfg.ResolveEndpoint()
			if tc.expectError {
				if err == nil {
					t.Fatalf("expected error for %s, but got resolved url: %s", tc.input, resolved)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error for %s: %v", tc.input, err)
				}
				if resolved != tc.expected {
					t.Errorf("ResolveEndpoint(%s) = %s; want %s", tc.input, resolved, tc.expected)
				}
			}
		})
	}
}

// TestClient_ConcurrentWrites 는 Heartbeat 및 ACK/RESULT 동시 발송 상황에서
// Gorilla WebSocket 의 concurrent write 충돌 없이 안전하게 직렬화되는지 검증하는 회귀 테스트입니다.
func TestClient_ConcurrentWrites(t *testing.T) {
	testToken := "test-concurrent-secret"
	mockSaaS := mock.NewMockSaaS(testToken)
	defer mockSaaS.Close()

	cfg := wss.Config{
		BaseURL:       mockSaaS.URL(),
		Credential:    testToken,
		AllowInsecure: true,
	}
	client := wss.NewClient(cfg)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Dial(ctx); err != nil {
		t.Fatalf("Dial failed: %v", err)
	}

	if _, err := client.SendHello(ctx); err != nil {
		t.Fatalf("SendHello failed: %v", err)
	}

	// 50개 고루틴에서 동시에 SendMessage 호출 (Heartbeat + ACK + RESULT 동시 전송 시뮬레이션)
	concurrency := 50
	errCh := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			msg := protocol.HeartbeatMessage{
				BaseEnvelope: protocol.BaseEnvelope{
					Type:      protocol.MessageTypeHeartbeat,
					MessageID: "msg-concurrent-" + string(rune('a'+idx%26)),
					SentAt:    time.Now().UTC(),
				},
				Payload: protocol.HeartbeatPayload{
					ObservedAt: time.Now().UTC(),
				},
			}
			errCh <- client.SendMessage(ctx, msg)
		}(i)
	}

	for i := 0; i < concurrency; i++ {
		if err := <-errCh; err != nil {
			t.Errorf("concurrent write failed at worker %d: %v", i, err)
		}
	}
}
