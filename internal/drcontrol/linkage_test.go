package drcontrol

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

type restrictFake struct {
	record                   wc.RestrictRecord
	found, unknown           bool
	lookups, begins, applies int
	now                      time.Time
}

func (f *restrictFake) LookupRestriction(context.Context, wc.Scope, uuid.UUID) (wc.RestrictRecord, bool, error) {
	f.lookups++
	return f.record, f.found, nil
}
func (f *restrictFake) BeginRestriction(_ context.Context, r wc.RestrictRequest, a wc.ResumeApproval) (bool, error) {
	f.begins++
	f.record = wc.RestrictRecord{Request: r, Approval: a, Status: wc.ResumeIntent, CreatedAt: f.now}
	f.found = true
	return true, nil
}
func (f *restrictFake) ApplyRestriction(context.Context, wc.RestrictRequest) (wc.RestrictRecord, error) {
	f.applies++
	if f.unknown {
		return wc.RestrictRecord{}, errors.New("fixture response lost")
	}
	f.record.Status = wc.ResumeApplied
	f.record.AppliedAt = f.now
	return f.record, nil
}
func restrictionFixture(t *testing.T) (*fixture, RestrictInvocation, *restrictFake) {
	f := setup(t)
	f.p.Actors[0].Actions = append(f.p.Actors[0].Actions, "init", "pause", "inspect-restriction")
	r := wc.RestrictRequest{CommandID: uuid.New(), Action: "init", Scope: f.p.Target, TrialID: "trial", RunID: "run", Phase: "activate", Principal: "operator", ApprovalRef: uuid.NewString(), PlanSHA256: strings.Repeat("a", 64), RequestedAt: f.now.Add(-time.Second), ValidUntil: f.now.Add(20 * time.Second)}
	a := Approval{Version: Version, Action: r.Action, RequestSHA256: r.SHA256(), Principal: r.Principal, ID: uuid.MustParse(r.ApprovalRef), NotBefore: r.RequestedAt, ExpiresAt: r.ValidUntil}
	raw := signedFixture(t, a, "approver", "restriction-approval", f.ap)
	i := RestrictInvocation{Version: Version, Action: r.Action, Request: r, Approval: Reference{f.p.ArtifactBucket, f.p.ArtifactPrefix + "/restriction.json", "restriction-version", Hash(raw), int64(len(raw))}}
	f.art.values[i.Approval.VersionID] = raw
	return f, i, &restrictFake{now: f.now}
}
func TestRestrictionSignedInitPauseHistoricalAndUnknown(t *testing.T) {
	for _, action := range []string{"init", "pause"} {
		t.Run(action, func(t *testing.T) {
			f, i, s := restrictionFixture(t)
			if action == "pause" {
				i.Action = action
				i.Request.Action = action
				i.Request.Binding = wc.Binding{Scope: f.p.Target, Revision: 2, Epoch: 2, Mode: wc.Active, PodID: "pod", BootID: uuid.New()}
				a := Approval{Version: Version, Action: action, RequestSHA256: i.Request.SHA256(), Principal: i.Request.Principal, ID: uuid.MustParse(i.Request.ApprovalRef), NotBefore: i.Request.RequestedAt, ExpiresAt: i.Request.ValidUntil}
				raw := signedFixture(t, a, "approver", "restriction-approval", f.ap)
				i.Approval.SHA256 = Hash(raw)
				i.Approval.Size = int64(len(raw))
				f.art.values[i.Approval.VersionID] = raw
			}
			approval := Approval{Version: Version, Action: action, RequestSHA256: i.Request.SHA256(), Principal: i.Request.Principal, ID: uuid.MustParse(i.Request.ApprovalRef), NotBefore: i.Request.RequestedAt, ExpiresAt: i.Request.ValidUntil}
			raw, err := SignApproval(f.p, "approver", privateFixture(t, f.ap), approval, nil, &i.Request, f.now)
			if err != nil {
				t.Fatal("restriction producer", err)
			}
			i.Approval.SHA256, i.Approval.Size = Hash(raw), int64(len(raw))
			f.art.values[i.Approval.VersionID] = raw
			call := func() (wc.RestrictRecord, error) {
				return ExecuteRestriction(t.Context(), f.p, i, f.id, f.art, s, func() time.Time { return f.now })
			}
			v, e := call()
			if e != nil || v.Status != wc.ResumeApplied || s.applies != 1 {
				t.Fatal(v, e)
			}
			f.now = f.now.Add(time.Hour)
			f.art.values = nil
			if _, e = call(); e != nil || s.applies != 1 {
				t.Fatal("historical replay mutated", e)
			}
			i.Request.RunID = "different"
			if _, e = call(); !errors.Is(e, wc.ErrConflict) {
				t.Fatal("CommandID payload conflict", e)
			}
		})
	}
	f, i, s := restrictionFixture(t)
	s.unknown = true
	call := func() (wc.RestrictRecord, error) {
		return ExecuteRestriction(t.Context(), f.p, i, f.id, f.art, s, func() time.Time { return f.now })
	}
	if _, e := call(); !errors.Is(e, wc.ErrUnknown) {
		t.Fatal(e)
	}
	s.unknown = false
	if _, e := call(); !errors.Is(e, wc.ErrUnknown) || s.applies != 1 {
		t.Fatal("UNKNOWN reapplied", e, s.applies)
	}
	i.Action = "inspect-restriction"
	f.now = f.now.Add(time.Hour)
	f.art.values = nil
	if _, e := call(); !errors.Is(e, wc.ErrUnknown) || s.applies != 1 {
		t.Fatal("inspect changed unknown", e)
	}
}
func TestRestrictionFailuresCannotBegin(t *testing.T) {
	for _, kind := range []string{"disabled", "actor", "action", "target", "hash", "signature", "purpose", "expired", "future", "phase", "key-principal", "approval-id", "request-conflict"} {
		t.Run(kind, func(t *testing.T) {
			f, i, s := restrictionFixture(t)
			switch kind {
			case "disabled":
				f.p.EnableExecution = false
			case "actor":
				f.id.id.ARN += "-other"
			case "action":
				f.p.Actors[0].Actions = []string{"inspect-restriction"}
			case "target":
				i.Request.Scope.Cluster = "other"
			case "hash":
				i.Approval.SHA256 = strings.Repeat("0", 64)
			case "signature":
				f.art.values[i.Approval.VersionID][20] ^= 1
			case "purpose":
				f.art.values[i.Approval.VersionID] = signedFixture(t, f.approval, "approver", "approval", f.ap)
			case "expired":
				f.now = i.Request.ValidUntil
			case "future":
				f.now = i.Request.RequestedAt.Add(-time.Second)
			case "phase":
				f.p.ApprovalKeys[0].Phases = []string{"other"}
			case "key-principal":
				f.p.ApprovalKeys[0].Principal = "other"
			case "approval-id":
				i.Request.ApprovalRef = uuid.NewString()
			case "request-conflict":
				i.Request.RunID = "different"
			}
			if _, e := ExecuteRestriction(t.Context(), f.p, i, f.id, f.art, s, func() time.Time { return f.now }); e == nil || s.begins+s.applies != 0 {
				t.Fatal("invalid restriction reached INTENT", e)
			}
		})
	}
}
func privateFixture(t *testing.T, key ed25519.PrivateKey) string {
	t.Helper()
	der, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "key.pem")
	if os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600) != nil {
		t.Fatal("key fixture")
	}
	protectTestFile(t, path)
	return path
}
func TestProtectedSigningRoundtripAndSeparation(t *testing.T) {
	f := setup(t)
	path := privateFixture(t, f.ap)
	raw, e := SignApproval(f.p, "approver", path, f.approval, &f.i.Request, nil, f.now)
	if e != nil {
		t.Fatal(e)
	}
	f.i.Approval.SHA256 = Hash(raw)
	f.i.Approval.Size = int64(len(raw))
	f.art.values[f.i.Approval.VersionID] = raw
	if _, e = f.execute(); e != nil {
		t.Fatal("producer/consumer approval", e)
	}
	obsPath := privateFixture(t, f.ob)
	raw, e = SignDocument(f.p, "observation", "observer", obsPath, f.observation, f.now)
	if e != nil {
		t.Fatal(e)
	}
	var envelope Signed
	if Strict(raw, &envelope) != nil {
		t.Fatal("invalid signed document")
	}
	f.art.values["ob-version"] = raw
	f.i.Evidence.SHA256 = Hash(raw)
	f.i.Evidence.Size = int64(len(raw))
	f.i.Request.PreconditionsSHA256 = Hash(raw)
	v, e := NewVerifier(f.p, f.i, f.art, func() time.Time { return f.now })
	if e != nil {
		t.Fatal(e)
	}
	if _, e = v.RecheckResume(t.Context(), f.i.Request); e != nil {
		t.Fatal("producer/consumer observation", e)
	}
	for _, tc := range []struct {
		name, purpose, id, path string
		payload                 any
	}{{"purpose", "approval", "observer", obsPath, f.observation}, {"wrong-public", "observation", "observer", path, f.observation}, {"wrong-id", "observation", "missing", obsPath, f.observation}, {"observation-as-approval", "observation", "observer", obsPath, f.approval}} {
		t.Run(tc.name, func(t *testing.T) {
			if _, e := SignDocument(f.p, tc.purpose, tc.id, tc.path, tc.payload, f.now); e == nil {
				t.Fatal("signing authority bypass")
			}
		})
	}
	bad := f.observation
	bad.Source = "SIMULATION"
	if _, e = SignDocument(f.p, "observation", "observer", obsPath, bad, f.now); e == nil {
		t.Fatal("simulation signed as live")
	}
	if _, e = SignDocument(f.p, "observation", "observer", obsPath, f.observation, f.observation.ValidUntil); e == nil {
		t.Fatal("expired signed")
	}
	f.p.ObservationKeys[0].Phases = []string{"other"}
	if _, e = SignDocument(f.p, "observation", "observer", obsPath, f.observation, f.now); e == nil {
		t.Fatal("wrong phase key")
	}
	if _, e = ReadProtected(filepath.Join(t.TempDir(), "absent")); e == nil {
		t.Fatal("missing key")
	}
	if _, e = ReadProtected("relative-key"); e == nil {
		t.Fatal("relative key")
	}
}

