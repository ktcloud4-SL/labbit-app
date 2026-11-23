package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

var _ wc.RestrictStore = (*WorkerControlStore)(nil)

func scanRestriction(row pgx.Row) (wc.RestrictRecord, error) {
	var r wc.RestrictRecord
	var hash string
	var raw, proof []byte
	var applied *time.Time
	if e := row.Scan(&hash, &raw, &proof, &r.Status, &r.CreatedAt, &applied); e != nil {
		return r, e
	}
	if json.Unmarshal(raw, &r.Request) != nil || json.Unmarshal(proof, &r.Approval) != nil || r.Request.Validate() != nil || hash != r.Request.SHA256() || wc.ValidateRestrictionApproval(r.Request, r.Approval, r.CreatedAt) != nil {
		return wc.RestrictRecord{}, wc.ErrUnavailable
	}
	if applied != nil {
		r.AppliedAt = *applied
	}
	if (r.Status == wc.ResumeIntent && applied == nil) || (r.Status == wc.ResumeApplied && applied != nil && !applied.Before(r.CreatedAt) && wc.ValidateRestrictionApproval(r.Request, r.Approval, *applied) == nil) {
		return r, nil
	}
	return wc.RestrictRecord{}, wc.ErrUnavailable
}
func (s *WorkerControlStore) LookupRestriction(ctx context.Context, scope wc.Scope, id uuid.UUID) (wc.RestrictRecord, bool, error) {
	if scope.Validate() != nil || id == uuid.Nil {
		return wc.RestrictRecord{}, false, wc.ErrInvalidBinding
	}
	if s == nil || s.db == nil {
		return wc.RestrictRecord{}, false, wc.ErrUnavailable
	}
	r, e := scanRestriction(s.db.QueryRow(ctx, `SELECT `+resumeColumns+` FROM worker_control_restrictions WHERE command_id=$1`, id))
	if errors.Is(e, pgx.ErrNoRows) {
		return r, false, nil
	}
	if e != nil {
		return wc.RestrictRecord{}, false, wc.ErrUnavailable
	}
	if r.Request.Scope != scope || r.Request.CommandID != id {
		return wc.RestrictRecord{}, true, wc.ErrConflict
	}
	return r, true, nil
}

// Scope lock also serializes init while there is no state row to lock. Existing
// state mutations still serialize with Enter/Resume on the same state row.
func restrictionState(ctx context.Context, tx pgx.Tx, r wc.RestrictRequest) error {
	if _, e := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "worker-control-restrict/"+r.Scope.ID); e != nil {
		return wc.ErrUnavailable
	}
	st, e := scanWorkerState(tx.QueryRow(ctx, `SELECT `+workerStateColumns+` FROM worker_control_states WHERE control_id=$1 FOR UPDATE`, r.Scope.ID))
	if r.Action == "init" {
		if errors.Is(e, wc.ErrUnavailable) {
			var exists bool
			if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM worker_control_states WHERE control_id=$1)`, r.Scope.ID).Scan(&exists) != nil {
				return wc.ErrUnavailable
			}
			if !exists {
				return nil
			}
		}
		return wc.ErrStale
	}
	if e != nil {
		return wc.ErrUnavailable
	}
	if !st.Matches(r.Binding) {
		return wc.ErrStale
	}
	return nil
}
func (s *WorkerControlStore) BeginRestriction(ctx context.Context, r wc.RestrictRequest, a wc.ResumeApproval) (bool, error) {
	if r.Validate() != nil {
		return false, wc.ErrInvalidBinding
	}
	created := false
	e := s.transaction(ctx, func(tx pgx.Tx) error {
		if e := restrictionState(ctx, tx, r); e != nil {
			return e
		}
		old, e := scanRestriction(tx.QueryRow(ctx, `SELECT `+resumeColumns+` FROM worker_control_restrictions WHERE command_id=$1`, r.CommandID))
		if e == nil {
			if old.Request != r {
				return wc.ErrConflict
			}
			return nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return wc.ErrUnavailable
		}
		var pending int64
		if tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM worker_control_restrictions WHERE control_id=$1 AND status='INTENT')+(SELECT count(*) FROM worker_control_resumes WHERE control_id=$1 AND status='INTENT')`, r.Scope.ID).Scan(&pending) != nil {
			return wc.ErrUnavailable
		}
		if pending != 0 {
			return wc.ErrHold
		}
		now, e := resumeNow(ctx, tx)
		if e != nil {
			return e
		}
		if e = wc.ValidateRestrictionApproval(r, a, now); e != nil {
			return e
		}
		raw, _ := json.Marshal(r)
		proof, _ := json.Marshal(a)
		if _, e = tx.Exec(ctx, `INSERT INTO worker_control_restrictions(command_id,control_id,request_sha256,approval_id,request_payload,proof_payload,status,created_at) VALUES($1,$2,$3,$4,$5,$6,'INTENT',$7)`, r.CommandID, r.Scope.ID, r.SHA256(), a.ID, raw, proof, now); e != nil {
			return wc.ErrUnavailable
		}
		created = true
		return nil
	})
	return created, e
}
func (s *WorkerControlStore) ApplyRestriction(ctx context.Context, r wc.RestrictRequest) (wc.RestrictRecord, error) {
	if r.Validate() != nil {
		return wc.RestrictRecord{}, wc.ErrInvalidBinding
	}
	var result wc.RestrictRecord
	e := s.transaction(ctx, func(tx pgx.Tx) error {
		if e := restrictionState(ctx, tx, r); e != nil {
			return e
		}
		v, e := scanRestriction(tx.QueryRow(ctx, `SELECT `+resumeColumns+` FROM worker_control_restrictions WHERE command_id=$1 FOR UPDATE`, r.CommandID))
		if e != nil {
			return wc.ErrUnavailable
		}
		if v.Request != r {
			return wc.ErrConflict
		}
		if v.Status != wc.ResumeIntent {
			return wc.ErrStale
		}
		now, e := resumeNow(ctx, tx)
		if e != nil {
			return e
		}
		if now.Before(v.CreatedAt) {
			return wc.ErrUnavailable
		}
		if e = wc.ValidateRestrictionApproval(r, v.Approval, now); e != nil {
			return e
		}
		if r.Action == "init" {
			e = createPausedWorkerTx(ctx, tx, r.Scope, r.CommandID)
		} else {
			e = pauseWorkerTx(ctx, tx, r.Binding, r.CommandID)
		}
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `UPDATE worker_control_restrictions SET status='APPLIED',applied_at=$2 WHERE command_id=$1`, r.CommandID, now); e != nil {
			return wc.ErrUnavailable
		}
		v.Status = wc.ResumeApplied
		v.AppliedAt = now
		result = v
		return nil
	})
	if e != nil {
		return wc.RestrictRecord{}, e
	}
	return result, nil
}
