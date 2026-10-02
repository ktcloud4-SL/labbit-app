package filetransport

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connectorwss"
	"github.com/ktcloud4-SL/labbit-app/internal/server/filetransport/filetest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/workspacefile"
)

// 테스트용 Credential 원문이다. 실제 Credential이 아니며 응답·frame·log에 나타나면 안 되는 고유 값이다.
const (
	credentialA = "test-file-credential-a-unique-7d31"
	credentialB = "test-file-credential-b-unique-2c9e"

	// sourceMarker와 secretDir는 파일 본문과 경로에 넣는 고유 값이다. Control frame, log, 오류 문구에 나타나면 안 된다.
	sourceMarker = "SOURCE-MARKER-must-not-leak-4f8a"
	secretDir    = "secret-dir-must-not-leak-9b21"
)

// fakeAuth는 Credential 원문 → Principal 표로 인증한다. 표에 없거나 revoke되었으면 ErrUnauthenticated다.
// connectorwss.Authenticator, connectorwss.HeartbeatRecorder, filetransport.Authenticator를 모두 구현한다.
type fakeAuth struct {
	mu         sync.Mutex
	principals map[string]connector.Principal
	revoked    map[uuid.UUID]bool
	failWith   error
	calls      int
}

func (f *fakeAuth) Authenticate(_ context.Context, credential connector.Credential) (connector.Principal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.failWith != nil {
		return connector.Principal{}, f.failWith
	}
	principal, ok := f.principals[string(credential)]
	if !ok || f.revoked[principal.CredentialID] {
		return connector.Principal{}, connector.ErrUnauthenticated
	}
	return principal, nil
}

func (f *fakeAuth) RecordHeartbeat(context.Context, connector.Principal, time.Time) error { return nil }

func (f *fakeAuth) revoke(credentialID uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked[credentialID] = true
}

func (f *fakeAuth) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failWith = err
}

// syncBuffer는 handler goroutine이 쓰는 log를 test가 안전하게 읽게 한다.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// fileSinkForwarder는 Router가 먼저 만들어져야 하는 순환을 끊는다(internal/server/app과 같은 방식).
type fileSinkForwarder struct{ target connector.FileSink }

func (f *fileSinkForwarder) HandleFileEvent(e connector.FileEvent) { f.target.HandleFileEvent(e) }

// harness는 실제 Connector Control WSS handler, Router, Registry, Broker, File Data WSS handler를 하나의 httptest server에 올린다.
// 이 위에 contract peer(fake Connector)를 연결해 Browser HTTP 이후의 SaaS ↔ Connector 구간을 검증한다.
type harness struct {
	t          *testing.T
	auth       *fakeAuth
	registry   *connector.Registry
	router     *connector.Router
	broker     *Broker
	server     *httptest.Server
	logs       *syncBuffer
	principalA connector.Principal
	principalB connector.Principal
}

