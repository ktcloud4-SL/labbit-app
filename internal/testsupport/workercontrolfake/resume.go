package workercontrolfake

import (
	"context"
	"time"

	"github.com/google/uuid"
	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

func (s *Store) SetClock(clock func() time.Time) { s.mu.Lock(); defer s.mu.Unlock(); s.clock = clock }

func (s *Store) LookupResume(ctx context.Context, scope wc.Scope, id uuid.UUID) (wc.ResumeRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return wc.ResumeRecord{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.faults.ResumeLookup {
		return wc.ResumeRecord{}, false, wc.ErrUnavailable
	}
	r, ok := s.resumes[id]
	if ok && r.Request.Scope != scope {
		return wc.ResumeRecord{}, true, wc.ErrConflict
	}
	return r, ok, nil
}

func (s *Store) BeginResume(ctx context.Context, r wc.ResumeRequest, p wc.ResumeProof) (bool, error) {
	if r.Validate() != nil {
		return false, wc.ErrInvalidBinding
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.faults.ResumeBegin {
		return false, wc.ErrUnavailable
	}
	if prior, ok := s.resumes[r.CommandID]; ok {
		if prior.Request != r {
			return false, wc.ErrConflict
		}
		return false, nil
	}
	if s.state.Scope != r.Scope || s.state.Revision != r.ExpectedRevision || s.state.Epoch != r.ExpectedEpoch || s.state.Mode != wc.PauseNew {
		return false, wc.ErrStale
	}
	if len(s.open) != 0 {
		return false, wc.ErrHold
	}
	for _, prior := range s.resumes {
		if prior.Status == wc.ResumeIntent {
			return false, wc.ErrHold
		}
	}
	if _, ok := s.approvals[p.Approval.ID]; ok {
		return false, wc.ErrConflict
	}
	now := s.clock()
	if err := p.Validate(r, now); err != nil {
		return false, err
	}
	s.resumes[r.CommandID] = wc.ResumeRecord{Request: r, Proof: p, Status: wc.ResumeIntent, CreatedAt: now}
	s.approvals[p.Approval.ID] = r.CommandID
	if s.faults.ResumeBeginCommitted {
		return false, wc.ErrUnavailable
	}
	return true, nil
}

func (s *Store) ApplyResume(ctx context.Context, r wc.ResumeRequest) (wc.ResumeRecord, error) {
	if r.Validate() != nil {
		return wc.ResumeRecord{}, wc.ErrInvalidBinding
	}
	if err := ctx.Err(); err != nil {
		return wc.ResumeRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.faults.ResumeApply {
		return wc.ResumeRecord{}, wc.ErrUnavailable
	}
	record, ok := s.resumes[r.CommandID]
	if !ok || record.Request != r {
		return wc.ResumeRecord{}, wc.ErrConflict
	}
	if record.Status != wc.ResumeIntent || s.state.Scope != r.Scope || s.state.Revision != r.ExpectedRevision || s.state.Epoch != r.ExpectedEpoch || s.state.Mode != wc.PauseNew {
		return wc.ResumeRecord{}, wc.ErrStale
	}
	if len(s.open) != 0 {
		return wc.ResumeRecord{}, wc.ErrHold
	}
	now := s.clock()
	if now.Before(record.CreatedAt) {
		return wc.ResumeRecord{}, wc.ErrUnavailable
	}
	if err := record.Proof.Validate(r, now); err != nil {
		return wc.ResumeRecord{}, err
	}
	s.state = r.NextState()
	record.Status = wc.ResumeApplied
	record.AppliedAt = now
	s.resumes[r.CommandID] = record
	if s.faults.ResumeApplyCommitted {
		return wc.ResumeRecord{}, wc.ErrUnavailable
	}
	return record, nil
}
