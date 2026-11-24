// One-shot signing/collection companion. Preview is default. It neither stores
// S3 objects nor mutates Worker state, maintenance, Kubernetes, or AWS.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ktcloud4-SL/labbit-app/internal/drcontrol"
	"io"
	"os"
	"strings"
	"time"
)

type output struct {
	Status, Reason, ExecutionGate     string
	RecoveryAuthorized, CloudVerified bool
	Report                            *drcontrol.CollectionReport `json:",omitempty"`
	Signed                            json.RawMessage             `json:",omitempty"`
}

func read(path string) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, drcontrol.ErrInput
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, drcontrol.MaxDocumentBytes+1))
	if e != nil || len(b) > drcontrol.MaxDocumentBytes {
		return nil, drcontrol.ErrInput
	}
	return b, nil
}
func run(args []string, out io.Writer) int {
	f := flag.NewFlagSet("labbit-dr-evidence", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	mode := f.String("mode", "", "approve or collect")
	pf := f.String("policy", "", "protected Policy")
	in := f.String("input", "", "immutable invocation")
	planFile := f.String("plan", "", "protected collection Plan")
	dsn := f.String("dsn-file", "", "protected DSN")
	key := f.String("key-file", "", "protected PKCS8 Ed25519 key")
	kid := f.String("key-id", "", "Policy public KeyID")
	safetyFile := f.String("safety-reference", "", "optional immutable attestation Reference")
	execute := f.Bool("execute", false, "dual opt-in, default no external IO or key loading")
	r := output{Status: "NOT_VERIFIED", Reason: "INPUT_NOT_VERIFIED", ExecutionGate: "HOLD"}
	finish := func(c int) int { _ = json.NewEncoder(out).Encode(r); return c }
	if f.Parse(args) != nil || f.NArg() != 0 || (*mode != "approve" && *mode != "collect") {
		return finish(2)
	}
	pb, e := read(*pf)
	if e != nil {
		return finish(2)
	}
	ib, e := read(*in)
	if e != nil {
		return finish(2)
	}
	var p drcontrol.Policy
	var head struct{ Action string }
	if drcontrol.Strict(pb, &p) != nil || p.Validate() != nil || json.Unmarshal(ib, &head) != nil {
		return finish(2)
	}
	restricted := head.Action == "init" || head.Action == "pause"
	var i drcontrol.Invocation
	var ri drcontrol.RestrictInvocation
	if restricted {
		if *mode != "approve" || drcontrol.Strict(ib, &ri) != nil || p.CheckRestriction(ri) != nil {
			return finish(2)
		}
	} else {
		if drcontrol.Strict(ib, &i) != nil || p.CheckInvocation(i) != nil || i.Action != "resume" {
			return finish(2)
		}
	}
	var plan drcontrol.CollectionPlan
	if *mode == "collect" {
		b, e := read(*planFile)
		if e != nil || drcontrol.Strict(b, &plan) != nil || plan.Validate() != nil || p.CollectionPlanSHA256 != plan.SHA256() {
			return finish(2)
		}
	}
	if !*execute {
		r.Status = "PREVIEW_ONLY"
		r.Reason = "NO_EXTERNAL_IO_NO_KEY_READ"
		return finish(0)
	}
	if !p.EnableExecution {
		r.Reason = "EXECUTION_DISABLED"
		return finish(2)
	}
	protected, e := drcontrol.ReadProtected(*pf)
	if e != nil || string(protected) != string(pb) {
		r.Reason = "POLICY_NOT_VERIFIED"
		return finish(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cloud, e := drcontrol.NewAWSCLI(p, drcontrol.CommandRunner{})
	if e != nil {
		return finish(2)
	}
	now := func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
	if *mode == "approve" {
		if restricted {
			e = drcontrol.AuthenticateRestriction(ctx, p, ri, cloud)
		} else {
			e = drcontrol.Authenticate(ctx, p, i, cloud)
		}
		if e != nil {
			r.Reason = "IDENTITY_NOT_VERIFIED"
			return finish(2)
		}
		if restricted {
			q := ri.Request
			a := drcontrol.Approval{Version: drcontrol.Version, Action: q.Action, RequestSHA256: q.SHA256(), Principal: q.Principal, NotBefore: q.RequestedAt, ExpiresAt: q.ValidUntil}
			a.ID, _ = uuid.Parse(q.ApprovalRef)
			r.Signed, e = drcontrol.SignApproval(p, *kid, *key, a, nil, &q, now())
		} else {
			q := i.Request
			a := drcontrol.Approval{Version: drcontrol.Version, Action: "resume", RequestSHA256: q.SHA256(), Principal: q.Principal, NotBefore: q.RequestedAt, ExpiresAt: q.ValidUntil}
			a.ID, _ = uuid.Parse(q.ApprovalRef)
			r.Signed, e = drcontrol.SignApproval(p, *kid, *key, a, &q, nil, now())
		}
		if e != nil {
			r.Reason = "SIGNING_NOT_VERIFIED"
			return finish(2)
		}
		r.Status = "SIGNED_APPROVAL"
		r.Reason = "LOCAL_DOCUMENT_ONLY_NOT_EXECUTED"
		return finish(0)
	}
	id, e := cloud.Identity(ctx)
	allowed := false
	for _, actor := range p.Actors {
		for _, a := range actor.Actions {
			if a == "collect" && actor.ARN == id.ARN && id.Account == p.AccountID {
				for _, k := range p.ObservationKeys {
					if k.ID == *kid && k.Principal == actor.Principal {
						allowed = true
					}
				}
			}
		}
	}
	if e != nil || !allowed {
		r.Reason = "IDENTITY_NOT_VERIFIED"
		return finish(2)
	}
	plb, e := drcontrol.ReadProtected(*planFile)
	if e != nil || drcontrol.Strict(plb, &plan) != nil || plan.SHA256() != p.CollectionPlanSHA256 {
		r.Reason = "PLAN_NOT_VERIFIED"
		return finish(2)
	}
	kc, e := drcontrol.ReadProtected(plan.Kubeconfig)
	if e != nil {
		r.Reason = "KUBECONFIG_NOT_VERIFIED"
		return finish(2)
	}
	clear(kc)
	ca, e := drcontrol.ReadProtected(plan.CAFile)
	if e != nil || drcontrol.Hash(ca) != plan.CASHA256 {
		r.Reason = "CLUSTER_CA_NOT_VERIFIED"
		return finish(2)
	}
	if cloud.VerifyDatabase(ctx) != nil {
		r.Reason = "DATABASE_TARGET_NOT_VERIFIED"
		return finish(2)
	}
	raw, e := drcontrol.ReadProtected(*dsn)
	if e != nil {
		return finish(2)
	}
	config, e := drcontrol.DatabaseConfig(p, strings.TrimSpace(string(raw)))
	clear(raw)
	if e != nil {
		return finish(2)
	}
	pool, e := pgxpool.NewWithConfig(ctx, config)
	if e != nil {
		return finish(2)
	}
	defer pool.Close()
	if drcontrol.VerifySQL(ctx, pool, p.Database) != nil {
		return finish(2)
	}
	kube, e := drcontrol.NewKubeCLI(plan, drcontrol.CommandRunner{})
	if e != nil {
		return finish(2)
	}
	var ref *drcontrol.Reference
	if *safetyFile != "" {
		b, e := read(*safetyFile)
		if e != nil {
			return finish(2)
		}
		var v drcontrol.Reference
		if drcontrol.Strict(b, &v) != nil || p.CheckReference(v) != nil {
			return finish(2)
		}
		ref = &v
	}
	report, e := drcontrol.Collect(ctx, p, i, plan, kube, drcontrol.SQLWorkReader{Pool: pool}, cloud, ref, now)
	if e != nil {
		r.Reason = "COLLECTION_NOT_VERIFIED"
		return finish(2)
	}
	r.Report = &report
	r.Signed, e = drcontrol.SignDocument(p, "observation", *kid, *key, report.Observation, now())
	if e != nil {
		r.Reason = "SIGNING_NOT_VERIFIED"
		return finish(2)
	}
	r.Status = "SIGNED_OBSERVATION"
	r.Reason = "SCOPED_READBACK_NOT_DR_APPROVAL"
	return finish(0)
}
func main() { os.Exit(run(os.Args[1:], os.Stdout)) }
