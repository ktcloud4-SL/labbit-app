//go:build integration

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"

	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connectorwss"
	"github.com/ktcloud4-SL/labbit-app/internal/server/preview"
	"github.com/ktcloud4-SL/labbit-app/internal/server/preview/previewtest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/previewsession"
	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime/realtimetest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal/terminaltest"
)

// 이 test들은 실제 PostgreSQL, 실제 Connector Control WSS와 Preview Data WSS, 실제 HTTP stack(host 기반 Preview Origin routing 포함)을 조립한다.
// Connector 쪽은 계약대로 동작하는 contract peer(previewtest)이고 Workspace VM의 application은 실제 TCP로 연결되는 fake HTTP 서버다.
// 실제 OpenStack, SSH TCP forwarding, VM은 없다(LBT-23, LBT-24, LBT-25 C3). 이 test의 통과를 실제 VM Preview E2E 통과로 보지 않는다.

const (
	previewPublicOrigin = "http://labbit.example.com"
	previewOriginSuffix = ".preview.example.com"
)

// 이 값들이 DB, log, Control frame에 나타나면 안 된다. 실제 사용자 데이터가 아니다.
const (
	previewPathMarker   = "preview-path-marker-3b7e"
	previewQueryMarker  = "preview-query-marker-91c4"
	previewHeaderMarker = "preview-header-marker-a05d"
	previewBodyMarker   = "preview-body-marker-6f12"
	previewAppCookie    = "preview-app-cookie-marker-c8e0"
	previewRespMarker   = "preview-response-marker-27ba"
)

// previewClock은 realtimetest.FakeClock을 preview.Clock으로 쓰는 adapter다(Timer 타입만 다르다).
type previewClock struct{ *realtimetest.FakeClock }

func (c previewClock) AfterFunc(d time.Duration, f func()) preview.Timer {
	return c.FakeClock.AfterFunc(d, f)
}

// workspaceApp은 Workspace VM의 application 대신 쓰는 실제 HTTP 서버다.
type workspaceApp struct {
	*httptest.Server
	mu       sync.Mutex
	requests []workspaceRequest
	handler  http.HandlerFunc
}

type workspaceRequest struct {
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
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		app.mu.Lock()
		app.requests = append(app.requests, workspaceRequest{Method: r.Method, Host: r.Host, URI: r.RequestURI, Header: r.Header.Clone(), Body: string(body)})
		handler := app.handler
		app.mu.Unlock()
		if handler != nil {
			handler(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprintf(w, "workspace says hello %s", previewRespMarker)
	}))
	t.Cleanup(app.Server.Close)
	return app
}

func (a *workspaceApp) addr() string { return strings.TrimPrefix(a.URL, "http://") }

func (a *workspaceApp) setHandler(h http.HandlerFunc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.handler = h
}

func (a *workspaceApp) all() []workspaceRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]workspaceRequest(nil), a.requests...)
}

// previewEnv는 실제 PostgreSQL과 contract peer로 Preview 전체를 조립한 환경이다.
type previewEnv struct {
	t       *testing.T
	dsn     string
	conn    *pgx.Conn
	fixture *terminaltest.Fixture
	stack   *controlStack
	server  *httptest.Server
	logs    *lockedBuffer
	clock   *realtimetest.FakeClock
	app     *workspaceApp
	peer    *previewtest.Peer

	ownerCookie, instructorCookie, peerCookie, outsiderCookie, foreignCookie string

	mu       sync.Mutex
	behavior func(previewtest.Open) previewtest.Behavior
}

type previewEnvOptions struct {
	// capabilities는 Connector peer가 HELLO에서 선언하는 값이다. nil이면 capabilities field가 없다(preview-v1을 모르는 Connector).
	capabilities []string
	// noPeer이면 Connector를 연결하지 않는다.
	noPeer bool
	// fakeClock이면 PreviewSession 시간(TTL, bootstrap, open timeout)이 가짜 시계를 쓴다.
	fakeClock bool
	// allowedPorts는 Backend 허용 port 목록이다. 비어 있으면 "3000,5173,80"이다.
	allowedPorts string
	mods         []func(*stackOptions)
}

var previewV1 = []string{"preview-v1"}

func newPreviewEnv(t *testing.T, o previewEnvOptions) *previewEnv {
	t.Helper()
	dsn := postgrestest.NewDatabase(t)
	postgrestest.Migrate(t, dsn, loadEmbeddedMigrations(t))
	conn := postgrestest.Connect(t, dsn)

	e := &previewEnv{t: t, dsn: dsn, conn: conn, fixture: terminaltest.New(t, conn), logs: &lockedBuffer{}, app: newWorkspaceApp(t)}
	e.ownerCookie = terminaltest.LoginSession(t, conn, e.fixture.OwnerID)
	e.instructorCookie = terminaltest.LoginSession(t, conn, e.fixture.InstructorID)
	e.peerCookie = terminaltest.LoginSession(t, conn, e.fixture.PeerID)
	e.outsiderCookie = terminaltest.LoginSession(t, conn, e.fixture.OutsiderID)
	e.foreignCookie = terminaltest.LoginSession(t, conn, e.fixture.ForeignID)

	pool, err := postgres.OpenPool(t.Context(), dsn)
	if err != nil {
		t.Fatalf("OpenPool() error = %v", err)
	}
	t.Cleanup(pool.Close)

	ports := o.allowedPorts
	if ports == "" {
		ports = "3000,5173,80"
	}
	policy, err := previewsession.ParseAllowedPorts(ports)
	if err != nil {
		t.Fatal(err)
	}
	origin, err := preview.ParseOriginTemplate("http://{sessionId}"+previewOriginSuffix, false)
	if err != nil {
		t.Fatal(err)
	}
	opts := stackOptions{
		Logger:       slog.New(slog.NewJSONHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		PublicOrigin: previewPublicOrigin,

		Preview: true, PreviewPolicy: policy, PreviewTTL: time.Hour, PreviewOrigin: origin,
		PreviewOpenTimeout: 3 * time.Second, PreviewAttachTimeout: 2 * time.Second, PreviewUpstreamTimeout: 5 * time.Second,
		PreviewBootstrapTTL: 2 * time.Minute,
	}
	if o.fakeClock {
		e.clock = realtimetest.NewFakeClock(time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC))
		opts.PreviewClock = previewClock{e.clock}
		// 가짜 시계에서는 열기 시간 초과가 실제 시간으로 동작하지 않으므로 충분히 길게 둔다.
		opts.PreviewOpenTimeout = time.Hour
	}
	for _, mod := range o.mods {
		mod(&opts)
	}
	e.stack, err = newControlStack(postgres.NewStore(pool), opts)
	if err != nil {
		t.Fatalf("newControlStack() error = %v", err)
	}

	// 실제 application listener와 같은 handler(host 기반 Preview Origin routing 포함)를 쓴다.
	e.server = httptest.NewServer(applicationHandler(e.stack.routes()))
	t.Cleanup(func() {
		e.stack.close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := e.stack.shutdown(ctx); err != nil {
			t.Errorf("shutdown error = %v", err)
		}
		e.server.Close()
	})

	if !o.noPeer {
		e.peer = e.startPeer(o.capabilities)
		e.waitConnectorReady()
	}
	return e
}

