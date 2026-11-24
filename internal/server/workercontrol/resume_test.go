package workercontrol_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
	"github.com/ktcloud4-SL/labbit-app/internal/testsupport/workercontrolfake"
)

type resumeAuth func(context.Context, wc.ResumeRequest) (wc.ResumeApproval, error)

func (f resumeAuth) AuthorizeResume(c context.Context, r wc.ResumeRequest) (wc.ResumeApproval, error) {
	return f(c, r)
}

type resumeCheck func(context.Context, wc.ResumeRequest) (wc.ResumeChecks, error)

func (f resumeCheck) RecheckResume(c context.Context, r wc.ResumeRequest) (wc.ResumeChecks, error) {
	return f(c, r)
}

func resumeFixture(t *testing.T, mode wc.Mode) (wc.Binding, *workercontrolfake.Store, wc.ResumeRequest, wc.ResumeProof, time.Time) {
	t.Helper()
	b, s := fixture(wc.PauseNew)
	now := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return now })
	r := wc.ResumeRequest{CommandID: uuid.New(), Scope: b.Scope, ExpectedRevision: b.Revision, ExpectedEpoch: b.Epoch, Desired: mode, TrialID: "trial-01", RunID: "run-01", Phase: "failover-worker", Principal: "operator-fixture", ApprovalRef: "approval-fixture", PlanSHA256: strings.Repeat("a", 64), PreconditionsSHA256: strings.Repeat("b", 64), RequestedAt: now.Add(-time.Second), ValidUntil: now.Add(time.Minute)}
	p := wc.ResumeProof{Approval: wc.ResumeApproval{RequestSHA256: r.SHA256(), ID: uuid.New(), Principal: r.Principal, NotBefore: r.RequestedAt, ExpiresAt: r.ValidUntil}, Checks: wc.ResumeChecks{RequestSHA256: r.SHA256(), EvidenceSHA256: r.PreconditionsSHA256, ObservedAt: now, ValidUntil: r.ValidUntil, AdmissionBlocked: "PASS", SingleWriter: "PASS", Fencing: "PASS", WorkInventory: "PASS", PodCoverage: "PASS", WorkReconciled: "PASS"}}
	return b, s, r, p, now
}
func resumer(t *testing.T, s wc.ResumeStore, p wc.ResumeProof, now time.Time) *wc.Resumer {
	t.Helper()
	c, e := wc.NewResumer(s, resumeAuth(func(context.Context, wc.ResumeRequest) (wc.ResumeApproval, error) { return p.Approval, nil }), resumeCheck(func(context.Context, wc.ResumeRequest) (wc.ResumeChecks, error) { return p.Checks, nil }), func() time.Time { return now })
	if e != nil {
		t.Fatal(e)
	}
	return c
}

