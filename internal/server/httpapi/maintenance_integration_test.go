//go:build integration

package httpapi_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/server/maintenance"
)

func TestMaintenanceHTTPAgainstPostgres(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mode")
	set := func(v string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(v), 0600); err != nil {
			t.Fatal(err)
		}
	}
	set("closed")
	gate := maintenance.New(path)
	l := newLabHTTP(t, gate.Enter)
	f := l.f
	provisionPath := "/api/v1/classes/" + f.ClassID.String() + "/lab-executions"
	body := `{"labSpecId":"` + f.LabSpecID.String() + `","targetStudentIds":["` + f.StudentAID.String() + `"]}`
	check := func(want int, code string, path, body, key string, user uuid.UUID) {
		t.Helper()
		w := l.send(http.MethodPost, path, body, user, key)
		if w.Code != want || (code != "" && problemCode(t, w) != code) {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
	}
	check(401, "unauthenticated", provisionPath, body, "p", uuid.Nil)
	check(403, "forbidden", provisionPath, body, "p", f.StudentAID)
	check(503, "maintenance", provisionPath, body, "p", f.InstructorID)
	if l.count(`SELECT count(*) FROM operations`) != 0 || l.count(`SELECT count(*) FROM operation_items`) != 0 {
		t.Fatal("closed provision wrote operations")
	}
	set("open")
	created := decodeAccepted(t, l.send(http.MethodPost, provisionPath, body, f.InstructorID, "p"), 202, "LAB_EXECUTION")
	l.settle()
	var instance string
	if err := l.conn.QueryRow(t.Context(), `SELECT id::text FROM lab_instances WHERE lab_execution_id=$1 AND user_id=$2`, created.Target.ID, f.StudentAID).Scan(&instance); err != nil {
		t.Fatal(err)
	}
	reset := "/api/v1/lab-instances/" + instance + "/reset"
	cleanup := "/api/v1/lab-executions/" + created.Target.ID + "/cleanup"
	set("closed")
	check(200, "", provisionPath, body, "p", f.InstructorID)
	beforeOps, beforeItems := l.count(`SELECT count(*) FROM operations`), l.count(`SELECT count(*) FROM operation_items`)
	for _, p := range []string{reset, cleanup} {
		check(503, "maintenance", p, "", "new", f.InstructorID)
		check(403, "forbidden", p, "", "new", f.StudentAID)
	}
	if l.count(`SELECT count(*) FROM operations`) != beforeOps || l.count(`SELECT count(*) FROM operation_items`) != beforeItems {
		t.Fatal("closed mutations wrote operations")
	}
	set("open")
	decodeAccepted(t, l.send(http.MethodPost, reset, "", f.InstructorID, "r"), 202, "LAB_INSTANCE")
	set("closed")
	check(200, "", reset, "", "r", f.InstructorID)
	l.settle()
	set("open")
	decodeAccepted(t, l.send(http.MethodPost, cleanup, "", f.InstructorID, "c"), 202, "LAB_EXECUTION")
	set("closed")
	check(200, "", cleanup, "", "c", f.InstructorID)
}