func (e *previewEnv) wsBase() string { return "ws" + strings.TrimPrefix(e.server.URL, "http") }

func (e *previewEnv) startPeer(capabilities []string) *previewtest.Peer {
	e.t.Helper()
	return previewtest.Start(e.t, previewtest.Config{
		ControlURL: e.wsBase() + connectorwss.Path, DataURL: e.wsBase() + preview.DataPath,
		Credential: terminaltest.Credential, Capabilities: capabilities, WorkspaceAddr: e.app.addr(),
		Behavior: func(open previewtest.Open) previewtest.Behavior {
			e.mu.Lock()
			fn := e.behavior
			e.mu.Unlock()
			if fn == nil {
				return previewtest.Behavior{}
			}
			return fn(open)
		},
	})
}

func (e *previewEnv) setBehavior(fn func(previewtest.Open) previewtest.Behavior) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.behavior = fn
}

func (e *previewEnv) waitConnectorReady() {
	e.t.Helper()
	eventually(e.t, "Connector protocol-ready", 10*time.Second, func() bool {
		return e.stack.Registry.WithReadyRoute(e.fixture.ConnectorID, func(connector.Session, connector.Route) error { return nil }) == nil
	})
}

// request는 SaaS 본 서비스 Origin(LABBIT_PUBLIC_ORIGIN)에서 온 것처럼 API를 호출한다.
func (e *previewEnv) request(method, path, cookie, body string, headers map[string]string) apiResponse {
	e.t.Helper()
	req, err := http.NewRequest(method, e.server.URL+path, strings.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Origin", previewPublicOrigin)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: realtime.SessionCookieName, Value: cookie})
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
	if err != nil {
		return apiResponse{Status: -1, Body: []byte(err.Error())}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return apiResponse{Status: resp.StatusCode, Header: resp.Header, Body: data}
}

func (e *previewEnv) createPath(labInstanceID uuid.UUID) string {
	return "/api/v1/lab-instances/" + labInstanceID.String() + "/preview-sessions"
}

// create는 PreviewSession 생성을 요청한다.
func (e *previewEnv) create(cookie string, labInstanceID uuid.UUID, port int) apiResponse {
	e.t.Helper()
	return e.request(http.MethodPost, e.createPath(labInstanceID), cookie, fmt.Sprintf(`{"targetPort":%d}`, port), nil)
}

// session은 성공한 생성 응답이다.
type session struct {
	ID         string
	TargetPort int
	PreviewURL string
	ExpiresAt  time.Time
	// Credential은 previewUrl의 fragment에 있던 일회용 bootstrap credential이다.
	Credential string
	Host       string
}

func (e *previewEnv) mustCreate(cookie string, labInstanceID uuid.UUID, port int) session {
	e.t.Helper()
	resp := e.create(cookie, labInstanceID, port)
	if resp.Status != http.StatusCreated {
		e.t.Fatalf("create status = %d: %s", resp.Status, resp.Body)
	}
	return parseSession(e.t, resp)
}

func parseSession(t *testing.T, resp apiResponse) session {
	t.Helper()
	var body struct {
		ID         string    `json:"id"`
		TargetPort int       `json:"targetPort"`
		PreviewURL string    `json:"previewUrl"`
		ExpiresAt  time.Time `json:"expiresAt"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		t.Fatalf("응답 해석 실패: %v: %s", err, resp.Body)
	}
	u, err := url.Parse(body.PreviewURL)
	if err != nil {
		t.Fatalf("previewUrl 해석 실패: %v", err)
	}
	return session{ID: body.ID, TargetPort: body.TargetPort, PreviewURL: body.PreviewURL, ExpiresAt: body.ExpiresAt, Credential: u.Fragment, Host: u.Host}
}

// previewRequest는 Preview Origin(별도 host)으로 요청한다. 같은 서버가 request Host로 Preview Gateway와 본 서비스를 가른다.
func (e *previewEnv) previewRequest(s session, method, path, body string, mods ...func(*http.Request)) *http.Response {
	e.t.Helper()
	req, err := http.NewRequest(method, e.server.URL+path, strings.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Host = s.Host
	for _, mod := range mods {
		mod(req)
	}
	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s 실패: %v", method, path, err)
	}
	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return string(data)
}

// exchange는 Browser JavaScript처럼 같은 Preview Origin에서 bootstrap credential을 교환하고 Preview Cookie를 돌려준다.
func (e *previewEnv) exchange(s session) (*http.Response, *http.Cookie) {
	e.t.Helper()
	body, _ := json.Marshal(map[string]string{"credential": s.Credential})
	resp := e.previewRequest(s, http.MethodPost, preview.ExchangePath, string(body), func(r *http.Request) {
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://"+s.Host)
	})
	defer resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name == "labbit-preview" {
			return resp, c
		}
	}
	return resp, nil
}

func (e *previewEnv) login(s session) *http.Cookie {
	e.t.Helper()
	resp, cookie := e.exchange(s)
	if resp.StatusCode != http.StatusNoContent || cookie == nil {
		e.t.Fatalf("exchange status = %d, cookie = %v", resp.StatusCode, cookie)
	}
	return cookie
}

func (e *previewEnv) get(s session, cookie *http.Cookie, path string, mods ...func(*http.Request)) (*http.Response, string) {
	e.t.Helper()
	resp := e.previewRequest(s, http.MethodGet, path, "", append([]func(*http.Request){func(r *http.Request) {
		if cookie != nil {
			r.AddCookie(cookie)
		}
	}}, mods...)...)
	return resp, readBody(e.t, resp)
}

// tableCounts는 모든 table의 row 수다. PreviewSession이 PostgreSQL에 아무것도 저장하지 않음을 보이는 데 쓴다.
func (e *previewEnv) tableCounts() map[string]int {
	e.t.Helper()
	rows, err := e.conn.Query(e.t.Context(), `SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' AND table_type = 'BASE TABLE'`)
	if err != nil {
		e.t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			e.t.Fatal(err)
		}
		names = append(names, name)
	}
	rows.Close()
	counts := make(map[string]int, len(names))
	for _, name := range names {
		counts[name] = terminaltest.Count(e.t, e.conn, name)
	}
	return counts
}

// assertNoPreviewSideEffects는 거절된 요청이 Connector Control/Data와 Gateway에 어떤 side effect도 만들지 않았음을 확인한다.
func (e *previewEnv) assertNoPreviewSideEffects(what string) {
	e.t.Helper()
	if e.peer != nil {
		if opens := e.peer.Opens(); len(opens) != 0 {
			e.t.Fatalf("%s: 거절된 요청이 Connector에 PREVIEW_OPEN을 보냄: %+v", what, opens)
		}
		if e.peer.DataDials() != 0 {
			e.t.Fatalf("%s: 거절된 요청이 Preview Data WSS를 만듦", what)
		}
	}
	if n := e.stack.Router.PendingPreviewOpens(e.fixture.ConnectorID); n != 0 {
		e.t.Fatalf("%s: PREVIEW_OPEN pending %d개가 남음", what, n)
	}
	for _, lab := range []uuid.UUID{e.fixture.LabInstanceID, e.fixture.PeerLabInstanceID} {
		if ids := e.stack.PreviewGateway.SessionsForLab(lab.String()); len(ids) != 0 {
			e.t.Fatalf("%s: Gateway에 PreviewSession이 남음: %v", what, ids)
		}
	}
}

func (e *previewEnv) problemCode(resp apiResponse) string {
	var p struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(resp.Body, &p)
	return p.Code
}

// ---- 전체 경로 ----

// Browser → POST PreviewSession → 권한·허용 port 승인 → Workspace VM 결정 → PREVIEW_OPEN → Connector의 Preview Data WSS attach → 활성 →
// Browser bootstrap 인증 → Gateway HTTP proxy → fake Workspace 응답 → Browser 응답.
func TestPreviewSessionFullPathThroughContractPeer(t *testing.T) {
	e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1})
	before := e.tableCounts()

	resp := e.create(e.ownerCookie, e.fixture.LabInstanceID, 5173)
	if resp.Status != http.StatusCreated {
		t.Fatalf("create status = %d: %s", resp.Status, resp.Body)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	s := parseSession(t, resp)
	if _, err := uuid.Parse(s.ID); err != nil || s.TargetPort != 5173 || s.Credential == "" {
		t.Fatalf("session = %+v", s)
	}

	// 응답과 previewUrl에는 VM IP/port, Provider Server ID, Connector ID가 없다. Preview Origin은 SaaS 본 서비스 Origin과 다른 host다.
	for _, hidden := range []string{
		e.fixture.Servers["workspace"].ProviderID, e.fixture.ConnectorID.String(), e.fixture.ProviderConnectionID.String(),
		terminaltest.SnapshotPrivateIPMarker, terminaltest.SnapshotImageIDMarker, terminaltest.SnapshotProviderConnMark,
	} {
		if strings.Contains(string(resp.Body), hidden) {
			t.Errorf("응답이 %q를 노출함: %s", hidden, resp.Body)
		}
	}
	mainHost := strings.TrimPrefix(previewPublicOrigin, "http://")
	if s.Host == mainHost || !strings.HasSuffix(s.Host, previewOriginSuffix) || !strings.HasPrefix(s.Host, s.ID+".") {
		t.Fatalf("Preview Origin host = %q, SaaS 본 서비스 host = %q", s.Host, mainHost)
	}
	if !strings.HasSuffix(strings.SplitN(s.PreviewURL, "#", 2)[0], preview.BootstrapPath) {
		t.Fatalf("previewUrl = %q, want Preview Origin의 bootstrap 경로", s.PreviewURL)
	}

	// Connector는 서버가 결정한 target과 승인한 정확한 port만 받는다.
	opens := e.peer.Opens()
	if len(opens) != 1 {
		t.Fatalf("PREVIEW_OPEN %d개, want 1개", len(opens))
	}
	open := opens[0]
	if open.PreviewSessionID != s.ID || open.LabInstanceID != e.fixture.LabInstanceID.String() || open.Generation != 1 ||
		open.TargetVMKey != "workspace" || open.ProviderServerID != e.fixture.Servers["workspace"].ProviderID || open.TargetPort != 5173 {
		t.Fatalf("PREVIEW_OPEN = %+v", open)
	}
	if open.RequestID == "" {
		t.Fatal("PREVIEW_OPEN에 요청 correlation이 없음")
	}

	// Browser: bootstrap 페이지 → credential 교환 → Preview Cookie → Workspace application.
	page, body := e.get(s, nil, preview.BootstrapPath)
	if page.StatusCode != http.StatusOK || !strings.Contains(body, "location.hash") || strings.Contains(body, s.Credential) {
		t.Fatalf("bootstrap = %d %s", page.StatusCode, body)
	}
	xresp, cookie := e.exchange(s)
	if xresp.StatusCode != http.StatusNoContent || cookie == nil || !cookie.HttpOnly || cookie.Domain != "" || cookie.Path != "/" {
		t.Fatalf("exchange = %d %+v", xresp.StatusCode, cookie)
	}
	if again, c := e.exchange(s); again.StatusCode != http.StatusUnauthorized || c != nil {
		t.Fatalf("bootstrap credential 재사용 status = %d", again.StatusCode)
	}

	e.app.setHandler(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/missing":
			http.Error(w, "app 404 "+previewRespMarker, http.StatusNotFound)
		case "/boom":
			http.Error(w, "app 500", http.StatusInternalServerError)
		default:
			w.Header().Set("X-App", "yes")
			_, _ = fmt.Fprintf(w, "%s %s %s body=%s", r.Method, r.URL.RequestURI(), previewRespMarker, body)
		}
	})
	// 사용자 코드가 쓰는 경로·query·header·Cookie·본문 marker를 실어 보낸다. 로그인 Session Cookie와 Authorization도 함께 보낸다(전달되면 안 되는 것은 전달되지 않는다).
	resp2 := e.previewRequest(s, http.MethodPost, "/"+previewPathMarker+"?q="+previewQueryMarker, previewBodyMarker, func(r *http.Request) {
		r.AddCookie(cookie)
		r.AddCookie(&http.Cookie{Name: "app", Value: previewAppCookie})
		r.AddCookie(&http.Cookie{Name: realtime.SessionCookieName, Value: e.ownerCookie})
		r.Header.Set("X-Custom", previewHeaderMarker)
		r.Header.Set("Content-Type", "text/plain")
	})
	got := readBody(t, resp2)
	if resp2.StatusCode != http.StatusOK || resp2.Header.Get("X-App") != "yes" ||
		got != fmt.Sprintf("POST /%s?q=%s %s body=%s", previewPathMarker, previewQueryMarker, previewRespMarker, previewBodyMarker) {
		t.Fatalf("POST = %d %q", resp2.StatusCode, got)
	}
	if r404, b := e.get(s, cookie, "/missing"); r404.StatusCode != http.StatusNotFound || !strings.Contains(b, "app 404") {
		t.Fatalf("app 404 = %d %q", r404.StatusCode, b)
	}
	if r500, b := e.get(s, cookie, "/boom"); r500.StatusCode != http.StatusInternalServerError || !strings.Contains(b, "app 500") {
		t.Fatalf("app 500 = %d %q", r500.StatusCode, b)
	}

	// Workspace application은 Labbit credential을 받지 못한다. Connector가 받은 byte 전체(= application이 받은 HTTP 요청)로 확인한다.
	toApp := string(e.peer.ReceivedFromSaaS())
	for _, secret := range []string{cookie.Value, s.Credential, e.ownerCookie, "labbit-preview", realtime.SessionCookieName} {
		if strings.Contains(toApp, secret) {
			t.Errorf("Workspace application으로 가는 byte에 Labbit credential(%q)이 있음", secret)
		}
	}
	// 같은 byte에는 사용자의 요청 내용이 있다(Preview 본문은 Data tunnel에만 존재한다).
	for _, want := range []string{previewPathMarker, previewBodyMarker, previewHeaderMarker, previewAppCookie} {
		if !strings.Contains(toApp, want) {
			t.Errorf("Data tunnel로 가는 byte에 %q가 없음", want)
		}
	}
	for _, r := range e.app.all() {
		if strings.Contains(fmt.Sprint(r.Header), cookie.Value) || strings.Contains(fmt.Sprint(r.Header), e.ownerCookie) {
			t.Errorf("fake Workspace가 받은 header에 Labbit credential이 있음: %v", r.Header)
		}
	}

	// persistent Control, PostgreSQL, log에는 Preview 본문·경로·query·Cookie가 없다.
	markers := []string{previewPathMarker, previewQueryMarker, previewHeaderMarker, previewBodyMarker, previewAppCookie, previewRespMarker, cookie.Value, s.Credential}
	var control strings.Builder
	for _, f := range e.peer.ControlFrames() {
		control.Write(f)
	}
	logs := e.logs.String()
	for _, marker := range markers {
		if strings.Contains(control.String(), marker) {
			t.Errorf("Control frame에 %q가 있음", marker)
		}
		if strings.Contains(logs, marker) {
			t.Errorf("log에 %q가 있음", marker)
		}
	}
	after := e.tableCounts()
	for table, n := range before {
		if after[table] != n {
			t.Errorf("table %s row 수가 %d에서 %d로 바뀜: PreviewSession은 PostgreSQL에 저장하지 않는다", table, n, after[table])
		}
	}
	if len(after) != len(before) {
		t.Errorf("table 수가 바뀜: %d → %d", len(before), len(after))
	}
}

