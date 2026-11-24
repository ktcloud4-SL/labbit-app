package drcontrol

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"time"

	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

type Identity struct{ Account, ARN string }
type IdentityReader interface {
	Identity(context.Context) (Identity, error)
}
type ArtifactReader interface {
	Read(context.Context, Reference) ([]byte, error)
}

func Authenticate(ctx context.Context, p Policy, i Invocation, reader IdentityReader) error {
	if reader == nil || ctx.Err() != nil || p.CheckInvocation(i) != nil {
		return ErrDenied
	}
	id, err := reader.Identity(ctx)
	if err != nil || ctx.Err() != nil || id.Account != p.AccountID {
		return ErrDenied
	}
	for _, a := range p.Actors {
		if a.ARN == id.ARN && a.Principal == i.Request.Principal && contains(a.Actions, i.Action) {
			return nil
		}
	}
	return ErrDenied
}

type Verifier struct {
	policy Policy
	input  Invocation
	reader ArtifactReader
	now    func() time.Time
}

func NewVerifier(p Policy, i Invocation, r ArtifactReader, now func() time.Time) (*Verifier, error) {
	if p.CheckInvocation(i) != nil || i.Action != "resume" || r == nil || now == nil {
		return nil, ErrInput
	}
	return &Verifier{p, i, r, now}, nil
}
func (v *Verifier) document(ctx context.Context, ref Reference, purpose string, keys []Key, request wc.ResumeRequest, dst any) (Key, error) {
	if ctx.Err() != nil || request != v.input.Request || v.policy.CheckReference(ref) != nil {
		return Key{}, ErrEvidence
	}
	b, err := v.reader.Read(ctx, ref)
	if err != nil || ctx.Err() != nil || int64(len(b)) != ref.Size || Hash(b) != ref.SHA256 {
		return Key{}, ErrEvidence
	}
	var signed Signed
	if Strict(b, &signed) != nil || signed.Version != Version {
		return Key{}, ErrEvidence
	}
	for _, key := range keys {
		if signed.KeyID == key.ID && contains(key.Phases, request.Phase) {
			pub, _ := base64.StdEncoding.DecodeString(key.PublicKey)
			sig, e := base64.StdEncoding.DecodeString(signed.Signature)
			message := append([]byte(Version+"/"+purpose+"\n"), signed.Payload...)
			if e != nil || len(sig) != ed25519.SignatureSize || !ed25519.Verify(ed25519.PublicKey(pub), message, sig) || Strict(signed.Payload, dst) != nil {
				return Key{}, ErrEvidence
			}
			return key, nil
		}
	}
	return Key{}, ErrEvidence
}
func (v *Verifier) AuthorizeResume(ctx context.Context, r wc.ResumeRequest) (wc.ResumeApproval, error) {
	var a Approval
	k, e := v.document(ctx, v.input.Approval, "approval", v.policy.ApprovalKeys, r, &a)
	if e != nil || a.Version != Version || a.Action != "resume" || a.ID.String() != r.ApprovalRef || a.RequestSHA256 != r.SHA256() || a.Principal != r.Principal || k.Principal != r.Principal || !validTime(a.NotBefore) || !validTime(a.ExpiresAt) || !a.ExpiresAt.After(a.NotBefore) || a.ExpiresAt.Sub(a.NotBefore) > time.Duration(v.policy.ApprovalMaxSeconds)*time.Second {
		return wc.ResumeApproval{}, wc.ErrApproval
	}
	approval := wc.ResumeApproval{RequestSHA256: a.RequestSHA256, ID: a.ID, Principal: a.Principal, NotBefore: a.NotBefore, ExpiresAt: a.ExpiresAt}
	now := v.now()
	if now.Before(a.NotBefore) || !now.Before(a.ExpiresAt) || now.Before(r.RequestedAt) || !now.Before(r.ValidUntil) {
		return wc.ResumeApproval{}, wc.ErrApproval
	}
	return approval, nil
}
func (v *Verifier) RecheckResume(ctx context.Context, r wc.ResumeRequest) (wc.ResumeChecks, error) {
	var o Observation
	_, e := v.document(ctx, v.input.Evidence, "observation", v.policy.ObservationKeys, r, &o)
	if e != nil || o.Version != Version || o.Source != "LIVE_READBACK" || o.Binding != Bind(r) || !validTime(o.ObservedAt) || !validTime(o.ValidUntil) || !o.ValidUntil.After(o.ObservedAt) || o.ValidUntil.Sub(o.ObservedAt) > time.Duration(v.policy.EvidenceMaxSeconds)*time.Second {
		return wc.ResumeChecks{}, wc.ErrUnavailable
	}
	c := wc.ResumeChecks{RequestSHA256: r.SHA256(), EvidenceSHA256: r.PreconditionsSHA256, ObservedAt: o.ObservedAt, ValidUntil: o.ValidUntil, AdmissionBlocked: o.AdmissionBlocked, SingleWriter: o.SingleWriter, Fencing: o.Fencing, WorkInventory: o.WorkInventory, PodCoverage: o.PodCoverage, WorkReconciled: o.WorkReconciled}
	// Reuse the owning proof's exact six safety conditions and time semantics.
	a := wc.ResumeApproval{RequestSHA256: r.SHA256(), ID: r.CommandID, Principal: r.Principal, NotBefore: r.RequestedAt, ExpiresAt: r.ValidUntil}
	if (wc.ResumeProof{Approval: a, Checks: c}).Validate(r, v.now()) != nil {
		return wc.ResumeChecks{}, wc.ErrHold
	}
	return c, nil
}

