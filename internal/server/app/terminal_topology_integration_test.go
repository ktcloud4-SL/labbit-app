//go:build integration

package app

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal/terminaltest"
)

func statusOf(t *testing.T, client *http.Client, method, url string, header http.Header, body string) int {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s error = %v", method, url, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// realtime role만 enabled된 process는 PostgreSQL DSN 없이 시작하지만 authority(api role)가 없으므로 Terminal route를 열지 않고
// /readyz가 실패한다. 인증 없는 Terminal route나 부분 동작 route를 열지 않는다.
func TestRealtimeOnlyProcessIsNotReadyAndOpensNoTerminalRoutes(t *testing.T) {
	// DB DSN을 전혀 주지 않는다(파일 내용도 비어 있다). realtime role은 DSN을 요구하지 않는다.
	admin, application := startServerWithApplication(t, "development", "realtime", "", "")

	waitForStatus(t, admin+"/livez", http.StatusOK)
	waitForStatus(t, admin+"/readyz", http.StatusServiceUnavailable)
	time.Sleep(200 * time.Millisecond)
	assertStatus(t, admin+"/readyz", http.StatusServiceUnavailable)

	for _, path := range []string{realtime.BrowserPath, realtime.DataPath, "/connector/v1/control", "/api/v1/me"} {
		if got := statusOf(t, probeClient, http.MethodGet, application+path, nil, ""); got != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404 (route를 열면 안 됨)", path, got)
		}
	}
	// WebSocket Upgrade를 시도해도 연결되지 않는다.
	dialer := websocket.Dialer{Subprotocols: []string{realtime.BrowserSubprotocol}, HandshakeTimeout: 3 * time.Second}
	if _, resp, err := dialer.Dial("ws"+strings.TrimPrefix(application, "http")+realtime.BrowserPath, http.Header{"Origin": []string{terminalTrustedOrigin}}); err == nil || resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("Upgrade = %v, %v, want 404", resp, err)
	}
}

// api role만 enabled된 process에는 Terminal Relay가 없다. 인증과 출처 검증을 거친 요청에 명확한 503(terminal_unavailable)로 응답한다.
func TestAPIOnlyProcessReportsTerminalUnavailable(t *testing.T) {
	dsn := postgrestest.NewDatabase(t)
	postgrestest.Migrate(t, dsn, loadEmbeddedMigrations(t))
	conn := postgrestest.Connect(t, dsn)
	f := terminaltest.New(t, conn)
	cookie := terminaltest.LoginSession(t, conn, f.OwnerID)

	admin, application := startServerWithApplication(t, "development", "api", dsn, "")
	waitForStatus(t, admin+"/readyz", http.StatusOK)

	auth := http.Header{"Cookie": []string{realtime.SessionCookieName + "=" + cookie}, "Origin": []string{terminalTrustedOrigin}, "Content-Type": []string{"application/json"}}
	create := application + "/api/v1/lab-instances/" + f.LabInstanceID.String() + "/terminal-sessions"
	if got := statusOf(t, probeClient, http.MethodPost, create, auth, createBody); got != http.StatusServiceUnavailable {
		t.Fatalf("create = %d, want 503", got)
	}
	if got := statusOf(t, probeClient, http.MethodDelete, application+"/api/v1/terminal-sessions/"+f.LabInstanceID.String(), auth, ""); got != http.StatusServiceUnavailable {
		t.Fatalf("delete = %d, want 503", got)
	}
	// 인증하지 않은 요청은 여전히 401이다.
	if got := statusOf(t, probeClient, http.MethodPost, create, http.Header{"Origin": []string{terminalTrustedOrigin}, "Content-Type": []string{"application/json"}}, createBody); got != http.StatusUnauthorized {
		t.Fatalf("unauthenticated create = %d, want 401", got)
	}
	// Terminal WSS route는 없다. Connector Control은 api role이 계속 제공한다.
	for path, want := range map[string]int{realtime.BrowserPath: http.StatusNotFound, realtime.DataPath: http.StatusNotFound, "/connector/v1/control": http.StatusBadRequest} {
		if got := statusOf(t, probeClient, http.MethodGet, application+path, nil, ""); got != want {
			t.Fatalf("GET %s = %d, want %d", path, got, want)
		}
	}
}

