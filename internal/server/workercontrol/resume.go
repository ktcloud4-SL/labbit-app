package workercontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"time"

	"github.com/google/uuid"
)

var (
	ErrApproval = errors.New("workercontrol: approval not verified")
	ErrUnknown  = errors.New("workercontrol: resume outcome unknown")
	ErrConflict = errors.New("workercontrol: command payload conflict")
)

var shaPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// ResumeRequest is an internal, immutable command, not an HTTP request or an
// approval. A new command is required for every phase, failback and mode change.
// Timestamps use UTC microseconds, matching PostgreSQL's timestamp precision.
type ResumeRequest struct {
	CommandID                                     uuid.UUID
	Scope                                         Scope
	ExpectedRevision, ExpectedEpoch               int64
	Desired                                       Mode
	TrialID, RunID, Phase, Principal, ApprovalRef string
	PlanSHA256, PreconditionsSHA256               string
	RequestedAt, ValidUntil                       time.Time
}

func exactTime(t time.Time) bool {
	return !t.IsZero() && t.Year() >= 1 && t.Year() <= 9999 && t.Location() == time.UTC && t.Equal(t.Truncate(time.Microsecond))
}

func (r ResumeRequest) Validate() error {
	if r.CommandID == uuid.Nil || r.Scope.Validate() != nil || r.ExpectedRevision < 1 || r.ExpectedRevision == math.MaxInt64 || r.ExpectedEpoch < 1 || r.ExpectedEpoch == math.MaxInt64 || (r.Desired != Active && r.Desired != ReconcileOnly) || !shaPattern.MatchString(r.PlanSHA256) || !shaPattern.MatchString(r.PreconditionsSHA256) || !exactTime(r.RequestedAt) || !exactTime(r.ValidUntil) || !r.ValidUntil.After(r.RequestedAt) {
		return ErrInvalidBinding
	}
	for _, label := range []string{r.TrialID, r.RunID, r.Phase, r.Principal, r.ApprovalRef} {
		if !labelPattern.MatchString(label) {
			return ErrInvalidBinding
		}
	}
	return nil
}

