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
	"github.com/jackc/pgx/v5"

	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connectorwss"
	"github.com/ktcloud4-SL/labbit-app/internal/server/filetransport"
	"github.com/ktcloud4-SL/labbit-app/internal/server/filetransport/filetest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal/terminaltest"
)

const (
	fileTrustedOrigin = "https://labbit.test"

	// 이 값들이 DB, log, Control frame에 나타나면 안 된다. 실제 파일 내용이 아니다.
	fileSourceMarker = "FILE-SOURCE-MARKER-8d17c3e2"
	fileNameMarker   = "file-name-marker-0b94a6f1"
)

// fileEnv는 실제 PostgreSQL, 실제 Connector Control WSS, File Data WSS, HTTP API를 조립한 환경이다.
// Connector 쪽은 계약대로 동작하는 contract peer(fake Connector)이며 실제 OpenStack/SSH/SFTP/VM filesystem은 없다(LBT-21 범위).
type fileEnv struct {
	t       *testing.T
	conn    *pgx.Conn
	fixture *terminaltest.Fixture
	stack   *controlStack
	server  *httptest.Server
	logs    *lockedBuffer
	fs      *filetest.FS
	peer    *filetest.Peer

	ownerCookie, instructorCookie, peerCookie, outsiderCookie, foreignCookie string

	mu       sync.Mutex
	behavior func(filetest.Open) filetest.Behavior
}

