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

	"github.com/ktcloud4-SL/labbit-app/internal/server/previewsession"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

const (
	previewCookie = "preview-test-session-cookie"
	// bootstrap credential의 marker다. 응답 본문에서만 나가며 log에 나타나면 안 된다. 실제 credential이 아니다.
	previewCredentialMarker = "PREVIEW-BOOTSTRAP-CREDENTIAL-MARKER-7c2e"
)

// fakePreviews는 use case의 결과를 HTTP로 옮기는 방식만 검증하기 위한 fake다.
type fakePreviews struct {
	createIn  []previewsession.CreateInput
	createFor []repository.User
	closeIDs  []string
	closeFor  []repository.User

	created   previewsession.Created
	createErr error
	closeErr  error
}

func (f *fakePreviews) Create(_ context.Context, user repository.User, in previewsession.CreateInput) (previewsession.Created, error) {
	f.createIn = append(f.createIn, in)
	f.createFor = append(f.createFor, user)
	return f.created, f.createErr
}

func (f *fakePreviews) Close(_ context.Context, user repository.User, id string) error {
	f.closeIDs = append(f.closeIDs, id)
	f.closeFor = append(f.closeFor, user)
	return f.closeErr
}

type previewHarness struct {
	*harness
	previews  *fakePreviews
	expires   time.Time
	sessionID string
}

