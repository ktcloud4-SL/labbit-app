//go:build integration

package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connectorwss"
)

// HELLO_ACK는 heartbeat 주기와 offline timeout을 초 단위 정수(최소 1)로 전달하고 실제 Connector Supervisor는 그 값을 그대로 쓴다.
// 그래서 실제 Supervisor를 쓰는 test는 초 단위 값을 쓴다.
const (
	lifecycleHeartbeat = time.Second
	lifecycleOffline   = 3 * time.Second
)

// lifecycleEnv는 실제 connectorwss.Handler, connector.Service, PostgreSQL Store로 구성한 Control endpoint다.
// app.Run은 계약 기본값(15초/45초)만 쓰므로 짧은 주기는 Handler Options로 주입한다.
type lifecycleEnv struct {
	t           *testing.T
	dsn         string
	url         string
	registry    *connector.Registry
	handler     *connectorwss.Handler
	db          *pgx.Conn
	connectorID uuid.UUID
}

func startLifecycleEnv(t *testing.T) *lifecycleEnv {
	t.Helper()
	dsn := postgrestest.NewDatabase(t)
	postgrestest.Migrate(t, dsn, loadEmbeddedMigrations(t))
	connectorID := seedConnector(t, dsn, e2eConnectorCredential, false, false)

	pool, err := postgres.OpenPool(t.Context(), dsn)
	if err != nil {
		t.Fatalf("OpenPool() error = %v", err)
	}
	t.Cleanup(pool.Close)

	service := connector.NewService(postgres.NewStore(pool))
	registry := connector.NewRegistry()
	handler, err := connectorwss.New(connectorwss.Options{
		Auth:              service,
		Heartbeats:        service,
		Registry:          registry,
		HeartbeatInterval: lifecycleHeartbeat,
		OfflineTimeout:    lifecycleOffline,
	})
	if err != nil {
		t.Fatalf("connectorwss.New() error = %v", err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET "+connectorwss.Path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := handler.Shutdown(ctx); err != nil {
			t.Errorf("handler.Shutdown() error = %v", err)
		}
	})

	return &lifecycleEnv{
		t: t, dsn: dsn, url: server.URL, registry: registry, handler: handler,
		db: postgrestest.Connect(t, dsn), connectorID: connectorID,
	}
}

// lastSeenAt은 connectors.last_seen_at이다. 기록된 적이 없으면 nil이다.
func (e *lifecycleEnv) lastSeenAt() *time.Time {
	e.t.Helper()
	var seen *time.Time
	if err := e.db.QueryRow(e.t.Context(), `SELECT last_seen_at FROM connectors WHERE id = $1`, e.connectorID).Scan(&seen); err != nil {
		e.t.Fatalf("last_seen_at 조회: %v", err)
	}
	return seen
}

func (e *lifecycleEnv) rows(table string) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(e.t.Context(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		e.t.Fatalf("%s row 수 조회: %v", table, err)
	}
	return n
}

// requireNoOperationActivity는 Operation/Provider 관련 row가 하나도 없음을 확인한다. reconnect는 Operation retry가 아니다.
func (e *lifecycleEnv) requireNoOperationActivity() {
	e.t.Helper()
	for _, table := range []string{"operations", "operation_items", "provider_resources"} {
		if n := e.rows(table); n != 0 {
			e.t.Fatalf("%s row = %d, want 0: 연결 수명 이벤트가 Operation 상태를 만들면 안 됨", table, n)
		}
	}
}

// current는 registry의 current Session이 생길 때까지 기다린다.
func (e *lifecycleEnv) current() connector.Session {
	e.t.Helper()
	var session connector.Session
	eventually(e.t, "current Session", 10*time.Second, func() bool {
		var ok bool
		session, ok = e.registry.Current(e.connectorID)
		return ok
	})
	return session
}

func eventually(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%v 안에 %s 조건이 충족되지 않음", timeout, what)
}

// supervisorRun은 실제 Connector wss.Supervisor 하나다. Provider 호출은 모두 세어 실패시킨다(연결 수명이 Operation을 실행하면 안 된다).
type supervisorRun struct {
	connected    chan *protocol.HelloAckPayload
	disconnected chan error
	providerCall atomic.Int64
	clientErrors atomic.Int64
}