func TestResumeApprovedTransitionRequiresNewBindingAcrossWorkers(t *testing.T) {
	for _, mode := range []wc.Mode{wc.Active, wc.ReconcileOnly} {
		t.Run(map[wc.Mode]string{wc.Active: "active", wc.ReconcileOnly: "reconcile"}[mode], func(t *testing.T) {
			b, s, r, p, now := resumeFixture(t, mode)
			old := control(t, b, s)
			record, err := resumer(t, s, p, now).Resume(t.Context(), r)
			if err != nil || record.Status != wc.ResumeApplied {
				t.Fatalf("resume=%+v/%v", record, err)
			}
			if _, err = old.AcquireMutation(t.Context()); err == nil {
				t.Fatal("old paused instance reopened")
			}
			var wg sync.WaitGroup
			for range 16 {
				wg.Go(func() {
					next := b
					next.Revision++
					next.Epoch++
					next.Mode = mode
					next.BootID = uuid.New()
					next.PodID = "new-pod"
					c := control(t, next, s)
					release, err := c.AcquireMutationFor(t.Context(), wc.MutationClaim)
					if mode == wc.Active {
						if err != nil {
							t.Error(err)
						} else {
							release()
						}
					} else if !errors.Is(err, wc.ErrPaused) {
						t.Errorf("reconcile admitted mutation: %v", err)
					}
					release, err = c.AcquireReconciliation(t.Context())
					if err != nil {
						t.Error(err)
					} else {
						release()
					}
				})
			}
			wg.Wait()
			report, _ := s.Report(t.Context(), b.Scope)
			if report.State != r.NextState() || report.OpenBoundaries != 0 {
				t.Fatal("transition/drain mismatch")
			}
			// Same command may return an old receipt after another pause. It never
			// activates again or grants permission based on the historical receipt.
			next := b
			next.Revision++
			next.Epoch++
			next.Mode = mode
			next.BootID = uuid.New()
			if err = s.Pause(t.Context(), next, uuid.New()); err != nil {
				t.Fatal(err)
			}
			if _, err = resumer(t, s, p, now.Add(time.Hour)).Resume(t.Context(), r); err != nil {
				t.Fatal(err)
			}
			report, _ = s.Report(t.Context(), b.Scope)
			if report.State.Mode != wc.PauseNew || report.State.Revision != r.ExpectedRevision+2 {
				t.Fatal("historical replay reactivated")
			}
		})
	}
}

func TestResumeRejectsApprovalAndRecheckFailuresBeforeIntent(t *testing.T) {
	for _, kind := range []string{"missing-id", "wrong-request", "wrong-principal", "expired", "not-yet", "future-request", "wrong-evidence", "old-observation", "future-observation", "expired-check", "admission", "writer", "fencing", "inventory", "coverage", "reconciled", "authorizer-error", "recheck-error"} {
		t.Run(kind, func(t *testing.T) {
			b, s, r, p, now := resumeFixture(t, wc.Active)
			switch kind {
			case "missing-id":
				p.Approval.ID = uuid.Nil
			case "wrong-request":
				p.Approval.RequestSHA256 = strings.Repeat("c", 64)
			case "wrong-principal":
				p.Approval.Principal = "other"
			case "expired":
				p.Approval.ExpiresAt = now
			case "not-yet":
				p.Approval.NotBefore = now.Add(time.Second)
			case "future-request":
				r.RequestedAt = now.Add(time.Second)
				p.Approval.RequestSHA256 = r.SHA256()
				p.Checks.RequestSHA256 = r.SHA256()
			case "wrong-evidence":
				p.Checks.EvidenceSHA256 = strings.Repeat("c", 64)
			case "old-observation":
				p.Checks.ObservedAt = r.RequestedAt.Add(-time.Microsecond)
			case "future-observation":
				p.Checks.ObservedAt = now.Add(time.Microsecond)
			case "expired-check":
				p.Checks.ValidUntil = now
			case "admission":
				p.Checks.AdmissionBlocked = "FAIL"
			case "writer":
				p.Checks.SingleWriter = "NOT_VERIFIED"
			case "fencing":
				p.Checks.Fencing = ""
			case "inventory":
				p.Checks.WorkInventory = "UNKNOWN"
			case "coverage":
				p.Checks.PodCoverage = "PARTIAL"
			case "reconciled":
				p.Checks.WorkReconciled = "NOT_VERIFIED"
			}
			a := resumeAuth(func(context.Context, wc.ResumeRequest) (wc.ResumeApproval, error) {
				if kind == "authorizer-error" {
					return wc.ResumeApproval{}, errors.New("private-auth")
				}
				return p.Approval, nil
			})
			check := resumeCheck(func(context.Context, wc.ResumeRequest) (wc.ResumeChecks, error) {
				if kind == "recheck-error" {
					return wc.ResumeChecks{}, errors.New("private-cloud")
				}
				return p.Checks, nil
			})
			c, _ := wc.NewResumer(s, a, check, func() time.Time { return now })
			if _, err := c.Resume(t.Context(), r); err == nil || strings.Contains(err.Error(), "private") {
				t.Fatalf("unchecked resume/error leaked: %v", err)
			}
			if _, found, _ := s.LookupResume(t.Context(), b.Scope, r.CommandID); found {
				t.Fatal("rejected request persisted INTENT")
			}
			report, _ := s.Report(t.Context(), b.Scope)
			if report.State.Mode != wc.PauseNew || report.State.Revision != b.Revision {
				t.Fatal("rejected request changed state")
			}
		})
	}
}