// newFileEnv는 migration이 적용된 database에 Fixture를 만들고 capabilities를 선언하는 Connector peer를 protocol-ready로 연결한다.
// capabilities가 nil이면 HELLO에 capabilities field를 싣지 않는다(file-v1을 모르는 기존 Connector).
func newFileEnv(t *testing.T, capabilities []string, mods ...func(*stackOptions)) *fileEnv {
	t.Helper()
	dsn := postgrestest.NewDatabase(t)
	postgrestest.Migrate(t, dsn, loadEmbeddedMigrations(t))
	conn := postgrestest.Connect(t, dsn)

	e := &fileEnv{t: t, conn: conn, fixture: terminaltest.New(t, conn), logs: &lockedBuffer{}, fs: filetest.NewFS()}
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

	opts := stackOptions{
		Logger:               slog.New(slog.NewJSONHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		PublicOrigin:         fileTrustedOrigin,
		FileAttachTimeout:    2 * time.Second,
		FileOperationTimeout: 5 * time.Second,
		MaxFileBytes:         64,
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
	mux.Handle("GET "+filetransport.DataPath, routes.ConnectorFileData)
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

	wsBase := "ws" + strings.TrimPrefix(e.server.URL, "http")
	e.peer = filetest.Start(t, filetest.Config{
		ControlURL: wsBase + connectorwss.Path, DataURL: wsBase + filetransport.DataPath,
		Credential: terminaltest.Credential, Capabilities: capabilities, FS: e.fs,
		Behavior: func(open filetest.Open) filetest.Behavior {
			e.mu.Lock()
			fn := e.behavior
			e.mu.Unlock()
			if fn == nil {
				return filetest.Behavior{}
			}
			return fn(open)
		},
	})
	e.waitConnectorReady()
	return e
}

func (e *fileEnv) setBehavior(fn func(filetest.Open) filetest.Behavior) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.behavior = fn
}

func (e *fileEnv) waitConnectorReady() {
	e.t.Helper()
	eventually(e.t, "Connector protocol-ready", 10*time.Second, func() bool {
		return e.stack.Registry.WithReadyRoute(e.fixture.ConnectorID, func(connector.Session, connector.Route) error { return nil }) == nil
	})
}

func (e *fileEnv) treePath(labInstanceID, path string) string {
	return "/api/v1/lab-instances/" + labInstanceID + "/files/tree?path=" + url.QueryEscape(path)
}

func (e *fileEnv) contentPath(labInstanceID, path string) string {
	return "/api/v1/lab-instances/" + labInstanceID + "/files/content?path=" + url.QueryEscape(path)
}

func (e *fileEnv) request(method, path, cookie, body string, headers map[string]string) apiResponse {
	e.t.Helper()
	return e.requestCtx(context.Background(), method, path, cookie, body, headers)
}

func (e *fileEnv) requestCtx(ctx context.Context, method, path, cookie, body string, headers map[string]string) apiResponse {
	e.t.Helper()
	req, err := http.NewRequestWithContext(ctx, method, e.server.URL+path, strings.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Origin", fileTrustedOrigin)
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

func (e *fileEnv) get(path, cookie string) apiResponse {
	e.t.Helper()
	return e.request(http.MethodGet, path, cookie, "", nil)
}

func (e *fileEnv) put(path, cookie, ifMatch string, content string) apiResponse {
	e.t.Helper()
	body, _ := json.Marshal(map[string]string{"content": content})
	headers := map[string]string{}
	if ifMatch != "" {
		headers["If-Match"] = ifMatch
	}
	return e.request(http.MethodPut, path, cookie, string(body), headers)
}

func (e *fileEnv) problemCode(resp apiResponse, status int) string {
	e.t.Helper()
	if resp.Status != status {
		e.t.Fatalf("status = %d, want %d: %s", resp.Status, status, resp.Body)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/problem+json" {
		e.t.Fatalf("Content-Type = %q, want application/problem+json", got)
	}
	code, _ := resp.json(e.t)["code"].(string)
	return code
}

// databaseContains는 모든 table의 row 중 needle을 포함한 것이 있는 table을 찾는다.
func (e *fileEnv) databaseContains(needle string) (table string, found bool) {
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

// noConnectorCall은 Connector가 File 요청을 받지 않았음을 확인한다. 거절된 요청은 Connector를 호출하지 않는다.
func (e *fileEnv) noConnectorCall(why string) {
	e.t.Helper()
	if opens := e.peer.Opens(); len(opens) != 0 {
		e.t.Fatalf("%s: Connector가 FILE_OPEN을 받음: %+v", why, opens)
	}
}

func (e *fileEnv) seed() {
	e.fs.Put("main.py", []byte("print('hello')\n"))
	e.fs.Put("src/app.py", []byte("def main():\n    pass\n"))
	e.fs.Put("src/한글.txt", []byte("안녕\n"))
	e.fs.Mkdir("docs")
}

// 전체 sequence(contract peer 기준):
//
//	Browser HTTP(Cookie, Origin) → 인증 → LabInstance 소유 · ClassMembership · READY · CreationSnapshot workspaceVmKey ·
//	현재 generation의 PRESENT SERVER ProviderResource 판정 → Control FILE_OPEN → Connector의 File Data WSS Upgrade → FILE_DATA_ATTACH →
//	FILE_TREE/FILE_READ/FILE_SAVE(+Binary) → 결과 → HTTP 응답
func TestWorkspaceFileEndToEnd(t *testing.T) {
	e := newFileEnv(t, []string{"file-v1"})
	e.seed()
	f := e.fixture
	lab := f.LabInstanceID.String()

	// Tree: root. 항목은 name, path, kind뿐이고 byte 순서로 정렬된다.
	resp := e.get(e.treePath(lab, ""), e.ownerCookie)
	if resp.Status != http.StatusOK {
		t.Fatalf("Tree status = %d: %s", resp.Status, resp.Body)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	tree := resp.json(t)
	items, _ := tree["items"].([]any)
	if tree["path"] != "" || len(items) != 3 {
		t.Fatalf("Tree = %s", resp.Body)
	}
	for i, want := range []map[string]any{
		{"name": "docs", "path": "docs", "kind": "directory"},
		{"name": "main.py", "path": "main.py", "kind": "file"},
		{"name": "src", "path": "src", "kind": "directory"},
	} {
		got, _ := items[i].(map[string]any)
		if len(got) != 3 || got["name"] != want["name"] || got["path"] != want["path"] || got["kind"] != want["kind"] {
			t.Fatalf("items[%d] = %v, want %v", i, got, want)
		}
	}
	// 하위 디렉터리는 응답이 준 path를 그대로 다시 보낸다. Unicode 파일 이름도 그대로다.
	resp = e.get(e.treePath(lab, "src"), e.ownerCookie)
	if resp.Status != http.StatusOK || !strings.Contains(string(resp.Body), `"path":"src/한글.txt"`) {
		t.Fatalf("Tree(src) = %d %s", resp.Status, resp.Body)
	}

	// Read: 본문과 ETag.
	resp = e.get(e.contentPath(lab, "src/app.py"), e.ownerCookie)
	if resp.Status != http.StatusOK {
		t.Fatalf("Read status = %d: %s", resp.Status, resp.Body)
	}
	etag := resp.Header.Get("ETag")
	body := resp.json(t)
	if body["path"] != "src/app.py" || body["content"] != "def main():\n    pass\n" || len(body) != 2 {
		t.Fatalf("Read body = %s", resp.Body)
	}
	if !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, `"`) || strings.HasPrefix(etag, "W/") {
		t.Fatalf("ETag = %q, want a strong entity-tag", etag)
	}

	// Save: 읽은 ETag를 If-Match로 되돌린다. 새 ETag가 돌아오고 본문이 바뀐다.
	resp = e.put(e.contentPath(lab, "src/app.py"), e.ownerCookie, etag, "def main():\n    return 1\n")
	if resp.Status != http.StatusOK {
		t.Fatalf("Save status = %d: %s", resp.Status, resp.Body)
	}
	newETag := resp.Header.Get("ETag")
	if newETag == "" || newETag == etag {
		t.Fatalf("Save ETag = %q (이전 %q)", newETag, etag)
	}
	if saved := resp.json(t); saved["path"] != "src/app.py" || len(saved) != 1 {
		t.Fatalf("Save body = %s", resp.Body)
	}
	if got, _ := e.fs.Content("src/app.py"); string(got) != "def main():\n    return 1\n" {
		t.Fatalf("저장된 본문 = %q", got)
	}

	// stale: 예전 ETag로 저장하면 412이고 파일을 덮어쓰지 않는다.
	resp = e.put(e.contentPath(lab, "src/app.py"), e.ownerCookie, etag, "OVERWRITE")
	if code := e.problemCode(resp, http.StatusPreconditionFailed); code != "stale_revision" {
		t.Fatalf("stale Save code = %q", code)
	}
	if got, _ := e.fs.Content("src/app.py"); string(got) != "def main():\n    return 1\n" {
		t.Fatalf("stale Save가 파일을 바꿈: %q", got)
	}
	// 새 ETag로는 계속 저장된다.
	if resp := e.put(e.contentPath(lab, "src/app.py"), e.ownerCookie, newETag, "def main():\n    return 2\n"); resp.Status != http.StatusOK {
		t.Fatalf("새 ETag로 저장 = %d: %s", resp.Status, resp.Body)
	}

	// 읽기/저장 오류 의미: 없는 파일, 디렉터리, 파일을 디렉터리로 조회.
	if code := e.problemCode(e.get(e.contentPath(lab, "ghost.txt"), e.ownerCookie), http.StatusNotFound); code != "path_not_found" {
		t.Fatalf("없는 파일 code = %q", code)
	}
	if code := e.problemCode(e.get(e.contentPath(lab, "src"), e.ownerCookie), http.StatusUnprocessableEntity); code != "path_not_file" {
		t.Fatalf("디렉터리 Read code = %q", code)
	}
	if code := e.problemCode(e.get(e.treePath(lab, "main.py"), e.ownerCookie), http.StatusUnprocessableEntity); code != "path_not_directory" {
		t.Fatalf("파일 Tree code = %q", code)
	}
	if code := e.problemCode(e.put(e.contentPath(lab, "ghost.txt"), e.ownerCookie, etag, "x"), http.StatusNotFound); code != "path_not_found" {
		t.Fatalf("없는 파일 Save code = %q", code)
	}
	if _, ok := e.fs.Content("ghost.txt"); ok {
		t.Fatal("Save가 없는 파일을 만듦")
	}

	// Connector가 받은 target은 DB에서 서버가 결정한 값이다. Browser가 보내지 않은 VM key, Server ID, generation이다.
	workspace := f.Servers["workspace"]
	for _, open := range e.peer.Opens() {
		if open.LabInstanceID != lab || open.Generation != 1 || open.TargetVMKey != "workspace" || open.ProviderServerID != workspace.ProviderID {
			t.Fatalf("FILE_OPEN = %+v, want the Workspace VM (%s) of generation 1", open, workspace.ProviderID)
		}
		if open.ProviderServerID == f.Servers["db"].ProviderID || open.TargetVMKey == "db" {
			t.Fatalf("Workspace가 아닌 VM을 대상으로 함: %+v", open)
		}
	}
	eventually(t, "pending cleanup", 5*time.Second, func() bool {
		return e.stack.FileBroker.PendingCount() == 0 && e.peer.Active() == 0
	})
}

// 다른 사용자, 강사, 같은 Organization의 다른 Class 학생, 다른 Organization, 로그인하지 않은 사용자는 Connector를 호출하기 전에 거절된다.
func TestWorkspaceFileAuthorization(t *testing.T) {
	e := newFileEnv(t, []string{"file-v1"})
	e.seed()
	f := e.fixture
	lab := f.LabInstanceID.String()

	denied := []struct {
		name   string
		cookie string
		lab    string
		status int
		code   string
	}{
		{"같은 Class의 다른 학생", e.peerCookie, lab, 403, "forbidden"},
		{"강사(LabInstance 소유자가 아님)", e.instructorCookie, lab, 403, "forbidden"},
		{"같은 Organization의 다른 Class 학생", e.outsiderCookie, lab, 403, "forbidden"},
		{"다른 Organization 사용자", e.foreignCookie, lab, 403, "forbidden"},
		{"로그인하지 않음", "", lab, 401, "unauthenticated"},
		{"만료/알 수 없는 Session", "unknown-session-cookie-value", lab, 401, "unauthenticated"},
		{"없는 LabInstance", e.ownerCookie, uuid.NewString(), 404, "not_found"},
		{"UUID가 아닌 ID", e.ownerCookie, "not-a-uuid", 404, "not_found"},
		{"대문자 UUID(canonical이 아님)", e.ownerCookie, strings.ToUpper(lab), 404, "not_found"},
	}
	for _, tc := range denied {
		t.Run(tc.name, func(t *testing.T) {
			for name, resp := range map[string]apiResponse{
				"tree": e.get(e.treePath(tc.lab, ""), tc.cookie),
				"read": e.get(e.contentPath(tc.lab, "main.py"), tc.cookie),
				"save": e.put(e.contentPath(tc.lab, "main.py"), tc.cookie, `"rev"`, "x"),
			} {
				if code := e.problemCode(resp, tc.status); code != tc.code {
					t.Errorf("%s: code = %q, want %q", name, code, tc.code)
				}
			}
			e.noConnectorCall(tc.name)
		})
	}
	if got, _ := e.fs.Content("main.py"); string(got) != "print('hello')\n" {
		t.Fatalf("거절된 요청이 파일을 바꿈: %q", got)
	}

	// 현재 ClassMembership이 없으면 본인 LabInstance라도 거절한다.
	t.Run("현재 ClassMembership 없음", func(t *testing.T) {
		terminaltest.Exec(t, e.conn, `DELETE FROM class_memberships WHERE class_id = $1 AND user_id = $2`, f.ClassID, f.OwnerID)
		for name, resp := range map[string]apiResponse{
			"tree": e.get(e.treePath(lab, ""), e.ownerCookie),
			"read": e.get(e.contentPath(lab, "main.py"), e.ownerCookie),
		} {
			if code := e.problemCode(resp, http.StatusForbidden); code != "forbidden" {
				t.Errorf("%s: code = %q", name, code)
			}
		}
		e.noConnectorCall("membership 없음")
	})

	// 본인 LabInstance는 정상이다(대조군).
	resp := e.get(e.treePath(f.PeerLabInstanceID.String(), ""), e.peerCookie)
	if resp.Status != http.StatusOK {
		t.Fatalf("Peer 본인 Tree = %d: %s", resp.Status, resp.Body)
	}
	if opens := e.peer.Opens(); len(opens) != 1 || opens[0].ProviderServerID != f.PeerServer.ProviderID || opens[0].LabInstanceID != f.PeerLabInstanceID.String() {
		t.Fatalf("Peer 요청의 FILE_OPEN = %+v, want Peer의 Workspace VM", opens)
	}
}

// READY가 아니거나 현재 generation에 PRESENT Workspace SERVER가 정확히 하나가 아니면 Connector를 호출하지 않는다.
// database 생성이 느리므로 하위 시나리오는 환경 하나의 상태를 순서대로 바꿔 가며 확인한다.
func TestWorkspaceFileTargetResolution(t *testing.T) {
	t.Run("READY가 아닌 LabInstance", func(t *testing.T) {
		e := newFileEnv(t, []string{"file-v1"})
		e.seed()
		lab := e.fixture.LabInstanceID.String()
		for _, status := range []string{"PROVISIONING", "RESETTING", "FAILED", "CLEANING_UP"} {
			terminaltest.Exec(t, e.conn, `UPDATE lab_instances SET status = $2 WHERE id = $1`, e.fixture.LabInstanceID, status)
			for name, resp := range map[string]apiResponse{
				"tree": e.get(e.treePath(lab, ""), e.ownerCookie),
				"read": e.get(e.contentPath(lab, "main.py"), e.ownerCookie),
				"save": e.put(e.contentPath(lab, "main.py"), e.ownerCookie, `"rev"`, "x"),
			} {
				if code := e.problemCode(resp, http.StatusConflict); code != "lab_instance_not_ready" {
					t.Errorf("%s/%s: code = %q", status, name, code)
				}
			}
			e.noConnectorCall(status)
		}
		// READY로 돌아오면 다시 사용할 수 있다(대조군).
		terminaltest.Exec(t, e.conn, `UPDATE lab_instances SET status = 'READY' WHERE id = $1`, e.fixture.LabInstanceID)
		if resp := e.get(e.treePath(lab, ""), e.ownerCookie); resp.Status != http.StatusOK {
			t.Fatalf("READY 복귀 뒤 Tree = %d: %s", resp.Status, resp.Body)
		}
	})

	t.Run("Workspace SERVER가 없거나 PRESENT가 아님", func(t *testing.T) {
		e := newFileEnv(t, []string{"file-v1"})
		e.seed()
		lab := e.fixture.LabInstanceID.String()
		workspace := e.fixture.Servers["workspace"].ResourceID
		for _, lifecycle := range []string{"DELETED", "MISSING", "DELETING"} {
			terminaltest.Exec(t, e.conn, `UPDATE provider_resources SET lifecycle_status = $2 WHERE id = $1`, workspace, lifecycle)
			if code := e.problemCode(e.get(e.treePath(lab, ""), e.ownerCookie), http.StatusConflict); code != "workspace_target_unavailable" {
				t.Errorf("%s: code = %q", lifecycle, code)
			}
			e.noConnectorCall(lifecycle)
		}
		terminaltest.Exec(t, e.conn, `DELETE FROM provider_resources WHERE id = $1`, workspace)
		if code := e.problemCode(e.get(e.treePath(lab, ""), e.ownerCookie), http.StatusConflict); code != "workspace_target_unavailable" {
			t.Errorf("row 없음: code = %q", code)
		}
		e.noConnectorCall("row 없음")
	})

	t.Run("같은 generation에 PRESENT Workspace SERVER가 둘", func(t *testing.T) {
		e := newFileEnv(t, []string{"file-v1"})
		e.seed()
		e.fixture.AddResource(t, e.conn, e.fixture.LabInstanceID, 1, "SERVER", "workspace", "PRESENT")
		resp := e.get(e.treePath(e.fixture.LabInstanceID.String(), ""), e.ownerCookie)
		// 어느 쪽인지 알 수 없다. 임의로 고르지 않고 내부 오류로 막는다. 응답에 내부 정보가 없다.
		if code := e.problemCode(resp, http.StatusInternalServerError); code != "internal_error" {
			t.Fatalf("code = %q", code)
		}
		if strings.Contains(string(resp.Body), e.fixture.Servers["workspace"].ProviderID) {
			t.Fatalf("응답에 Provider ID가 있음: %s", resp.Body)
		}
		e.noConnectorCall("중복 PRESENT")
	})

	t.Run("CreationSnapshot과 Workspace VM 선택", func(t *testing.T) {
		e := newFileEnv(t, []string{"file-v1"})
		e.seed()

		// vms에 없는 workspaceVmKey는 어느 VM도 가리키지 않는다. fail closed.
		bad := e.fixture.AddLabInstanceWithSnapshot(t, e.conn, terminaltest.Snapshot("ghost-key", terminaltest.DefaultVMs()))
		if code := e.problemCode(e.get(e.treePath(bad.String(), ""), e.ownerCookie), http.StatusInternalServerError); code != "internal_error" {
			t.Fatalf("snapshot 모순 code = %q", code)
		}
		e.noConnectorCall("snapshot 모순")

		// snapshot의 Workspace VM은 있지만 그 LabInstance에 ProviderResource가 없다.
		noServers := e.fixture.AddLabInstanceWithSnapshot(t, e.conn, terminaltest.Snapshot("workspace", terminaltest.DefaultVMs()))
		if code := e.problemCode(e.get(e.treePath(noServers.String(), ""), e.ownerCookie), http.StatusConflict); code != "workspace_target_unavailable" {
			t.Fatalf("ProviderResource 없음 code = %q", code)
		}
		e.noConnectorCall("ProviderResource 없음")

		// 같은 LabInstance의 다른 VM(db)이 PRESENT여도 workspaceVmKey가 가리키는 VM만 대상이다.
		if resp := e.get(e.treePath(e.fixture.LabInstanceID.String(), ""), e.ownerCookie); resp.Status != http.StatusOK {
			t.Fatalf("status = %d: %s", resp.Status, resp.Body)
		}
		opens := e.peer.Opens()
		if len(opens) != 1 || opens[0].TargetVMKey != "workspace" || opens[0].ProviderServerID != e.fixture.Servers["workspace"].ProviderID {
			t.Fatalf("FILE_OPEN = %+v", opens)
		}
	})
}

// 대상은 항상 현재 generation의 ProviderResource다. Reset으로 generation이 바뀌면 새 VM을 대상으로 하고 이전 VM은 쓰지 않는다.
func TestWorkspaceFileFollowsTheCurrentGeneration(t *testing.T) {
	e := newFileEnv(t, []string{"file-v1"})
	e.seed()
	lab := e.fixture.LabInstanceID.String()

	if resp := e.get(e.treePath(lab, ""), e.ownerCookie); resp.Status != http.StatusOK {
		t.Fatalf("generation 1 Tree = %d: %s", resp.Status, resp.Body)
	}
	generation, workspace := e.fixture.BumpGeneration(t, e.conn, e.fixture.LabInstanceID)
	if resp := e.get(e.treePath(lab, ""), e.ownerCookie); resp.Status != http.StatusOK {
		t.Fatalf("generation %d Tree = %d: %s", generation, resp.Status, resp.Body)
	}
	opens := e.peer.Opens()
	if len(opens) != 2 {
		t.Fatalf("FILE_OPEN %d개", len(opens))
	}
	if opens[0].Generation != 1 || opens[0].ProviderServerID != e.fixture.Servers["workspace"].ProviderID {
		t.Fatalf("첫 FILE_OPEN = %+v", opens[0])
	}
	if opens[1].Generation != generation || opens[1].ProviderServerID != workspace.ProviderID || opens[1].ProviderServerID == opens[0].ProviderServerID {
		t.Fatalf("Reset 뒤 FILE_OPEN = %+v, want generation %d and the new Workspace VM %s", opens[1], generation, workspace.ProviderID)
	}
}

// 요청을 처리하는 동안 Reset이 generation을 바꾸면 오래된 VM의 결과를 성공으로 돌려주지 않는다.
func TestWorkspaceFileResetRaceIsNotSuccess(t *testing.T) {
	for _, op := range []string{"read", "save", "tree"} {
		t.Run(op, func(t *testing.T) {
			e := newFileEnv(t, []string{"file-v1"})
			e.seed()
			lab := e.fixture.LabInstanceID.String()
			etag := `"` + filetest.Revision([]byte("print('hello')\n")) + `"`

			stall := make(chan struct{})
			e.setBehavior(func(filetest.Open) filetest.Behavior { return filetest.Behavior{Stall: stall} })

			done := make(chan apiResponse, 1)
			go func() {
				switch op {
				case "read":
					done <- e.get(e.contentPath(lab, "main.py"), e.ownerCookie)
				case "save":
					done <- e.put(e.contentPath(lab, "main.py"), e.ownerCookie, etag, "changed")
				default:
					done <- e.get(e.treePath(lab, ""), e.ownerCookie)
				}
			}()
			eventually(t, "FILE_OPEN", 5*time.Second, func() bool { return len(e.peer.Opens()) == 1 })
			eventually(t, "요청 frame", 5*time.Second, func() bool {
				for _, f := range e.peer.DataFrames() {
					if f.FromSaaS && !f.Binary && strings.Contains(string(f.Raw), `"payload"`) && !strings.Contains(string(f.Raw), "ATTACHED") {
						return true
					}
				}
				return false
			})

			// Reset이 끝나 generation이 올라갔다(오래된 VM은 곧 사라진다).
			e.fixture.BumpGeneration(t, e.conn, e.fixture.LabInstanceID)
			close(stall)

			resp := <-done
			if code := e.problemCode(resp, http.StatusConflict); code != "workspace_target_changed" {
				t.Fatalf("code = %q", code)
			}
			// 오래된 VM의 본문이나 ETag를 돌려주지 않는다.
			if resp.Header.Get("ETag") != "" || strings.Contains(string(resp.Body), "print(") {
				t.Fatalf("성공 응답의 흔적: %v %s", resp.Header, resp.Body)
			}
		})
	}
}

// Connector가 없으면 503(connector_unavailable), file-v1을 선언하지 않았으면 503(file_transport_unavailable)이다.
func TestWorkspaceFileConnectorAvailability(t *testing.T) {
	t.Run("file-v1을 선언하지 않은 Connector", func(t *testing.T) {
		e := newFileEnv(t, nil) // 기존 Connector: capabilities field가 없다.
		e.seed()
		lab := e.fixture.LabInstanceID.String()
		for name, resp := range map[string]apiResponse{
			"tree": e.get(e.treePath(lab, ""), e.ownerCookie),
			"read": e.get(e.contentPath(lab, "main.py"), e.ownerCookie),
			"save": e.put(e.contentPath(lab, "main.py"), e.ownerCookie, `"rev"`, "x"),
		} {
			if code := e.problemCode(resp, http.StatusServiceUnavailable); code != "file_transport_unavailable" {
				t.Errorf("%s: code = %q", name, code)
			}
		}
		e.noConnectorCall("capability 없음")
		for _, raw := range e.peer.ControlFrames() {
			if strings.Contains(string(raw), "FILE_") {
				t.Fatalf("capability 없는 Connector가 File Control을 받음: %s", raw)
			}
		}
	})

	t.Run("Connector가 연결되어 있지 않음", func(t *testing.T) {
		e := newFileEnv(t, []string{"file-v1"})
		e.seed()
		e.peer.Stop()
		eventually(t, "Connector 연결 해제", 5*time.Second, func() bool {
			return e.stack.Registry.WithReadyRoute(e.fixture.ConnectorID, func(connector.Session, connector.Route) error { return nil }) != nil
		})
		resp := e.get(e.treePath(e.fixture.LabInstanceID.String(), ""), e.ownerCookie)
		if code := e.problemCode(resp, http.StatusServiceUnavailable); code != "connector_unavailable" {
			t.Fatalf("code = %q", code)
		}
	})

	t.Run("Connector가 VM에 접근하지 못함", func(t *testing.T) {
		e := newFileEnv(t, []string{"file-v1"})
		e.seed()
		e.setBehavior(func(filetest.Open) filetest.Behavior { return filetest.Behavior{FailOpen: "UNAVAILABLE"} })
		resp := e.get(e.treePath(e.fixture.LabInstanceID.String(), ""), e.ownerCookie)
		if code := e.problemCode(resp, http.StatusServiceUnavailable); code != "file_transport_unavailable" {
			t.Fatalf("code = %q", code)
		}
	})
}

// HTTP 요청이 취소되면 pending 상태를 정리하고 Connector에 FILE_CLOSE를 보낸다.
func TestWorkspaceFileClientCancelCleansUp(t *testing.T) {
	e := newFileEnv(t, []string{"file-v1"})
	e.seed()
	stall := make(chan struct{})
	t.Cleanup(func() { close(stall) })
	e.setBehavior(func(filetest.Open) filetest.Behavior { return filetest.Behavior{Stall: stall} })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan apiResponse, 1)
	go func() {
		done <- e.requestCtx(ctx, http.MethodGet, e.contentPath(e.fixture.LabInstanceID.String(), "main.py"), e.ownerCookie, "", nil)
	}()
	eventually(t, "FILE_OPEN", 5*time.Second, func() bool { return len(e.peer.Opens()) == 1 })
	cancel()
	<-done

	eventually(t, "FILE_CLOSE", 5*time.Second, func() bool {
		closes := e.peer.Closes()
		return len(closes) == 1 && closes[0].Reason == "REQUEST_CANCELED"
	})
	eventually(t, "pending cleanup", 5*time.Second, func() bool {
		return e.stack.FileBroker.PendingCount() == 0 && e.peer.Active() == 0
	})
}

// 파일 본문과 경로는 PostgreSQL, log, persistent Control WSS에 남지 않는다. 대조군으로 검사기가 동작함도 확인한다.
func TestWorkspaceFileBodyAndPathAreNotStoredOrLogged(t *testing.T) {
	e := newFileEnv(t, []string{"file-v1"})
	secretPath := "proj-" + fileNameMarker + "/notes-" + fileNameMarker + ".txt"
	e.fs.Put(secretPath, []byte("body "+fileSourceMarker+"\n"))
	lab := e.fixture.LabInstanceID.String()

	resp := e.get(e.treePath(lab, "proj-"+fileNameMarker), e.ownerCookie)
	if resp.Status != http.StatusOK {
		t.Fatalf("Tree = %d: %s", resp.Status, resp.Body)
	}
	resp = e.get(e.contentPath(lab, secretPath), e.ownerCookie)
	if resp.Status != http.StatusOK || !strings.Contains(string(resp.Body), fileSourceMarker) {
		t.Fatalf("Read = %d: %s", resp.Status, resp.Body)
	}
	etag := resp.Header.Get("ETag")
	if resp := e.put(e.contentPath(lab, secretPath), e.ownerCookie, etag, "new "+fileSourceMarker+" saved"); resp.Status != http.StatusOK {
		t.Fatalf("Save = %d: %s", resp.Status, resp.Body)
	}
	// 오류 경로도 포함한다.
	e.get(e.contentPath(lab, "proj-"+fileNameMarker+"/ghost-"+fileNameMarker), e.ownerCookie)
	e.put(e.contentPath(lab, secretPath), e.ownerCookie, etag, "stale "+fileSourceMarker)
	time.Sleep(300 * time.Millisecond)

	logs := e.logs.String()
	for name, secret := range map[string]string{
		"파일 본문":           fileSourceMarker,
		"파일 경로":           fileNameMarker,
		"로그인 Session":     e.ownerCookie,
		"Connector 인증 정보": terminaltest.Credential,
	} {
		if table, found := e.databaseContains(secret); found {
			t.Errorf("%s가 DB table %s에 저장됨", name, table)
		}
		if strings.Contains(logs, secret) {
			t.Errorf("%s가 log에 남음", name)
		}
	}
	if strings.Contains(strings.ToLower(logs), "bearer ") {
		t.Error("log에 Authorization header가 남음")
	}

	// persistent Control WSS에는 lifecycle/correlation metadata만 실린다.
	for _, raw := range e.peer.ControlFrames() {
		if s := string(raw); strings.Contains(s, fileSourceMarker) || strings.Contains(s, fileNameMarker) {
			t.Errorf("Control frame에 경로나 본문이 있음: %s", s)
		}
	}
	// 대조군: 검사기는 저장된 것을 찾고, log에는 correlation ID가 있으며, Data WSS에는 본문과 경로가 실제로 흐른다.
	if _, found := e.databaseContains(e.fixture.LabInstanceID.String()); !found {
		t.Error("DB 검사기가 저장된 LabInstance ID를 찾지 못함")
	}
	if !strings.Contains(logs, "file_request_id") || !strings.Contains(logs, e.fixture.LabInstanceID.String()) {
		t.Error("log에 file_request_id/lab_instance_id가 없음: 관측 가능한 correlation이 사라짐")
	}
	var onData strings.Builder
	for _, f := range e.peer.DataFrames() {
		onData.Write(f.Raw)
	}
	if !strings.Contains(onData.String(), fileSourceMarker) || !strings.Contains(onData.String(), fileNameMarker) {
		t.Error("Data WSS에 본문이나 경로가 흐르지 않음(검사가 무의미)")
	}
	// 요청마다 table이 늘지 않는다(새 FileSession table이 없다).
	if table, found := e.databaseContains("file_request"); found {
		t.Errorf("table %s에 file request 상태가 저장됨", table)
	}
}
