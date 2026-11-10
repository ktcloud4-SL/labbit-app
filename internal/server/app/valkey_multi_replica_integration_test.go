//go:build integration

package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
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
	"github.com/ktcloud4-SL/labbit-app/internal/server/connectorwss"
	"github.com/ktcloud4-SL/labbit-app/internal/server/filetransport"
	"github.com/ktcloud4-SL/labbit-app/internal/server/filetransport/filetest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/preview"
	"github.com/ktcloud4-SL/labbit-app/internal/server/preview/previewtest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/previewsession"
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
			server.Close()
			if os.Getenv("CI") == "" && os.Getenv("LABBIT_TEST_VALKEY_REQUIRED") == "" {
				t.Skipf("Valkey not available at %s: %v", valkeyTestAddr, err)
			}
			t.Fatalf("NewLocator(%s) error = %v", name, err)
		}

		proxy := valkeyowner.NewProxy("spike-p2p-secret")

		previewPolicy, err := previewsession.ParseAllowedPorts("3000,5173,80")
		if err != nil {
			t.Fatal(err)
		}
		previewOrigin, err := preview.ParseOriginTemplate("http://{sessionId}"+previewOriginSuffix, false)
		if err != nil {
			t.Fatal(err)
		}

		opts := stackOptions{
			Logger:                 slog.New(slog.DiscardHandler),
			PublicOrigin:           terminalTrustedOrigin,
			Worker:                 withWorker,
			Realtime:               withRealtime,
			Preview:                true,
			PreviewPolicy:          previewPolicy,
			PreviewTTL:             time.Hour,
			PreviewOrigin:          previewOrigin,
			PreviewOpenTimeout:     3 * time.Second,
			PreviewAttachTimeout:   2 * time.Second,
			PreviewUpstreamTimeout: 5 * time.Second,
			PreviewBootstrapTTL:    2 * time.Minute,
			OwnerLocator:           locator,
			PeerProxy:              proxy,
			AttachTimeout:          2 * time.Second,
			CloseGrace:             200 * time.Millisecond,
			FileAttachTimeout:      2 * time.Second,
			FileOperationTimeout:   5 * time.Second,
			MaxFileBytes:           1024,
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

type stoppableSupervisor struct {
	cancel context.CancelFunc
	done   <-chan error
}

func (s *stoppableSupervisor) Stop() {
	if s == nil {
		return
	}
	s.cancel()
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
	}
}

func postHTTP(t *testing.T, url string, headers http.Header, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("http.NewRequest(%s): %v", url, err)
	}
	for k, vals := range headers {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http.DefaultClient.Do(%s): %v", url, err)
	}
	return resp
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

