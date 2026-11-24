package workercontrol_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/server/maintenance"
	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

func modeFile(t *testing.T, mode string) (string, *maintenance.Gate) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mode")
	setMode(t, path, mode)
	return path, maintenance.New(path)
}
func setMode(t *testing.T, path, mode string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(mode), 0600); err != nil {
		t.Fatal(err)
	}
}
func maintenanceResumer(t *testing.T, store wc.ResumeStore, p wc.ResumeProof, gate *maintenance.Gate, now time.Time) *wc.MaintenanceResumer {
	t.Helper()
	s, err := wc.NewMaintenanceResumer(store, resumeAuth(func(context.Context, wc.ResumeRequest) (wc.ResumeApproval, error) { return p.Approval, nil }), resumeCheck(func(context.Context, wc.ResumeRequest) (wc.ResumeChecks, error) { return p.Checks, nil }), gate, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMaintenanceResumeRequiresVerifiedClosedLocalAndExternalChecks(t *testing.T) {
	for _, kind := range []string{"missing", "invalid", "open", "http-active", "worker-active", "query-active", "external-admission-fail", "external-coverage-unverified", "external-writer-fail"} {
		t.Run(kind, func(t *testing.T) {
			b, s, r, p, now := resumeFixture(t, wc.Active)
			path, g := modeFile(t, "closed")
			switch kind {
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "invalid":
				setMode(t, path, "unknown")
			case "open":
				setMode(t, path, "open")
			case "http-active", "worker-active":
				setMode(t, path, "open")
				k := "http"
				if kind == "worker-active" {
					k = "worker"
				}
				release, ok := g.Enter(k)
				if !ok {
					t.Fatal("fixture")
				}
				defer release()
				setMode(t, path, "closed")
			case "query-active":
				defer g.ObserveReconciliation()()
			case "external-admission-fail":
				p.Checks.AdmissionBlocked = "FAIL"
			case "external-coverage-unverified":
				p.Checks.PodCoverage = "NOT_VERIFIED"
			case "external-writer-fail":
				p.Checks.SingleWriter = "FAIL"
			}
			if _, err := maintenanceResumer(t, s, p, g, now).Resume(t.Context(), r); err == nil {
				t.Fatal("unsafe resume")
			}
			report, _ := s.Report(t.Context(), b.Scope)
			if report.PendingResumes != 0 || report.State.Mode != wc.PauseNew {
				t.Fatal("changed before verified")
			}
		})
	}
	_, s, _, p, now := resumeFixture(t, wc.Active)
	if _, err := wc.NewMaintenanceResumer(s, nil, nil, maintenance.New(""), func() time.Time { return now }); err == nil {
		t.Fatal("unconfigured barrier")
	}
	_, g := modeFile(t, "closed")
	if _, err := wc.NewMaintenanceResumer(s, nil, nil, g, func() time.Time { return now }); err == nil {
		t.Fatal("missing trusted ports")
	}
	_ = p
}

type afterResumeIntent struct {
	wc.ResumeStore
	after func()
}

func (s afterResumeIntent) BeginResume(ctx context.Context, r wc.ResumeRequest, p wc.ResumeProof) (bool, error) {
	ok, err := s.ResumeStore.BeginResume(ctx, r, p)
	if ok && err == nil {
		s.after()
	}
	return ok, err
}

func TestMaintenanceResumeRechecksBarrierBeforeIntentAndApply(t *testing.T) {
	for _, when := range []string{"recheck-opens", "recheck-query-starts", "after-intent-opens", "after-intent-query-starts"} {
		t.Run(when, func(t *testing.T) {
			b, s, r, p, now := resumeFixture(t, wc.Active)
			path, g := modeFile(t, "closed")
			var release func()
			defer func() {
				if release != nil {
					release()
				}
			}()
			hook := func() {
				if when == "recheck-opens" || when == "after-intent-opens" {
					setMode(t, path, "open")
				} else {
					release = g.ObserveReconciliation()
				}
			}
			var store wc.ResumeStore = s
			if when == "after-intent-opens" || when == "after-intent-query-starts" {
				store = afterResumeIntent{s, hook}
			}
			a := resumeAuth(func(context.Context, wc.ResumeRequest) (wc.ResumeApproval, error) { return p.Approval, nil })
			c := resumeCheck(func(context.Context, wc.ResumeRequest) (wc.ResumeChecks, error) {
				if when == "recheck-opens" || when == "recheck-query-starts" {
					hook()
				}
				return p.Checks, nil
			})
			bridge, err := wc.NewMaintenanceResumer(store, a, c, g, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			if _, err = bridge.Resume(t.Context(), r); !errors.Is(err, wc.ErrUnknown) {
				t.Fatalf("expected conservative UNKNOWN: %v", err)
			}
			report, _ := s.Report(t.Context(), b.Scope)
			want := int64(0)
			if when == "after-intent-opens" || when == "after-intent-query-starts" {
				want = 1
			}
			if report.PendingResumes != want || report.State.Mode != wc.PauseNew {
				t.Fatalf("report=%+v", report)
			}
		})
	}
}

func TestMaintenanceResumeReceiptNeverOpensFileOrOldWorker(t *testing.T) {
	b, s, r, p, now := resumeFixture(t, wc.Active)
	path, g := modeFile(t, "closed")
	bridge := maintenanceResumer(t, s, p, g, now)
	if rec, err := bridge.Resume(t.Context(), r); err != nil || rec.Status != wc.ResumeApplied {
		t.Fatal(rec, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "closed" {
		t.Fatal("application opened platform file")
	}
	if _, ok := g.Enter("http"); ok {
		t.Fatal("receipt opened admission")
	}
	if _, err := control(t, b, s).AcquireMutation(t.Context()); err == nil {
		t.Fatal("old worker reopened")
	}
	setMode(t, path, "open") // Simulated external approved operator, not bridge.
	if rec, err := bridge.Resume(t.Context(), r); err != nil || rec.Status != wc.ResumeApplied {
		t.Fatal("historical readback", err)
	}
	if _, err := control(t, b, s).AcquireMutation(t.Context()); err == nil {
		t.Fatal("file OPEN overrode old binding")
	}
	newBinding := b
	newBinding.Revision++
	newBinding.Epoch++
	newBinding.Mode = wc.Active
	release, err := control(t, newBinding, s).AcquireMutation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	release()
}
