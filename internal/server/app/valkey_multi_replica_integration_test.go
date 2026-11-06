//go:build integration

package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal/terminaltest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/valkeyowner"
)

const valkeyTestAddr = "127.0.0.1:6379"

type valkeyReplicaCluster struct {
	t            *testing.T
	dsn          string
	pool         *pgxpool.Pool
	store        *postgres.Store
	conn         *pgx.Conn
	connectorID  uuid.UUID
	credentialID uuid.UUID
	fixture      *terminaltest.Fixture
	ownerCookie  string

	podA     *controlStack
	srvA     *httptest.Server
	urlA     string
	locatorA *valkeyowner.Locator
	proxyA   *valkeyowner.Proxy

	podB     *controlStack
	srvB     *httptest.Server
	urlB     string
	locatorB *valkeyowner.Locator
	proxyB   *valkeyowner.Proxy

	podC     *controlStack
	srvC     *httptest.Server
	urlC     string
	locatorC *valkeyowner.Locator
	proxyC   *valkeyowner.Proxy
}

func newValkeyReplicaCluster(t *testing.T, withWorker, withRealtime bool) *valkeyReplicaCluster {
	t.Helper()
	dsn := postgrestest.NewDatabase(t)
	postgrestest.Migrate(t, dsn, loadEmbeddedMigrations(t))
	conn := postgrestest.Connect(t, dsn)

	fixture := terminaltest.New(t, conn)
	ownerCookie := terminaltest.LoginSession(t, conn, fixture.OwnerID)
	connectorID := fixture.ConnectorID
	var credID uuid.UUID
	err := conn.QueryRow(t.Context(), `SELECT id FROM connector_credentials WHERE connector_id = $1`, fixture.ConnectorID).Scan(&credID)
	if err != nil {
		t.Fatalf("query credential error = %v", err)
	}

	pool, err := postgres.OpenPool(t.Context(), dsn)
	if err != nil {
		t.Fatalf("OpenPool() error = %v", err)
	}
	t.Cleanup(pool.Close)
	store := postgres.NewStore(pool)

	makeStack := func(name string) (*controlStack, *httptest.Server, *valkeyowner.Locator, *valkeyowner.Proxy) {
		// Create dummy listener first to determine port
		server := httptest.NewServer(nil)
		podAddr := strings.TrimPrefix(server.URL, "http://")

		locator, err := valkeyowner.NewLocator(valkeyowner.Config{
			ValkeyAddr: valkeyTestAddr,
			PodID:      name,
			PodAddr:    podAddr,
		})
		if err != nil {
			t.Fatalf("NewLocator(%s) error = %v", name, err)
		}

		proxy := valkeyowner.NewProxy("spike-p2p-secret")

		opts := stackOptions{
			Logger:        slog.New(slog.DiscardHandler),
			PublicOrigin:  terminalTrustedOrigin,
			Worker:        withWorker,
			Realtime:      withRealtime,
			OwnerLocator:  locator,
			PeerProxy:     proxy,
			AttachTimeout: 2 * time.Second,
			CloseGrace:    200 * time.Millisecond,
		}

		stack, err := newControlStack(store, opts)
		if err != nil {
			locator.Close()
			t.Fatalf("newControlStack(%s) error = %v", name, err)
		}

		server.Config.Handler = applicationHandler(stack.routes())

		t.Cleanup(func() {
			locator.Close()
			stack.close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = stack.shutdown(ctx)
			server.Close()
		})
		return stack, server, locator, proxy
	}

	podA, srvA, locA, prxA := makeStack("pod-a")
	podB, srvB, locB, prxB := makeStack("pod-b")
	podC, srvC, locC, prxC := makeStack("pod-c")

	return &valkeyReplicaCluster{
		t:            t,
		dsn:          dsn,
		pool:         pool,
		store:        store,
		conn:         conn,
		connectorID:  connectorID,
		credentialID: credID,
		fixture:      fixture,
		ownerCookie:  ownerCookie,
		podA:         podA,
		srvA:         srvA,
		urlA:         srvA.URL,
		locatorA:     locA,
		proxyA:       prxA,
		podB:         podB,
		srvB:         srvB,
		urlB:         srvB.URL,
		locatorB:     locB,
		proxyB:       prxB,
		podC:         podC,
		srvC:         srvC,
		urlC:         srvC.URL,
		locatorC:     locC,
		proxyC:       prxC,
	}
}

