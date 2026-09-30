package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/class"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

type classGetCall struct {
	user    repository.User
	classID string
}

// fakeClasses는 handler가 넘기는 값과 결과를 HTTP로 옮기는 방식만 검증하기 위한 Classes다.
// 권한 판정은 class.Service의 책임이므로 여기서는 정해 둔 결과를 그대로 반환한다.
type fakeClasses struct {
	views   []class.View
	listErr error
	// detail이 nil이면 어떤 ID도 존재하지 않는다.
	detail func(user repository.User, classID string) (class.View, error)

	listCalls int
	listUsers []repository.User
	getCalls  []classGetCall
}

func (f *fakeClasses) List(_ context.Context, user repository.User) ([]class.View, error) {
	f.listCalls++
	f.listUsers = append(f.listUsers, user)
	return f.views, f.listErr
}

func (f *fakeClasses) Get(_ context.Context, user repository.User, classID string) (class.View, error) {
	f.getCalls = append(f.getCalls, classGetCall{user: user, classID: classID})
	if f.detail == nil {
		return class.View{}, class.ErrNotFound
	}
	return f.detail(user, classID)
}

// signIn은 유효한 Session을 만들고 그 Cookie를 붙이는 modifier를 반환한다.
func (h *harness) signIn() func(*http.Request) {
	h.auth.sessions["live-session"] = h.auth.principal
	return withCookie("live-session")
}

func decodeObject(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("응답이 JSON object가 아닙니다: %q (%v)", rec.Body.String(), err)
	}
	return body
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func TestListClassesReturnsSummariesWithMyRolePerClass(t *testing.T) {
	h := newHarness(t)
	teaching := class.View{ID: uuid.New(), Name: "Algorithms", MyRole: repository.ClassRoleInstructor}
	studying := class.View{ID: uuid.New(), Name: "Databases", MyRole: repository.ClassRoleStudent}
	h.classes.views = []class.View{teaching, studying}

	rec := h.send(http.MethodGet, "/api/v1/classes", "", h.signIn())

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := decodeObject(t, rec)
	// OpenAPI ClassList: items만 필수이고 nextCursor는 없으면 생략한다.
	if got := keysOf(body); !reflect.DeepEqual(got, []string{"items"}) {
		t.Fatalf("응답 필드 = %v, want [items]", got)
	}
	items, _ := body["items"].([]any)
	want := []map[string]any{
		{"id": teaching.ID.String(), "name": "Algorithms", "myRole": "INSTRUCTOR"},
		{"id": studying.ID.String(), "name": "Databases", "myRole": "STUDENT"},
	}
	if len(items) != len(want) {
		t.Fatalf("items = %v, want %d개", items, len(want))
	}
	for i, item := range items {
		// 아직 구현하지 않는 optional 필드(activeLabExecution 등)는 응답에 없어야 한다.
		if !reflect.DeepEqual(item, any(want[i])) {
			t.Fatalf("items[%d] = %v, want %v", i, item, want[i])
		}
	}
}

func TestListClassesPassesAuthenticatedUserToApplication(t *testing.T) {
	h := newHarness(t)

	h.send(http.MethodGet, "/api/v1/classes", "", h.signIn())

	if h.classes.listCalls != 1 || h.classes.listUsers[0] != h.auth.principal.User {
		t.Fatalf("List 호출 = %d회, users=%+v, want 1회 with %+v", h.classes.listCalls, h.classes.listUsers, h.auth.principal.User)
	}
}

// Class가 없는 것은 오류가 아니라 정상적인 빈 목록이다. items는 null이 아니라 빈 배열이어야 한다.
func TestListClassesEmptyIsOKWithEmptyArray(t *testing.T) {
	for name, views := range map[string][]class.View{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.classes.views = views

			rec := h.send(http.MethodGet, "/api/v1/classes", "", h.signIn())

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != `{"items":[]}` {
				t.Fatalf("body = %s, want {\"items\":[]}", got)
			}
		})
	}
}

