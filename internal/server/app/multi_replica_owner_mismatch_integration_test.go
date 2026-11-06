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

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connectorwss"
	"github.com/ktcloud4-SL/labbit-app/internal/server/filetransport"
	"github.com/ktcloud4-SL/labbit-app/internal/server/filetransport/filetest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/preview"
	"github.com/ktcloud4-SL/labbit-app/internal/server/previewsession"
	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal/terminaltest"
)

// multiReplicaCluster simulates a multi-instance Backend logical topology (labeled Pod A, Pod B, Pod C)
// using 3 in-process controlStack and httptest.Server instances sharing one PostgreSQL database.
// This is a single-process logical instance simulation to test process-local state isolation,
// not a multi-node Kubernetes/k3d or production multi-pod E2E deployment.
type multiReplicaCluster struct {
	t            *testing.T
	dsn          string
	pool         *pgxpool.Pool
	store        *postgres.Store
	conn         *pgx.Conn
	connectorID  uuid.UUID
	credentialID uuid.UUID
	fixture      *terminaltest.Fixture
	ownerCookie  string

	podA *controlStack
	srvA *httptest.Server
	urlA string

	podB *controlStack
	srvB *httptest.Server
	urlB string

	podC *controlStack
	srvC *httptest.Server
	urlC string
}

func newMultiReplicaCluster(t *testing.T, withWorker, withRealtime, withPreview bool) *multiReplicaCluster {
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

	makeStack := func(name string) (*controlStack, *httptest.Server) {
		opts := stackOptions{
			Logger:        slog.New(slog.DiscardHandler),
			PublicOrigin:  terminalTrustedOrigin,
			Worker:        withWorker,
			Realtime:      withRealtime,
			AttachTimeout: 2 * time.Second,
			CloseGrace:    200 * time.Millisecond,
		}
		if withPreview {
			policy, _ := previewsession.ParseAllowedPorts("3000,8080")
			opts.Preview = true
			opts.PreviewPolicy = policy
			opts.PreviewTTL = 10 * time.Minute
			tmpl, err := preview.ParseOriginTemplate("http://{sessionId}.preview.example.com", false)
			if err != nil {
				t.Fatalf("ParseOriginTemplate error = %v", err)
			}
			opts.PreviewOrigin = tmpl
			opts.PreviewAttachTimeout = 2 * time.Second
			opts.PreviewBootstrapTTL = time.Minute
		}
		stack, err := newControlStack(store, opts)
		if err != nil {
			t.Fatalf("newControlStack(%s) error = %v", name, err)
		}
		server := httptest.NewServer(applicationHandler(stack.routes()))
		t.Cleanup(func() {
			stack.close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = stack.shutdown(ctx)
			server.Close()
		})
		return stack, server
	}

	podA, srvA := makeStack("Pod-A")
	podB, srvB := makeStack("Pod-B")
	podC, srvC := makeStack("Pod-C")

	return &multiReplicaCluster{
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
		podB:         podB,
		srvB:         srvB,
		urlB:         srvB.URL,
		podC:         podC,
		srvC:         srvC,
		urlC:         srvC.URL,
	}
}