func newHarness(t *testing.T, mutate ...func(*Options)) *harness {
	t.Helper()
	principalA := connector.Principal{ConnectorID: uuid.New(), OrganizationID: uuid.New(), CredentialID: uuid.New()}
	principalB := connector.Principal{ConnectorID: uuid.New(), OrganizationID: uuid.New(), CredentialID: uuid.New()}
	auth := &fakeAuth{
		principals: map[string]connector.Principal{credentialA: principalA, credentialB: principalB},
		revoked:    map[uuid.UUID]bool{},
	}
	logs := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	registry := connector.NewRegistry()

	forwarder := &fileSinkForwarder{}
	router, err := connector.NewRouter(connector.RouterOptions{Registry: registry, FileSink: forwarder, Logger: logger})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	opts := Options{
		Control: router, Auth: auth, Logger: logger,
		// 정상 경로가 기다릴 일은 없다. 실패 test는 짧은 값으로 바꾼다.
		AttachTimeout: 5 * time.Second, OperationTimeout: 5 * time.Second,
	}
	for _, fn := range mutate {
		fn(&opts)
	}
	broker, err := New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	forwarder.target = broker
	registry.SetRevokeObserver(broker)

	control, err := connectorwss.New(connectorwss.Options{Auth: auth, Heartbeats: auth, Registry: registry, Router: router, Logger: logger})
	if err != nil {
		t.Fatalf("connectorwss.New() error = %v", err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET "+connectorwss.Path, control)
	mux.Handle("GET "+DataPath, broker.Handler())
	server := httptest.NewServer(mux)
	t.Cleanup(func() {
		broker.Close()
		control.Close()
		server.Close()
	})
	return &harness{t: t, auth: auth, registry: registry, router: router, broker: broker, server: server, logs: logs, principalA: principalA, principalB: principalB}
}

func (h *harness) controlURL() string {
	return "ws" + strings.TrimPrefix(h.server.URL, "http") + connectorwss.Path
}

func (h *harness) dataURL() string {
	return "ws" + strings.TrimPrefix(h.server.URL, "http") + DataPath
}

// peer는 principal A의 Connector로 연결한 contract peer를 시작하고 protocol-ready가 될 때까지 기다린다.
// capabilities가 file-v1을 포함하지 않아도 ready는 기다린다(capability 판정은 SaaS가 한다).
func (h *harness) peer(capabilities []string, fs *filetest.FS, behavior func(filetest.Open) filetest.Behavior) *filetest.Peer {
	h.t.Helper()
	p := filetest.Start(h.t, filetest.Config{
		ControlURL: h.controlURL(), DataURL: h.dataURL(), Credential: credentialA,
		Capabilities: capabilities, FS: fs, Behavior: behavior,
	})
	h.waitReady(h.principalA.ConnectorID)
	return p
}

// fileV1Peer는 file-v1을 선언한 peer다.
func (h *harness) fileV1Peer(fs *filetest.FS, behavior func(filetest.Open) filetest.Behavior) *filetest.Peer {
	h.t.Helper()
	return h.peer([]string{protocol.CapabilityFileV1}, fs, behavior)
}

func (h *harness) waitReady(connectorID uuid.UUID) {
	h.t.Helper()
	waitFor(h.t, "protocol-ready route", func() bool {
		return h.registry.WithReadyRoute(connectorID, func(connector.Session, connector.Route) error { return nil }) == nil
	})
}

// target은 principal A의 Connector가 맡은 Workspace VM이다.
func (h *harness) target() workspacefile.Target {
	return workspacefile.Target{
		LabInstanceID: labInstanceID, Generation: 3, ConnectorID: h.principalA.ConnectorID,
		WorkspaceVMKey: "vk-web", ProviderServerID: "srv-web-g3", RequestID: "request-1",
	}
}

var labInstanceID = uuid.MustParse("00000000-0000-4000-8000-0000000000d1")

func (h *harness) tree(ctx context.Context, dir string) ([]workspacefile.Entry, error) {
	h.t.Helper()
	path, err := workspacefile.ParseDirectory(dir)
	if err != nil {
		h.t.Fatalf("ParseDirectory(%q) error = %v", dir, err)
	}
	return h.broker.Tree(ctx, h.target(), path)
}

func (h *harness) read(ctx context.Context, file string, maxBytes int64) (workspacefile.FileData, error) {
	h.t.Helper()
	path, err := workspacefile.ParseFile(file)
	if err != nil {
		h.t.Fatalf("ParseFile(%q) error = %v", file, err)
	}
	return h.broker.Read(ctx, h.target(), path, maxBytes)
}

func (h *harness) save(ctx context.Context, file, expected string, content string) (workspacefile.Revision, error) {
	h.t.Helper()
	path, err := workspacefile.ParseFile(file)
	if err != nil {
		h.t.Fatalf("ParseFile(%q) error = %v", file, err)
	}
	return h.broker.Save(ctx, h.target(), path, workspacefile.Revision(expected), []byte(content))
}

// waitIdle은 요청의 pending 상태(Broker, Router, Data WSS trust, peer)가 모두 정리될 때까지 기다린다.
func (h *harness) waitIdle(p *filetest.Peer) {
	h.t.Helper()
	waitFor(h.t, "cleanup of pending file state", func() bool {
		return h.broker.PendingCount() == 0 &&
			h.router.PendingFileOpens(h.principalA.ConnectorID) == 0 &&
			h.broker.trust.size() == 0 &&
			(p == nil || p.Active() == 0)
	})
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// rawData는 Data WSS에 지정한 header/subprotocol로 Upgrade를 시도한다. 성공하지 못하면 응답을 함께 반환한다.
func (h *harness) rawData(credential string, subprotocols ...string) (*websocket.Conn, *http.Response, error) {
	h.t.Helper()
	header := http.Header{}
	if credential != "" {
		header.Set("Authorization", "Bearer "+credential)
	}
	dialer := websocket.Dialer{Subprotocols: subprotocols, HandshakeTimeout: 5 * time.Second}
	return dialer.Dial(h.dataURL(), header)
}

// dataFrame은 contract peer가 아닌 test가 직접 보내는 File Data WSS frame이다.
func dataFrame(kind, requestID string, generation int64, payload map[string]any) map[string]any {
	return map[string]any{
		"type": kind, "messageId": uuid.NewString(), "sentAt": time.Now().UTC().Format(time.RFC3339Nano),
		"fileRequestId": requestID, "labInstanceId": labInstanceID.String(), "generation": generation, "payload": payload,
	}
}

// h2uuid는 test용 고정 UUID다.
func h2uuid(n byte) uuid.UUID {
	var id uuid.UUID
	id[15] = n
	id[6] = 0x40
	id[8] = 0x80
	return id
}