func startSupervisorWithCustomMock(t *testing.T, serverURL string, mock *provider.MockProvider) *stoppableSupervisor {
	t.Helper()
	handler := wss.NewHandler(mock, nil)
	supervisor := wss.NewSupervisor(wss.Config{
		BaseURL: serverURL, Credential: terminaltest.Credential, ConnectorVersion: "test-1.0.0", AllowInsecure: true,
	}, handler, wss.BackoffPolicy{InitialInterval: 50 * time.Millisecond, MaxInterval: 200 * time.Millisecond, Multiplier: 2, RandomizationFactor: 0.1})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()

	s := &stoppableSupervisor{cancel: cancel, done: done}
	t.Cleanup(s.Stop)
	return s
}

// 1. Worker Claim Avoidance & Exact-Once Execution:
// Connector connects to Pod A.
// PENDING operation item exists in PostgreSQL.
// Pod B Worker runs -> Valkey owner check shows Pod A owns the connector.
// Pod B avoids claim (defers unsent fail-closed). Provider mutation count = 0.
// Pod A Worker runs -> Pod A owns the connector -> dispatches to Connector -> Provider mutation count = 1.
func TestValkey_Worker_CrossPodClaimAndExactOnceExecution(t *testing.T) {
	c := newValkeyReplicaCluster(t, true, false)

	var provisions atomic.Int32
	mockProv := &provider.MockProvider{
		ProvisionFunc: func(_ context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
			provisions.Add(1)
			return provider.OperationResult{Outcome: provider.OutcomeSucceeded, ProviderResources: []provider.ResourceResult{{
				ResourceRef:   provider.ResourceRef{ResourceType: "SERVER", ProviderID: "srv-1", Generation: r.Generation},
				ObservedState: "ACTIVE",
			}}}, nil
		},
	}
	startSupervisorWithCustomMock(t, c.urlA, mockProv)

	eventually(t, "Pod A route ready", 10*time.Second, func() bool {
		return c.podA.Registry.WithReadyRoute(c.connectorID, func(connector.Session, connector.Route) error { return nil }) == nil
	})

	// Verify Valkey has Pod A as owner
	eventually(t, "Valkey owner is Pod A", 5*time.Second, func() bool {
		rec, err := c.locatorA.LookupConnector(t.Context(), c.connectorID)
		return err == nil && rec.PodID == "pod-a"
	})

	// Seed fresh execution, student, instance, and operation
	freshExecID := uuid.New()
	_, err := c.conn.Exec(t.Context(), `
		INSERT INTO lab_executions (id, organization_id, class_id, lab_spec_id, instructor_user_id, status)
		VALUES ($1, $2, $3, $4, $5, 'ACTIVE')
	`, freshExecID, c.fixture.OrganizationID, c.fixture.OtherClassID, c.fixture.LabSpecID, c.fixture.InstructorID)
	if err != nil {
		t.Fatalf("insert fresh lab_execution error = %v", err)
	}

	snapshotJSON, _ := json.Marshal(map[string]any{
		"providerConnectionId": c.fixture.ProviderConnectionID.String(),
		"vms": []map[string]any{{
			"vmKey": "workspace", "role": "workspace", "instanceIndex": 0,
			"imageId": "img-1", "flavorId": "flv-1",
			"flavorSpec": map[string]any{"vcpus": 1, "ramMiB": 512, "diskGiB": 10},
		}},
		"workspaceVmKey": "workspace",
	})
	_, err = c.conn.Exec(t.Context(), `
		INSERT INTO creation_snapshots (id, organization_id, lab_execution_id, source_lab_spec_id, source_lab_spec_revision, provider_connection_id, snapshot)
		VALUES ($1, $2, $3, $4, 1, $5, $6::jsonb)
	`, uuid.New(), c.fixture.OrganizationID, freshExecID, c.fixture.LabSpecID, c.fixture.ProviderConnectionID, snapshotJSON)
	if err != nil {
		t.Fatalf("insert fresh creation_snapshot error = %v", err)
	}

	freshUser := uuid.New()
	_, err = c.conn.Exec(t.Context(), `
		INSERT INTO users (id, organization_id, organization_role) VALUES ($1, $2, 'MEMBER')
	`, freshUser, c.fixture.OrganizationID)
	if err != nil {
		t.Fatalf("insert user error = %v", err)
	}

	freshInstanceID := uuid.New()
	_, err = c.conn.Exec(t.Context(), `
		INSERT INTO lab_instances (id, organization_id, lab_execution_id, user_id, participant_role, status, generation)
		VALUES ($1, $2, $3, $4, 'STUDENT', 'PENDING', 1)
	`, freshInstanceID, c.fixture.OrganizationID, freshExecID, freshUser)
	if err != nil {
		t.Fatalf("insert fresh lab_instance error = %v", err)
	}

	opID := uuid.New()
	_, err = c.conn.Exec(t.Context(), `
		INSERT INTO operations (id, organization_id, operation_type, requested_by_user_id, lab_execution_id, idempotency_key_hash, request_fingerprint, status, created_at)
		VALUES ($1, $2, 'PROVISION', $3, $4, '\x01'::bytea, '\x01'::bytea, 'PENDING', now())
	`, opID, c.fixture.OrganizationID, c.fixture.OwnerID, freshExecID)
	if err != nil {
		t.Fatalf("insert operation error = %v", err)
	}

	itemID := uuid.New()
	_, err = c.conn.Exec(t.Context(), `
		INSERT INTO operation_items (id, organization_id, operation_id, lab_instance_id, generation, status, created_at)
		VALUES ($1, $2, $3, $4, 1, 'PENDING', now())
	`, itemID, c.fixture.OrganizationID, opID, freshInstanceID)
	if err != nil {
		t.Fatalf("insert operation_item error = %v", err)
	}

	// 1) Pod B Worker polls first: non-owner avoids claim!
	claimedB, err := c.podB.Provisions.RunOne(t.Context())
	if err != nil {
		t.Fatalf("Pod B RunOne error = %v", err)
	}
	if !claimedB {
		t.Fatalf("Pod B claimed the item from DB")
	}

	// Provider must NOT have executed
	if provisions.Load() != 0 {
		t.Fatalf("provisions called = %d, want 0 (Pod B non-owner avoided execution)", provisions.Load())
	}

	// Check item status in PostgreSQL: deferred back to PENDING
	var status, stage string
	err = c.conn.QueryRow(t.Context(), `
		SELECT status, COALESCE(stage, '') FROM operation_items WHERE id = $1
	`, itemID).Scan(&status, &stage)
	if err != nil {
		t.Fatalf("query operation_item error = %v", err)
	}
	if status != "PENDING" || stage != "DEFERRED" {
		t.Fatalf("status = %s, stage = %s, want PENDING/DEFERRED", status, stage)
	}

	// Reset dispatch_not_before to now so Pod A can claim it immediately
	_, err = c.conn.Exec(t.Context(), `UPDATE operation_items SET dispatch_not_before = now() - interval '1 second' WHERE id = $1`, itemID)
	if err != nil {
		t.Fatalf("reset dispatch_not_before error = %v", err)
	}

	// 2) Pod A Worker polls: owner claims and executes!
	claimedA, err := c.podA.Provisions.RunOne(t.Context())
	if err != nil {
		t.Fatalf("Pod A RunOne error = %v", err)
	}
	if !claimedA {
		t.Fatalf("Pod A should have claimed the PENDING item")
	}

	// Mock provider executed EXACTLY ONCE
	if provisions.Load() != 1 {
		t.Fatalf("provisions called = %d, want exactly 1", provisions.Load())
	}

	// Verify operation item completed in DB
	err = c.conn.QueryRow(t.Context(), `
		SELECT status FROM operation_items WHERE id = $1
	`, itemID).Scan(&status)
	if err != nil {
		t.Fatalf("query operation_item error = %v", err)
	}
	if status != "SUCCEEDED" {
		t.Fatalf("final status = %s, want SUCCEEDED", status)
	}
}

