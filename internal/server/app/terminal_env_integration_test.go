//go:build integration

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"

	"github.com/ktcloud4-SL/labbit-app/internal/observability"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connectorwss"
	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime/realtimetest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal/terminaltest"
)

const (
	terminalTrustedOrigin = "https://labbit.test"

	// 이 값들이 DB나 log에 나타나면 안 된다. 실제 입력/출력이 아니다.
	terminalInputMarker  = "INPUT-MARKER-3c91f0a7"
	terminalOutputMarker = "OUTPUT-MARKER-b27e55d1"
)

var terminalStart = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

// lockedBuffer는 여러 goroutine이 쓰는 log를 담는다.
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

// terminalEnv는 실제 PostgreSQL, 실제 Connector Control WSS, Terminal Relay, HTTP API를 모두 조립한 환경이다.
// Connector 쪽은 계약대로 동작하는 contract peer이며 실제 OpenStack/SSH/PTY는 없다.
// Connector→실제 Workspace VM SSH/PTY acceptance는 LBT-20, Browser/SaaS까지 포함한 full-stack 공동 통합은 LBT-22(C2)에서 검증한다.
type terminalEnv struct {
	t       *testing.T
	dsn     string
	conn    *pgx.Conn
	fixture *terminaltest.Fixture
	clock   *realtimetest.FakeClock
	stack   *controlStack
	server  *httptest.Server
	logs    *lockedBuffer

	connector *terminaltest.Connector

	ownerCookie, peerCookie, outsiderCookie, foreignCookie string
}

// newTerminalEnv는 migration이 적용된 database에 Fixture를 만들고 Connector를 protocol-ready로 연결한다.
func newTerminalEnv(t *testing.T, mods ...func(*stackOptions)) *terminalEnv {
	t.Helper()
	dsn := postgrestest.NewDatabase(t)
	postgrestest.Migrate(t, dsn, loadEmbeddedMigrations(t))
	conn := postgrestest.Connect(t, dsn)

	e := &terminalEnv{
		t: t, dsn: dsn, conn: conn, fixture: terminaltest.New(t, conn),
		clock: realtimetest.NewFakeClock(terminalStart), logs: &lockedBuffer{},
	}
	e.ownerCookie = terminaltest.LoginSession(t, conn, e.fixture.OwnerID)
	e.peerCookie = terminaltest.LoginSession(t, conn, e.fixture.PeerID)
	e.outsiderCookie = terminaltest.LoginSession(t, conn, e.fixture.OutsiderID)
	e.foreignCookie = terminaltest.LoginSession(t, conn, e.fixture.ForeignID)

	pool, err := postgres.OpenPool(t.Context(), dsn)
	if err != nil {
		t.Fatalf("OpenPool() error = %v", err)
	}
	t.Cleanup(pool.Close)

	opts := stackOptions{
		Logger:        observability.NewJSONLoggerTo(e.logs, "labbit-server", "test", "development", "debug"),
		PublicOrigin:  terminalTrustedOrigin,
		Realtime:      true,
		Clock:         e.clock,
		AttachTimeout: 2 * time.Second,
		CloseGrace:    200 * time.Millisecond,
	}
	for _, mod := range mods {
		mod(&opts)
	}
	e.stack, err = newControlStack(postgres.NewStore(pool), opts)
	if err != nil {
		t.Fatalf("newControlStack() error = %v", err)
	}

	mux := http.NewServeMux()
	routes := e.stack.routes()
	mux.Handle("/api/v1/", routes.API)
	mux.Handle("GET "+connectorwss.Path, routes.ConnectorControl)
	mux.Handle("GET "+realtime.BrowserPath, routes.BrowserTerminal)
	mux.Handle("GET "+realtime.DataPath, routes.ConnectorTerminalData)
	e.server = httptest.NewServer(mux)
	t.Cleanup(func() {
		e.stack.close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := e.stack.shutdown(ctx); err != nil {
			t.Errorf("shutdown error = %v", err)
		}
		e.server.Close()
	})

	e.connector = terminaltest.NewConnector(t, e.server.URL, terminaltest.Credential)
	e.connector.Start()
	e.waitConnectorReady()
	return e
}

