package preview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// 계약(contracts/connector/)의 subprotocol이다. internal 구현의 상수와 일부러 분리해 wire 값 자체를 검증한다.
const previewSubprotocol = "labbit.connector-preview.v1"

const testCredential = "preview-test-connector-credential-4b71"

// fakeClock은 시간을 직접 움직이는 Clock이다. Advance가 도래한 timer를 호출한 goroutine에서 실행한다.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	at      time.Time
	f       func()
	stopped bool
	fired   bool
	c       *fakeClock
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{at: c.now.Add(d), f: f, c: c}
	c.timers = append(c.timers, t)
	return t
}

func (t *fakeTimer) Stop() bool {
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	was := !t.stopped && !t.fired
	t.stopped = true
	return was
}

// Advance는 시간을 d만큼 앞으로 보내고 도래한 timer를 실행한다.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var due []*fakeTimer
	for _, t := range c.timers {
		if !t.stopped && !t.fired && !t.at.After(c.now) {
			t.fired = true
			due = append(due, t)
		}
	}
	c.mu.Unlock()
	for _, t := range due {
		t.f()
	}
}

// fakeAuth는 Credential 문자열로 Connector identity를 정하는 ConnectorAuthenticator다.
type fakeAuth struct {
	mu      sync.Mutex
	known   map[string]ConnectorIdentity
	failure error
}

func (a *fakeAuth) AuthenticateConnector(_ context.Context, credential string) (ConnectorIdentity, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failure != nil {
		return ConnectorIdentity{}, a.failure
	}
	id, ok := a.known[credential]
	if !ok {
		return ConnectorIdentity{}, ErrUnauthenticated
	}
	return id, nil
}

// endedRecorder는 Lifecycle이 받은 종료 통지를 모은다.
type endedRecorder struct {
	mu    sync.Mutex
	ended []Ended
}

func (r *endedRecorder) SessionEnded(e Ended) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ended = append(r.ended, e)
}

func (r *endedRecorder) all() []Ended {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Ended(nil), r.ended...)
}

type fakeOpener struct {
	openFn func(ctx context.Context, sessionID string) error
}

func (f fakeOpener) OpenTunnel(ctx context.Context, sessionID string) error {
	if f.openFn != nil {
		return f.openFn(ctx, sessionID)
	}
	return errors.New("preview: fake opener openFn 미설정")
}

// lockedBuffer는 동시에 써도 되는 log 출력이다.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// workspaceApp은 Workspace VM의 application 대신 쓰는 실제 HTTP 서버다. 받은 요청의 header와 body를 기록한다.
type workspaceApp struct {
	*httptest.Server
	mu       sync.Mutex
	requests []recordedRequest
	handler  http.HandlerFunc
}

type recordedRequest struct {
	Method string
	Host   string
	URI    string
	Header http.Header
	Body   string
}

func newWorkspaceApp(t *testing.T) *workspaceApp {
	t.Helper()
	app := &workspaceApp{}
	app.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		// 기록한 뒤 handler도 본문을 읽을 수 있게 되돌린다.
		r.Body = io.NopCloser(bytes.NewReader(body))
		app.mu.Lock()
		app.requests = append(app.requests, recordedRequest{Method: r.Method, Host: r.Host, URI: r.RequestURI, Header: r.Header.Clone(), Body: string(body)})
		handler := app.handler
		app.mu.Unlock()
		if handler != nil {
			handler(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "hello from workspace")
	}))
	t.Cleanup(app.Server.Close)
	return app
}

func (a *workspaceApp) setHandler(h http.HandlerFunc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.handler = h
}

func (a *workspaceApp) all() []recordedRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]recordedRequest(nil), a.requests...)
}

func (a *workspaceApp) addr() string { return strings.TrimPrefix(a.URL, "http://") }

// env는 Gateway와 두 개의 HTTP 서버(Connector Data WSS, Preview Origin), 가짜 Workspace application을 묶은 test 환경이다.
type env struct {
	t        *testing.T
	gw       *Gateway
	clock    *fakeClock
	auth     *fakeAuth
	lifecyle *endedRecorder
	logs     *lockedBuffer
	app      *workspaceApp

	dataServer   *httptest.Server
	originServer *httptest.Server

	connectorID  uuid.UUID
	credentialID uuid.UUID
	controlID    uuid.UUID
}