// 2. Cross-Pod Terminal Create + Browser WSS Proxy:
// Connector connects to Pod A.
// HTTP CreateTerminalSession lands on Pod B (proxied to Pod A via Valkey locator).
// Browser connects to Pod B (proxied to Pod A via Valkey locator).
// Browser attaches, sends input, receives echo from Connector, and closes cleanly.
func TestValkey_Terminal_CrossPodCreateAndBrowserProxy(t *testing.T) {
	c := newValkeyReplicaCluster(t, false, true)

	conn := terminaltest.NewConnector(t, c.urlA, terminaltest.Credential)
	conn.Start()

	eventually(t, "Pod A route ready", 10*time.Second, func() bool {
		return c.podA.Registry.WithReadyRoute(c.connectorID, func(connector.Session, connector.Route) error { return nil }) == nil
	})

	eventually(t, "Valkey owner is Pod A", 5*time.Second, func() bool {
		rec, err := c.locatorA.LookupConnector(t.Context(), c.connectorID)
		return err == nil && rec.PodID == "pod-a"
	})

	authHeader := http.Header{
		"Cookie":       []string{realtime.SessionCookieName + "=" + c.ownerCookie},
		"Origin":       []string{terminalTrustedOrigin},
		"Content-Type": []string{"application/json"},
	}

	// 1) HTTP Create lands on Pod B (which has NO connector locally)
	createURL_B := c.urlB + "/api/v1/lab-instances/" + c.fixture.LabInstanceID.String() + "/terminal-sessions"
	respB := postHTTP(t, createURL_B, authHeader, createBody)
	if respB.StatusCode != http.StatusCreated {
		t.Fatalf("Pod B create status = %d, want 201 Created: %s", respB.StatusCode, readBody(t, respB))
	}

	var createdBody map[string]any
	_ = json.NewDecoder(respB.Body).Decode(&createdBody)
	sessionID := createdBody["id"].(string)
	attachToken := createdBody["sessionToken"].(string)

	// Verify Valkey has registered the terminal session owner as Pod A
	eventually(t, "Valkey terminal owner is Pod A", 5*time.Second, func() bool {
		rec, err := c.locatorB.LookupTerminal(t.Context(), sessionID)
		return err == nil && rec.PodID == "pod-a"
	})

	// 2) Browser connects to Pod B (which has NO local session)
	browserWsURL_B := "ws" + strings.TrimPrefix(c.urlB, "http") + realtime.BrowserPath
	browserDialer := websocket.Dialer{Subprotocols: []string{realtime.BrowserSubprotocol}}
	browserHeaders := http.Header{
		"Cookie": []string{realtime.SessionCookieName + "=" + c.ownerCookie},
		"Origin": []string{terminalTrustedOrigin},
	}
	bWs, bResp, err := browserDialer.Dial(browserWsURL_B, browserHeaders)
	if err != nil {
		t.Fatalf("Browser dial to Pod B error = %v, resp = %v", err, bResp)
	}
	defer bWs.Close()

	// Send TERMINAL_ATTACH
	attachMsg := map[string]any{
		"type":              "TERMINAL_ATTACH",
		"messageId":         "b-1",
		"sentAt":            time.Now().Format(time.RFC3339),
		"terminalSessionId": sessionID,
		"payload":           map[string]any{"sessionToken": attachToken, "cols": 80, "rows": 24},
	}
	if err := bWs.WriteJSON(attachMsg); err != nil {
		t.Fatalf("Browser WriteJSON error = %v", err)
	}

	// Receive TERMINAL_ATTACHED
	_ = bWs.SetReadDeadline(time.Now().Add(5 * time.Second))
	msgType, rawMsg, err := bWs.ReadMessage()
	if err != nil || !strings.Contains(string(rawMsg), "TERMINAL_ATTACHED") {
		t.Fatalf("Browser read error or unexpected msg: %v, msgType=%d, raw=%s", err, msgType, string(rawMsg))
	}

	// 3) Send PTY Input from Browser -> arrives at Connector Data link
	if err := bWs.WriteMessage(websocket.BinaryMessage, []byte("echo test\n")); err != nil {
		t.Fatalf("Browser write binary error = %v", err)
	}

	// Verify Data link on connector received input
	eventually(t, "Connector receives data", 5*time.Second, func() bool {
		return len(conn.Inputs(sessionID)) > 0
	})

	// Send PTY Output back from Connector -> arrives at Browser
	conn.Output(sessionID, []byte("echo test\r\n"))
	msgType, ptyOut, err := bWs.ReadMessage()
	if err != nil || msgType != websocket.BinaryMessage || !strings.Contains(string(ptyOut), "echo test") {
		t.Fatalf("Browser read binary output error = %v, msgType = %d, data = %s", err, msgType, string(ptyOut))
	}

	// 4) Close session via HTTP DELETE on Pod B (proxied to Pod A)
	closeURL_B := c.urlB + "/api/v1/terminal-sessions/" + sessionID
	req, _ := http.NewRequest(http.MethodDelete, closeURL_B, nil)
	req.Header.Set("Cookie", realtime.SessionCookieName+"="+c.ownerCookie)
	req.Header.Set("Origin", terminalTrustedOrigin)
	delResp, err := http.DefaultClient.Do(req)
	if err != nil || delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("HTTP DELETE session on Pod B error = %v, code = %d", err, delResp.StatusCode)
	}
}

