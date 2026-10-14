package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/class"
	"github.com/ktcloud4-SL/labbit-app/internal/server/labexecution"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// fakeLabExecutions는 handler가 넘기는 값과 결과를 HTTP로 옮기는 방식만 검증한다. 권한·대상·Idempotency 판정은 labexecution.Service의 책임이다.
type fakeLabExecutions struct {
	accepted  labexecution.Accepted
	execution labexecution.ExecutionView
	instance  labexecution.InstanceView
	err       error

	calls  int
	inputs []labexecution.ProvisionInput
	ids    []string
}

func (f *fakeLabExecutions) Provision(_ context.Context, _ repository.User, in labexecution.ProvisionInput) (labexecution.Accepted, error) {
	f.calls++
	f.inputs = append(f.inputs, in)
	return f.accepted, f.err
}

func (f *fakeLabExecutions) Execution(_ context.Context, _ repository.User, id string) (labexecution.ExecutionView, error) {
	f.calls++
	f.ids = append(f.ids, id)
	return f.execution, f.err
}

func (f *fakeLabExecutions) Instance(_ context.Context, _ repository.User, id string) (labexecution.InstanceView, error) {
	f.calls++
	f.ids = append(f.ids, id)
	return f.instance, f.err
}

const (
	classPath            = "/api/v1/classes/6f1c1c1e-0000-4000-8000-0000000000c1/lab-executions"
	validProvisionBody   = `{"labSpecId":"spec-1","targetStudentIds":["s-2","s-1"]}`
	acceptedOperationID  = "6f1c1c1e-0000-4000-8000-0000000000a1"
	acceptedExecutionID  = "6f1c1c1e-0000-4000-8000-0000000000e1"
	acceptedInstanceID   = "6f1c1c1e-0000-4000-8000-0000000000f1"
	acceptedInstructorID = "6f1c1c1e-0000-4000-8000-0000000000b1"
)

func newLabExecutionHarness(t *testing.T, fake *fakeLabExecutions) *harness {
	t.Helper()
	return newHarness(t, func(o *Options) { o.LabExecutions = fake })
}

func sampleAccepted(replayed bool) labexecution.Accepted {
	return labexecution.Accepted{OperationID: uuid.MustParse(acceptedOperationID), LabExecutionID: uuid.MustParse(acceptedExecutionID), Replayed: replayed}
}