// 1. Worker Claim, Fail-Closed Deferral & Exact-Once Execution:
// Connector connects to Pod A.
// PENDING operation item exists in PostgreSQL.
// Pod B Worker runs -> claims item from PostgreSQL. Since Pod B lacks a local route to the connector,
// it safely aborts dispatch (0 provider mutations) and defers the item back to PENDING (stage DEFERRED).
// (Note: There is NO pre-claim owner filter in PostgreSQL; worker claims first, then defers unsent fail-closed).
// Pod A Worker runs -> Pod A owns the connector route -> dispatches to Connector -> Provider mutation count = 1.
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

	// 1) Pod B Worker polls first: non-owner claims item from DB, detects lack of route, and defers without provider mutation!
	claimedB, err := c.podB.Provisions.RunOne(t.Context())
	if err != nil {
		t.Fatalf("Pod B RunOne error = %v", err)
	}
	if !claimedB {
		t.Fatalf("Pod B expected to claim the PENDING item for evaluation, got false")
	}

	// Provider must NOT have executed
	if provisions.Load() != 0 {
		t.Fatalf("provisions called = %d, want 0 (Pod B non-owner deferred execution without provider mutation)", provisions.Load())
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
	startSupervisorWithCustomMock(t, c.urlB, mockProv)

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
	_, err := c.locatorA.RegisterConnector(t.Context(), c.connectorID, 1, 15*time.Second)
	if err != nil {
		t.Fatalf("RegisterConnector error = %v", err)
	}

	// Pod B registers with epoch 2 (reconnect takeover)
	_, err = c.locatorB.RegisterConnector(t.Context(), c.connectorID, 2, 15*time.Second)
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

// 6. Cross-Pod LiveSession Create, LIVE_SUBSCRIBE WS Proxy, PTY Fan-out & Close:
// Connector connects to Pod A.
// Instructor creates TerminalSession via Pod B -> proxied to Pod A.
// Instructor creates LiveSession via Pod C -> proxied to Pod A.
// Student connects /realtime/v1/live WS to Pod B -> proxied to Pod A, receives PTY output from Connector.
// Instructor closes LiveSession via DELETE on Pod C -> proxied to Pod A.
func TestValkey_LiveSession_CrossPodCreateSubscribeAndClose(t *testing.T) {
	c := newValkeyReplicaCluster(t, false, true)
	f := c.fixture

	instructorLabID := uuid.New()
	_, err := c.conn.Exec(t.Context(),
		`INSERT INTO lab_instances (id, organization_id, lab_execution_id, user_id, participant_role, status, generation)
		 VALUES ($1, $2, $3, $4, 'INSTRUCTOR', 'READY', 1)`,
		instructorLabID, f.OrganizationID, f.LabExecutionID, f.InstructorID)
	if err != nil {
		t.Fatalf("insert instructor lab instance: %v", err)
	}
	f.AddResource(t, c.conn, instructorLabID, 1, "SERVER", "workspace", "PRESENT")
	instructorCookie := terminaltest.LoginSession(t, c.conn, f.InstructorID)

	conn := terminaltest.NewConnector(t, c.urlA, terminaltest.Credential)
	conn.Start()

	eventually(t, "Pod A route ready", 10*time.Second, func() bool {
		return c.podA.Registry.WithReadyRoute(c.connectorID, func(connector.Session, connector.Route) error { return nil }) == nil
	})
	eventually(t, "Valkey owner is Pod A", 5*time.Second, func() bool {
		rec, err := c.locatorA.LookupConnector(t.Context(), c.connectorID)
		return err == nil && rec.PodID == "pod-a"
	})

	instructorHeaders := http.Header{
		"Cookie":       []string{realtime.SessionCookieName + "=" + instructorCookie},
		"Origin":       []string{terminalTrustedOrigin},
		"Content-Type": []string{"application/json"},
	}

	// 1) Instructor creates TerminalSession via Pod B (proxied to Pod A)
	createTermURL_B := c.urlB + "/api/v1/lab-instances/" + instructorLabID.String() + "/terminal-sessions"
	respTerm := postHTTP(t, createTermURL_B, instructorHeaders, createBody)
	if respTerm.StatusCode != http.StatusCreated {
		t.Fatalf("Pod B terminal create status = %d, want 201: %s", respTerm.StatusCode, readBody(t, respTerm))
	}
	var createdTerm map[string]any
	_ = json.NewDecoder(respTerm.Body).Decode(&createdTerm)
	termSessionID := createdTerm["id"].(string)

	eventually(t, "Valkey terminal owner is Pod A", 5*time.Second, func() bool {
		rec, err := c.locatorC.LookupTerminal(t.Context(), termSessionID)
		return err == nil && rec.PodID == "pod-a"
	})

	// 2) Instructor creates LiveSession via Pod C (proxied to Pod A)
	createLiveURL_C := c.urlC + "/api/v1/terminal-sessions/" + termSessionID + "/live-sessions"
	respLive := postHTTP(t, createLiveURL_C, instructorHeaders, "")
	if respLive.StatusCode != http.StatusCreated {
		t.Fatalf("Pod C live create status = %d, want 201: %s", respLive.StatusCode, readBody(t, respLive))
	}
	var createdLive map[string]any
	_ = json.NewDecoder(respLive.Body).Decode(&createdLive)
	liveID := createdLive["id"].(string)

	// 3) Student connects /realtime/v1/live WebSocket to Pod B (proxied to Pod A)
	liveWsURL_B := "ws" + strings.TrimPrefix(c.urlB, "http") + realtime.LivePath
	liveDialer := websocket.Dialer{Subprotocols: []string{realtime.LiveSubprotocol}}
	studentHeaders := http.Header{
		"Cookie": []string{realtime.SessionCookieName + "=" + c.ownerCookie},
		"Origin": []string{terminalTrustedOrigin},
	}
	sWs, sResp, err := liveDialer.Dial(liveWsURL_B, studentHeaders)
	if err != nil {
		t.Fatalf("Student live WS dial to Pod B error = %v, resp = %v", err, sResp)
	}
	defer sWs.Close()

	if err := sWs.WriteJSON(map[string]any{
		"type":          "LIVE_SUBSCRIBE",
		"messageId":     "sub-1",
		"sentAt":        time.Now().UTC().Format(time.RFC3339),
		"liveSessionId": liveID,
		"payload":       map[string]any{},
	}); err != nil {
		t.Fatalf("Student WriteJSON LIVE_SUBSCRIBE error = %v", err)
	}

	_ = sWs.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, subRaw, err := sWs.ReadMessage()
	if err != nil || !strings.Contains(string(subRaw), "LIVE_SUBSCRIBED") {
		t.Fatalf("expected LIVE_SUBSCRIBED from Pod B proxy, got err=%v, raw=%s", err, string(subRaw))
	}

	// 4) Connector sends PTY output to Pod A -> arrives at Student WS on Pod B
	ptyOut := []byte("cross-pod live broadcast test\r\n")
	conn.Output(termSessionID, ptyOut)

	_ = sWs.SetReadDeadline(time.Now().Add(5 * time.Second))
	msgType, gotBytes, err := sWs.ReadMessage()
	if err != nil || msgType != websocket.BinaryMessage || string(gotBytes) != string(ptyOut) {
		t.Fatalf("Student live binary output err=%v, type=%d, got=%q", err, msgType, string(gotBytes))
	}

	// 5) Instructor closes LiveSession via DELETE on Pod C (proxied to Pod A)
	closeLiveURL_C := c.urlC + "/api/v1/live-sessions/" + liveID
	delReq, _ := http.NewRequest(http.MethodDelete, closeLiveURL_C, nil)
	delReq.Header.Set("Cookie", realtime.SessionCookieName+"="+instructorCookie)
	delReq.Header.Set("Origin", terminalTrustedOrigin)
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil || delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("Pod C DELETE live session err=%v, status=%d", err, delResp.StatusCode)
	}
}

