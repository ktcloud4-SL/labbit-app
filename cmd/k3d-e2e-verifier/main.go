package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/valkey-io/valkey-go"

	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
)

type syntheticConnector struct {
	base      string
	cred      string
	ctrl      *websocket.Conn
	ctrlWrite sync.Mutex
	dataWs    *websocket.Conn
	closed    chan struct{}
}

func startSyntheticConnector(base, cred string) (*syntheticConnector, error) {
	wsBase := "ws" + strings.TrimPrefix(base, "http")
	dialer := websocket.Dialer{
		Subprotocols:     []string{"labbit.connector.v1"},
		HandshakeTimeout: 5 * time.Second,
	}
	headers := http.Header{"Authorization": []string{"Bearer " + cred}}
	ws, _, err := dialer.Dial(wsBase+"/connector/v1/control", headers)
	if err != nil {
		return nil, fmt.Errorf("control dial failed: %w", err)
	}

	sc := &syntheticConnector{
		base:   wsBase,
		cred:   cred,
		ctrl:   ws,
		closed: make(chan struct{}),
	}

	// 1. HELLO
	nowStr := time.Now().UTC().Format(time.RFC3339Nano)
	hello := map[string]any{
		"type":      "HELLO",
		"messageId": uuid.NewString(),
		"sentAt":    nowStr,
		"payload": map[string]any{
			"connectorVersion": "k3d-spike-1.0.0",
			"runtimeId":        "k3d-spike-runtime",
			"startedAt":        nowStr,
		},
	}
	sc.writeControl(hello)

	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var ack map[string]any
	if err := ws.ReadJSON(&ack); err != nil || ack["type"] != "HELLO_ACK" {
		ws.Close()
		return nil, fmt.Errorf("expected HELLO_ACK, got %v, err=%v", ack, err)
	}
	_ = ws.SetReadDeadline(time.Time{})

	// Background heartbeat
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-sc.closed:
				return
			case <-ticker.C:
				nowStr := time.Now().UTC().Format(time.RFC3339Nano)
				sc.writeControl(map[string]any{
					"type":      "HEARTBEAT",
					"messageId": uuid.NewString(),
					"sentAt":    nowStr,
					"payload":   map[string]any{"observedAt": nowStr},
				})
			}
		}
	}()

	go sc.controlLoop()
	return sc, nil
}