type envOption func(*Options)

func newEnv(t *testing.T, mods ...envOption) *env {
	t.Helper()
	tmpl, err := ParseOriginTemplate("http://{sessionId}.preview.test", false)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{
		t: t, clock: newFakeClock(), lifecyle: &endedRecorder{}, logs: &lockedBuffer{}, app: newWorkspaceApp(t),
		connectorID: uuid.New(), credentialID: uuid.New(), controlID: uuid.New(),
	}
	e.auth = &fakeAuth{known: map[string]ConnectorIdentity{testCredential: {ConnectorID: e.connectorID, CredentialID: e.credentialID}}}
	opts := Options{
		Origin: tmpl, Connectors: e.auth, Clock: e.clock,
		Logger:        slog.New(slog.NewJSONHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		AttachTimeout: 2 * time.Second, UpstreamTimeout: 3 * time.Second,
	}
	for _, mod := range mods {
		mod(&opts)
	}
	e.gw, err = New(opts)
	if err != nil {
		t.Fatal(err)
	}
	e.gw.SetLifecycle(e.lifecyle)
	e.dataServer = httptest.NewServer(e.gw.DataHandler())
	e.originServer = httptest.NewServer(e.gw.Handler())
	t.Cleanup(func() {
		e.gw.Close()
		e.dataServer.Close()
		e.originServer.Close()
	})
	return e
}

// expected는 기본 PreviewSession correlation이다. session마다 ID를 새로 만든다.
func (e *env) expected(mods ...func(*Expected)) Expected {
	x := Expected{
		SessionID: uuid.NewString(), OwnerID: "user-1", OrganizationID: "org-1", ConnectorID: e.connectorID,
		LabInstanceID: "lab-1", Generation: 3, TargetVMKey: "vk-web", ProviderServerID: "srv-web-g3", TargetPort: 5173,
		TTL: time.Hour, RequestID: "request-1", OpenMessageID: "open-1",
	}
	for _, mod := range mods {
		mod(&x)
	}
	return x
}

// expect는 PreviewSession을 등록하고 현재 Control Session에 묶는다(PREVIEW_OPEN을 보낸 것과 같다).
func (e *env) expect(mods ...func(*Expected)) Expected {
	e.t.Helper()
	x := e.expected(mods...)
	if err := e.gw.Expect(x); err != nil {
		e.t.Fatalf("Expect() error = %v", err)
	}
	if err := e.gw.Bind(x.SessionID, Binding{ConnectorID: e.connectorID, ControlSessionID: e.controlID, CredentialID: e.credentialID}); err != nil {
		e.t.Fatalf("Bind() error = %v", err)
	}
	return x
}

func (e *env) dataURL() string { return "ws" + strings.TrimPrefix(e.dataServer.URL, "http") }

func (e *env) dialData(credential string) (*websocket.Conn, *http.Response, error) {
	dialer := websocket.Dialer{Subprotocols: []string{previewSubprotocol}, HandshakeTimeout: 5 * time.Second}
	header := http.Header{}
	if credential != "" {
		header.Set("Authorization", "Bearer "+credential)
	}
	return dialer.Dial(e.dataURL()+DataPath, header)
}

// attachFrame은 Connector가 보내는 preview-data.schema.json PREVIEW_ATTACH다. fields로 값을 덮어쓴다(nil이면 삭제).
func attachFrame(x Expected, fields map[string]any) map[string]any {
	openID := x.OpenMessageID
	if openID == "" {
		openID = "open-1"
	}
	msg := map[string]any{
		"type": "PREVIEW_ATTACH", "messageId": "attach-1", "sentAt": time.Now().UTC().Format(time.RFC3339),
		"replyToMessageId": openID,
		"previewSessionId": x.SessionID, "labInstanceId": x.LabInstanceID, "generation": x.Generation,
		"payload": map[string]any{"runtimeId": "runtime-1", "targetVmKey": x.TargetVMKey, "providerServerId": x.ProviderServerID, "targetPort": x.TargetPort},
	}
	for k, v := range fields {
		if v == nil {
			delete(msg, k)
		} else {
			msg[k] = v
		}
	}
	return msg
}

func payloadOf(msg map[string]any) map[string]any { return msg["payload"].(map[string]any) }

func sendJSON(t *testing.T, ws *websocket.Conn, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	_ = ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := ws.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}
}

