//go:build integration

package drcontrol

import (
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	migrationfiles "github.com/ktcloud4-SL/labbit-app/db/migrations"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
	"strings"
	"sync"
	"testing"
	"time"
)

func linkageDB(t *testing.T) (*pgxpool.Pool, *postgres.WorkerControlStore) {
	t.Helper()
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
	return pool, postgres.NewWorkerControlStore(pool)
}
func newRestrictionAt(scope wc.Scope, action string, b wc.Binding) (wc.RestrictRequest, wc.ResumeApproval) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	r := wc.RestrictRequest{CommandID: uuid.New(), Action: action, Scope: scope, Binding: b, TrialID: "trial", RunID: "run", Phase: "activate", Principal: "operator", ApprovalRef: uuid.NewString(), PlanSHA256: strings.Repeat("a", 64), RequestedAt: now.Add(-time.Second), ValidUntil: now.Add(30 * time.Second)}
	return r, wc.ResumeApproval{ID: uuid.MustParse(r.ApprovalRef), RequestSHA256: r.SHA256(), Principal: r.Principal, NotBefore: r.RequestedAt, ExpiresAt: r.ValidUntil}
}
func TestRestrictionInitPauseCASAndUnknownWithDisposablePostgres(t *testing.T) {
	pool, store := linkageDB(t)
	f, i, _ := restrictionFixture(t)
	r, a := newRestrictionAt(f.p.Target, "init", wc.Binding{})
	i.Request = r
	raw := signedFixture(t, Approval{Version: Version, Action: r.Action, RequestSHA256: r.SHA256(), Principal: r.Principal, ID: a.ID, NotBefore: a.NotBefore, ExpiresAt: a.ExpiresAt}, "approver", "restriction-approval", f.ap)
	i.Approval.SHA256 = Hash(raw)
	i.Approval.Size = int64(len(raw))
	f.art.values[i.Approval.VersionID] = raw
	call := func() (wc.RestrictRecord, error) {
		return ExecuteRestriction(t.Context(), f.p, i, f.id, f.art, store, func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) })
	}
	if v, e := call(); e != nil || v.Status != wc.ResumeApplied {
		t.Fatal(v, e)
	}
	if _, e := call(); e != nil {
		t.Fatal("historical init", e)
	}
	// Real CAS: an unrelated bootstrap never replaces an existing scope.
	other, approval := newRestrictionAt(f.p.Target, "init", wc.Binding{})
	if _, e := store.BeginRestriction(t.Context(), other, approval); e == nil {
		t.Fatal("init reset existing scope")
	}
	// Resume uses its existing signed port, then pause invalidates all old bindings.
	f.now = time.Now().UTC().Truncate(time.Microsecond)
	f.i.Request.RequestedAt = f.now.Add(-time.Second)
	f.i.Request.ValidUntil = f.now.Add(30 * time.Second)
	f.observation.Binding = Bind(f.i.Request)
	f.observation.ObservedAt = f.now
	f.observation.ValidUntil = f.now.Add(20 * time.Second)
	f.approval.NotBefore = f.i.Request.RequestedAt
	f.approval.ExpiresAt = f.i.Request.ValidUntil
	f.refresh(t)
	if _, e := Execute(t.Context(), f.p, f.i, f.id, f.art, store, func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }); e != nil {
		t.Fatal("resume", e)
	}
	b := wc.Binding{Scope: f.p.Target, Revision: 2, Epoch: 2, Mode: wc.Active, PodID: "backend-1", BootID: uuid.New()}
	ticket := wc.Ticket{ID: uuid.New(), Binding: b, Kind: wc.MutationBoundary, Phase: wc.MutationClaim}
	if e := store.Enter(t.Context(), ticket); e != nil {
		t.Fatal(e)
	}
	pause, proof := newRestrictionAt(f.p.Target, "pause", b)
	if created, e := store.BeginRestriction(t.Context(), pause, proof); e != nil || !created {
		t.Fatal(e)
	}
	if v, e := store.ApplyRestriction(t.Context(), pause); e != nil || v.Status != wc.ResumeApplied {
		t.Fatal(v, e)
	}
	if e := store.Enter(t.Context(), wc.Ticket{ID: uuid.New(), Binding: b, Kind: wc.MutationBoundary, Phase: wc.MutationDispatch}); !errors.Is(e, wc.ErrStale) {
		t.Fatal("stale worker dispatch", e)
	}
	if e := store.Leave(t.Context(), ticket); e != nil {
		t.Fatal("old boundary drain", e)
	}
	w, e := (SQLWorkReader{Pool: pool}).ReadWork(t.Context(), f.p.Target)
	if e != nil || w.State.Revision != 3 || w.State.Epoch != 2 || w.OpenBoundaries != 0 || w.PendingRestrictions != 0 || !w.Writer {
		t.Fatal("actual SQL readback", w, e)
	}
	// A paused no-op is still a durable, exact command receipt.
	b.Revision = 3
	b.Mode = wc.PauseNew
	noop, proof := newRestrictionAt(f.p.Target, "pause", b)
	if _, e = store.BeginRestriction(t.Context(), noop, proof); e != nil {
		t.Fatal(e)
	}
	if _, e = store.ApplyRestriction(t.Context(), noop); e != nil {
		t.Fatal(e)
	}
	unknown, proof := newRestrictionAt(f.p.Target, "pause", b)
	if _, e = store.BeginRestriction(t.Context(), unknown, proof); e != nil {
		t.Fatal(e)
	}
	s, _ := wc.NewRestrictor(store, restrictionVerifier{}, time.Now)
	if _, e = s.Restrict(t.Context(), unknown); !errors.Is(e, wc.ErrUnknown) {
		t.Fatal("restart reapplied INTENT", e)
	}
	changed := unknown
	changed.RunID = "changed"
	if _, _, e = s.Inspect(t.Context(), changed); !errors.Is(e, wc.ErrConflict) {
		t.Fatal("payload conflict", e)
	}
	w, e = (SQLWorkReader{Pool: pool}).ReadWork(t.Context(), f.p.Target)
	if e != nil || w.PendingRestrictions != 1 || w.State.Revision != 3 {
		t.Fatal("UNKNOWN not preserved", w, e)
	}
	f.i.Request.ExpectedRevision = 3
	f.i.Request.ExpectedEpoch = 2
	f.i.Request.CommandID = uuid.New()
	f.observation.Binding = Bind(f.i.Request)
	f.refresh(t)
	v, _ := NewVerifier(f.p, f.i, f.art, func() time.Time { return f.now })
	aa, _ := v.AuthorizeResume(t.Context(), f.i.Request)
	cc, _ := v.RecheckResume(t.Context(), f.i.Request)
	if _, e = store.BeginResume(t.Context(), f.i.Request, wc.ResumeProof{Approval: aa, Checks: cc}); !errors.Is(e, wc.ErrHold) {
		t.Fatal("resume ignored pending restriction", e)
	}
}
func TestRestrictionConcurrentOperatorsAndAppliedConstraint(t *testing.T) {
	pool, store := linkageDB(t)
	scope := wc.Scope{ID: "concurrent", Region: "ap-northeast-3", Cluster: "fixture"}
	if e := store.CreatePaused(t.Context(), scope, uuid.New()); e != nil {
		t.Fatal(e)
	}
	b := wc.Binding{Scope: scope, Revision: 1, Epoch: 1, Mode: wc.PauseNew, PodID: "pod", BootID: uuid.New()}
	r, a := newRestrictionAt(scope, "pause", b)
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	winners := 0
	for n := 0; n < 8; n++ {
		wg.Go(func() {
			<-start
			created, e := store.BeginRestriction(t.Context(), r, a)
			if e == nil && created {
				mu.Lock()
				winners++
				mu.Unlock()
			}
		})
	}
	close(start)
	wg.Wait()
	if winners != 1 {
		t.Fatal("INTENT creators", winners)
	}
	if _, e := store.ApplyRestriction(t.Context(), r); e != nil {
		t.Fatal(e)
	}
	var count int
	if pool.QueryRow(t.Context(), `SELECT count(*) FROM worker_control_restrictions`).Scan(&count) != nil || count != 1 {
		t.Fatal("duplicate receipt", count)
	}
	if _, e := pool.Exec(t.Context(), `UPDATE worker_control_restrictions SET applied_at=NULL WHERE command_id=$1`, r.CommandID); e == nil {
		t.Fatal("APPLIED without timestamp accepted")
	}
	conflict := r
	conflict.PlanSHA256 = strings.Repeat("c", 64)
	if _, e := store.BeginRestriction(t.Context(), conflict, a); !errors.Is(e, wc.ErrConflict) {
		t.Fatal("same CommandID different plan", e)
	}
}