func TestCreateLabExecutionAcceptsWith202AndLocation(t *testing.T) {
	fake := &fakeLabExecutions{accepted: sampleAccepted(false)}
	h := newLabExecutionHarness(t, fake)

	rec := h.send(http.MethodPost, classPath, validProvisionBody, unsafeJSON(h.signIn(), withHeader("Idempotency-Key", "key-1"))...)

	if rec.Code != http.StatusAccepted || rec.Header().Get("Location") != "/api/v1/operations/"+acceptedOperationID {
		t.Fatalf("status=%d location=%q body=%s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	want := `{"operationId":"` + acceptedOperationID + `","target":{"type":"LAB_EXECUTION","id":"` + acceptedExecutionID + `"}}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Fatalf("body=%s want %s", got, want)
	}
	in := fake.inputs[0]
	if in.ClassID != "6f1c1c1e-0000-4000-8000-0000000000c1" || in.LabSpecID != "spec-1" || strings.Join(in.TargetStudentIDs, ",") != "s-2,s-1" ||
		in.IdempotencyKey != "key-1" || in.RequestID == "" {
		t.Fatalf("input=%+v", in)
	}
}

func TestCreateLabExecutionReplayIs200WithoutLocation(t *testing.T) {
	h := newLabExecutionHarness(t, &fakeLabExecutions{accepted: sampleAccepted(true)})

	rec := h.send(http.MethodPost, classPath, validProvisionBody, unsafeJSON(h.signIn(), withHeader("Idempotency-Key", "key-1"))...)

	if rec.Code != http.StatusOK || rec.Header().Get("Location") != "" || !strings.Contains(rec.Body.String(), acceptedOperationID) {
		t.Fatalf("status=%d location=%q body=%s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
}

func TestCreateLabExecutionRejectsMalformedRequestBeforeUseCase(t *testing.T) {
	for name, tt := range map[string]struct {
		body string
		mods []func(*http.Request)
	}{
		"missing key":        {validProvisionBody, nil},
		"empty key":          {validProvisionBody, []func(*http.Request){withHeader("Idempotency-Key", "")}},
		"two keys":           {validProvisionBody, []func(*http.Request){func(r *http.Request) { r.Header.Add("Idempotency-Key", "a"); r.Header.Add("Idempotency-Key", "b") }}},
		"oversized key":      {validProvisionBody, []func(*http.Request){withHeader("Idempotency-Key", strings.Repeat("k", 4*255+1))}},
		"not json":           {`labSpecId=spec-1`, []func(*http.Request){withHeader("Idempotency-Key", "k"), withHeader("Content-Type", "text/plain")}},
		"missing labSpecId":  {`{"targetStudentIds":["s-1"]}`, []func(*http.Request){withHeader("Idempotency-Key", "k")}},
		"empty labSpecId":    {`{"labSpecId":"","targetStudentIds":["s-1"]}`, []func(*http.Request){withHeader("Idempotency-Key", "k")}},
		"missing students":   {`{"labSpecId":"spec-1"}`, []func(*http.Request){withHeader("Idempotency-Key", "k")}},
		"empty students":     {`{"labSpecId":"spec-1","targetStudentIds":[]}`, []func(*http.Request){withHeader("Idempotency-Key", "k")}},
		"duplicate students": {`{"labSpecId":"spec-1","targetStudentIds":["s-1","s-1"]}`, []func(*http.Request){withHeader("Idempotency-Key", "k")}},
		"empty student":      {`{"labSpecId":"spec-1","targetStudentIds":[""]}`, []func(*http.Request){withHeader("Idempotency-Key", "k")}},
		"non-string student": {`{"labSpecId":"spec-1","targetStudentIds":[1]}`, []func(*http.Request){withHeader("Idempotency-Key", "k")}},
		"null student":       {`{"labSpecId":"spec-1","targetStudentIds":[null]}`, []func(*http.Request){withHeader("Idempotency-Key", "k")}},
		"unknown field":      {`{"labSpecId":"spec-1","targetStudentIds":["s-1"],"force":true}`, []func(*http.Request){withHeader("Idempotency-Key", "k")}},
		"two values":         {validProvisionBody + validProvisionBody, []func(*http.Request){withHeader("Idempotency-Key", "k")}},
	} {
		t.Run(name, func(t *testing.T) {
			fake := &fakeLabExecutions{accepted: sampleAccepted(false)}
			h := newLabExecutionHarness(t, fake)
			rec := h.send(http.MethodPost, classPath, tt.body, unsafeJSON(append([]func(*http.Request){h.signIn()}, tt.mods...)...)...)
			requireProblem(t, rec, http.StatusBadRequest, codeInvalidRequest)
			if fake.calls != 0 {
				t.Fatalf("use case called %d times", fake.calls)
			}
		})
	}
}

func TestCreateLabExecutionRequiresSessionAndOrigin(t *testing.T) {
	fake := &fakeLabExecutions{accepted: sampleAccepted(false)}
	h := newLabExecutionHarness(t, fake)

	rec := h.send(http.MethodPost, classPath, validProvisionBody, unsafeJSON(withHeader("Idempotency-Key", "k"))...)
	requireProblem(t, rec, http.StatusUnauthorized, codeUnauthenticated)
	rec = h.send(http.MethodPost, classPath, validProvisionBody, h.signIn(), withHeader("Content-Type", "application/json"), withHeader("Idempotency-Key", "k"), withoutSource)
	requireProblem(t, rec, http.StatusForbidden, codeCSRFRejected)
	if fake.calls != 0 {
		t.Fatalf("use case called %d times", fake.calls)
	}
}

func TestLabExecutionErrorsMapToStableProblems(t *testing.T) {
	const leak = "dial tcp 10.0.0.7:5432 password=hunter2 glance-image-raw"
	for _, tt := range []struct {
		err    error
		status int
		code   string
	}{
		{labexecution.ErrInvalidRequest, 400, codeInvalidRequest},
		{labexecution.ErrNotFound, 404, codeNotFound},
		{labexecution.ErrForbidden, 403, codeForbidden},
		{labexecution.ErrActiveExecution, 409, "active_lab_execution_exists"},
		{labexecution.ErrIdempotencyConflict, 409, "idempotency_key_reused"},
		{labexecution.ErrInvalidTargets, 422, "invalid_target_students"},
		{labexecution.ErrLabSpecNotProvisionable, 422, "lab_spec_not_provisionable"},
		{labexecution.ErrInconsistentData, 500, codeInternal},
		{&repository.Error{Kind: repository.KindInternal, Op: "CreateLabExecution", Cause: errors.New(leak)}, 500, codeInternal},
	} {
		fake := &fakeLabExecutions{err: tt.err}
		h := newLabExecutionHarness(t, fake)
		post := h.send(http.MethodPost, classPath, validProvisionBody, unsafeJSON(h.signIn(), withHeader("Idempotency-Key", "k"))...)
		requireProblem(t, post, tt.status, tt.code)
		if tt.status != 400 && tt.status != 409 && tt.status != 422 {
			for _, path := range []string{"/api/v1/lab-executions/e-1", "/api/v1/lab-instances/i-1"} {
				requireProblem(t, h.send(http.MethodGet, path, "", h.signIn()), tt.status, tt.code)
			}
		}
		if strings.Contains(post.Body.String(), "hunter2") || strings.Contains(h.logs.String(), "hunter2") || strings.Contains(h.logs.String(), "glance-image-raw") {
			t.Fatalf("raw cause leaked: body=%s logs=%s", post.Body, h.logs)
		}
	}
}

func TestGetLabExecutionAndInstanceProjectOpenAPIFields(t *testing.T) {
	student := uuid.MustParse("6f1c1c1e-0000-4000-8000-0000000000b2")
	studentInstance := labexecution.InstanceView{ID: uuid.MustParse("6f1c1c1e-0000-4000-8000-0000000000f2"), UserID: student,
		LabExecutionID: uuid.MustParse(acceptedExecutionID), Status: "PENDING", Generation: 1}
	fake := &fakeLabExecutions{
		execution: labexecution.ExecutionView{
			ID: uuid.MustParse(acceptedExecutionID), ClassID: uuid.MustParse("6f1c1c1e-0000-4000-8000-0000000000c1"),
			LabSpecID: uuid.MustParse("6f1c1c1e-0000-4000-8000-0000000000d1"), InstructorUserID: uuid.MustParse(acceptedInstructorID),
			TargetUserIDs: []uuid.UUID{student}, Status: "PROVISIONING",
			LabInstances: []labexecution.InstanceView{{ID: uuid.MustParse(acceptedInstanceID), UserID: uuid.MustParse(acceptedInstructorID),
				LabExecutionID: uuid.MustParse(acceptedExecutionID), Status: "PENDING", Generation: 1}, studentInstance},
		},
		instance: studentInstance,
	}
	h := newLabExecutionHarness(t, fake)

	rec := h.send(http.MethodGet, "/api/v1/lab-executions/"+acceptedExecutionID, "", h.signIn())
	want := `{"id":"` + acceptedExecutionID + `","classId":"6f1c1c1e-0000-4000-8000-0000000000c1","labSpecId":"6f1c1c1e-0000-4000-8000-0000000000d1",` +
		`"instructorUserId":"` + acceptedInstructorID + `","targetUserIds":["6f1c1c1e-0000-4000-8000-0000000000b2"],"status":"PROVISIONING",` +
		`"labInstances":[{"id":"` + acceptedInstanceID + `","userId":"` + acceptedInstructorID + `","status":"PENDING","generation":1},` +
		`{"id":"6f1c1c1e-0000-4000-8000-0000000000f2","userId":"6f1c1c1e-0000-4000-8000-0000000000b2","status":"PENDING","generation":1}]}`
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != want {
		t.Fatalf("status=%d body=%s\nwant %s", rec.Code, rec.Body, want)
	}
	rec = h.send(http.MethodGet, "/api/v1/lab-instances/6f1c1c1e-0000-4000-8000-0000000000f2", "", h.signIn())
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != 200 || len(body) != 5 || body["labExecutionId"] != acceptedExecutionID ||
		body["userId"] != student.String() || body["status"] != "PENDING" || body["generation"] != float64(1) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if strings.Join(fake.ids, ",") != acceptedExecutionID+",6f1c1c1e-0000-4000-8000-0000000000f2" {
		t.Fatalf("ids=%v", fake.ids)
	}
}

// Class 응답의 activeLabExecution/myLabInstance는 optional이다. 있으면 OpenAPI LabExecutionSummary/LabInstanceSummary 모양이고 없으면 생략한다.
func TestClassResponsesProjectActiveLabExecutionAndMyLabInstance(t *testing.T) {
	classID := uuid.MustParse("6f1c1c1e-0000-4000-8000-0000000000c1")
	active := &class.ExecutionSummary{ID: uuid.MustParse(acceptedExecutionID), Status: "PROVISIONING"}
	mine := &class.InstanceSummary{ID: uuid.MustParse(acceptedInstanceID), UserID: uuid.MustParse(acceptedInstructorID), Status: "PENDING", Generation: 1}
	h := newHarness(t)
	h.classes.views = []class.View{{ID: classID, Name: "A", MyRole: repository.ClassRoleInstructor, ActiveLabExecution: active},
		{ID: uuid.MustParse("6f1c1c1e-0000-4000-8000-0000000000c2"), Name: "B", MyRole: repository.ClassRoleStudent}}
	h.classes.detail = func(repository.User, string) (class.View, error) {
		return class.View{ID: classID, Name: "A", MyRole: repository.ClassRoleInstructor, ActiveLabExecution: active, MyLabInstance: mine}, nil
	}

	list := h.send(http.MethodGet, "/api/v1/classes", "", h.signIn())
	wantList := `{"items":[{"id":"` + classID.String() + `","name":"A","myRole":"INSTRUCTOR","activeLabExecution":{"id":"` + acceptedExecutionID + `","status":"PROVISIONING"}},` +
		`{"id":"6f1c1c1e-0000-4000-8000-0000000000c2","name":"B","myRole":"STUDENT"}]}`
	if list.Code != 200 || strings.TrimSpace(list.Body.String()) != wantList {
		t.Fatalf("list=%d %s\nwant %s", list.Code, list.Body, wantList)
	}
	detail := h.send(http.MethodGet, "/api/v1/classes/"+classID.String(), "", h.signIn())
	wantDetail := `{"id":"` + classID.String() + `","name":"A","myRole":"INSTRUCTOR","activeLabExecution":{"id":"` + acceptedExecutionID + `","status":"PROVISIONING"},` +
		`"myLabInstance":{"id":"` + acceptedInstanceID + `","userId":"` + acceptedInstructorID + `","status":"PENDING","generation":1}}`
	if detail.Code != 200 || strings.TrimSpace(detail.Body.String()) != wantDetail {
		t.Fatalf("detail=%d %s\nwant %s", detail.Code, detail.Body, wantDetail)
	}
}