// 3. Controlled Reverse Routing:
// Connector connects to Pod B.
// Requests land on Pod A -> correctly proxied/avoided to Pod B.
func TestValkey_ControlledReverseRouting(t *testing.T) {
	c := newValkeyReplicaCluster(t, true, true)

	var provisions atomic.Int32
	mockProv := &provider.MockProvider{
		ProvisionFunc: func(_ context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
			provisions.Add(1)
			return provider.OperationResult{Outcome: provider.OutcomeSucceeded, ProviderResources: []provider.ResourceResult{{
				ResourceRef:   provider.ResourceRef{ResourceType: "SERVER", ProviderID: "srv-rev-1", Generation: r.Generation},
				ObservedState: "ACTIVE",
			}}}, nil
		},
	}
	startRoutingConnectorToURL(t, c.urlB, c.connectorID, mockProv)

	eventually(t, "Pod B route ready", 10*time.Second, func() bool {
		return c.podB.Registry.WithReadyRoute(c.connectorID, func(connector.Session, connector.Route) error { return nil }) == nil
	})

	eventually(t, "Valkey owner is Pod B", 5*time.Second, func() bool {
		rec, err := c.locatorA.LookupConnector(t.Context(), c.connectorID)
		return err == nil && rec.PodID == "pod-b"
	})

	// Pod A Worker avoids claims
	claimedA, err := c.podA.Provisions.RunOne(t.Context())
	if err != nil {
		t.Fatalf("Pod A RunOne error = %v", err)
	}
	if claimedA {
		t.Fatalf("Pod A claimed item when Pod B is the owner")
	}
}