func (r ResumeRequest) SHA256() string {
	if r.Validate() != nil {
		return ""
	}
	b, err := json.Marshal(r)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (r ResumeRequest) NextState() State {
	return State{Scope: r.Scope, Revision: r.ExpectedRevision + 1, Epoch: r.ExpectedEpoch + 1, Mode: r.Desired}
}

// These ports must be implemented by trusted authentication/Evidence readers.
// References and caller-supplied approved=true are never proof. This increment
// supplies no production verifier and exposes no endpoint or auto-resume loop.
type ResumeAuthorizer interface {
	AuthorizeResume(context.Context, ResumeRequest) (ResumeApproval, error)
}
type ResumeRechecker interface {
	RecheckResume(context.Context, ResumeRequest) (ResumeChecks, error)
}
type ResumeApproval struct {
	RequestSHA256        string
	ID                   uuid.UUID
	Principal            string
	NotBefore, ExpiresAt time.Time
}
type ResumeChecks struct {
	RequestSHA256, EvidenceSHA256                                                       string
	ObservedAt, ValidUntil                                                              time.Time
	AdmissionBlocked, SingleWriter, Fencing, WorkInventory, PodCoverage, WorkReconciled string
}
type ResumeProof struct {
	Approval ResumeApproval
	Checks   ResumeChecks
}

// Validate checks exact binding and time again at INTENT and apply. PASS here
// is accepted only from the trusted ports; it does not itself implement fencing.
func (p ResumeProof) Validate(r ResumeRequest, now time.Time) error {
	if r.Validate() != nil {
		return ErrInvalidBinding
	}
	a, c := p.Approval, p.Checks
	if a.ID == uuid.Nil || a.RequestSHA256 != r.SHA256() || a.Principal != r.Principal || !exactTime(a.NotBefore) || !exactTime(a.ExpiresAt) || !a.ExpiresAt.After(a.NotBefore) || now.Before(a.NotBefore) || !now.Before(a.ExpiresAt) || now.Before(r.RequestedAt) || !now.Before(r.ValidUntil) {
		return ErrApproval
	}
	if c.RequestSHA256 != r.SHA256() || c.EvidenceSHA256 != r.PreconditionsSHA256 || !exactTime(c.ObservedAt) || !exactTime(c.ValidUntil) || c.ObservedAt.Before(r.RequestedAt) || c.ObservedAt.After(now) || !c.ValidUntil.After(c.ObservedAt) || !now.Before(c.ValidUntil) {
		return ErrUnavailable
	}
	for _, result := range []string{c.AdmissionBlocked, c.SingleWriter, c.Fencing, c.WorkInventory, c.PodCoverage} {
		if result != "PASS" {
			return ErrHold
		}
	}
	if r.Desired == Active && c.WorkReconciled != "PASS" {
		return ErrHold
	}
	return nil
}

type ResumeStatus string

const (
	ResumeIntent  ResumeStatus = "INTENT"
	ResumeApplied ResumeStatus = "APPLIED"
)

// APPLIED is a historical command receipt, never permission to dispatch. Each
// participating new process must still Enter with the exact new binding.
type ResumeRecord struct {
	Request              ResumeRequest
	Proof                ResumeProof
	Status               ResumeStatus
	CreatedAt, AppliedAt time.Time
}

// BeginResume commits an INTENT before ApplyResume. Only the caller receiving
// created=true may attempt Apply once. An existing INTENT is UNKNOWN/HOLD;
// restarts and duplicate callers may only inspect it. Do not auto-retry writes.
// All methods are trusted internal control-plane ports, not end-user APIs.
type ResumeStore interface {
	LookupResume(context.Context, Scope, uuid.UUID) (ResumeRecord, bool, error)
	BeginResume(context.Context, ResumeRequest, ResumeProof) (created bool, err error)
	ApplyResume(context.Context, ResumeRequest) (ResumeRecord, error)
}

type Resumer struct {
	store      ResumeStore
	authorizer ResumeAuthorizer
	rechecker  ResumeRechecker
	now        func() time.Time
}

func NewResumer(store ResumeStore, auth ResumeAuthorizer, recheck ResumeRechecker, now func() time.Time) (*Resumer, error) {
	if store == nil || auth == nil || recheck == nil || now == nil {
		return nil, ErrInvalidBinding
	}
	return &Resumer{store: store, authorizer: auth, rechecker: recheck, now: now}, nil
}

func resumeCall[T any](ctx context.Context, fn func(context.Context) (T, error)) (T, error) {
	c, cancel := context.WithTimeout(ctx, durableQueryTimeout)
	defer cancel()
	return fn(c)
}

func (s *Resumer) Inspect(ctx context.Context, r ResumeRequest) (ResumeRecord, bool, error) {
	if s == nil || r.Validate() != nil {
		return ResumeRecord{}, false, ErrInvalidBinding
	}
	c, cancel := context.WithTimeout(ctx, durableQueryTimeout)
	defer cancel()
	record, found, err := s.store.LookupResume(c, r.Scope, r.CommandID)
	if err != nil {
		if err == ErrConflict {
			return ResumeRecord{}, true, ErrConflict
		}
		return ResumeRecord{}, false, ErrUnavailable
	}
	if !found {
		return ResumeRecord{}, false, nil
	}
	if record.Request != r {
		return ResumeRecord{}, true, ErrConflict
	}
	if record.Proof.Validate(r, record.CreatedAt) != nil || record.CreatedAt.IsZero() {
		return ResumeRecord{}, true, ErrUnavailable
	}
	if record.Status == ResumeIntent {
		return record, true, ErrUnknown
	}
	if record.Status != ResumeApplied || record.AppliedAt.Before(record.CreatedAt) || record.Proof.Validate(r, record.AppliedAt) != nil {
		return ResumeRecord{}, true, ErrUnavailable
	}
	return record, true, nil
}

func (s *Resumer) Resume(ctx context.Context, r ResumeRequest) (ResumeRecord, error) {
	if s == nil || r.Validate() != nil {
		return ResumeRecord{}, ErrInvalidBinding
	}
	prior, found, err := s.Inspect(ctx, r)
	if err != nil || found {
		return prior, err
	}
	a, err := resumeCall(ctx, func(c context.Context) (ResumeApproval, error) { return s.authorizer.AuthorizeResume(c, r) })
	if err != nil {
		return ResumeRecord{}, ErrApproval
	}
	c, err := resumeCall(ctx, func(c context.Context) (ResumeChecks, error) { return s.rechecker.RecheckResume(c, r) })
	if err != nil {
		return ResumeRecord{}, ErrUnavailable
	}
	proof := ResumeProof{Approval: a, Checks: c}
	if err = proof.Validate(r, s.now()); err != nil {
		return ResumeRecord{}, err
	}
	created, err := resumeCall(ctx, func(c context.Context) (bool, error) { return s.store.BeginResume(c, r, proof) })
	if err != nil {
		return ResumeRecord{}, ErrUnknown
	}
	if !created {
		record, found, err := s.Inspect(ctx, r)
		if !found && err == nil {
			return ResumeRecord{}, ErrUnknown
		}
		return record, err
	}
	// Any error from this point preserves the INTENT. No automatic retry, no
	// compensating activation, and no clearing another process's pause latch.
	record, err := resumeCall(ctx, func(c context.Context) (ResumeRecord, error) { return s.store.ApplyResume(c, r) })
	if err != nil {
		return ResumeRecord{}, ErrUnknown
	}
	if record.Request != r || record.Status != ResumeApplied || record.Proof != proof || record.AppliedAt.Before(record.CreatedAt) || record.Proof.Validate(r, record.CreatedAt) != nil || record.Proof.Validate(r, record.AppliedAt) != nil {
		return ResumeRecord{}, ErrUnknown
	}
	return record, nil
}
