package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/drcontrol"
	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEvidencePreviewAndDisabledExecutionNeverReadKeyOrConnect(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "must-not-execute")
	scope := wc.Scope{ID: "fixture", Region: "ap-northeast-3", Cluster: "fixture"}
	now := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	// Executable and DB endpoint intentionally do not exist. A preview must still
	// succeed, and explicit execution remains denied by the protected policy.
	p := drcontrol.Policy{Version: drcontrol.Version, AccountID: "123456789012", Region: scope.Region, AWSExecutable: marker, AWSProfile: "fixture", Target: scope, Actors: []drcontrol.Actor{{ARN: "arn:aws:iam::123456789012:user/fixture", Principal: "fixture", Actions: []string{"resume", "init", "pause", "collect"}}}, ApprovalKeys: []drcontrol.Key{{ID: "approval", Principal: "fixture", PublicKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)), Phases: []string{"activate"}}}, ObservationKeys: []drcontrol.Key{{ID: "observation", Principal: "fixture", PublicKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32)), Phases: []string{"activate"}}}, ArtifactBucket: "fixture", ArtifactPrefix: "control", ApprovalMaxSeconds: 60, EvidenceMaxSeconds: 30, Database: drcontrol.Database{Region: scope.Region, ARN: "arn:aws:rds:ap-northeast-3:123456789012:db:fixture", ResourceID: "db-fixture", Host: "nonexistent.invalid", Port: 5432, Name: "fixture", User: "fixture"}}
	i := drcontrol.Invocation{Version: drcontrol.Version, Action: "resume", Request: wc.ResumeRequest{CommandID: uuid.New(), Scope: scope, ExpectedRevision: 1, ExpectedEpoch: 1, Desired: wc.Active, TrialID: "trial", RunID: "run", Phase: "activate", Principal: "fixture", ApprovalRef: uuid.NewString(), PlanSHA256: strings.Repeat("a", 64), PreconditionsSHA256: strings.Repeat("b", 64), RequestedAt: now, ValidUntil: now.Add(time.Minute)}}

	i.Approval = drcontrol.Reference{Bucket: p.ArtifactBucket, Key: "control/approval", VersionID: "ap", SHA256: strings.Repeat("a", 64), Size: 1}
	i.Evidence = drcontrol.Reference{Bucket: p.ArtifactBucket, Key: "control/observation", VersionID: "ob", SHA256: i.Request.PreconditionsSHA256, Size: 1}
	plan := drcontrol.CollectionPlan{CAFile: filepath.Join(dir, "ca.pem"), CASHA256: strings.Repeat("c", 64), KubectlExecutable: marker, Kubeconfig: filepath.Join(dir, "not-config"), Context: "fixture", APIServer: "https://fixture.invalid", KubeSystemUID: "uid", Namespace: "labbit", Pods: []drcontrol.PodTarget{{Name: "backend", UID: "uid", Container: "backend", ImageID: "image@sha256:" + strings.Repeat("b", 64)}}}
	p.CollectionPlanSHA256 = plan.SHA256()
	pp := filepath.Join(dir, "policy.json")
	ip := filepath.Join(dir, "input.json")
	pl := filepath.Join(dir, "plan.json")
	for path, v := range map[string]any{pp: p, ip: i, pl: plan} {
		b, _ := json.Marshal(v)
		if os.WriteFile(path, b, 0600) != nil {
			t.Fatal("fixture")
		}
	}
	for _, mode := range []string{"approve", "collect"} {
		for _, execute := range []bool{false, true} {
			t.Run(mode+"/"+map[bool]string{false: "preview", true: "disabled"}[execute], func(t *testing.T) {
				args := []string{"--mode", mode, "--policy", pp, "--input", ip, "--plan", pl, "--key-id", "missing", "--key-file", marker}
				if execute {
					args = append(args, "--execute")
				}
				var b bytes.Buffer
				code := run(args, &b)
				var result output
				if json.Unmarshal(b.Bytes(), &result) != nil || result.ExecutionGate != "HOLD" || result.RecoveryAuthorized || result.CloudVerified || len(result.Signed) != 0 {
					t.Fatal(b.String())
				}
				if !execute && (code != 0 || result.Reason != "NO_EXTERNAL_IO_NO_KEY_READ") {
					t.Fatal(b.String())
				}
				if execute && (code != 2 || result.Reason != "EXECUTION_DISABLED") {
					t.Fatal(b.String())
				}
			})
		}
	}
	r := wc.RestrictRequest{CommandID: uuid.New(), Action: "init", Scope: scope, TrialID: "trial", RunID: "run", Phase: "activate", Principal: "fixture", ApprovalRef: uuid.NewString(), PlanSHA256: strings.Repeat("a", 64), RequestedAt: now, ValidUntil: now.Add(time.Minute)}
	ri := drcontrol.RestrictInvocation{Version: drcontrol.Version, Action: "init", Request: r, Approval: i.Approval}
	b, _ := json.Marshal(ri)
	_ = os.WriteFile(ip, b, 0600)
	var out bytes.Buffer
	if code := run([]string{"--mode", "approve", "--policy", pp, "--input", ip, "--key-file", marker}, &out); code != 0 {
		t.Fatal(out.String())
	}
}