// 같은 LabInstance에서 두 port의 PreviewSession은 서로 다른 tunnel이며 서로 섞이지 않는다. 하나를 닫아도 다른 것은 유지된다.
func TestConcurrentPreviewSessionsAreIndependent(t *testing.T) {
	e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1})
	other := newWorkspaceApp(t)
	other.setHandler(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "second app") })
	e.setBehavior(func(open previewtest.Open) previewtest.Behavior {
		if open.TargetPort == 3000 {
			return previewtest.Behavior{WorkspaceAddr: other.addr()}
		}
		return previewtest.Behavior{}
	})

	a := e.mustCreate(e.ownerCookie, e.fixture.LabInstanceID, 5173)
	b := e.mustCreate(e.ownerCookie, e.fixture.LabInstanceID, 3000)
	if a.ID == b.ID || a.Host == b.Host {
		t.Fatalf("두 PreviewSession이 같은 ID/host를 가짐: %+v %+v", a, b)
	}
	ca, cb := e.login(a), e.login(b)
	if _, body := e.get(a, ca, "/"); !strings.Contains(body, "workspace says hello") {
		t.Fatalf("A = %q", body)
	}
	if _, body := e.get(b, cb, "/"); body != "second app" {
		t.Fatalf("B = %q", body)
	}
	// A의 Cookie는 B의 Origin에서 인증이 아니다.
	if resp, _ := e.get(b, ca, "/"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("다른 PreviewSession의 Cookie status = %d, want 401", resp.StatusCode)
	}

	if resp := e.request(http.MethodDelete, "/api/v1/preview-sessions/"+a.ID, e.ownerCookie, "", nil); resp.Status != http.StatusNoContent {
		t.Fatalf("DELETE A status = %d: %s", resp.Status, resp.Body)
	}
	if resp, _ := e.get(a, ca, "/"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("종료한 A status = %d, want 401", resp.StatusCode)
	}
	if _, body := e.get(b, cb, "/"); body != "second app" {
		t.Fatalf("A를 닫은 뒤 B = %q", body)
	}
}