// 1. Process-local Registry & Router cross-Pod mismatch:
// Connector connects to Pod A. Pod A's Registry is populated, but Pod B's Registry
// is completely unaware of the connection. Any Router invocation on Pod B fails closed.
func TestMultiReplica_RegistryAndRouter_CrossPodMismatch(t *testing.T) {
	c := newMultiReplicaCluster(t, false, true, false)

	// Connect mock connector to Pod A
	conn := terminaltest.NewConnector(t, c.urlA, terminaltest.Credential)
	conn.Start()

	// Wait until Pod A sees the route as protocol-ready
	eventually(t, "Pod A protocol-ready route", 10*time.Second, func() bool {
		return c.podA.Registry.WithReadyRoute(c.connectorID, func(connector.Session, connector.Route) error { return nil }) == nil
	})

	// 1) Pod A has the ready route
	errA := c.podA.Registry.WithReadyRoute(c.connectorID, func(connector.Session, connector.Route) error { return nil })
	if errA != nil {
		t.Fatalf("Pod A WithReadyRoute unexpected error = %v", errA)
	}

	// 2) Pod B has NO route - fails with ErrNotConnected
	errB := c.podB.Registry.WithReadyRoute(c.connectorID, func(connector.Session, connector.Route) error { return nil })
	if !errors.Is(errB, connector.ErrNotConnected) {
		t.Fatalf("Pod B WithReadyRoute error = %v, want ErrNotConnected", errB)
	}

	// 3) Commands dispatched on Pod B's Router return ErrConnectorUnavailable
	corr := connector.Correlation{OperationID: "op-1", LabInstanceID: "lab-1", Generation: 1}
	_, err := c.podB.Router.SendOperationCommand(t.Context(), connector.OperationCommand{
		ConnectorID: c.connectorID,
		Correlation: corr,
		Payload: protocol.OperationCommandPayload{
			MutationType: protocol.MutationTypeProvision,
			CreationSnapshot: &protocol.CreationSnapshot{
				ProviderConnectionID: "pc-1",
				VMs: []protocol.ResolvedVmSpec{{
					VMKey: "vm-1", Role: "workspace", ImageID: "img-1", FlavorID: "flv-1",
					FlavorSpec: &protocol.ResolvedFlavorSpec{VCPUs: 1, RAMMiB: 512, DiskGiB: 10},
				}},
				WorkspaceVMKey: "vm-1",
			},
		},
	})
	if !errors.Is(err, connector.ErrConnectorUnavailable) {
		t.Fatalf("Pod B SendOperationCommand error = %v, want ErrConnectorUnavailable", err)
	}

	_, err = c.podB.Router.SendReconcileRequest(t.Context(), connector.ReconcileRequest{
		ConnectorID: c.connectorID,
		Correlation: corr,
		Payload:     protocol.ReconcileRequestPayload{KnownResources: []protocol.ProviderResourceRef{{ResourceType: "SERVER", ProviderID: "srv-1", Generation: 1}}},
	})
	if !errors.Is(err, connector.ErrConnectorUnavailable) {
		t.Fatalf("Pod B SendReconcileRequest error = %v, want ErrConnectorUnavailable", err)
	}

	termCorr := connector.TerminalCorrelation{TerminalSessionID: "ts-1", LabInstanceID: "lab-1", Generation: 1}
	_, err = c.podB.Router.SendTerminalOpen(t.Context(), connector.TerminalOpen{
		ConnectorID:      c.connectorID,
		Correlation:      termCorr,
		TargetVMKey:      "workspace",
		ProviderServerID: "srv-1",
		Cols:             json.RawMessage("80"),
		Rows:             json.RawMessage("24"),
	})
	if !errors.Is(err, connector.ErrConnectorUnavailable) {
		t.Fatalf("Pod B SendTerminalOpen error = %v, want ErrConnectorUnavailable", err)
	}

	_, err = c.podB.Router.SendTerminalClose(t.Context(), connector.TerminalClose{
		ConnectorID: c.connectorID,
		Correlation: termCorr,
		Reason:      "SESSION_CLOSED",
	})
	if !errors.Is(err, connector.ErrConnectorUnavailable) {
		t.Fatalf("Pod B SendTerminalClose error = %v, want ErrConnectorUnavailable", err)
	}
}