// Execute authenticates on every invocation, including APPLIED/INTENT readback.
// An Inspect response is historical only. An existing INTENT is never applied.
func Execute(ctx context.Context, p Policy, i Invocation, identity IdentityReader, artifacts ArtifactReader, store wc.ResumeStore, now func() time.Time) (wc.ResumeRecord, error) {
	if !p.EnableExecution || store == nil || now == nil {
		return wc.ResumeRecord{}, ErrDenied
	}
	if err := Authenticate(ctx, p, i, identity); err != nil {
		return wc.ResumeRecord{}, err
	}
	if i.Action == "inspect" {
		// Resumer.Inspect does not invoke these ports; nil ports remain forbidden.
		r, e := wc.NewResumer(store, denyPorts{}, denyPorts{}, now)
		if e != nil {
			return wc.ResumeRecord{}, ErrInput
		}
		result, found, e := r.Inspect(ctx, i.Request)
		if e != nil {
			return result, e
		}
		if !found {
			return wc.ResumeRecord{}, ErrUnavailable
		}
		return result, nil
	}
	v, e := NewVerifier(p, i, artifacts, now)
	if e != nil {
		return wc.ResumeRecord{}, e
	}
	r, e := wc.NewResumer(store, v, v, now)
	if e != nil {
		return wc.ResumeRecord{}, ErrInput
	}
	// Preserve authenticated historical replay even when old artifacts expire or
	// disappear. Resume performs another Inspect/CAS after preparation for races.
	prior, found, e := r.Inspect(ctx, i.Request)
	if e != nil || found {
		return prior, e
	}
	prepared, e := PrepareArtifacts(ctx, p, artifacts, i.Approval, i.Evidence)
	if e != nil {
		return wc.ResumeRecord{}, e
	}
	defer prepared.Clear()
	v.reader = prepared
	return r.Resume(ctx, i.Request)
}

type denyPorts struct{}

func (denyPorts) AuthorizeResume(context.Context, wc.ResumeRequest) (wc.ResumeApproval, error) {
	return wc.ResumeApproval{}, wc.ErrApproval
}
func (denyPorts) RecheckResume(context.Context, wc.ResumeRequest) (wc.ResumeChecks, error) {
	return wc.ResumeChecks{}, wc.ErrHold
}