func (sc *syntheticConnector) controlLoop() {
	defer close(sc.closed)
	for {
		_, data, err := sc.ctrl.ReadMessage()
		if err != nil {
			return
		}
		var msg struct {
			Type              string          `json:"type"`
			MessageID         string          `json:"messageId"`
			RequestID         string          `json:"requestId"`
			TerminalSessionID string          `json:"terminalSessionId"`
			LabInstanceID     string          `json:"labInstanceId"`
			Generation        int64           `json:"generation"`
			Payload           json.RawMessage `json:"payload"`
		}
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		switch msg.Type {
		case "TERMINAL_OPEN":
			dialer := websocket.Dialer{
				Subprotocols:     []string{"labbit.connector-terminal.v1"},
				HandshakeTimeout: 5 * time.Second,
			}
			dataWs, _, err := dialer.Dial(sc.base+"/connector/v1/terminal-data", http.Header{"Authorization": []string{"Bearer " + sc.cred}})
			if err != nil {
				log.Printf("Terminal Data dial failed: %v", err)
				nowStr := time.Now().UTC().Format(time.RFC3339Nano)
				sc.writeControl(map[string]any{
					"type":              "TERMINAL_OPEN_RESULT",
					"messageId":         uuid.NewString(),
					"replyToMessageId":  msg.MessageID,
					"sentAt":            nowStr,
					"terminalSessionId": msg.TerminalSessionID,
					"labInstanceId":     msg.LabInstanceID,
					"generation":        msg.Generation,
					"payload":           map[string]any{"outcome": "FAILED", "error": map[string]any{"code": "ATTACH_FAILED"}},
				})
				continue
			}
			sc.dataWs = dataWs

			nowStr := time.Now().UTC().Format(time.RFC3339Nano)
			// Send TERMINAL_DATA_ATTACH
			_ = dataWs.WriteJSON(map[string]any{
				"type":              "TERMINAL_DATA_ATTACH",
				"messageId":         uuid.NewString(),
				"sentAt":            nowStr,
				"terminalSessionId": msg.TerminalSessionID,
				"labInstanceId":     msg.LabInstanceID,
				"generation":        msg.Generation,
				"payload":           map[string]any{"runtimeId": "k3d-spike-runtime"},
			})

			var attached map[string]any
			_ = dataWs.SetReadDeadline(time.Now().Add(5 * time.Second))
			_ = dataWs.ReadJSON(&attached)
			_ = dataWs.SetReadDeadline(time.Time{})

			// Reply TERMINAL_OPEN_RESULT SUCCEEDED
			nowStr = time.Now().UTC().Format(time.RFC3339Nano)
			sc.writeControl(map[string]any{
				"type":              "TERMINAL_OPEN_RESULT",
				"messageId":         uuid.NewString(),
				"replyToMessageId":  msg.MessageID,
				"sentAt":            nowStr,
				"terminalSessionId": msg.TerminalSessionID,
				"labInstanceId":     msg.LabInstanceID,
				"generation":        msg.Generation,
				"payload":           map[string]any{"outcome": "SUCCEEDED"},
			})

			// Start echo loop on data connection
			go func(dws *websocket.Conn) {
				defer dws.Close()
				for {
					mType, mData, err := dws.ReadMessage()
					if err != nil {
						return
					}
					if mType == websocket.BinaryMessage {
						_ = dws.WriteMessage(websocket.BinaryMessage, mData)
					}
				}
			}(dataWs)

		case "TERMINAL_CLOSE":
			if sc.dataWs != nil {
				_ = sc.dataWs.Close()
			}
			nowStr := time.Now().UTC().Format(time.RFC3339Nano)
			sc.writeControl(map[string]any{
				"type":              "TERMINAL_ENDED",
				"messageId":         uuid.NewString(),
				"sentAt":            nowStr,
				"terminalSessionId": msg.TerminalSessionID,
				"labInstanceId":     msg.LabInstanceID,
				"generation":        msg.Generation,
				"payload":           map[string]any{"reason": "SESSION_CLOSED"},
			})
		}
	}
}

func (sc *syntheticConnector) writeControl(v any) {
	sc.ctrlWrite.Lock()
	defer sc.ctrlWrite.Unlock()
	_ = sc.ctrl.WriteJSON(v)
}

func (sc *syntheticConnector) Close() {
	if sc.ctrl != nil {
		_ = sc.ctrl.Close()
	}
	if sc.dataWs != nil {
		_ = sc.dataWs.Close()
	}
}

