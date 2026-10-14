//go:build integration

package httpapi_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	migrationfiles "github.com/ktcloud4-SL/labbit-app/db/migrations"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/auth"
	"github.com/ktcloud4-SL/labbit-app/internal/server/class"
	"github.com/ktcloud4-SL/labbit-app/internal/server/httpapi"
	"github.com/ktcloud4-SL/labbit-app/internal/server/labexecution"
	"github.com/ktcloud4-SL/labbit-app/internal/server/labexecution/labexecutiontest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/labspec"
	"github.com/ktcloud4-SL/labbit-app/internal/server/operation"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

func TestProvisionHTTPAgainstPostgres(t *testing.T) {
	dsn := postgrestest.NewDatabase(t)
	migrations, err := postgres.LoadMigrations(migrationfiles.Files)
	if err != nil {
		t.Fatal(err)
	}
	postgrestest.Migrate(t, dsn, migrations)
	conn := postgrestest.Connect(t, dsn)
	f := labexecutiontest.New(t, conn)
	pool, err := postgres.OpenPool(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store := postgres.NewStore(pool)
	h, err := httpapi.New(httpapi.Options{Auth: auth.NewService(store, auth.Argon2id{}, nil), Classes: class.NewService(store), LabSpecs: labspec.NewService(store),
		Operations: operation.NewService(store), LabExecutions: labexecution.NewService(store), PublicOrigin: trustedOrigin})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	tokens := map[uuid.UUID]string{}
	for i, user := range f.Users() {
		raw := bytes.Repeat([]byte{byte(i + 1)}, 32)
		hash := sha256.Sum256(raw)
		tokens[user] = base64.RawURLEncoding.EncodeToString(raw)
		if err := store.CreateAuthSession(t.Context(), repository.AuthSession{ID: uuid.New(), UserID: user, TokenHash: hash[:], CreatedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	send := func(method, path, body string, user uuid.UUID, key string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if user != uuid.Nil {
			r.AddCookie(&http.Cookie{Name: "__Host-labbit-session", Value: tokens[user]})
		}
		if method == http.MethodPost {
			r.Header.Set("Origin", trustedOrigin)
			r.Header.Set("Content-Type", "application/json")
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		// 응답에는 실제 Provider 값, Startup Script, 내부 Idempotency/Trace metadata가 나타나지 않는다.
		for _, leak := range []string{labexecutiontest.ImageMarker, labexecutiontest.FlavorMarker, labexecutiontest.ScriptMarker, "idempotency_key_hash", "traceparent", "SQLSTATE"} {
			if strings.Contains(w.Body.String(), leak) {
				t.Fatalf("%s %s leaked %q: %s", method, path, leak, w.Body)
			}
		}
		return w
	}
	code := func(w *httptest.ResponseRecorder) string {
		var p struct{ Code string }
		_ = json.Unmarshal(w.Body.Bytes(), &p)
		return p.Code
	}
	classPath := "/api/v1/classes/" + f.ClassID.String() + "/lab-executions"
	body := func(students ...uuid.UUID) string {
		ids := make([]string, len(students))
		for i, s := range students {
			ids[i] = `"` + s.String() + `"`
		}
		return `{"labSpecId":"` + f.LabSpecID.String() + `","targetStudentIds":[` + strings.Join(ids, ",") + `]}`
	}

	// 권한·대상 검증은 아무 row도 만들지 않는다.
	for _, tt := range []struct {
		name   string
		user   uuid.UUID
		path   string
		body   string
		key    string
		status int
		code   string
	}{
		{"no session", uuid.Nil, classPath, body(f.StudentAID), "k", 401, "unauthenticated"},
		{"no key", f.InstructorID, classPath, body(f.StudentAID), "", 400, "invalid_request"},
		{"student", f.StudentAID, classPath, body(f.StudentBID), "k", 403, "forbidden"},
		{"ADMIN without membership", f.AdminID, classPath, body(f.StudentAID), "k", 403, "forbidden"},
		{"other organization", f.ForeignID, classPath, body(f.StudentAID), "k", 403, "forbidden"},
		{"missing class", f.InstructorID, "/api/v1/classes/" + uuid.NewString() + "/lab-executions", body(f.StudentAID), "k", 404, "not_found"},
		{"other class student", f.InstructorID, classPath, body(f.OutsiderID), "k", 422, "invalid_target_students"},
		{"instructor as target", f.InstructorID, classPath, body(f.CoInstructorID), "k", 422, "invalid_target_students"},
	} {
		if w := send(http.MethodPost, tt.path, tt.body, tt.user, tt.key); w.Code != tt.status || code(w) != tt.code {
			t.Fatalf("%s: %d %s", tt.name, w.Code, w.Body)
		}
	}

	created := send(http.MethodPost, classPath, body(f.StudentAID, f.StudentBID), f.InstructorID, "http-provision")
	var accepted struct {
		OperationID string
		Target      struct{ Type, ID string }
	}
	if err := json.Unmarshal(created.Body.Bytes(), &accepted); err != nil || created.Code != 202 || accepted.Target.Type != "LAB_EXECUTION" ||
		created.Header().Get("Location") != "/api/v1/operations/"+accepted.OperationID {
		t.Fatalf("created=%d %s location=%q", created.Code, created.Body, created.Header().Get("Location"))
	}
	replay := send(http.MethodPost, classPath, body(f.StudentBID, f.StudentAID), f.InstructorID, "http-provision")
	if replay.Code != 200 || replay.Body.String() != created.Body.String() || replay.Header().Get("Location") != "" {
		t.Fatalf("replay=%d %s", replay.Code, replay.Body)
	}
	if w := send(http.MethodPost, classPath, body(f.StudentAID), f.InstructorID, "http-provision"); w.Code != 409 || code(w) != "idempotency_key_reused" {
		t.Fatalf("same key other meaning=%d %s", w.Code, w.Body)
	}
	if w := send(http.MethodPost, classPath, body(f.StudentAID), f.InstructorID, "http-provision-2"); w.Code != 409 || code(w) != "active_lab_execution_exists" {
		t.Fatalf("active=%d %s", w.Code, w.Body)
	}

	// Location의 Operation은 접수 상태(PENDING)이고 대상은 LabExecution이다.
	op := send(http.MethodGet, created.Header().Get("Location"), "", f.InstructorID, "")
	var operationBody struct {
		ID, Type, Status string
		Target           struct{ Type, ID string }
	}
	if err := json.Unmarshal(op.Body.Bytes(), &operationBody); err != nil || op.Code != 200 || operationBody.Type != "PROVISION" || operationBody.Status != "PENDING" ||
		operationBody.Target.ID != accepted.Target.ID {
		t.Fatalf("operation=%d %s", op.Code, op.Body)
	}

	executionPath := "/api/v1/lab-executions/" + accepted.Target.ID
	exec := send(http.MethodGet, executionPath, "", f.InstructorID, "")
	var execution struct {
		ID, ClassID, LabSpecID, InstructorUserID, Status string
		TargetUserIDs                                    []string
		LabInstances                                     []struct {
			ID, UserID, Status string
			Generation         int
		}
	}
	if err := json.Unmarshal(exec.Body.Bytes(), &execution); err != nil || exec.Code != 200 || execution.ClassID != f.ClassID.String() ||
		execution.LabSpecID != f.LabSpecID.String() || execution.InstructorUserID != f.InstructorID.String() || execution.Status != "PROVISIONING" ||
		len(execution.TargetUserIDs) != 2 || len(execution.LabInstances) != 3 || execution.LabInstances[0].UserID != f.InstructorID.String() {
		t.Fatalf("execution=%d %s", exec.Code, exec.Body)
	}
	instanceOf := map[string]string{}
	for _, i := range execution.LabInstances {
		if i.Status != "PENDING" || i.Generation != 1 {
			t.Fatalf("instance=%+v", i)
		}
		instanceOf[i.UserID] = i.ID
	}
	if instanceOf[f.StudentCID.String()] != "" {
		t.Fatal("unselected student got a LabInstance")
	}
	for _, tt := range []struct {
		path   string
		user   uuid.UUID
		status int
	}{
		{executionPath, f.CoInstructorID, 200}, {executionPath, f.StudentAID, 403}, {executionPath, f.AdminID, 403}, {executionPath, f.ForeignID, 403},
		{"/api/v1/lab-executions/" + uuid.NewString(), f.InstructorID, 404},
		{"/api/v1/lab-instances/" + instanceOf[f.StudentAID.String()], f.StudentAID, 200},
		{"/api/v1/lab-instances/" + instanceOf[f.StudentAID.String()], f.InstructorID, 200},
		{"/api/v1/lab-instances/" + instanceOf[f.StudentAID.String()], f.StudentBID, 403},
		{"/api/v1/lab-instances/" + instanceOf[f.InstructorID.String()], f.StudentAID, 403},
		{"/api/v1/lab-instances/" + instanceOf[f.StudentAID.String()], f.OutsiderID, 403},
		{"/api/v1/lab-instances/" + instanceOf[f.StudentAID.String()], f.AdminID, 403},
		{"/api/v1/lab-instances/not-an-id", f.StudentAID, 404},
	} {
		if w := send(http.MethodGet, tt.path, "", tt.user, ""); w.Code != tt.status {
			t.Fatalf("GET %s as %s = %d want %d: %s", tt.path, tt.user, w.Code, tt.status, w.Body)
		}
	}
	instance := send(http.MethodGet, "/api/v1/lab-instances/"+instanceOf[f.StudentAID.String()], "", f.StudentAID, "")
	var instanceBody map[string]any
	if err := json.Unmarshal(instance.Body.Bytes(), &instanceBody); err != nil || instanceBody["labExecutionId"] != accepted.Target.ID ||
		instanceBody["userId"] != f.StudentAID.String() || instanceBody["generation"] != float64(1) || instanceBody["status"] != "PENDING" {
		t.Fatalf("instance=%s", instance.Body)
	}

	// Class 상세는 active LabExecution과 자신의 LabInstance(선택된 학생만)를 보여 준다. 목록은 active LabExecution만이다.
	var detail struct {
		ActiveLabExecution *struct{ ID, Status string }
		MyLabInstance      *struct{ ID string }
	}
	for _, tt := range []struct {
		user     uuid.UUID
		instance string
	}{{f.StudentAID, instanceOf[f.StudentAID.String()]}, {f.InstructorID, instanceOf[f.InstructorID.String()]}, {f.StudentCID, ""}} {
		detail.ActiveLabExecution, detail.MyLabInstance = nil, nil
		w := send(http.MethodGet, "/api/v1/classes/"+f.ClassID.String(), "", tt.user, "")
		if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || w.Code != 200 || detail.ActiveLabExecution == nil ||
			detail.ActiveLabExecution.ID != accepted.Target.ID || detail.ActiveLabExecution.Status != "PROVISIONING" {
			t.Fatalf("class detail=%d %s", w.Code, w.Body)
		}
		if (tt.instance == "") != (detail.MyLabInstance == nil) || (detail.MyLabInstance != nil && detail.MyLabInstance.ID != tt.instance) {
			t.Fatalf("myLabInstance for %s=%s", tt.user, w.Body)
		}
	}
	list := send(http.MethodGet, "/api/v1/classes", "", f.InstructorID, "")
	if list.Code != 200 || strings.Count(list.Body.String(), `"activeLabExecution"`) != 1 || strings.Contains(list.Body.String(), "myLabInstance") {
		t.Fatalf("class list=%s", list.Body)
	}
	t.Log("HTTP + PostgreSQL: 202+Location, replay 200, 409 key reuse/active, 400/401/403/404/422, GET execution/instance authorization, Class projection, no Provider/script leakage")
}