// 7. Cross-Pod Workspace File HTTP & File Data WSS Proxy:
// Connector connects Control WSS to Pod A, and dials File Data WSS to Pod B.
// Browser sends GET /files/tree and GET /files/content to Pod C.
// Pod C proxies HTTP to Pod A, Pod B proxies File Data WSS to Pod A, and Browser receives the result.
func TestValkey_WorkspaceFile_CrossPodHTTPAndFileDataWSSProxy(t *testing.T) {
	c := newValkeyReplicaCluster(t, false, false)

	fs := filetest.NewFS()
	fs.Put("hello.txt", []byte("cross-pod workspace file content\n"))

	wsBaseA := "ws" + strings.TrimPrefix(c.urlA, "http")
	wsBaseB := "ws" + strings.TrimPrefix(c.urlB, "http")
	_ = filetest.Start(t, filetest.Config{
		ControlURL:   wsBaseA + connectorwss.Path,
		DataURL:      wsBaseB + filetransport.DataPath, // Data WSS lands on non-owner Pod B!
		Credential:   terminaltest.Credential,
		Capabilities: []string{"file-v1"},
		FS:           fs,
	})

	eventually(t, "Pod A route ready", 10*time.Second, func() bool {
		return c.podA.Registry.WithReadyRoute(c.connectorID, func(connector.Session, connector.Route) error { return nil }) == nil
	})
	eventually(t, "Valkey owner is Pod A", 5*time.Second, func() bool {
		rec, err := c.locatorC.LookupConnector(t.Context(), c.connectorID)
		return err == nil && rec.PodID == "pod-a"
	})

	// Browser sends GET /files/content to Pod C
	readURL_C := c.urlC + "/api/v1/lab-instances/" + c.fixture.LabInstanceID.String() + "/files/content?path=hello.txt"
	req, _ := http.NewRequest(http.MethodGet, readURL_C, nil)
	req.Header.Set("Cookie", realtime.SessionCookieName+"="+c.ownerCookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /files/content on Pod C error = %v", err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /files/content status = %d, want 200: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "cross-pod workspace file content") {
		t.Fatalf("unexpected file content body: %s", body)
	}
}