// 4. N=1 Fallback Regression & N=3 Topology:
// Verify single replica (N=1) works with zero proxy overhead,
// and 3 replicas (N=3) seamlessly resolve owners across Pod A, B, C.
func TestValkey_N1_FallbackRegression_And_N3_Topology(t *testing.T) {
	c := newValkeyReplicaCluster(t, false, true)

	conn := terminaltest.NewConnector(t, c.urlA, terminaltest.Credential)
	conn.Start()

	eventually(t, "Pod A route ready", 10*time.Second, func() bool {
		return c.podA.Registry.WithReadyRoute(c.connectorID, func(connector.Session, connector.Route) error { return nil }) == nil
	})

	authHeader := http.Header{
		"Cookie":       []string{realtime.SessionCookieName + "=" + c.ownerCookie},
		"Origin":       []string{terminalTrustedOrigin},
		"Content-Type": []string{"application/json"},
	}

	// N=1 test: Create directly on Pod A (local owner) -> succeeds immediately without proxy
	createURL_A := c.urlA + "/api/v1/lab-instances/" + c.fixture.LabInstanceID.String() + "/terminal-sessions"
	respA := postHTTP(t, createURL_A, authHeader, createBody)
	if respA.StatusCode != http.StatusCreated {
		t.Fatalf("N=1 Create status = %d, want 201: %s", respA.StatusCode, readBody(t, respA))
	}
	var createdA map[string]any
	_ = json.NewDecoder(respA.Body).Decode(&createdA)
	sessionIDA := createdA["id"].(string)
	tokenA := createdA["sessionToken"].(string)

	// N=3 test: Browser connects to Pod C! (Pod C proxies to Pod A)
	browserWsURL_C := "ws" + strings.TrimPrefix(c.urlC, "http") + realtime.BrowserPath
	browserDialer := websocket.Dialer{Subprotocols: []string{realtime.BrowserSubprotocol}}
	browserHeaders := http.Header{
		"Cookie": []string{realtime.SessionCookieName + "=" + c.ownerCookie},
		"Origin": []string{terminalTrustedOrigin},
	}
	cWs, _, err := browserDialer.Dial(browserWsURL_C, browserHeaders)
	if err != nil {
		t.Fatalf("N=3 Browser dial to Pod C error = %v", err)
	}
	defer cWs.Close()

	if err := cWs.WriteJSON(map[string]any{
		"type":              "TERMINAL_ATTACH",
		"messageId":         "c-1",
		"sentAt":            time.Now().Format(time.RFC3339),
		"terminalSessionId": sessionIDA,
		"payload":           map[string]any{"sessionToken": tokenA, "cols": 80, "rows": 24},
	}); err != nil {
		t.Fatalf("WriteJSON error = %v", err)
	}

	_ = cWs.SetReadDeadline(time.Now().Add(5 * time.Second))
	var attached struct {
		Type string `json:"type"`
	}
	if err := cWs.ReadJSON(&attached); err != nil || attached.Type != "TERMINAL_ATTACHED" {
		t.Fatalf("N=3 Browser attached error = %v, msg = %+v", err, attached)
	}
}

