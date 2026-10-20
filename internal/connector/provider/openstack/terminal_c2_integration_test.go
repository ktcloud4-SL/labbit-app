package openstackprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/terminal"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
	serverconnector "github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connectorwss"
	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
)

// c2Session represents the server-side state of a terminal session in the C2 integration test.
type c2Session struct {
	terminalSessionID string
	token             string
	labInstanceID     string
	generation        int64
	attached          bool
	detached          bool
	ended             bool
	endReason         string
}

// c2Auth implements the minimal required interfaces to drive SaaS Control and Relay:
// - serverconnector.Authenticator
// - serverconnector.HeartbeatRecorder
// - realtime.ConnectorAuthenticator
// - realtime.Control
type c2Auth struct {
	mu             sync.Mutex
	connectorID    uuid.UUID
	orgID          uuid.UUID
	credentialID   uuid.UUID
	credential     string
	browserSession string
	sessions       map[string]*c2Session
	router         *serverconnector.Router
}

func newC2Auth(connectorID uuid.UUID, credential, browserSession string) *c2Auth {
	return &c2Auth{
		connectorID:    connectorID,
		orgID:          uuid.New(),
		credentialID:   uuid.New(),
		credential:     credential,
		browserSession: browserSession,
		sessions:       make(map[string]*c2Session),
	}
}

func (a *c2Auth) setRouter(r *serverconnector.Router) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.router = r
}

func (a *c2Auth) registerSession(id, token, labID string, gen int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessions[id] = &c2Session{
		terminalSessionID: id,
		token:             token,
		labInstanceID:     labID,
		generation:        gen,
	}
}

// serverconnector.Authenticator
func (a *c2Auth) Authenticate(_ context.Context, credential serverconnector.Credential) (serverconnector.Principal, error) {
	if string(credential) != a.credential {
		return serverconnector.Principal{}, serverconnector.ErrUnauthenticated
	}
	return serverconnector.Principal{
		ConnectorID:    a.connectorID,
		OrganizationID: a.orgID,
		CredentialID:   a.credentialID,
	}, nil
}

// serverconnector.HeartbeatRecorder
func (a *c2Auth) RecordHeartbeat(context.Context, serverconnector.Principal, time.Time) error {
	return nil
}

// realtime.ConnectorAuthenticator
func (a *c2Auth) AuthenticateConnector(_ context.Context, credential realtime.ConnectorCredential) (realtime.ConnectorIdentity, error) {
	if string(credential) != a.credential {
		return realtime.ConnectorIdentity{}, realtime.ErrUnauthenticated
	}
	return realtime.ConnectorIdentity{
		ConnectorID:  a.connectorID.String(),
		CredentialID: a.credentialID.String(),
	}, nil
}

// realtime.Control implementation
func (a *c2Auth) AuthenticateBrowser(_ context.Context, session realtime.SessionToken) error {
	if string(session) != a.browserSession {
		return realtime.ErrUnauthenticated
	}
	return nil
}

func (a *c2Auth) AuthorizeAttach(_ context.Context, session realtime.SessionToken, req realtime.AttachRequest) (realtime.AttachGrant, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if string(session) != a.browserSession {
		return realtime.AttachGrant{}, realtime.ErrUnauthenticated
	}
	s, ok := a.sessions[req.TerminalSessionID]
	if !ok {
		return realtime.AttachGrant{}, realtime.ErrSessionNotFound
	}
	if s.ended {
		return realtime.AttachGrant{}, realtime.ErrSessionEnded
	}
	if string(req.Token) != s.token {
		return realtime.AttachGrant{}, realtime.ErrInvalidToken
	}
	return realtime.AttachGrant{
		TerminalSessionID: s.terminalSessionID,
		LabInstanceID:     s.labInstanceID,
		Generation:        s.generation,
	}, nil
}

func (a *c2Auth) RecordAttached(_ context.Context, terminalSessionID string, _ time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s, ok := a.sessions[terminalSessionID]; ok {
		s.attached = true
		s.detached = false
	}
	return nil
}

func (a *c2Auth) RecordDetached(_ context.Context, terminalSessionID string, _, _ time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s, ok := a.sessions[terminalSessionID]; ok {
		s.attached = false
		s.detached = true
	}
	return nil
}

func (a *c2Auth) isDetached(terminalSessionID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s, ok := a.sessions[terminalSessionID]; ok {
		return s.detached
	}
	return false
}

func (a *c2Auth) CloseSession(ctx context.Context, terminalSessionID, reason string) error {
	a.mu.Lock()
	s, ok := a.sessions[terminalSessionID]
	if !ok {
		a.mu.Unlock()
		return nil
	}
	if s.ended {
		a.mu.Unlock()
		return nil
	}
	s.ended = true
	s.endReason = reason
	router := a.router
	connID := a.connectorID
	labID := s.labInstanceID
	gen := s.generation
	a.mu.Unlock()

	if router != nil {
		_, _ = router.SendTerminalClose(ctx, serverconnector.TerminalClose{
			ConnectorID: connID,
			Correlation: serverconnector.TerminalCorrelation{
				TerminalSessionID: terminalSessionID,
				LabInstanceID:     labID,
				Generation:        gen,
			},
			Reason: reason,
		})
	}
	return nil
}

