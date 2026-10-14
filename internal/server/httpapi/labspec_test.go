package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/class"
	"github.com/ktcloud4-SL/labbit-app/internal/server/labspec"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// fakeLabSpecs는 handler가 넘기는 값과 결과를 HTTP로 옮기는 방식만 검증한다. 권한·revision·의미 판정은 labspec.Service의 책임이다.
type fakeLabSpecs struct {
	options []labspec.MappingOptions
	list    []labspec.Summary
	result  labspec.Definition
	err     error

	calls   int
	ids     []string
	ifMatch []string
	writes  []labspec.Write
}

func (f *fakeLabSpecs) MappingOptions(context.Context, repository.User) ([]labspec.MappingOptions, error) {
	f.calls++
	return f.options, f.err
}

func (f *fakeLabSpecs) List(context.Context, repository.User) ([]labspec.Summary, error) {
	f.calls++
	return f.list, f.err
}

func (f *fakeLabSpecs) Get(_ context.Context, _ repository.User, id string) (labspec.Definition, error) {
	f.calls++
	f.ids = append(f.ids, id)
	return f.result, f.err
}

func (f *fakeLabSpecs) Create(_ context.Context, _ repository.User, in labspec.Write) (labspec.Definition, error) {
	f.calls++
	f.writes = append(f.writes, in)
	return f.result, f.err
}

func (f *fakeLabSpecs) Update(_ context.Context, _ repository.User, id, ifMatch string, in labspec.Write) (labspec.Definition, error) {
	f.calls++
	f.ids = append(f.ids, id)
	f.ifMatch = append(f.ifMatch, ifMatch)
	f.writes = append(f.writes, in)
	return f.result, f.err
}

func newLabSpecHarness(t *testing.T, fake *fakeLabSpecs) *harness {
	t.Helper()
	return newHarness(t, func(o *Options) { o.LabSpecs = fake })
}

const validLabSpecBody = `{
	"name": "Kubernetes Basic",
	"description": "control과 worker",
	"vms": [
		{"role": "control", "name": "Control Plane", "imageRef": "img", "sizeRef": "size", "count": 1},
		{"role": "worker", "imageRef": "img", "sizeRef": "size", "count": 2.0}
	],
	"workspaceVm": {"role": "control", "instanceIndex": 0},
	"internetOutbound": true,
	"startupScript": "#!/bin/sh"
}`

func ptr(s string) *string { return &s }

func sampleDefinition() labspec.Definition {
	return labspec.Definition{
		Summary: labspec.Summary{
			ID: uuid.MustParse("6f1c1c1e-0000-4000-8000-000000000001"), OwnerUserID: uuid.MustParse("6f1c1c1e-0000-4000-8000-000000000002"),
			Name: "Kubernetes Basic",
		},
		Revision: 7,
		VMs: []labspec.VM{
			{Role: "control", Name: ptr("Control Plane"), ImageRef: "i-1", SizeRef: "s-1", Count: 1},
			{Role: "worker", ImageRef: "i-1", SizeRef: "s-1", Count: 2},
		},
		WorkspaceVM:      labspec.WorkspaceVM{Role: "control", InstanceIndex: 0},
		InternetOutbound: false,
	}
}

func unsafeJSON(mods ...func(*http.Request)) []func(*http.Request) {
	return append([]func(*http.Request){withHeader("Origin", trustedOrigin), withHeader("Content-Type", "application/json")}, mods...)
}

func TestListLabSpecsReturnsSummariesWithOptionalDescriptionOmitted(t *testing.T) {
	fake := &fakeLabSpecs{list: []labspec.Summary{
		{ID: uuid.MustParse("6f1c1c1e-0000-4000-8000-00000000000a"), OwnerUserID: uuid.MustParse("6f1c1c1e-0000-4000-8000-00000000000b"), Name: "A", Description: ptr("desc")},
		{ID: uuid.MustParse("6f1c1c1e-0000-4000-8000-00000000000c"), OwnerUserID: uuid.MustParse("6f1c1c1e-0000-4000-8000-00000000000d"), Name: "B"},
	}}
	h := newLabSpecHarness(t, fake)

	rec := h.send(http.MethodGet, "/api/v1/lab-specs", "", h.signIn())

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	want := `{"items":[{"id":"6f1c1c1e-0000-4000-8000-00000000000a","name":"A","description":"desc","ownerUserId":"6f1c1c1e-0000-4000-8000-00000000000b"},` +
		`{"id":"6f1c1c1e-0000-4000-8000-00000000000c","name":"B","ownerUserId":"6f1c1c1e-0000-4000-8000-00000000000d"}]}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Fatalf("body = %s\nwant %s", got, want)
	}
}

func TestListLabSpecsEmptyIsArray(t *testing.T) {
	h := newLabSpecHarness(t, &fakeLabSpecs{})

	rec := h.send(http.MethodGet, "/api/v1/lab-specs", "", h.signIn())

	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"items":[]}` {
		t.Fatalf("status = %d, body = %s, want 200 {\"items\":[]}", rec.Code, rec.Body.String())
	}
}