// waitConnectorReady는 Connector의 Control connection이 protocol-ready(HELLO_ACK 완료)가 될 때까지 기다린다.
func (e *terminalEnv) waitConnectorReady() {
	e.t.Helper()
	eventually(e.t, "Connector protocol-ready", 10*time.Second, func() bool {
		return e.stack.Registry.WithReadyRoute(e.fixture.ConnectorID, func(connector.Session, connector.Route) error { return nil }) == nil
	})
}

// apiResponse는 HTTP 응답이다.
type apiResponse struct {
	Status int
	Header http.Header
	Body   []byte
}

func (r apiResponse) json(t *testing.T) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(r.Body, &obj); err != nil {
		t.Fatalf("응답이 JSON이 아님: %v (%s)", err, r.Body)
	}
	return obj
}

// request는 Browser처럼 Cookie와 Origin을 가진 HTTP 요청을 보낸다.
func (e *terminalEnv) request(method, path, cookie, body string, mods ...func(*http.Request)) apiResponse {
	e.t.Helper()
	return e.requestCtx(context.Background(), method, path, cookie, body, mods...)
}

func (e *terminalEnv) requestCtx(ctx context.Context, method, path, cookie, body string, mods ...func(*http.Request)) apiResponse {
	e.t.Helper()
	req, err := http.NewRequestWithContext(ctx, method, e.server.URL+path, strings.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Origin", terminalTrustedOrigin)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: realtime.SessionCookieName, Value: cookie})
	}
	for _, mod := range mods {
		mod(req)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}).Do(req)
	if err != nil {
		return apiResponse{Status: -1, Body: []byte(err.Error())}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return apiResponse{Status: resp.StatusCode, Header: resp.Header, Body: data}
}

const createBody = `{"targetVmKey":"workspace","cols":120,"rows":40}`

func (e *terminalEnv) createPath(labInstanceID uuid.UUID) string {
	return "/api/v1/lab-instances/" + labInstanceID.String() + "/terminal-sessions"
}

// createSession은 Owner의 LabInstance에 TerminalSession을 만들고 201을 확인한다.
func (e *terminalEnv) createSession(cookie string, labInstanceID uuid.UUID) created {
	e.t.Helper()
	resp := e.request(http.MethodPost, e.createPath(labInstanceID), cookie, createBody)
	if resp.Status != http.StatusCreated {
		e.t.Fatalf("TerminalSession 생성 = %d, want 201: %s", resp.Status, resp.Body)
	}
	body := resp.json(e.t)
	id, _ := body["id"].(string)
	token, _ := body["sessionToken"].(string)
	expires, _ := body["tokenExpiresAt"].(string)
	generation, _ := body["generation"].(float64)
	if id == "" || token == "" || expires == "" || generation < 1 {
		e.t.Fatalf("생성 응답에 필수 필드가 없음: %s", resp.Body)
	}
	return created{ID: id, Token: token, Generation: int64(generation), TokenExpiresAt: expires, cookie: cookie}
}

// created는 생성 응답이다.
type created struct {
	ID             string
	Token          string
	Generation     int64
	TokenExpiresAt string
	cookie         string
}

// sessionRow는 terminal_sessions row의 관측값이다.
type sessionRow struct {
	Status         string
	EndReason      *string
	Generation     int64
	GraceExpiresAt *time.Time
	DetachedAt     *time.Time
	AttachedAt     *time.Time
	EndedAt        *time.Time
	TokenHash      []byte
	TokenExpiresAt time.Time
	CreatedAt      time.Time
}

