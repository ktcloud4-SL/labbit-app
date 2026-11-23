package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/drcontrol"
	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

func TestPreviewAndDisabledExecutionMakeNoExternalCalls(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "must-not-execute")
	scope := wc.Scope{ID: "fixture", Region: "ap-northeast-3", Cluster: "fixture"}
	now := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	// Executable and DB endpoint intentionally do not exist. A preview must still
	// succeed, and explicit execution remains denied by the protected policy.
	p := drcontrol.Policy{Version: drcontrol.Version, AccountID: "123456789012", Region: scope.Region, AWSExecutable: marker, AWSProfile: "fixture", Target: scope, Actors: []drcontrol.Actor{{ARN: "arn:aws:iam::123456789012:user/fixture", Principal: "fixture", Actions: []string{"inspect"}}}, ApprovalKeys: []drcontrol.Key{{ID: "approval", Principal: "fixture", PublicKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)), Phases: []string{"activate"}}}, ObservationKeys: []drcontrol.Key{{ID: "observation", Principal: "fixture", PublicKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32)), Phases: []string{"activate"}}}, ArtifactBucket: "fixture", ArtifactPrefix: "control", ApprovalMaxSeconds: 60, EvidenceMaxSeconds: 30, Database: drcontrol.Database{Region: scope.Region, ARN: "arn:aws:rds:ap-northeast-3:123456789012:db:fixture", ResourceID: "db-fixture", Host: "nonexistent.invalid", Port: 5432, Name: "fixture", User: "fixture"}}
	i := drcontrol.Invocation{Version: drcontrol.Version, Action: "inspect", Request: wc.ResumeRequest{CommandID: uuid.New(), Scope: scope, ExpectedRevision: 1, ExpectedEpoch: 1, Desired: wc.Active, TrialID: "trial", RunID: "run", Phase: "activate", Principal: "fixture", ApprovalRef: uuid.NewString(), PlanSHA256: strings.Repeat("a", 64), PreconditionsSHA256: strings.Repeat("b", 64), RequestedAt: now, ValidUntil: now.Add(time.Minute)}}
	pb, _ := json.Marshal(p)
	ib, _ := json.Marshal(i)
	pp := filepath.Join(dir, "policy.json")
	ip := filepath.Join(dir, "input.json")
	if os.WriteFile(pp, pb, 0600) != nil || os.WriteFile(ip, ib, 0600) != nil {
		t.Fatal("fixture write")
	}
	for _, execute := range []bool{false, true} {
		args := []string{"--policy", pp, "--input", ip}
		if execute {
			args = append(args, "--execute")
		}
		var out bytes.Buffer
		code := run(args, &out)
		var r result
		if json.Unmarshal(out.Bytes(), &r) != nil || r.ExecutionGate != "HOLD" || r.RecoveryAuthorized || r.CloudVerified {
			t.Fatal("real gate changed")
		}
		if !execute && (code != 0 || r.Status != "PREVIEW_ONLY") {
			t.Fatal("preview failed")
		}
		if execute && (code == 0 || r.Reason != "EXECUTION_DISABLED") {
			t.Fatal("disabled execution attempted")
		}
	}
}