func TestGetClassReturnsDetailWithMyRole(t *testing.T) {
	for _, role := range []repository.ClassRole{repository.ClassRoleInstructor, repository.ClassRoleStudent} {
		t.Run(string(role), func(t *testing.T) {
			h := newHarness(t)
			id := uuid.NewString()
			h.classes.detail = func(_ repository.User, classID string) (class.View, error) {
				return class.View{ID: uuid.MustParse(classID), Name: "Algorithms", MyRole: role}, nil
			}

			rec := h.send(http.MethodGet, "/api/v1/classes/"+id, "", h.signIn())

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			want := map[string]any{"id": id, "name": "Algorithms", "myRole": string(role)}
			if got := decodeObject(t, rec); !reflect.DeepEqual(got, want) {
				t.Fatalf("body = %v, want %v", got, want)
			}
			if len(h.classes.getCalls) != 1 || h.classes.getCalls[0].user != h.auth.principal.User || h.classes.getCalls[0].classID != id {
				t.Fatalf("Get 호출 = %+v, want 1회 with authenticated user and path ID", h.classes.getCalls)
			}
		})
	}
}

// 403은 Class가 존재하지만 접근할 수 없을 때, 404는 존재하지 않을 때다. 두 의미를 바꾸지 않는다.
func TestGetClassMapsApplicationResultsToStatus(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
		wantProb string
	}{
		{name: "forbidden", err: class.ErrForbidden, wantCode: http.StatusForbidden, wantProb: codeForbidden},
		{name: "not found", err: class.ErrNotFound, wantCode: http.StatusNotFound, wantProb: codeNotFound},
		{name: "wrapped forbidden", err: fmt.Errorf("wrapped: %w", class.ErrForbidden), wantCode: http.StatusForbidden, wantProb: codeForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.classes.detail = func(repository.User, string) (class.View, error) { return class.View{}, tt.err }

			rec := h.send(http.MethodGet, "/api/v1/classes/"+uuid.NewString(), "", h.signIn())

			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantCode)
			}
			if problem := decodeProblem(t, rec); problem.Code != tt.wantProb {
				t.Fatalf("problem.code = %q, want %q", problem.Code, tt.wantProb)
			}
			assertNoSetCookie(t, rec)
		})
	}
}

// 접근 불가/부재 응답은 Class 이름이나 역할 같은 Class 데이터를 담지 않는다.
func TestGetClassDeniedResponsesDoNotLeakClassData(t *testing.T) {
	h := newHarness(t)
	h.classes.detail = func(repository.User, string) (class.View, error) {
		// 잘못 구현된 use case가 데이터와 오류를 함께 반환해도 handler가 데이터를 내보내지 않는다.
		return class.View{ID: uuid.New(), Name: "Secret Class Name", MyRole: repository.ClassRoleInstructor}, class.ErrForbidden
	}

	rec := h.send(http.MethodGet, "/api/v1/classes/"+uuid.NewString(), "", h.signIn())

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	for _, leak := range []string{"Secret Class Name", "INSTRUCTOR", "myRole"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Fatalf("403 응답이 Class 데이터(%q)를 포함합니다: %s", leak, rec.Body.String())
		}
	}
}

// path의 classId는 opaque string이다. handler는 형식을 검증하지 않고 그대로 Application에 전달하며,
// 존재하지 않는 값과 같은 404로 응답한다(별도 400 없음).
func TestGetClassPassesOpaquePathIDThroughAndMalformedIsNotFound(t *testing.T) {
	for _, id := range []string{"not-a-uuid", "0", strings.ToUpper(uuid.NewString()), "urn:uuid:" + uuid.NewString(), "%E2%82%AC"} {
		t.Run(id, func(t *testing.T) {
			h := newHarness(t) // detail이 nil이므로 어떤 ID도 존재하지 않는다.

			rec := h.send(http.MethodGet, "/api/v1/classes/"+id, "", h.signIn())

			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
			if problem := decodeProblem(t, rec); problem.Code != codeNotFound {
				t.Fatalf("problem.code = %q", problem.Code)
			}
			wantID := id
			if id == "%E2%82%AC" {
				wantID = "€"
			}
			if len(h.classes.getCalls) != 1 || h.classes.getCalls[0].classID != wantID {
				t.Fatalf("Application에 전달된 ID = %+v, want %q", h.classes.getCalls, wantID)
			}
		})
	}
}

