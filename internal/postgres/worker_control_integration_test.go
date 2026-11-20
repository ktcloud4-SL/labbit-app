//go:build integration

package postgres_test

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

// Added but deliberately NOT RUN in this increment: user prohibited actual DB
// access. Run only against the approved disposable integration database later.
func TestWorkerControlPostgresCreatePauseAndConcurrentEnter(t *testing.T) {
	db := newTestDB(t)
	s := postgres.NewWorkerControlStore(db.pool)
	scope := wc.Scope{ID: "trial", Region: "region-fixture", Cluster: "cluster-fixture"}
	id := uuid.New()
	if err := s.CreatePaused(t.Context(), scope, id); err != nil {
		t.Fatal(err)
	}
	if err := s.CreatePaused(t.Context(), scope, id); err != nil {
		t.Fatal(err)
	}
	if err := s.CreatePaused(t.Context(), scope, uuid.New()); !errors.Is(err, wc.ErrStale) {
		t.Fatal("different create request accepted")
	}
	// Fixture seed only; this earlier boundary test bypasses the separate
	// approved Resume path, which has its own integration test below.
	if _, err := db.pool.Exec(t.Context(), `UPDATE worker_control_states SET mode='ACTIVE' WHERE control_id=$1`, scope.ID); err != nil {
		t.Fatal(err)
	}
	b := wc.Binding{Scope: scope, Revision: 1, Epoch: 1, Mode: wc.Active, PodID: "pod-a", BootID: uuid.New()}
	var wg sync.WaitGroup
	tickets := make(chan wc.Ticket, 32)
	for range 32 {
		wg.Go(func() {
			ticket := wc.Ticket{Binding: b, ID: uuid.New(), Kind: wc.MutationBoundary, Phase: wc.MutationDispatch}
			if err := s.Enter(t.Context(), ticket); err != nil {
				t.Error(err)
				return
			}
			tickets <- ticket
		})
	}
	wg.Wait()
	close(tickets)
	if err := s.Pause(t.Context(), b, uuid.New()); err != nil {
		t.Fatal(err)
	}
	r, err := s.Report(t.Context(), scope)
	if err != nil || r.Drained() || r.OpenBoundaries != 32 {
		t.Fatalf("report=%+v/%v", r, err)
	}
	if err = s.Enter(t.Context(), wc.Ticket{Binding: b, ID: uuid.New(), Kind: wc.MutationBoundary, Phase: wc.MutationClaim}); !errors.Is(err, wc.ErrStale) {
		t.Fatal("old revision allowed")
	}
	for ticket := range tickets {
		wrong := ticket
		wrong.Binding.BootID = uuid.New()
		if err = s.Leave(t.Context(), wrong); !errors.Is(err, wc.ErrStale) {
			t.Fatal("foreign close allowed")
		}
		if err = s.Leave(t.Context(), ticket); err != nil {
			t.Fatal(err)
		}
	}
	r, err = s.Report(t.Context(), scope)
	if err != nil || !r.Drained() {
		t.Fatal("old revision intent could not close")
	}
}

// Compile only under the current user request: NOT executed against a DB.
func TestWorkerControlPostgresApprovedResumeJournalAndConcurrentApply(t *testing.T) {
	db := newTestDB(t)
	s := postgres.NewWorkerControlStore(db.pool)
	scope := wc.Scope{ID: "resume-trial", Region: "region-fixture", Cluster: "cluster-fixture"}
	if err := s.CreatePaused(t.Context(), scope, uuid.New()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	r := wc.ResumeRequest{CommandID: uuid.New(), Scope: scope, ExpectedRevision: 1, ExpectedEpoch: 1, Desired: wc.Active, TrialID: "trial", RunID: "run", Phase: "failover", Principal: "operator-fixture", ApprovalRef: "approval-fixture", PlanSHA256: strings.Repeat("a", 64), PreconditionsSHA256: strings.Repeat("b", 64), RequestedAt: now.Add(-time.Second), ValidUntil: now.Add(time.Minute)}
	p := wc.ResumeProof{Approval: wc.ResumeApproval{RequestSHA256: r.SHA256(), ID: uuid.New(), Principal: r.Principal, NotBefore: r.RequestedAt, ExpiresAt: r.ValidUntil}, Checks: wc.ResumeChecks{RequestSHA256: r.SHA256(), EvidenceSHA256: r.PreconditionsSHA256, ObservedAt: now, ValidUntil: r.ValidUntil, AdmissionBlocked: "PASS", SingleWriter: "PASS", Fencing: "PASS", WorkInventory: "PASS", PodCoverage: "PASS", WorkReconciled: "PASS"}}
	if created, err := s.BeginResume(t.Context(), r, p); err != nil || !created {
		t.Fatal(err)
	}
	if created, err := s.BeginResume(t.Context(), r, p); err != nil || created {
		t.Fatal("duplicate INTENT", err)
	}
	report, err := s.Report(t.Context(), scope)
	if err != nil || report.PendingResumes != 1 || report.Drained() {
		t.Fatal("pending intent not HOLD", err)
	}
	var wg sync.WaitGroup
	var applied atomic.Int32
	for range 16 {
		wg.Go(func() {
			_, err := s.ApplyResume(t.Context(), r)
			if err == nil {
				applied.Add(1)
			} else if !errors.Is(err, wc.ErrStale) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if applied.Load() != 1 {
		t.Fatalf("applied %d times", applied.Load())
	}
	receipt, found, err := s.LookupResume(t.Context(), scope, r.CommandID)
	if err != nil || !found || receipt.Status != wc.ResumeApplied {
		t.Fatal("receipt round trip", err)
	}
	report, err = s.Report(t.Context(), scope)
	if err != nil || report.PendingResumes != 0 || report.State != r.NextState() {
		t.Fatal("state/receipt not atomic", err)
	}
	old := wc.Binding{Scope: scope, Revision: 1, Epoch: 1, Mode: wc.Active, PodID: "old", BootID: uuid.New()}
	if err = s.Enter(t.Context(), wc.Ticket{Binding: old, ID: uuid.New(), Kind: wc.MutationBoundary, Phase: wc.MutationClaim}); !errors.Is(err, wc.ErrStale) {
		t.Fatal("old epoch entered", err)
	}
}