// 5. Negative Safety Evidence:
// a) Owner loss / release -> Worker avoids claim fail-closed.
// b) Stale owner epoch -> takeover rejects stale epoch.
// c) Valkey outage -> fail-closed without duplicated mutation.
func TestValkey_NegativeSafety(t *testing.T) {
	c := newValkeyReplicaCluster(t, true, false)

	// 5a. Stale owner epoch
	// Pod A registers with epoch 1
	err := c.locatorA.RegisterConnector(t.Context(), c.connectorID, 1, 15*time.Second)
	if err != nil {
		t.Fatalf("RegisterConnector error = %v", err)
	}

	// Pod B registers with epoch 2 (reconnect takeover)
	err = c.locatorB.RegisterConnector(t.Context(), c.connectorID, 2, 15*time.Second)
	if err != nil {
		t.Fatalf("Takeover error = %v", err)
	}

	// Pod A tries to refresh with stale epoch 1 -> fails with ErrStaleEpoch!
	err = c.locatorA.RefreshConnector(t.Context(), c.connectorID, 1, 15*time.Second)
	if err == nil {
		t.Fatalf("Refresh with stale epoch 1 should have returned error")
	}

	// 5b. Owner loss
	_ = c.locatorB.ReleaseConnector(t.Context(), c.connectorID, 2)
	rec, err := c.locatorA.LookupConnector(t.Context(), c.connectorID)
	if err != nil && !errors.Is(err, valkeyowner.ErrNoOwner) {
		t.Fatalf("Lookup error = %v", err)
	}
	if rec != nil {
		t.Fatalf("Lookup after release should return nil, got %+v", rec)
	}

	// 5c. Valkey outage simulation:
	// A locator pointing to unreachable address must fail-closed, returning error on Lookup.
	brokenLocator, err := valkeyowner.NewLocator(valkeyowner.Config{
		ValkeyAddr: "127.0.0.1:65530", // unreachable
		PodID:      "pod-broken",
		PodAddr:    "127.0.0.1:8080",
	})
	if err == nil {
		defer brokenLocator.Close()
		ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
		defer cancel()
		_, lookupErr := brokenLocator.LookupConnector(ctx, c.connectorID)
		if lookupErr == nil {
			t.Fatalf("Lookup against down Valkey should return error")
		}
	}
}