// Application/Repository 오류는 500이며 응답과 log에 원문을 내보내지 않는다.
func TestClassEndpointsInternalErrorsAreSafe(t *testing.T) {
	const raw = `ERROR: connection refused (SQLSTATE 08006) host=10.0.0.7 password=hunter2`
	repoErr := &repository.Error{Kind: repository.KindInternal, Op: "ClassesByUser", SQLState: "08006", Cause: errors.New(raw)}

	endpoints := []struct {
		operation string
		inject    func(h *harness, err error)
		path      string
	}{
		{operation: "list_classes", inject: func(h *harness, err error) { h.classes.listErr = err }, path: "/api/v1/classes"},
		{operation: "get_class", inject: func(h *harness, err error) {
			h.classes.detail = func(repository.User, string) (class.View, error) { return class.View{}, err }
		}, path: "/api/v1/classes/" + uuid.NewString()},
	}
	variants := []struct {
		name     string
		err      error
		wantKind string
	}{
		{name: "repository error", err: fmt.Errorf("class: 조회: %w", repoErr), wantKind: "internal"},
		{name: "unclassified error carrying secrets", err: errors.New(raw), wantKind: "unclassified"},
		{name: "inconsistent data", err: class.ErrInconsistentData, wantKind: "inconsistent_data"},
	}

	for _, endpoint := range endpoints {
		for _, variant := range variants {
			t.Run(endpoint.operation+"/"+variant.name, func(t *testing.T) {
				h := newHarness(t)
				endpoint.inject(h, variant.err)

				rec := h.send(http.MethodGet, endpoint.path, "", h.signIn())

				if rec.Code != http.StatusInternalServerError {
					t.Fatalf("status = %d, want 500", rec.Code)
				}
				problem := decodeProblem(t, rec)
				if problem.Code != codeInternal {
					t.Fatalf("problem.code = %q, want %q", problem.Code, codeInternal)
				}
				assertNoSetCookie(t, rec)
				for _, leak := range []string{"hunter2", "10.0.0.7", "SQLSTATE", "08006", "ClassesByUser", "connection refused", "repository", "inconsistent"} {
					if strings.Contains(rec.Body.String(), leak) {
						t.Fatalf("응답이 내부 정보(%q)를 노출합니다: %s", leak, rec.Body.String())
					}
				}

				logs := h.logs.String()
				for _, leak := range []string{"hunter2", "10.0.0.7", "connection refused", raw} {
					if strings.Contains(logs, leak) {
						t.Fatalf("log가 원문(%q)을 포함합니다: %s", leak, logs)
					}
				}
				records := logRecords(t, h.logs)
				if len(records) != 1 {
					t.Fatalf("log record = %d개, want 1개: %s", len(records), logs)
				}
				record := records[0]
				if record["request_id"] != problem.RequestID || record["operation"] != endpoint.operation || record["error_kind"] != variant.wantKind {
					t.Fatalf("log record = %v, want request_id=%s operation=%s error_kind=%s", record, problem.RequestID, endpoint.operation, variant.wantKind)
				}
			})
		}
	}
}

func TestClassResponsesAreNotCacheable(t *testing.T) {
	h := newHarness(t)
	h.classes.detail = func(repository.User, string) (class.View, error) {
		return class.View{ID: uuid.New(), Name: "Algorithms", MyRole: repository.ClassRoleStudent}, nil
	}
	cookie := h.signIn()

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"list ok":        h.send(http.MethodGet, "/api/v1/classes", "", cookie),
		"detail ok":      h.send(http.MethodGet, "/api/v1/classes/"+uuid.NewString(), "", cookie),
		"list rejected":  h.send(http.MethodGet, "/api/v1/classes", ""),
		"detail invalid": h.send(http.MethodGet, "/api/v1/classes/"+uuid.NewString(), "", withCookie("stale")),
	} {
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s Cache-Control = %q, want no-store", name, got)
		}
	}
}

// 이번 Work Unit은 Membership 목록을 구현하지 않는다. 계약에는 있지만 아직 제공하지 않는 endpoint를 흉내 내지 않는다.
func TestClassMembershipsEndpointIsNotServedYet(t *testing.T) {
	h := newHarness(t)

	rec := h.send(http.MethodGet, "/api/v1/classes/"+uuid.NewString()+"/memberships", "", h.signIn())

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if len(h.classes.getCalls) != 0 {
		t.Fatal("memberships 경로가 Class 상세 handler로 처리되었습니다")
	}
}