func (e *terminalEnv) row(id string) sessionRow {
	e.t.Helper()
	var r sessionRow
	err := e.conn.QueryRow(e.t.Context(),
		`SELECT status, end_reason, generation, grace_expires_at, detached_at, attached_at, ended_at, attach_token_hash, token_expires_at, created_at
		 FROM terminal_sessions WHERE id = $1`, id).
		Scan(&r.Status, &r.EndReason, &r.Generation, &r.GraceExpiresAt, &r.DetachedAt, &r.AttachedAt, &r.EndedAt, &r.TokenHash, &r.TokenExpiresAt, &r.CreatedAt)
	if err != nil {
		e.t.Fatalf("terminal_sessions 조회 실패: %v", err)
	}
	return r
}

func (e *terminalEnv) waitStatus(id, status string) sessionRow {
	e.t.Helper()
	var r sessionRow
	eventually(e.t, fmt.Sprintf("TerminalSession %s가 %s", id, status), 10*time.Second, func() bool {
		r = e.row(id)
		return r.Status == status
	})
	return r
}

func (e *terminalEnv) sessionCount() int { return terminaltest.Count(e.t, e.conn, "terminal_sessions") }

// browser는 Browser 역할의 Terminal WSS client다. reader goroutine 하나가 channel로 넘겨 read timeout이 connection을 망가뜨리지 않는다.
type browser struct {
	t    *testing.T
	conn *websocket.Conn
	msgs chan browserMessage
	once sync.Once
}

type browserMessage struct {
	kind int
	data []byte
	err  error
}

func (b *browser) start() {
	b.once.Do(func() {
		b.msgs = make(chan browserMessage, 1024)
		go func() {
			defer close(b.msgs)
			for {
				kind, data, err := b.conn.ReadMessage()
				if err != nil {
					b.msgs <- browserMessage{err: err}
					return
				}
				b.msgs <- browserMessage{kind: kind, data: data}
			}
		}()
	})
}

func (b *browser) next() browserMessage {
	b.t.Helper()
	b.start()
	select {
	case m, ok := <-b.msgs:
		if !ok {
			return browserMessage{err: io.EOF}
		}
		return m
	case <-time.After(5 * time.Second):
		return browserMessage{err: fmt.Errorf("시간 안에 message가 오지 않음")}
	}
}

func (b *browser) json() map[string]any {
	b.t.Helper()
	m := b.next()
	if m.err != nil || m.kind != websocket.TextMessage {
		b.t.Fatalf("JSON message = %+v", m)
	}
	var obj map[string]any
	if err := json.Unmarshal(m.data, &obj); err != nil {
		b.t.Fatalf("JSON decode: %v (%s)", err, m.data)
	}
	return obj
}

func (b *browser) binary() []byte {
	b.t.Helper()
	m := b.next()
	if m.err != nil || m.kind != websocket.BinaryMessage {
		b.t.Fatalf("Binary message = %+v", m)
	}
	return m.data
}

// closeCode는 close frame까지 읽고 close code를 반환한다. 그 전의 message는 collected에 담는다.
func (b *browser) closeCode() (code int, collected []browserMessage) {
	b.t.Helper()
	for {
		m := b.next()
		if m.err == nil {
			collected = append(collected, m)
			continue
		}
		if closeErr, ok := m.err.(*websocket.CloseError); ok {
			return closeErr.Code, collected
		}
		b.t.Fatalf("close frame 없이 종료: %v", m.err)
	}
}

func (b *browser) write(kind int, data []byte) {
	b.t.Helper()
	if err := b.conn.WriteMessage(kind, data); err != nil {
		b.t.Fatalf("WriteMessage() error = %v", err)
	}
}

func (b *browser) close() { _ = b.conn.Close() }