// ---- 권한 ----

// 거절된 요청은 어떤 Control/Data side effect도 만들지 않는다.
func TestPreviewCreateRejectsEveryAuthorizationFailureWithoutSideEffects(t *testing.T) {
	e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1})
	lab := e.fixture.LabInstanceID

	cases := []struct {
		name   string
		cookie string
		lab    uuid.UUID
		want   int
		code   string
	}{
		{"로그인 안 함", "", lab, http.StatusUnauthorized, "unauthenticated"},
		{"다른 사용자(같은 Class의 학생)", e.peerCookie, lab, http.StatusForbidden, "forbidden"},
		{"강사도 다른 사용자의 LabInstance는 안 됨", e.instructorCookie, lab, http.StatusForbidden, "forbidden"},
		{"다른 Class의 사용자", e.outsiderCookie, lab, http.StatusForbidden, "forbidden"},
		{"다른 Organization의 사용자", e.foreignCookie, lab, http.StatusForbidden, "forbidden"},
		{"존재하지 않는 LabInstance", e.ownerCookie, uuid.New(), http.StatusNotFound, "not_found"},
	}
	for _, tc := range cases {
		resp := e.create(tc.cookie, tc.lab, 5173)
		if resp.Status != tc.want || e.problemCode(resp) != tc.code {
			t.Errorf("%s: status = %d code = %q, want %d %q: %s", tc.name, resp.Status, e.problemCode(resp), tc.want, tc.code, resp.Body)
		}
		e.assertNoPreviewSideEffects(tc.name)
	}

	// 형식이 틀린 ID와 출처 검증.
	if resp := e.request(http.MethodPost, "/api/v1/lab-instances/not-a-uuid/preview-sessions", e.ownerCookie, `{"targetPort":5173}`, nil); resp.Status != http.StatusNotFound {
		t.Errorf("잘못된 ID status = %d", resp.Status)
	}
	if resp := e.request(http.MethodPost, e.createPath(lab), e.ownerCookie, `{"targetPort":5173}`, map[string]string{"Origin": "https://evil.test"}); resp.Status != http.StatusForbidden || e.problemCode(resp) != "csrf_rejected" {
		t.Errorf("foreign Origin status = %d %s", resp.Status, resp.Body)
	}
	e.assertNoPreviewSideEffects("형식/출처")
}

