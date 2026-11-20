// Package workercontrolfake is an offline fixture. It is not a persistence or
// PostgreSQL locking implementation and must never be used by application code.
package workercontrolfake

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

type Faults struct{ Enter, EnterCommitted, Leave, LeaveCommitted, Pause, Report, ResumeLookup, ResumeBegin, ResumeBeginCommitted, ResumeApply, ResumeApplyCommitted bool }
type Store struct {
	mu        sync.Mutex
	state     wc.State
	open      map[uuid.UUID]wc.Ticket
	closed    map[uuid.UUID]wc.Ticket
	pauses    map[uuid.UUID]wc.Binding
	faults    Faults
	resumes   map[uuid.UUID]wc.ResumeRecord
	approvals map[uuid.UUID]uuid.UUID
	clock     func() time.Time
}

// New seeds an already-approved mode for tests only, bypassing production's
// separate trusted approval/RECHECK/INTENT path.
func New(state wc.State) *Store {
	return &Store{state: state, open: map[uuid.UUID]wc.Ticket{}, closed: map[uuid.UUID]wc.Ticket{}, pauses: map[uuid.UUID]wc.Binding{}, resumes: map[uuid.UUID]wc.ResumeRecord{}, approvals: map[uuid.UUID]uuid.UUID{}, clock: func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }}
}
func (s *Store) SetFaults(f Faults) { s.mu.Lock(); defer s.mu.Unlock(); s.faults = f }
func (s *Store) Enter(ctx context.Context, t wc.Ticket) error {
	if t.Validate() != nil {
		return wc.ErrInvalidBinding
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.faults.Enter {
		return wc.ErrUnavailable
	}
	if !s.state.Matches(t.Binding) {
		return wc.ErrStale
	}
	if s.state.Mode != wc.Active && !(s.state.Mode == wc.ReconcileOnly && t.Kind == wc.ReconciliationBoundary) {
		return wc.ErrPaused
	}
	if _, ok := s.open[t.ID]; ok {
		return wc.ErrUnavailable
	}
	if _, ok := s.closed[t.ID]; ok {
		return wc.ErrUnavailable
	}
	s.open[t.ID] = t
	if s.faults.EnterCommitted {
		return wc.ErrUnavailable
	}
	return nil
}
func (s *Store) Leave(ctx context.Context, t wc.Ticket) error {
	if t.Validate() != nil {
		return wc.ErrInvalidBinding
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.faults.Leave {
		return wc.ErrUnavailable
	}
	if old, ok := s.closed[t.ID]; ok {
		if old == t {
			return nil
		}
		return wc.ErrStale
	}
	if old, ok := s.open[t.ID]; !ok || old != t {
		return wc.ErrStale
	}
	delete(s.open, t.ID)
	s.closed[t.ID] = t
	if s.faults.LeaveCommitted {
		return wc.ErrUnavailable
	}
	return nil
}
func (s *Store) Pause(ctx context.Context, b wc.Binding, id uuid.UUID) error {
	if b.Validate() != nil || id == uuid.Nil {
		return wc.ErrInvalidBinding
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.faults.Pause {
		return wc.ErrUnavailable
	}
	if old, ok := s.pauses[id]; ok {
		if old == b && s.state.Scope == b.Scope && s.state.Epoch == b.Epoch && s.state.Mode == wc.PauseNew && s.state.Revision == b.Revision+1 {
			return nil
		}
		return wc.ErrStale
	}
	if !s.state.Matches(b) {
		return wc.ErrStale
	}
	if s.state.Mode == wc.PauseNew {
		return nil
	}
	s.state.Mode = wc.PauseNew
	s.state.Revision++
	s.pauses[id] = b
	return nil
}
func (s *Store) Report(ctx context.Context, scope wc.Scope) (wc.Report, error) {
	if err := ctx.Err(); err != nil {
		return wc.Report{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.faults.Report || s.state.Scope != scope {
		return wc.Report{}, wc.ErrUnavailable
	}
	var pending int64
	for _, r := range s.resumes {
		if r.Status == wc.ResumeIntent {
			pending++
		}
	}
	return wc.Report{State: s.state, OpenBoundaries: int64(len(s.open)), PendingResumes: pending, ObservedAt: s.clock()}, nil
}
func (s *Store) OpenTickets() []wc.Ticket {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []wc.Ticket{}
	for _, t := range s.open {
		out = append(out, t)
	}
	return out
}