func startSupervisor(t *testing.T, env *lifecycleEnv, initialBackoff time.Duration) *supervisorRun {
	t.Helper()
	run := &supervisorRun{connected: make(chan *protocol.HelloAckPayload, 32), disconnected: make(chan error, 32)}

	unexpected := func() (provider.OperationResult, error) {
		run.providerCall.Add(1)
		return provider.OperationResult{}, errors.New("연결 수명 test에서 Provider가 호출됨")
	}
	mock := &provider.MockProvider{
		ProvisionFunc: func(context.Context, provider.ProvisionRequest) (provider.OperationResult, error) {
			return unexpected()
		},
		ResetFunc:   func(context.Context, provider.ResetRequest) (provider.OperationResult, error) { return unexpected() },
		CleanupFunc: func(context.Context, provider.CleanupRequest) (provider.OperationResult, error) { return unexpected() },
		ReconcileFunc: func(context.Context, provider.ReconcileRequest) (provider.ReconcileResult, error) {
			run.providerCall.Add(1)
			return provider.ReconcileResult{}, errors.New("연결 수명 test에서 Provider가 호출됨")
		},
	}
	handler := wss.NewHandler(mock, nil)
	handler.SetOnError(func(error) { run.clientErrors.Add(1) })

	supervisor := wss.NewSupervisor(wss.Config{
		BaseURL:          env.url,
		Credential:       e2eConnectorCredential,
		ConnectorVersion: "test-1.0.0",
		AllowInsecure:    true,
	}, handler, wss.BackoffPolicy{InitialInterval: initialBackoff, MaxInterval: initialBackoff * 4, Multiplier: 2, RandomizationFactor: 0.1})
	supervisor.SetOnConnected(func(ack *protocol.HelloAckPayload) {
		select {
		case run.connected <- ack:
		default:
		}
	})
	supervisor.SetOnDisconnected(func(err error) {
		select {
		case run.disconnected <- err:
		default:
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("Supervisor가 종료되지 않음")
		}
	})
	return run
}

func (r *supervisorRun) waitConnected(t *testing.T) *protocol.HelloAckPayload {
	t.Helper()
	select {
	case ack := <-r.connected:
		return ack
	case <-time.After(15 * time.Second):
		t.Fatal("Supervisor가 연결되지 않음")
		return nil
	}
}

func (r *supervisorRun) waitDisconnected(t *testing.T) error {
	t.Helper()
	select {
	case err := <-r.disconnected:
		return err
	case <-time.After(15 * time.Second):
		t.Fatal("Supervisor 연결이 끊기지 않음")
		return nil
	}
}

// requireCloseCode는 err가 code의 WebSocket close를 포함하는지 확인한다.
func requireCloseCode(t *testing.T, err error, code int) {
	t.Helper()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != code {
		t.Fatalf("error = %v, want WebSocket close %d", err, code)
	}
}

// 실제 Supervisor가 HELLO_ACK로 협상한 주기로 HEARTBEAT를 보내면 Backend가 유효한 HEARTBEAT를 받아 PostgreSQL의
// last_seen_at을 계속 갱신하고, offline timeout보다 오래 같은 Session이 유지된다.
func TestSupervisorHeartbeatsKeepSessionAndAdvanceLastSeen(t *testing.T) {
	env := startLifecycleEnv(t)
	started := time.Now()
	run := startSupervisor(t, env, 50*time.Millisecond)

	ack := run.waitConnected(t)
	connectedAt := time.Now()
	if ack.HeartbeatIntervalSeconds != 1 || ack.OfflineTimeoutSeconds != 3 {
		t.Fatalf("협상된 heartbeat/offline = %d/%d, want 1/3", ack.HeartbeatIntervalSeconds, ack.OfflineTimeoutSeconds)
	}
	session := env.current()
	if env.lastSeenAt() != nil {
		t.Fatal("HEARTBEAT 전에 last_seen_at이 기록됨(HELLO는 heartbeat가 아님)")
	}

	// 실제 heartbeat.Runner의 HEARTBEAT가 서로 다른 서버 수신 시각으로 세 번 이상 기록된다.
	var seen []time.Time
	eventually(t, "last_seen_at 3회 갱신", 15*time.Second, func() bool {
		if got := env.lastSeenAt(); got != nil && (len(seen) == 0 || got.After(seen[len(seen)-1])) {
			seen = append(seen, *got)
		}
		return len(seen) >= 3
	})
	for _, at := range seen {
		if at.Before(started.Add(-time.Second)) || at.After(time.Now().Add(time.Second)) {
			t.Fatalf("last_seen_at = %v, want 이 test 동안의 서버 시각", at)
		}
	}

	// offline timeout보다 오래 지나도 같은 Session이 유지되고 연결이 끊기지 않았다.
	eventually(t, "offline timeout 경과", 15*time.Second, func() bool { return time.Since(connectedAt) > lifecycleOffline+time.Second })
	if got, ok := env.registry.Current(env.connectorID); !ok || got != session {
		t.Fatalf("registry = %+v, %v, want 같은 Session %+v", got, ok, session)
	}
	select {
	case err := <-run.disconnected:
		t.Fatalf("heartbeat를 보내는 Supervisor 연결이 끊김: %v", err)
	default:
	}

	if run.providerCall.Load() != 0 || run.clientErrors.Load() != 0 {
		t.Fatalf("Provider 호출 = %d, client 오류 = %d, want 0/0", run.providerCall.Load(), run.clientErrors.Load())
	}
	env.requireNoOperationActivity()
}

