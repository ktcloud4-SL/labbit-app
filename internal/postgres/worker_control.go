package postgres

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

// WorkerControlStore uses short transactions only. The selected pool must be
// the single authoritative control Writer; asynchronous replicas are not safe
// substitutes. DB errors are deliberately not returned with raw text.
type WorkerControlStore struct {
	db    dbtx
	begin func(context.Context) (pgx.Tx, error)
}

var _ workercontrol.DurableStore = (*WorkerControlStore)(nil)

func NewWorkerControlStore(pool *pgxpool.Pool) *WorkerControlStore {
	if pool == nil {
		return nil
	}
	return &WorkerControlStore{db: pool, begin: func(ctx context.Context) (pgx.Tx, error) { return pool.BeginTx(ctx, pgx.TxOptions{}) }}
}

func (s *WorkerControlStore) transaction(ctx context.Context, fn func(pgx.Tx) error) error {
	if s == nil || s.db == nil || s.begin == nil {
		return workercontrol.ErrUnavailable
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return workercontrol.ErrUnavailable
	}
	committed := false
	defer func() {
		if !committed {
			c, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
			defer cancel()
			_ = tx.Rollback(c)
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return workercontrol.ErrUnavailable
	}
	committed = true
	return nil
}

const workerStateColumns = `control_id, region, cluster, revision, epoch, mode`

func scanWorkerState(row pgx.Row) (workercontrol.State, error) {
	var st workercontrol.State
	var mode string
	if err := row.Scan(&st.Scope.ID, &st.Scope.Region, &st.Scope.Cluster, &st.Revision, &st.Epoch, &mode); err != nil {
		return st, workercontrol.ErrUnavailable
	}
	m, err := workerMode(mode)
	st.Mode = m
	if err != nil || st.Scope.Validate() != nil || st.Revision < 1 || st.Epoch < 1 {
		return workercontrol.State{}, workercontrol.ErrUnavailable
	}
	return st, nil
}
func workerMode(raw string) (workercontrol.Mode, error) {
	switch raw {
	case "ACTIVE":
		return workercontrol.Active, nil
	case "PAUSE_NEW":
		return workercontrol.PauseNew, nil
	case "RECONCILE_ONLY":
		return workercontrol.ReconcileOnly, nil
	}
	return 0, workercontrol.ErrUnavailable
}
func workerModeName(mode workercontrol.Mode) string {
	switch mode {
	case workercontrol.Active:
		return "ACTIVE"
	case workercontrol.PauseNew:
		return "PAUSE_NEW"
	case workercontrol.ReconcileOnly:
		return "RECONCILE_ONLY"
	}
	return "INVALID"
}

func (s *WorkerControlStore) Enter(ctx context.Context, t workercontrol.Ticket) error {
	if t.Validate() != nil {
		return workercontrol.ErrInvalidBinding
	}
	return s.transaction(ctx, func(tx pgx.Tx) error {
		st, err := scanWorkerState(tx.QueryRow(ctx, `SELECT `+workerStateColumns+` FROM worker_control_states WHERE control_id=$1 FOR UPDATE`, t.Binding.Scope.ID))
		if err != nil {
			return err
		}
		if !st.Matches(t.Binding) {
			return workercontrol.ErrStale
		}
		if st.Mode != workercontrol.Active && !(st.Mode == workercontrol.ReconcileOnly && t.Kind == workercontrol.ReconciliationBoundary) {
			return workercontrol.ErrPaused
		}
		_, err = tx.Exec(ctx, `INSERT INTO worker_control_boundaries (id,control_id,region,cluster,revision,epoch,mode,pod_id,boot_id,kind,phase) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, t.ID, t.Binding.Scope.ID, t.Binding.Scope.Region, t.Binding.Scope.Cluster, t.Binding.Revision, t.Binding.Epoch, workerModeName(t.Binding.Mode), t.Binding.PodID, t.Binding.BootID, string(t.Kind), string(t.Phase))
		if err != nil {
			return workercontrol.ErrUnavailable
		}
		return nil
	})
}

func (s *WorkerControlStore) Leave(ctx context.Context, t workercontrol.Ticket) error {
	if t.Validate() != nil {
		return workercontrol.ErrInvalidBinding
	}
	return s.transaction(ctx, func(tx pgx.Tx) error {
		// Entry binding, not current state: Pause has already advanced revision.
		var closed *time.Time
		err := tx.QueryRow(ctx, `SELECT closed_at FROM worker_control_boundaries WHERE id=$1 AND control_id=$2 AND region=$3 AND cluster=$4 AND revision=$5 AND epoch=$6 AND mode=$7 AND pod_id=$8 AND boot_id=$9 AND kind=$10 AND phase=$11 FOR UPDATE`, t.ID, t.Binding.Scope.ID, t.Binding.Scope.Region, t.Binding.Scope.Cluster, t.Binding.Revision, t.Binding.Epoch, workerModeName(t.Binding.Mode), t.Binding.PodID, t.Binding.BootID, string(t.Kind), string(t.Phase)).Scan(&closed)
		if errors.Is(err, pgx.ErrNoRows) {
			return workercontrol.ErrStale
		}
		if err != nil {
			return workercontrol.ErrUnavailable
		}
		if closed != nil {
			return nil
		}
		_, err = tx.Exec(ctx, `UPDATE worker_control_boundaries SET closed_at=clock_timestamp() WHERE id=$1`, t.ID)
		if err != nil {
			return workercontrol.ErrUnavailable
		}
		return nil
	})
}

func (s *WorkerControlStore) Pause(ctx context.Context, b workercontrol.Binding, requestID uuid.UUID) error {
	if b.Validate() != nil || requestID == uuid.Nil {
		return workercontrol.ErrInvalidBinding
	}
	return s.transaction(ctx, func(tx pgx.Tx) error { return pauseWorkerTx(ctx, tx, b, requestID) })
}

func pauseWorkerTx(ctx context.Context, tx pgx.Tx, b workercontrol.Binding, requestID uuid.UUID) error {
	st, err := scanWorkerState(tx.QueryRow(ctx, `SELECT `+workerStateColumns+` FROM worker_control_states WHERE control_id=$1 FOR UPDATE`, b.Scope.ID))
	if err != nil {
		return err
	}
	if st.Scope != b.Scope || st.Epoch != b.Epoch {
		return workercontrol.ErrStale
	}
	var expected, revision, epoch int64
	var region, cluster string
	var fromMode, podID *string
	var bootID *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT expected_revision,revision,epoch,region,cluster,from_mode,pod_id,boot_id FROM worker_control_changes WHERE control_id=$1 AND request_id=$2`, b.Scope.ID, requestID).Scan(&expected, &revision, &epoch, &region, &cluster, &fromMode, &podID, &bootID)
	if err == nil {
		if expected == b.Revision && revision == st.Revision && epoch == b.Epoch && region == b.Scope.Region && cluster == b.Scope.Cluster && fromMode != nil && *fromMode == workerModeName(b.Mode) && podID != nil && *podID == b.PodID && bootID != nil && *bootID == b.BootID && st.Mode == workercontrol.PauseNew {
			return nil
		}
		return workercontrol.ErrStale
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return workercontrol.ErrUnavailable
	}
	if !st.Matches(b) {
		return workercontrol.ErrStale
	}
	if st.Mode == workercontrol.PauseNew {
		return nil
	} // Observation only; no new revision or implicit activation.
	if st.Revision == math.MaxInt64 {
		return workercontrol.ErrStale
	}
	_, err = tx.Exec(ctx, `UPDATE worker_control_states SET revision=revision+1,mode='PAUSE_NEW',updated_at=clock_timestamp() WHERE control_id=$1`, b.Scope.ID)
	if err != nil {
		return workercontrol.ErrUnavailable
	}
	_, err = tx.Exec(ctx, `INSERT INTO worker_control_changes (control_id,request_id,expected_revision,revision,epoch,mode,region,cluster,from_mode,pod_id,boot_id) VALUES ($1,$2,$3,$4,$5,'PAUSE_NEW',$6,$7,$8,$9,$10)`, b.Scope.ID, requestID, b.Revision, b.Revision+1, b.Epoch, b.Scope.Region, b.Scope.Cluster, workerModeName(b.Mode), b.PodID, b.BootID)
	if err != nil {
		return workercontrol.ErrUnavailable
	}
	return nil
}

// CreatePaused is trusted provisioning only, not an operator API. It cannot
// change an existing row, epoch, target, or mode; duplicate requests are checked.
func (s *WorkerControlStore) CreatePaused(ctx context.Context, scope workercontrol.Scope, requestID uuid.UUID) error {
	if scope.Validate() != nil || requestID == uuid.Nil {
		return workercontrol.ErrInvalidBinding
	}
	return s.transaction(ctx, func(tx pgx.Tx) error { return createPausedWorkerTx(ctx, tx, scope, requestID) })
}

func createPausedWorkerTx(ctx context.Context, tx pgx.Tx, scope workercontrol.Scope, requestID uuid.UUID) error {
	_, err := tx.Exec(ctx, `INSERT INTO worker_control_states (control_id,region,cluster,revision,epoch,mode) VALUES ($1,$2,$3,1,1,'PAUSE_NEW') ON CONFLICT (control_id) DO NOTHING`, scope.ID, scope.Region, scope.Cluster)
	if err != nil {
		return workercontrol.ErrUnavailable
	}
	st, err := scanWorkerState(tx.QueryRow(ctx, `SELECT `+workerStateColumns+` FROM worker_control_states WHERE control_id=$1 FOR UPDATE`, scope.ID))
	if err != nil {
		return err
	}
	if st.Scope != scope || st.Revision != 1 || st.Epoch != 1 || st.Mode != workercontrol.PauseNew {
		return workercontrol.ErrStale
	}
	// Same target with a different creation request is a conflict, not reset.
	var prior uuid.UUID
	err = tx.QueryRow(ctx, `SELECT request_id FROM worker_control_changes WHERE control_id=$1 AND revision=1`, scope.ID).Scan(&prior)
	if err == nil {
		if prior == requestID {
			return nil
		}
		return workercontrol.ErrStale
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return workercontrol.ErrUnavailable
	}
	_, err = tx.Exec(ctx, `INSERT INTO worker_control_changes (control_id,request_id,expected_revision,revision,epoch,mode,region,cluster) VALUES ($1,$2,0,1,1,'PAUSE_NEW',$3,$4)`, scope.ID, requestID, scope.Region, scope.Cluster)
	if err != nil {
		return workercontrol.ErrUnavailable
	}
	return nil
}

func (s *WorkerControlStore) Report(ctx context.Context, scope workercontrol.Scope) (workercontrol.Report, error) {
	if scope.Validate() != nil {
		return workercontrol.Report{}, workercontrol.ErrInvalidBinding
	}
	if s == nil || s.db == nil {
		return workercontrol.Report{}, workercontrol.ErrUnavailable
	}
	var r workercontrol.Report
	var mode string
	// A single MVCC statement includes all open revisions/boots, not just this Pod.
	err := s.db.QueryRow(ctx, `SELECT s.control_id,s.region,s.cluster,s.revision,s.epoch,s.mode,(SELECT count(*) FROM worker_control_boundaries b WHERE b.control_id=s.control_id AND b.closed_at IS NULL),(SELECT count(*) FROM worker_control_resumes x WHERE x.control_id=s.control_id AND x.status='INTENT'),(SELECT count(*) FROM worker_control_restrictions x WHERE x.control_id=s.control_id AND x.status='INTENT'),clock_timestamp() FROM worker_control_states s WHERE s.control_id=$1`, scope.ID).Scan(&r.State.Scope.ID, &r.State.Scope.Region, &r.State.Scope.Cluster, &r.State.Revision, &r.State.Epoch, &mode, &r.OpenBoundaries, &r.PendingResumes, &r.PendingRestrictions, &r.ObservedAt)
	if err != nil {
		return workercontrol.Report{}, workercontrol.ErrUnavailable
	}
	r.State.Mode, err = workerMode(mode)
	if err != nil || r.State.Scope != scope || r.State.Revision < 1 || r.State.Epoch < 1 || r.OpenBoundaries < 0 || r.PendingResumes < 0 || r.PendingRestrictions < 0 || r.ObservedAt.IsZero() {
		return workercontrol.Report{}, workercontrol.ErrUnavailable
	}
	return r, nil
}