// readCloseCode는 SaaS가 연결을 닫을 때까지 읽고 close code를 반환한다. close frame 없이 끊겼으면 -1이다.
func readCloseCode(ws *websocket.Conn) int {
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err := ws.ReadMessage(); err != nil {
			if ce, ok := err.(*websocket.CloseError); ok {
				return ce.Code
			}
			return -1
		}
	}
}

// connector는 attach한 가짜 Connector다. Workspace application으로의 TCP 연결과 Data WSS 사이에서 byte를 옮긴다.
type connector struct {
	ws   *websocket.Conn
	tcp  net.Conn
	done chan struct{}
	once sync.Once

	writeMu sync.Mutex
	// closed는 SaaS가 Data WSS를 닫았을 때 그 close code를 담는다(close frame 없이 끊겼으면 -1).
	closed chan int
}

// send는 Data WSS에 frame 하나를 쓴다. pump와 test가 같은 connection에 쓰므로 직렬화한다.
func (c *connector) send(kind int, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.ws.WriteMessage(kind, data)
}

// closeCode는 SaaS가 Data WSS를 닫을 때까지 기다리고 그 close code를 반환한다.
func (c *connector) closeCode(t *testing.T) int {
	t.Helper()
	select {
	case code := <-c.closed:
		return code
	case <-time.After(5 * time.Second):
		t.Fatal("Data WSS가 닫히지 않음")
		return 0
	}
}

// attachAndServe는 Data WSS를 열어 PREVIEW_ATTACH를 보내고 PREVIEW_ATTACHED를 받은 뒤 fake application으로 byte를 옮기기 시작한다.
func (e *env) attachAndServe(x Expected) *connector {
	e.t.Helper()
	ws, resp, err := e.dialData(testCredential)
	if err != nil {
		e.t.Fatalf("Data WSS 연결 실패: %v (response %v)", err, resp)
	}
	sendJSON(e.t, ws, attachFrame(x, nil))
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	kind, data, err := ws.ReadMessage()
	if err != nil || kind != websocket.TextMessage {
		e.t.Fatalf("PREVIEW_ATTACHED 수신 실패: %v", err)
	}
	var ack map[string]any
	if err := json.Unmarshal(data, &ack); err != nil || ack["type"] != "PREVIEW_ATTACHED" || ack["replyToMessageId"] != "attach-1" ||
		ack["previewSessionId"] != x.SessionID || ack["labInstanceId"] != x.LabInstanceID || ack["generation"] != float64(x.Generation) {
		e.t.Fatalf("PREVIEW_ATTACHED = %s", data)
	}
	_ = ws.SetReadDeadline(time.Time{})

	tcp, err := net.Dial("tcp", e.app.addr())
	if err != nil {
		e.t.Fatalf("fake application 연결 실패: %v", err)
	}
	c := &connector{ws: ws, tcp: tcp, done: make(chan struct{}), closed: make(chan int, 1)}
	go c.pump()
	e.t.Cleanup(c.stop)
	return c
}