func TestResumeUnknownNeverAutoReappliesAcrossRestart(t *testing.T) {
	for _, kind := range []string{"lookup", "begin-before", "begin-after", "apply-before", "apply-after"} {
		t.Run(kind, func(t *testing.T) {
			b, s, r, p, now := resumeFixture(t, wc.Active)
			f := workercontrolfake.Faults{}
			switch kind {
			case "lookup":
				f.ResumeLookup = true
			case "begin-before":
				f.ResumeBegin = true
			case "begin-after":
				f.ResumeBeginCommitted = true
			case "apply-before":
				f.ResumeApply = true
			case "apply-after":
				f.ResumeApplyCommitted = true
			}
			s.SetFaults(f)
			if _, err := resumer(t, s, p, now).Resume(t.Context(), r); err == nil {
				t.Fatal("failed call reported success")
			}
			s.SetFaults(workercontrolfake.Faults{})
			restart := resumer(t, s, p, now)
			if kind == "begin-after" || kind == "apply-before" {
				for range 2 {
					if _, err := restart.Resume(t.Context(), r); !errors.Is(err, wc.ErrUnknown) {
						t.Fatalf("pending retried: %v", err)
					}
				}
				report, _ := s.Report(t.Context(), b.Scope)
				if report.State.Mode != wc.PauseNew {
					t.Fatal("unknown intent auto-applied")
				}
				other := r
				other.CommandID = uuid.New()
				pp := p
				pp.Approval.ID = uuid.New()
				pp.Approval.RequestSHA256 = other.SHA256()
				pp.Checks.RequestSHA256 = other.SHA256()
				if _, err := resumer(t, s, pp, now).Resume(t.Context(), other); err == nil {
					t.Fatal("new command bypassed pending intent")
				}
			} else if kind == "apply-after" {
				got, err := restart.Resume(t.Context(), r)
				if err != nil || got.Status != wc.ResumeApplied {
					t.Fatalf("readback failed: %v", err)
				}
				report, _ := s.Report(t.Context(), b.Scope)
				if report.State.Revision != b.Revision+1 {
					t.Fatal("applied twice")
				}
			}
		})
	}
}

func TestResumeConcurrentCommandsOnlyOneTransitionAndExactReplay(t *testing.T) {
	b, s, r, p, now := resumeFixture(t, wc.Active)
	var wg sync.WaitGroup
	var successes atomic.Int32
	for range 32 {
		wg.Go(func() {
			if _, err := resumer(t, s, p, now).Resume(t.Context(), r); err == nil {
				successes.Add(1)
			}
		})
	}
	wg.Wait()
	if successes.Load() == 0 {
		t.Fatal("no owner completed")
	}
	report, _ := s.Report(t.Context(), b.Scope)
	if report.State != r.NextState() {
		t.Fatal("concurrent transition repeated")
	}
	for _, kind := range []string{"run", "phase", "plan", "target", "mode", "principal"} {
		t.Run(kind, func(t *testing.T) {
			changed := r
			switch kind {
			case "run":
				changed.RunID = "other"
			case "phase":
				changed.Phase = "failback"
			case "plan":
				changed.PlanSHA256 = strings.Repeat("c", 64)
			case "target":
				changed.Scope.Cluster = "other"
			case "mode":
				changed.Desired = wc.ReconcileOnly
			case "principal":
				changed.Principal = "other"
			}
			if _, err := resumer(t, s, p, now).Resume(t.Context(), changed); err == nil {
				t.Fatal("changed payload replay accepted")
			}
		})
	}
}

