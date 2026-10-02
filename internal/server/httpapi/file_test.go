package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/ktcloud4-SL/labbit-app/internal/observability"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
	"github.com/ktcloud4-SL/labbit-app/internal/server/workspacefile"
)

const (
	fileCookie = "file-test-session-cookie"
	// 이 값이 응답, 오류 문구, log에 나타나면 안 된다.
	leakMarker = "LEAK-MARKER-must-not-appear-anywhere-6e0d"
)

// fakeFiles는 use case의 결과를 HTTP로 옮기는 방식만 검증하기 위한 fake다.
type fakeFiles struct {
	treeIn   []workspacefile.TreeInput
	readIn   []workspacefile.ReadInput
	saveIn   []workspacefile.SaveInput
	treeFor  []repository.User
	maxBytes int64

	listing    workspacefile.Listing
	file       workspacefile.File
	saved      workspacefile.Saved
	treeErr    error
	readErr    error
	saveErr    error
	treeCalled int
}

func (f *fakeFiles) Tree(_ context.Context, user repository.User, in workspacefile.TreeInput) (workspacefile.Listing, error) {
	f.treeIn = append(f.treeIn, in)
	f.treeFor = append(f.treeFor, user)
	return f.listing, f.treeErr
}

func (f *fakeFiles) Read(_ context.Context, _ repository.User, in workspacefile.ReadInput) (workspacefile.File, error) {
	f.readIn = append(f.readIn, in)
	return f.file, f.readErr
}

func (f *fakeFiles) Save(_ context.Context, _ repository.User, in workspacefile.SaveInput) (workspacefile.Saved, error) {
	f.saveIn = append(f.saveIn, in)
	return f.saved, f.saveErr
}

func (f *fakeFiles) MaxFileBytes() int64 { return f.maxBytes }

func (f *fakeFiles) calls() int { return len(f.treeIn) + len(f.readIn) + len(f.saveIn) }

type fileHarness struct {
	*harness
	files *fakeFiles
}

func newFileHarness(t *testing.T, mods ...func(*Options)) *fileHarness {
	t.Helper()
	fake := newFakeAuth()
	fake.sessions[fileCookie] = fake.principal
	logs := &bytes.Buffer{}
	files := &fakeFiles{
		maxBytes: 16,
		listing: workspacefile.Listing{Path: "src", Items: []workspacefile.Item{
			{Name: "app.py", Path: "src/app.py", Kind: workspacefile.KindFile},
			{Name: "lib", Path: "src/lib", Kind: workspacefile.KindDirectory},
		}},
		file:  workspacefile.File{Path: "src/app.py", Content: "print('안녕')\n", Revision: "rev-1"},
		saved: workspacefile.Saved{Path: "src/app.py", Revision: "rev-2"},
	}
	opts := Options{
		Auth:         fake,
		Classes:      &fakeClasses{},
		Files:        files,
		PublicOrigin: trustedOrigin,
		Logger:       slog.New(slog.NewJSONHandler(logs, nil)),
	}
	for _, mod := range mods {
		mod(&opts)
	}
	handler, err := New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return &fileHarness{harness: &harness{handler: handler, auth: fake, classes: &fakeClasses{}, logs: logs}, files: files}
}

func (h *fileHarness) tree(query string, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	base := []func(*http.Request){withCookie(fileCookie)}
	return h.send(http.MethodGet, "/api/v1/lab-instances/"+labInstanceID+"/files/tree"+query, "", append(base, mods...)...)
}

func (h *fileHarness) read(query string, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	base := []func(*http.Request){withCookie(fileCookie)}
	return h.send(http.MethodGet, "/api/v1/lab-instances/"+labInstanceID+"/files/content"+query, "", append(base, mods...)...)
}

func (h *fileHarness) save(query, body string, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	base := []func(*http.Request){
		withCookie(fileCookie), withHeader("Origin", trustedOrigin), withHeader("Content-Type", "application/json"), withHeader("If-Match", `"rev-1"`),
	}
	return h.send(http.MethodPut, "/api/v1/lab-instances/"+labInstanceID+"/files/content"+query, body, append(base, mods...)...)
}

func requireProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d: %s", rec.Code, status, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), leakMarker) {
		t.Fatalf("응답에 민감한 값이 있음: %s", rec.Body.String())
	}
	problem := decodeProblem(t, rec)
	if problem.Code != code || problem.RequestID == "" {
		t.Fatalf("problem = %+v, want code %q", problem, code)
	}
}

func TestListWorkspaceFiles(t *testing.T) {
	h := newFileHarness(t)

	rec := h.tree("?path=src")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	// OpenAPI WorkspaceFileTree: path와 items뿐이다. 항목은 name, path, kind뿐이다(uid/gid, mode, 크기, SSH metadata 없음).
	if len(body) != 2 || body["path"] != "src" {
		t.Fatalf("body = %v", body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %v", items)
	}
	first, _ := items[0].(map[string]any)
	if len(first) != 3 || first["name"] != "app.py" || first["path"] != "src/app.py" || first["kind"] != "file" {
		t.Fatalf("items[0] = %v", first)
	}
	if second, _ := items[1].(map[string]any); second["kind"] != "directory" {
		t.Fatalf("items[1] = %v", second)
	}

	// use case는 path의 LabInstance ID, query의 path, 현재 사용자, 요청 correlation만 받는다.
	if len(h.files.treeIn) != 1 {
		t.Fatalf("Tree 호출 %d번", len(h.files.treeIn))
	}
	in := h.files.treeIn[0]
	if in.LabInstanceID != labInstanceID || in.Path != "src" || in.RequestID == "" {
		t.Fatalf("TreeInput = %+v", in)
	}
	if h.files.treeFor[0] != h.auth.principal.User {
		t.Fatalf("현재 사용자 = %+v", h.files.treeFor[0])
	}
}

func TestListWorkspaceFilesEmptyDirectoryIsAnEmptyArray(t *testing.T) {
	h := newFileHarness(t)
	h.files.listing = workspacefile.Listing{Path: "", Items: nil}
	rec := h.tree("")
	if !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatalf("body = %s, want an empty array (not null)", rec.Body.String())
	}
}

// query의 percent-decoding은 handler에서 한 번만 일어난다. 이후 계층이 다시 decode하지 않도록 값을 그대로 넘긴다.
func TestPathQueryIsDecodedExactlyOnce(t *testing.T) {
	cases := []struct {
		query string
		want  string
	}{
		{"", ""},
		{"?path=", ""},
		{"?path=src", "src"},
		{"?path=src%2Fmain", "src/main"}, // 정상 인코딩한 구분자다.
		{"?path=src/main", "src/main"},
		{"?path=%ED%95%9C%EA%B8%80", "한글"},
		{"?path=a+b", "a b"}, // query에서 +는 공백이다.
		{"?path=a%20b", "a b"},
		// 한 번 decode한 결과가 그대로 use case에 간다. 이중 인코딩은 percent-escape가 남아 use case가 거절한다.
		{"?path=%2e%2e", ".."},
		{"?path=%252e%252e", "%2e%2e"},
		{"?path=a%252fb", "a%2fb"},
		{"?path=a%255cb", "a%5cb"},
		{"?path=%2Fetc%2Fpasswd", "/etc/passwd"},
		{"?path=a%00b", "a\x00b"},
		{"?path=..%5Cx", `..\x`},
		{"?other=1&path=x", "x"},
	}
	for _, tc := range cases {
		h := newFileHarness(t)
		rec := h.tree(tc.query)
		if rec.Code != http.StatusOK {
			t.Fatalf("%q: status = %d: %s", tc.query, rec.Code, rec.Body.String())
		}
		if got := h.files.treeIn[0].Path; got != tc.want {
			t.Errorf("%q: use case path = %q, want %q", tc.query, got, tc.want)
		}
	}
}

func TestMalformedOrAmbiguousPathQueryIsRejectedBeforeTheUseCase(t *testing.T) {
	for _, query := range []string{
		"?path=%zz",      // 잘못된 percent-encoding
		"?path=%",        // 잘린 percent-encoding
		"?path=a&path=b", // 어느 것이 대상인지 알 수 없다
		"?path=a&path=",  //
		"?path=a;b=c",    // 세미콜론 구분자
		"?path=%E0%A4%A", // 잘린 escape
	} {
		h := newFileHarness(t)
		for name, rec := range map[string]*httptest.ResponseRecorder{"tree": h.tree(query), "read": h.read(query)} {
			requireProblem(t, rec, http.StatusBadRequest, "invalid_request")
			if h.files.calls() != 0 {
				t.Fatalf("%s %q: use case가 호출됨", name, query)
			}
		}
	}
}

