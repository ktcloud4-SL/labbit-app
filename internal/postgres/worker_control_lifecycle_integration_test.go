//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

func resumeFixture(scope wc.Scope) (wc.ResumeRequest, wc.ResumeProof) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	r := wc.ResumeRequest{CommandID: uuid.New(), Scope: scope, ExpectedRevision: 1, ExpectedEpoch: 1, Desired: wc.Active, TrialID: "trial", RunID: "run", Phase: "failover", Principal: "operator-fixture", ApprovalRef: "approval-fixture", PlanSHA256: strings.Repeat("a", 64), PreconditionsSHA256: strings.Repeat("b", 64), RequestedAt: now.Add(-time.Second), ValidUntil: now.Add(time.Minute)}
	p := wc.ResumeProof{Approval: wc.ResumeApproval{RequestSHA256: r.SHA256(), ID: uuid.New(), Principal: r.Principal, NotBefore: r.RequestedAt, ExpiresAt: r.ValidUntil}, Checks: wc.ResumeChecks{RequestSHA256: r.SHA256(), EvidenceSHA256: r.PreconditionsSHA256, ObservedAt: now, ValidUntil: r.ValidUntil, AdmissionBlocked: "PASS", SingleWriter: "PASS", Fencing: "PASS", WorkInventory: "PASS", PodCoverage: "PASS", WorkReconciled: "PASS"}}
	return r, p
}

// An invalid v13 row must stop additive migration 14 without inventing Evidence.
func TestWorkerControlResumeAppliedTimeConstraintIsAdditive(t *testing.T) {
	dsn := postgrestest.NewDatabase(t)
	conn := postgrestest.Connect(t, dsn)
	all := embeddedMigrations(t)
	postgrestest.Migrate(t, dsn, migrationsUpTo(all, 13))
	if _, err := conn.Exec(t.Context(), `INSERT INTO worker_control_states(control_id,region,cluster,revision,epoch,mode) VALUES ('constraint','region-fixture','cluster-fixture',1,1,'PAUSE_NEW')`); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO worker_control_resumes(command_id,control_id,request_sha256,approval_id,request_payload,proof_payload,status,created_at,applied_at) VALUES ($1,'constraint',$2,$3,$4,$5,$6,$7,$8)`
	now := time.Now().UTC().Truncate(time.Microsecond)
	badID := uuid.New()
	if _, err := conn.Exec(t.Context(), insert, badID, strings.Repeat("a", 64), uuid.New(), []byte(`{}`), []byte(`{}`), "APPLIED", now, nil); err != nil {
		t.Fatalf("v13 defect not reproduced: %v", err)
	}
	if _, err := postgres.Migrate(t.Context(), conn, all, discardLogger); err == nil {
		t.Fatal("invalid old row silently accepted")
	}
	var preserved bool
	if err := conn.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM worker_control_resumes WHERE command_id=$1 AND applied_at IS NULL) AND NOT EXISTS(SELECT 1 FROM labbit_schema_migrations WHERE version=14)`, badID).Scan(&preserved); err != nil || !preserved {
		t.Fatalf("failed migration changed row/history: %v", err)
	}
	// This database is disposable: removing the deliberately corrupt fixture is
	// not a production repair policy.
	if _, err := conn.Exec(t.Context(), `DELETE FROM worker_control_resumes WHERE command_id=$1`, badID); err != nil {
		t.Fatal(err)
	}
	applied, err := postgres.Migrate(t.Context(), conn, all, discardLogger)
	if err != nil || len(applied) != 1 || applied[0].Version != 14 {
		t.Fatalf("additive migration = %v/%v", migrationNames(applied), err)
	}
	for _, tc := range []struct {
		name, status string
		at           any
		valid        bool
	}{
		{"intent-null", "INTENT", nil, true},
		{"applied-null", "APPLIED", nil, false},
		{"applied-before-created", "APPLIED", now.Add(-time.Second), false},
		{"intent-with-time", "INTENT", now, false},
		{"applied-at-created", "APPLIED", now, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := conn.Exec(t.Context(), insert, uuid.New(), strings.Repeat("a", 64), uuid.New(), []byte(`{}`), []byte(`{}`), tc.status, now, tc.at)
			if tc.valid {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var pgErr *pgconn.PgError
				if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
					t.Fatalf("want CHECK violation, got %v", err)
				}
			}
		})
	}
}

