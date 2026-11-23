package postgres

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

// Scripted driver boundary, not a substitute for PostgreSQL transactions/locks.
type wcRow struct {
	values []any
	err    error
}

func (r wcRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return errors.New("fixture column mismatch")
	}
	for i, d := range dest {
		v := reflect.ValueOf(d).Elem()
		if r.values[i] == nil {
			v.SetZero()
		} else {
			v.Set(reflect.ValueOf(r.values[i]))
		}
	}
	return nil
}

type wcCall struct {
	sql  string
	args []any
}
type wcTx struct {
	pgx.Tx
	rows               []wcRow
	calls              []wcCall
	execs              int
	commitErr, execErr error
	commits, rollbacks int
	rollbackCanceled   bool
}

func (tx *wcTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	tx.calls = append(tx.calls, wcCall{sql, args})
	if len(tx.rows) == 0 {
		return wcRow{err: errors.New("unexpected query")}
	}
	r := tx.rows[0]
	tx.rows = tx.rows[1:]
	return r
}
func (tx *wcTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tx.calls = append(tx.calls, wcCall{sql, args})
	tx.execs++
	return pgconn.NewCommandTag("UPDATE 1"), tx.execErr
}
func (tx *wcTx) Commit(context.Context) error { tx.commits++; return tx.commitErr }
func (tx *wcTx) Rollback(ctx context.Context) error {
	tx.rollbacks++
	tx.rollbackCanceled = ctx.Err() != nil
	return nil
}
func wcStore(tx *wcTx) *WorkerControlStore {
	return &WorkerControlStore{db: tx, begin: func(context.Context) (pgx.Tx, error) { return tx, nil }}
}
func wcBinding() wc.Binding {
	return wc.Binding{Scope: wc.Scope{ID: "trial", Region: "region-fixture", Cluster: "cluster-fixture"}, Revision: 5, Epoch: 2, Mode: wc.Active, PodID: "pod-a", BootID: uuid.New()}
}
func wcState(b wc.Binding) wcRow {
	return wcRow{values: []any{b.Scope.ID, b.Scope.Region, b.Scope.Cluster, b.Revision, b.Epoch, workerModeName(b.Mode)}}
}
func wcTicket(b wc.Binding) wc.Ticket {
	return wc.Ticket{Binding: b, ID: uuid.New(), Kind: wc.MutationBoundary, Phase: wc.MutationDispatch}
}

func TestDurableWorkerControlAdapterCommitsEntryBeforeReturningAndRedactsFailure(t *testing.T) {
	b := wcBinding()
	for _, stage := range []string{"success", "insert", "commit", "begin"} {
		t.Run(stage, func(t *testing.T) {
			tx := &wcTx{rows: []wcRow{wcState(b)}}
			s := wcStore(tx)
			private := errors.New("private-driver-payload")
			switch stage {
			case "insert":
				tx.execErr = private
			case "commit":
				tx.commitErr = private
			case "begin":
				s.begin = func(context.Context) (pgx.Tx, error) { return nil, private }
			}
			err := s.Enter(t.Context(), wcTicket(b))
			if stage == "success" {
				if err != nil || tx.commits != 1 || tx.rollbacks != 0 {
					t.Fatalf("entry=%v commits=%d", err, tx.commits)
				}
			} else {
				if !errors.Is(err, wc.ErrUnavailable) || strings.Contains(err.Error(), "private") {
					t.Fatal("driver error leaked or permission inferred")
				}
				if stage != "begin" && tx.rollbacks != 1 {
					t.Fatal("transaction not rolled back")
				}
			}
			if stage != "begin" && (!strings.Contains(tx.calls[0].sql, "FOR UPDATE") || !strings.Contains(tx.calls[1].sql, "INSERT INTO worker_control_boundaries")) {
				t.Fatal("entry did not serialize/record before return")
			}
		})
	}
}

func TestDurableWorkerControlAdapterRejectsWrongTargetEpochRevisionModeAndMissingRow(t *testing.T) {
	b := wcBinding()
	for _, kind := range []string{"scope", "epoch", "revision", "mode", "missing"} {
		t.Run(kind, func(t *testing.T) {
			state := b
			switch kind {
			case "scope":
				state.Scope.Cluster = "other"
			case "epoch":
				state.Epoch++
			case "revision":
				state.Revision++
			case "mode":
				state.Mode = wc.PauseNew
			}
			row := wcState(state)
			if kind == "missing" {
				row = wcRow{err: pgx.ErrNoRows}
			}
			tx := &wcTx{rows: []wcRow{row}}
			if err := wcStore(tx).Enter(t.Context(), wcTicket(b)); err == nil {
				t.Fatal("stale entry allowed")
			}
			if tx.execs != 0 || tx.commits != 0 || tx.rollbacks != 1 {
				t.Fatal("failed verification wrote intent")
			}
		})
	}
}