func TestReadWorkspaceFile(t *testing.T) {
	h := newFileHarness(t)

	rec := h.read("?path=src%2Fapp.py")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("ETag"); got != `"rev-1"` {
		t.Fatalf("ETag = %q, want a quoted strong entity-tag", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	// OpenAPI WorkspaceFileContent: path와 content뿐이다. revision은 ETag header로만 전달한다.
	if len(body) != 2 || body["path"] != "src/app.py" || body["content"] != "print('안녕')\n" {
		t.Fatalf("body = %v", body)
	}
	in := h.files.readIn[0]
	if in.LabInstanceID != labInstanceID || in.Path != "src/app.py" || in.RequestID == "" {
		t.Fatalf("ReadInput = %+v", in)
	}
}

func TestReadWorkspaceFileRequiresAPath(t *testing.T) {
	h := newFileHarness(t)
	requireProblem(t, h.read(""), http.StatusBadRequest, "invalid_request")
	requireProblem(t, h.read("?other=1"), http.StatusBadRequest, "invalid_request")
	if h.files.calls() != 0 {
		t.Fatal("use case가 호출됨")
	}
	// 빈 path는 "있는" 값이다. 경로 규칙은 use case가 판단한다.
	h.files.readErr = workspacefile.ErrInvalidPath
	requireProblem(t, h.read("?path="), http.StatusBadRequest, "invalid_path")
	if len(h.files.readIn) != 1 || h.files.readIn[0].Path != "" {
		t.Fatalf("ReadInput = %+v", h.files.readIn)
	}
}

func TestSaveWorkspaceFile(t *testing.T) {
	h := newFileHarness(t)

	rec := h.save("?path=src%2Fapp.py", `{"content":"print('hi')\n"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("ETag"); got != `"rev-2"` {
		t.Fatalf("ETag = %q, want the new revision", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 1 || body["path"] != "src/app.py" {
		t.Fatalf("body = %v", body)
	}
	in := h.files.saveIn[0]
	if in.LabInstanceID != labInstanceID || in.Path != "src/app.py" || in.IfMatchRevision != "rev-1" || in.Content != "print('hi')\n" || in.RequestID == "" {
		t.Fatalf("SaveInput = %+v", in)
	}
}

func TestSaveWorkspaceFileIfMatch(t *testing.T) {
	cases := []struct {
		name    string
		header  []string
		status  int
		code    string
		wantRev string
	}{
		{"없음", nil, 400, "if_match_required", ""},
		{"strong entity-tag", []string{`"rev-1"`}, 200, "", "rev-1"},
		{"앞뒤 공백", []string{`  "rev-1" `}, 200, "", "rev-1"},
		{"Connector가 만들 수 없는 값도 형식이 맞으면 use case가 판단", []string{`"abc def"`}, 400, "invalid_if_match", ""},
		{"빈 entity-tag는 형식상 유효(use case가 412)", []string{`""`}, 200, "", ""},
		{"*", []string{`*`}, 400, "invalid_if_match", ""},
		{"weak validator", []string{`W/"rev-1"`}, 400, "invalid_if_match", ""},
		{"따옴표 없음", []string{`rev-1`}, 400, "invalid_if_match", ""},
		{"여는 따옴표만", []string{`"rev-1`}, 400, "invalid_if_match", ""},
		{"목록", []string{`"rev-1", "rev-2"`}, 400, "invalid_if_match", ""},
		{"목록(공백 없이)", []string{`"rev-1","rev-2"`}, 400, "invalid_if_match", ""},
		{"header가 둘", []string{`"rev-1"`, `"rev-2"`}, 400, "invalid_if_match", ""},
		{"내부에 따옴표", []string{`"re"v"`}, 400, "invalid_if_match", ""},
		{"제어 문자", []string{"\"re\tv\""}, 400, "invalid_if_match", ""},
		{"빈 header 값", []string{``}, 400, "invalid_if_match", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newFileHarness(t)
			rec := h.save("?path=a.txt", `{"content":"x"}`, func(r *http.Request) {
				r.Header.Del("If-Match")
				for _, v := range tc.header {
					r.Header.Add("If-Match", v)
				}
			})
			if tc.code != "" {
				requireProblem(t, rec, tc.status, tc.code)
				if h.files.calls() != 0 {
					t.Fatal("If-Match가 올바르지 않은데 use case가 호출됨")
				}
				return
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			if got := h.files.saveIn[0].IfMatchRevision; got != tc.wantRev {
				t.Fatalf("IfMatchRevision = %q, want %q", got, tc.wantRev)
			}
		})
	}
}

func TestSaveWorkspaceFileBody(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		contentType string
		status      int
		code        string
		wantContent string
	}{
		{"JSON", `{"content":"a"}`, "application/json", 200, "", "a"},
		{"charset가 붙은 Content-Type", `{"content":"a"}`, "application/json; charset=utf-8", 200, "", "a"},
		{"빈 본문", `{"content":""}`, "application/json", 200, "", ""},
		{"줄바꿈과 공백을 변환하지 않음", "{\"content\":\"a\\r\\n\\tb  \\n\"}", "application/json", 200, "", "a\r\n\tb  \n"},
		{"NUL escape는 use case가 판단", `{"content":"a\u0000b"}`, "application/json", 200, "", "a\x00b"},
		{"짝이 맞는 surrogate escape", `{"content":"\ud83d\ude00"}`, "application/json", 200, "", "😀"},
		{"이스케이프된 백슬래시 뒤의 u", `{"content":"\\ud800"}`, "application/json", 200, "", `\ud800`},
		{"U+FFFD 문자 자체", "{\"content\":\"\xef\xbf\xbd\"}", "application/json", 200, "", "\ufffd"},
		{"escape한 U+FFFD", `{"content":"\ufffd"}`, "application/json", 200, "", "\ufffd"},

		{"lone high surrogate", `{"content":"\ud800"}`, "application/json", 422, "unsupported_encoding", ""},
		{"lone low surrogate", `{"content":"\udc00"}`, "application/json", 422, "unsupported_encoding", ""},
		{"high surrogate 뒤에 일반 문자", `{"content":"\ud800a"}`, "application/json", 422, "unsupported_encoding", ""},
		{"high surrogate 두 개", `{"content":"\ud800\ud800"}`, "application/json", 422, "unsupported_encoding", ""},
		{"low가 먼저인 쌍", `{"content":"\ude00\ud83d"}`, "application/json", 422, "unsupported_encoding", ""},
		{"유효하지 않은 UTF-8", "{\"content\":\"a\xffb\"}", "application/json", 422, "unsupported_encoding", ""},
		{"잘린 multibyte", "{\"content\":\"\xed\x95\"}", "application/json", 422, "unsupported_encoding", ""},

		{"Content-Type이 JSON이 아님", `{"content":"a"}`, "text/plain", 400, "invalid_request", ""},
		{"Content-Type 없음", `{"content":"a"}`, "", 400, "invalid_request", ""},
		{"JSON이 아님", `not json`, "application/json", 400, "invalid_request", ""},
		{"빈 body", ``, "application/json", 400, "invalid_request", ""},
		{"content 없음", `{}`, "application/json", 400, "invalid_request", ""},
		{"content null", `{"content":null}`, "application/json", 400, "invalid_request", ""},
		{"content가 문자열이 아님", `{"content":123}`, "application/json", 400, "invalid_request", ""},
		{"알 수 없는 field", `{"content":"a","path":"b"}`, "application/json", 400, "invalid_request", ""},
		{"JSON 값이 둘", `{"content":"a"}{"content":"b"}`, "application/json", 400, "invalid_request", ""},
		{"JSON 배열", `["a"]`, "application/json", 400, "invalid_request", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newFileHarness(t)
			rec := h.save("?path=a.txt", tc.body, func(r *http.Request) {
				if tc.contentType == "" {
					r.Header.Del("Content-Type")
				} else {
					r.Header.Set("Content-Type", tc.contentType)
				}
			})
			if tc.code != "" {
				requireProblem(t, rec, tc.status, tc.code)
				if h.files.calls() != 0 {
					t.Fatal("본문이 올바르지 않은데 use case가 호출됨")
				}
				return
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			if got := h.files.saveIn[0].Content; got != tc.wantContent {
				t.Fatalf("Content = %q, want %q", got, tc.wantContent)
			}
		})
	}
}

func TestSaveWorkspaceFileRequiresAPath(t *testing.T) {
	h := newFileHarness(t)
	requireProblem(t, h.save("", `{"content":"a"}`), http.StatusBadRequest, "invalid_request")
	requireProblem(t, h.save("?path=a&path=b", `{"content":"a"}`), http.StatusBadRequest, "invalid_request")
	if h.files.calls() != 0 {
		t.Fatal("use case가 호출됨")
	}
}

// body 상한은 파일 크기 한도에 JSON escape 확장을 더한 값이다. 넘으면 읽지 않고 413이다.
func TestSaveWorkspaceFileBodyBound(t *testing.T) {
	h := newFileHarness(t)
	h.files.maxBytes = 16

	// 한도 안(제어 문자를 모두 6 byte escape로 쓴 16 byte)은 use case까지 간다.
	escaped := strings.Repeat(`\u0001`, 16)
	rec := h.save("?path=a.txt", `{"content":"`+escaped+`"}`)
	if rec.Code != http.StatusOK || len(h.files.saveIn) != 1 || len(h.files.saveIn[0].Content) != 16 {
		t.Fatalf("한도 안의 escape 본문 = %d %s", rec.Code, rec.Body.String())
	}

	h = newFileHarness(t)
	h.files.maxBytes = 16
	rec = h.save("?path=a.txt", `{"content":"`+strings.Repeat("a", 4096)+`"}`)
	requireProblem(t, rec, http.StatusRequestEntityTooLarge, "file_too_large")
	if h.files.calls() != 0 {
		t.Fatal("한도를 넘는 body가 use case까지 감")
	}
}

func TestWorkspaceFileRoutesRequireAuthentication(t *testing.T) {
	h := newFileHarness(t)
	noCookie := func(r *http.Request) { r.Header.Del("Cookie") }
	requireProblem(t, h.tree("", noCookie), http.StatusUnauthorized, "unauthenticated")
	requireProblem(t, h.read("?path=a", noCookie), http.StatusUnauthorized, "unauthenticated")
	requireProblem(t, h.save("?path=a", `{"content":"a"}`, noCookie), http.StatusUnauthorized, "unauthenticated")
	unknownSession := func(r *http.Request) {
		r.Header.Del("Cookie")
		withCookie("unknown-session")(r)
	}
	requireProblem(t, h.tree("", unknownSession), http.StatusUnauthorized, "unauthenticated")
	if h.files.calls() != 0 {
		t.Fatal("인증되지 않은 요청이 use case까지 감")
	}
}

// PUT은 unsafe method이므로 Origin 검증이 인증보다 먼저다.
func TestSaveWorkspaceFileRequiresATrustedOrigin(t *testing.T) {
	h := newFileHarness(t)
	for name, mod := range map[string]func(*http.Request){
		"Origin/Referer 없음": withoutSource,
		"다른 Origin":         withHeader("Origin", "https://evil.example"),
	} {
		rec := h.save("?path=a", `{"content":"a"}`, mod)
		requireProblem(t, rec, http.StatusForbidden, "csrf_rejected")
		if h.files.calls() != 0 {
			t.Fatalf("%s: use case가 호출됨", name)
		}
	}
	// 읽기(GET)에는 Origin을 요구하지 않는다.
	if rec := h.read("?path=a"); rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d", rec.Code)
	}
}