// Queue Pause ahead of Enter on a real PostgreSQL row lock. The old binding
// must be rejected after the paused revision commits, across separate sessions.
func TestWorkerControlPostgresPauseSerializesQueuedEnter(t *testing.T) {
	db := newTestDB(t)
	s := postgres.NewWorkerControlStore(db.pool)
	scope := wc.Scope{ID: "queued", Region: "region-fixture", Cluster: "cluster-fixture"}
	if err := s.CreatePaused(t.Context(), scope, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(t.Context(), `UPDATE worker_control_states SET mode='ACTIVE' WHERE control_id=$1`, scope.ID); err != nil {
		t.Fatal(err)
	}
	b := wc.Binding{Scope: scope, Revision: 1, Epoch: 1, Mode: wc.Active, PodID: "old-pod", BootID: uuid.New()}
	tx, err := db.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(t.Context(), `SELECT control_id FROM worker_control_states WHERE control_id=$1 FOR UPDATE`, scope.ID); err != nil {
		t.Fatal(err)
	}
	pauseDone := make(chan error, 1)
	go func() { pauseDone <- s.Pause(t.Context(), b, uuid.New()) }()
	waiters := func(want int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			var n int
			if err := db.pool.QueryRow(t.Context(), `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'SELECT control_id, region, cluster, revision, epoch, mode FROM worker_control_states%'`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n >= want {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("did not observe %d blocked sessions", want)
	}
	waiters(1)
	enterDone := make(chan error, 1)
	go func() {
		enterDone <- s.Enter(t.Context(), wc.Ticket{Binding: b, ID: uuid.New(), Kind: wc.MutationBoundary, Phase: wc.MutationDispatch})
	}()
	waiters(2)
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-pauseDone; err != nil {
		t.Fatal(err)
	}
	if err := <-enterDone; !errors.Is(err, wc.ErrStale) {
		t.Fatalf("queued old Enter = %v", err)
	}
	r, err := s.Report(t.Context(), scope)
	if err != nil || r.State.Mode != wc.PauseNew || r.State.Revision != 2 || r.OpenBoundaries != 0 || !r.Drained() {
		t.Fatalf("report=%+v/%v", r, err)
	}
}

type resumePorts struct {
	proof wc.ResumeProof
	calls atomic.Int32
}

func (p *resumePorts) AuthorizeResume(context.Context, wc.ResumeRequest) (wc.ResumeApproval, error) {
	p.calls.Add(1)
	return p.proof.Approval, nil
}
func (p *resumePorts) RecheckResume(context.Context, wc.ResumeRequest) (wc.ResumeChecks, error) {
	p.calls.Add(1)
	return p.proof.Checks, nil
}

// Drop an application response only after the real Store committed. This is
// an injected caller-boundary failure, not a TCP/DB-crash fault injection.
type lostResumeResponse struct {
	wc.ResumeStore
	begin   bool
	applied atomic.Int32
}

func (s *lostResumeResponse) BeginResume(ctx context.Context, r wc.ResumeRequest, p wc.ResumeProof) (bool, error) {
	created, err := s.ResumeStore.BeginResume(ctx, r, p)
	if err == nil && s.begin {
		return false, wc.ErrUnavailable
	}
	return created, err
}
func (s *lostResumeResponse) ApplyResume(ctx context.Context, r wc.ResumeRequest) (wc.ResumeRecord, error) {
	s.applied.Add(1)
	record, err := s.ResumeStore.ApplyResume(ctx, r)
	if err == nil {
		return wc.ResumeRecord{}, wc.ErrUnavailable
	}
	return record, err
}

func TestWorkerControlPostgresLostResponseAndReconnectNeverAutoApplies(t *testing.T) {
	for _, lostBegin := range []bool{true, false} {
		name := "apply-response"
		if lostBegin {
			name = "begin-response"
		}
		t.Run(name, func(t *testing.T) {
			db := newTestDB(t)
			store := postgres.NewWorkerControlStore(db.pool)
			scope := wc.Scope{ID: "restart", Region: "region-fixture", Cluster: "cluster-fixture"}
			if err := store.CreatePaused(t.Context(), scope, uuid.New()); err != nil {
				t.Fatal(err)
			}
			r, p := resumeFixture(scope)
			ports := &resumePorts{proof: p}
			fault := &lostResumeResponse{ResumeStore: store, begin: lostBegin}
			resumer, err := wc.NewResumer(fault, ports, ports, func() time.Time { return time.Now().UTC() })
			if err != nil {
				t.Fatal(err)
			}
			if _, err = resumer.Resume(t.Context(), r); !errors.Is(err, wc.ErrUnknown) {
				t.Fatalf("lost response=%v", err)
			}
			db.pool.Close()
			pool, err := postgres.OpenPool(t.Context(), db.dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			freshStore := postgres.NewWorkerControlStore(pool)
			freshPorts := &resumePorts{proof: p}
			fresh, err := wc.NewResumer(freshStore, freshPorts, freshPorts, func() time.Time { return time.Now().UTC() })
			if err != nil {
				t.Fatal(err)
			}
			record, err := fresh.Resume(t.Context(), r)
			if lostBegin {
				if !errors.Is(err, wc.ErrUnknown) || record.Status != wc.ResumeIntent || fault.applied.Load() != 0 {
					t.Fatalf("INTENT replayed: %+v/%v", record, err)
				}
			} else if err != nil || record.Status != wc.ResumeApplied || fault.applied.Load() != 1 {
				t.Fatalf("APPLIED lost: %+v/%v", record, err)
			}
			if freshPorts.calls.Load() != 0 {
				t.Fatal("reconnected caller reauthorized/rechecked an existing command")
			}
			report, err := freshStore.Report(t.Context(), scope)
			if err != nil {
				t.Fatal(err)
			}
			if lostBegin {
				if report.State.Mode != wc.PauseNew || report.PendingResumes != 1 || report.Drained() {
					t.Fatalf("INTENT not HOLD: %+v", report)
				}
			} else if report.State != r.NextState() || report.PendingResumes != 0 {
				t.Fatalf("non-atomic receipt/state: %+v", report)
			}
		})
	}
}