func (a *c2Auth) SessionEnded(_ context.Context, terminalSessionID string, end realtime.End) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s, ok := a.sessions[terminalSessionID]; ok {
		s.ended = true
		s.endReason = end.Reason
	}
	return nil
}

// c2TerminalSink collects terminal lifecycle events forwarded by Router.
type c2TerminalSink struct {
	mu     sync.Mutex
	events []serverconnector.TerminalEvent
	notify chan struct{}
}

func newC2TerminalSink() *c2TerminalSink {
	return &c2TerminalSink{notify: make(chan struct{}, 100)}
}

func (s *c2TerminalSink) HandleTerminalEvent(e serverconnector.TerminalEvent) {
	s.mu.Lock()
	s.events = append(s.events, e)
	s.mu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *c2TerminalSink) waitForOpenResult(t *testing.T, sessionID string, timeout time.Duration) serverconnector.TerminalOpenResultEvent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		for _, ev := range s.events {
			if res, ok := ev.(serverconnector.TerminalOpenResultEvent); ok && res.Correlation.TerminalSessionID == sessionID {
				s.mu.Unlock()
				return res
			}
		}
		s.mu.Unlock()
		select {
		case <-s.notify:
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatalf("timed out waiting for TerminalOpenResultEvent for session %s", sessionID)
	return serverconnector.TerminalOpenResultEvent{}
}

func (s *c2TerminalSink) waitForEnded(t *testing.T, sessionID string, timeout time.Duration) serverconnector.TerminalEndedEvent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		for _, ev := range s.events {
			if ended, ok := ev.(serverconnector.TerminalEndedEvent); ok && ended.Correlation.TerminalSessionID == sessionID {
				s.mu.Unlock()
				return ended
			}
		}
		s.mu.Unlock()
		select {
		case <-s.notify:
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatalf("timed out waiting for TerminalEndedEvent for session %s", sessionID)
	return serverconnector.TerminalEndedEvent{}
}

type c2Received struct {
	kind int
	data []byte
}

// c2BrowserPeer encapsulates a simulated browser WebSocket connection to /realtime/v1/terminal.
type c2BrowserPeer struct {
	t       *testing.T
	conn    *websocket.Conn
	start   sync.Once
	msgs    chan c2Received
	readErr error
}

func newC2BrowserPeer(t *testing.T, conn *websocket.Conn) *c2BrowserPeer {
	p := &c2BrowserPeer{
		t:    t,
		conn: conn,
		msgs: make(chan c2Received, 4096),
	}
	t.Cleanup(func() { _ = p.conn.Close() })
	return p
}

func (p *c2BrowserPeer) readLoop() {
	defer close(p.msgs)
	for {
		kind, data, err := p.conn.ReadMessage()
		if err != nil {
			p.readErr = err
			return
		}
		p.msgs <- c2Received{kind: kind, data: data}
	}
}

func (p *c2BrowserPeer) next(timeout time.Duration) (c2Received, error) {
	p.start.Do(func() { go p.readLoop() })
	select {
	case m, ok := <-p.msgs:
		if !ok {
			return c2Received{}, p.readErr
		}
		return m, nil
	case <-time.After(timeout):
		return c2Received{}, errors.New("read timeout")
	}
}

func (p *c2BrowserPeer) writeJSON(v any) {
	p.t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		p.t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := p.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		p.t.Fatalf("WriteMessage(text) error = %v", err)
	}
}

func (p *c2BrowserPeer) writeBinary(data []byte) {
	p.t.Helper()
	if err := p.conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
		p.t.Fatalf("WriteMessage(binary) error = %v", err)
	}
}

func (p *c2BrowserPeer) readJSON(timeout time.Duration) map[string]any {
	p.t.Helper()
	m, err := p.next(timeout)
	if err != nil {
		p.t.Fatalf("readJSON failed: %v", err)
	}
	if m.kind != websocket.TextMessage {
		p.t.Fatalf("expected text message, got kind %d", m.kind)
	}
	var res map[string]any
	if err := json.Unmarshal(m.data, &res); err != nil {
		p.t.Fatalf("json.Unmarshal failed: %v", err)
	}
	return res
}

func (p *c2BrowserPeer) readBinaryUntil(marker string, timeout time.Duration) string {
	p.t.Helper()
	deadline := time.Now().Add(timeout)
	var accumulated []byte
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		msg, err := p.next(remaining)
		if err != nil {
			break
		}
		if msg.kind == websocket.BinaryMessage {
			accumulated = append(accumulated, msg.data...)
			if strings.Contains(string(accumulated), marker) {
				return string(accumulated)
			}
		}
	}
	p.t.Fatalf("timed out waiting for binary marker %q (accumulated_len=%d)", marker, len(accumulated))
	return ""
}

func (p *c2BrowserPeer) close() {
	_ = p.conn.Close()
}

