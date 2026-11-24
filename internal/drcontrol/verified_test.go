package drcontrol

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

type identityFake struct {
	id    Identity
	err   error
	calls int
}

func (f *identityFake) Identity(context.Context) (Identity, error) { f.calls++; return f.id, f.err }

type artifactsFake struct {
	values map[string][]byte
	calls  int
}

func (f *artifactsFake) Read(_ context.Context, r Reference) ([]byte, error) {
	f.calls++
	return f.values[r.VersionID], nil
}

type storeFake struct {
	record                   wc.ResumeRecord
	found                    bool
	lookups, begins, applies int
	unknown                  bool
	now                      time.Time
}

func (f *storeFake) LookupResume(context.Context, wc.Scope, uuid.UUID) (wc.ResumeRecord, bool, error) {
	f.lookups++
	return f.record, f.found, nil
}
func (f *storeFake) BeginResume(_ context.Context, r wc.ResumeRequest, p wc.ResumeProof) (bool, error) {
	f.begins++
	f.record = wc.ResumeRecord{Request: r, Proof: p, Status: wc.ResumeIntent, CreatedAt: f.now}
	f.found = true
	return true, nil
}
func (f *storeFake) ApplyResume(context.Context, wc.ResumeRequest) (wc.ResumeRecord, error) {
	f.applies++
	if f.unknown {
		return wc.ResumeRecord{}, errors.New("fixture ACK lost")
	}
	f.record.Status = wc.ResumeApplied
	f.record.AppliedAt = f.now
	return f.record, nil
}

type fixture struct {
	p           Policy
	i           Invocation
	id          *identityFake
	art         *artifactsFake
	s           *storeFake
	now         time.Time
	approval    Approval
	observation Observation
	ap, ob      ed25519.PrivateKey
}