func newPreviewHarness(t *testing.T) *previewHarness {
	t.Helper()
	fake := newFakeAuth()
	fake.sessions[previewCookie] = fake.principal
	classes := &fakeClasses{}
	logs := &bytes.Buffer{}
	h := &previewHarness{
		previews:  &fakePreviews{},
		expires:   time.Date(2026, 10, 6, 18, 30, 0, 0, time.UTC),
		sessionID: uuid.NewString(),
	}
	h.previews.created = previewsession.Created{
		ID: h.sessionID, TargetPort: 3000,
		PreviewURL: "https://" + h.sessionID + ".preview.example.test/__labbit/bootstrap#" + previewCredentialMarker,
		ExpiresAt:  h.expires,
	}
	handler, err := New(Options{
		Auth:         fake,
		Classes:      classes,
		Previews:     h.previews,
		PublicOrigin: trustedOrigin,
		Logger:       slog.New(slog.NewJSONHandler(logs, nil)),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	h.harness = &harness{handler: handler, auth: fake, classes: classes, logs: logs}
	return h
}

const validPreviewBody = `{"targetPort":3000}`

func (h *previewHarness) create(body string, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	base := []func(*http.Request){withCookie(previewCookie), withHeader("Origin", trustedOrigin), withHeader("Content-Type", "application/json")}
	return h.send(http.MethodPost, "/api/v1/lab-instances/"+labInstanceID+"/preview-sessions", body, append(base, mods...)...)
}

func (h *previewHarness) remove(id string, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	base := []func(*http.Request){withCookie(previewCookie), withHeader("Origin", trustedOrigin)}
	return h.send(http.MethodDelete, "/api/v1/preview-sessions/"+id, "", append(base, mods...)...)
}

func TestCreatePreviewSessionReturnsThePreviewURLOnce(t *testing.T) {
	h := newPreviewHarness(t)

	rec := h.create(validPreviewBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	// previewUrl의 fragment에 일회용 credential이 있으므로 응답은 cache되지 않아야 한다.
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"id": h.sessionID, "targetPort": float64(3000),
		"previewUrl": "https://" + h.sessionID + ".preview.example.test/__labbit/bootstrap#" + previewCredentialMarker,
		"expiresAt":  "2026-10-06T18:30:00Z",
	}
	if fmt.Sprint(body) != fmt.Sprint(want) {
		t.Fatalf("body = %v, want %v", body, want)
	}

	// use case에는 경로의 LabInstance ID와 본문의 port만 전달한다. Provider Server ID, Connector ID, generation, VM key는 받지 않는다.
	if len(h.previews.createIn) != 1 {
		t.Fatalf("Create 호출 = %d, want 1", len(h.previews.createIn))
	}
	in := h.previews.createIn[0]
	if in.LabInstanceID != labInstanceID || in.TargetPort != 3000 {
		t.Fatalf("CreateInput = %+v", in)
	}
	if _, err := uuid.Parse(in.RequestID); err != nil {
		t.Fatalf("RequestID = %q, want generated correlation id", in.RequestID)
	}
	if h.previews.createFor[0].ID != h.auth.principal.User.ID {
		t.Fatal("현재 인증된 사용자가 use case에 전달되지 않음")
	}

	// bootstrap credential은 응답 본문에서만 나간다.
	if strings.Contains(h.logs.String(), previewCredentialMarker) {
		t.Fatal("log에 bootstrap credential이 남음")
	}
}

// Schema는 targetPort를 integer, minimum 1, maximum 65535로 정의한다. 3000.0, 3e3도 integer이며 같은 port다.
func TestCreatePreviewSessionAcceptsEverySchemaValidPort(t *testing.T) {
	for _, tt := range []struct {
		raw  string
		want int
	}{
		{"1", 1}, {"22", 22}, {"80", 80}, {"3000", 3000}, {"65535", 65535},
		{"3000.0", 3000}, {"3e3", 3000}, {"3E3", 3000}, {"30e2", 3000}, {"5173.000", 5173},
	} {
		t.Run(tt.raw, func(t *testing.T) {
			h := newPreviewHarness(t)
			rec := h.create(`{"targetPort":` + tt.raw + `}`)
			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
			}
			if got := h.previews.createIn[0].TargetPort; got != tt.want {
				t.Fatalf("TargetPort = %d, want %d", got, tt.want)
			}
		})
	}
}

// HTTP layer의 검증은 형식뿐이다. 어떤 port를 승인할지는 Backend 허용 목록이 정하며 이 layer가 default나 범위를 정하지 않는다.
func TestCreatePreviewSessionPassesTheRequestedPortThroughWithoutApprovingIt(t *testing.T) {
	h := newPreviewHarness(t)
	h.previews.createErr = previewsession.ErrPortNotAllowed
	rec := h.create(`{"targetPort":22}`)
	h.assertProblem(t, rec, http.StatusForbidden, codePreviewPortNotAllowed)
	if len(h.previews.createIn) != 1 || h.previews.createIn[0].TargetPort != 22 {
		t.Fatalf("22를 use case에 넘기지 않음: %+v", h.previews.createIn)
	}
}

func TestCreatePreviewSessionRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name string
		body string
		mods []func(*http.Request)
	}{
		{name: "empty body", body: ``},
		{name: "not json", body: `targetPort=3000`},
		{name: "json array", body: `[]`},
		{name: "empty object", body: `{}`},
		{name: "missing targetPort", body: `{"port":3000}`},
		{name: "targetPort zero", body: `{"targetPort":0}`},
		{name: "targetPort negative", body: `{"targetPort":-1}`},
		{name: "targetPort 65536", body: `{"targetPort":65536}`},
		{name: "targetPort huge", body: `{"targetPort":99999999999999999999}`},
		{name: "targetPort huge exponent", body: `{"targetPort":1e30}`},
		{name: "targetPort fraction", body: `{"targetPort":3000.5}`},
		{name: "targetPort as string", body: `{"targetPort":"3000"}`},
		{name: "targetPort null", body: `{"targetPort":null}`},
		{name: "targetPort boolean", body: `{"targetPort":true}`},
		{name: "targetPort array", body: `{"targetPort":[3000]}`},
		{name: "targetPort range string", body: `{"targetPort":"3000-3010"}`},
		// VM, Provider Server ID, Connector ID, generation, private address는 요청으로 받지 않는다(additionalProperties: false).
		{name: "targetVmKey is not accepted", body: `{"targetPort":3000,"targetVmKey":"workspace"}`},
		{name: "providerServerId is not accepted", body: `{"targetPort":3000,"providerServerId":"srv-1"}`},
		{name: "connectorId is not accepted", body: `{"targetPort":3000,"connectorId":"c"}`},
		{name: "generation is not accepted", body: `{"targetPort":3000,"generation":1}`},
		{name: "private ip is not accepted", body: `{"targetPort":3000,"host":"10.0.0.5"}`},
		{name: "two json values", body: `{"targetPort":3000}{"targetPort":3001}`},
		{name: "trailing garbage", body: `{"targetPort":3000} x`},
		{name: "wrong content type", body: validPreviewBody, mods: []func(*http.Request){withHeader("Content-Type", "text/plain")}},
		{name: "form content type", body: `targetPort=3000`, mods: []func(*http.Request){withHeader("Content-Type", "application/x-www-form-urlencoded")}},
		{name: "missing content type", body: validPreviewBody, mods: []func(*http.Request){func(r *http.Request) { r.Header.Del("Content-Type") }}},
		{name: "oversized body", body: `{"targetPort":3000,"x":"` + strings.Repeat("x", maxPreviewBodyBytes) + `"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newPreviewHarness(t)
			rec := h.create(tt.body, tt.mods...)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			if problem := decodeProblem(t, rec); problem.Code != codeInvalidRequest {
				t.Fatalf("problem.code = %q, want %q", problem.Code, codeInvalidRequest)
			}
			if len(h.previews.createIn) != 0 {
				t.Fatal("잘못된 요청이 use case까지 전달됨")
			}
		})
	}
}

// 인증과 Origin 검증은 use case를 호출하기 전에 끝난다.
func TestPreviewEndpointsRequireAuthenticationAndOrigin(t *testing.T) {
	t.Run("create without session", func(t *testing.T) {
		h := newPreviewHarness(t)
		rec := h.send(http.MethodPost, "/api/v1/lab-instances/"+labInstanceID+"/preview-sessions", validPreviewBody,
			withHeader("Origin", trustedOrigin), withHeader("Content-Type", "application/json"))
		if rec.Code != http.StatusUnauthorized || decodeProblem(t, rec).Code != codeUnauthenticated {
			t.Fatalf("status = %d, want 401 unauthenticated", rec.Code)
		}
		if len(h.previews.createIn) != 0 {
			t.Fatal("인증 없는 요청이 use case까지 전달됨")
		}
	})
	t.Run("create with unknown session clears the stale cookie", func(t *testing.T) {
		h := newPreviewHarness(t)
		req := httptest.NewRequest(http.MethodPost, trustedOrigin+"/api/v1/lab-instances/"+labInstanceID+"/preview-sessions", strings.NewReader(validPreviewBody))
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
		if len(h.previews.createIn) != 0 {
			t.Fatal("유효하지 않은 Session의 요청이 use case까지 전달됨")
		}
	})
	for _, source := range []struct {
		name string
		mods []func(*http.Request)
	}{
		{name: "missing origin and referer", mods: []func(*http.Request){withoutSource}},
		{name: "foreign origin", mods: []func(*http.Request){withHeader("Origin", "https://evil.test")}},
		// Preview Origin에서 온 요청도 본 서비스 API의 trusted origin이 아니다. 사용자 코드가 SaaS API를 호출할 수 없다.
		{name: "preview origin", mods: []func(*http.Request){withHeader("Origin", "https://"+uuid.NewString()+".preview.example.test")}},
		{name: "foreign referer", mods: []func(*http.Request){withoutSource, withHeader("Referer", "https://evil.test/x")}},
	} {
		t.Run("create "+source.name, func(t *testing.T) {
			h := newPreviewHarness(t)
			rec := h.create(validPreviewBody, source.mods...)
			if rec.Code != http.StatusForbidden || decodeProblem(t, rec).Code != codeCSRFRejected {
				t.Fatalf("status = %d, want 403 csrf_rejected", rec.Code)
			}
			if len(h.previews.createIn) != 0 {
				t.Fatal("출처가 거절된 요청이 use case까지 전달됨")
			}
		})
		t.Run("delete "+source.name, func(t *testing.T) {
			h := newPreviewHarness(t)
			rec := h.remove(h.sessionID, source.mods...)
			if rec.Code != http.StatusForbidden || decodeProblem(t, rec).Code != codeCSRFRejected {
				t.Fatalf("status = %d, want 403 csrf_rejected", rec.Code)
			}
			if len(h.previews.closeIDs) != 0 {
				t.Fatal("출처가 거절된 요청이 use case까지 전달됨")
			}
		})
	}
	t.Run("delete without session", func(t *testing.T) {
		h := newPreviewHarness(t)
		rec := h.send(http.MethodDelete, "/api/v1/preview-sessions/"+h.sessionID, "", withHeader("Origin", trustedOrigin))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
		if len(h.previews.closeIDs) != 0 {
			t.Fatal("인증 없는 요청이 use case까지 전달됨")
		}
	})
}

func TestPreviewUseCaseErrorsMapToOpenAPIStatus(t *testing.T) {
	// repository.Error는 DB 원문을 포함할 수 있다. 500 응답과 log에 그 원문이 나오면 안 된다.
	dbError := &repository.Error{Kind: repository.KindInternal, Op: "LabInstanceForShare", SQLState: "XX000", Constraint: "fk_secret_constraint", Cause: errors.New("dsn=postgres://user:secret-password@db/labbit")}

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "invalid port", err: previewsession.ErrInvalidPort, wantStatus: 400, wantCode: codeInvalidRequest},
		{name: "lab instance or session not found", err: previewsession.ErrNotFound, wantStatus: 404, wantCode: codeNotFound},
		{name: "not the owner or no class membership", err: previewsession.ErrForbidden, wantStatus: 403, wantCode: codeForbidden},
		{name: "port not allowed", err: previewsession.ErrPortNotAllowed, wantStatus: 403, wantCode: codePreviewPortNotAllowed},
		{name: "port rejected by the connector", err: previewsession.ErrPortRejected, wantStatus: 403, wantCode: codePreviewPortRejected},
		{name: "lab instance not ready", err: previewsession.ErrLabInstanceNotReady, wantStatus: 409, wantCode: codeLabInstanceNotReady},
		{name: "workspace vm not usable", err: previewsession.ErrTargetUnavailable, wantStatus: 409, wantCode: codeWorkspaceUnavailable},
		{name: "workspace changed while opening", err: previewsession.ErrTargetChanged, wantStatus: 409, wantCode: codeWorkspaceChanged},
		{name: "app not running", err: previewsession.ErrAppNotRunning, wantStatus: 502, wantCode: codePreviewAppNotRunning},
		{name: "vm unreachable", err: previewsession.ErrTargetUnreachable, wantStatus: 504, wantCode: codePreviewTargetUnreachable},
		{name: "open timeout", err: previewsession.ErrOpenTimeout, wantStatus: 504, wantCode: codePreviewOpenTimeout},
		{name: "connector unavailable", err: previewsession.ErrConnectorUnavailable, wantStatus: 503, wantCode: codeConnectorUnavailable},
		{name: "connector lacks preview-v1", err: previewsession.ErrTransportUnsupported, wantStatus: 503, wantCode: codePreviewTransportUnavailable},
		{name: "open failed", err: previewsession.ErrOpenFailed, wantStatus: 503, wantCode: codePreviewOpenFailed},
		{name: "canceled request", err: context.Canceled, wantStatus: 503, wantCode: codePreviewOpenFailed},
		{name: "deadline exceeded", err: context.DeadlineExceeded, wantStatus: 503, wantCode: codePreviewOpenFailed},
		{name: "gateway unavailable in this topology", err: previewsession.ErrUnavailable, wantStatus: 503, wantCode: codePreviewUnavailable},
		{name: "wrapped typed error", err: fmt.Errorf("wrapped: %w", previewsession.ErrForbidden), wantStatus: 403, wantCode: codeForbidden},
		{name: "inconsistent data fails closed", err: previewsession.ErrInconsistentData, wantStatus: 500, wantCode: codeInternal},
		{name: "repository error", err: fmt.Errorf("previewsession: 조회: %w", dbError), wantStatus: 500, wantCode: codeInternal},
		{name: "unclassified error", err: errors.New("boom: secret-detail"), wantStatus: 500, wantCode: codeInternal},
	}
	for _, tt := range tests {
		t.Run("create "+tt.name, func(t *testing.T) {
			h := newPreviewHarness(t)
			h.previews.createErr = tt.err
			rec := h.create(validPreviewBody)
			h.assertProblem(t, rec, tt.wantStatus, tt.wantCode)
		})
	}
	// 종료 요청에서 나올 수 있는 오류만 확인한다(생성 중에만 나오는 오류는 제외).
	onDelete := map[string]bool{
		"lab instance or session not found": true, "not the owner or no class membership": true,
		"gateway unavailable in this topology": true, "wrapped typed error": true,
		"inconsistent data fails closed": true, "repository error": true, "unclassified error": true,
	}
	for _, tt := range tests {
		if !onDelete[tt.name] {
			continue
		}
		t.Run("delete "+tt.name, func(t *testing.T) {
			h := newPreviewHarness(t)
			h.previews.closeErr = tt.err
			rec := h.remove(h.sessionID)
			h.assertProblem(t, rec, tt.wantStatus, tt.wantCode)
		})
	}
}

func (h *previewHarness) assertProblem(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d: %s", rec.Code, wantStatus, rec.Body.String())
	}
	problem := decodeProblem(t, rec)
	if problem.Code != wantCode {
		t.Fatalf("problem.code = %q, want %q", problem.Code, wantCode)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("Content-Type = %q", got)
	}
	// 응답에는 어떤 내부 오류 문자열이나 bootstrap credential도 싣지 않는다. log에는 오류 원문(Cause, DSN)을 남기지 않는다.
	for _, internal := range []string{"secret-password", "fk_secret_constraint", "XX000", "secret-detail", "dsn=", previewCredentialMarker} {
		if strings.Contains(rec.Body.String(), internal) {
			t.Fatalf("응답이 %q를 포함함: %s", internal, rec.Body.String())
		}
	}
	for _, raw := range []string{"secret-password", "secret-detail", "dsn=", previewCredentialMarker} {
		if strings.Contains(h.logs.String(), raw) {
			t.Fatalf("log가 %q를 포함함: %s", raw, h.logs.String())
		}
	}
}

func TestClosePreviewSession(t *testing.T) {
	h := newPreviewHarness(t)

	rec := h.remove(h.sessionID)
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("status = %d body %q, want 204 empty", rec.Code, rec.Body.String())
	}
	if len(h.previews.closeIDs) != 1 || h.previews.closeIDs[0] != h.sessionID {
		t.Fatalf("Close 호출 = %v, want [%s]", h.previews.closeIDs, h.sessionID)
	}
	if h.previews.closeFor[0].ID != h.auth.principal.User.ID {
		t.Fatal("현재 인증된 사용자가 use case에 전달되지 않음")
	}
	// 같은 종료 요청을 다시 보내도 204다(멱등). 멱등성은 use case가 보장하며 handler는 결과를 그대로 옮긴다.
	if rec := h.remove(h.sessionID); rec.Code != http.StatusNoContent {
		t.Fatalf("repeat status = %d, want 204", rec.Code)
	}
}

// Preview Gateway가 없는 배포 구성(Previews 없음)은 인증과 출처 검증을 거친 뒤 명확한 503으로 응답한다.
func TestPreviewEndpointsWithoutGatewayAreUnavailable(t *testing.T) {
	h := newHarness(t) // Previews를 주입하지 않는다.
	h.auth.sessions[previewCookie] = h.auth.principal

	rec := h.send(http.MethodPost, "/api/v1/lab-instances/"+labInstanceID+"/preview-sessions", validPreviewBody,
		withCookie(previewCookie), withHeader("Origin", trustedOrigin), withHeader("Content-Type", "application/json"))
	if rec.Code != http.StatusServiceUnavailable || decodeProblem(t, rec).Code != codePreviewUnavailable {
		t.Fatalf("create status = %d, want 503 preview_unavailable: %s", rec.Code, rec.Body.String())
	}
	rec = h.send(http.MethodDelete, "/api/v1/preview-sessions/"+uuid.NewString(), "", withCookie(previewCookie), withHeader("Origin", trustedOrigin))
	if rec.Code != http.StatusServiceUnavailable || decodeProblem(t, rec).Code != codePreviewUnavailable {
		t.Fatalf("delete status = %d, want 503 preview_unavailable", rec.Code)
	}
	// 인증 없는 요청은 여전히 401이다(존재 여부를 인증 없이 알려 주지 않는다).
	rec = h.send(http.MethodPost, "/api/v1/lab-instances/"+labInstanceID+"/preview-sessions", validPreviewBody,
		withHeader("Origin", trustedOrigin), withHeader("Content-Type", "application/json"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", rec.Code)
	}
}

// 지원하지 않는 method는 use case에 도달하지 않는다. 조회/목록/갱신 API는 없다.
func TestPreviewSessionHasNoOtherOperations(t *testing.T) {
	h := newPreviewHarness(t)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/lab-instances/" + labInstanceID + "/preview-sessions"},
		{http.MethodPut, "/api/v1/lab-instances/" + labInstanceID + "/preview-sessions"},
		{http.MethodDelete, "/api/v1/lab-instances/" + labInstanceID + "/preview-sessions"},
		{http.MethodGet, "/api/v1/preview-sessions/" + h.sessionID},
		{http.MethodPut, "/api/v1/preview-sessions/" + h.sessionID},
		{http.MethodPost, "/api/v1/preview-sessions/" + h.sessionID},
		{http.MethodPatch, "/api/v1/preview-sessions/" + h.sessionID},
	} {
		rec := h.send(tc.method, tc.path, "{}", withCookie(previewCookie), withHeader("Origin", trustedOrigin), withHeader("Content-Type", "application/json"))
		if rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
			t.Errorf("%s %s status = %d, want 405 또는 404", tc.method, tc.path, rec.Code)
		}
	}
	if len(h.previews.createIn) != 0 || len(h.previews.closeIDs) != 0 {
		t.Fatal("지원하지 않는 method가 use case에 도달함")
	}
}