// c2ResizeAwarePTY wraps MockPTY with a channel to notify resize events deterministically without sleep or data race.
type c2ResizeAwarePTY struct {
	*terminal.MockPTY
	resizeCh chan [2]int
}

func newC2ResizeAwarePTY(cols, rows int) *c2ResizeAwarePTY {
	return &c2ResizeAwarePTY{
		MockPTY:  terminal.NewMockEchoPTY(cols, rows),
		resizeCh: make(chan [2]int, 10),
	}
}

func (p *c2ResizeAwarePTY) Resize(cols, rows int) error {
	if err := p.MockPTY.Resize(cols, rows); err != nil {
		return err
	}
	select {
	case p.resizeCh <- [2]int{cols, rows}:
	default:
	}
	return nil
}

// TestTerminalC2_FullStackLoopback verifies the complete C2 full-stack round trip
// (Browser WSS ↔ SaaS Relay ↔ Connector Transport ↔ Echo PTY) in an automated loopback test
// without requiring real cloud credentials.
func waitC2ProtocolReady(t *testing.T, router *serverconnector.Router, connectorID uuid.UUID) {
	t.Helper()
	// Receiving HELLO_ACK does not mean the server has installed its route yet.
	// Wait for that fixture precondition without sending or retrying a command.
	readyDeadline := time.Now().Add(2 * time.Second)
	for {
		err := router.Registry().WithReadyRoute(connectorID, func(serverconnector.Session, serverconnector.Route) error { return nil })
		if err == nil {
			return
		}
		if !errors.Is(err, serverconnector.ErrNotReady) || time.Now().After(readyDeadline) {
			t.Fatalf("Connector protocol-ready route unavailable: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestTerminalC2_FullStackLoopback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const (
		c2Credential     = "c2-loopback-secret-credential"
		c2BrowserCookie  = "c2-loopback-browser-cookie"
		trustedWebOrigin = "http://localhost:5173"
		labID            = "lbt22-loopback-inst"
	)

	connectorID := uuid.New()
	auth := newC2Auth(connectorID, c2Credential, c2BrowserCookie)
	registry := serverconnector.NewRegistry()
	sink := newC2TerminalSink()

	router, err := serverconnector.NewRouter(serverconnector.RouterOptions{
		Registry:     registry,
		TerminalSink: sink,
	})
	if err != nil {
		t.Fatalf("NewRouter failed: %v", err)
	}
	auth.setRouter(router)

	controlHandler, err := connectorwss.New(connectorwss.Options{
		Auth:              auth,
		Heartbeats:        auth,
		Registry:          registry,
		Router:            router,
		OfflineTimeout:    10 * time.Minute,
		HeartbeatInterval: 15 * time.Second,
	})
	if err != nil {
		t.Fatalf("connectorwss.New failed: %v", err)
	}
	defer controlHandler.Close()

	relay, err := realtime.New(realtime.Options{
		Control:       auth,
		Connectors:    auth,
		AllowOrigin:   func(origin string) bool { return origin == trustedWebOrigin },
		Grace:         2 * time.Second,
		AttachTimeout: 2 * time.Second,
		WriteTimeout:  2 * time.Second,
	})
	if err != nil {
		t.Fatalf("realtime.New failed: %v", err)
	}
	defer relay.Close()

	mux := http.NewServeMux()
	mux.Handle(connectorwss.Path, controlHandler)
	mux.Handle(realtime.DataPath, relay.DataHandler())
	mux.Handle(realtime.BrowserPath, relay.BrowserHandler())

	saasServer := httptest.NewServer(mux)
	defer saasServer.Close()

	saasBaseWS := "ws" + strings.TrimPrefix(saasServer.URL, "http")

	controlClient := wss.NewClient(wss.Config{
		BaseURL:       saasBaseWS + connectorwss.Path,
		Credential:    c2Credential,
		RuntimeID:     "c2-loopback-runtime",
		AllowInsecure: true,
	})
	if err := controlClient.Dial(ctx); err != nil {
		t.Fatalf("Connector Control Dial failed: %v", err)
	}
	defer controlClient.Close()

	if _, err := controlClient.SendHello(ctx); err != nil {
		t.Fatalf("Connector SendHello failed: %v", err)
	}
	waitC2ProtocolReady(t, router, connectorID)

	sessions := terminal.NewSessionManager(2*time.Second, nil)
	defer sessions.CloseAll(protocol.TerminalReasonServiceRestart)

	mockProvider := &coreprovider.MockProvider{
		ConnectionID: "loopback-provider",
	}

	connectorHandler := wss.NewHandler(mockProvider, controlClient)
	echoPTY := newC2ResizeAwarePTY(80, 24)
	connectorHandler.SetTerminalManager(sessions, func(targetVmKey, serverID string, cols, rows int) (terminal.PTYChannel, error) {
		return echoPTY, nil
	}, terminal.DataWSSClientConfig{
		EndpointURL:   saasBaseWS + realtime.DataPath,
		Credential:    c2Credential,
		RuntimeID:     "c2-loopback-runtime",
		AllowInsecure: true,
		DialTimeout:   2 * time.Second,
	})

	listenCtx, stopListen := context.WithCancel(ctx)
	listenDone := make(chan error, 1)
	go func() {
		listenDone <- connectorHandler.Listen(listenCtx, controlClient.Conn())
	}()
	defer func() {
		stopListen()
		_ = controlClient.Close()
		select {
		case <-listenDone:
		case <-time.After(time.Second):
		}
	}()

	dialBrowser := func() *c2BrowserPeer {
		header := http.Header{}
		header.Set("Origin", trustedWebOrigin)
		header.Set("Cookie", realtime.SessionCookieName+"="+c2BrowserCookie)
		dialer := websocket.Dialer{
			Subprotocols:     []string{realtime.BrowserSubprotocol},
			HandshakeTimeout: 2 * time.Second,
		}
		conn, resp, err := dialer.Dial(saasBaseWS+realtime.BrowserPath, header)
		if err != nil {
			t.Fatalf("Browser WSS dial failed: err=%v resp=%+v", err, resp)
		}
		return newC2BrowserPeer(t, conn)
	}

	const (
		c2SessionID   = "c2-loopback-term-session-001"
		c2AttachToken = "c2-loopback-token-123"
	)

	auth.registerSession(c2SessionID, c2AttachToken, labID, 1)

	if err := relay.Expect(realtime.Expected{
		TerminalSessionID: c2SessionID,
		ConnectorID:       connectorID.String(),
		LabInstanceID:     labID,
		Generation:        1,
	}); err != nil {
		t.Fatalf("relay.Expect failed: %v", err)
	}

	_, err = router.SendTerminalOpen(ctx, serverconnector.TerminalOpen{
		ConnectorID: connectorID,
		Correlation: serverconnector.TerminalCorrelation{
			TerminalSessionID: c2SessionID,
			LabInstanceID:     labID,
			Generation:        1,
		},
		TargetVMKey:      "workspace",
		ProviderServerID: "mock-server-01",
		Cols:             json.RawMessage("80"),
		Rows:             json.RawMessage("24"),
	})
	if err != nil {
		t.Fatalf("SendTerminalOpen failed: %v", err)
	}

	openResult := sink.waitForOpenResult(t, c2SessionID, 5*time.Second)
	if openResult.Payload.Outcome != serverconnector.TerminalOutcomeSucceeded {
		t.Fatalf("TERMINAL_OPEN did not succeed: outcome=%s", openResult.Payload.Outcome)
	}

	if err := relay.Activate(c2SessionID, time.Now().Add(5*time.Second)); err != nil {
		t.Fatalf("relay.Activate failed: %v", err)
	}

	browser := dialBrowser()
	browser.writeJSON(map[string]any{
		"type":              "TERMINAL_ATTACH",
		"messageId":         "attach-req-1",
		"sentAt":            time.Now().UTC().Format(time.RFC3339Nano),
		"terminalSessionId": c2SessionID,
		"payload": map[string]any{
			"sessionToken": c2AttachToken,
			"cols":         80,
			"rows":         24,
		},
	})

	attached := browser.readJSON(5 * time.Second)
	if attached["type"] != "TERMINAL_ATTACHED" {
		t.Fatalf("expected TERMINAL_ATTACHED, got %v", attached["type"])
	}
	attachedPayload, _ := attached["payload"].(map[string]any)
	if resumed, _ := attachedPayload["resumed"].(bool); resumed {
		t.Fatal("expected resumed=false on initial attach")
	}

	browser.writeBinary([]byte("LBT22_C2_ROUNDTRIP_TEST\n"))
	out := browser.readBinaryUntil("LBT22_C2_ROUNDTRIP_TEST", 5*time.Second)
	if !strings.Contains(out, "LBT22_C2_ROUNDTRIP_TEST") {
		t.Fatalf("expected echo output marker not received (received_len=%d)", len(out))
	}
	t.Log("Scenario 1A: Browser ↔ Relay ↔ Connector ↔ Echo PTY PASS")

	browser.writeJSON(map[string]any{
		"type":              "TERMINAL_RESIZE",
		"messageId":         "resize-req-1",
		"sentAt":            time.Now().UTC().Format(time.RFC3339Nano),
		"terminalSessionId": c2SessionID,
		"payload": map[string]any{
			"cols": 120,
			"rows": 40,
		},
	})
	deadline := time.Now().Add(5 * time.Second)
	resized := false
	for time.Now().Before(deadline) {
		select {
		case size := <-echoPTY.resizeCh:
			if size[0] == 120 && size[1] == 40 {
				resized = true
				break
			}
		case <-time.After(50 * time.Millisecond):
		}
		if resized {
			break
		}
	}
	if !resized {
		t.Fatal("timed out waiting for PTY Resize event to 120x40")
	}
	t.Log("Scenario 1B: Terminal RESIZE PASS")

	browser.close()

	// Verify that SaaS/Relay recorded the session transition to DETACHED and armed Grace period.
	deadline = time.Now().Add(3 * time.Second)
	detached := false
	for time.Now().Before(deadline) {
		if auth.isDetached(c2SessionID) {
			detached = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !detached {
		t.Fatalf("expected session %s to transition to DETACHED state in control", c2SessionID)
	}

	if active := sessions.ActiveCount(); active != 1 {
		t.Fatalf("expected 1 active session during grace, got %d", active)
	}

	browser2 := dialBrowser()
	defer browser2.close()

	browser2.writeJSON(map[string]any{
		"type":              "TERMINAL_ATTACH",
		"messageId":         "attach-req-2",
		"sentAt":            time.Now().UTC().Format(time.RFC3339Nano),
		"terminalSessionId": c2SessionID,
		"payload": map[string]any{
			"sessionToken": c2AttachToken,
			"cols":         120,
			"rows":         40,
		},
	})

	reconnected := browser2.readJSON(5 * time.Second)
	if reconnected["type"] != "TERMINAL_ATTACHED" {
		t.Fatalf("expected TERMINAL_ATTACHED on reconnect, got %v", reconnected["type"])
	}
	reconnectedPayload, _ := reconnected["payload"].(map[string]any)
	if resumed, _ := reconnectedPayload["resumed"].(bool); !resumed {
		t.Fatal("expected resumed=true on reconnect")
	}

	browser2.writeBinary([]byte("RECONNECT_VERIFY\n"))
	reconOut := browser2.readBinaryUntil("RECONNECT_VERIFY", 5*time.Second)
	if !strings.Contains(reconOut, "RECONNECT_VERIFY") {
		t.Fatalf("expected echo after reconnect marker not received (received_len=%d)", len(reconOut))
	}
	t.Log("Scenario 2: Browser Reconnect within Grace Period PASS")

	_ = echoPTY.Close()

	endedEvent := sink.waitForEnded(t, c2SessionID, 5*time.Second)
	if endedEvent.Correlation.TerminalSessionID != c2SessionID {
		t.Fatalf("expected ended event for session %s, got %s", c2SessionID, endedEvent.Correlation.TerminalSessionID)
	}

	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && sessions.ActiveCount() > 0 {
		time.Sleep(50 * time.Millisecond)
	}
	if sessions.ActiveCount() != 0 {
		t.Fatalf("expected 0 active sessions after PTY close, got %d", sessions.ActiveCount())
	}
	t.Log("Scenario 3: PTY Close & Safe Resource Release PASS")
}

// TestOpenStackTerminalC2Integration verifies the complete End-to-End round trip:
// Browser WebSocket ↔ SaaS Terminal Relay ↔ Connector Transport ↔ Real OpenStack VM PTY.
// It exercises:
// 1. Session Open, WSS Attach, and bidirectional raw byte streaming (stdin/stdout echo).
// 2. Terminal window resizing (RESIZE control frame and stty size reflection).
// 3. Unclean disconnect, session preservation within grace period, and successful reconnect (resumed=true).
// 4. Session termination, remote shell exit, clean PTY release, and absent residual state.
func TestOpenStackTerminalC2Integration(t *testing.T) {
	if os.Getenv("LABBIT_OPENSTACK_TERMINAL_C2_TEST") != "1" {
		t.Skip("set LABBIT_OPENSTACK_TERMINAL_C2_TEST=1 for real local OpenStack Terminal C2 full-stack acceptance")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	// 1. Initialize Real OpenStack Provider and discover test resources.
	a, err := New(ctx, ConfigFromEnvironment())
	if err != nil {
		t.Fatalf("Provider initialization failed: %v", err)
	}
	images, err := a.ListImages(ctx)
	if err != nil {
		t.Fatalf("Image discovery failed: %v", err)
	}
	flavors, err := a.ListFlavors(ctx)
	if err != nil {
		t.Fatalf("Flavor discovery failed: %v", err)
	}
	networks, err := a.ListNetworks(ctx)
	if err != nil {
		t.Fatalf("Network discovery failed: %v", err)
	}

	image := exactImage(t, images, environmentOrDefault("LABBIT_OPENSTACK_TEST_IMAGE", "ubuntu"))
	flavor := exactFlavor(t, flavors, environmentOrDefault("LABBIT_OPENSTACK_TEST_FLAVOR", "m1.small"))
	management := exactNetwork(t, networks, environmentOrDefault("LABBIT_OPENSTACK_TEST_MANAGEMENT_NETWORK", "sharednet1"))
	keyFile := a.provision.SSHPrivateKeyFile
	keyPair := integrationKeyPairName(t, ctx, a, keyFile)
	allowedCIDR := environmentOrDefault("LABBIT_OPENSTACK_TEST_SSH_CIDR", "172.16.8.1/32")
	managementSG := integrationManagementSecurityGroup(t, ctx, a, allowedCIDR)

	a.provision = normalizedProvisionConfig(ProvisionConfig{
		ProviderConnectionID:      "local-integration",
		ProjectID:                 integrationProjectID(t, a),
		ManagementNetworkID:       management.ID,
		ManagementSecurityGroupID: managementSG,
		KeyPairName:               keyPair,
		SSHAllowedCIDR:            allowedCIDR,
		LabSubnetCIDR:             environmentOrDefault("LABBIT_OPENSTACK_TEST_LAB_CIDR", "198.20.0.0/24"),
		SSHUsername:               a.provision.SSHUsername,
		SSHPrivateKeyFile:         keyFile,
		SSHKnownHostsFile:         t.TempDir() + "/known_hosts",
		PollInterval:              2 * time.Second,
	})

	labID := "lbt22-" + integrationRunID(t)
	var tracked []coreprovider.ResourceResult
	t.Cleanup(func() {
		cleanupM2Resources(t, a, tracked)
		t.Logf("Tracked resource absence verified after C2 test: distinctResources=%d", len(tracked))
	})

	// 2. Provision real disposable Workspace VM.
	result, err := a.Provision(ctx, coreprovider.ProvisionRequest{
		Correlation: coreprovider.Correlation{
			OperationID:   labID + "-provision",
			LabInstanceID: labID,
			Generation:    1,
		},
		CreationSnapshot: coreprovider.CreationSnapshot{
			ProviderConnectionID: "local-integration",
			WorkspaceVMKey:       "workspace",
			VMs: []coreprovider.VMSpec{{
				VMKey: "workspace", Role: "WORKSPACE",
				ImageID: image.ID, FlavorID: flavor.ID,
				FlavorSpec: coreprovider.FlavorSpec{VCPUs: flavor.VCPUs, RAMMiB: flavor.RAMMiB, DiskGiB: flavor.DiskGiB},
			}},
		},
	})
	tracked = append(tracked, result.ProviderResources...)
	if err != nil || result.Outcome != coreprovider.OutcomeSucceeded {
		t.Fatalf("Provision failed: outcome=%s err=%v", result.Outcome, err)
	}
	t.Log("OP-01 Provision ACTIVE and authenticated SSH ready for C2")

	target := vmaccess.Target{
		LabInstanceID: labID,
		Generation:    1,
		VMKey:         "workspace",
		ServerID:      providerResourceID(t, result.ProviderResources, coreprovider.ResourceTypeServer, "workspace"),
	}
	access := vmaccess.NewManager(a, vmaccess.Config{IdleTimeout: time.Second})
	defer access.Close()

	// 3. Assemble SaaS Control Stack & Realtime Relay.
	connectorID := uuid.New()
	const (
		c2Credential     = "c2-connector-secret-credential"
		c2BrowserCookie  = "c2-browser-session-cookie"
		trustedWebOrigin = "http://localhost:5173"
	)

	auth := newC2Auth(connectorID, c2Credential, c2BrowserCookie)
	registry := serverconnector.NewRegistry()
	sink := newC2TerminalSink()

	router, err := serverconnector.NewRouter(serverconnector.RouterOptions{
		Registry:     registry,
		TerminalSink: sink,
	})
	if err != nil {
		t.Fatalf("NewRouter failed: %v", err)
	}
	auth.setRouter(router)

	controlHandler, err := connectorwss.New(connectorwss.Options{
		Auth:              auth,
		Heartbeats:        auth,
		Registry:          registry,
		Router:            router,
		OfflineTimeout:    10 * time.Minute,
		HeartbeatInterval: 15 * time.Second,
	})
	if err != nil {
		t.Fatalf("connectorwss.New failed: %v", err)
	}
	defer controlHandler.Close()

	// Use shortened 5-second grace for integration test (production default is 60s).
	relay, err := realtime.New(realtime.Options{
		Control:       auth,
		Connectors:    auth,
		AllowOrigin:   func(origin string) bool { return origin == trustedWebOrigin },
		Grace:         5 * time.Second,
		AttachTimeout: 5 * time.Second,
		WriteTimeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("realtime.New failed: %v", err)
	}
	defer relay.Close()

	mux := http.NewServeMux()
	mux.Handle(connectorwss.Path, controlHandler)
	mux.Handle(realtime.DataPath, relay.DataHandler())
	mux.Handle(realtime.BrowserPath, relay.BrowserHandler())

	saasServer := httptest.NewServer(mux)
	defer saasServer.Close()

	saasBaseWS := "ws" + strings.TrimPrefix(saasServer.URL, "http")

	// 4. Assemble and Connect Real Connector.
	controlClient := wss.NewClient(wss.Config{
		BaseURL:       saasBaseWS + connectorwss.Path,
		Credential:    c2Credential,
		RuntimeID:     "c2-connector-runtime",
		AllowInsecure: true,
	})
	if err := controlClient.Dial(ctx); err != nil {
		t.Fatalf("Connector Control Dial failed: %v", err)
	}
	defer controlClient.Close()

	if _, err := controlClient.SendHello(ctx); err != nil {
		t.Fatalf("Connector SendHello failed: %v", err)
	}

	waitC2ProtocolReady(t, router, connectorID)

	sessions := terminal.NewSessionManager(5*time.Second, nil)
	defer sessions.CloseAll(protocol.TerminalReasonServiceRestart)

	connectorHandler := wss.NewHandler(a, controlClient)
	connectorHandler.SetVMPTYFactory(func(ctx context.Context, target vmaccess.Target, cols, rows int) (terminal.PTYChannel, error) {
		return terminal.NewManagedSSHPTY(ctx, access, target, cols, rows)
	})
	connectorHandler.SetTerminalManager(sessions, nil, terminal.DataWSSClientConfig{
		EndpointURL:   saasBaseWS + realtime.DataPath,
		Credential:    c2Credential,
		RuntimeID:     "c2-connector-runtime",
		AllowInsecure: true,
		DialTimeout:   5 * time.Second,
	})

	listenCtx, stopListen := context.WithCancel(ctx)
	listenDone := make(chan error, 1)
	go func() {
		listenDone <- connectorHandler.Listen(listenCtx, controlClient.Conn())
	}()
	defer func() {
		stopListen()
		_ = controlClient.Close()
		select {
		case <-listenDone:
		case <-time.After(time.Second):
		}
	}()

	// Helper to dial Browser WebSocket.
	dialBrowser := func() *c2BrowserPeer {
		header := http.Header{}
		header.Set("Origin", trustedWebOrigin)
		header.Set("Cookie", realtime.SessionCookieName+"="+c2BrowserCookie)
		dialer := websocket.Dialer{
			Subprotocols:     []string{realtime.BrowserSubprotocol},
			HandshakeTimeout: 5 * time.Second,
		}
		conn, resp, err := dialer.Dial(saasBaseWS+realtime.BrowserPath, header)
		if err != nil {
			t.Fatalf("Browser WSS dial failed: err=%v resp=%+v", err, resp)
		}
		return newC2BrowserPeer(t, conn)
	}

	// -------------------------------------------------------------------------
	// Scenario 1: Happy Path (Session Open, Attach, Stdin/Stdout Echo, Resize)
	// -------------------------------------------------------------------------
	const (
		c2SessionID   = "c2-term-session-001"
		c2AttachToken = "c2-attach-token-secret-123"
	)

	auth.registerSession(c2SessionID, c2AttachToken, labID, 1)

	// Pre-register expected correlation in Relay.
	if err := relay.Expect(realtime.Expected{
		TerminalSessionID: c2SessionID,
		ConnectorID:       connectorID.String(),
		LabInstanceID:     labID,
		Generation:        1,
	}); err != nil {
		t.Fatalf("relay.Expect failed: %v", err)
	}

	// SaaS issues TERMINAL_OPEN command to Connector.
	_, err = router.SendTerminalOpen(ctx, serverconnector.TerminalOpen{
		ConnectorID: connectorID,
		Correlation: serverconnector.TerminalCorrelation{
			TerminalSessionID: c2SessionID,
			LabInstanceID:     labID,
			Generation:        1,
		},
		TargetVMKey:      target.VMKey,
		ProviderServerID: target.ServerID,
		Cols:             json.RawMessage("80"),
		Rows:             json.RawMessage("24"),
	})
	if err != nil {
		t.Fatalf("SendTerminalOpen failed: %v", err)
	}

	// Wait for Connector to attach Data WSS and return TERMINAL_OPEN_RESULT.
	openResult := sink.waitForOpenResult(t, c2SessionID, 10*time.Second)
	if openResult.Payload.Outcome != serverconnector.TerminalOutcomeSucceeded {
		t.Fatalf("TERMINAL_OPEN did not succeed: outcome=%s", openResult.Payload.Outcome)
	}

	// Activate terminal session in Relay.
	if err := relay.Activate(c2SessionID, time.Now().Add(5*time.Second)); err != nil {
		t.Fatalf("relay.Activate failed: %v", err)
	}

	// Connect Browser and send TERMINAL_ATTACH.
	browser := dialBrowser()
	browser.writeJSON(map[string]any{
		"type":              "TERMINAL_ATTACH",
		"messageId":         "attach-req-1",
		"sentAt":            time.Now().UTC().Format(time.RFC3339Nano),
		"terminalSessionId": c2SessionID,
		"payload": map[string]any{
			"sessionToken": c2AttachToken,
			"cols":         80,
			"rows":         24,
		},
	})

	attached := browser.readJSON(5 * time.Second)
	if attached["type"] != "TERMINAL_ATTACHED" {
		t.Fatalf("expected TERMINAL_ATTACHED, got %v", attached["type"])
	}
	attachedPayload, _ := attached["payload"].(map[string]any)
	if resumed, _ := attachedPayload["resumed"].(bool); resumed {
		t.Fatal("expected resumed=false on initial attach")
	}

	// Stdin / Stdout verification through real VM PTY.
	browser.writeBinary([]byte("printf 'LBT22-C2-MARKER:%s\\n' 'E2E_ROUNDTRIP_SUCCESS'\n"))
	out := browser.readBinaryUntil("LBT22-C2-MARKER:E2E_ROUNDTRIP_SUCCESS", 10*time.Second)
	if !strings.Contains(out, "E2E_ROUNDTRIP_SUCCESS") {
		t.Fatalf("expected stdout marker not received")
	}
	t.Log("Scenario 1A: Browser ↔ Relay ↔ Connector ↔ Real VM PTY I/O PASS")

	// Terminal Resize verification.
	browser.writeJSON(map[string]any{
		"type":              "TERMINAL_RESIZE",
		"messageId":         "resize-req-1",
		"sentAt":            time.Now().UTC().Format(time.RFC3339Nano),
		"terminalSessionId": c2SessionID,
		"payload": map[string]any{
			"cols": 120,
			"rows": 40,
		},
	})

	browser.writeBinary([]byte("printf 'LBT22-SIZE:%s\\n' \"$(stty size)\"\n"))
	sizeOut := browser.readBinaryUntil("LBT22-SIZE:40 120", 10*time.Second)
	if !strings.Contains(sizeOut, "40 120") {
		t.Fatalf("expected resized stty size '40 120' marker not received (received_len=%d)", len(sizeOut))
	}
	t.Log("Scenario 1B: Terminal RESIZE (120x40 -> stty 40 120) PASS")

	// -------------------------------------------------------------------------
	// Scenario 2: Unclean Browser Disconnect & Graceful Reconnect
	// -------------------------------------------------------------------------
	// Set an environment variable in the shell to verify state preservation.
	browser.writeBinary([]byte("export LBT22_PRESERVED_STATE='C2_RECONNECT_OK'\n"))
	browser.writeBinary([]byte("printf 'LBT22-STATE-SET:%s\\n' 'DONE'\n"))
	_ = browser.readBinaryUntil("LBT22-STATE-SET:DONE", 5*time.Second)

	// Simulate sudden browser network disconnection.
	browser.close()

	// Verify that SaaS/Relay recorded the session transition to DETACHED and armed Grace period.
	deadline := time.Now().Add(5 * time.Second)
	detached := false
	for time.Now().Before(deadline) {
		if auth.isDetached(c2SessionID) {
			detached = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !detached {
		t.Fatalf("expected session %s to transition to DETACHED state in control", c2SessionID)
	}

	// Connector and VM PTY must still be running in grace period.
	if active := sessions.ActiveCount(); active != 1 {
		t.Fatalf("expected 1 active session during grace, got %d", active)
	}

	// Reconnect with a new browser socket using the same sessionToken.
	browser2 := dialBrowser()
	defer browser2.close()

	browser2.writeJSON(map[string]any{
		"type":              "TERMINAL_ATTACH",
		"messageId":         "attach-req-2",
		"sentAt":            time.Now().UTC().Format(time.RFC3339Nano),
		"terminalSessionId": c2SessionID,
		"payload": map[string]any{
			"sessionToken": c2AttachToken,
			"cols":         120,
			"rows":         40,
		},
	})

	reconnected := browser2.readJSON(5 * time.Second)
	if reconnected["type"] != "TERMINAL_ATTACHED" {
		t.Fatalf("expected TERMINAL_ATTACHED on reconnect, got %v", reconnected["type"])
	}
	reconnectedPayload, _ := reconnected["payload"].(map[string]any)
	if resumed, _ := reconnectedPayload["resumed"].(bool); !resumed {
		t.Fatal("expected resumed=true on reconnect")
	}

	// Verify the shell environment and state were preserved across reconnect.
	browser2.writeBinary([]byte("printf 'LBT22-STATE-ECHO:%s\\n' \"$LBT22_PRESERVED_STATE\"\n"))
	reconnectOut := browser2.readBinaryUntil("LBT22-STATE-ECHO:C2_RECONNECT_OK", 10*time.Second)
	if !strings.Contains(reconnectOut, "C2_RECONNECT_OK") {
		t.Fatalf("preserved state not found after reconnect (received_len=%d)", len(reconnectOut))
	}
	t.Log("Scenario 2: Browser Reconnect within Grace Period & State Preservation PASS")

	// -------------------------------------------------------------------------
	// Scenario 3: Clean Exit & Resource Cleanup
	// -------------------------------------------------------------------------
	// Exiting the remote shell should terminate the PTY and notify Relay and SaaS.
	browser2.writeBinary([]byte("exit 0\n"))

	endedEvent := sink.waitForEnded(t, c2SessionID, 10*time.Second)
	if endedEvent.Correlation.TerminalSessionID != c2SessionID {
		t.Fatalf("expected ended event for session %s, got %s", c2SessionID, endedEvent.Correlation.TerminalSessionID)
	}

	// Wait briefly for sessions manager to reflect termination.
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && sessions.ActiveCount() > 0 {
		time.Sleep(50 * time.Millisecond)
	}
	if sessions.ActiveCount() != 0 {
		t.Fatalf("expected 0 active sessions after shell exit, got %d", sessions.ActiveCount())
	}
	t.Log("Scenario 3: Shell Exit, Safe TERMINAL_ENDED, and Resource Release PASS")
}