func TestPreviewCreateRequiresTheCurrentClassMembership(t *testing.T) {
	e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1})
	terminaltest.Exec(t, e.conn, `DELETE FROM class_memberships WHERE class_id = $1 AND user_id = $2`, e.fixture.ClassID, e.fixture.OwnerID)
	resp := e.create(e.ownerCookie, e.fixture.LabInstanceID, 5173)
	if resp.Status != http.StatusForbidden || e.problemCode(resp) != "forbidden" {
		t.Fatalf("status = %d %s, want 403 forbidden", resp.Status, resp.Body)
	}
	e.assertNoPreviewSideEffects("Membership 없음")
}

func TestPreviewCreateRequiresAReadyLabInstance(t *testing.T) {
	for _, status := range []string{"PENDING", "PROVISIONING", "RESETTING", "FAILED"} {
		t.Run(status, func(t *testing.T) {
			e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1})
			terminaltest.Exec(t, e.conn, `UPDATE lab_instances SET status = $2 WHERE id = $1`, e.fixture.LabInstanceID, status)
			resp := e.create(e.ownerCookie, e.fixture.LabInstanceID, 5173)
			if resp.Status != http.StatusConflict || e.problemCode(resp) != "lab_instance_not_ready" {
				t.Fatalf("status = %d %s, want 409 lab_instance_not_ready", resp.Status, resp.Body)
			}
			e.assertNoPreviewSideEffects(status)
		})
	}
}

// Workspace VM은 immutable CreationSnapshot과 현재 generation의 PRESENT SERVER ProviderResource로 정한다. 모순이면 fail closed다.
func TestPreviewCreateResolvesTheWorkspaceVMFailClosed(t *testing.T) {
	t.Run("현재 generation에 Workspace SERVER가 없음", func(t *testing.T) {
		e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1})
		// Reset이 generation을 올렸지만 새 SERVER는 아직 없다.
		terminaltest.Exec(t, e.conn, `UPDATE lab_instances SET generation = 2 WHERE id = $1`, e.fixture.LabInstanceID)
		resp := e.create(e.ownerCookie, e.fixture.LabInstanceID, 5173)
		if resp.Status != http.StatusConflict || e.problemCode(resp) != "workspace_target_unavailable" {
			t.Fatalf("status = %d %s", resp.Status, resp.Body)
		}
		e.assertNoPreviewSideEffects("generation 2에 SERVER 없음")
	})
	t.Run("Workspace SERVER가 PRESENT가 아님", func(t *testing.T) {
		e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1})
		terminaltest.Exec(t, e.conn, `UPDATE provider_resources SET lifecycle_status = 'DELETING' WHERE id = $1`, e.fixture.Servers["workspace"].ResourceID)
		resp := e.create(e.ownerCookie, e.fixture.LabInstanceID, 5173)
		if resp.Status != http.StatusConflict || e.problemCode(resp) != "workspace_target_unavailable" {
			t.Fatalf("status = %d %s", resp.Status, resp.Body)
		}
		e.assertNoPreviewSideEffects("PRESENT 아님")
	})
	t.Run("PRESENT Workspace SERVER가 둘", func(t *testing.T) {
		e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1})
		e.fixture.AddResource(t, e.conn, e.fixture.LabInstanceID, 1, "SERVER", "workspace", "PRESENT")
		resp := e.create(e.ownerCookie, e.fixture.LabInstanceID, 5173)
		if resp.Status != http.StatusInternalServerError || e.problemCode(resp) != "internal_error" {
			t.Fatalf("status = %d %s", resp.Status, resp.Body)
		}
		e.assertNoPreviewSideEffects("PRESENT 둘")
	})
	t.Run("다른 VM의 Server는 Workspace가 아님", func(t *testing.T) {
		e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1})
		// workspace SERVER를 없애고 db SERVER만 남겨도 db를 Workspace로 쓰지 않는다.
		terminaltest.Exec(t, e.conn, `UPDATE provider_resources SET lifecycle_status = 'DELETED' WHERE id = $1`, e.fixture.Servers["workspace"].ResourceID)
		resp := e.create(e.ownerCookie, e.fixture.LabInstanceID, 5173)
		if resp.Status != http.StatusConflict {
			t.Fatalf("status = %d %s", resp.Status, resp.Body)
		}
		e.assertNoPreviewSideEffects("db만 PRESENT")
	})
	// 저장된 CreationSnapshot이 모순이면 값을 만들어 채우지 않는다.
	for name, snapshot := range map[string]string{
		"workspaceVmKey가 vms에 없음": terminaltest.Snapshot("ghost-vm", terminaltest.DefaultVMs()),
		"workspaceVmKey가 비어 있음":   terminaltest.Snapshot("", terminaltest.DefaultVMs()),
		"workspaceVmKey가 중복":      terminaltest.Snapshot("workspace", append(terminaltest.DefaultVMs(), terminaltest.VM{Key: "workspace", Role: "workspace", Index: 1})),
		"vms가 비어 있음":              terminaltest.Snapshot("workspace", nil),
	} {
		t.Run(name, func(t *testing.T) {
			e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1})
			lab := e.fixture.AddLabInstanceWithSnapshot(t, e.conn, snapshot)
			e.fixture.AddResource(t, e.conn, lab, 1, "SERVER", "workspace", "PRESENT")
			resp := e.create(e.ownerCookie, lab, 5173)
			if resp.Status != http.StatusInternalServerError || e.problemCode(resp) != "internal_error" {
				t.Fatalf("status = %d %s", resp.Status, resp.Body)
			}
			e.assertNoPreviewSideEffects(name)
		})
	}
}

// ---- 허용 port ----