type podFake struct {
	snapshot PodSnapshot
	process  wc.ProcessReadback
	err      error
	change   bool
	calls    int
	now      time.Time
}

func (f *podFake) Snapshot(context.Context) (PodSnapshot, error) {
	f.calls++
	s := f.snapshot
	if f.change && f.calls > 1 {
		s.Pods = append([]PodIdentity(nil), s.Pods...)
		s.Pods[0].ResourceVersion = "changed"
	}
	return s, f.err
}
func (f *podFake) Process(context.Context, string) (wc.ProcessReadback, string, error) {
	b, _ := json.Marshal(f.process)
	return f.process, Hash(b), f.err
}

type workFake struct {
	v   WorkSnapshot
	err error
}

func (f workFake) ReadWork(context.Context, wc.Scope) (WorkSnapshot, error) { return f.v, f.err }
func collectorFixture(t *testing.T) (*fixture, CollectionPlan, *podFake, *workFake) {
	f := setup(t)
	plan := CollectionPlan{CAFile: filepath.Join(t.TempDir(), "ca.pem"), CASHA256: strings.Repeat("d", 64), KubectlExecutable: filepath.Join(t.TempDir(), "kubectl.exe"), Kubeconfig: filepath.Join(t.TempDir(), "config"), Context: "pinned", APIServer: "https://cluster.example.test", KubeSystemUID: "system-uid", Namespace: "labbit", Pods: []PodTarget{{"backend-1", "pod-uid", "backend", "registry.example/backend@sha256:" + strings.Repeat("b", 64)}}}
	f.p.CollectionPlanSHA256 = plan.SHA256()
	pods := &podFake{snapshot: PodSnapshot{Pods: []PodIdentity{{Name: "backend-1", UID: "pod-uid", ResourceVersion: "12", Ready: true, Containers: []ContainerIdentity{{"backend", plan.Pods[0].ImageID, 0, true}}}}, SHA256: strings.Repeat("c", 64)}, process: wc.ProcessReadback{Binding: wc.Binding{Scope: f.p.Target, Revision: 1, Epoch: 1, Mode: wc.PauseNew, PodID: "backend-1", BootID: uuid.New()}, AdmissionClosed: true, MaintenanceState: "closed", Active: map[string]int{"http": 0, "worker": 0, "reconciliation": 0}, ObservedAt: f.now}}
	w := &workFake{v: WorkSnapshot{Database: f.p.Database.Name, User: f.p.Database.User, Writer: true, State: wc.State{Scope: f.p.Target, Revision: 1, Epoch: 1, Mode: wc.PauseNew}, ObservedAt: f.now}}
	return f, plan, pods, w
}
func TestCollectorScopedPassMissingExternalSafetyStillHold(t *testing.T) {
	f, p, k, w := collectorFixture(t)
	v, e := Collect(t.Context(), f.p, f.i, p, k, w, f.art, nil, func() time.Time { return f.now })
	if e != nil {
		t.Fatal(e)
	}
	o := v.Observation
	if o.AdmissionBlocked != "PASS" || o.PodCoverage != "PASS" || o.WorkInventory != "PASS" || o.SingleWriter != "NOT_VERIFIED" || o.Fencing != "NOT_VERIFIED" || o.WorkReconciled != "NOT_VERIFIED" || v.ExecutionGate != "HOLD" || v.RecoveryAuthorized || v.CloudVerified {
		t.Fatal(v)
	}
	// Real verifier refuses the resulting signed observation despite local PASS.
	raw := signedFixture(t, o, "observer", "observation", f.ob)
	f.i.Evidence.SHA256 = Hash(raw)
	f.i.Evidence.Size = int64(len(raw))
	f.i.Request.PreconditionsSHA256 = Hash(raw)
	f.art.values["ob-version"] = raw
	verifier, _ := NewVerifier(f.p, f.i, f.art, func() time.Time { return f.now })
	if _, e = verifier.RecheckResume(t.Context(), f.i.Request); !errors.Is(e, wc.ErrHold) {
		t.Fatal("missing safety allowed resume", e)
	}
}
func TestCollectorUnsafeAndUnobservableReadbacks(t *testing.T) {
	for _, kind := range []string{"missing-pod", "extra-pod", "pod-uid", "restart", "image", "boot", "scope", "epoch", "file", "active-http", "active-worker", "missing-count", "unverified", "sql-failure", "db-target", "future", "plan-tamper"} {
		t.Run(kind, func(t *testing.T) {
			f, p, k, w := collectorFixture(t)
			switch kind {
			case "missing-pod":
				k.snapshot.Pods = nil
			case "extra-pod":
				k.snapshot.Pods = append(k.snapshot.Pods, PodIdentity{Name: "rogue", UID: "rogue"})
			case "pod-uid":
				k.snapshot.Pods[0].UID = "new"
			case "restart":
				k.change = true
			case "image":
				k.snapshot.Pods[0].Containers[0].ImageID = "wrong"
			case "boot":
				k.process.Binding.BootID = uuid.Nil
			case "scope":
				k.process.Binding.Scope.Cluster = "other"
			case "epoch":
				k.process.Binding.Epoch++
			case "file":
				k.process.AdmissionClosed = false
			case "active-http":
				k.process.Active["http"] = 1
			case "active-worker":
				k.process.Control.InFlight = 1
			case "missing-count":
				delete(k.process.Active, "reconciliation")
			case "unverified":
				k.process.Control.Unverified = true
			case "sql-failure":
				w.err = context.DeadlineExceeded
			case "db-target":
				w.v.Database = "other"
			case "future":
				k.process.ObservedAt = f.now.Add(time.Second)
			case "plan-tamper":
				p.Context = "other"
			}
			v, e := Collect(t.Context(), f.p, f.i, p, k, w, f.art, nil, func() time.Time { return f.now })
			if e == nil && v.Observation.AdmissionBlocked == "PASS" && v.Observation.PodCoverage == "PASS" && v.Observation.WorkInventory == "PASS" {
				t.Fatal("unsafe readback accepted", kind, v)
			}
		})
	}
}