func main() {
	log.Println("=== Starting LBT-198 k3d Multi-Pod E2E Proof ===")

	pgDSN := os.Getenv("PG_DSN")
	if pgDSN == "" {
		pgDSN = "postgres://labbit:labbit-local-dummy@postgres.lbt198-valkey-poc.svc.cluster.local:5432/postgres?sslmode=disable"
	}
	valkeyAddr := os.Getenv("VALKEY_ADDR")
	if valkeyAddr == "" {
		valkeyAddr = "valkey.lbt198-valkey-poc.svc.cluster.local:6379"
	}
	url0 := os.Getenv("BACKEND_0_URL")
	if url0 == "" {
		url0 = "http://127.0.0.1:8080"
	}
	url1 := os.Getenv("BACKEND_1_URL")
	if url1 == "" {
		url1 = "http://backend-1.backend-headless.lbt198-valkey-poc.svc.cluster.local:8080"
	}

	ctx := context.Background()

	// 1. Connect to PostgreSQL and Valkey
	log.Println("[1/8] Connecting to PostgreSQL...")
	conn, err := pgx.Connect(ctx, pgDSN)
	if err != nil {
		log.Fatalf("FATAL: PostgreSQL connection failed (credentials suppressed)")
	}
	defer conn.Close(ctx)

	log.Printf("[1/8] Connecting to Valkey (%s)...", valkeyAddr)
	vkClient, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{valkeyAddr}, DisableCache: true})
	if err != nil {
		log.Fatalf("FATAL: Valkey connection failed: %v", err)
	}
	defer vkClient.Close()

	if err := vkClient.Do(ctx, vkClient.B().Ping().Build()).Error(); err != nil {
		log.Fatalf("FATAL: Valkey PING failed: %v", err)
	}
	log.Println("[1/8] Valkey PING: PONG (verified)")

	// 2. Seed database fixture (Organization, User, Session, Class, LabInstance, ConnectorCredential)
	log.Println("[2/8] Seeding DB fixture for E2E verification...")
	orgID := uuid.New()
	userID := uuid.New()
	rawToken := make([]byte, 32)
	_, _ = rand.Read(rawToken)
	tokenHash := sha256.Sum256(rawToken)
	sessionToken := base64.RawURLEncoding.EncodeToString(rawToken)

	_, err = conn.Exec(ctx, `
		INSERT INTO organizations (id, name) VALUES ($1, 'k3d-test-org')
	`, orgID)
	if err != nil {
		log.Fatalf("insert org failed: %v", err)
	}

	_, err = conn.Exec(ctx, `
		INSERT INTO users (id, organization_id, organization_role)
		VALUES ($1, $2, 'MEMBER')
	`, userID, orgID)
	if err != nil {
		log.Fatalf("insert user failed: %v", err)
	}

	now := time.Now()
	_, err = conn.Exec(ctx, `
		INSERT INTO auth_sessions (id, user_id, token_hash, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5)
	`, uuid.New(), userID, tokenHash[:], now, now.Add(8*time.Hour))
	if err != nil {
		log.Fatalf("insert auth_sessions failed: %v", err)
	}

	classID := uuid.New()
	_, err = conn.Exec(ctx, `
		INSERT INTO classes (id, organization_id, name) VALUES ($1, $2, 'k3d-class')
	`, classID, orgID)
	if err != nil {
		log.Fatalf("insert class failed: %v", err)
	}

	_, err = conn.Exec(ctx, `
		INSERT INTO class_memberships (organization_id, class_id, user_id, role)
		VALUES ($1, $2, $3, 'STUDENT')
	`, orgID, classID, userID)
	if err != nil {
		log.Fatalf("insert class_membership failed: %v", err)
	}

	connectorID := uuid.New()
	credSecret := fmt.Sprintf("e2e-cred-%s", uuid.NewString())
	credHash := sha256.Sum256([]byte(credSecret))
	_, err = conn.Exec(ctx, `
		INSERT INTO connectors (id, organization_id, name) VALUES ($1, $2, 'k3d-connector')
	`, connectorID, orgID)
	if err != nil {
		log.Fatalf("insert connectors failed: %v", err)
	}

	_, err = conn.Exec(ctx, `
		INSERT INTO connector_credentials (id, connector_id, credential_hash)
		VALUES ($1, $2, $3)
	`, uuid.New(), connectorID, credHash[:])
	if err != nil {
		log.Fatalf("insert connector_credentials failed: %v", err)
	}

	labSpecID := uuid.New()
	_, err = conn.Exec(ctx, `
		INSERT INTO lab_specs (id, organization_id, owner_user_id, name, revision, internet_outbound, workspace_role, workspace_instance_index)
		VALUES ($1, $2, $3, 'k3d-spec', 1, false, 'workspace', 0)
	`, labSpecID, orgID, userID)
	if err != nil {
		log.Fatalf("insert lab_specs failed: %v", err)
	}

	execID := uuid.New()
	_, err = conn.Exec(ctx, `
		INSERT INTO lab_executions (id, organization_id, class_id, lab_spec_id, instructor_user_id, status)
		VALUES ($1, $2, $3, $4, $5, 'ACTIVE')
	`, execID, orgID, classID, labSpecID, userID)
	if err != nil {
		log.Fatalf("insert lab_executions failed: %v", err)
	}

	providerConnID := uuid.New()
	_, err = conn.Exec(ctx, `
		INSERT INTO provider_connections (id, organization_id, connector_id, name, status)
		VALUES ($1, $2, $3, 'k3d-conn', 'ACTIVE')
	`, providerConnID, orgID, connectorID)
	if err != nil {
		log.Fatalf("insert provider_connections failed: %v", err)
	}

	snapshotJSON, _ := json.Marshal(map[string]any{
		"providerConnectionId": providerConnID.String(),
		"vms": []map[string]any{{
			"vmKey": "workspace", "role": "workspace", "instanceIndex": 0,
			"imageId": "img-1", "flavorId": "flv-1",
			"flavorSpec": map[string]any{"vcpus": 1, "ramMiB": 512, "diskGiB": 10},
		}},
		"workspaceVmKey": "workspace",
	})
	_, err = conn.Exec(ctx, `
		INSERT INTO creation_snapshots (id, organization_id, lab_execution_id, source_lab_spec_id, source_lab_spec_revision, provider_connection_id, snapshot)
		VALUES ($1, $2, $3, $4, 1, $5, $6::jsonb)
	`, uuid.New(), orgID, execID, labSpecID, providerConnID, snapshotJSON)
	if err != nil {
		log.Fatalf("insert creation_snapshots failed: %v", err)
	}

	labInstanceID := uuid.New()
	_, err = conn.Exec(ctx, `
		INSERT INTO lab_instances (id, organization_id, lab_execution_id, user_id, participant_role, status, generation)
		VALUES ($1, $2, $3, $4, 'STUDENT', 'READY', 1)
	`, labInstanceID, orgID, execID, userID)
	if err != nil {
		log.Fatalf("insert lab_instances failed: %v", err)
	}

	serverID := uuid.New()
	_, err = conn.Exec(ctx, `
		INSERT INTO provider_resources (id, organization_id, lab_instance_id, provider_connection_id, generation, resource_type, logical_name, provider_id, lifecycle_status)
		VALUES ($1, $2, $3, $4, 1, 'SERVER', 'workspace', 'vm-srv-1', 'PRESENT')
	`, serverID, orgID, labInstanceID, providerConnID)
	if err != nil {
		log.Fatalf("insert provider_resources failed: %v", err)
	}
	log.Println("[2/8] DB fixture seeded successfully.")

	// 3. Connect Synthetic Mock Connector to backend-0
	log.Printf("[3/8] Connecting Mock Connector to backend-0 (%s)...", url0)
	var capturedSessionID string

	sc, err := startSyntheticConnector(url0, credSecret)
	if err != nil {
		log.Fatalf("FATAL: Failed to start synthetic connector: %v", err)
	}
	defer sc.Close()

	// 4. Verify Valkey owner mapping for Connector
	log.Println("[4/8] Checking Valkey owner mapping for ConnectorID...")
	key := fmt.Sprintf("labbit:valkey:connector:owner:%s", connectorID.String())
	var ownerJSON string
	for i := 0; i < 20; i++ {
		cmd := vkClient.B().Get().Key(key).Build()
		res, err := vkClient.Do(ctx, cmd).ToString()
		if err == nil && res != "" {
			ownerJSON = res
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if ownerJSON == "" {
		log.Fatalf("FATAL: Connector owner not registered in Valkey within 10s!")
	}
	log.Printf("[4/8] Valkey record found: %s", ownerJSON)
	var ownerRec struct {
		PodID   string `json:"pod_id"`
		PodAddr string `json:"pod_addr"`
		Epoch   int64  `json:"epoch"`
	}
	_ = json.Unmarshal([]byte(ownerJSON), &ownerRec)
	if ownerRec.PodID != "backend-0" {
		log.Fatalf("FATAL: Expected owner pod_id to be backend-0, got %s", ownerRec.PodID)
	}
	log.Printf("[4/8] VERIFIED: Connector owned by %s at %s with epoch %d", ownerRec.PodID, ownerRec.PodAddr, ownerRec.Epoch)

	// 5. Test Cross-Pod HTTP Create: request lands on backend-1, proxied to backend-0!
	log.Printf("[5/8] Sending CreateTerminalSession HTTP POST to backend-1 (%s)...", url1)
	createURL := fmt.Sprintf("%s/api/v1/lab-instances/%s/terminal-sessions", url1, labInstanceID.String())
	bodyJSON := `{"targetVmKey":"workspace","cols":80,"rows":24}`
	req, err := http.NewRequest(http.MethodPost, createURL, bytes.NewBufferString(bodyJSON))
	if err != nil {
		log.Fatalf("create request error: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", fmt.Sprintf("%s=%s", realtime.SessionCookieName, sessionToken))
	req.Header.Set("Origin", "http://localhost:8080")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("FATAL: HTTP POST to backend-1 failed: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	log.Printf("[5/8] HTTP response from backend-1: status=%d", resp.StatusCode)
	if resp.StatusCode != http.StatusCreated {
		log.Fatalf("FATAL: Expected 201 Created from cross-pod proxy, got %d", resp.StatusCode)
	}

	var created struct {
		ID           string `json:"id"`
		SessionToken string `json:"sessionToken"`
	}
	if err := json.Unmarshal(respBody, &created); err != nil {
		log.Fatalf("unmarshal created response failed (body suppressed)")
	}
	capturedSessionID = created.ID
	log.Printf("[5/8] Terminal session successfully created cross-pod: ID=%s", capturedSessionID)

	// Verify Valkey terminal owner key
	termKey := fmt.Sprintf("labbit:valkey:terminal:owner:%s", capturedSessionID)
	termOwnerJSON, err := vkClient.Do(ctx, vkClient.B().Get().Key(termKey).Build()).ToString()
	if err != nil {
		log.Fatalf("FATAL: Terminal owner key not found in Valkey: %v", err)
	}
	log.Printf("[5/8] Valkey terminal owner record: %s", termOwnerJSON)

	// 6. Test Cross-Pod WebSocket: Browser connects to backend-1 (proxied to backend-0)!
	log.Printf("[6/8] Connecting Browser WebSocket to backend-1 (%s%s)...", url1, realtime.BrowserPath)
	browserWSURL := strings.Replace(url1, "http://", "ws://", 1) + realtime.BrowserPath
	dialer := websocket.Dialer{
		Subprotocols:     []string{realtime.BrowserSubprotocol},
		HandshakeTimeout: 5 * time.Second,
	}
	wsHeaders := http.Header{
		"Cookie": []string{fmt.Sprintf("%s=%s", realtime.SessionCookieName, sessionToken)},
		"Origin": []string{"http://localhost:8080"},
	}
	bWs, bResp, err := dialer.Dial(browserWSURL, wsHeaders)
	if err != nil {
		var status int
		if bResp != nil {
			status = bResp.StatusCode
		}
		log.Fatalf("FATAL: Browser dial to backend-1 failed: %v (status=%d)", err, status)
	}
	defer bWs.Close()
	log.Println("[6/8] Browser WebSocket connected to backend-1!")

	// Send TERMINAL_ATTACH frame
	attachFrame := map[string]any{
		"type":              "TERMINAL_ATTACH",
		"messageId":         uuid.NewString(),
		"sentAt":            time.Now().Format(time.RFC3339),
		"terminalSessionId": capturedSessionID,
		"payload":           map[string]any{"sessionToken": created.SessionToken, "cols": 80, "rows": 24},
	}
	if err := bWs.WriteJSON(attachFrame); err != nil {
		log.Fatalf("FATAL: Browser WriteJSON failed: %v", err)
	}

	// Read response frame
	_ = bWs.SetReadDeadline(time.Now().Add(5 * time.Second))
	msgType, msgData, err := bWs.ReadMessage()
	if err != nil {
		log.Fatalf("FATAL: Browser read frame failed: %v", err)
	}
	log.Printf("[6/8] Browser received frame (type=%d, bytes=%d)", msgType, len(msgData))
	if !strings.Contains(string(msgData), "TERMINAL_ATTACHED") {
		log.Fatalf("FATAL: Expected TERMINAL_ATTACHED frame, got message type %d", msgType)
	}
	log.Println("[6/8] VERIFIED: Browser WSS cross-pod attachment SUCCEEDED!")

	// Test Bidirectional PTY Data Streaming: Browser writes binary data -> proxied to backend-0 -> sent to connector -> echoed back -> proxied to backend-1 -> received by Browser
	testPayload := []byte("k3d-cross-pod-pty-echo-test\r\n")
	if err := bWs.WriteMessage(websocket.BinaryMessage, testPayload); err != nil {
		log.Fatalf("FATAL: Browser failed to write binary PTY frame: %v", err)
	}
	_ = bWs.SetReadDeadline(time.Now().Add(5 * time.Second))
	ptyType, ptyData, err := bWs.ReadMessage()
	if err != nil {
		log.Fatalf("FATAL: Browser failed to read echoed binary PTY frame: %v", err)
	}
	if ptyType != websocket.BinaryMessage || !bytes.Equal(ptyData, testPayload) {
		log.Fatalf("FATAL: Expected echoed binary data %q, got type=%d data=%q", string(testPayload), ptyType, string(ptyData))
	}
	log.Printf("[6/8] VERIFIED: Bidirectional cross-pod PTY streaming confirmed: %q", string(ptyData))

	// 7. Delete TerminalSession via backend-1 (proxied to backend-0)
	log.Printf("[7/8] Closing TerminalSession via DELETE to backend-1...")
	delURL := fmt.Sprintf("%s/api/v1/terminal-sessions/%s", url1, capturedSessionID)
	delReq, _ := http.NewRequest(http.MethodDelete, delURL, nil)
	delReq.Header.Set("Cookie", fmt.Sprintf("%s=%s", realtime.SessionCookieName, sessionToken))
	delReq.Header.Set("Origin", "http://localhost:8080")
	delResp, err := client.Do(delReq)
	if err != nil || delResp.StatusCode != http.StatusNoContent {
		log.Fatalf("FATAL: DELETE failed: %v, status=%d", err, delResp.StatusCode)
	}
	log.Println("[7/8] VERIFIED: DELETE returned 204 No Content!")

	// Check Valkey terminal key deleted
	time.Sleep(500 * time.Millisecond)
	delCheck, _ := vkClient.Do(ctx, vkClient.B().Get().Key(termKey).Build()).ToString()
	if delCheck != "" {
		log.Fatalf("FATAL: Terminal key still present in Valkey after DELETE: %s", delCheck)
	}
	log.Println("[7/8] VERIFIED: Terminal owner key deleted from Valkey!")

	// 8. Negative Safety: Valkey owner loss fail-closed test
	log.Println("[8/8] Testing Negative Safety: Disconnecting Connector...")
	sc.Close()
	time.Sleep(1 * time.Second)

	// HTTP POST Create when no connector is owned -> must fail-closed with 503
	negReq, err := http.NewRequest(http.MethodPost, createURL, bytes.NewBufferString(bodyJSON))
	if err != nil {
		log.Fatalf("FATAL: Failed to build negative test request: %v", err)
	}
	negReq.Header.Set("Content-Type", "application/json")
	negReq.Header.Set("Cookie", fmt.Sprintf("%s=%s", realtime.SessionCookieName, sessionToken))
	negReq.Header.Set("Origin", "http://localhost:8080")

	negResp, err := client.Do(negReq)
	if err != nil {
		log.Fatalf("FATAL: Negative safety request to backend-1 failed: %v", err)
	}
	defer negResp.Body.Close()
	log.Printf("[8/8] Fail-closed response status: %d (expected 503)", negResp.StatusCode)
	if negResp.StatusCode != http.StatusServiceUnavailable {
		log.Fatalf("FATAL: Expected 503 Service Unavailable when no connector owner exists, got %d", negResp.StatusCode)
	}
	log.Println("[8/8] VERIFIED: When no owner exists, request fails-closed with 503!")

	log.Println("=========================================================")
	log.Println("PARTIAL POC VERIFIED: Synthetic Terminal Create/WSS-PTY/Delete & Owner Fail-Closed 503")
	log.Println("NOTE: Worker async execution, File/Preview routes, and Live streams are NOT verified in this test")
	log.Println("=========================================================")
}