func TestWorkspaceFileRoutesOnlyAllowTheDocumentedMethods(t *testing.T) {
	h := newFileHarness(t)
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/files/content"}, {http.MethodDelete, "/files/content"}, {http.MethodPatch, "/files/content"},
		{http.MethodPost, "/files/tree"}, {http.MethodPut, "/files/tree"}, {http.MethodDelete, "/files/tree"},
	} {
		rec := h.send(tc.method, "/api/v1/lab-instances/"+labInstanceID+tc.path+"?path=a", "", withCookie(fileCookie), withHeader("Origin", trustedOrigin))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want 405", tc.method, tc.path, rec.Code)
		}
	}
	if h.files.calls() != 0 {
		t.Fatal("허용하지 않은 method가 use case까지 감")
	}
}

// use case의 typed error가 OpenAPI의 status와 stable code로 바뀐다. 오류 원문(경로, 본문, Connector 문구)은 응답에 없다.
func TestFileErrorsMapToProblemDetails(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{workspacefile.ErrInvalidPath, 400, "invalid_path"},
		{&workspacefile.PathError{Reason: "dot_segment"}, 400, "invalid_path"},
		{workspacefile.ErrNotFound, 404, "not_found"},
		{workspacefile.ErrForbidden, 403, "forbidden"},
		{workspacefile.ErrLabInstanceNotReady, 409, "lab_instance_not_ready"},
		{workspacefile.ErrTargetUnavailable, 409, "workspace_target_unavailable"},
		{workspacefile.ErrTargetChanged, 409, "workspace_target_changed"},
		{workspacefile.ErrPathNotFound, 404, "path_not_found"},
		{workspacefile.ErrNotAFile, 422, "path_not_file"},
		{workspacefile.ErrNotADirectory, 422, "path_not_directory"},
		{workspacefile.ErrFilePermissionDenied, 403, "file_permission_denied"},
		{workspacefile.ErrStaleRevision, 412, "stale_revision"},
		{workspacefile.ErrTooLarge, 413, "file_too_large"},
		{workspacefile.ErrDirectoryTooLarge, 413, "directory_too_large"},
		{workspacefile.ErrUnsupportedEncoding, 422, "unsupported_encoding"},
		{workspacefile.ErrBinaryContent, 422, "binary_content"},
		{workspacefile.ErrConnectorUnavailable, 503, "connector_unavailable"},
		{workspacefile.ErrTransportUnavailable, 503, "file_transport_unavailable"},
		{workspacefile.ErrSaveOutcomeUnknown, 503, "file_save_outcome_unknown"},
		{context.Canceled, 503, "file_transport_unavailable"},
		{context.DeadlineExceeded, 503, "file_transport_unavailable"},
		{workspacefile.ErrInconsistentData, 500, "internal_error"},
		{errors.New("pq: SQLSTATE 23505 " + leakMarker), 500, "internal_error"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d/%s", tc.status, tc.code), func(t *testing.T) {
			// 오류가 감싸고 있는 원문은 응답에 나타나지 않는다.
			wrapped := fmt.Errorf("%w: %s", tc.err, leakMarker)
			for name, call := range map[string]func(*fileHarness) *httptest.ResponseRecorder{
				"tree": func(h *fileHarness) *httptest.ResponseRecorder {
					h.files.treeErr = wrapped
					return h.tree("?path=a")
				},
				"read": func(h *fileHarness) *httptest.ResponseRecorder {
					h.files.readErr = wrapped
					return h.read("?path=a")
				},
				"save": func(h *fileHarness) *httptest.ResponseRecorder {
					h.files.saveErr = wrapped
					return h.save("?path=a", `{"content":"a"}`)
				},
			} {
				h := newFileHarness(t)
				rec := call(h)
				requireProblem(t, rec, tc.status, tc.code)
				if rec.Header().Get("ETag") != "" {
					t.Fatalf("%s: 오류 응답에 ETag가 있음", name)
				}
				if tc.status == 500 && strings.Contains(h.logs.String(), leakMarker) {
					t.Fatalf("%s: 내부 오류 원문이 log에 남음:\n%s", name, h.logs.String())
				}
			}
		})
	}
}