func TestCollectorDistinguishesUnsafeFromMissingLocalEvidence(t *testing.T) {
	for _, kind := range []string{"missing-count", "negative-count", "invalid-file", "inconsistent-closed", "open", "positive-count", "unknown-positive-count", "in-flight"} {
		t.Run(kind, func(t *testing.T) {
			f, p, k, w := collectorFixture(t)
			want := "NOT_VERIFIED"
			switch kind {
			case "missing-count":
				delete(k.process.Active, "reconciliation")
			case "negative-count":
				k.process.Active["worker"] = -1
			case "invalid-file":
				k.process.MaintenanceState = "NOT_VERIFIED"
				k.process.AdmissionClosed = false
			case "inconsistent-closed":
				k.process.AdmissionClosed = false
			case "open":
				k.process.MaintenanceState = "open"
				k.process.AdmissionClosed = false
				want = "FAIL"
			case "positive-count":
				k.process.Active["worker"] = 1
				want = "FAIL"
			case "unknown-positive-count":
				k.process.Active["other"] = 1
				want = "FAIL"
			case "in-flight":
				k.process.Control.InFlight = 1
				delete(k.process.Active, "worker")
				want = "FAIL"
			}
			v, e := Collect(t.Context(), f.p, f.i, p, k, w, f.art, nil, func() time.Time { return f.now })
			if e != nil || v.Observation.AdmissionBlocked != want || v.Observation.PodCoverage != "PASS" {
				t.Fatalf("want %s, got %+v: %v", want, v, e)
			}
		})
	}
}
