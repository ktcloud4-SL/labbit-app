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

// HTTP/real PostgreSQL receipt checks. settle is only fixture preparation for
// RESET/CLEANUP, not evidence of Worker drain (covered by app integration).
func TestDRMaintenanceCommittedReplayAndCurrentAuthorityAgainstPostgres(t *testing.T) {
	for _, kind := range []string{"PROVISION", "RESET", "CLEANUP"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mode")
			set := func(mode string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(mode), 0600); err != nil {
					t.Fatal(err)
				}
			}
			set("open")
			l := newLabHTTP(t, maintenance.New(path).Enter)
			f := l.f
			url := "/api/v1/classes/" + f.ClassID.String() + "/lab-executions"
			body := `{"labSpecId":"` + f.LabSpecID.String() + `","targetStudentIds":["` + f.StudentAID.String() + `"]}`
			key := "dr-replay-provision"
			original := l.send(http.MethodPost, url, body, f.InstructorID, key)
			created := decodeAccepted(t, original, 202, "LAB_EXECUTION")
			if kind != "PROVISION" {
				l.settle()
				body = ""
				key = "dr-replay-" + kind
				if kind == "RESET" {
					var id string
					if err := l.conn.QueryRow(t.Context(), `SELECT id::text FROM lab_instances WHERE lab_execution_id=$1 AND user_id=$2`, created.Target.ID, f.StudentAID).Scan(&id); err != nil {
						t.Fatal(err)
					}
					url = "/api/v1/lab-instances/" + id + "/reset"
				} else {
					url = "/api/v1/lab-executions/" + created.Target.ID + "/cleanup"
				}
				original = l.send(http.MethodPost, url, body, f.InstructorID, key)
				if original.Code != 202 {
					t.Fatalf("fixture acceptance=%d %s", original.Code, original.Body)
				}
			}
			set("closed")
			snapshot := func() string {
				t.Helper()
				var value string
				if err := l.conn.QueryRow(t.Context(), `SELECT jsonb_build_object('operations',(SELECT jsonb_agg(to_jsonb(o) ORDER BY o.id) FROM operations o),'items',(SELECT jsonb_agg(to_jsonb(i) ORDER BY i.id) FROM operation_items i))::text`).Scan(&value); err != nil {
					t.Fatal(err)
				}
				return value
			}
			before := snapshot()
			for range 2 {
				replay := l.send(http.MethodPost, url, body, f.InstructorID, key)
				if replay.Code != 200 || replay.Body.String() != original.Body.String() || replay.Header().Get("Location") != "" {
					t.Fatalf("replay changed receipt: %d %s", replay.Code, replay.Body)
				}
			}
			for _, tc := range []struct {
				user   uuid.UUID
				key    string
				status int
				code   string
			}{
				{uuid.Nil, key, 401, "unauthenticated"},
				{f.StudentAID, key, 403, "forbidden"},
				{f.AdminID, key, 403, "forbidden"},
				{f.InstructorID, "new-closed-key", 503, "maintenance"},
			} {
				w := l.send(http.MethodPost, url, body, tc.user, tc.key)
				if w.Code != tc.status || problemCode(t, w) != tc.code {
					t.Fatalf("authority/closed=%d %s", w.Code, w.Body)
				}
			}
			if kind == "PROVISION" {
				other := `{"labSpecId":"` + f.LabSpecID.String() + `","targetStudentIds":["` + f.StudentBID.String() + `"]}`
				w := l.send(http.MethodPost, url, other, f.InstructorID, key)
				if w.Code != 409 || problemCode(t, w) != "idempotency_key_reused" {
					t.Fatalf("different meaning=%d %s", w.Code, w.Body)
				}
			}
			if snapshot() != before {
				t.Fatal("replay or denied requests mutated durable work")
			}
			// The original key is not authority: current membership still wins.
			l.exec(`DELETE FROM class_memberships WHERE class_id=$1 AND user_id=$2`, f.ClassID, f.InstructorID)
			w := l.send(http.MethodPost, url, body, f.InstructorID, key)
			if w.Code != 403 || problemCode(t, w) != "forbidden" {
				t.Fatalf("revoked replay=%d %s", w.Code, w.Body)
			}
			if snapshot() != before {
				t.Fatal("revoked replay changed work")
			}
		})
	}
}