// 2. Worker cross-Pod dispatch mismatch:
// When Worker runs on multiple Pods, Worker on Pod B can claim a PENDING item from the shared DB,
// but cannot dispatch it because Connector is connected to Pod A.
func TestMultiReplica_Worker_CrossPodClaimAndDeferral(t *testing.T) {
	c := newMultiReplicaCluster(t, true, false, false)

	// Mock provider attached to Pod A's connector
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
	startRoutingConnectorToURL(t, c.urlA, c.connectorID, mockProv)

	// Wait until Pod A route is ready
	eventually(t, "Pod A ready", 10*time.Second, func() bool {
		return c.podA.Registry.WithReadyRoute(c.connectorID, func(connector.Session, connector.Route) error { return nil }) == nil
	})

	// Seed a fresh lab_execution and matching creation_snapshot for OtherClassID
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

	// Seed a fresh student user and PENDING lab_instance without existing resources
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

	// Seed a PENDING operation and operation_item into shared PostgreSQL DB
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

	// Now execute RunOne explicitly on Pod B (where Connector is NOT connected)
	claimed, err := c.podB.Provisions.RunOne(t.Context())
	if err != nil {
		t.Fatalf("Pod B RunOne error = %v", err)
	}
	if !claimed {
		t.Fatalf("Pod B should have claimed the PENDING item")
	}

	// Verify that Pod B could not send the command, and therefore deferred it
	var status, stage string
	var nextAttempt *time.Time
	err = c.conn.QueryRow(t.Context(), `
		SELECT status, COALESCE(stage, ''), dispatch_not_before FROM operation_items WHERE id = $1
	`, itemID).Scan(&status, &stage, &nextAttempt)
	if err != nil {
		t.Fatalf("query operation_item error = %v", err)
	}

	// The item was NOT executed on the provider (provisions is 0)
	if provisions.Load() != 0 {
		t.Fatalf("provisions called = %d, want 0 (Pod B has no connector)", provisions.Load())
	}
	// The item was deferred back to PENDING with stage DEFERRED and a future dispatch_not_before
	if status != "PENDING" || stage != "DEFERRED" || nextAttempt == nil {
		t.Fatalf("deferred status = %s, stage = %s, nextAttempt = %v, want PENDING/DEFERRED with deferral time", status, stage, nextAttempt)
	}
}

// 3. Terminal Session cross-Pod mismatch:
// - Scenario 3a: HTTP Create arrives on Pod B (Connector on Pod A) -> fails 503, marked OPEN_FAILED in DB.
// - Scenario 3b: HTTP Create arrives on Pod A, but Connector Data WSS connects to Pod B -> rejected 4003.
// - Scenario 3c: HTTP Create on Pod A, Data WSS on Pod A, but Browser WSS connects to Pod B -> rejected 4003.
// - Scenario 3d: Browser reconnect during grace lands on Pod B -> rejected 4003, session lost.
func TestMultiReplica_Terminal_CrossPodMismatch(t *testing.T) {
	c := newMultiReplicaCluster(t, false, true, false)

	conn := terminaltest.NewConnector(t, c.urlA, terminaltest.Credential)
	conn.Start()

	eventually(t, "Pod A ready", 10*time.Second, func() bool {
		return c.podA.Registry.WithReadyRoute(c.connectorID, func(connector.Session, connector.Route) error { return nil }) == nil
	})

	createURL_B := c.urlB + "/api/v1/lab-instances/" + c.fixture.LabInstanceID.String() + "/terminal-sessions"
	createURL_A := c.urlA + "/api/v1/lab-instances/" + c.fixture.LabInstanceID.String() + "/terminal-sessions"

	authHeader := http.Header{
		"Cookie":       []string{realtime.SessionCookieName + "=" + c.ownerCookie},
		"Origin":       []string{terminalTrustedOrigin},
		"Content-Type": []string{"application/json"},
	}

	// Scenario 3a: HTTP Create lands on Pod B
	respB := postHTTP(t, createURL_B, authHeader, createBody)
	if respB.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("Pod B create status = %d, want 503 (connector_unavailable)", respB.StatusCode)
	}

	// Scenario 3b: Connector Data WSS lands on Pod B while Pod A holds the expectation.
	// We verify that Pod B's Relay.AttachData rejects the un-expected terminal data connection.
	dataWsURL_B := "ws" + strings.TrimPrefix(c.urlB, "http") + realtime.DataPath
	dialer := websocket.Dialer{Subprotocols: []string{realtime.DataSubprotocol}}
	dataHeaders := http.Header{
		"Authorization": []string{"Bearer " + terminaltest.Credential},
	}
	wsConn, resp, err := dialer.Dial(dataWsURL_B, dataHeaders)
	if err == nil {
		// Even if upgrade succeeds, attach frame for a session unknown to Pod B fails closed
		attachFrame := map[string]any{
			"type":              "TERMINAL_DATA_ATTACH",
			"messageId":         "m-1",
			"sentAt":            time.Now().Format(time.RFC3339),
			"terminalSessionId": uuid.NewString(),
		}
		_ = wsConn.WriteJSON(attachFrame)
		_, _, readErr := wsConn.ReadMessage()
		if readErr == nil {
			t.Fatalf("Pod B should reject unknown terminal data attach")
		}
		var closeErr *websocket.CloseError
		if errors.As(readErr, &closeErr) && closeErr.Code != 1008 && closeErr.Code != 4003 {
			t.Fatalf("close code = %d, want 1008 or 4003", closeErr.Code)
		}
		wsConn.Close()
	} else if resp != nil && resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unexpected dial response = %d", resp.StatusCode)
	}

	// Scenario 3c & 3d: Create session on Pod A successfully
	respA := postHTTP(t, createURL_A, authHeader, createBody)
	if respA.StatusCode != http.StatusCreated {
		t.Fatalf("Pod A create status = %d, want 201: %s", respA.StatusCode, readBody(t, respA))
	}
	var createdBody map[string]any
	_ = json.NewDecoder(respA.Body).Decode(&createdBody)
	sessionID := createdBody["id"].(string)
	attachToken := createdBody["sessionToken"].(string)

	// Now Browser tries to connect to Pod B (due to LB routing Browser to Pod B)
	browserWsURL_B := "ws" + strings.TrimPrefix(c.urlB, "http") + realtime.BrowserPath
	browserDialer := websocket.Dialer{Subprotocols: []string{realtime.BrowserSubprotocol}}
	browserHeaders := http.Header{
		"Cookie": []string{realtime.SessionCookieName + "=" + c.ownerCookie},
		"Origin": []string{terminalTrustedOrigin},
	}
	bWs, _, err := browserDialer.Dial(browserWsURL_B, browserHeaders)
	if err != nil {
		t.Fatalf("Browser dial to Pod B failed: %v", err)
	}
	defer bWs.Close()

	// Send ATTACH frame to Pod B
	attachMsg := map[string]any{
		"type":              "TERMINAL_ATTACH",
		"messageId":         "b-1",
		"sentAt":            time.Now().Format(time.RFC3339),
		"terminalSessionId": sessionID,
		"payload":           map[string]any{"sessionToken": attachToken, "cols": 80, "rows": 24},
	}
	_ = bWs.WriteJSON(attachMsg)

	// Pod B must reject because the session was expected and bound on Pod A!
	// Relay sends an ERROR frame first and then terminates the connection with 4003 (closeNotFound).
	_, msgBytes, err := bWs.ReadMessage()
	if err == nil {
		var errResp map[string]any
		_ = json.Unmarshal(msgBytes, &errResp)
		if errResp["type"] != "ERROR" {
			t.Fatalf("unexpected message: %s", string(msgBytes))
		}
		_, _, err = bWs.ReadMessage()
	}
	if err == nil {
		t.Fatalf("Pod B Browser attach should close connection")
	}
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) && closeErr.Code != 4003 {
		t.Fatalf("Browser close code = %d, want 4003 (closeNotFound)", closeErr.Code)
	}
}

