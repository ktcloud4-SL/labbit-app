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
	"github.com/jackc/pgx/v5"

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

type labHTTP struct {
	t      *testing.T
	h      http.Handler
	conn   *pgx.Conn
	f      *labexecutiontest.Fixture
	tokens map[uuid.UUID]string
}

func newLabHTTP(t *testing.T) *labHTTP {
	t.Helper()
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
	l := &labHTTP{t: t, h: h, conn: conn, f: f, tokens: map[uuid.UUID]string{}}
	now := time.Now().UTC()
	for i, user := range f.Users() {
		raw := bytes.Repeat([]byte{byte(i + 1)}, 32)
		hash := sha256.Sum256(raw)
		l.tokens[user] = base64.RawURLEncoding.EncodeToString(raw)
		if err := store.CreateAuthSession(t.Context(), repository.AuthSession{ID: uuid.New(), UserID: user, TokenHash: hash[:], CreatedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

// send는 Frontend client처럼 Reset/Cleanup을 body와 Content-Type 없이 보낸다. Provision만 JSON body가 있다.
func (l *labHTTP) send(method, path, body string, user uuid.UUID, key string) *httptest.ResponseRecorder {
	l.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if user != uuid.Nil {
		r.AddCookie(&http.Cookie{Name: "__Host-labbit-session", Value: l.tokens[user]})
	}
	if method == http.MethodPost {
		r.Header.Set("Origin", trustedOrigin)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	l.h.ServeHTTP(w, r)
	// 응답에는 CreationSnapshot의 실제 Provider 값·Startup Script, ProviderResource ID, 내부 Idempotency/Trace/DB metadata가 없다.
	for _, leak := range []string{labexecutiontest.ImageMarker, labexecutiontest.FlavorMarker, labexecutiontest.ScriptMarker, "server-marker-",
		"idempotency_key_hash", "request_fingerprint", "traceparent", "SQLSTATE", "operation_items"} {
		if strings.Contains(w.Body.String(), leak) {
			l.t.Fatalf("%s %s leaked %q: %s", method, path, leak, w.Body)
		}
	}
	return w
}

func (l *labHTTP) exec(sql string, args ...any) {
	l.t.Helper()
	labexecutiontest.Exec(l.t, l.conn, sql, args...)
}

func (l *labHTTP) count(sql string, args ...any) int {
	l.t.Helper()
	var n int
	if err := l.conn.QueryRow(l.t.Context(), sql, args...).Scan(&n); err != nil {
		l.t.Fatal(err)
	}
	return n
}

// settle은 Worker(LBT-18) 대역으로 진행 중인 모든 item/Operation을 SUCCEEDED로 만든다. 격리된 test database에서만 쓴다.
func (l *labHTTP) settle() {
	l.t.Helper()
	l.exec(`UPDATE operation_items SET status = 'SUCCEEDED', started_at = created_at, finished_at = created_at WHERE status IN ('PENDING', 'RUNNING', 'RECONCILING')`)
	l.exec(`UPDATE operations SET status = 'SUCCEEDED', started_at = created_at, finished_at = created_at, updated_at = created_at WHERE status IN ('PENDING', 'RUNNING', 'RECONCILING')`)
	l.exec(`UPDATE lab_instances SET status = 'READY'`)
	l.exec(`UPDATE lab_executions SET status = 'READY' WHERE finished_at IS NULL`)
}

type acceptedBody struct {
	OperationID string
	Target      struct{ Type, ID string }
}

func decodeAccepted(t *testing.T, w *httptest.ResponseRecorder, status int, targetType string) acceptedBody {
	t.Helper()
	var a acceptedBody
	if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil || w.Code != status || a.Target.Type != targetType || a.OperationID == "" {
		t.Fatalf("status=%d want %d body=%s", w.Code, status, w.Body)
	}
	if status == http.StatusAccepted && w.Header().Get("Location") != "/api/v1/operations/"+a.OperationID {
		t.Fatalf("location=%q", w.Header().Get("Location"))
	}
	if status == http.StatusOK && w.Header().Get("Location") != "" {
		t.Fatalf("replay location=%q", w.Header().Get("Location"))
	}
	return a
}

// LBT-17 HTTP·PostgreSQL 통합 경로: Provision 접수 → (Worker 대역 settle) → Reset/Cleanup 접수와 replay·충돌·권한 →
// Operation 조회 → (Worker 대역 종료) → 같은 Class의 다음 Provision. 실제 Provider/Worker/Browser는 포함하지 않는다.
func TestResetCleanupHTTPAgainstPostgres(t *testing.T) {
	l := newLabHTTP(t)
	f := l.f
	provision := l.send(http.MethodPost, "/api/v1/classes/"+f.ClassID.String()+"/lab-executions",
		`{"labSpecId":"`+f.LabSpecID.String()+`","targetStudentIds":["`+f.StudentAID.String()+`","`+f.StudentBID.String()+`"]}`, f.InstructorID, "provision-1")
	execution := decodeAccepted(t, provision, http.StatusAccepted, "LAB_EXECUTION").Target.ID
	instance := map[uuid.UUID]string{}
	rows, err := l.conn.Query(t.Context(), `SELECT user_id, id::text FROM lab_instances WHERE lab_execution_id = $1`, execution)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var user uuid.UUID
		var id string
		if err := rows.Scan(&user, &id); err != nil {
			t.Fatal(err)
		}
		instance[user] = id
	}
	resetPath := func(user uuid.UUID) string { return "/api/v1/lab-instances/" + instance[user] + "/reset" }
	cleanupPath := "/api/v1/lab-executions/" + execution + "/cleanup"

	// Provision이 아직 진행 중(PENDING item)이면 Reset/Cleanup 모두 충돌이다.
	if w := l.send(http.MethodPost, resetPath(f.StudentAID), "", f.InstructorID, "early-reset"); w.Code != 409 || problemCode(t, w) != "operation_in_progress" {
		t.Fatalf("reset during provision=%d %s", w.Code, w.Body)
	}
	if w := l.send(http.MethodPost, cleanupPath, "", f.InstructorID, "early-cleanup"); w.Code != 409 || problemCode(t, w) != "operation_in_progress" {
		t.Fatalf("cleanup during provision=%d %s", w.Code, w.Body)
	}
	l.settle()
	for user, id := range instance {
		l.exec(`INSERT INTO provider_resources (id, organization_id, lab_instance_id, provider_connection_id, generation, resource_type, logical_name, provider_id, lifecycle_status)
			VALUES ($1, $2, $3, $4, 1, 'SERVER', 'vm-1', $5, 'PRESENT')`, uuid.New(), f.OrganizationID, id, f.ProviderConnectionID, "server-marker-"+user.String())
	}

	// 권한·형식·대상 오류는 아무 Operation도 만들지 않는다.
	for _, tt := range []struct {
		name   string
		path   string
		user   uuid.UUID
		key    string
		status int
		code   string
	}{
		{"reset no session", resetPath(f.StudentAID), uuid.Nil, "k", 401, "unauthenticated"},
		{"reset no key", resetPath(f.StudentAID), f.InstructorID, "", 400, "invalid_request"},
		{"reset key too long", resetPath(f.StudentAID), f.InstructorID, strings.Repeat("k", 256), 400, "invalid_request"},
		{"reset own instance by student", resetPath(f.StudentAID), f.StudentAID, "k", 403, "forbidden"},
		{"reset other student by student", resetPath(f.StudentAID), f.StudentBID, "k", 403, "forbidden"},
		{"reset by ADMIN without membership", resetPath(f.StudentAID), f.AdminID, "k", 403, "forbidden"},
		{"reset by other class member", resetPath(f.StudentAID), f.OutsiderID, "k", 403, "forbidden"},
		{"reset by other organization", resetPath(f.StudentAID), f.ForeignID, "k", 403, "forbidden"},
		{"reset instructor instance", resetPath(f.InstructorID), f.InstructorID, "k", 422, "invalid_reset_target"},
		{"reset missing instance", "/api/v1/lab-instances/" + uuid.NewString() + "/reset", f.InstructorID, "k", 404, "not_found"},
		{"reset malformed id", "/api/v1/lab-instances/not-an-id/reset", f.InstructorID, "k", 404, "not_found"},
		{"cleanup no session", cleanupPath, uuid.Nil, "k", 401, "unauthenticated"},
		{"cleanup no key", cleanupPath, f.InstructorID, "", 400, "invalid_request"},
		{"cleanup by student", cleanupPath, f.StudentAID, "k", 403, "forbidden"},
		{"cleanup by ADMIN without membership", cleanupPath, f.AdminID, "k", 403, "forbidden"},
		{"cleanup by other organization", cleanupPath, f.ForeignID, "k", 403, "forbidden"},
		{"cleanup missing execution", "/api/v1/lab-executions/" + uuid.NewString() + "/cleanup", f.InstructorID, "k", 404, "not_found"},
		{"cleanup malformed id", "/api/v1/lab-executions/x/cleanup", f.InstructorID, "k", 404, "not_found"},
	} {
		if w := l.send(http.MethodPost, tt.path, "", tt.user, tt.key); w.Code != tt.status || problemCode(t, w) != tt.code {
			t.Fatalf("%s: %d %s", tt.name, w.Code, w.Body)
		}
	}
	if n := l.count(`SELECT count(*) FROM operations WHERE operation_type IN ('RESET', 'CLEANUP')`); n != 0 {
		t.Fatalf("rejected requests created %d operations", n)
	}

	// Reset: 202 + Location, 같은 key는 200 replay, 다른 의미는 409, 같은 대상의 다른 변경은 409.
	reset := decodeAccepted(t, l.send(http.MethodPost, resetPath(f.StudentAID), "", f.InstructorID, "reset-a"), http.StatusAccepted, "LAB_INSTANCE")
	if reset.Target.ID != instance[f.StudentAID] {
		t.Fatalf("reset target=%s", reset.Target.ID)
	}
	if again := decodeAccepted(t, l.send(http.MethodPost, resetPath(f.StudentAID), "", f.InstructorID, "reset-a"), http.StatusOK, "LAB_INSTANCE"); again != reset {
		t.Fatalf("replay=%+v want %+v", again, reset)
	}
	if w := l.send(http.MethodPost, resetPath(f.StudentBID), "", f.InstructorID, "reset-a"); w.Code != 409 || problemCode(t, w) != "idempotency_key_reused" {
		t.Fatalf("same key other instance=%d %s", w.Code, w.Body)
	}
	if w := l.send(http.MethodPost, resetPath(f.StudentAID), "", f.CoInstructorID, "reset-a-2"); w.Code != 409 || problemCode(t, w) != "operation_in_progress" {
		t.Fatalf("second reset=%d %s", w.Code, w.Body)
	}
	if w := l.send(http.MethodPost, cleanupPath, "", f.InstructorID, "cleanup-1"); w.Code != 409 || problemCode(t, w) != "operation_in_progress" {
		t.Fatalf("cleanup during reset=%d %s", w.Code, w.Body)
	}
	// 다른 학생의 Reset은 독립적이다.
	decodeAccepted(t, l.send(http.MethodPost, resetPath(f.StudentBID), "", f.CoInstructorID, "reset-b"), http.StatusAccepted, "LAB_INSTANCE")

	// Location의 Operation: RESET/PENDING, 대상 LabInstance. 강사와 그 LabInstance owner만 읽는다.
	for _, tt := range []struct {
		user   uuid.UUID
		status int
	}{{f.InstructorID, 200}, {f.CoInstructorID, 200}, {f.StudentAID, 200}, {f.StudentBID, 403}, {f.AdminID, 403}, {f.ForeignID, 403}} {
		w := l.send(http.MethodGet, "/api/v1/operations/"+reset.OperationID, "", tt.user, "")
		if w.Code != tt.status {
			t.Fatalf("GET reset operation as %s=%d %s", tt.user, w.Code, w.Body)
		}
		if tt.status == 200 {
			var op struct {
				ID, Type, Status string
				Target           struct{ Type, ID string }
			}
			if err := json.Unmarshal(w.Body.Bytes(), &op); err != nil || op.ID != reset.OperationID || op.Type != "RESET" || op.Status != "PENDING" ||
				op.Target.Type != "LAB_INSTANCE" || op.Target.ID != instance[f.StudentAID] {
				t.Fatalf("operation=%s", w.Body)
			}
		}
	}
	// 접수는 LabInstance의 현재 generation을 바꾸지 않는다(활성화는 Worker). item은 다음 generation이다.
	inst := l.send(http.MethodGet, "/api/v1/lab-instances/"+instance[f.StudentAID], "", f.StudentAID, "")
	var instBody struct {
		Generation int
		Status     string
	}
	if err := json.Unmarshal(inst.Body.Bytes(), &instBody); err != nil || inst.Code != 200 || instBody.Generation != 1 || instBody.Status != "READY" {
		t.Fatalf("instance=%d %s", inst.Code, inst.Body)
	}
	if n := l.count(`SELECT count(*) FROM operation_items WHERE operation_id = $1 AND lab_instance_id = $2 AND generation = 2 AND status = 'PENDING'`,
		reset.OperationID, instance[f.StudentAID]); n != 1 {
		t.Fatalf("reset items=%d", n)
	}

	// Cleanup: 이전 Reset이 끝난 뒤 저장된 LabInstance 3개 전부를 현재 generation으로 등록한다. Class Membership 변경과 무관하다.
	l.settle()
	l.exec(`DELETE FROM class_memberships WHERE class_id = $1 AND user_id = $2`, f.ClassID, f.StudentBID)
	cleanup := decodeAccepted(t, l.send(http.MethodPost, cleanupPath, "", f.InstructorID, "cleanup-1"), http.StatusAccepted, "LAB_EXECUTION")
	if cleanup.Target.ID != execution {
		t.Fatalf("cleanup target=%s", cleanup.Target.ID)
	}
	if again := decodeAccepted(t, l.send(http.MethodPost, cleanupPath, "", f.InstructorID, "cleanup-1"), http.StatusOK, "LAB_EXECUTION"); again != cleanup {
		t.Fatalf("cleanup replay=%+v", again)
	}
	if w := l.send(http.MethodPost, cleanupPath, "", f.CoInstructorID, "cleanup-2"); w.Code != 409 || problemCode(t, w) != "operation_in_progress" {
		t.Fatalf("second cleanup=%d %s", w.Code, w.Body)
	}
	if w := l.send(http.MethodPost, resetPath(f.StudentAID), "", f.InstructorID, "reset-during-cleanup"); w.Code != 409 || problemCode(t, w) != "operation_in_progress" {
		t.Fatalf("reset during cleanup=%d %s", w.Code, w.Body)
	}
	if n := l.count(`SELECT count(*) FROM operation_items WHERE operation_id = $1`, cleanup.OperationID); n != 3 {
		t.Fatalf("cleanup items=%d", n)
	}
	op := l.send(http.MethodGet, "/api/v1/operations/"+cleanup.OperationID, "", f.CoInstructorID, "")
	if op.Code != 200 || !strings.Contains(op.Body.String(), `"type":"CLEANUP"`) || !strings.Contains(op.Body.String(), `"status":"PENDING"`) ||
		!strings.Contains(op.Body.String(), `"target":{"type":"LAB_EXECUTION","id":"`+execution+`"}`) {
		t.Fatalf("cleanup operation=%d %s", op.Code, op.Body)
	}
	if w := l.send(http.MethodGet, "/api/v1/operations/"+cleanup.OperationID, "", f.StudentAID, ""); w.Code != 403 {
		t.Fatalf("student reads cleanup operation=%d", w.Code)
	}
	// 접수만으로 LabExecution은 종료되지 않는다. 다음 Provision은 여전히 active LabExecution 충돌이며 이력은 그대로다.
	if w := l.send(http.MethodGet, "/api/v1/lab-executions/"+execution, "", f.InstructorID, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"READY"`) ||
		strings.Count(w.Body.String(), `"userId"`) != 3 {
		t.Fatalf("execution after cleanup accept=%d %s", w.Code, w.Body)
	}
	next := `{"labSpecId":"` + f.LabSpecID.String() + `","targetStudentIds":["` + f.StudentCID.String() + `"]}`
	if w := l.send(http.MethodPost, "/api/v1/classes/"+f.ClassID.String()+"/lab-executions", next, f.InstructorID, "provision-2"); w.Code != 409 || problemCode(t, w) != "active_lab_execution_exists" {
		t.Fatalf("provision during cleanup=%d %s", w.Code, w.Body)
	}
	if n := l.count(`SELECT count(*) FROM provider_resources WHERE lifecycle_status = 'PRESENT'`); n != 3 {
		t.Fatalf("provider resources=%d", n)
	}

	// Worker 대역이 Cleanup을 끝내 LabExecution을 종료했다. 새 Reset/Cleanup은 409, 기존 key replay는 200, 다음 Provision은 202다.
	l.settle()
	l.exec(`UPDATE lab_executions SET finished_at = now(), status = 'CLEANED' WHERE id = $1`, execution)
	if w := l.send(http.MethodPost, cleanupPath, "", f.InstructorID, "cleanup-3"); w.Code != 409 || problemCode(t, w) != "lab_execution_finished" {
		t.Fatalf("cleanup finished=%d %s", w.Code, w.Body)
	}
	if w := l.send(http.MethodPost, resetPath(f.StudentAID), "", f.InstructorID, "reset-finished"); w.Code != 409 || problemCode(t, w) != "lab_execution_finished" {
		t.Fatalf("reset finished=%d %s", w.Code, w.Body)
	}
	decodeAccepted(t, l.send(http.MethodPost, cleanupPath, "", f.InstructorID, "cleanup-1"), http.StatusOK, "LAB_EXECUTION")
	decodeAccepted(t, l.send(http.MethodPost, resetPath(f.StudentAID), "", f.InstructorID, "reset-a"), http.StatusOK, "LAB_INSTANCE")
	decodeAccepted(t, l.send(http.MethodPost, "/api/v1/classes/"+f.ClassID.String()+"/lab-executions", next, f.InstructorID, "provision-2"), http.StatusAccepted, "LAB_EXECUTION")
	if n := l.count(`SELECT count(*) FROM lab_executions`); n != 2 {
		t.Fatalf("executions=%d (history must be kept)", n)
	}
	t.Log("HTTP + PostgreSQL: Reset/Cleanup 202+Location, replay 200, 409 key reuse/in-progress/finished, 400/401/403/404/422, Operation GET authorization, generation/finished_at unchanged at accept, next Provision after finish")
}
