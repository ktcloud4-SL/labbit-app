//go:build integration

package httpapi_test

import (
	"bytes"
	"context"
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
	"github.com/ktcloud4-SL/labbit-app/internal/server/operation"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal/terminaltest"
)

func TestOperationHTTPAgainstPostgres(t *testing.T) {
	dsn := postgrestest.NewDatabase(t)
	migrations, err := postgres.LoadMigrations(migrationfiles.Files)
	if err != nil {
		t.Fatal(err)
	}
	postgrestest.Migrate(t, dsn, migrations)
	conn := postgrestest.Connect(t, dsn)
	f := terminaltest.New(t, conn)
	pool, err := postgres.OpenPool(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store := postgres.NewStore(pool)
	h, err := httpapi.New(httpapi.Options{Auth: auth.NewService(store, auth.Argon2id{}, nil), Classes: class.NewService(store), Operations: operation.NewService(store), PublicOrigin: trustedOrigin})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	created := now.Add(-time.Minute)
	updated := now.Add(-time.Second)
	in := operation.Registration{OrganizationID: f.OrganizationID, RequestedByUserID: f.InstructorID, Type: "PROVISION", LabExecutionID: &f.LabExecutionID, IdempotencyKey: "http-operation-dummy", Fingerprint: operation.SemanticFingerprint([]byte("canonical-request-dummy")), Items: []operation.Item{{LabInstanceID: f.LabInstanceID, Generation: 1}, {LabInstanceID: f.PeerLabInstanceID, Generation: 1}}}
	register := func(in operation.Registration) uuid.UUID {
		t.Helper()
		var result operation.Result
		err := store.WithinTransaction(t.Context(), func(ctx context.Context, r repository.Repositories) error {
			var e error
			result, e = operation.Register(ctx, r, in, created)
			return e
		})
		if err != nil {
			t.Fatal(err)
		}
		return result.Operation.ID
	}
	provision := register(in)
	const sensitive = "provider-password-raw-dummy"
	terminaltest.Exec(t, conn, `UPDATE operations SET status='FAILED',stage='FINALIZING',error_code=$2,error_message=$2,started_at=$3,finished_at=$4,updated_at=$4 WHERE id=$1`, provision, sensitive, created.Add(time.Second), updated)
	terminaltest.Exec(t, conn, `UPDATE operation_items SET status='FAILED',finished_at=$2 WHERE operation_id=$1`, provision, updated)
	in.Type = "RESET"
	in.LabExecutionID = nil
	in.LabInstanceID = &f.LabInstanceID
	in.Items = in.Items[:1]
	in.Items[0].Generation = 2
	in.IdempotencyKey = "http-reset-dummy"
	in.Fingerprint = operation.SemanticFingerprint([]byte("reset-instance-generation-dummy"))
	reset := register(in)
	// Authentication uses real DB sessions and auth.Service. Tokens below are disposable test values.
	tokens := map[uuid.UUID]string{}
	for i, user := range []uuid.UUID{f.InstructorID, f.OwnerID, f.PeerID, f.OutsiderID, f.ForeignID} {
		raw := bytes.Repeat([]byte{byte(i + 1)}, 32)
		hash := sha256.Sum256(raw)
		tokens[user] = base64.RawURLEncoding.EncodeToString(raw)
		if err := store.CreateAuthSession(t.Context(), repository.AuthSession{ID: uuid.New(), UserID: user, TokenHash: hash[:], CreatedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	send := func(id, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/operations/"+id, nil)
		if token != "" {
			r.AddCookie(&http.Cookie{Name: "__Host-labbit-session", Value: token})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, tt := range []struct {
		user   uuid.UUID
		id     uuid.UUID
		status int
	}{
		{f.InstructorID, provision, 200}, {f.OwnerID, provision, 403}, {f.PeerID, provision, 403}, {f.OutsiderID, provision, 403}, {f.ForeignID, provision, 403},
		{f.InstructorID, reset, 200}, {f.OwnerID, reset, 200}, {f.PeerID, reset, 403}, {f.OutsiderID, reset, 403}, {f.ForeignID, reset, 403},
	} {
		w := send(tt.id.String(), tokens[tt.user])
		if w.Code != tt.status {
			t.Fatalf("user %s op %s status=%d want=%d: %s", tt.user, tt.id, w.Code, tt.status, w.Body)
		}
		if strings.Contains(w.Body.String(), sensitive) || strings.Contains(w.Body.String(), "traceparent") || strings.Contains(w.Body.String(), "idempotency") {
			t.Fatal("internal metadata leaked")
		}
		if tt.status == 200 {
			var body struct {
				ID, Type, Status, Stage string
				Target                  struct{ Type, ID string }
				Error                   *struct{ Code, Message string }
				CreatedAt, UpdatedAt    time.Time
				StartedAt, FinishedAt   *time.Time
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.ID != tt.id.String() || !body.CreatedAt.Equal(created) {
				t.Fatalf("body=%+v", body)
			}
			if tt.id == provision {
				if body.Type != "PROVISION" || body.Status != "FAILED" || body.Stage != "FINALIZING" || body.Target.Type != "LAB_EXECUTION" || body.Target.ID != f.LabExecutionID.String() || body.Error == nil || body.Error.Code != "operation_failed" || body.StartedAt == nil || body.FinishedAt == nil || !body.FinishedAt.Equal(updated) || !body.UpdatedAt.Equal(updated) {
					t.Fatalf("failed operation=%+v", body)
				}
			} else if body.Type != "RESET" || body.Status != "PENDING" || body.Target.Type != "LAB_INSTANCE" || body.Target.ID != f.LabInstanceID.String() || body.Error != nil || body.StartedAt != nil || body.FinishedAt != nil || !body.UpdatedAt.Equal(created) {
				t.Fatalf("pending reset=%+v", body)
			}
		} else if strings.Contains(w.Body.String(), "FINALIZING") || strings.Contains(w.Body.String(), f.LabExecutionID.String()) {
			t.Fatal("forbidden response leaked state")
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable operation")
		}
	}
	for _, id := range []string{uuid.NewString(), "not-a-uuid", uuid.Nil.String()} {
		if w := send(id, tokens[f.InstructorID]); w.Code != 404 {
			t.Fatalf("404=%d %s", w.Code, w.Body)
		}
	}
	for _, token := range []string{"", "invalid-session"} {
		if w := send(provision.String(), token); w.Code != 401 {
			t.Fatalf("401=%d", w.Code)
		}
	}
	terminaltest.Exec(t, conn, `UPDATE users SET organization_role='ADMIN' WHERE id=$1`, f.OutsiderID)
	if w := send(provision.String(), tokens[f.OutsiderID]); w.Code != 403 {
		t.Fatalf("ADMIN bypass=%d", w.Code)
	}
	terminaltest.Exec(t, conn, `DELETE FROM class_memberships WHERE class_id=$1 AND user_id=$2`, f.ClassID, f.InstructorID)
	if w := send(provision.String(), tokens[f.InstructorID]); w.Code != 403 {
		t.Fatalf("removed membership=%d", w.Code)
	}
	terminaltest.Exec(t, conn, `UPDATE users SET disabled_at=$2 WHERE id=$1`, f.OwnerID, now)
	if w := send(reset.String(), tokens[f.OwnerID]); w.Code != 401 {
		t.Fatalf("disabled session=%d", w.Code)
	}
	terminaltest.Exec(t, conn, `ALTER TABLE operations RENAME TO operations_unavailable`)
	w := send(provision.String(), tokens[f.PeerID])
	if w.Code != 500 || strings.Contains(w.Body.String(), "operations_unavailable") || strings.Contains(w.Body.String(), "SQLSTATE") {
		t.Fatalf("safe DB error=%d %s", w.Code, w.Body)
	}
	t.Log("HTTP + PostgreSQL: PROVISION/RESET state, target, required updatedAt, optional timestamps/error; 200/401/403/404/500; membership revocation and tenant isolation")
}
