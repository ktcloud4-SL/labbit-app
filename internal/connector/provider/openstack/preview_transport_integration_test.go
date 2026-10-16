package openstackprovider_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/app"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	openstackprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider/openstack"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/terminal"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

const previewFixtureCredential = "lbt174-synthetic-fixture-credential"

type previewGatewayPeer struct {
	attach protocol.PreviewAttachMessage
	conn   *websocket.Conn
}
type strictPreviewGateway struct {
	mu       sync.Mutex
	expected map[string]protocol.PreviewOpenMessage
	conns    []*websocket.Conn
	accepted chan previewGatewayPeer
	rejected chan struct{}
	stop     chan struct{}
	server   *httptest.Server
}

func newStrictPreviewGateway(t *testing.T) *strictPreviewGateway {
	t.Helper()
	g := &strictPreviewGateway{expected: make(map[string]protocol.PreviewOpenMessage), accepted: make(chan previewGatewayPeer, 16), rejected: make(chan struct{}, 16), stop: make(chan struct{})}
	up := websocket.Upgrader{Subprotocols: []string{protocol.SubprotocolPreviewData}}
	g.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/connector/v1/preview-data" || r.Header.Get("Authorization") != "Bearer "+previewFixtureCredential {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		ws, e := up.Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer ws.Close()
		g.mu.Lock()
		g.conns = append(g.conns, ws)
		g.mu.Unlock()
		_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
		var attach protocol.PreviewAttachMessage
		if ws.ReadJSON(&attach) != nil {
			return
		}
		g.mu.Lock()
		expected, ok := g.expected[attach.PreviewSessionID]
		g.mu.Unlock()
		if !ok || attach.Type != protocol.MessageTypePreviewAttach || attach.ReplyToMessageID != expected.MessageID ||
			attach.LabInstanceID != expected.LabInstanceID || attach.Generation != expected.Generation ||
			attach.Payload.RuntimeID == "" || attach.Payload.TargetVmKey != expected.Payload.TargetVmKey ||
			attach.Payload.ProviderServerID != expected.Payload.ProviderServerID || attach.Payload.TargetPort != expected.Payload.TargetPort {
			_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(1008, "attach rejected"), time.Now().Add(time.Second))
			select {
			case g.rejected <- struct{}{}:
			default:
			}
			return
		}
		_ = ws.SetReadDeadline(time.Time{})
		if ws.WriteJSON(protocol.PreviewAttachedMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypePreviewAttached, MessageID: uuid.NewString(), SentAt: time.Now().UTC(), ReplyToMessageID: attach.MessageID, PreviewSessionID: attach.PreviewSessionID, LabInstanceID: attach.LabInstanceID, Generation: attach.Generation}, Payload: protocol.PreviewAttachedPayload{}}) != nil {
			return
		}
		select {
		case g.accepted <- previewGatewayPeer{attach, ws}:
		case <-g.stop:
			return
		}
		<-g.stop
	}))
	t.Cleanup(func() {
		close(g.stop)
		g.mu.Lock()
		for _, c := range g.conns {
			_ = c.Close()
		}
		g.mu.Unlock()
		g.server.Close()
	})
	return g
}
func (g *strictPreviewGateway) url() string {
	return strings.Replace(g.server.URL, "http", "ws", 1) + "/connector/v1/preview-data"
}
func (g *strictPreviewGateway) expect(msg protocol.PreviewOpenMessage) {
	g.mu.Lock()
	g.expected[msg.PreviewSessionID] = msg
	g.mu.Unlock()
}
func (g *strictPreviewGateway) peer(t *testing.T) previewGatewayPeer {
	t.Helper()
	select {
	case p := <-g.accepted:
		return p
	case <-time.After(10 * time.Second):
		t.Fatal("Preview attach missing")
		return previewGatewayPeer{}
	}
}
func previewReply(t *testing.T, saas *mock.MockSaaS, id, kind string, timeout time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, raw := range saas.ReceivedMessages() {
			var env protocol.BaseEnvelope
			if json.Unmarshal(raw, &env) == nil && env.ReplyToMessageID == id && env.Type == kind {
				return raw
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("Control reply missing: %s", kind)
	return nil
}
func previewOperation(t *testing.T, saas *mock.MockSaaS, lab string, gen int64, kind string, snapshot *protocol.CreationSnapshot, refs []protocol.ProviderResourceRef) protocol.OperationResultMessage {
	t.Helper()
	id := uuid.NewString()
	if e := saas.SendRaw(protocol.OperationCommandMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeOperationCommand, MessageID: id, SentAt: time.Now().UTC(), OperationID: uuid.NewString(), LabInstanceID: lab, Generation: gen}, Payload: protocol.OperationCommandPayload{MutationType: kind, CreationSnapshot: snapshot, ProviderResources: refs}}); e != nil {
		t.Fatal("Control operation send failed")
	}
	var out protocol.OperationResultMessage
	if json.Unmarshal(previewReply(t, saas, id, protocol.MessageTypeOperationResult, 8*time.Minute), &out) != nil || out.Payload.Outcome != protocol.OutcomeSucceeded {
		t.Fatal("actual local lifecycle operation failed")
	}
	return out
}
func previewRefs(result protocol.OperationResultMessage, gen int64) []protocol.ProviderResourceRef {
	var refs []protocol.ProviderResourceRef
	for _, r := range result.Payload.ProviderResources {
		if r.Generation == gen {
			refs = append(refs, protocol.ProviderResourceRef{ResourceType: r.ResourceType, ProviderID: r.ProviderID, Generation: r.Generation, LogicalName: r.LogicalName})
		}
	}
	return refs
}
func startPreviewHTTP(t *testing.T, ctx context.Context, access *vmaccess.Manager, target vmaccess.Target) *vmaccess.Session {
	t.Helper()
	s, e := access.OpenSession(ctx, target)
	if e != nil {
		t.Fatal("HTTP SSH channel failed")
	}
	var out io.Reader
	if e = s.Setup(ctx, func(raw *ssh.Session) error {
		var e error
		out, e = raw.StdoutPipe()
		if e != nil {
			return e
		}
		raw.Stderr = io.Discard
		return raw.Start("exec python3 -u -c 'from http.server import BaseHTTPRequestHandler,HTTPServer; exec(\"class H(BaseHTTPRequestHandler):\\n def do_GET(self):\\n  b=b\\\"LBT174-HTTP-OK\\\"; self.send_response(200); self.send_header(\\\"Content-Length\\\",str(len(b))); self.end_headers(); self.wfile.write(b)\\n def log_message(self,*a): pass\"); h=HTTPServer((\"127.0.0.1\",3000),H); print(\"READY\",flush=True); h.serve_forever()'")
	}); e != nil {
		_ = s.Close()
		t.Fatal("HTTP fixture start failed")
	}
	ready := make(chan bool, 1)
	go func() { line, e := bufio.NewReader(out).ReadString('\n'); ready <- e == nil && line == "READY\n" }()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("HTTP fixture not ready")
		}
	case <-time.After(10 * time.Second):
		_ = s.Close()
		t.Fatal("HTTP fixture readiness timeout")
	}
	return s
}
func previewHTTP(t *testing.T, peer previewGatewayPeer) {
	t.Helper()
	request := []byte("GET /fixture HTTP/1.1\r\nHost: fixture\r\nConnection: close\r\n\r\n")
	// HTTP bytes deliberately cross arbitrary WebSocket frame boundaries.
	for len(request) > 0 {
		n := 7
		if n > len(request) {
			n = len(request)
		}
		if peer.conn.WriteMessage(websocket.BinaryMessage, request[:n]) != nil {
			t.Fatal("Preview binary request failed")
		}
		request = request[n:]
	}
	_ = peer.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var stream bytes.Buffer
	for {
		kind, b, e := peer.conn.ReadMessage()
		if e != nil {
			if !websocket.IsCloseError(e, 1000) {
				t.Fatal("Preview upstream did not close normally")
			}
			break
		}
		if kind != websocket.BinaryMessage || stream.Len()+len(b) > 8192 {
			t.Fatal("invalid Preview response stream")
		}
		stream.Write(b)
	}
	response, e := http.ReadResponse(bufio.NewReader(&stream), nil)
	if e != nil {
		t.Fatal("invalid forwarded HTTP response")
	}
	defer response.Body.Close()
	body, e := io.ReadAll(io.LimitReader(response.Body, 128))
	if e != nil || response.StatusCode != 200 || string(body) != "LBT174-HTTP-OK" {
		t.Fatal("forwarded HTTP response mismatch")
	}
}
func previewAwaitNoTunnel(t *testing.T, a *app.ConnectorApp, id string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s, ok := a.PreviewManager.GetSession(id)
		if !ok || !s.HasActiveTunnel() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("Preview tunnel retained after close")
}

