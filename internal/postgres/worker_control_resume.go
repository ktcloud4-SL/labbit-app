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

var _ wc.ResumeStore = (*WorkerControlStore)(nil)

const resumeColumns = `request_sha256,request_payload,proof_payload,status,created_at,applied_at`

func scanResume(row pgx.Row) (wc.ResumeRecord, error) {
	var record wc.ResumeRecord
	var hash, status string
	var request, proof []byte
	var applied *time.Time
	if err := row.Scan(&hash, &request, &proof, &status, &record.CreatedAt, &applied); err != nil {
		return record, err
	}
	if json.Unmarshal(request, &record.Request) != nil || json.Unmarshal(proof, &record.Proof) != nil || record.Request.Validate() != nil || hash != record.Request.SHA256() {
		return wc.ResumeRecord{}, wc.ErrUnavailable
	}
	record.Status = wc.ResumeStatus(status)
	if applied != nil {
		record.AppliedAt = *applied
	}
	if record.Proof.Validate(record.Request, record.CreatedAt) != nil || (record.Status == wc.ResumeIntent && applied != nil) || (record.Status == wc.ResumeApplied && (applied == nil || applied.Before(record.CreatedAt) || record.Proof.Validate(record.Request, *applied) != nil)) || (record.Status != wc.ResumeIntent && record.Status != wc.ResumeApplied) {
		return wc.ResumeRecord{}, wc.ErrUnavailable
	}
	return record, nil
}

func (s *WorkerControlStore) LookupResume(ctx context.Context, scope wc.Scope, id uuid.UUID) (wc.ResumeRecord, bool, error) {
	if scope.Validate() != nil || id == uuid.Nil {
		return wc.ResumeRecord{}, false, wc.ErrInvalidBinding
	}
	if s == nil || s.db == nil {
		return wc.ResumeRecord{}, false, wc.ErrUnavailable
	}
	r, err := scanResume(s.db.QueryRow(ctx, `SELECT `+resumeColumns+` FROM worker_control_resumes WHERE command_id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return wc.ResumeRecord{}, false, nil
	}
	if err != nil {
		return wc.ResumeRecord{}, false, wc.ErrUnavailable
	}
	if r.Request.CommandID != id || r.Request.Scope != scope {
		return wc.ResumeRecord{}, true, wc.ErrConflict
	}
	return r, true, nil
}

func resumeStateMatches(st wc.State, r wc.ResumeRequest) bool {
	return st.Scope == r.Scope && st.Revision == r.ExpectedRevision && st.Epoch == r.ExpectedEpoch && st.Mode == wc.PauseNew
}

func resumeNow(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return now, wc.ErrUnavailable
	}
	return now.UTC(), nil
}

// BeginResume serializes on the same state row as Enter/Pause/Apply. Its
// durable INTENT is intentionally a separate commit from the actual transition.
func (s *WorkerControlStore) BeginResume(ctx context.Context, r wc.ResumeRequest, p wc.ResumeProof) (bool, error) {
	if r.Validate() != nil {
		return false, wc.ErrInvalidBinding
	}
	created := false
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		st, err := scanWorkerState(tx.QueryRow(ctx, `SELECT `+workerStateColumns+` FROM worker_control_states WHERE control_id=$1 FOR UPDATE`, r.Scope.ID))
		if err != nil {
			return err
		}
		prior, err := scanResume(tx.QueryRow(ctx, `SELECT `+resumeColumns+` FROM worker_control_resumes WHERE command_id=$1`, r.CommandID))
		if err == nil {
			if prior.Request != r {
				return wc.ErrConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return wc.ErrUnavailable
		}
		if !resumeStateMatches(st, r) {
			return wc.ErrStale
		}
		var unresolved int64
		if tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM worker_control_boundaries WHERE control_id=$1 AND closed_at IS NULL)+(SELECT count(*) FROM worker_control_resumes WHERE control_id=$1 AND status='INTENT')`, r.Scope.ID).Scan(&unresolved) != nil {
			return wc.ErrUnavailable
		}
		if unresolved != 0 {
			return wc.ErrHold
		}
		now, err := resumeNow(ctx, tx)
		if err != nil {
			return err
		}
		if err = p.Validate(r, now); err != nil {
			return err
		}
		request, err := json.Marshal(r)
		if err != nil {
			return wc.ErrInvalidBinding
		}
		proof, err := json.Marshal(p)
		if err != nil {
			return wc.ErrInvalidBinding
		}
		_, err = tx.Exec(ctx, `INSERT INTO worker_control_resumes (command_id,control_id,request_sha256,approval_id,request_payload,proof_payload,status,created_at) VALUES ($1,$2,$3,$4,$5,$6,'INTENT',$7)`, r.CommandID, r.Scope.ID, r.SHA256(), p.Approval.ID, request, proof, now)
		if err != nil {
			return wc.ErrUnavailable
		}
		created = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return created, nil
}

// ApplyResume is called at most once by the Begin creator, never as crash
// recovery. Any failed/ambiguous response requires read-only inspection.
func (s *WorkerControlStore) ApplyResume(ctx context.Context, r wc.ResumeRequest) (wc.ResumeRecord, error) {
	if r.Validate() != nil {
		return wc.ResumeRecord{}, wc.ErrInvalidBinding
	}
	var result wc.ResumeRecord
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		st, err := scanWorkerState(tx.QueryRow(ctx, `SELECT `+workerStateColumns+` FROM worker_control_states WHERE control_id=$1 FOR UPDATE`, r.Scope.ID))
		if err != nil {
			return err
		}
		record, err := scanResume(tx.QueryRow(ctx, `SELECT `+resumeColumns+` FROM worker_control_resumes WHERE command_id=$1 FOR UPDATE`, r.CommandID))
		if err != nil {
			return wc.ErrUnavailable
		}
		if record.Request != r {
			return wc.ErrConflict
		}
		if record.Status != wc.ResumeIntent {
			return wc.ErrStale
		}
		if !resumeStateMatches(st, r) {
			return wc.ErrStale
		}
		var open int64
		if tx.QueryRow(ctx, `SELECT count(*) FROM worker_control_boundaries WHERE control_id=$1 AND closed_at IS NULL`, r.Scope.ID).Scan(&open) != nil {
			return wc.ErrUnavailable
		}
		if open != 0 {
			return wc.ErrHold
		}
		now, err := resumeNow(ctx, tx)
		if err != nil {
			return err
		}
		if now.Before(record.CreatedAt) {
			return wc.ErrUnavailable
		}
		if err = record.Proof.Validate(r, now); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE worker_control_states SET revision=revision+1,epoch=epoch+1,mode=$2,updated_at=$3 WHERE control_id=$1`, r.Scope.ID, workerModeName(r.Desired), now)
		if err != nil {
			return wc.ErrUnavailable
		}
		_, err = tx.Exec(ctx, `UPDATE worker_control_resumes SET status='APPLIED',applied_at=$2 WHERE command_id=$1`, r.CommandID, now)
		if err != nil {
			return wc.ErrUnavailable
		}
		record.Status = wc.ResumeApplied
		record.AppliedAt = now
		result = record
		return nil
	})
	if err != nil {
		return wc.ResumeRecord{}, err
	}
	return result, nil
}