// 같은 Connector Credential의 두 번째 실제 client가 HELLO에 성공하면 Supervisor의 연결은 4002로 끝나고,
// Supervisor가 재접속하면 다시 current가 되어 두 번째 client를 4002로 종료한다. 재접속 뒤에도 heartbeat가 계속 기록된다.
// 이 과정에서 Provider 호출이나 Operation 상태 변화는 없다(reconnect는 Operation retry가 아니다).
func TestSupervisorReconnectsAfterDuplicateReplacement(t *testing.T) {
	env := startLifecycleEnv(t)
	// 중간 Session(B)이 current인 구간을 안정적으로 관찰할 수 있게 재접속 대기를 넉넉히 둔다.
	run := startSupervisor(t, env, 400*time.Millisecond)
	run.waitConnected(t)
	first := env.current()

	// 두 번째 실제 client B가 같은 Credential로 연결한다. B가 current가 되고 Supervisor 연결은 4002로 끝난다.
	other := newConnectorClient(env.url, e2eConnectorCredential)
	t.Cleanup(func() { _ = other.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := other.Dial(ctx); err != nil {
		t.Fatalf("B Dial() error = %v", err)
	}
	if _, err := other.SendHello(ctx); err != nil {
		t.Fatalf("B SendHello() error = %v", err)
	}
	var second connector.Session
	eventually(t, "B가 current", 10*time.Second, func() bool {
		session, ok := env.registry.Current(env.connectorID)
		second = session
		return ok && session != first
	})
	requireCloseCode(t, run.waitDisconnected(t), 4002)

	// Supervisor가 재접속해 다시 current가 되고, 그 HELLO가 B를 교체한다. Session은 세 번 모두 다르다.
	run.waitConnected(t)
	var third connector.Session
	eventually(t, "재접속한 Supervisor가 current", 10*time.Second, func() bool {
		session, ok := env.registry.Current(env.connectorID)
		third = session
		return ok && session != second
	})
	if third == first || third == second || first == second {
		t.Fatalf("Session이 재사용됨: %+v %+v %+v", first, second, third)
	}
	conn := other.Conn()
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, _, err := conn.ReadMessage()
	requireCloseCode(t, err, 4002)

	// 재접속한 Supervisor의 heartbeat가 계속 기록된다.
	before := env.lastSeenAt()
	eventually(t, "재접속 뒤 heartbeat 기록", 10*time.Second, func() bool {
		got := env.lastSeenAt()
		return got != nil && (before == nil || got.After(*before))
	})
	if got, ok := env.registry.Current(env.connectorID); !ok || got != third {
		t.Fatalf("registry = %+v, %v, want 재접속한 Session %+v", got, ok, third)
	}
	select {
	case err := <-run.disconnected:
		t.Fatalf("재접속한 Supervisor 연결이 다시 끊김: %v", err)
	default:
	}

	if run.providerCall.Load() != 0 || run.clientErrors.Load() != 0 {
		t.Fatalf("Provider 호출 = %d, client 오류 = %d, want 0/0", run.providerCall.Load(), run.clientErrors.Load())
	}
	env.requireNoOperationActivity()
}

// PostgreSQL에서 Credential이 revoke되면 다음 유효한 HEARTBEAT에서 4001로 끝나고, 그 HEARTBEAT는 last_seen_at을 갱신하지 않으며
// 이후 재접속은 401로 거절된다.
func TestSupervisorIsClosedWith4001AfterDatabaseRevoke(t *testing.T) {
	env := startLifecycleEnv(t)
	run := startSupervisor(t, env, 50*time.Millisecond)
	run.waitConnected(t)
	env.current()

	// 방금 heartbeat가 기록된 직후에 revoke한다. 다음 heartbeat까지 약 1초가 남아 있어 last_seen_at 비교가 결정적이다.
	var beforeRevoke time.Time
	var previous *time.Time
	eventually(t, "heartbeat 기록", 10*time.Second, func() bool {
		got := env.lastSeenAt()
		if got == nil || (previous != nil && got.Equal(*previous)) {
			previous = got
			return false
		}
		if previous == nil {
			previous = got // 첫 값은 기준으로만 쓰고 그 다음 갱신을 기다린다.
			return false
		}
		beforeRevoke = *got
		return true
	})
	if _, err := env.db.Exec(t.Context(), `UPDATE connector_credentials SET revoked_at = now() WHERE connector_id = $1`, env.connectorID); err != nil {
		t.Fatalf("Credential revoke: %v", err)
	}

	requireCloseCode(t, run.waitDisconnected(t), 4001)
	eventually(t, "registry release", 5*time.Second, func() bool {
		_, ok := env.registry.Current(env.connectorID)
		return !ok
	})
	if got := env.lastSeenAt(); got == nil || !got.Equal(beforeRevoke) {
		t.Fatalf("revoke를 감지한 HEARTBEAT가 last_seen_at을 갱신함: %v, want %v", got, beforeRevoke)
	}

	// 같은 Credential의 재접속은 인증 단계에서 거절된다.
	if err := run.waitDisconnected(t); err == nil || !strings.Contains(err.Error(), "status 401") {
		t.Fatalf("revoke 뒤 재접속 error = %v, want status 401", err)
	}
	if _, ok := env.registry.Current(env.connectorID); ok {
		t.Fatal("revoke된 Credential이 다시 registry에 등록됨")
	}
	if got := env.lastSeenAt(); got == nil || !got.Equal(beforeRevoke) {
		t.Fatalf("revoke 뒤 last_seen_at이 갱신됨: %v, want %v", got, beforeRevoke)
	}
	env.requireNoOperationActivity()
}

// 실제 client가 HELLO만 하고 HEARTBEAT를 보내지 않으면 협상된 offline timeout 뒤에 OFFLINE으로 표준 1001 종료되고
// last_seen_at은 기록되지 않는다.
func TestRealClientWithoutHeartbeatGoesOfflineAgainstPostgreSQL(t *testing.T) {
	env := startLifecycleEnv(t)
	client := newConnectorClient(env.url, e2eConnectorCredential)
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := client.Dial(ctx); err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	ack, err := client.SendHello(ctx)
	if err != nil {
		t.Fatalf("SendHello() error = %v", err)
	}
	if ack.OfflineTimeoutSeconds != 3 {
		t.Fatalf("offlineTimeoutSeconds = %d, want 3", ack.OfflineTimeoutSeconds)
	}
	env.current()

	started := time.Now()
	conn := client.Conn()
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	_, _, err = conn.ReadMessage()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != websocket.CloseGoingAway || closeErr.Text != "heartbeat timeout" {
		t.Fatalf("종료 = %v, want close %d heartbeat timeout", err, websocket.CloseGoingAway)
	}
	if elapsed := time.Since(started); elapsed < lifecycleOffline/2 {
		t.Fatalf("HELLO %v 만에 OFFLINE: 협상된 %v보다 훨씬 일찍 끊김", elapsed, lifecycleOffline)
	}
	eventually(t, "registry release", 5*time.Second, func() bool {
		_, ok := env.registry.Current(env.connectorID)
		return !ok
	})
	if got := env.lastSeenAt(); got != nil {
		t.Fatalf("HEARTBEAT 없이 last_seen_at이 기록됨: %v", *got)
	}
	env.requireNoOperationActivity()
}

// Handler shutdown은 실제 Supervisor 연결을 1001로 정리하고 registry에 아무것도 남기지 않으며, 이후 재접속은 503으로 거절한다.
func TestShutdownDrainsSupervisorConnectionAgainstPostgreSQL(t *testing.T) {
	env := startLifecycleEnv(t)
	run := startSupervisor(t, env, 50*time.Millisecond)
	run.waitConnected(t)
	env.current()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := env.handler.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	requireCloseCode(t, run.waitDisconnected(t), websocket.CloseGoingAway)
	if _, ok := env.registry.Current(env.connectorID); ok {
		t.Fatal("Shutdown 뒤에도 registry에 Session이 남음")
	}
	if err := run.waitDisconnected(t); err == nil || !strings.Contains(err.Error(), "status 503") {
		t.Fatalf("Shutdown 뒤 재접속 error = %v, want status 503", err)
	}
	if run.providerCall.Load() != 0 {
		t.Fatalf("Provider 호출 = %d, want 0", run.providerCall.Load())
	}
	env.requireNoOperationActivity()
}