// dialBrowser는 Cookie와 Origin으로 Terminal WSS에 Upgrade한다.
func (e *terminalEnv) dialBrowser(cookie, origin string) (*browser, *http.Response, error) {
	e.t.Helper()
	header := http.Header{}
	if origin != "" {
		header.Set("Origin", origin)
	}
	if cookie != "" {
		header.Set("Cookie", realtime.SessionCookieName+"="+cookie)
	}
	dialer := websocket.Dialer{Subprotocols: []string{realtime.BrowserSubprotocol}, HandshakeTimeout: 5 * time.Second}
	conn, resp, err := dialer.Dial("ws"+strings.TrimPrefix(e.server.URL, "http")+realtime.BrowserPath, header)
	if err != nil {
		return nil, resp, err
	}
	b := &browser{t: e.t, conn: conn}
	e.t.Cleanup(b.close)
	return b, resp, nil
}

func attachFrame(t *testing.T, sessionID, token string, extra map[string]any) []byte {
	t.Helper()
	payload := map[string]any{"sessionToken": token, "cols": 132, "rows": 43}
	for k, v := range extra {
		payload[k] = v
	}
	data, err := json.Marshal(map[string]any{
		"type": "TERMINAL_ATTACH", "messageId": "attach-" + uuid.NewString(), "sentAt": time.Now().UTC().Format(time.RFC3339Nano),
		"terminalSessionId": sessionID, "payload": payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// attach는 Cookie와 attach token으로 Browser를 붙이고 TERMINAL_ATTACHED까지 받는다.
func (e *terminalEnv) attach(s created) (*browser, map[string]any) {
	e.t.Helper()
	b, _, err := e.dialBrowser(s.cookie, terminalTrustedOrigin)
	if err != nil {
		e.t.Fatalf("Browser WSS 연결 실패: %v", err)
	}
	b.write(websocket.TextMessage, attachFrame(e.t, s.ID, s.Token, nil))
	attached := b.json()
	if attached["type"] != "TERMINAL_ATTACHED" {
		e.t.Fatalf("attach 응답 = %v, want TERMINAL_ATTACHED", attached)
	}
	return b, attached
}

// attachRejected는 attach가 거절되는 것을 확인하고 ERROR code와 close code를 반환한다.
func (e *terminalEnv) attachRejected(cookie, sessionID, token string) (code string, closeCode int) {
	e.t.Helper()
	b, _, err := e.dialBrowser(cookie, terminalTrustedOrigin)
	if err != nil {
		e.t.Fatalf("Browser WSS 연결 실패: %v", err)
	}
	b.write(websocket.TextMessage, attachFrame(e.t, sessionID, token, nil))
	msg := b.json()
	if msg["type"] != "ERROR" {
		e.t.Fatalf("응답 = %v, want ERROR", msg)
	}
	payload, _ := msg["payload"].(map[string]any)
	code, _ = payload["code"].(string)
	closeCode, _ = b.closeCode()
	return code, closeCode
}

// tokenDigest는 Browser에 전달한 token의 SHA-256 digest다. DB에는 이 값만 있어야 한다.
func tokenDigest(t *testing.T, token string) []byte {
	t.Helper()
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(raw) != 32 {
		t.Fatalf("token 형식 = %q (%d bytes), %v", token, len(raw), err)
	}
	digest := sha256.Sum256(raw)
	return digest[:]
}

// databaseContains는 public schema의 모든 table의 모든 row를 text로 바꿔 needle이 하나라도 들어 있는지 검사한다.
// "코드상 저장하지 않는다"가 아니라 실제 DB 내용을 관찰한다.
func (e *terminalEnv) databaseContains(needle string) (table string, found bool) {
	e.t.Helper()
	rows, err := e.conn.Query(e.t.Context(), `SELECT tablename FROM pg_tables WHERE schemaname = 'public'`)
	if err != nil {
		e.t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			e.t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	for _, name := range tables {
		var n int
		query := fmt.Sprintf(`SELECT count(*) FROM %s t WHERE t::text LIKE '%%' || $1 || '%%'`, pgx.Identifier{name}.Sanitize())
		if err := e.conn.QueryRow(e.t.Context(), query, needle).Scan(&n); err != nil {
			e.t.Fatalf("%s 검사 실패: %v", name, err)
		}
		if n > 0 {
			return name, true
		}
	}
	return "", false
}