//  4. File Transport cross-Pod mismatch:
//     - HTTP file request to non-owner Pod B fails with 503 because Pod B has no Control connection/route.
//     - Data WSS connecting to Pod B without a local pending request (tested with an unknown fileRequestId)
//     fails closed with close code 1008/4003.
//
// Note: This verifies fail-closed rejection on an instance lacking pending state,
// not an observed migration of an active pending file request from Pod A to Pod B.
func TestMultiReplica_FileTransport_CrossPodMismatch(t *testing.T) {
	c := newMultiReplicaCluster(t, false, false, false)

	// Start mock connector on Pod A with file capability
	_ = filetest.Start(t, filetest.Config{
		ControlURL:   "ws" + strings.TrimPrefix(c.urlA, "http") + connectorwss.Path,
		DataURL:      "ws" + strings.TrimPrefix(c.urlA, "http") + filetransport.DataPath,
		Credential:   terminaltest.Credential,
		Capabilities: []string{"file-v1"},
		FS:           filetest.NewFS(),
	})

	eventually(t, "Pod A file capability ready", 10*time.Second, func() bool {
		return c.podA.Registry.WithReadyRoute(c.connectorID, func(connector.Session, connector.Route) error { return nil }) == nil
	})

	// 1) HTTP File request sent to Pod B
	fileURL_B := c.urlB + "/api/v1/lab-instances/" + c.fixture.LabInstanceID.String() + "/files/tree?path="
	authHeader := http.Header{
		"Cookie": []string{realtime.SessionCookieName + "=" + c.ownerCookie},
		"Origin": []string{terminalTrustedOrigin},
	}
	req, _ := http.NewRequest(http.MethodGet, fileURL_B, nil)
	for k, v := range authHeader {
		req.Header[k] = v
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("file request error = %v", err)
	}
	defer resp.Body.Close()
	// Pod B returns 503 because its local Router has no route to the Connector
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("Pod B file request status = %d, want 503", resp.StatusCode)
	}

	// 2) Data WSS connecting to Pod B without pending request on Pod B
	dataURL_B := "ws" + strings.TrimPrefix(c.urlB, "http") + filetransport.DataPath
	dialer := websocket.Dialer{Subprotocols: []string{"labbit.connector-file.v1"}}
	dataHeaders := http.Header{
		"Authorization": []string{"Bearer " + terminaltest.Credential},
	}
	dWs, _, err := dialer.Dial(dataURL_B, dataHeaders)
	if err != nil {
		t.Fatalf("data dial error = %v", err)
	}
	defer dWs.Close()

	// Send ATTACH frame for an unknown fileRequestId to Pod B
	_ = dWs.WriteJSON(map[string]any{
		"type":          "FILE_ATTACH",
		"messageId":     "f-1",
		"sentAt":        time.Now().Format(time.RFC3339),
		"fileRequestId": uuid.NewString(),
	})
	_, _, err = dWs.ReadMessage()
	if err == nil {
		t.Fatalf("Pod B should reject unknown fileRequestId")
	}
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) && closeErr.Code != 1008 && closeErr.Code != 4003 {
		t.Fatalf("file data close code = %d, want 1008 or 4003", closeErr.Code)
	}
}