func TestPreviewCreateApprovesOnlyThePortsTheBackendListed(t *testing.T) {
	e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1, allowedPorts: "3000,80,5000,22"})

	// 3000, 80, 5000은 목록에 있어서 승인한다. 어느 것도 코드의 기본값이나 숫자 범위가 아니다.
	for _, port := range []int{3000, 80, 5000} {
		resp := e.create(e.ownerCookie, e.fixture.LabInstanceID, port)
		if resp.Status != http.StatusCreated {
			t.Fatalf("목록에 있는 %d를 승인하지 않음: %d %s", port, resp.Status, resp.Body)
		}
	}
	opened := map[int]bool{}
	for _, open := range e.peer.Opens() {
		opened[open.TargetPort] = true
	}
	if len(opened) != 3 || !opened[3000] || !opened[80] || !opened[5000] {
		t.Fatalf("Connector가 받은 port = %v", opened)
	}
	before := len(e.peer.Opens())

	// 목록에 없는 port, 숫자 범위로 인접한 port, SSH 관리 port(22는 목록에 있어도)는 거절하며 Connector에 아무것도 보내지 않는다.
	for _, port := range []int{22, 21, 81, 2999, 3001, 4999, 5001, 5173, 8080, 65535} {
		resp := e.create(e.ownerCookie, e.fixture.LabInstanceID, port)
		if resp.Status != http.StatusForbidden || e.problemCode(resp) != "preview_port_not_allowed" {
			t.Errorf("port %d: status = %d code = %q, want 403 preview_port_not_allowed: %s", port, resp.Status, e.problemCode(resp), resp.Body)
		}
	}
	if got := len(e.peer.Opens()); got != before {
		t.Fatalf("거절된 port가 Connector에 PREVIEW_OPEN %d개를 보냄", got-before)
	}
	// 형식이 틀린 port는 HTTP layer가 거절한다.
	for _, body := range []string{`{"targetPort":0}`, `{"targetPort":65536}`, `{"targetPort":"3000"}`, `{}`, `{"targetPort":3000,"host":"10.0.0.5"}`} {
		resp := e.request(http.MethodPost, e.createPath(e.fixture.LabInstanceID), e.ownerCookie, body, nil)
		if resp.Status != http.StatusBadRequest {
			t.Errorf("%s: status = %d", body, resp.Status)
		}
	}
}

// ---- Connector ----

func TestPreviewCreateWithoutThePreviewCapabilityNeverSendsPreviewMessages(t *testing.T) {
	// 연결은 되어 있지만 preview-v1을 선언하지 않았다(기존 Connector). 버전으로 추론하지 않는다.
	e := newPreviewEnv(t, previewEnvOptions{capabilities: nil})
	resp := e.create(e.ownerCookie, e.fixture.LabInstanceID, 5173)
	if resp.Status != http.StatusServiceUnavailable || e.problemCode(resp) != "preview_transport_unavailable" {
		t.Fatalf("status = %d %s, want 503 preview_transport_unavailable", resp.Status, resp.Body)
	}
	e.assertNoPreviewSideEffects("capability 없음")

	// file-v1만 선언한 Connector도 Preview를 지원하지 않는다.
	e2 := newPreviewEnv(t, previewEnvOptions{capabilities: []string{"file-v1"}})
	resp = e2.create(e2.ownerCookie, e2.fixture.LabInstanceID, 5173)
	if resp.Status != http.StatusServiceUnavailable || e2.problemCode(resp) != "preview_transport_unavailable" {
		t.Fatalf("file-v1만: status = %d %s", resp.Status, resp.Body)
	}
	e2.assertNoPreviewSideEffects("file-v1만")
}

func TestPreviewCreateWithoutAConnectorIsUnavailable(t *testing.T) {
	e := newPreviewEnv(t, previewEnvOptions{noPeer: true})
	resp := e.create(e.ownerCookie, e.fixture.LabInstanceID, 5173)
	if resp.Status != http.StatusServiceUnavailable || e.problemCode(resp) != "connector_unavailable" {
		t.Fatalf("status = %d %s, want 503 connector_unavailable", resp.Status, resp.Body)
	}
	e.assertNoPreviewSideEffects("Connector 없음")
}

// Connector가 Workspace VM의 port를 열지 못하면 attach하지 않고 PREVIEW_OPEN_RESULT FAILED로 알린다. SaaS는 안전한 code만 매핑한다.
func TestPreviewCreateMapsConnectorOpenFailures(t *testing.T) {
	cases := []struct {
		name     string
		behavior previewtest.Behavior
		status   int
		code     string
	}{
		{"application이 실행 중이 아님", previewtest.Behavior{FailOpen: "APP_NOT_RUNNING"}, http.StatusBadGateway, "preview_app_not_running"},
		{"Connector가 port를 거절", previewtest.Behavior{FailOpen: "PORT_REJECTED"}, http.StatusForbidden, "preview_port_rejected"},
		{"VM에 도달할 수 없음", previewtest.Behavior{FailOpen: "VM_UNREACHABLE"}, http.StatusGatewayTimeout, "preview_target_unreachable"},
		{"Connector 내부 오류", previewtest.Behavior{FailOpen: "INTERNAL_ERROR"}, http.StatusServiceUnavailable, "preview_open_failed"},
		{"알 수 없는 code", previewtest.Behavior{FailOpen: "SECRET-RAW-ERROR /home/student/private"}, http.StatusServiceUnavailable, "preview_open_failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1})
			e.setBehavior(func(previewtest.Open) previewtest.Behavior { return tc.behavior })
			resp := e.create(e.ownerCookie, e.fixture.LabInstanceID, 5173)
			if resp.Status != tc.status || e.problemCode(resp) != tc.code {
				t.Fatalf("status = %d %s, want %d %s", resp.Status, resp.Body, tc.status, tc.code)
			}
			// Connector 오류 원문은 응답과 log에 나오지 않는다.
			if strings.Contains(string(resp.Body), "SECRET-RAW-ERROR") || strings.Contains(e.logs.String(), "SECRET-RAW-ERROR") {
				t.Fatal("Connector 오류 원문이 노출됨")
			}
			// 성공 상태로 남은 PreviewSession이 없다. Connector가 이미 실패를 알렸으므로 PREVIEW_CLOSE는 없다.
			eventually(t, "정리", 5*time.Second, func() bool {
				return e.stack.Router.PendingPreviewOpens(e.fixture.ConnectorID) == 0 &&
					len(e.stack.PreviewGateway.SessionsForLab(e.fixture.LabInstanceID.String())) == 0
			})
			if closes := e.peer.Closes(); len(closes) != 0 {
				t.Fatalf("PREVIEW_CLOSE = %+v", closes)
			}
		})
	}
}

