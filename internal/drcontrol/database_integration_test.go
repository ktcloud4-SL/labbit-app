//go:build integration

package drcontrol

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	migrationfiles "github.com/ktcloud4-SL/labbit-app/db/migrations"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

// Uses a test-owned database only. TLS/RDS/STS are not exercised by this test.
func TestSignedOperatorWithDisposablePostgres(t *testing.T) {
	dsn := postgrestest.NewDatabase(t)
	ms, e := postgres.LoadMigrations(migrationfiles.Files)
	if e != nil {
		t.Fatal(e)
	}
	postgrestest.Migrate(t, dsn, ms)
	pool, e := pgxpool.New(t.Context(), dsn)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(pool.Close)
	store := postgres.NewWorkerControlStore(pool)
	f := setup(t)
	f.now = time.Now().UTC().Truncate(time.Microsecond)
	r := &f.i.Request
	r.RequestedAt = f.now.Add(-time.Second)
	r.ValidUntil = f.now.Add(20 * time.Second)
	f.observation.Binding = Bind(*r)
	f.observation.ObservedAt = f.now
	f.observation.ValidUntil = f.now.Add(10 * time.Second)
	f.approval.NotBefore = r.RequestedAt
	f.approval.ExpiresAt = r.ValidUntil
	f.refresh(t)
	if e = store.CreatePaused(t.Context(), f.p.Target, uuid.New()); e != nil {
		t.Fatal(e)
	}
	var db, user string
	if pool.QueryRow(t.Context(), "SELECT current_database(), session_user").Scan(&db, &user) != nil {
		t.Fatal("SQL identity read")
	}
	d := f.p.Database
	d.Name = db
	d.User = user
	if VerifySQL(t.Context(), pool, d) != nil {
		t.Fatal("writer identity not verified")
	}
	d.Name = "foreign"
	if VerifySQL(t.Context(), pool, d) == nil {
		t.Fatal("wrong SQL database accepted")
	}
	c, cancel := context.WithCancel(t.Context())
	cancel()
	if VerifySQL(c, pool, f.p.Database) == nil {
		t.Fatal("cancelled SQL accepted")
	}
	receipt, e := Execute(t.Context(), f.p, f.i, f.id, f.art, store, func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) })
	if e != nil || receipt.Status != wc.ResumeApplied {
		t.Fatal("signed resume did not reach durable store", e)
	}
	report, e := store.Report(t.Context(), f.p.Target)
	if e != nil || report.State.Mode != wc.Active || report.State.Revision != 2 || report.State.Epoch != 2 {
		t.Fatal("new binding missing", e)
	}
	old := wc.Ticket{Binding: wc.Binding{Scope: f.p.Target, Revision: 1, Epoch: 1, Mode: wc.Active, PodID: "old-pod", BootID: uuid.New()}, ID: uuid.New(), Kind: wc.MutationBoundary, Phase: wc.MutationClaim}
	if e = store.Enter(t.Context(), old); !errors.Is(e, wc.ErrStale) {
		t.Fatal("old process binding accepted", e)
	}
	if _, e = Execute(t.Context(), f.p, f.i, f.id, f.art, store, func() time.Time { return time.Now().UTC() }); e != nil {
		t.Fatal("historical receipt failed", e)
	}
	var rows int
	if pool.QueryRow(t.Context(), "SELECT count(*) FROM worker_control_resumes").Scan(&rows) != nil || rows != 1 {
		t.Fatal("duplicate resume wrote new intent")
	}
}