// 오류 detail에는 경로가 들어가지 않는다.
func TestProblemDetailNeverEchoesThePath(t *testing.T) {
	h := newFileHarness(t)
	h.files.readErr = workspacefile.ErrPathNotFound
	rec := h.read("?path=" + "secret-dir-" + leakMarker + "%2Ffile.txt")
	requireProblem(t, rec, http.StatusNotFound, "path_not_found")
	if logs := h.logs.String(); strings.Contains(logs, leakMarker) {
		t.Fatalf("log에 경로가 남음:\n%s", logs)
	}
}

// File use case가 조립되지 않은 구성은 인증과 Origin 검증을 거친 뒤 503이다.
func TestWorkspaceFilesWithoutAUseCaseAreUnavailable(t *testing.T) {
	fake := newFakeAuth()
	fake.sessions[fileCookie] = fake.principal
	handler, err := New(Options{Auth: fake, Classes: &fakeClasses{}, PublicOrigin: trustedOrigin})
	if err != nil {
		t.Fatal(err)
	}
	h := &fileHarness{harness: &harness{handler: handler, auth: fake}, files: &fakeFiles{}}
	requireProblem(t, h.tree(""), http.StatusServiceUnavailable, "file_transport_unavailable")
	requireProblem(t, h.read("?path=a"), http.StatusServiceUnavailable, "file_transport_unavailable")
	requireProblem(t, h.save("?path=a", `{"content":"a"}`), http.StatusServiceUnavailable, "file_transport_unavailable")
	requireProblem(t, h.tree("", func(r *http.Request) { r.Header.Del("Cookie") }), http.StatusUnauthorized, "unauthenticated")
}