// 실제로 아무 application도 listen하지 않는 port에 TCP 연결을 열려 하면 connection refused이고 Connector는 APP_NOT_RUNNING으로 알린다.
func TestPreviewCreateReportsAnApplicationThatIsNotListening(t *testing.T) {
	e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1})
	closed := freeAddr(t) // 바로 닫은 port다. 아무것도 listen하지 않는다.
	e.setBehavior(func(previewtest.Open) previewtest.Behavior { return previewtest.Behavior{WorkspaceAddr: closed} })
	resp := e.create(e.ownerCookie, e.fixture.LabInstanceID, 5173)
	if resp.Status != http.StatusBadGateway || e.problemCode(resp) != "preview_app_not_running" {
		t.Fatalf("status = %d %s, want 502 preview_app_not_running", resp.Status, resp.Body)
	}
}

// attach가 시간 안에 오지 않으면 성공하지 않고 Connector에 PREVIEW_CLOSE를 보내며 pending이 남지 않는다.
func TestPreviewCreateTimesOutWithoutAnAttach(t *testing.T) {
	e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1, mods: []func(*stackOptions){func(o *stackOptions) { o.PreviewOpenTimeout = 600 * time.Millisecond }}})
	e.setBehavior(func(previewtest.Open) previewtest.Behavior { return previewtest.Behavior{NoAttach: true} })
	resp := e.create(e.ownerCookie, e.fixture.LabInstanceID, 5173)
	if resp.Status != http.StatusGatewayTimeout || e.problemCode(resp) != "preview_open_timeout" {
		t.Fatalf("status = %d %s, want 504 preview_open_timeout", resp.Status, resp.Body)
	}
	eventually(t, "PREVIEW_CLOSE", 5*time.Second, func() bool { return len(e.peer.Closes()) == 1 })
	if c := e.peer.Closes()[0]; c.Reason != "OPEN_TIMEOUT" {
		t.Fatalf("PREVIEW_CLOSE = %+v", c)
	}
	if n := e.stack.Router.PendingPreviewOpens(e.fixture.ConnectorID); n != 0 {
		t.Fatalf("PREVIEW_OPEN pending %d개가 남음", n)
	}
	if ids := e.stack.PreviewGateway.SessionsForLab(e.fixture.LabInstanceID.String()); len(ids) != 0 {
		t.Fatalf("PreviewSession이 남음: %v", ids)
	}
}

// Connector가 잘못된 correlation으로 attach하면 그 connection만 거절되고 PreviewSession은 만들어지지 않는다(시간 초과).
func TestPreviewCreateRejectsAnAttachWithWrongCorrelation(t *testing.T) {
	wrong := map[string]func(previewtest.Frame){
		"다른 previewSessionId": func(f previewtest.Frame) { f["previewSessionId"] = uuid.NewString() },
		"다른 labInstanceId":    func(f previewtest.Frame) { f["labInstanceId"] = uuid.NewString() },
		"다른 generation":       func(f previewtest.Frame) { f["generation"] = 9 },
		"다른 targetVmKey":      func(f previewtest.Frame) { f["payload"].(map[string]any)["targetVmKey"] = "db" },
		"다른 providerServerId": func(f previewtest.Frame) { f["payload"].(map[string]any)["providerServerId"] = "provider-other" },
		"다른 targetPort":       func(f previewtest.Frame) { f["payload"].(map[string]any)["targetPort"] = 5174 },
		"SSH 관리 port":         func(f previewtest.Frame) { f["payload"].(map[string]any)["targetPort"] = 22 },
	}
	for name, mutate := range wrong {
		t.Run(name, func(t *testing.T) {
			e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1, mods: []func(*stackOptions){func(o *stackOptions) { o.PreviewOpenTimeout = 700 * time.Millisecond }}})
			e.setBehavior(func(previewtest.Open) previewtest.Behavior {
				return previewtest.Behavior{Attach: mutate}
			})
			resp := e.create(e.ownerCookie, e.fixture.LabInstanceID, 5173)
			if resp.Status != http.StatusGatewayTimeout || e.problemCode(resp) != "preview_open_timeout" {
				t.Fatalf("status = %d %s, want 504 preview_open_timeout(attach가 성립하지 않음)", resp.Status, resp.Body)
			}
			if ids := e.stack.PreviewGateway.SessionsForLab(e.fixture.LabInstanceID.String()); len(ids) != 0 {
				t.Fatalf("PreviewSession이 남음: %v", ids)
			}
		})
	}
}

// 다른 Connector의 Credential로 Preview Data WSS를 열어도 이 PreviewSession을 완료시키지 못한다.
func TestPreviewCreateRejectsAnAttachFromAnotherConnector(t *testing.T) {
	e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1, mods: []func(*stackOptions){func(o *stackOptions) { o.PreviewOpenTimeout = 700 * time.Millisecond }}})
	const otherCredential = "another-connector-credential-for-preview-test-0f3a"
	otherConnector := uuid.New()
	digest := connector.CredentialDigest(otherCredential)
	terminaltest.Exec(t, e.conn, `INSERT INTO connectors (id, organization_id, name) VALUES ($1, $2, 'Other Connector')`, otherConnector, e.fixture.OrganizationID)
	terminaltest.Exec(t, e.conn, `INSERT INTO connector_credentials (id, connector_id, credential_hash) VALUES ($1, $2, $3)`, uuid.New(), otherConnector, digest[:])

	e.setBehavior(func(previewtest.Open) previewtest.Behavior {
		return previewtest.Behavior{DataCredential: otherCredential}
	})
	resp := e.create(e.ownerCookie, e.fixture.LabInstanceID, 5173)
	if resp.Status != http.StatusGatewayTimeout || e.problemCode(resp) != "preview_open_timeout" {
		t.Fatalf("status = %d %s", resp.Status, resp.Body)
	}
}

// ---- Reset race ----

