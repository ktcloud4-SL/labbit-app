package workercontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"time"

	"github.com/google/uuid"
)

// RestrictRequest only creates PAUSE_NEW or restricts an exact existing binding.
// It never grants dispatch. Init has a zero Binding; pause binds a real process.
type RestrictRequest struct {
	CommandID                                                 uuid.UUID
	Action                                                    string
	Scope                                                     Scope
	Binding                                                   Binding
	TrialID, RunID, Phase, Principal, ApprovalRef, PlanSHA256 string
	RequestedAt, ValidUntil                                   time.Time
}

func (r RestrictRequest) Validate() error {
	if r.CommandID == uuid.Nil || r.Scope.Validate() != nil || !shaPattern.MatchString(r.PlanSHA256) || !exactTime(r.RequestedAt) || !exactTime(r.ValidUntil) || !r.ValidUntil.After(r.RequestedAt) {
		return ErrInvalidBinding
	}
	for _, s := range []string{r.TrialID, r.RunID, r.Phase, r.Principal, r.ApprovalRef} {
		if !labelPattern.MatchString(s) {
			return ErrInvalidBinding
		}
	}
	switch r.Action {
	case "init":
		if r.Binding != (Binding{}) {
			return ErrInvalidBinding
		}
	case "pause":
		if r.Binding.Validate() != nil || r.Binding.Scope != r.Scope || r.Binding.Revision == math.MaxInt64 {
			return ErrInvalidBinding
		}
	default:
		return ErrInvalidBinding
	}
	return nil
}
func (r RestrictRequest) SHA256() string {
	if r.Validate() != nil {
		return ""
	}
	b, _ := json.Marshal(r)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (r RestrictRequest) NextState() State {
	if r.Action == "init" {
		return State{Scope: r.Scope, Revision: 1, Epoch: 1, Mode: PauseNew}
	}
	rev := r.Binding.Revision
	if r.Binding.Mode != PauseNew {
		rev++
	}
	return State{Scope: r.Scope, Revision: rev, Epoch: r.Binding.Epoch, Mode: PauseNew}
}
func ValidateRestrictionApproval(r RestrictRequest, a ResumeApproval, now time.Time) error {
	if r.Validate() != nil {
		return ErrInvalidBinding
	}
	if a.ID == uuid.Nil || a.ID.String() != r.ApprovalRef || a.RequestSHA256 != r.SHA256() || a.Principal != r.Principal || !exactTime(a.NotBefore) || !exactTime(a.ExpiresAt) || !a.ExpiresAt.After(a.NotBefore) || now.Before(a.NotBefore) || !now.Before(a.ExpiresAt) || now.Before(r.RequestedAt) || !now.Before(r.ValidUntil) {
		return ErrApproval
	}
	return nil
}

type RestrictRecord struct {
	Request              RestrictRequest
	Approval             ResumeApproval
	Status               ResumeStatus
	CreatedAt, AppliedAt time.Time
}
type RestrictStore interface {
	LookupRestriction(context.Context, Scope, uuid.UUID) (RestrictRecord, bool, error)
	BeginRestriction(context.Context, RestrictRequest, ResumeApproval) (bool, error)
	ApplyRestriction(context.Context, RestrictRequest) (RestrictRecord, error)
}
type RestrictAuthorizer interface {
	AuthorizeRestriction(context.Context, RestrictRequest) (ResumeApproval, error)
}
type Restrictor struct {
	store RestrictStore
	auth  RestrictAuthorizer
	now   func() time.Time
}

func NewRestrictor(s RestrictStore, a RestrictAuthorizer, now func() time.Time) (*Restrictor, error) {
	if s == nil || a == nil || now == nil {
		return nil, ErrInvalidBinding
	}
	return &Restrictor{s, a, now}, nil
}
func (s *Restrictor) Inspect(ctx context.Context, r RestrictRequest) (RestrictRecord, bool, error) {
	if s == nil || r.Validate() != nil {
		return RestrictRecord{}, false, ErrInvalidBinding
	}
	record, e := resumeCall(ctx, func(c context.Context) (struct {
		R RestrictRecord
		F bool
	}, error) {
		v, f, e := s.store.LookupRestriction(c, r.Scope, r.CommandID)
		return struct {
			R RestrictRecord
			F bool
		}{v, f}, e
	})
	if e != nil {
		return RestrictRecord{}, false, e
	}
	if !record.F {
		return RestrictRecord{}, false, nil
	}
	v := record.R
	if v.Request != r {
		return RestrictRecord{}, true, ErrConflict
	}
	if ValidateRestrictionApproval(r, v.Approval, v.CreatedAt) != nil {
		return RestrictRecord{}, true, ErrUnavailable
	}
	if v.Status == ResumeIntent && v.AppliedAt.IsZero() {
		return v, true, ErrUnknown
	}
	if v.Status != ResumeApplied || v.AppliedAt.Before(v.CreatedAt) || ValidateRestrictionApproval(r, v.Approval, v.AppliedAt) != nil {
		return RestrictRecord{}, true, ErrUnavailable
	}
	return v, true, nil
}
func (s *Restrictor) Restrict(ctx context.Context, r RestrictRequest) (RestrictRecord, error) {
	prior, found, e := s.Inspect(ctx, r)
	if e != nil || found {
		return prior, e
	}
	a, e := resumeCall(ctx, func(c context.Context) (ResumeApproval, error) { return s.auth.AuthorizeRestriction(c, r) })
	if e != nil || ValidateRestrictionApproval(r, a, s.now()) != nil {
		return RestrictRecord{}, ErrApproval
	}
	created, e := resumeCall(ctx, func(c context.Context) (bool, error) { return s.store.BeginRestriction(c, r, a) })
	if e != nil {
		return RestrictRecord{}, ErrUnknown
	}
	if !created {
		v, f, e := s.Inspect(ctx, r)
		if !f && e == nil {
			e = ErrUnknown
		}
		return v, e
	}
	v, e := resumeCall(ctx, func(c context.Context) (RestrictRecord, error) { return s.store.ApplyRestriction(c, r) })
	if e != nil || v.Request != r || v.Approval != a || v.Status != ResumeApplied || v.AppliedAt.Before(v.CreatedAt) || ValidateRestrictionApproval(r, a, v.CreatedAt) != nil || ValidateRestrictionApproval(r, a, v.AppliedAt) != nil {
		return RestrictRecord{}, ErrUnknown
	}
	return v, nil
}
