package drcontrol

import (
	"context"
	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
	"time"
)

type RestrictInvocation struct {
	Version, Action string
	Request         wc.RestrictRequest
	Approval        Reference
}

func (p Policy) CheckRestriction(i RestrictInvocation) error {
	if p.Validate() != nil || i.Version != Version || i.Request.Validate() != nil || i.Request.Scope != p.Target || (i.Action != i.Request.Action && i.Action != "inspect-restriction") {
		return ErrInput
	}
	if i.Action != "inspect-restriction" && p.CheckReference(i.Approval) != nil {
		return ErrInput
	}
	return nil
}
func AuthenticateRestriction(ctx context.Context, p Policy, i RestrictInvocation, reader IdentityReader) error {
	if p.CheckRestriction(i) != nil || reader == nil || ctx.Err() != nil {
		return ErrDenied
	}
	id, e := reader.Identity(ctx)
	if e != nil || ctx.Err() != nil || id.Account != p.AccountID {
		return ErrDenied
	}
	for _, a := range p.Actors {
		if a.ARN == id.ARN && a.Principal == i.Request.Principal && contains(a.Actions, i.Action) {
			return nil
		}
	}
	return ErrDenied
}

type restrictionVerifier struct {
	p      Policy
	i      RestrictInvocation
	reader ArtifactReader
	now    func() time.Time
}

func (v restrictionVerifier) AuthorizeRestriction(ctx context.Context, r wc.RestrictRequest) (wc.ResumeApproval, error) {
	if r != v.i.Request {
		return wc.ResumeApproval{}, ErrDenied
	}
	var a Approval
	k, e := readSigned(ctx, v.p, v.reader, v.i.Approval, "restriction-approval", v.p.ApprovalKeys, r.Phase, &a)
	if e != nil || k.Principal != r.Principal || a.Version != Version || a.Action != r.Action || !validTime(a.NotBefore) || !validTime(a.ExpiresAt) || a.ExpiresAt.Sub(a.NotBefore) > time.Duration(v.p.ApprovalMaxSeconds)*time.Second {
		return wc.ResumeApproval{}, wc.ErrApproval
	}
	proof := wc.ResumeApproval{RequestSHA256: a.RequestSHA256, ID: a.ID, Principal: a.Principal, NotBefore: a.NotBefore, ExpiresAt: a.ExpiresAt}
	if wc.ValidateRestrictionApproval(r, proof, v.now()) != nil {
		return wc.ResumeApproval{}, wc.ErrApproval
	}
	return proof, nil
}
func ExecuteRestriction(ctx context.Context, p Policy, i RestrictInvocation, identity IdentityReader, artifacts ArtifactReader, store wc.RestrictStore, now func() time.Time) (wc.RestrictRecord, error) {
	if !p.EnableExecution || store == nil || now == nil {
		return wc.RestrictRecord{}, ErrDenied
	}
	if e := AuthenticateRestriction(ctx, p, i, identity); e != nil {
		return wc.RestrictRecord{}, e
	}
	s, e := wc.NewRestrictor(store, restrictionVerifier{p, i, artifacts, now}, now)
	if e != nil {
		return wc.RestrictRecord{}, ErrInput
	}
	if i.Action == "inspect-restriction" {
		v, f, e := s.Inspect(ctx, i.Request)
		if !f && e == nil {
			e = ErrUnavailable
		}
		return v, e
	}
	prior, found, e := s.Inspect(ctx, i.Request)
	if e != nil || found {
		return prior, e
	}
	prepared, e := PrepareArtifacts(ctx, p, artifacts, i.Approval)
	if e != nil {
		return wc.RestrictRecord{}, e
	}
	defer prepared.Clear()
	s, e = wc.NewRestrictor(store, restrictionVerifier{p, i, prepared, now}, now)
	if e != nil {
		return wc.RestrictRecord{}, ErrInput
	}
	return s.Restrict(ctx, i.Request)
}