func TestLabSpecEndpointsRequireAuthentication(t *testing.T) {
	fake := &fakeLabSpecs{}
	h := newLabSpecHarness(t, fake)
	id := uuid.NewString()
	tests := []struct {
		method, path string
		mods         []func(*http.Request)
	}{
		{http.MethodGet, "/api/v1/lab-specs", nil},
		{http.MethodGet, "/api/v1/lab-specs/mapping-options", nil},
		{http.MethodPost, "/api/v1/lab-specs", unsafeJSON()},
		{http.MethodGet, "/api/v1/lab-specs/" + id, nil},
		{http.MethodPut, "/api/v1/lab-specs/" + id, unsafeJSON(withHeader("If-Match", `"1"`))},
		{http.MethodGet, "/api/v1/classes/" + id + "/memberships", nil},
	}
	for _, tt := range tests {
		rec := h.send(tt.method, tt.path, validLabSpecBody, tt.mods...)
		if rec.Code != http.StatusUnauthorized || decodeProblem(t, rec).Code != codeUnauthenticated {
			t.Fatalf("%s %s = %d, want 401: %s", tt.method, tt.path, rec.Code, rec.Body.String())
		}
	}
	if fake.calls != 0 {
		t.Fatalf("인증 전에 use case가 %d번 호출되었습니다", fake.calls)
	}
}

