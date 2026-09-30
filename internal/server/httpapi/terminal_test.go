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
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal"
)

const (
	terminalCookie = "terminal-test-session-cookie"
	// 이 값이 log나 오류 응답에 나타나면 안 된다. 실제 token이 아니다.
	issuedAttachToken = "issued-attach-token-for-http-test-0123456789abcdef"
)

// fakeTerminals는 use case의 결과를 HTTP로 옮기는 방식만 검증하기 위한 fake다.
type fakeTerminals struct {
	createIn  []terminal.CreateInput
	createFor []repository.User
	closeIDs  []string

	created   terminal.Created
	createErr error
	closeErr  error
}

func (f *fakeTerminals) Create(_ context.Context, user repository.User, in terminal.CreateInput) (terminal.Created, error) {
	f.createIn = append(f.createIn, in)
	f.createFor = append(f.createFor, user)
	return f.created, f.createErr
}

func (f *fakeTerminals) Close(_ context.Context, _ repository.User, id string) error {
	f.closeIDs = append(f.closeIDs, id)
	return f.closeErr
}

type terminalHarness struct {
	*harness
	terminals *fakeTerminals
	expires   time.Time
	sessionID uuid.UUID
}

// newTerminalHarness는 Terminals를 가진 handler와 이미 로그인된 Session을 만든다.
func newTerminalHarness(t *testing.T) *terminalHarness {
	t.Helper()
	fake := newFakeAuth()
	fake.sessions[terminalCookie] = fake.principal
	classes := &fakeClasses{}
	logs := &bytes.Buffer{}
	h := &terminalHarness{
		terminals: &fakeTerminals{},
		expires:   time.Date(2026, 10, 1, 17, 0, 0, 0, time.UTC),
		sessionID: uuid.New(),
	}
	h.terminals.created = terminal.Created{
		ID: h.sessionID, Generation: 3, Token: realtime.AttachToken(issuedAttachToken), TokenExpiresAt: h.expires,
	}
	handler, err := New(Options{
		Auth:         fake,
		Classes:      classes,
		Terminals:    h.terminals,
		PublicOrigin: trustedOrigin,
		Logger:       slog.New(slog.NewJSONHandler(logs, nil)),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	h.harness = &harness{handler: handler, auth: fake, classes: classes, logs: logs}
	return h
}

const validCreateBody = `{"targetVmKey":"workspace","cols":120,"rows":40}`

func (h *terminalHarness) create(body string, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	base := []func(*http.Request){withCookie(terminalCookie), withHeader("Origin", trustedOrigin), withHeader("Content-Type", "application/json")}
	return h.send(http.MethodPost, "/api/v1/lab-instances/"+labInstanceID+"/terminal-sessions", body, append(base, mods...)...)
}

func (h *terminalHarness) remove(id string, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	base := []func(*http.Request){withCookie(terminalCookie), withHeader("Origin", trustedOrigin)}
	return h.send(http.MethodDelete, "/api/v1/terminal-sessions/"+id, "", append(base, mods...)...)
}

const labInstanceID = "30000000-0000-4000-8000-000000000001"

func TestCreateTerminalSessionReturnsTokenOnce(t *testing.T) {
	h := newTerminalHarness(t)

	rec := h.create(validCreateBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	// sessionToken 응답은 cache되지 않아야 한다.
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"id": h.sessionID.String(), "generation": float64(3), "sessionToken": issuedAttachToken,
		"tokenExpiresAt": "2026-10-01T17:00:00Z",
	}
	if fmt.Sprint(body) != fmt.Sprint(want) {
		t.Fatalf("body = %v, want %v", body, want)
	}

	// use case에는 경로의 LabInstance ID와 본문의 값만 전달한다. Provider Server ID, Connector ID, generation은 받지 않는다.
	if len(h.terminals.createIn) != 1 {
		t.Fatalf("Create 호출 = %d, want 1", len(h.terminals.createIn))
	}
	in := h.terminals.createIn[0]
	if in.LabInstanceID != labInstanceID || in.TargetVMKey != "workspace" || string(in.Cols) != "120" || string(in.Rows) != "40" {
		t.Fatalf("CreateInput = %+v", in)
	}
	if _, err := uuid.Parse(in.RequestID); err != nil {
		t.Fatalf("RequestID = %q, want generated correlation id", in.RequestID)
	}
	if h.terminals.createFor[0].ID != h.auth.principal.User.ID {
		t.Fatal("현재 인증된 사용자가 use case에 전달되지 않음")
	}

	// token 원문은 응답 본문에서만 나간다.
	if strings.Contains(h.logs.String(), issuedAttachToken) {
		t.Fatal("log에 attach token이 남음")
	}
}

// Schema는 cols/rows를 integer, minimum 1, maximum 없음으로 정의한다. 1.0, 1e2도 integer이며 값을 바꾸지 않고 전달한다.
func TestCreateTerminalSessionAcceptsEverySchemaValidSize(t *testing.T) {
	for _, tt := range []struct{ cols, rows string }{
		{"1", "1"},
		{"1.0", "1e2"},
		{"100E-2", "9007199254740993"},
		{"123456789012345678901234567890", "1e30"},
	} {
		t.Run(tt.cols+"x"+tt.rows, func(t *testing.T) {
			h := newTerminalHarness(t)
			rec := h.create(fmt.Sprintf(`{"targetVmKey":"workspace","cols":%s,"rows":%s}`, tt.cols, tt.rows))
			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
			}
			in := h.terminals.createIn[0]
			if string(in.Cols) != tt.cols || string(in.Rows) != tt.rows {
				t.Fatalf("cols/rows = %s/%s, want raw %s/%s", in.Cols, in.Rows, tt.cols, tt.rows)
			}
		})
	}
}

func TestCreateTerminalSessionRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name string
		body string
		mods []func(*http.Request)
	}{
		{name: "empty body", body: ``},
		{name: "not json", body: `targetVmKey=workspace`},
		{name: "json array", body: `[]`},
		{name: "missing targetVmKey", body: `{"cols":80,"rows":24}`},
		{name: "empty targetVmKey", body: `{"targetVmKey":"","cols":80,"rows":24}`},
		{name: "targetVmKey not a string", body: `{"targetVmKey":7,"cols":80,"rows":24}`},
		{name: "missing cols", body: `{"targetVmKey":"w","rows":24}`},
		{name: "missing rows", body: `{"targetVmKey":"w","cols":80}`},
		{name: "cols zero", body: `{"targetVmKey":"w","cols":0,"rows":24}`},
		{name: "rows negative", body: `{"targetVmKey":"w","cols":80,"rows":-1}`},
		{name: "cols fraction", body: `{"targetVmKey":"w","cols":1.5,"rows":24}`},
		{name: "cols as string", body: `{"targetVmKey":"w","cols":"80","rows":24}`},
		{name: "cols null", body: `{"targetVmKey":"w","cols":null,"rows":24}`},
		{name: "cols boolean", body: `{"targetVmKey":"w","cols":true,"rows":24}`},
		// Provider Server ID, Connector ID, generation은 요청으로 받지 않는다(additionalProperties: false).
		{name: "providerServerId is not accepted", body: `{"targetVmKey":"w","cols":80,"rows":24,"providerServerId":"srv-1"}`},
		{name: "connectorId is not accepted", body: `{"targetVmKey":"w","cols":80,"rows":24,"connectorId":"c"}`},
		{name: "generation is not accepted", body: `{"targetVmKey":"w","cols":80,"rows":24,"generation":1}`},
		{name: "two json values", body: `{"targetVmKey":"w","cols":80,"rows":24}{"x":1}`},
		{name: "trailing garbage", body: `{"targetVmKey":"w","cols":80,"rows":24} x`},
		{name: "wrong content type", body: validCreateBody, mods: []func(*http.Request){withHeader("Content-Type", "text/plain")}},
		{name: "missing content type", body: validCreateBody, mods: []func(*http.Request){func(r *http.Request) { r.Header.Del("Content-Type") }}},
		{name: "oversized body", body: `{"targetVmKey":"` + strings.Repeat("x", maxTerminalBodyBytes) + `","cols":80,"rows":24}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTerminalHarness(t)
			rec := h.create(tt.body, tt.mods...)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			if problem := decodeProblem(t, rec); problem.Code != codeInvalidRequest {
				t.Fatalf("problem.code = %q, want %q", problem.Code, codeInvalidRequest)
			}
			if len(h.terminals.createIn) != 0 {
				t.Fatal("잘못된 요청이 use case까지 전달됨")
			}
		})
	}
}

// 인증과 Origin 검증은 use case를 호출하기 전에 끝난다.
func TestTerminalEndpointsRequireAuthenticationAndOrigin(t *testing.T) {
	t.Run("create without session", func(t *testing.T) {
		h := newTerminalHarness(t)
		rec := h.send(http.MethodPost, "/api/v1/lab-instances/"+labInstanceID+"/terminal-sessions", validCreateBody,
			withHeader("Origin", trustedOrigin), withHeader("Content-Type", "application/json"))
		if rec.Code != http.StatusUnauthorized || decodeProblem(t, rec).Code != codeUnauthenticated {
			t.Fatalf("status = %d, want 401 unauthenticated", rec.Code)
		}
		if len(h.terminals.createIn) != 0 {
			t.Fatal("인증 없는 요청이 use case까지 전달됨")
		}
	})
	t.Run("create with unknown session clears the stale cookie", func(t *testing.T) {
		h := newTerminalHarness(t)
		req := httptest.NewRequest(http.MethodPost, trustedOrigin+"/api/v1/lab-instances/"+labInstanceID+"/terminal-sessions", strings.NewReader(validCreateBody))
		req.Header.Set("Origin", trustedOrigin)
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "unknown"})
		rec := httptest.NewRecorder()
		h.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
		if stale := sessionCookieFrom(t, rec); stale.MaxAge != -1 {
			t.Fatalf("stale Cookie가 제거되지 않음: %+v", stale)
		}
		if len(h.terminals.createIn) != 0 {
			t.Fatal("유효하지 않은 Session의 요청이 use case까지 전달됨")
		}
	})
	for _, source := range []struct {
		name string
		mods []func(*http.Request)
	}{
		{name: "missing origin and referer", mods: []func(*http.Request){withoutSource}},
		{name: "foreign origin", mods: []func(*http.Request){withHeader("Origin", "https://evil.test")}},
		{name: "foreign referer", mods: []func(*http.Request){withoutSource, withHeader("Referer", "https://evil.test/x")}},
	} {
		t.Run("create "+source.name, func(t *testing.T) {
			h := newTerminalHarness(t)
			rec := h.create(validCreateBody, source.mods...)
			if rec.Code != http.StatusForbidden || decodeProblem(t, rec).Code != codeCSRFRejected {
				t.Fatalf("status = %d, want 403 csrf_rejected", rec.Code)
			}
			if len(h.terminals.createIn) != 0 {
				t.Fatal("출처가 거절된 요청이 use case까지 전달됨")
			}
		})
		t.Run("delete "+source.name, func(t *testing.T) {
			h := newTerminalHarness(t)
			rec := h.remove(h.sessionID.String(), source.mods...)
			if rec.Code != http.StatusForbidden || decodeProblem(t, rec).Code != codeCSRFRejected {
				t.Fatalf("status = %d, want 403 csrf_rejected", rec.Code)
			}
			if len(h.terminals.closeIDs) != 0 {
				t.Fatal("출처가 거절된 요청이 use case까지 전달됨")
			}
		})
	}
	t.Run("delete without session", func(t *testing.T) {
		h := newTerminalHarness(t)
		rec := h.send(http.MethodDelete, "/api/v1/terminal-sessions/"+h.sessionID.String(), "", withHeader("Origin", trustedOrigin))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
		if len(h.terminals.closeIDs) != 0 {
			t.Fatal("인증 없는 요청이 use case까지 전달됨")
		}
	})
}

func TestTerminalUseCaseErrorsMapToOpenAPIStatus(t *testing.T) {
	// repository.Error는 DB 원문을 포함할 수 있다. 500 응답과 log에 그 원문이 나오면 안 된다.
	dbError := &repository.Error{Kind: repository.KindInternal, Op: "TerminalSessionByID", SQLState: "XX000", Constraint: "fk_secret_constraint", Cause: errors.New("dsn=postgres://user:secret-password@db/labbit")}

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "lab instance or session not found", err: terminal.ErrNotFound, wantStatus: 404, wantCode: codeNotFound},
		{name: "not the owner or no class membership", err: terminal.ErrForbidden, wantStatus: 403, wantCode: codeForbidden},
		{name: "lab instance not ready", err: terminal.ErrLabInstanceNotReady, wantStatus: 409, wantCode: codeLabInstanceNotReady},
		{name: "target vm not usable", err: terminal.ErrTargetUnavailable, wantStatus: 409, wantCode: codeTerminalTargetNotReady},
		{name: "target vm unknown", err: terminal.ErrTargetNotFound, wantStatus: 422, wantCode: codeTerminalTargetInvalid},
		{name: "connector unavailable", err: terminal.ErrConnectorUnavailable, wantStatus: 503, wantCode: codeConnectorUnavailable},
		{name: "open failed", err: terminal.ErrOpenFailed, wantStatus: 503, wantCode: codeTerminalOpenFailed},
		{name: "relay unavailable in this topology", err: terminal.ErrUnavailable, wantStatus: 503, wantCode: codeTerminalUnavailable},
		{name: "wrapped typed error", err: fmt.Errorf("wrapped: %w", terminal.ErrForbidden), wantStatus: 403, wantCode: codeForbidden},
		{name: "inconsistent data fails closed", err: terminal.ErrInconsistentData, wantStatus: 500, wantCode: codeInternal},
		{name: "repository error", err: fmt.Errorf("terminal: 조회: %w", dbError), wantStatus: 500, wantCode: codeInternal},
		{name: "unclassified error", err: errors.New("boom: secret-detail"), wantStatus: 500, wantCode: codeInternal},
	}
	for _, tt := range tests {
		t.Run("create "+tt.name, func(t *testing.T) {
			h := newTerminalHarness(t)
			h.terminals.createErr = tt.err
			rec := h.create(validCreateBody)
			h.assertProblem(t, rec, tt.wantStatus, tt.wantCode)
		})
		if tt.err == terminal.ErrLabInstanceNotReady || tt.err == terminal.ErrTargetUnavailable || tt.err == terminal.ErrTargetNotFound ||
			tt.err == terminal.ErrConnectorUnavailable || tt.err == terminal.ErrOpenFailed {
			continue // 종료 요청에서는 나오지 않는 오류다.
		}
		t.Run("delete "+tt.name, func(t *testing.T) {
			h := newTerminalHarness(t)
			h.terminals.closeErr = tt.err
			rec := h.remove(h.sessionID.String())
			h.assertProblem(t, rec, tt.wantStatus, tt.wantCode)
		})
	}
}

func (h *terminalHarness) assertProblem(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d: %s", rec.Code, wantStatus, rec.Body.String())
	}
	problem := decodeProblem(t, rec)
	if problem.Code != wantCode {
		t.Fatalf("problem.code = %q, want %q", problem.Code, wantCode)
	}
	// 응답에는 어떤 내부 오류 문자열도 싣지 않는다. log에는 오류 원문(Cause, DSN)을 남기지 않는다.
	// repository.Error의 SQLSTATE와 constraint 이름은 기존 설계상 log 전용 진단 metadata이므로 log에는 허용하지만 응답에는 싣지 않는다.
	for _, internal := range []string{"secret-password", "fk_secret_constraint", "XX000", "secret-detail", "dsn="} {
		if strings.Contains(rec.Body.String(), internal) {
			t.Fatalf("응답이 %q를 포함함: %s", internal, rec.Body.String())
		}
	}
	for _, raw := range []string{"secret-password", "secret-detail", "dsn="} {
		if strings.Contains(h.logs.String(), raw) {
			t.Fatalf("log가 오류 원문 %q를 포함함: %s", raw, h.logs.String())
		}
	}
}

func TestCloseTerminalSession(t *testing.T) {
	h := newTerminalHarness(t)
	id := h.sessionID.String()

	rec := h.remove(id)
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("status = %d body %q, want 204 empty", rec.Code, rec.Body.String())
	}
	if len(h.terminals.closeIDs) != 1 || h.terminals.closeIDs[0] != id {
		t.Fatalf("Close 호출 = %v, want [%s]", h.terminals.closeIDs, id)
	}
	// 같은 종료 요청을 다시 보내도 204다(멱등). 멱등성은 use case가 보장하며 handler는 결과를 그대로 옮긴다.
	if rec := h.remove(id); rec.Code != http.StatusNoContent {
		t.Fatalf("repeat status = %d, want 204", rec.Code)
	}
}

// Terminal Relay가 없는 배포 구성(Terminals 없음)은 인증과 출처 검증을 거친 뒤 명확한 503으로 응답한다.
func TestTerminalEndpointsWithoutRelayAreUnavailable(t *testing.T) {
	h := newHarness(t) // Terminals를 주입하지 않는다.
	h.auth.sessions[terminalCookie] = h.auth.principal

	rec := h.send(http.MethodPost, "/api/v1/lab-instances/"+labInstanceID+"/terminal-sessions", validCreateBody,
		withCookie(terminalCookie), withHeader("Origin", trustedOrigin), withHeader("Content-Type", "application/json"))
	if rec.Code != http.StatusServiceUnavailable || decodeProblem(t, rec).Code != codeTerminalUnavailable {
		t.Fatalf("create status = %d, want 503 terminal_unavailable: %s", rec.Code, rec.Body.String())
	}
	rec = h.send(http.MethodDelete, "/api/v1/terminal-sessions/"+uuid.NewString(), "", withCookie(terminalCookie), withHeader("Origin", trustedOrigin))
	if rec.Code != http.StatusServiceUnavailable || decodeProblem(t, rec).Code != codeTerminalUnavailable {
		t.Fatalf("delete status = %d, want 503 terminal_unavailable", rec.Code)
	}
	// 인증 없는 요청은 여전히 401이다(존재 여부를 인증 없이 알려 주지 않는다).
	rec = h.send(http.MethodPost, "/api/v1/lab-instances/"+labInstanceID+"/terminal-sessions", validCreateBody,
		withHeader("Origin", trustedOrigin), withHeader("Content-Type", "application/json"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", rec.Code)
	}
}

// 지원하지 않는 method는 use case에 도달하지 않는다. GET/list/refresh API는 없다.
func TestTerminalSessionHasNoOtherOperations(t *testing.T) {
	h := newTerminalHarness(t)
	for _, tt := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/terminal-sessions/" + h.sessionID.String()},
		{http.MethodGet, "/api/v1/lab-instances/" + labInstanceID + "/terminal-sessions"},
		{http.MethodPut, "/api/v1/terminal-sessions/" + h.sessionID.String()},
		{http.MethodPost, "/api/v1/terminal-sessions/" + h.sessionID.String() + "/token"},
		{http.MethodPost, "/api/v1/terminal-sessions/" + h.sessionID.String() + "/refresh"},
	} {
		rec := h.send(tt.method, tt.path, "", withCookie(terminalCookie), withHeader("Origin", trustedOrigin), withHeader("Content-Type", "application/json"))
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s status = %d, want 404 or 405", tt.method, tt.path, rec.Code)
		}
	}
	if len(h.terminals.createIn)+len(h.terminals.closeIDs) != 0 {
		t.Fatal("정의되지 않은 endpoint가 use case를 호출함")
	}
}