// pump는 SaaS가 보낸 Binary frame을 application으로, application이 보낸 byte를 Binary frame으로 옮긴다.
func (c *connector) pump() {
	defer close(c.done)
	errc := make(chan struct{}, 2)
	go func() {
		defer func() { errc <- struct{}{} }()
		for {
			kind, data, err := c.ws.ReadMessage()
			if err != nil {
				code := -1
				var ce *websocket.CloseError
				if errors.As(err, &ce) {
					code = ce.Code
				}
				select {
				case c.closed <- code:
				default:
				}
				_ = c.tcp.Close()
				return
			}
			if kind == websocket.BinaryMessage {
				if _, err := c.tcp.Write(data); err != nil {
					return
				}
			}
		}
	}()
	go func() {
		defer func() { errc <- struct{}{} }()
		buf := make([]byte, 16<<10)
		for {
			n, err := c.tcp.Read(buf)
			if n > 0 {
				if werr := c.send(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				// application이 TCP 연결을 닫았다. 남은 byte를 모두 보낸 뒤 정상 종료한다.
				_ = c.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "tcp closed"), time.Now().Add(time.Second))
				return
			}
		}
	}()
	<-errc
}

func (c *connector) stop() {
	c.once.Do(func() {
		_ = c.ws.Close()
		_ = c.tcp.Close()
	})
	<-c.done
}

// activeSession은 PreviewSession을 만들고 attach해 활성화한 뒤 Browser가 받을 bootstrap credential을 돌려준다.
type activeSession struct {
	Expected
	Credential string
	Peer       *connector
}

func (e *env) activate(mods ...func(*Expected)) activeSession {
	e.t.Helper()
	x := e.expect(mods...)
	peer := e.attachAndServe(x)
	attached, _, ok := e.gw.Pending(x.SessionID)
	if !ok {
		e.t.Fatal("Pending()이 PreviewSession을 찾지 못함")
	}
	select {
	case <-attached:
	case <-time.After(5 * time.Second):
		e.t.Fatal("attach가 완료되지 않음")
	}
	act, err := e.gw.Activate(x.SessionID)
	if err != nil {
		e.t.Fatalf("Activate() error = %v", err)
	}
	_, credential, found := strings.Cut(act.URL, "#")
	if !found || credential == "" {
		e.t.Fatalf("URL = %q, fragment에 credential이 있어야 함", act.URL)
	}
	return activeSession{Expected: x, Credential: credential, Peer: peer}
}

func (e *env) host(x Expected) string { return x.SessionID + ".preview.test" }

// origin은 PreviewSession의 Preview Origin이다(Origin header 값).
func (e *env) origin(x Expected) string { return e.gw.Origin().Origin(x.SessionID) }

// do는 Preview Origin에 요청한다. Host는 PreviewSession의 Origin host로 덮어쓴다.
func (e *env) do(x Expected, method, path string, body string, mods ...func(*http.Request)) (*http.Response, string) {
	e.t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, e.originServer.URL+path, reader)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Host = e.host(x)
	for _, mod := range mods {
		mod(req)
	}
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s 실패: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, string(data)
}

// exchange는 bootstrap credential을 교환해 Preview Cookie를 받는다.
func (e *env) exchange(x Expected, credential string) (*http.Response, string) {
	e.t.Helper()
	body, _ := json.Marshal(map[string]string{"credential": credential})
	return e.do(x, http.MethodPost, ExchangePath, string(body), func(r *http.Request) {
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", e.origin(x))
	})
}

// login은 credential을 교환하고 Preview Cookie를 돌려준다.
func (e *env) login(x activeSession) *http.Cookie {
	e.t.Helper()
	resp, body := e.exchange(x.Expected, x.Credential)
	if resp.StatusCode != http.StatusNoContent {
		e.t.Fatalf("exchange status = %d, body = %s", resp.StatusCode, body)
	}
	for _, c := range resp.Cookies() {
		if c.Name == e.gw.cookieName() {
			return c
		}
	}
	e.t.Fatalf("Preview Cookie가 없음: %v", resp.Header["Set-Cookie"])
	return nil
}

// get은 Preview Cookie로 인증한 GET이다.
func (e *env) get(x Expected, cookie *http.Cookie, path string, mods ...func(*http.Request)) (*http.Response, string) {
	e.t.Helper()
	return e.do(x, http.MethodGet, path, "", append([]func(*http.Request){func(r *http.Request) {
		if cookie != nil {
			r.AddCookie(cookie)
		}
	}}, mods...)...)
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("시간 안에 %s 조건이 만족되지 않음", what)
}
