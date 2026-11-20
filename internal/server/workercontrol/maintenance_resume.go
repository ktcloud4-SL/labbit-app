package workercontrol

import (
	"context"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/server/maintenance"
)

// MaintenanceResumer connects PR194's local barrier to the approval journal.
// It never opens the file, rolls out Pods or changes an existing Control. A
// successful receipt still requires an approved external deployment of the new
// exact binding and a separately observed file opening. External Authorizer and
// Rechecker remain mandatory; the local check cannot replace PodCoverage,
// fencing, actual Writer or accepted/UNKNOWN work reconciliation.
type MaintenanceResumer struct {
	gate    *maintenance.Gate
	resumer *Resumer
}

func NewMaintenanceResumer(store ResumeStore, auth ResumeAuthorizer, checks ResumeRechecker, gate *maintenance.Gate, now func() time.Time) (*MaintenanceResumer, error) {
	if store == nil || !gate.Configured() {
		return nil, ErrInvalidBinding
	}
	r, err := NewResumer(maintenanceResumeStore{ResumeStore: store, gate: gate}, auth, checks, now)
	if err != nil {
		return nil, err
	}
	return &MaintenanceResumer{gate: gate, resumer: r}, nil
}

func (s *MaintenanceResumer) Resume(ctx context.Context, request ResumeRequest) (ResumeRecord, error) {
	if s == nil {
		return ResumeRecord{}, ErrInvalidBinding
	}
	// Historical readback is allowed after opening, but never re-applies state.
	prior, found, err := s.resumer.Inspect(ctx, request)
	if err != nil || found {
		return prior, err
	}
	if !s.gate.LocalResumeReady() {
		return ResumeRecord{}, ErrHold
	}
	return s.resumer.Resume(ctx, request)
}

type maintenanceResumeStore struct {
	ResumeStore
	gate *maintenance.Gate
}

func (s maintenanceResumeStore) BeginResume(ctx context.Context, r ResumeRequest, p ResumeProof) (bool, error) {
	if !s.gate.LocalResumeReady() {
		return false, ErrHold
	}
	return s.ResumeStore.BeginResume(ctx, r, p)
}

func (s maintenanceResumeStore) ApplyResume(ctx context.Context, r ResumeRequest) (ResumeRecord, error) {
	if !s.gate.LocalResumeReady() {
		return ResumeRecord{}, ErrHold
	}
	return s.ResumeStore.ApplyResume(ctx, r)
}