func signedFixture(t *testing.T, payload any, id, purpose string, key ed25519.PrivateKey) []byte {
	t.Helper()
	b, e := json.Marshal(payload)
	if e != nil {
		t.Fatal(e)
	}
	sig := ed25519.Sign(key, append([]byte(Version+"/"+purpose+"\n"), b...))
	b, e = json.Marshal(Signed{Version, id, b, base64.StdEncoding.EncodeToString(sig)})
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func (f *fixture) refresh(t *testing.T) {
	ob := signedFixture(t, f.observation, "observer", "observation", f.ob)
	f.i.Evidence = Reference{"fixture-bucket", "control/observation.json", "ob-version", Hash(ob), int64(len(ob))}
	f.i.Request.PreconditionsSHA256 = Hash(ob)
	f.approval.RequestSHA256 = f.i.Request.SHA256()
	ap := signedFixture(t, f.approval, "approver", "approval", f.ap)
	f.i.Approval = Reference{"fixture-bucket", "control/approval.json", "ap-version", Hash(ap), int64(len(ap))}
	f.art.values = map[string][]byte{"ap-version": ap, "ob-version": ob}
}
func setup(t *testing.T) *fixture {
	t.Helper()
	now := time.Date(2026, 11, 21, 0, 0, 1, 0, time.UTC)
	pub1, key1, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	pub2, key2, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	scope := wc.Scope{ID: "trial-scope", Region: "ap-northeast-3", Cluster: "fixture-cluster"}
	actor := "arn:aws:iam::123456789012:user/fixture-operator"
	aid := uuid.New()
	f := &fixture{p: Policy{Version: Version, AccountID: "123456789012", Region: scope.Region, AWSExecutable: filepath.Join(t.TempDir(), "aws.exe"), AWSProfile: "fixture", Target: scope, Actors: []Actor{{actor, "operator", []string{"resume", "inspect"}}}, ApprovalKeys: []Key{{"approver", "operator", base64.StdEncoding.EncodeToString(pub1), []string{"activate"}}}, ObservationKeys: []Key{{"observer", "observer", base64.StdEncoding.EncodeToString(pub2), []string{"activate"}}}, ArtifactBucket: "fixture-bucket", ArtifactPrefix: "control", ApprovalMaxSeconds: 60, EvidenceMaxSeconds: 30, Database: Database{Region: scope.Region, ARN: "arn:aws:rds:ap-northeast-3:123456789012:db:fixture", ResourceID: "db-fixture", Host: "fixture.example.test", Port: 5432, Name: "labbit", User: "fixture"}, EnableExecution: true}, now: now, ap: key1, ob: key2, id: &identityFake{id: Identity{"123456789012", actor}}, art: &artifactsFake{}, s: &storeFake{now: now}}
	r := wc.ResumeRequest{CommandID: uuid.New(), Scope: scope, ExpectedRevision: 1, ExpectedEpoch: 1, Desired: wc.Active, TrialID: "trial", RunID: "run", Phase: "activate", Principal: "operator", ApprovalRef: aid.String(), PlanSHA256: strings.Repeat("a", 64), RequestedAt: now.Add(-time.Second), ValidUntil: now.Add(20 * time.Second)}
	f.i = Invocation{Version: Version, Action: "resume", Request: r}
	f.approval = Approval{Version: Version, Action: "resume", Principal: "operator", ID: aid, NotBefore: now.Add(-time.Second), ExpiresAt: now.Add(20 * time.Second)}
	f.observation = Observation{Version: Version, Source: "LIVE_READBACK", Binding: Bind(r), ObservedAt: now, ValidUntil: now.Add(10 * time.Second), AdmissionBlocked: "PASS", SingleWriter: "PASS", Fencing: "PASS", WorkInventory: "PASS", PodCoverage: "PASS", WorkReconciled: "PASS"}
	f.refresh(t)
	return f
}
func (f *fixture) execute() (wc.ResumeRecord, error) {
	return Execute(context.Background(), f.p, f.i, f.id, f.art, f.s, func() time.Time { return f.now })
}
func TestVerifiedResumeAndAuthenticatedHistoricalReplay(t *testing.T) {
	f := setup(t)
	r, e := f.execute()
	if e != nil || r.Status != wc.ResumeApplied || f.s.begins != 1 || f.s.applies != 1 {
		t.Fatalf("resume: %v %s", e, r.Status)
	}
	// Expired documents must not mutate again; an authorized historical receipt
	// remains readable and never changes a process's immutable binding.
	f.now = f.now.Add(time.Hour)
	f.art.values = nil
	r, e = f.execute()
	if e != nil || r.Status != wc.ResumeApplied || f.s.applies != 1 || f.art.calls != 2 || f.id.calls != 2 {
		t.Fatalf("historical replay: %v", e)
	}
	f.id.id.ARN = "arn:aws:iam::123456789012:user/foreign"
	before := f.s.lookups
	if _, e = f.execute(); e == nil || f.s.lookups != before {
		t.Fatal("unauthorized actor reached historical journal")
	}
}
func TestAuthenticationFailsBeforeJournal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*fixture)
	}{
		{"account", func(f *fixture) { f.id.id.Account = "999999999999" }}, {"arn", func(f *fixture) { f.id.id.ARN += "-other" }}, {"principal", func(f *fixture) { f.i.Request.Principal = "other" }}, {"scope", func(f *fixture) { f.i.Request.Scope.Cluster = "other" }}, {"action", func(f *fixture) { f.p.Actors[0].Actions = []string{"inspect"} }}, {"disabled", func(f *fixture) { f.p.EnableExecution = false }}, {"identity timeout", func(f *fixture) { f.id.err = context.DeadlineExceeded }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			tc.change(f)
			if _, e := f.execute(); e == nil || f.s.lookups+f.s.begins+f.s.applies != 0 {
				t.Fatal("denied invocation reached store")
			}
		})
	}
}
func TestUnverifiedProofCannotCreateIntent(t *testing.T) {
	cases := []struct {
		name   string
		change func(*fixture)
	}{
		{"WorkReconciled FAIL", func(f *fixture) { f.observation.WorkReconciled = "FAIL" }},
		{"simulation", func(f *fixture) { f.observation.Source = "SIMULATION" }}, {"FAIL", func(f *fixture) { f.observation.Fencing = "FAIL" }}, {"NOT_VERIFIED", func(f *fixture) { f.observation.WorkReconciled = "NOT_VERIFIED" }}, {"missing pod coverage", func(f *fixture) { f.observation.PodCoverage = "" }}, {"target", func(f *fixture) { f.observation.Binding.Scope.Cluster = "other" }}, {"run", func(f *fixture) { f.observation.Binding.RunID = "other" }}, {"plan", func(f *fixture) { f.observation.Binding.PlanSHA256 = strings.Repeat("b", 64) }}, {"epoch", func(f *fixture) { f.observation.Binding.ExpectedEpoch++ }}, {"future observation", func(f *fixture) { f.observation.ObservedAt = f.now.Add(time.Second) }}, {"expired observation", func(f *fixture) { f.observation.ValidUntil = f.now }}, {"observation ttl", func(f *fixture) { f.observation.ValidUntil = f.now.Add(31 * time.Second) }}, {"expired approval", func(f *fixture) { f.approval.ExpiresAt = f.now }}, {"future approval", func(f *fixture) { f.approval.NotBefore = f.now.Add(time.Second) }}, {"approval ttl", func(f *fixture) { f.approval.ExpiresAt = f.now.Add(time.Hour) }}, {"approval principal", func(f *fixture) { f.approval.Principal = "other" }}, {"approval id", func(f *fixture) { f.approval.ID = uuid.New() }}, {"phase key", func(f *fixture) { f.p.ObservationKeys[0].Phases = []string{"failback"} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			tc.change(f)
			f.refresh(t)
			if _, e := f.execute(); e == nil || f.s.begins+f.s.applies != 0 {
				t.Fatal("unverified proof reached INTENT")
			}
		})
	}
}
func TestImmutableDocumentsAndPurposeSeparation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*fixture)
	}{
		{"hash tamper", func(f *fixture) { f.art.values["ob-version"][0] = '[' }}, {"missing version", func(f *fixture) { f.i.Evidence.VersionID = "null" }}, {"other prefix", func(f *fixture) { f.i.Approval.Key = "control-other/a" }}, {"oversize", func(f *fixture) { f.i.Approval.Size = MaxDocumentBytes + 1 }}, {"wrong size", func(f *fixture) { f.i.Approval.Size++ }}, {"shared signing key", func(f *fixture) { f.p.ObservationKeys[0].PublicKey = f.p.ApprovalKeys[0].PublicKey }}, {"wrong purpose", func(f *fixture) {
			b := signedFixture(t, f.approval, "approver", "observation", f.ap)
			f.art.values["ap-version"] = b
			f.i.Approval.SHA256 = Hash(b)
			f.i.Approval.Size = int64(len(b))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			tc.change(f)
			if _, e := f.execute(); e == nil || f.s.begins != 0 {
				t.Fatal("tampered document accepted")
			}
		})
	}
}
func TestUnknownNeverReapplied(t *testing.T) {
	f := setup(t)
	f.s.unknown = true
	if _, e := f.execute(); !errors.Is(e, wc.ErrUnknown) {
		t.Fatal(e)
	}
	before := f.art.calls
	if _, e := f.execute(); !errors.Is(e, wc.ErrUnknown) || f.s.applies != 1 || f.s.begins != 1 || f.art.calls != before {
		t.Fatal("UNKNOWN automatically retried", e)
	}
}
func TestInspectRequiresIdentityButNoArtifacts(t *testing.T) {
	f := setup(t)
	if _, e := f.execute(); e != nil {
		t.Fatal(e)
	}
	f.i.Action = "inspect"
	f.i.Approval = Reference{}
	f.i.Evidence = Reference{}
	f.art.values = nil
	if _, e := f.execute(); e != nil || f.s.applies != 1 || f.art.calls != 2 {
		t.Fatal(e)
	}
}
func TestStrictJSON(t *testing.T) {
	for _, s := range []string{`{"Version":"a","Version":"b"}`, `{"Version":"a","version":"b"}`, `{"Target":{"ID":"a","id":"b"}}`, `{"unknown":1}`, `{} {}`, `{"Version":"a"} trailing`, `{"Version":"a","Target":{"ID":"a","ID":"b"}}`, string([]byte{'{', '"', 'V', 'e', 'r', 's', 'i', 'o', 'n', '"', ':', '"', 0xff, '"', '}'})} {
		var p Policy
		if Strict([]byte(s), &p) == nil {
			t.Fatalf("accepted ambiguous JSON %q", s)
		}
	}
}
func TestKeyAliasesCannotMixApprovalAndObservation(t *testing.T) {
	for _, sep := range []string{"\n", "\r\n"} {
		f := setup(t)
		key := f.p.ApprovalKeys[0].PublicKey
		f.p.ObservationKeys[0].PublicKey = key[:8] + sep + key[8:]
		if f.p.Validate() == nil {
			t.Fatal("same decoded key allowed for both purposes")
		}
	}
	var s Signed
	if Strict([]byte(`{"KeyID":"a","keyid":"b"}`), &s) == nil {
		t.Fatal("ambiguous signature key accepted")
	}
	for _, raw := range []string{`{"Signature":"a","ſignature":"b"}`, `{"KeyID":"a","KeyID":"b"}`} {
		if Strict([]byte(raw), &s) == nil {
			t.Fatal("Unicode field alias accepted")
		}
	}
	f := setup(t)
	key := f.p.ApprovalKeys[0].PublicKey
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	index := strings.IndexByte(alphabet, key[len(key)-2])
	f.p.ObservationKeys[0].PublicKey = key[:len(key)-2] + string(alphabet[index^1]) + "="
	if f.p.Validate() == nil {
		t.Fatal("noncanonical base64 pad bits accepted")
	}
}
func TestDatabaseConfigurationNeverFallsBack(t *testing.T) {
	f := setup(t)
	good := "postgres://fixture:dummy@fixture.example.test:5432/labbit?sslmode=verify-full"
	if _, e := DatabaseConfig(f.p, good); e != nil {
		t.Fatal(e)
	}
	for _, dsn := range []string{strings.Replace(good, "verify-full", "require", 1), strings.Replace(good, "verify-full", "disable", 1), strings.Replace(good, "fixture.example.test", "other.example.test", 1), good + "&options=-csearch_path%3Devil", strings.Replace(good, "/labbit?", "/other?", 1), strings.Replace(good, "fixture:dummy", "foreign:dummy", 1)} {
		if _, e := DatabaseConfig(f.p, dsn); e == nil {
			t.Fatal("unsafe database config accepted")
		}
	}
}
