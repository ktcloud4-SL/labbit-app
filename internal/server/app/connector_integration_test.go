//go:build integration

package app

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
)

// 테스트용 Connector Credential이다. 실제 Secret이 아니다.
const e2eConnectorCredential = "test-e2e-connector-credential-unique-2f8c"

// seedConnector는 Organization, Connector, Credential을 저장하고 Connector ID를 반환한다.
// credential_hash는 production 경로와 같은 connector.CredentialDigest로 만든다.
func seedConnector(t *testing.T, dsn, credential string, connectorRevoked, credentialRevoked bool) uuid.UUID {
	t.Helper()
	conn := postgrestest.Connect(t, dsn)
	ctx := t.Context()

	now := time.Now()
	var connectorRevokedAt, credentialRevokedAt *time.Time
	if connectorRevoked {
		connectorRevokedAt = &now
	}
	if credentialRevoked {
		credentialRevokedAt = &now
	}

	organizationID, connectorID := uuid.New(), uuid.New()
	digest := connector.CredentialDigest(credential)
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO organizations (id, name) VALUES ($1, $2)`, []any{organizationID, "Connector E2E Org"}},
		{`INSERT INTO connectors (id, organization_id, name, revoked_at) VALUES ($1, $2, $3, $4)`,
			[]any{connectorID, organizationID, "E2E Connector", connectorRevokedAt}},
		{`INSERT INTO connector_credentials (id, connector_id, credential_hash, revoked_at) VALUES ($1, $2, $3, $4)`,
			[]any{uuid.New(), connectorID, digest[:], credentialRevokedAt}},
	} {
		if _, err := conn.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("seed %q error = %v", stmt.sql, err)
		}
	}
	return connectorID
}

// newConnectorClient는 repository의 실제 Connector WSS client다. loopback test 경계에서만 비보안 ws://를 허용한다.
func newConnectorClient(application, credential string) *wss.Client {
	return wss.NewClient(wss.Config{
		BaseURL:          application,
		Credential:       credential,
		ConnectorVersion: "test-1.0.0",
		AllowInsecure:    true,
	})
}

// 실제 Connector client가 실제 Backend application listener와 PostgreSQL을 상대로
// Bearer 인증 → subprotocol 협상 → HELLO → HELLO_ACK를 완료한다.
func TestConnectorClientCompletesHandshakeAgainstServer(t *testing.T) {
	dsn := postgrestest.NewDatabase(t)
	postgrestest.Migrate(t, dsn, loadEmbeddedMigrations(t))
	seedConnector(t, dsn, e2eConnectorCredential, false, false)

	admin, application := startServerWithApplication(t, "development", "api,realtime", dsn, "")
	waitForStatus(t, admin+"/readyz", http.StatusOK)

	client := newConnectorClient(application, e2eConnectorCredential)
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	if err := client.Dial(ctx); err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	if got := client.Conn().Subprotocol(); got != protocol.SubprotocolControl {
		t.Fatalf("negotiated subprotocol = %q, want %q", got, protocol.SubprotocolControl)
	}

	// SendHello는 HELLO_ACK의 type, replyToMessageId == HELLO.messageId, serverTime,
	// heartbeatIntervalSeconds, offlineTimeoutSeconds를 검증하고 하나라도 어긋나면 오류를 반환한다.
	ack, err := client.SendHello(ctx)
	if err != nil {
		t.Fatalf("SendHello() error = %v", err)
	}
	if ack.HeartbeatIntervalSeconds != 15 || ack.OfflineTimeoutSeconds != 45 {
		t.Fatalf("heartbeat/offline = %d/%d, want 15/45", ack.HeartbeatIntervalSeconds, ack.OfflineTimeoutSeconds)
	}
	if ack.ServerTime.IsZero() || time.Since(ack.ServerTime) > time.Minute || time.Until(ack.ServerTime) > time.Minute {
		t.Fatalf("serverTime = %v, want current server time", ack.ServerTime)
	}

	// handshake 이후에도 연결이 유지되고 Connector가 보낸 HEARTBEAT를 받아들인다.
	conn := client.Conn()
	pong := make(chan struct{}, 4)
	conn.SetPongHandler(func(string) error {
		pong <- struct{}{}
		return nil
	})
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	ping := func(label string) {
		t.Helper()
		if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)); err != nil {
			t.Fatalf("%s ping error = %v", label, err)
		}
		select {
		case <-pong:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: pong 없음, HELLO_ACK 이후 연결이 닫힘", label)
		}
	}
	ping("HELLO_ACK 직후")
	heartbeat := protocol.HeartbeatMessage{
		BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeHeartbeat, MessageID: uuid.NewString(), SentAt: time.Now().UTC()},
		Payload:      protocol.HeartbeatPayload{ObservedAt: time.Now().UTC()},
	}
	if err := client.SendMessage(ctx, heartbeat); err != nil {
		t.Fatalf("SendMessage(HEARTBEAT) error = %v", err)
	}
	ping("HEARTBEAT 이후")
}

// 인증할 수 없는 Credential은 실제 client의 Dial 단계에서 401로 거절되며 HELLO 단계까지 가지 못한다.
func TestConnectorClientIsRejectedWithUnusableCredential(t *testing.T) {
	dsn := postgrestest.NewDatabase(t)
	postgrestest.Migrate(t, dsn, loadEmbeddedMigrations(t))
	seedConnector(t, dsn, "test-e2e-revoked-credential-unique", false, true)
	seedConnector(t, dsn, "test-e2e-credential-of-revoked-connector-unique", true, false)

	admin, application := startServerWithApplication(t, "development", "api,realtime", dsn, "")
	waitForStatus(t, admin+"/readyz", http.StatusOK)

	for name, credential := range map[string]string{
		"revoke된 Credential": "test-e2e-revoked-credential-unique",
		"revoke된 Connector":  "test-e2e-credential-of-revoked-connector-unique",
		"존재하지 않는 Credential": "test-e2e-unknown-credential-unique",
	} {
		t.Run(name, func(t *testing.T) {
			client := newConnectorClient(application, credential)
			t.Cleanup(func() { _ = client.Close() })
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			err := client.Dial(ctx)
			if err == nil {
				t.Fatal("Dial() error = nil, want 401 rejection")
			}
			if !strings.Contains(err.Error(), "status 401") {
				t.Fatalf("Dial() error = %v, want status 401", err)
			}
			if strings.Contains(err.Error(), credential) {
				t.Fatalf("Dial() error leaks credential: %v", err)
			}
			if client.Conn() != nil {
				t.Fatal("인증 실패 후에도 client connection이 성립함")
			}
		})
	}
}

// 1 MiB를 넘는 JSON Text message는 실제 client 연결에서도 1009로 종료된다.
func TestConnectorClientOversizedMessageIsClosedWith1009(t *testing.T) {
	dsn := postgrestest.NewDatabase(t)
	postgrestest.Migrate(t, dsn, loadEmbeddedMigrations(t))
	seedConnector(t, dsn, e2eConnectorCredential, false, false)

	admin, application := startServerWithApplication(t, "development", "api,realtime", dsn, "")
	waitForStatus(t, admin+"/readyz", http.StatusOK)

	client := newConnectorClient(application, e2eConnectorCredential)
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := client.Dial(ctx); err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	if _, err := client.SendHello(ctx); err != nil {
		t.Fatalf("SendHello() error = %v", err)
	}

	oversized := map[string]any{
		"type":      protocol.MessageTypeHeartbeat,
		"messageId": uuid.NewString(),
		"sentAt":    time.Now().UTC(),
		"payload":   map[string]any{"padding": string(bytes.Repeat([]byte("x"), int(protocol.MaxJSONMessageSize)))},
	}
	if err := client.SendMessage(ctx, oversized); err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}

	conn := client.Conn()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err := conn.ReadMessage()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != protocol.CloseMessageTooBig {
		t.Fatalf("ReadMessage() error = %v, want close code %d", err, protocol.CloseMessageTooBig)
	}
}

// Connector Control endpoint는 realtime role이 활성화되고 Credential 조회용 PostgreSQL이 있을 때만 제공한다.
func TestConnectorControlEndpointRequiresRealtimeRoleAndDatabase(t *testing.T) {
	dsn := postgrestest.NewDatabase(t)
	postgrestest.Migrate(t, dsn, loadEmbeddedMigrations(t))
	seedConnector(t, dsn, e2eConnectorCredential, false, false)

	tests := map[string]string{
		"realtime role 없음":       "api",
		"PostgreSQL 없는 realtime": "realtime",
	}
	for name, roles := range tests {
		t.Run(name, func(t *testing.T) {
			_, application := startServerWithApplication(t, "development", roles, dsn, "")

			client := newConnectorClient(application, e2eConnectorCredential)
			t.Cleanup(func() { _ = client.Close() })
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			// 서버 listener가 뜰 때까지 기다린다.
			deadline := time.Now().Add(15 * time.Second)
			var err error
			for time.Now().Before(deadline) {
				if err = client.Dial(ctx); err != nil && strings.Contains(err.Error(), "status") {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if err == nil || !strings.Contains(err.Error(), "status 404") {
				t.Fatalf("Dial() error = %v, want status 404 (endpoint 미제공)", err)
			}
		})
	}
}