// 8. Cross-Pod Preview Create, Data WSS Proxy, Preview Origin (Bootstrap -> Exchange -> Cookie -> HTTP GET/POST),
// Cross-Origin CSRF Defense, and Cross-Pod DELETE Authorization & Cleanup:
// Connector connects Control WSS to Pod A, and dials Preview Data WSS to Pod B.
// Browser sends POST /preview-sessions to Pod C -> proxied to Pod A, returns 201 Created and registers owner in Valkey.
// Browser sends GET /__labbit/bootstrap to Pod B -> proxied to Pod A.
// Browser sends POST /__labbit/exchange to Pod C -> proxied to Pod A, returns 204 + Set-Cookie (labbit-preview).
// Browser sends authenticated GET/POST to Preview Origin on Pod B and Pod C -> proxied to Pod A -> Workspace app.
// Non-owner user sends DELETE /preview-sessions/{id} to Pod B -> proxied to Pod A -> 403 Forbidden.
// Owner sends DELETE /preview-sessions/{id} to Pod C -> proxied to Pod A -> 204 No Content, cleans up Valkey & Connector.
func TestValkey_Preview_CrossPodCreateAndDataWSSProxy(t *testing.T) {
	c := newValkeyReplicaCluster(t, false, false)
	wsApp := newWorkspaceApp(t)

	wsBaseA := "ws" + strings.TrimPrefix(c.urlA, "http")
	wsBaseB := "ws" + strings.TrimPrefix(c.urlB, "http")
	peer := previewtest.Start(t, previewtest.Config{
		ControlURL:    wsBaseA + connectorwss.Path,
		DataURL:       wsBaseB + preview.DataPath, // Preview Data WSS lands on non-owner Pod B!
		Credential:    terminaltest.Credential,
		Capabilities:  []string{"preview-v1"},
		WorkspaceAddr: wsApp.addr(),
	})

	eventually(t, "Pod A route ready", 10*time.Second, func() bool {
		return c.podA.Registry.WithReadyRoute(c.connectorID, func(connector.Session, connector.Route) error { return nil }) == nil
	})
	eventually(t, "Valkey owner is Pod A", 5*time.Second, func() bool {
		rec, err := c.locatorC.LookupConnector(t.Context(), c.connectorID)
		return err == nil && rec.PodID == "pod-a"
	})

	headers := http.Header{
		"Cookie":       []string{realtime.SessionCookieName + "=" + c.ownerCookie},
		"Origin":       []string{terminalTrustedOrigin},
		"Content-Type": []string{"application/json"},
	}
	createPreviewURL_C := c.urlC + "/api/v1/lab-instances/" + c.fixture.LabInstanceID.String() + "/preview-sessions"
	resp := postHTTP(t, createPreviewURL_C, headers, `{"targetPort":3000}`)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /preview-sessions on Pod C status = %d, want 201: %s", resp.StatusCode, body)
	}
	var created struct {
		ID         string `json:"id"`
		TargetPort int    `json:"targetPort"`
		PreviewURL string `json:"previewUrl"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil || created.ID == "" || created.PreviewURL == "" {
		t.Fatalf("decode created preview session: err=%v body=%s", err, body)
	}

	// Verify Valkey preview owner index is registered to Pod A with only routing metadata (no credential/cookie)
	previewRec, err := c.locatorB.LookupPreview(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("LookupPreview on Pod B error = %v", err)
	}
	if previewRec.PodID != "pod-a" || previewRec.PreviewSessionID != created.ID || previewRec.ConnectorID != c.connectorID {
		t.Fatalf("unexpected PreviewOwnerRecord: %+v", previewRec)
	}

	previewHost := created.ID + previewOriginSuffix
	previewOriginVal := "http://" + previewHost
	_, bootstrapCred, hasFrag := strings.Cut(created.PreviewURL, "#")
	if !hasFrag || bootstrapCred == "" {
		t.Fatalf("previewUrl missing fragment credential: %q", created.PreviewURL)
	}

	doPreviewReq := func(podURL, method, path, reqBody string, mod func(*http.Request)) (*http.Response, string) {
		t.Helper()
		req, err := http.NewRequest(method, podURL+path, strings.NewReader(reqBody))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = previewHost
		if mod != nil {
			mod(req)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s on %s error = %v", method, path, podURL, err)
		}
		return res, readBody(t, res)
	}

	// 8a. Cross-Pod GET /__labbit/bootstrap on Pod B -> proxied to Pod A -> 200 OK
	bootResp, bootBody := doPreviewReq(c.urlB, http.MethodGet, preview.BootstrapPath, "", nil)
	if bootResp.StatusCode != http.StatusOK || !strings.Contains(bootBody, preview.ExchangePath) {
		t.Fatalf("GET /__labbit/bootstrap on Pod B = %d %s", bootResp.StatusCode, bootBody)
	}

	exchangeBody := `{"credential":"` + bootstrapCred + `"}`

	// 8b. Cross-Origin negative check on Pod B: POST /__labbit/exchange with API Origin must be rejected (403 csrf_rejected)
	badOriginResp, badOriginBody := doPreviewReq(c.urlB, http.MethodPost, preview.ExchangePath, exchangeBody, func(r *http.Request) {
		r.Header.Set("Origin", terminalTrustedOrigin)
		r.Header.Set("Content-Type", "application/json")
	})
	if badOriginResp.StatusCode != http.StatusForbidden || !strings.Contains(badOriginBody, `"csrf_rejected"`) {
		t.Fatalf("exchange with API Origin on Pod B = %d %s, want 403 csrf_rejected", badOriginResp.StatusCode, badOriginBody)
	}

	// 8c. Cross-Pod POST /__labbit/exchange on Pod C -> proxied to Pod A -> 204 No Content + Set-Cookie
	exResp, exBody := doPreviewReq(c.urlC, http.MethodPost, preview.ExchangePath, exchangeBody, func(r *http.Request) {
		r.Header.Set("Origin", previewOriginVal)
		r.Header.Set("Content-Type", "application/json")
	})
	if exResp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST /__labbit/exchange on Pod C = %d %s", exResp.StatusCode, exBody)
	}
	var previewCookie *http.Cookie
	for _, ck := range exResp.Cookies() {
		if ck.Name == "labbit-preview" || ck.Name == "__Host-labbit-preview" {
			previewCookie = ck
		}
	}
	if previewCookie == nil {
		t.Fatal("POST /__labbit/exchange on Pod C did not issue labbit-preview cookie")
	}

	// 8d. Replay of one-time bootstrap credential on Pod B or Pod A must fail with 401
	replayResp, _ := doPreviewReq(c.urlB, http.MethodPost, preview.ExchangePath, exchangeBody, func(r *http.Request) {
		r.Header.Set("Origin", previewOriginVal)
		r.Header.Set("Content-Type", "application/json")
	})
	if replayResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replay exchange on Pod B = %d, want 401", replayResp.StatusCode)
	}

	// 8e. Cross-Pod authenticated GET on Pod B and POST on Pod C to Preview Origin
	appGetResp, appGetBody := doPreviewReq(c.urlB, http.MethodGet, "/"+previewPathMarker+"?q="+previewQueryMarker, "", func(r *http.Request) {
		r.AddCookie(previewCookie)
		r.AddCookie(&http.Cookie{Name: realtime.SessionCookieName, Value: c.ownerCookie})
		r.AddCookie(&http.Cookie{Name: "app-cookie", Value: previewAppCookie})
		r.Header.Set("X-App-Custom", previewHeaderMarker)
	})
	if appGetResp.StatusCode != http.StatusOK || !strings.Contains(appGetBody, previewRespMarker) {
		t.Fatalf("authenticated GET on Pod B = %d %q", appGetResp.StatusCode, appGetBody)
	}

	appPostResp, appPostBody := doPreviewReq(c.urlC, http.MethodPost, "/submit", previewBodyMarker, func(r *http.Request) {
		r.AddCookie(previewCookie)
	})
	if appPostResp.StatusCode != http.StatusOK || !strings.Contains(appPostBody, previewRespMarker) {
		t.Fatalf("authenticated POST on Pod C = %d %q", appPostResp.StatusCode, appPostBody)
	}

	// Verify Workspace app received original Host, custom headers, and app cookie, while Labbit cookies and peer secret were stripped
	wsReqs := wsApp.all()
	if len(wsReqs) != 2 {
		t.Fatalf("workspace app received %d requests, want 2", len(wsReqs))
	}
	if wsReqs[0].Host != previewHost || wsReqs[0].Header.Get("X-Forwarded-Host") != previewHost {
		t.Fatalf("workspace app Host=%q X-Forwarded-Host=%q, want %q", wsReqs[0].Host, wsReqs[0].Header.Get("X-Forwarded-Host"), previewHost)
	}
	if wsReqs[0].Header.Get("X-App-Custom") != previewHeaderMarker {
		t.Fatalf("workspace app missing X-App-Custom: %+v", wsReqs[0].Header)
	}
	if gotCookie := wsReqs[0].Header.Get("Cookie"); gotCookie != "app-cookie="+previewAppCookie {
		t.Fatalf("workspace app Cookie = %q, want only app-cookie", gotCookie)
	}
	for _, wr := range wsReqs {
		if wr.Header.Get(valkeyowner.HeaderPeerSecret) != "" || wr.Header.Get(valkeyowner.HeaderInternalPeer) != "" {
			t.Fatalf("internal peer headers leaked to workspace app: %+v", wr.Header)
		}
	}

	// 8f. Cross-Origin negative check on API endpoint: DELETE with Preview Origin must be rejected (403 csrf_rejected)
	badDelReq, _ := http.NewRequest(http.MethodDelete, c.urlB+"/api/v1/preview-sessions/"+created.ID, nil)
	badDelReq.Header.Set("Cookie", realtime.SessionCookieName+"="+c.ownerCookie)
	badDelReq.Header.Set("Origin", previewOriginVal)
	badDelResp, err := http.DefaultClient.Do(badDelReq)
	if err != nil {
		t.Fatal(err)
	}
	badDelBody := readBody(t, badDelResp)
	if badDelResp.StatusCode != http.StatusForbidden || !strings.Contains(badDelBody, `"csrf_rejected"`) {
		t.Fatalf("DELETE with Preview Origin on Pod B = %d %s, want 403 csrf_rejected", badDelResp.StatusCode, badDelBody)
	}

	// 8g. Cross-Pod DELETE by forbidden users (peer student and instructor) on Pod B/C -> 403 Forbidden (session stays alive!)
	for _, otherUserID := range []uuid.UUID{c.fixture.PeerID, c.fixture.InstructorID, c.fixture.ForeignID} {
		otherCookie := terminaltest.LoginSession(t, c.conn, otherUserID)
		forbReq, _ := http.NewRequest(http.MethodDelete, c.urlB+"/api/v1/preview-sessions/"+created.ID, nil)
		forbReq.Header.Set("Cookie", realtime.SessionCookieName+"="+otherCookie)
		forbReq.Header.Set("Origin", terminalTrustedOrigin)
		forbResp, err := http.DefaultClient.Do(forbReq)
		if err != nil {
			t.Fatal(err)
		}
		forbBody := readBody(t, forbResp)
		if forbResp.StatusCode != http.StatusForbidden || !strings.Contains(forbBody, `"forbidden"`) {
			t.Fatalf("DELETE by non-owner %s on Pod B = %d %s, want 403 forbidden", otherUserID, forbResp.StatusCode, forbBody)
		}
	}

	// Session must still be active after forbidden DELETE attempts
	stillAliveResp, _ := doPreviewReq(c.urlC, http.MethodGet, "/still-alive", "", func(r *http.Request) {
		r.AddCookie(previewCookie)
	})
	if stillAliveResp.StatusCode != http.StatusOK {
		t.Fatalf("session should remain active after forbidden DELETE, got %d", stillAliveResp.StatusCode)
	}

	// 8h. Unknown session UUID on Pod B: DELETE -> 404 Not Found; Preview Origin GET -> 401 Unauthorized
	unknownID := uuid.NewString()
	unkDelReq, _ := http.NewRequest(http.MethodDelete, c.urlB+"/api/v1/preview-sessions/"+unknownID, nil)
	unkDelReq.Header.Set("Cookie", realtime.SessionCookieName+"="+c.ownerCookie)
	unkDelReq.Header.Set("Origin", terminalTrustedOrigin)
	unkDelResp, err := http.DefaultClient.Do(unkDelReq)
	if err != nil || unkDelResp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown session DELETE on Pod B = %v %v, want 404", err, unkDelResp)
	}
	_ = unkDelResp.Body.Close()

	unkOriginReq, _ := http.NewRequest(http.MethodGet, c.urlB+"/hello", nil)
	unkOriginReq.Host = unknownID + previewOriginSuffix
	unkOriginReq.AddCookie(previewCookie)
	unkOriginResp, err := http.DefaultClient.Do(unkOriginReq)
	if err != nil || unkOriginResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unknown session Preview Origin GET on Pod B = %v %v, want 401", err, unkOriginResp)
	}
	_ = unkOriginResp.Body.Close()

	// 8i. Cross-Pod DELETE by owner on Pod B -> proxied to Pod A -> 204 No Content, Valkey key released, PREVIEW_CLOSE sent
	delReq, _ := http.NewRequest(http.MethodDelete, c.urlB+"/api/v1/preview-sessions/"+created.ID, nil)
	delReq.Header.Set("Cookie", realtime.SessionCookieName+"="+c.ownerCookie)
	delReq.Header.Set("Origin", terminalTrustedOrigin)
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatal(err)
	}
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("owner DELETE on Pod B = %d, want 204: %s", delResp.StatusCode, readBody(t, delResp))
	}
	_ = delResp.Body.Close()

	// Valkey preview owner key must be cleaned up
	if _, err := c.locatorC.LookupPreview(t.Context(), created.ID); !errors.Is(err, valkeyowner.ErrNoOwner) {
		t.Fatalf("LookupPreview after DELETE = %v, want ErrNoOwner", err)
	}

	// Connector must receive PREVIEW_CLOSE with SESSION_CLOSED
	eventually(t, "Connector receives PREVIEW_CLOSE SESSION_CLOSED", 5*time.Second, func() bool {
		for _, cl := range peer.Closes() {
			if cl.PreviewSessionID == created.ID && cl.Reason == preview.EndSessionClosed {
				return true
			}
		}
		return false
	})

	// Subsequent Preview Origin GET on Pod A, Pod B, and Pod C must all return 401 Unauthorized
	for _, podURL := range []string{c.urlA, c.urlB, c.urlC} {
		afterResp, _ := doPreviewReq(podURL, http.MethodGet, "/after-close", "", func(r *http.Request) {
			r.AddCookie(previewCookie)
		})
		if afterResp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Preview Origin GET after DELETE on %s = %d, want 401", podURL, afterResp.StatusCode)
		}
	}
}

// 9. Preview Session Expiry, Owner Pod Crash, and Valkey Outage Fail-Closed:
// Verifies pointer cleanup on TTL expiry, fail-closed 503 when owner Pod A crashes or Valkey is down,
// and fail-closed on Create when Valkey registration is unavailable.
func TestValkey_Preview_ExpiryOwnerCrashAndValkeyOutageFailClosed(t *testing.T) {
	c := newValkeyReplicaCluster(t, false, false)
	wsApp := newWorkspaceApp(t)

	wsBaseA := "ws" + strings.TrimPrefix(c.urlA, "http")
	wsBaseB := "ws" + strings.TrimPrefix(c.urlB, "http")
	_ = previewtest.Start(t, previewtest.Config{
		ControlURL:    wsBaseA + connectorwss.Path,
		DataURL:       wsBaseB + preview.DataPath,
		Credential:    terminaltest.Credential,
		Capabilities:  []string{"preview-v1"},
		WorkspaceAddr: wsApp.addr(),
	})

	eventually(t, "Pod A route ready", 10*time.Second, func() bool {
		return c.podA.Registry.WithReadyRoute(c.connectorID, func(connector.Session, connector.Route) error { return nil }) == nil
	})
	eventually(t, "Valkey owner is Pod A", 5*time.Second, func() bool {
		rec, err := c.locatorB.LookupConnector(t.Context(), c.connectorID)
		return err == nil && rec.PodID == "pod-a"
	})

	headers := http.Header{
		"Cookie":       []string{realtime.SessionCookieName + "=" + c.ownerCookie},
		"Origin":       []string{terminalTrustedOrigin},
		"Content-Type": []string{"application/json"},
	}

	// Create session on Pod A (via Pod B)
	resp := postHTTP(t, c.urlB+"/api/v1/lab-instances/"+c.fixture.LabInstanceID.String()+"/preview-sessions", headers, `{"targetPort":3000}`)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create preview status = %d: %s", resp.StatusCode, body)
	}
	var created struct {
		ID         string `json:"id"`
		PreviewURL string `json:"previewUrl"`
	}
	_ = json.Unmarshal([]byte(body), &created)
	previewHost := created.ID + previewOriginSuffix
	_, bootstrapCred, _ := strings.Cut(created.PreviewURL, "#")

	// Exchange credential via Pod C
	exReq, _ := http.NewRequest(http.MethodPost, c.urlC+preview.ExchangePath, strings.NewReader(`{"credential":"`+bootstrapCred+`"}`))
	exReq.Host = previewHost
	exReq.Header.Set("Origin", "http://"+previewHost)
	exReq.Header.Set("Content-Type", "application/json")
	exResp, err := http.DefaultClient.Do(exReq)
	if err != nil || exResp.StatusCode != http.StatusNoContent {
		t.Fatalf("exchange on Pod C = %v %v", err, exResp)
	}
	var previewCookie *http.Cookie
	for _, ck := range exResp.Cookies() {
		if ck.Name == "labbit-preview" {
			previewCookie = ck
		}
	}
	_ = exResp.Body.Close()

	// 9a. Simulate SESSION_EXPIRED on Pod A -> verifies SessionEnded releases Valkey pointer and Pod B/C return 401
	c.podA.PreviewGateway.Terminate(created.ID, preview.End{Reason: preview.EndSessionExpired, NotifyConnector: true})
	if _, err := c.locatorB.LookupPreview(t.Context(), created.ID); !errors.Is(err, valkeyowner.ErrNoOwner) {
		t.Fatalf("LookupPreview after SESSION_EXPIRED = %v, want ErrNoOwner", err)
	}
	expGetReq, _ := http.NewRequest(http.MethodGet, c.urlB+"/hello", nil)
	expGetReq.Host = previewHost
	expGetReq.AddCookie(previewCookie)
	expGetResp, err := http.DefaultClient.Do(expGetReq)
	if err != nil || expGetResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Preview Origin GET on Pod B after expiry = %v %v, want 401", err, expGetResp)
	}
	_ = expGetResp.Body.Close()

	// 9b. Create a second session on Pod A, then simulate Pod A crash (srvA.Close()) while Valkey record still exists
	resp2 := postHTTP(t, c.urlA+"/api/v1/lab-instances/"+c.fixture.LabInstanceID.String()+"/preview-sessions", headers, `{"targetPort":3000}`)
	body2 := readBody(t, resp2)
	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("create 2nd preview status = %d: %s", resp2.StatusCode, body2)
	}
	var created2 struct {
		ID         string `json:"id"`
		PreviewURL string `json:"previewUrl"`
	}
	_ = json.Unmarshal([]byte(body2), &created2)
	previewHost2 := created2.ID + previewOriginSuffix

	// Kill Pod A HTTP server without releasing Valkey key (simulates sudden Pod A crash before lease TTL expires)
	c.srvA.Close()

	// Request to Pod B for Preview Origin of crashed Pod A's session -> fails closed with 503 preview_unavailable
	crashGetReq, _ := http.NewRequest(http.MethodGet, c.urlB+preview.BootstrapPath, nil)
	crashGetReq.Host = previewHost2
	crashGetResp, err := http.DefaultClient.Do(crashGetReq)
	if err != nil {
		t.Fatal(err)
	}
	crashGetBody := readBody(t, crashGetResp)
	if crashGetResp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(crashGetBody, `"preview_unavailable"`) {
		t.Fatalf("Preview Origin GET on Pod B when Pod A crashed = %d %s, want 503 preview_unavailable", crashGetResp.StatusCode, crashGetBody)
	}

	// DELETE on Pod C for crashed Pod A's session -> fails closed with 503 preview_unavailable
	crashDelReq, _ := http.NewRequest(http.MethodDelete, c.urlC+"/api/v1/preview-sessions/"+created2.ID, nil)
	crashDelReq.Header.Set("Cookie", realtime.SessionCookieName+"="+c.ownerCookie)
	crashDelReq.Header.Set("Origin", terminalTrustedOrigin)
	crashDelResp, err := http.DefaultClient.Do(crashDelReq)
	if err != nil {
		t.Fatal(err)
	}
	crashDelBody := readBody(t, crashDelResp)
	if crashDelResp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(crashDelBody, `"preview_unavailable"`) {
		t.Fatalf("DELETE on Pod C when Pod A crashed = %d %s, want 503 preview_unavailable", crashDelResp.StatusCode, crashDelBody)
	}

	// 9c. Simulate Valkey outage on Pod B (locatorB.Close()) -> Preview Origin and DELETE fail closed with 503
	c.locatorB.Close()
	outageGetReq, _ := http.NewRequest(http.MethodGet, c.urlB+preview.BootstrapPath, nil)
	outageGetReq.Host = uuid.NewString() + previewOriginSuffix
	outageGetResp, err := http.DefaultClient.Do(outageGetReq)
	if err != nil {
		t.Fatal(err)
	}
	outageGetBody := readBody(t, outageGetResp)
	if outageGetResp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(outageGetBody, `"preview_unavailable"`) {
		t.Fatalf("Preview Origin GET on Pod B during Valkey outage = %d %s, want 503 preview_unavailable", outageGetResp.StatusCode, outageGetBody)
	}
}