func TestHasLoneSurrogateEscape(t *testing.T) {
	for body, want := range map[string]bool{
		``:                           false,
		`"a"`:                        false,
		`"\ud83d\ude00"`:             false,
		`"\uD83D\uDE00"`:             false,
		`"\ud83d\ude00\ud83d\ude00"`: false,
		`"\\ud800"`:                  false,
		`"\\\\ud800"`:                false,
		`"\ud800"`:                   true,
		`"\udc00"`:                   true,
		`"\ud800\u0041"`:             true,
		`"\ud800\ud800"`:             true,
		`"\ude00\ud83d"`:             true,
		`"\ud83d\ude00\ud800"`:       true,
		`"\\\ud800"`:                 true,
		`"\u12"`:                     false, // 유효한 JSON이 아니다. decode가 거절한다.
		`"\`:                         false,
		`"\ud83d`:                    true,
	} {
		if got := hasLoneSurrogateEscape([]byte(body)); got != want {
			t.Errorf("hasLoneSurrogateEscape(%q) = %v, want %v", body, got, want)
		}
	}
}

// HTTP metric의 route label은 등록된 pattern뿐이다. 파일 경로(query), LabInstance ID, 본문은 label이나 값에 나타나지 않는다.
func TestWorkspaceFileMetricsUseRouteTemplatesOnly(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := observability.NewHTTPMetrics(reg)
	h := newFileHarness(t, func(o *Options) { o.Metrics = m })
	secretPath := "secret-dir-" + leakMarker + "/notes.txt"

	h.tree("?path=" + secretPath)
	h.read("?path=" + secretPath)
	h.save("?path="+secretPath, `{"content":"`+leakMarker+`"}`)
	h.files.readErr = workspacefile.ErrPathNotFound
	h.read("?path=" + secretPath)

	for _, want := range []struct {
		method, route, status string
		count                 float64
	}{
		{"GET", "/api/v1/lab-instances/{labInstanceId}/files/tree", "2xx", 1},
		{"GET", "/api/v1/lab-instances/{labInstanceId}/files/content", "2xx", 1},
		{"GET", "/api/v1/lab-instances/{labInstanceId}/files/content", "4xx", 1},
		{"PUT", "/api/v1/lab-instances/{labInstanceId}/files/content", "2xx", 1},
	} {
		if got := testutil.ToFloat64(m.Requests.WithLabelValues(want.method, want.route, want.status)); got != want.count {
			t.Errorf("requests{%s,%s,%s} = %g, want %g", want.method, want.route, want.status, got, want.count)
		}
	}
	rec := httptest.NewRecorder()
	promhttp.HandlerFor(reg, promhttp.HandlerOpts{}).ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	for _, forbidden := range []string{leakMarker, "secret-dir", "notes.txt", labInstanceID, "path="} {
		if strings.Contains(body, forbidden) {
			t.Errorf("metrics에 민감한 값이 있음: %q", forbidden)
		}
	}
}