func TestResumeOpenOldIntentAndExpiredStagedProofStayHold(t *testing.T) {
	t.Run("unresolved-boundary", func(t *testing.T) {
		b, s := fixture(wc.Active)
		c := control(t, b, s)
		release, err := c.AcquireMutation(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		peer := b
		peer.PodID = "pause-peer"
		peer.BootID = uuid.New()
		if err = control(t, peer, s).Pause(t.Context()); !errors.Is(err, wc.ErrHold) {
			t.Fatal(err)
		}
		_, _, r, p, now := resumeFixture(t, wc.Active)
		r.Scope = b.Scope
		r.ExpectedRevision = b.Revision + 1
		r.ExpectedEpoch = b.Epoch
		p.Approval.RequestSHA256 = r.SHA256()
		p.Checks.RequestSHA256 = r.SHA256()
		s.SetClock(func() time.Time { return now })
		if _, err = resumer(t, s, p, now).Resume(t.Context(), r); err == nil {
			t.Fatal("open old ticket ignored")
		}
	})
	t.Run("expires-after-intent", func(t *testing.T) {
		b, s, r, p, now := resumeFixture(t, wc.Active)
		if ok, err := s.BeginResume(t.Context(), r, p); err != nil || !ok {
			t.Fatal(err)
		}
		s.SetClock(func() time.Time { return now.Add(time.Hour) })
		if _, err := s.ApplyResume(t.Context(), r); err == nil {
			t.Fatal("expired staged proof activated")
		}
		if _, err := resumer(t, s, p, now).Resume(t.Context(), r); !errors.Is(err, wc.ErrUnknown) {
			t.Fatal("expired INTENT erased")
		}
		report, _ := s.Report(t.Context(), b.Scope)
		if report.State.Mode != wc.PauseNew {
			t.Fatal("expired intent activated")
		}
	})
}

func TestResumeMissingPortsInvalidInputsAndCanceledRequest(t *testing.T) {
	_, s, r, p, now := resumeFixture(t, wc.Active)
	if _, err := wc.NewResumer(s, nil, nil, time.Now); err == nil {
		t.Fatal("missing verifier accepted")
	}
	for _, kind := range []string{"id", "revision", "epoch", "mode", "plan", "label", "window", "precision"} {
		t.Run(kind, func(t *testing.T) {
			bad := r
			switch kind {
			case "id":
				bad.CommandID = uuid.Nil
			case "revision":
				bad.ExpectedRevision = 0
			case "epoch":
				bad.ExpectedEpoch = 0
			case "mode":
				bad.Desired = wc.PauseNew
			case "plan":
				bad.PlanSHA256 = ""
			case "label":
				bad.RunID = "bad\nrun"
			case "window":
				bad.ValidUntil = bad.RequestedAt
			case "precision":
				bad.RequestedAt = bad.RequestedAt.Add(time.Nanosecond)
			}
			if _, err := resumer(t, s, p, now).Resume(t.Context(), bad); !errors.Is(err, wc.ErrInvalidBinding) {
				t.Fatal("invalid command accepted")
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := resumer(t, s, p, now).Resume(ctx, r); err == nil {
		t.Fatal("canceled command activated")
	}
}

// Deliberately inconsistent store responses must never look like success.
type missingResumeStore struct {
	wc.ResumeStore
	applied atomic.Int32
}

func (s *missingResumeStore) LookupResume(context.Context, wc.Scope, uuid.UUID) (wc.ResumeRecord, bool, error) {
	return wc.ResumeRecord{}, false, nil
}
func (s *missingResumeStore) BeginResume(context.Context, wc.ResumeRequest, wc.ResumeProof) (bool, error) {
	return false, nil
}
func (s *missingResumeStore) ApplyResume(context.Context, wc.ResumeRequest) (wc.ResumeRecord, error) {
	s.applied.Add(1)
	return wc.ResumeRecord{}, nil
}
func TestResumeMissingDuplicateReceiptNeverReportsSuccess(t *testing.T) {
	_, _, r, p, now := resumeFixture(t, wc.Active)
	s := &missingResumeStore{}
	if _, err := resumer(t, s, p, now).Resume(t.Context(), r); !errors.Is(err, wc.ErrUnknown) {
		t.Fatalf("missing receipt accepted: %v", err)
	}
	if s.applied.Load() != 0 {
		t.Fatal("duplicate attempted mutation")
	}
}

func TestResumePendingIntentKeepsAuthoritativePauseHold(t *testing.T) {
	b, s, r, p, _ := resumeFixture(t, wc.Active)
	if created, err := s.BeginResume(t.Context(), r, p); err != nil || !created {
		t.Fatal(err)
	}
	report, err := s.Report(t.Context(), b.Scope)
	if err != nil || report.OpenBoundaries != 0 || report.PendingResumes != 1 || report.Drained() {
		t.Fatalf("pending ignored: %+v/%v", report, err)
	}
	if err = control(t, b, s).Pause(t.Context()); !errors.Is(err, wc.ErrHold) {
		t.Fatalf("pause ignored pending: %v", err)
	}
}

func TestResumeApprovalConsumedOnceAndFailbackNeedsFreshAuthorization(t *testing.T) {
	b, s, r, p, now := resumeFixture(t, wc.Active)
	if _, err := resumer(t, s, p, now).Resume(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	next := b
	next.Revision++
	next.Epoch++
	next.Mode = wc.Active
	if err := s.Pause(t.Context(), next, uuid.New()); err != nil {
		t.Fatal(err)
	}
	back := r
	back.CommandID = uuid.New()
	back.RunID = "run-failback"
	back.Phase = "failback-worker"
	back.ExpectedRevision += 2
	back.ExpectedEpoch++
	pp := p
	pp.Approval.RequestSHA256 = back.SHA256()
	pp.Checks.RequestSHA256 = back.SHA256()
	if _, err := resumer(t, s, pp, now).Resume(t.Context(), back); err == nil {
		t.Fatal("same approval consumed twice")
	}
	pp.Approval.ID = uuid.New()
	if _, err := resumer(t, s, pp, now).Resume(t.Context(), back); err != nil {
		t.Fatal(err)
	}
	report, _ := s.Report(t.Context(), b.Scope)
	if report.State != back.NextState() {
		t.Fatal("fresh failback did not advance")
	}
	old := b
	old.Mode = wc.Active
	old.Revision = report.State.Revision
	old.Epoch = report.State.Epoch - 1
	if _, err := control(t, old, s).AcquireMutation(t.Context()); !errors.Is(err, wc.ErrStale) {
		t.Fatal("old epoch entered after failback")
	}
}

func TestResumeRejectsUnencodableTimesAndCanonicalHashCollision(t *testing.T) {
	_, s, r, p, now := resumeFixture(t, wc.Active)
	for _, year := range []int{-1, 0, 10000} {
		t.Run(time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC).String(), func(t *testing.T) {
			bad := r
			bad.RequestedAt = time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
			if bad.Validate() == nil || bad.SHA256() != "" {
				t.Fatal("unencodable request received digest")
			}
			if _, err := resumer(t, s, p, now).Resume(t.Context(), bad); !errors.Is(err, wc.ErrInvalidBinding) {
				t.Fatal("invalid time was persisted")
			}
			proof := p
			proof.Approval.NotBefore = bad.RequestedAt
			if proof.Validate(r, now) == nil {
				t.Fatal("unencodable approval accepted")
			}
			proof = p
			proof.Checks.ObservedAt = bad.RequestedAt
			if proof.Validate(r, now) == nil {
				t.Fatal("unencodable observation accepted")
			}
		})
	}
	other := r
	other.RunID = "different-run"
	if r.SHA256() == other.SHA256() || r.SHA256() == "" {
		t.Fatal("distinct valid commands not bound")
	}
}
