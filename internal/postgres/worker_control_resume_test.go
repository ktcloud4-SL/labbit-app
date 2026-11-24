package postgres

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

func wcResume() (wc.ResumeRequest, wc.ResumeProof, time.Time, wc.Binding) {
	b := wcBinding()
	b.Mode = wc.PauseNew
	now := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	r := wc.ResumeRequest{CommandID: uuid.New(), Scope: b.Scope, ExpectedRevision: b.Revision, ExpectedEpoch: b.Epoch, Desired: wc.Active, TrialID: "trial", RunID: "run", Phase: "failover", Principal: "trusted-fixture", ApprovalRef: "approval", PlanSHA256: strings.Repeat("a", 64), PreconditionsSHA256: strings.Repeat("b", 64), RequestedAt: now.Add(-time.Second), ValidUntil: now.Add(time.Minute)}
	p := wc.ResumeProof{Approval: wc.ResumeApproval{RequestSHA256: r.SHA256(), ID: uuid.New(), Principal: r.Principal, NotBefore: r.RequestedAt, ExpiresAt: r.ValidUntil}, Checks: wc.ResumeChecks{RequestSHA256: r.SHA256(), EvidenceSHA256: r.PreconditionsSHA256, ObservedAt: now, ValidUntil: r.ValidUntil, AdmissionBlocked: "PASS", SingleWriter: "PASS", Fencing: "PASS", WorkInventory: "PASS", PodCoverage: "PASS", WorkReconciled: "PASS"}}
	return r, p, now, b
}
func wcResumeRow(r wc.ResumeRequest, p wc.ResumeProof, now time.Time, status wc.ResumeStatus) wcRow {
	raw, _ := json.Marshal(r)
	proof, _ := json.Marshal(p)
	var at *time.Time
	if status == wc.ResumeApplied {
		a := now
		at = &a
	}
	return wcRow{values: []any{r.SHA256(), raw, proof, string(status), now, at}}
}

func TestResumePostgresBeginRecordsIntentSeparatelyAndNeverActivates(t *testing.T) {
	r, p, now, b := wcResume()
	for _, kind := range []string{"success", "existing-intent", "existing-applied", "conflict", "open", "stale", "epoch", "mode", "expired", "unavailable", "commit-lost"} {
		t.Run(kind, func(t *testing.T) {
			state := b
			prior := wcRow{err: pgx.ErrNoRows}
			open := int64(0)
			clock := now
			switch kind {
			case "existing-intent":
				prior = wcResumeRow(r, p, now, wc.ResumeIntent)
			case "existing-applied":
				prior = wcResumeRow(r, p, now, wc.ResumeApplied)
			case "conflict":
				other := r
				other.RunID = "other"
				pp := p
				pp.Approval.RequestSHA256 = other.SHA256()
				pp.Checks.RequestSHA256 = other.SHA256()
				prior = wcResumeRow(other, pp, now, wc.ResumeIntent)
			case "open":
				open = 1
			case "stale":
				state.Revision++
			case "epoch":
				state.Epoch++
			case "mode":
				state.Mode = wc.Active
			case "expired":
				clock = r.ValidUntil
			}
			tx := &wcTx{rows: []wcRow{wcState(state), prior, {values: []any{open}}, {values: []any{clock}}}}
			if kind == "unavailable" {
				tx.execErr = errors.New("private-resume-payload")
			}
			if kind == "commit-lost" {
				tx.commitErr = errors.New("private-commit")
			}
			created, err := wcStore(tx).BeginResume(t.Context(), r, p)
			if kind == "success" {
				if err != nil || !created || tx.commits != 1 || tx.execs != 1 {
					t.Fatalf("intent=%v/%v", created, err)
				}
				if !strings.Contains(tx.calls[0].sql, "FOR UPDATE") || !strings.Contains(tx.calls[len(tx.calls)-1].sql, "'INTENT'") {
					t.Fatal("intent not durable")
				}
			} else if strings.HasPrefix(kind, "existing-") {
				if err != nil || created || tx.execs != 0 {
					t.Fatal("existing command mutated")
				}
			} else if err == nil || created || strings.Contains(err.Error(), "private") {
				t.Fatalf("failed intent accepted/leaked: %v", err)
			}
			for _, call := range tx.calls {
				if strings.Contains(call.sql, "UPDATE worker_control_states") {
					t.Fatal("Begin activated before separate INTENT commit")
				}
			}
		})
	}
}