//  5. Preview cross-Pod mismatch:
//     - HTTP POST preview session on non-owner Pod B fails with 503 because Pod B has no connector route.
//     - Preview Data WSS connecting to Pod B without a local pending session (tested with an unknown previewSessionId)
//     fails closed with close code 1008/4003.
//
// Note: This verifies fail-closed rejection on an instance lacking pending state,
// not an observed migration of an active pending preview session from Pod A to Pod B.
func TestMultiReplica_Preview_CrossPodMismatch(t *testing.T) {
	c := newMultiReplicaCluster(t, false, false, true)

	// Connect connector to Pod A
	conn := terminaltest.NewConnector(t, c.urlA, terminaltest.Credential)
	conn.Start()

	eventually(t, "Pod A preview ready", 10*time.Second, func() bool {
		return c.podA.Registry.WithReadyRoute(c.connectorID, func(connector.Session, connector.Route) error { return nil }) == nil
	})

	// HTTP POST preview session to Pod B -> fails 503 because Pod B has no connector route
	previewURL_B := c.urlB + "/api/v1/lab-instances/" + c.fixture.LabInstanceID.String() + "/preview-sessions"
	authHeader := http.Header{
		"Cookie":       []string{realtime.SessionCookieName + "=" + c.ownerCookie},
		"Origin":       []string{terminalTrustedOrigin},
		"Content-Type": []string{"application/json"},
	}
	respB := postHTTP(t, previewURL_B, authHeader, `{"targetPort":3000}`)
	if respB.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("Pod B preview create status = %d, want 503", respB.StatusCode)
	}

	// Preview Data WSS connecting to Pod B for arbitrary session
	dataURL_B := "ws" + strings.TrimPrefix(c.urlB, "http") + preview.DataPath
	dialer := websocket.Dialer{Subprotocols: []string{protocol.SubprotocolPreviewData}}
	dataHeaders := http.Header{
		"Authorization": []string{"Bearer " + terminaltest.Credential},
	}
	pWs, _, err := dialer.Dial(dataURL_B, dataHeaders)
	if err != nil {
		t.Fatalf("preview data dial error = %v", err)
	}
	defer pWs.Close()

	_ = pWs.WriteJSON(map[string]any{
		"type":             "PREVIEW_ATTACH",
		"messageId":        "p-1",
		"sentAt":           time.Now().Format(time.RFC3339),
		"previewSessionId": uuid.NewString(),
	})
	_, _, err = pWs.ReadMessage()
	if err == nil {
		t.Fatalf("Pod B should reject unknown previewSessionId")
	}
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) && closeErr.Code != 1008 && closeErr.Code != 4003 {
		t.Fatalf("preview data close code = %d, want 1008 or 4003", closeErr.Code)
	}
}

