package drcontrol

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"github.com/google/uuid"
	"strings"
	"time"

	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

// readSigned preserves exact signed payload bytes. Signature proves origin,
// never the truth of the observation; the collector owns factual validation.
func readSigned(ctx context.Context, p Policy, reader ArtifactReader, ref Reference, purpose string, keys []Key, phase string, dst any) (Key, error) {
	if reader == nil || ctx.Err() != nil || p.CheckReference(ref) != nil {
		return Key{}, ErrEvidence
	}
	b, e := reader.Read(ctx, ref)
	if e != nil || ctx.Err() != nil || int64(len(b)) != ref.Size || Hash(b) != ref.SHA256 {
		return Key{}, ErrEvidence
	}
	var s Signed
	if Strict(b, &s) != nil || s.Version != Version {
		return Key{}, ErrEvidence
	}
	for _, k := range keys {
		if k.ID != s.KeyID || !contains(k.Phases, phase) {
			continue
		}
		pub, _ := base64.StdEncoding.DecodeString(k.PublicKey)
		sig, e := base64.StdEncoding.DecodeString(s.Signature)
		if e != nil || len(sig) != ed25519.SignatureSize || !ed25519.Verify(pub, append([]byte(Version+"/"+purpose+"\n"), s.Payload...), sig) || Strict(s.Payload, dst) != nil {
			return Key{}, ErrEvidence
		}
		return k, nil
	}
	return Key{}, ErrEvidence
}

// SignDocument accepts typed documents only, never arbitrary caller JSON.
// Keys are PKCS8 Ed25519 PEM in OS-protected local files, not CLI arguments.
func SignDocument(p Policy, purpose, keyID, keyFile string, payload any, now time.Time) ([]byte, error) {
	if p.Validate() != nil {
		return nil, ErrInput
	}
	var phase, principal string
	var keys []Key
	switch v := payload.(type) {
	case Approval:
		if purpose != "approval" && purpose != "restriction-approval" {
			return nil, ErrInput
		}
		// Approval has no phase field: use SignApproval with its exact request.
		return nil, ErrInput
	case Observation:
		if purpose != "observation" || v.Version != Version || v.Source != "LIVE_READBACK" || !validTime(v.ObservedAt) || !validTime(v.ValidUntil) || v.ObservedAt.After(now) || !now.Before(v.ValidUntil) || v.ValidUntil.Sub(v.ObservedAt) > time.Duration(p.EvidenceMaxSeconds)*time.Second || v.Binding.Scope != p.Target {
			return nil, ErrInput
		}
		phase = v.Binding.Phase
		b := v.Binding
		request := wc.ResumeRequest{CommandID: b.CommandID, Scope: b.Scope, ExpectedRevision: b.ExpectedRevision, ExpectedEpoch: b.ExpectedEpoch, Desired: b.Desired, TrialID: b.TrialID, RunID: b.RunID, Phase: b.Phase, Principal: b.Principal, ApprovalRef: b.ApprovalRef, PlanSHA256: b.PlanSHA256, PreconditionsSHA256: strings.Repeat("0", 64), RequestedAt: b.RequestedAt, ValidUntil: b.ValidUntil}
		if request.Validate() != nil || v.ObservedAt.Before(b.RequestedAt) || v.ValidUntil.After(b.ValidUntil) {
			return nil, ErrInput
		}
		for _, status := range []string{v.AdmissionBlocked, v.SingleWriter, v.Fencing, v.WorkInventory, v.PodCoverage, v.WorkReconciled} {
			if status != "PASS" && status != "FAIL" && status != "NOT_VERIFIED" {
				return nil, ErrInput
			}
		}
		keys = p.ObservationKeys
	default:
		return nil, ErrInput
	}
	return signTyped(purpose, keyID, keyFile, phase, principal, keys, payload)
}
func SignApproval(p Policy, keyID, keyFile string, a Approval, resume *wc.ResumeRequest, restrict *wc.RestrictRequest, now time.Time) ([]byte, error) {
	if p.Validate() != nil || a.Version != Version || !validTime(a.NotBefore) || !validTime(a.ExpiresAt) || a.ExpiresAt.Sub(a.NotBefore) > time.Duration(p.ApprovalMaxSeconds)*time.Second {
		return nil, ErrInput
	}
	purpose := "approval"
	phase := ""
	if resume != nil && restrict == nil {
		r := *resume
		phase = r.Phase
		if r.Validate() != nil || a.ID == uuid.Nil || r.Scope != p.Target || a.Action != "resume" || a.ID.String() != r.ApprovalRef || a.RequestSHA256 != r.SHA256() || a.Principal != r.Principal || now.Before(r.RequestedAt) || !now.Before(r.ValidUntil) {
			return nil, ErrInput
		}
	} else if restrict != nil && resume == nil {
		r := *restrict
		phase = r.Phase
		purpose = "restriction-approval"
		if r.Scope != p.Target || a.Action != r.Action || wc.ValidateRestrictionApproval(r, wc.ResumeApproval{RequestSHA256: a.RequestSHA256, ID: a.ID, Principal: a.Principal, NotBefore: a.NotBefore, ExpiresAt: a.ExpiresAt}, now) != nil {
			return nil, ErrInput
		}
	} else {
		return nil, ErrInput
	}
	if now.Before(a.NotBefore) || !now.Before(a.ExpiresAt) || !a.ExpiresAt.After(a.NotBefore) {
		return nil, ErrInput
	}
	return signTyped(purpose, keyID, keyFile, phase, a.Principal, p.ApprovalKeys, a)
}
func signTyped(purpose, id, path, phase, principal string, keys []Key, payload any) ([]byte, error) {
	var selected *Key
	for n := range keys {
		k := &keys[n]
		if k.ID == id && contains(k.Phases, phase) && (principal == "" || k.Principal == principal) {
			selected = k
			break
		}
	}
	if selected == nil {
		return nil, ErrDenied
	}
	raw, e := ReadProtected(path)
	if e != nil {
		return nil, ErrDenied
	}
	defer clear(raw)
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "PRIVATE KEY" || len(rest) != 0 {
		return nil, ErrDenied
	}
	defer clear(block.Bytes)
	parsed, e := x509.ParsePKCS8PrivateKey(block.Bytes)
	if e != nil {
		return nil, ErrDenied
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(key) != ed25519.PrivateKeySize {
		return nil, ErrDenied
	}
	defer clear(key)
	pub, _ := base64.StdEncoding.DecodeString(selected.PublicKey)
	if !ed25519.PublicKey(pub).Equal(key.Public()) {
		return nil, ErrDenied
	}
	b, e := json.Marshal(payload)
	if e != nil || len(b) > MaxDocumentBytes {
		return nil, ErrInput
	}
	sig := ed25519.Sign(key, append([]byte(Version+"/"+purpose+"\n"), b...))
	result, e := json.Marshal(Signed{Version, id, b, base64.StdEncoding.EncodeToString(sig)})
	if e != nil || len(result) > MaxDocumentBytes {
		return nil, ErrInput
	}
	return result, nil
}