// PreviewSession을 만드는 동안 Reset이 generation을 바꾸면 오래된 target을 성공으로 돌려주지 않는다.
func TestPreviewCreateDoesNotSucceedWhenResetChangesTheGenerationWhileOpening(t *testing.T) {
	e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1})
	racer := postgrestest.Connect(t, e.dsn) // peer goroutine이 쓰는 별도 연결이다.
	e.setBehavior(func(previewtest.Open) previewtest.Behavior {
		// Connector가 PREVIEW_OPEN을 받은 직후, attach하기 전에 Reset이 끝난 것처럼 generation을 올린다.
		e.fixture.BumpGeneration(t, racer, e.fixture.LabInstanceID)
		return previewtest.Behavior{}
	})
	resp := e.create(e.ownerCookie, e.fixture.LabInstanceID, 5173)
	if resp.Status != http.StatusConflict || e.problemCode(resp) != "workspace_target_changed" {
		t.Fatalf("status = %d %s, want 409 workspace_target_changed", resp.Status, resp.Body)
	}
	// Connector가 이미 TCP forwarding을 열었으므로 정리를 요청한다.
	eventually(t, "PREVIEW_CLOSE", 5*time.Second, func() bool { return len(e.peer.Closes()) == 1 })
	if c := e.peer.Closes()[0]; c.Reason != "LAB_RESET" {
		t.Fatalf("PREVIEW_CLOSE = %+v", c)
	}
	if ids := e.stack.PreviewGateway.SessionsForLab(e.fixture.LabInstanceID.String()); len(ids) != 0 {
		t.Fatalf("PreviewSession이 남음: %v", ids)
	}
	// 새 generation에서는 다시 만들 수 있고 Connector는 새 generation의 Server를 받는다.
	e.setBehavior(nil)
	s := e.mustCreate(e.ownerCookie, e.fixture.LabInstanceID, 5173)
	opens := e.peer.Opens()
	if last := opens[len(opens)-1]; last.PreviewSessionID != s.ID || last.Generation != 2 {
		t.Fatalf("새 PREVIEW_OPEN = %+v, want generation 2", last)
	}
}

// ---- Preview가 없는 구성 ----

func TestPreviewEndpointsWithoutThePreviewRoleAreUnavailable(t *testing.T) {
	e := newPreviewEnv(t, previewEnvOptions{noPeer: true, mods: []func(*stackOptions){func(o *stackOptions) {
		o.Preview, o.PreviewPolicy, o.PreviewTTL, o.PreviewOrigin = false, previewsession.Policy{}, 0, preview.OriginTemplate{}
	}}})
	if e.stack.PreviewGateway != nil || e.stack.Previews != nil {
		t.Fatal("Preview가 꺼져 있는데 조립됨")
	}
	resp := e.create(e.ownerCookie, e.fixture.LabInstanceID, 5173)
	if resp.Status != http.StatusServiceUnavailable || e.problemCode(resp) != "preview_unavailable" {
		t.Fatalf("create status = %d %s, want 503 preview_unavailable", resp.Status, resp.Body)
	}
	resp = e.request(http.MethodDelete, "/api/v1/preview-sessions/"+uuid.NewString(), e.ownerCookie, "", nil)
	if resp.Status != http.StatusServiceUnavailable || e.problemCode(resp) != "preview_unavailable" {
		t.Fatalf("delete status = %d %s", resp.Status, resp.Body)
	}
	// Preview route는 열려 있지 않다.
	dialer := websocket.Dialer{Subprotocols: []string{"labbit.connector-preview.v1"}, HandshakeTimeout: 5 * time.Second}
	if _, resp, err := dialer.Dial(e.wsBase()+preview.DataPath, http.Header{"Authorization": {"Bearer " + terminaltest.Credential}}); err == nil || resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("Preview Data route가 열려 있음: %v %v", err, resp)
	}
}

// PREVIEW_OPEN_RESULT가 전혀 오지 않아도 attach 성공만으로 Create 및 후속 sequential tunnel들이 모두 성공하고,
// 각 tunnel 요청 후 real Router의 pending PREVIEW_OPEN이 0으로 유지되어 ErrDuplicateCorrelation이 발생하지 않는다.
func TestPreviewSequentialTunnelsSucceedWithoutOpenResult(t *testing.T) {
	e := newPreviewEnv(t, previewEnvOptions{capabilities: previewV1})

	// Connector peer가 PREVIEW_OPEN_RESULT SUCCEEDED를 의도적으로 보내지 않도록 설정
	e.setBehavior(func(open previewtest.Open) previewtest.Behavior {
		return previewtest.Behavior{SkipOpenResult: true}
	})

	// 1. Create: OPEN_RESULT 없이 attach만으로 성공해야 함
	s := e.mustCreate(e.ownerCookie, e.fixture.LabInstanceID, 5173)

	// Create 성공 직후 Router pending은 0이어야 함
	if pending := e.stack.Router.PendingPreviewOpens(e.fixture.ConnectorID); pending != 0 {
		t.Fatalf("Create 후 Router pending = %d, want 0", pending)
	}

	cookie := e.login(s)

	requestCount := 0
	e.app.setHandler(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Connection", "close")
		_, _ = fmt.Fprintf(w, "app response %d for %s", requestCount, r.URL.Path)
	})

	// 2. 첫 번째 요청 (/index.html): 초기 tunnel 사용
	r1, b1 := e.get(s, cookie, "/index.html")
	if r1.StatusCode != http.StatusOK || b1 != "app response 1 for /index.html" {
		t.Fatalf("첫 번째 요청 응답 = %d %q", r1.StatusCode, b1)
	}
	if pending := e.stack.Router.PendingPreviewOpens(e.fixture.ConnectorID); pending != 0 {
		t.Fatalf("첫 번째 요청 후 Router pending = %d, want 0", pending)
	}

	// 3. 두 번째 요청 (/app.js): sequential tunnel open (OPEN_RESULT 없이도 새 tunnel 연결 성공)
	r2, b2 := e.get(s, cookie, "/app.js")
	if r2.StatusCode != http.StatusOK || b2 != "app response 2 for /app.js" {
		t.Fatalf("두 번째 요청 응답 = %d %q", r2.StatusCode, b2)
	}
	if pending := e.stack.Router.PendingPreviewOpens(e.fixture.ConnectorID); pending != 0 {
		t.Fatalf("두 번째 요청 후 Router pending = %d, want 0", pending)
	}

	// 4. 세 번째 요청 (/style.css): 추가 sequential tunnel open
	r3, b3 := e.get(s, cookie, "/style.css")
	if r3.StatusCode != http.StatusOK || b3 != "app response 3 for /style.css" {
		t.Fatalf("세 번째 요청 응답 = %d %q", r3.StatusCode, b3)
	}
	if pending := e.stack.Router.PendingPreviewOpens(e.fixture.ConnectorID); pending != 0 {
		t.Fatalf("세 번째 요청 후 Router pending = %d, want 0", pending)
	}

	// 5. 적어도 3번의 개별 Data WSS dial이 발생했는지 확인
	if dials := e.peer.DataDials(); dials < 3 {
		t.Fatalf("Data dials = %d, want >= 3", dials)
	}
}