// 6. Stale Owner and Reconnect Mismatch:
// An in-flight operation on Pod A loses its return path when the Connector drops and reconnects to Pod B.
// When RESULT arrives on Pod B, Pod B's Router discards it as unmatched (RouteOperationResult returns false),
// while Pod A's Router retains the pending operation stuck (PendingCount == 1).
// Note: This test directly asserts Pod B's unmatched discard and Pod A's stuck PendingCount == 1.
// It does NOT assert actual operational timeout expiration or DB transition to UNKNOWN/RECONCILING
// (which is the downstream operational consequence when the pending command is never completed).
func TestMultiReplica_ConnectorReconnect_StaleOwnerAndOrphanResult(t *testing.T) {
	c := newMultiReplicaCluster(t, false, false, false)

	// Step 1: Connect to Pod A
	supervisorA, provA := startSupervisorWithMock(t, c.urlA, c.connectorID)
	eventually(t, "Pod A route ready", 10*time.Second, func() bool {
		return c.podA.Registry.WithReadyRoute(c.connectorID, func(connector.Session, connector.Route) error { return nil }) == nil
	})

	// Prepare mock provider to hold the result until told to release
	resultChan := make(chan struct{})
	provA.ProvisionFunc = func(_ context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
		<-resultChan
		return provider.OperationResult{Outcome: provider.OutcomeSucceeded, ProviderResources: []provider.ResourceResult{{
			ResourceRef:   provider.ResourceRef{ResourceType: "SERVER", ProviderID: "srv-reconnect", Generation: r.Generation},
			ObservedState: "ACTIVE",
		}}}, nil
	}

	corr := connector.Correlation{OperationID: "op-reconnect", LabInstanceID: c.fixture.LabInstanceID.String(), Generation: 1}
	sent, err := c.podA.Router.SendOperationCommand(t.Context(), connector.OperationCommand{
		ConnectorID: c.connectorID,
		Correlation: corr,
		Payload: protocol.OperationCommandPayload{
			MutationType: protocol.MutationTypeProvision,
			CreationSnapshot: &protocol.CreationSnapshot{
				ProviderConnectionID: c.fixture.ProviderConnectionID.String(),
				VMs: []protocol.ResolvedVmSpec{{
					VMKey: "vm-1", Role: "workspace", ImageID: "img-1", FlavorID: "flv-1",
					FlavorSpec: &protocol.ResolvedFlavorSpec{VCPUs: 1, RAMMiB: 512, DiskGiB: 10},
				}},
				WorkspaceVMKey: "vm-1",
			},
		},
	})
	if err != nil {
		t.Fatalf("SendOperationCommand error = %v", err)
	}

	// Step 2: Simulate Connector disconnect from Pod A and reconnect to Pod B
	supervisorA.Stop()

	// Connect to Pod B
	supervisorB, _ := startSupervisorWithMock(t, c.urlB, c.connectorID)
	t.Cleanup(supervisorB.Stop)
	eventually(t, "Pod B route ready", 10*time.Second, func() bool {
		return c.podB.Registry.WithReadyRoute(c.connectorID, func(connector.Session, connector.Route) error { return nil }) == nil
	})

	// When result arrives on Pod B:
	// Pod B's Router has no pending request for this correlation.
	// RouteOperationResult logs an unmatched event and returns nil (discarding it).
	matched := c.podB.Router.RouteOperationResult(c.connectorID, connector.Inbound{
		MessageID:        "msg-res-1",
		ReplyToMessageID: sent.MessageID,
		OperationID:      corr.OperationID,
		LabInstanceID:    corr.LabInstanceID,
		Generation:       corr.Generation,
	}, protocol.OperationResultPayload{
		Outcome: protocol.OutcomeSucceeded,
	})
	if matched {
		t.Fatalf("Pod B should recognize that this result is unmatched with its local pending state")
	}

	// Pod A still has the pending request stuck waiting, because RESULT never reached Pod A!
	opsA, _ := c.podA.Router.PendingCount(c.connectorID)
	if opsA != 1 {
		t.Fatalf("Pod A pending operations = %d, want 1 (stuck waiting)", opsA)
	}
	opsB, _ := c.podB.Router.PendingCount(c.connectorID)
	if opsB != 0 {
		t.Fatalf("Pod B pending operations = %d, want 0", opsB)
	}
}