func TestGetLabSpecReturnsDefinitionAndETag(t *testing.T) {
	fake := &fakeLabSpecs{result: sampleDefinition()}
	h := newLabSpecHarness(t, fake)

	rec := h.send(http.MethodGet, "/api/v1/lab-specs/some%2Fid", "", h.signIn())

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("ETag"); got != `"7"` {
		t.Fatalf("ETag = %q, want \"7\"", got)
	}
	if !reflect.DeepEqual(fake.ids, []string{"some/id"}) {
		t.Fatalf("Get id = %v, want decoded path value", fake.ids)
	}
	want := `{"id":"6f1c1c1e-0000-4000-8000-000000000001","name":"Kubernetes Basic","ownerUserId":"6f1c1c1e-0000-4000-8000-000000000002",` +
		`"vms":[{"role":"control","name":"Control Plane","imageRef":"i-1","sizeRef":"s-1","count":1},{"role":"worker","imageRef":"i-1","sizeRef":"s-1","count":2}],` +
		`"workspaceVm":{"role":"control","instanceIndex":0},"internetOutbound":false}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Fatalf("body = %s\nwant %s", got, want)
	}
}

func TestLabSpecErrorsMapToProblemDetails(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"not found", labspec.ErrNotFound, http.StatusNotFound, codeNotFound},
		{"forbidden", labspec.ErrForbidden, http.StatusForbidden, codeForbidden},
		{"stale", labspec.ErrStaleRevision, http.StatusPreconditionFailed, codeStaleRevision},
		{"invalid", &labspec.ValidationError{Reason: labspec.ReasonDuplicateRole}, http.StatusUnprocessableEntity, codeInvalidLabSpec},
		{"inconsistent", labspec.ErrInconsistentData, http.StatusInternalServerError, codeInternal},
		{"repository", &repository.Error{Kind: repository.KindInternal, Op: "LabSpecByID", Cause: errors.New("password=hunter2")}, http.StatusInternalServerError, codeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeLabSpecs{err: tt.err}
			h := newLabSpecHarness(t, fake)
			id := uuid.NewString()
			for _, send := range []func() (string, int, string){
				func() (string, int, string) {
					r := h.send(http.MethodGet, "/api/v1/lab-specs/"+id, "", h.signIn())
					return "GET", r.Code, decodeProblem(t, r).Code
				},
				func() (string, int, string) {
					r := h.send(http.MethodPut, "/api/v1/lab-specs/"+id, validLabSpecBody, unsafeJSON(h.signIn(), withHeader("If-Match", `"1"`))...)
					return "PUT", r.Code, decodeProblem(t, r).Code
				},
				func() (string, int, string) {
					r := h.send(http.MethodPost, "/api/v1/lab-specs", validLabSpecBody, unsafeJSON(h.signIn())...)
					return "POST", r.Code, decodeProblem(t, r).Code
				},
			} {
				method, status, code := send()
				if status != tt.status || code != tt.code {
					t.Fatalf("%s = %d %q, want %d %q", method, status, code, tt.status, tt.code)
				}
			}
			if strings.Contains(h.logs.String(), "hunter2") {
				t.Fatalf("log에 Repository 원문이 남았습니다: %s", h.logs.String())
			}
		})
	}
}

func TestCreateLabSpecReturns201WithLocationAndPassesDecodedWrite(t *testing.T) {
	def := sampleDefinition()
	def.Revision = 1
	fake := &fakeLabSpecs{result: def}
	h := newLabSpecHarness(t, fake)

	rec := h.send(http.MethodPost, "/api/v1/lab-specs", validLabSpecBody, unsafeJSON(h.signIn())...)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/api/v1/lab-specs/"+def.ID.String() {
		t.Fatalf("Location = %q", got)
	}
	body := decodeObject(t, rec)
	if body["id"] != def.ID.String() {
		t.Fatalf("body id = %v", body["id"])
	}
	want := labspec.Write{
		Name: "Kubernetes Basic", Description: ptr("control과 worker"),
		VMs: []labspec.VM{
			{Role: "control", Name: ptr("Control Plane"), ImageRef: "img", SizeRef: "size", Count: 1},
			{Role: "worker", ImageRef: "img", SizeRef: "size", Count: 2},
		},
		WorkspaceVM:      labspec.WorkspaceVM{Role: "control", InstanceIndex: 0},
		InternetOutbound: true, StartupScript: ptr("#!/bin/sh"),
	}
	if len(fake.writes) != 1 || !reflect.DeepEqual(fake.writes[0], want) {
		t.Fatalf("Create input = %+v, want %+v", fake.writes, want)
	}
}

// OpenAPI LabSpecWrite의 형식 위반은 Application을 호출하지 않고 400이다.
func TestLabSpecWriteShapeViolationsAre400(t *testing.T) {
	mutate := func(f func(m map[string]any)) string {
		var m map[string]any
		_ = json.Unmarshal([]byte(validLabSpecBody), &m)
		f(m)
		b, _ := json.Marshal(m)
		return string(b)
	}
	vm := func(m map[string]any) map[string]any { return m["vms"].([]any)[0].(map[string]any) }
	ws := func(m map[string]any) map[string]any { return m["workspaceVm"].(map[string]any) }
	tests := map[string]string{
		"JSON 아님":                `{"name":`,
		"JSON 값 둘":               validLabSpecBody + `{}`,
		"배열":                     `[]`,
		"name 없음":                mutate(func(m map[string]any) { delete(m, "name") }),
		"name 빈 문자열":             mutate(func(m map[string]any) { m["name"] = "" }),
		"name 숫자":                mutate(func(m map[string]any) { m["name"] = 1 }),
		"description null":       mutate(func(m map[string]any) { m["description"] = nil }),
		"알 수 없는 field":           mutate(func(m map[string]any) { m["classId"] = "x" }),
		"vms 없음":                 mutate(func(m map[string]any) { delete(m, "vms") }),
		"vms 빈 배열":               mutate(func(m map[string]any) { m["vms"] = []any{} }),
		"vm 알 수 없는 field":        mutate(func(m map[string]any) { vm(m)["imageId"] = "x" }),
		"vm role 없음":             mutate(func(m map[string]any) { delete(vm(m), "role") }),
		"vm imageRef 빈 문자열":      mutate(func(m map[string]any) { vm(m)["imageRef"] = "" }),
		"vm sizeRef 없음":          mutate(func(m map[string]any) { delete(vm(m), "sizeRef") }),
		"vm count 0":             mutate(func(m map[string]any) { vm(m)["count"] = 0 }),
		"vm count 소수":            mutate(func(m map[string]any) { vm(m)["count"] = 1.5 }),
		"vm count 문자열":           mutate(func(m map[string]any) { vm(m)["count"] = "1" }),
		"vm count 없음":            mutate(func(m map[string]any) { delete(vm(m), "count") }),
		"vm name null":           mutate(func(m map[string]any) { vm(m)["name"] = nil }),
		"workspaceVm 없음":         mutate(func(m map[string]any) { delete(m, "workspaceVm") }),
		"workspaceVm role 빈 문자열": mutate(func(m map[string]any) { ws(m)["role"] = "" }),
		"workspaceVm index 음수":   mutate(func(m map[string]any) { ws(m)["instanceIndex"] = -1 }),
		"workspaceVm index 없음":   mutate(func(m map[string]any) { delete(ws(m), "instanceIndex") }),
		"workspaceVm 추가 field":   mutate(func(m map[string]any) { ws(m)["vmKey"] = "x" }),
		"internetOutbound 없음":    mutate(func(m map[string]any) { delete(m, "internetOutbound") }),
		"internetOutbound 문자열":   mutate(func(m map[string]any) { m["internetOutbound"] = "true" }),
		"startupScript null":     mutate(func(m map[string]any) { m["startupScript"] = nil }),
		"workspaceVm index 큰 음수": strings.Replace(validLabSpecBody, `"instanceIndex": 0`, `"instanceIndex": -1e40`, 1),
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			fake := &fakeLabSpecs{result: sampleDefinition()}
			h := newLabSpecHarness(t, fake)
			for _, send := range []func() int{
				func() int {
					r := h.send(http.MethodPost, "/api/v1/lab-specs", body, unsafeJSON(h.signIn())...)
					if r.Code == http.StatusBadRequest && decodeProblem(t, r).Code != codeInvalidRequest {
						t.Fatalf("code != invalid_request")
					}
					return r.Code
				},
				func() int {
					r := h.send(http.MethodPut, "/api/v1/lab-specs/"+uuid.NewString(), body, unsafeJSON(h.signIn(), withHeader("If-Match", `"1"`))...)
					return r.Code
				},
			} {
				if got := send(); got != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400 (body %s)", got, body)
				}
			}
			if fake.calls != 0 {
				t.Fatalf("형식 오류인데 use case가 호출되었습니다")
			}
		})
	}
	// 같은 body라도 Content-Type이 JSON이 아니면 400이다.
	fake := &fakeLabSpecs{result: sampleDefinition()}
	h := newLabSpecHarness(t, fake)
	rec := h.send(http.MethodPost, "/api/v1/lab-specs", validLabSpecBody, h.signIn(), withHeader("Origin", trustedOrigin), withHeader("Content-Type", "text/plain"))
	if rec.Code != http.StatusBadRequest || fake.calls != 0 {
		t.Fatalf("text/plain status = %d, calls = %d, want 400 and 0", rec.Code, fake.calls)
	}
}

// Schema에 maximum이 없으므로 int64를 넘는 정수는 형식 오류가 아니라 Application이 판단할 범위 밖의 값이다.
func TestLabSpecWriteHugeIntegersReachApplication(t *testing.T) {
	fake := &fakeLabSpecs{result: sampleDefinition()}
	h := newLabSpecHarness(t, fake)
	body := strings.Replace(strings.Replace(validLabSpecBody, `"count": 1}`, `"count": 1e40}`, 1), `"instanceIndex": 0`, `"instanceIndex": 99999999999999999999`, 1)

	rec := h.send(http.MethodPost, "/api/v1/lab-specs", body, unsafeJSON(h.signIn())...)

	if rec.Code != http.StatusCreated || len(fake.writes) != 1 {
		t.Fatalf("status = %d, want use case 호출: %s", rec.Code, rec.Body.String())
	}
	if got := fake.writes[0]; got.VMs[0].Count != math.MaxInt64 || got.WorkspaceVM.InstanceIndex != math.MaxInt64 {
		t.Fatalf("범위를 넘는 정수 = %d, %d, want MaxInt64", got.VMs[0].Count, got.WorkspaceVM.InstanceIndex)
	}
}

func TestUpdateLabSpecRequiresSingleStrongIfMatch(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		code   string
	}{
		{"없음", nil, codeIfMatchRequired},
		{"따옴표 없음", []string{"1"}, codeInvalidIfMatch},
		{"weak", []string{`W/"1"`}, codeInvalidIfMatch},
		{"wildcard", []string{"*"}, codeInvalidIfMatch},
		{"목록", []string{`"1", "2"`}, codeInvalidIfMatch},
		{"header 둘", []string{`"1"`, `"2"`}, codeInvalidIfMatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeLabSpecs{result: sampleDefinition()}
			h := newLabSpecHarness(t, fake)
			mods := unsafeJSON(h.signIn(), func(r *http.Request) {
				for _, v := range tt.values {
					r.Header.Add("If-Match", v)
				}
			})

			rec := h.send(http.MethodPut, "/api/v1/lab-specs/"+uuid.NewString(), validLabSpecBody, mods...)

			if rec.Code != http.StatusBadRequest || decodeProblem(t, rec).Code != tt.code || fake.calls != 0 {
				t.Fatalf("status = %d (%s), calls = %d, want 400 %s and no call", rec.Code, rec.Body.String(), fake.calls, tt.code)
			}
		})
	}
}

func TestUpdateLabSpecPassesRevisionAndReturnsNewETag(t *testing.T) {
	def := sampleDefinition()
	def.Revision = 8
	fake := &fakeLabSpecs{result: def}
	h := newLabSpecHarness(t, fake)
	id := uuid.NewString()

	rec := h.send(http.MethodPut, "/api/v1/lab-specs/"+id, validLabSpecBody, unsafeJSON(h.signIn(), withHeader("If-Match", `"7"`))...)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("ETag"); got != `"8"` {
		t.Fatalf("ETag = %q, want \"8\"", got)
	}
	if !reflect.DeepEqual(fake.ids, []string{id}) || !reflect.DeepEqual(fake.ifMatch, []string{"7"}) {
		t.Fatalf("Update(id=%v, ifMatch=%v)", fake.ids, fake.ifMatch)
	}
}

func TestLabSpecUnsafeMethodsRequireTrustedOrigin(t *testing.T) {
	fake := &fakeLabSpecs{result: sampleDefinition()}
	h := newLabSpecHarness(t, fake)

	post := h.send(http.MethodPost, "/api/v1/lab-specs", validLabSpecBody, h.signIn(), withHeader("Content-Type", "application/json"), withoutSource)
	put := h.send(http.MethodPut, "/api/v1/lab-specs/"+uuid.NewString(), validLabSpecBody, h.signIn(),
		withHeader("Content-Type", "application/json"), withHeader("If-Match", `"1"`), withHeader("Origin", "https://evil.test"))

	if post.Code != http.StatusForbidden || decodeProblem(t, post).Code != codeCSRFRejected {
		t.Fatalf("POST without Origin = %d", post.Code)
	}
	if put.Code != http.StatusForbidden || decodeProblem(t, put).Code != codeCSRFRejected {
		t.Fatalf("PUT cross origin = %d", put.Code)
	}
	if fake.calls != 0 {
		t.Fatalf("CSRF 거절 전에 use case가 호출되었습니다")
	}
}

func TestListClassMembershipsReturnsOpenAPIItems(t *testing.T) {
	h := newHarness(t)
	alice, bob := uuid.New(), uuid.New()
	h.classes.members = func(repository.User, string) ([]class.Member, error) {
		return []class.Member{
			{UserID: alice, Username: "alice", Role: repository.ClassRoleInstructor},
			{UserID: bob, Username: "bob", Role: repository.ClassRoleStudent},
		}, nil
	}

	rec := h.send(http.MethodGet, "/api/v1/classes/c-1/memberships", "", h.signIn())

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	want := `{"items":[{"userId":"` + alice.String() + `","username":"alice","role":"INSTRUCTOR"},{"userId":"` + bob.String() + `","username":"bob","role":"STUDENT"}]}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Fatalf("body = %s\nwant %s", got, want)
	}
	if len(h.classes.memberCalls) != 1 || h.classes.memberCalls[0].classID != "c-1" || h.classes.memberCalls[0].user != h.auth.principal.User {
		t.Fatalf("Members 호출 = %+v", h.classes.memberCalls)
	}
}

func TestListClassMembershipsErrors(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{class.ErrForbidden, http.StatusForbidden, codeForbidden},
		{class.ErrNotFound, http.StatusNotFound, codeNotFound},
		{class.ErrInconsistentData, http.StatusInternalServerError, codeInternal},
	}
	for _, tt := range tests {
		h := newHarness(t)
		h.classes.members = func(repository.User, string) ([]class.Member, error) { return nil, tt.err }

		rec := h.send(http.MethodGet, "/api/v1/classes/c-1/memberships", "", h.signIn())

		if rec.Code != tt.status || decodeProblem(t, rec).Code != tt.code {
			t.Fatalf("%v: status = %d, want %d %s", tt.err, rec.Code, tt.status, tt.code)
		}
	}
	// 빈 목록은 오류가 아니라 빈 배열이다.
	h := newHarness(t)
	h.classes.members = func(repository.User, string) ([]class.Member, error) { return nil, nil }
	rec := h.send(http.MethodGet, "/api/v1/classes/c-1/memberships", "", h.signIn())
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"items":[]}` {
		t.Fatalf("빈 목록 = %d %s", rec.Code, rec.Body.String())
	}
}