func TestDurableWorkerControlAdapterLeaveUsesExactEntryBindingAfterPause(t *testing.T) {
	b := wcBinding()
	ticket := wcTicket(b)
	for _, kind := range []string{"open", "closed", "foreign"} {
		t.Run(kind, func(t *testing.T) {
			row := wcRow{values: []any{nil}}
			if kind == "closed" {
				at := time.Now()
				row.values = []any{&at}
			}
			if kind == "foreign" {
				row = wcRow{err: pgx.ErrNoRows}
			}
			tx := &wcTx{rows: []wcRow{row}}
			err := wcStore(tx).Leave(t.Context(), ticket)
			if kind == "foreign" {
				if !errors.Is(err, wc.ErrStale) || tx.execs != 0 {
					t.Fatal("foreign ticket closed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			call := tx.calls[0]
			if strings.Contains(call.sql, "worker_control_states") || !strings.Contains(call.sql, "boot_id=$9 AND kind=$10 AND phase=$11 FOR UPDATE") || call.args[4] != b.Revision || call.args[8] != b.BootID {
				t.Fatal("entry binding lost")
			}
			if kind == "closed" && tx.execs != 0 {
				t.Fatal("duplicate close rewrote timestamp")
			}
		})
	}
}

func TestDurableWorkerControlAdapterPauseCASAndIdempotency(t *testing.T) {
	b := wcBinding()
	id := uuid.New()
	for _, kind := range []string{"new", "same-request", "changed-request", "stale", "foreign-scope", "changed-mode", "changed-pod", "changed-boot", "changed-history-target"} {
		t.Run(kind, func(t *testing.T) {
			st := b
			request := wcRow{err: pgx.ErrNoRows}
			if kind == "same-request" || kind == "changed-request" || strings.HasPrefix(kind, "changed-") {
				st.Revision++
				st.Mode = wc.PauseNew
				expected := b.Revision
				if kind == "changed-request" {
					expected--
				}
				mode, pod, boot, region := workerModeName(b.Mode), b.PodID, b.BootID, b.Scope.Region
				if kind == "changed-mode" {
					mode = "RECONCILE_ONLY"
				}
				if kind == "changed-pod" {
					pod = "other"
				}
				if kind == "changed-boot" {
					boot = uuid.New()
				}
				if kind == "changed-history-target" {
					region = "other"
				}
				request = wcRow{values: []any{expected, st.Revision, b.Epoch, b.Scope.Region, b.Scope.Cluster, &mode, &pod, &boot}}
				request.values[3] = region
			}
			if kind == "stale" {
				st.Revision++
			}
			if kind == "foreign-scope" {
				st.Scope.Region = "other"
			}
			tx := &wcTx{rows: []wcRow{wcState(st), request}}
			err := wcStore(tx).Pause(t.Context(), b, id)
			if kind == "new" {
				if err != nil || tx.execs != 2 || tx.commits != 1 {
					t.Fatal("pause not atomic")
				}
				return
			}
			if kind == "same-request" {
				if err != nil || tx.execs != 0 || tx.commits != 1 {
					t.Fatal("pause retry changed state")
				}
				return
			}
			if !errors.Is(err, wc.ErrStale) || tx.execs != 0 {
				t.Fatalf("changed payload accepted %v", err)
			}
		})
	}
}

func TestDurableWorkerControlAdapterCreateOnlyPausedAndConsistentReport(t *testing.T) {
	b := wcBinding()
	b.Mode = wc.PauseNew
	b.Revision = 1
	b.Epoch = 1
	id := uuid.New()
	for _, kind := range []string{"create", "retry", "other-request", "active", "other-target"} {
		t.Run(kind, func(t *testing.T) {
			st := b
			prior := wcRow{err: pgx.ErrNoRows}
			if kind == "retry" {
				prior = wcRow{values: []any{id}}
			}
			if kind == "other-request" {
				prior = wcRow{values: []any{uuid.New()}}
			}
			if kind == "active" {
				st.Mode = wc.Active
			}
			if kind == "other-target" {
				st.Scope.Cluster = "other"
			}
			tx := &wcTx{rows: []wcRow{wcState(st), prior}}
			err := wcStore(tx).CreatePaused(t.Context(), b.Scope, id)
			if !strings.Contains(tx.calls[0].sql, "'PAUSE_NEW') ON CONFLICT (control_id) DO NOTHING") {
				t.Fatal("initializer can overwrite")
			}
			if kind == "create" || kind == "retry" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, wc.ErrStale) {
				t.Fatal("existing scope reset")
			}
		})
	}
	for _, kind := range []string{"success", "missing", "invalid", "other-target"} {
		t.Run("report/"+kind, func(t *testing.T) {
			scope := b.Scope
			revision := int64(2)
			if kind == "other-target" {
				scope.Cluster = "other"
			}
			if kind == "invalid" {
				revision = 0
			}
			row := wcRow{values: []any{scope.ID, scope.Region, scope.Cluster, revision, b.Epoch, "PAUSE_NEW", int64(3), int64(0), int64(0), time.Now().UTC()}}
			if kind == "missing" {
				row = wcRow{err: pgx.ErrNoRows}
			}
			tx := &wcTx{rows: []wcRow{row}}
			r, err := wcStore(tx).Report(t.Context(), b.Scope)
			if kind == "success" {
				if err != nil || r.Drained() || r.OpenBoundaries != 3 {
					t.Fatal("open global intents omitted")
				}
				if len(tx.calls) != 1 || !strings.Contains(tx.calls[0].sql, "b.closed_at IS NULL") {
					t.Fatal("inconsistent report")
				}
			} else if !errors.Is(err, wc.ErrUnavailable) {
				t.Fatal("invalid report accepted")
			}
		})
	}
}