func TestResumePostgresApplyRechecksAndCommitsStateReceiptAtomically(t *testing.T) {
	r, p, now, b := wcResume()
	for _, kind := range []string{"success", "already-applied", "changed-command", "stale", "open", "expired", "backward-clock", "write-failed", "commit-lost", "missing"} {
		t.Run(kind, func(t *testing.T) {
			state := b
			row := wcResumeRow(r, p, now, wc.ResumeIntent)
			open := int64(0)
			clock := now.Add(time.Second)
			switch kind {
			case "already-applied":
				row = wcResumeRow(r, p, now, wc.ResumeApplied)
			case "changed-command":
				other := r
				other.Phase = "failback"
				pp := p
				pp.Approval.RequestSHA256 = other.SHA256()
				pp.Checks.RequestSHA256 = other.SHA256()
				row = wcResumeRow(other, pp, now, wc.ResumeIntent)
			case "stale":
				state.Epoch++
			case "open":
				open = 1
			case "expired":
				clock = r.ValidUntil
			case "backward-clock":
				clock = now.Add(-time.Microsecond)
			case "missing":
				row = wcRow{err: pgx.ErrNoRows}
			}
			tx := &wcTx{rows: []wcRow{wcState(state), row, {values: []any{open}}, {values: []any{clock}}}}
			if kind == "write-failed" {
				tx.execErr = errors.New("private-update")
			}
			if kind == "commit-lost" {
				tx.commitErr = errors.New("private-commit")
			}
			record, err := wcStore(tx).ApplyResume(t.Context(), r)
			if kind == "success" {
				if err != nil || record.Status != wc.ResumeApplied || !record.AppliedAt.Equal(clock) || tx.execs != 2 || tx.commits != 1 {
					t.Fatalf("apply=%+v/%v", record, err)
				}
				if !strings.Contains(tx.calls[0].sql, "FOR UPDATE") || !strings.Contains(tx.calls[1].sql, "FOR UPDATE") || !strings.Contains(tx.calls[4].sql, "epoch=epoch+1") {
					t.Fatal("serialization/epoch lost")
				}
			} else if err == nil || record.Status != "" || strings.Contains(err.Error(), "private") {
				t.Fatal("failed apply returned activation")
			}
			if kind != "success" && tx.rollbacks != 1 {
				t.Fatal("failed apply transaction not rolled back")
			}
		})
	}
}

func TestResumePostgresLookupRejectsCorruptOrForeignRecord(t *testing.T) {
	r, p, now, _ := wcResume()
	for _, kind := range []string{"intent", "applied", "missing", "hash", "request", "proof", "phase", "status", "target", "command", "time"} {
		t.Run(kind, func(t *testing.T) {
			row := wcResumeRow(r, p, now, wc.ResumeIntent)
			id := r.CommandID
			scope := r.Scope
			switch kind {
			case "applied":
				row = wcResumeRow(r, p, now, wc.ResumeApplied)
			case "missing":
				row = wcRow{err: pgx.ErrNoRows}
			case "hash":
				row.values[0] = strings.Repeat("c", 64)
			case "request":
				row.values[1] = []byte(`{`)
			case "proof":
				row.values[2] = []byte(`{`)
			case "phase":
				changed := p
				changed.Approval.RequestSHA256 = strings.Repeat("c", 64)
				row = wcResumeRow(r, changed, now, wc.ResumeIntent)
			case "status":
				row.values[3] = "UNKNOWN"
			case "target":
				scope.Cluster = "foreign"
			case "command":
				id = uuid.New()
			case "time":
				row.values[5] = &now
			}
			got, found, err := wcStore(&wcTx{rows: []wcRow{row}}).LookupResume(t.Context(), scope, id)
			if kind == "intent" || kind == "applied" {
				if err != nil || !found || got.Request != r {
					t.Fatal("valid readback rejected")
				}
			} else if kind == "missing" {
				if err != nil || found {
					t.Fatal("missing became receipt")
				}
			} else if err == nil {
				t.Fatal("corrupt/foreign readback accepted")
			}
		})
	}
}

func TestResumePostgresMissingStoreAndInvalidRequestNeverStartsTransaction(t *testing.T) {
	r, p, _, _ := wcResume()
	var s *WorkerControlStore
	if _, e := s.BeginResume(t.Context(), r, p); !errors.Is(e, wc.ErrUnavailable) {
		t.Fatal(e)
	}
	if _, e := s.ApplyResume(t.Context(), r); !errors.Is(e, wc.ErrUnavailable) {
		t.Fatal(e)
	}
	if _, _, e := s.LookupResume(t.Context(), r.Scope, r.CommandID); !errors.Is(e, wc.ErrUnavailable) {
		t.Fatal(e)
	}
	tx := &wcTx{}
	r.ExpectedEpoch = 0
	if _, e := wcStore(tx).BeginResume(t.Context(), r, p); !errors.Is(e, wc.ErrInvalidBinding) {
		t.Fatal(e)
	}
	if len(tx.calls) != 0 {
		t.Fatal("invalid command touched store")
	}
}