// Opt-in local OpenStack acceptance. The real App wires Control, Preview WSS,
// VMAccess and Provider lifecycle; only SaaS/Gateway authority is a fixture.
// HTTP/PTYSFTP bytes and credentials are asserted in memory, never logged.
func TestOpenStackPreviewTransportIntegration(t *testing.T) {
	if os.Getenv("LABBIT_OPENSTACK_PREVIEW_TRANSPORT_TEST") != "1" {
		t.Skip("set LABBIT_OPENSTACK_PREVIEW_TRANSPORT_TEST=1 for disposable local OpenStack Preview transport acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 17*time.Minute)
	defer cancel()
	fixture := openstackprovider.NewPreviewTransportFixture(t, ctx)
	saas := mock.NewMockSaaS(previewFixtureCredential)
	defer saas.Close()
	gateway := newStrictPreviewGateway(t)
	t.Setenv("LABBIT_ENVIRONMENT", "development")
	t.Setenv("LABBIT_SAAS_BASE_URL", saas.URL())
	t.Setenv("LABBIT_PREVIEW_GATEWAY_URL", gateway.url())
	t.Setenv("LABBIT_CONNECTOR_CREDENTIAL", previewFixtureCredential)
	t.Setenv("LABBIT_CONNECTOR_CREDENTIAL_FILE", "")
	t.Setenv("LABBIT_WORKSPACE_ROOT", "")
	t.Setenv("LABBIT_WORKSPACE_SFTP_HELPER", "")
	client := wss.NewClient(wss.Config{BaseURL: saas.URL(), Credential: previewFixtureCredential, RuntimeID: "lbt174-local-runtime", AllowInsecure: true})
	if client.Dial(ctx) != nil {
		t.Fatal("Control dial failed")
	}
	defer client.Close()
	if _, e := client.SendHello(ctx); e != nil {
		t.Fatal("Control HELLO failed")
	}
	connector, e := app.BuildConnector(fixture, client)
	if e != nil {
		t.Fatal("production App wiring failed")
	}
	defer connector.VMAccess.Close()
	defer connector.TerminalManager.Shutdown(protocol.TerminalReasonServiceRestart)
	defer connector.PreviewManager.Shutdown(protocol.PreviewReasonServiceRestart)
	listenDone := make(chan error, 1)
	go func() { listenDone <- connector.Handler.Listen(ctx, client.Conn()) }()
	defer func() {
		cancel()
		_ = client.Close()
		select {
		case <-listenDone:
		case <-time.After(2 * time.Second):
			t.Error("Control listener did not stop")
		}
	}()
	lab := "lbt174-" + uuid.NewString()
	created := previewOperation(t, saas, lab, 1, protocol.MutationTypeProvision, fixture.Snapshot, nil)
	target := fixture.Target(t, lab, 1)
	httpServer := startPreviewHTTP(t, ctx, connector.VMAccess, target)
	defer httpServer.Close()
	open := func(id, attempt string, target vmaccess.Target, port int) protocol.PreviewOpenMessage {
		return protocol.PreviewOpenMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypePreviewOpen, MessageID: attempt, SentAt: time.Now().UTC(), PreviewSessionID: id, LabInstanceID: target.LabInstanceID, Generation: target.Generation}, Payload: protocol.PreviewOpenPayload{TargetVmKey: target.VMKey, ProviderServerID: target.ServerID, TargetPort: port}}
	}
	sendOpen := func(msg protocol.PreviewOpenMessage, want string) {
		t.Helper()
		gateway.expect(msg)
		if saas.SendRaw(msg) != nil {
			t.Fatal("Control Preview send failed")
		}
		var out protocol.PreviewOpenResultMessage
		if json.Unmarshal(previewReply(t, saas, msg.MessageID, protocol.MessageTypePreviewOpenResult, 15*time.Second), &out) != nil {
			t.Fatal("invalid Preview result")
		}
		if want == "" {
			if out.Payload.Outcome != protocol.OutcomeSucceeded {
				t.Fatal("Preview open failed")
			}
		} else if out.Payload.Outcome != protocol.OutcomeFailed || out.Payload.Error == nil || out.Payload.Error.Code != want {
			t.Fatal("unsafe Preview error mapping")
		}
	}
	id := uuid.NewString()
	for i := 0; i < 2; i++ {
		msg := open(id, uuid.NewString(), target, 3000)
		sendOpen(msg, "")
		peer := gateway.peer(t)
		if peer.attach.ReplyToMessageID != msg.MessageID {
			t.Fatal("OPEN/ATTACH correlation lost")
		}
		previewHTTP(t, peer)
		previewAwaitNoTunnel(t, connector, id)
	}
	t.Log("Control OPEN -> outbound Preview WSS -> exact ATTACH -> actual SSH forwarding -> VM HTTP 200 twice; sequential tunnel contract PASS")
	// Two active sessions remain isolated; closing one preserves the sibling.
	one, two := uuid.NewString(), uuid.NewString()
	sendOpen(open(one, uuid.NewString(), target, 3000), "")
	p1 := gateway.peer(t)
	sendOpen(open(two, uuid.NewString(), target, 3000), "")
	p2 := gateway.peer(t)
	closeMsg := protocol.PreviewCloseMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypePreviewClose, MessageID: uuid.NewString(), SentAt: time.Now().UTC(), PreviewSessionID: one, LabInstanceID: lab, Generation: 1}, Payload: protocol.PreviewClosePayload{Reason: protocol.PreviewReasonSessionClosed}}
	if saas.SendRaw(closeMsg) != nil {
		t.Fatal("Preview CLOSE failed")
	}
	_ = p1.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, e := p1.conn.ReadMessage(); e == nil {
		t.Fatal("closed session retained Data WSS")
	}
	previewHTTP(t, p2)
	previewAwaitNoTunnel(t, connector, two)
	// Production shared VMAccess still supports PTY and SFTP after Preview close.
	pty, e := terminal.NewManagedSSHPTY(ctx, connector.VMAccess, target, 80, 24)
	if e != nil {
		t.Fatal("sibling PTY failed")
	}
	defer pty.Close()
	if _, e = pty.Write([]byte("printf 'LBT174-%s\\n' 'PTY-OK'\n")); e != nil {
		t.Fatal("sibling PTY write failed")
	}
	ptyRead := make(chan bool, 1)
	go func() {
		b := make([]byte, 256)
		var seen strings.Builder
		for seen.Len() < 4096 {
			n, e := pty.Read(b)
			seen.Write(b[:n])
			if strings.Contains(seen.String(), "LBT174-PTY-OK") {
				ptyRead <- true
				return
			}
			if e != nil {
				break
			}
		}
		ptyRead <- false
	}()
	select {
	case ok := <-ptyRead:
		if !ok {
			t.Fatal("PTY marker missing")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PTY read timeout")
	}
	fileSession, e := connector.VMAccess.OpenSession(ctx, target)
	if e != nil {
		t.Fatal("SFTP session failed")
	}
	defer fileSession.Close()
	var sc *sftp.Client
	if e = fileSession.Setup(ctx, func(raw *ssh.Session) error {
		in, e := raw.StdinPipe()
		if e != nil {
			return e
		}
		out, e := raw.StdoutPipe()
		if e != nil {
			return e
		}
		if e = raw.RequestSubsystem("sftp"); e != nil {
			return e
		}
		sc, e = sftp.NewClientPipe(out, in)
		return e
	}); e != nil {
		t.Fatal("SFTP setup failed")
	}
	defer sc.Close()
	if _, e = sc.Stat("."); e != nil {
		t.Fatal("SFTP failed after Preview close")
	}
	sendOpen(open(uuid.NewString(), uuid.NewString(), target, 3001), protocol.PreviewErrorCodeAppNotRunning)
	sendOpen(open(uuid.NewString(), uuid.NewString(), target, 22), protocol.PreviewErrorCodePortRejected)
	wrong := target
	wrong.ServerID = "missing-server"
	sendOpen(open(uuid.NewString(), uuid.NewString(), wrong, 3000), protocol.PreviewErrorCodeVMUnreachable)
	t.Log("Concurrent session isolation, Control CLOSE, actual PTY/SFTP after Preview close, APP_NOT_RUNNING/PORT_REJECTED/VM_UNREACHABLE PASS")
	// Reset while an actual Preview tunnel is active. The App lifecycle wrapper
	// retires both attached/idle Preview sessions and the old SSH generation.
	active := uuid.NewString()
	sendOpen(open(active, uuid.NewString(), target, 3000), "")
	oldPeer := gateway.peer(t)
	reset := previewOperation(t, saas, lab, 2, protocol.MutationTypeReset, fixture.Snapshot, previewRefs(created, 1))
	_ = oldPeer.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, e := oldPeer.conn.ReadMessage(); e == nil {
		t.Fatal("old generation Data WSS survived Reset")
	}
	if connector.PreviewManager.Count() != 0 {
		t.Fatal("old logical Preview sessions survived Reset")
	}
	sendOpen(open(uuid.NewString(), uuid.NewString(), target, 3000), protocol.PreviewErrorCodeUnavailable)
	newTarget := fixture.Target(t, lab, 2)
	newHTTP := startPreviewHTTP(t, ctx, connector.VMAccess, newTarget)
	defer newHTTP.Close()
	active = uuid.NewString()
	sendOpen(open(active, uuid.NewString(), newTarget, 3000), "")
	newPeer := gateway.peer(t)
	previewHTTP(t, newPeer)
	previewAwaitNoTunnel(t, connector, active)
	previewOperation(t, saas, lab, 2, protocol.MutationTypeCleanup, nil, previewRefs(reset, 2))
	if connector.PreviewManager.Count() != 0 {
		t.Fatal("Preview session survived Cleanup")
	}
	sendOpen(open(uuid.NewString(), uuid.NewString(), newTarget, 3000), protocol.PreviewErrorCodeUnavailable)
	t.Log("Actual Control Reset gen1->gen2 and Cleanup: old WSS closed, logical sessions removed, stale targets denied, fresh generation HTTP PASS")
	// Lifecycle/control metadata must not contain the request stream or fixture
	// response, cookies, SSH output or credentials.
	for _, raw := range saas.ReceivedMessages() {
		for _, forbidden := range []string{"GET /fixture", "LBT174-HTTP-OK", "LBT174-PTY-OK", previewFixtureCredential} {
			if bytes.Contains(raw, []byte(forbidden)) {
				t.Fatal("Preview/SSH content leaked onto Control")
			}
		}
	}
	t.Log("Control contains lifecycle metadata only; HTTP/PTY bytes and synthetic credential not present PASS")
}