// api와 realtime role을 같은 process에 켜면 Terminal route가 마운트되고(v0.1의 co-location), app.Run의 실제 조립으로
// 생성 → Connector OPEN → attach → I/O → 종료가 끝까지 동작한다. test용 조립이 아니라 production 조립이다.
func TestRunComposesTheTerminalStackWhenAPIAndRealtimeAreCoLocated(t *testing.T) {
	dsn := postgrestest.NewDatabase(t)
	postgrestest.Migrate(t, dsn, loadEmbeddedMigrations(t))
	conn := postgrestest.Connect(t, dsn)
	f := terminaltest.New(t, conn)
	cookie := terminaltest.LoginSession(t, conn, f.OwnerID)

	admin, application := startServerWithApplication(t, "development", "api,realtime", dsn, "")
	waitForStatus(t, admin+"/readyz", http.StatusOK)

	// route가 마운트되어 있다(non-upgrade 요청은 handler가 400으로 거절한다).
	for _, path := range []string{realtime.BrowserPath, realtime.DataPath} {
		if got := statusOf(t, probeClient, http.MethodGet, application+path, nil, ""); got != http.StatusBadRequest {
			t.Fatalf("GET %s = %d, want 400", path, got)
		}
	}
	create := application + "/api/v1/lab-instances/" + f.LabInstanceID.String() + "/terminal-sessions"
	if got := statusOf(t, probeClient, http.MethodPost, create, http.Header{"Origin": []string{terminalTrustedOrigin}, "Content-Type": []string{"application/json"}}, createBody); got != http.StatusUnauthorized {
		t.Fatalf("unauthenticated create = %d, want 401", got)
	}

	peer := terminaltest.NewConnector(t, application, terminaltest.Credential)
	peer.Start()

	request := func(method, path, body string) apiResponse {
		req, _ := http.NewRequest(method, application+path, strings.NewReader(body))
		req.Header.Set("Origin", terminalTrustedOrigin)
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: realtime.SessionCookieName, Value: cookie})
		resp, err := probeClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s error = %v", method, path, err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return apiResponse{Status: resp.StatusCode, Header: resp.Header, Body: data}
	}

	// Connector가 protocol-ready가 될 때까지 기다린다(그 전에는 503 connector_unavailable이다).
	var resp apiResponse
	eventually(t, "TerminalSession 생성", 15*time.Second, func() bool {
		resp = request(http.MethodPost, "/api/v1/lab-instances/"+f.LabInstanceID.String()+"/terminal-sessions", createBody)
		return resp.Status != http.StatusServiceUnavailable
	})
	if resp.Status != http.StatusCreated {
		t.Fatalf("create = %d, want 201: %s", resp.Status, resp.Body)
	}
	body := resp.json(t)
	s := created{ID: body["id"].(string), Token: body["sessionToken"].(string), cookie: cookie}

	header := http.Header{"Origin": []string{terminalTrustedOrigin}, "Cookie": []string{realtime.SessionCookieName + "=" + cookie}}
	dialer := websocket.Dialer{Subprotocols: []string{realtime.BrowserSubprotocol}, HandshakeTimeout: 5 * time.Second}
	ws, _, err := dialer.Dial("ws"+strings.TrimPrefix(application, "http")+realtime.BrowserPath, header)
	if err != nil {
		t.Fatalf("Browser WSS 연결 실패: %v", err)
	}
	defer ws.Close()
	b := &browser{t: t, conn: ws}
	b.write(websocket.TextMessage, attachFrame(t, s.ID, s.Token, nil))
	if attached := b.json(); attached["type"] != "TERMINAL_ATTACHED" {
		t.Fatalf("attach 응답 = %v", attached)
	}
	b.write(websocket.BinaryMessage, []byte("hello"))
	eventually(t, "INPUT", 5*time.Second, func() bool { return string(peer.Inputs(s.ID)) == "hello" })
	peer.Output(s.ID, []byte("world"))
	if got := b.binary(); string(got) != "world" {
		t.Fatalf("OUTPUT = %q", got)
	}

	if resp := request(http.MethodDelete, "/api/v1/terminal-sessions/"+s.ID, ""); resp.Status != http.StatusNoContent {
		t.Fatalf("DELETE = %d", resp.Status)
	}
	var status string
	eventually(t, "ENDED", 10*time.Second, func() bool {
		if err := conn.QueryRow(t.Context(), `SELECT status FROM terminal_sessions WHERE id = $1`, s.ID).Scan(&status); err != nil {
			return false
		}
		return status == "ENDED"
	})
	if code, _ := b.closeCode(); code != 1000 {
		t.Fatalf("Browser close code = %d, want 1000", code)
	}
}