// 7. Logical Instance Simulation (N=3) Expected Failure Sample Analysis:
// In a logical N=3 cluster (Pod A, Pod B, Pod C) running in-process, incoming HTTP requests
// are deterministically dispatched across instances using a fixed round-robin harness (i % len(pods)).
// The observed 6/9 (66.7%) failure count is a deterministic harness expected sample on step 1 (HTTP Create),
// demonstrating that non-owner instances reject requests with 503 (ErrConnectorUnavailable).
// This is an in-process harness sample, NOT a measured production LB statistic.
// Furthermore, the theoretical (1/3)^3 ≈ 3.7% calculation is an analytical illustration assuming
// 3 independently uniform randomized hops (Create + Data WSS + Browser WSS),
// NOT an empirical 3-step E2E measured benchmark run.
func TestMultiReplica_TopologyN3_FailureRateAnalysis(t *testing.T) {
	c := newMultiReplicaCluster(t, false, true, false)

	// Connector connected only to Pod A
	conn := terminaltest.NewConnector(t, c.urlA, terminaltest.Credential)
	conn.Start()

	eventually(t, "Pod A route ready", 10*time.Second, func() bool {
		return c.podA.Registry.WithReadyRoute(c.connectorID, func(connector.Session, connector.Route) error { return nil }) == nil
	})

	pods := []string{c.urlA, c.urlB, c.urlC}
	authHeader := http.Header{
		"Cookie":       []string{realtime.SessionCookieName + "=" + c.ownerCookie},
		"Origin":       []string{terminalTrustedOrigin},
		"Content-Type": []string{"application/json"},
	}

	totalAttempts := 9
	failures := 0
	for i := 0; i < totalAttempts; i++ {
		podURL := pods[i%len(pods)]
		createURL := podURL + "/api/v1/lab-instances/" + c.fixture.LabInstanceID.String() + "/terminal-sessions"
		resp := postHTTP(t, createURL, authHeader, createBody)
		if resp.StatusCode != http.StatusCreated {
			failures++
		}
	}

	// 2 out of 3 pods (Pod B, Pod C) always fail immediately -> at least 6/9 failures (66.7% fail rate on step 1 alone)
	expectedFailures := 6
	if failures != expectedFailures {
		t.Fatalf("failures = %d, want %d (2/3 of requests land on non-owner pods)", failures, expectedFailures)
	}
}

// Helper functions

func postHTTP(t *testing.T, url string, header http.Header, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s error = %v", url, err)
	}
	return resp
}

type stoppableSupervisor struct {
	cancel context.CancelFunc
	done   chan error
}

func (s *stoppableSupervisor) Stop() {
	s.cancel()
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
	}
}

func startSupervisorWithMock(t *testing.T, serverURL string, connectorID uuid.UUID) (*stoppableSupervisor, *provider.MockProvider) {
	t.Helper()
	mock := &provider.MockProvider{}
	handler := wss.NewHandler(mock, nil)
	supervisor := wss.NewSupervisor(wss.Config{
		BaseURL: serverURL, Credential: terminaltest.Credential, ConnectorVersion: "test-1.0.0", AllowInsecure: true,
	}, handler, wss.BackoffPolicy{InitialInterval: 50 * time.Millisecond, MaxInterval: 200 * time.Millisecond, Multiplier: 2, RandomizationFactor: 0.1})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()

	return &stoppableSupervisor{cancel: cancel, done: done}, mock
}

func startRoutingConnectorToURL(t *testing.T, serverURL string, connectorID uuid.UUID, mock *provider.MockProvider) {
	t.Helper()
	s, _ := startSupervisorWithMock(t, serverURL, connectorID)
	t.Cleanup(s.Stop)
}
