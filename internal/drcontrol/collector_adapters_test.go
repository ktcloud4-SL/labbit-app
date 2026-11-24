package drcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

func TestCollectorKnownDBBacklogDoesNotRequireExternalSafetyToFail(t *testing.T) {
	counts := []struct {
		name string
		set  func(*WorkSnapshot)
	}{
		{"operations", func(w *WorkSnapshot) { w.Operations = 1 }},
		{"items", func(w *WorkSnapshot) { w.Items = 1 }},
		{"open-boundaries", func(w *WorkSnapshot) { w.OpenBoundaries = 1 }},
		{"pending-resumes", func(w *WorkSnapshot) { w.PendingResumes = 1 }},
		{"pending-restrictions", func(w *WorkSnapshot) { w.PendingRestrictions = 1 }},
		{"provider-unresolved", func(w *WorkSnapshot) { w.ProviderUnresolved = 1 }},
	}
	for _, count := range counts {
		for _, safety := range []string{"absent", "expired", "bad-signature", "wrong-target", "provider-not-verified", "valid"} {
			t.Run(count.name+"/"+safety, func(t *testing.T) {
				f, plan, pods, work := collectorFixture(t)
				count.set(&work.v)
				var safetyRef *Reference
				if safety != "absent" {
					raw := []byte(`{"kind":"synthetic-safety-source","mutations":0}`)
					ref := Reference{f.p.ArtifactBucket, f.p.ArtifactPrefix + "/raw.json", "raw-version", Hash(raw), int64(len(raw))}
					f.art.values[ref.VersionID] = raw
					a := SafetyAttestation{Version: Version, Source: "LIVE_READBACK", DBResourceID: f.p.Database.ResourceID, Binding: Bind(f.i.Request), ObservedAt: f.now, ValidUntil: f.now.Add(5 * time.Second), SingleWriter: "PASS", Fencing: "PASS", ProviderReconciled: "PASS", Sources: []Reference{ref}}
					switch safety {
					case "expired":
						a.ValidUntil = f.now
					case "wrong-target":
						a.DBResourceID = "other-db"
					case "provider-not-verified":
						a.ProviderReconciled = "NOT_VERIFIED"
					}
					b := signedFixture(t, a, "observer", "safety-attestation", f.ob)
					if safety == "bad-signature" {
						var envelope Signed
						if err := json.Unmarshal(b, &envelope); err != nil {
							t.Fatal(err)
						}
						envelope.Signature = strings.Repeat("A", len(envelope.Signature))
						b, _ = json.Marshal(envelope)
					}
					r := Reference{f.p.ArtifactBucket, f.p.ArtifactPrefix + "/safety.json", "safety-version", Hash(b), int64(len(b))}
					f.art.values[r.VersionID] = b
					safetyRef = &r
				}
				v, err := Collect(t.Context(), f.p, f.i, plan, pods, work, f.art, safetyRef, func() time.Time { return f.now })
				if err != nil || v.Observation.WorkInventory != "PASS" || v.Observation.WorkReconciled != "FAIL" {
					t.Fatalf("known DB backlog must remain FAIL regardless of external safety: %+v, %v", v, err)
				}
				if safety == "absent" || safety == "expired" || safety == "bad-signature" || safety == "wrong-target" {
					if v.Observation.SingleWriter != "NOT_VERIFIED" || v.Observation.Fencing != "NOT_VERIFIED" {
						t.Fatal("DB backlog invented external safety evidence", v)
					}
				}
				if v.ExecutionGate != "HOLD" || v.RecoveryAuthorized || v.CloudVerified {
					t.Fatal("collection authorized recovery", v)
				}
			})
		}
	}
}

func TestCollectorUnverifiedDBCannotEstablishReconciledFailOrPass(t *testing.T) {
	for _, kind := range []string{"all-zero", "query-failed", "wrong-database", "wrong-user", "not-writer", "wrong-scope", "stale-revision", "stale-epoch", "not-paused", "future", "old", "negative"} {
		t.Run(kind, func(t *testing.T) {
			f, plan, pods, work := collectorFixture(t)
			work.v.Operations = 1
			switch kind {
			case "all-zero":
				work.v.Operations = 0
			case "query-failed":
				work.err = context.DeadlineExceeded
			case "wrong-database":
				work.v.Database = "other"
			case "wrong-user":
				work.v.User = "other"
			case "not-writer":
				work.v.Writer = false
			case "wrong-scope":
				work.v.State.Scope.Cluster = "other"
			case "stale-revision":
				work.v.State.Revision++
			case "stale-epoch":
				work.v.State.Epoch++
			case "not-paused":
				work.v.State.Mode = wc.Active
			case "future":
				work.v.ObservedAt = f.now.Add(time.Second)
			case "old":
				work.v.ObservedAt = f.now.Add(-time.Second)
			case "negative":
				work.v.Items = -1
			}
			v, err := Collect(t.Context(), f.p, f.i, plan, pods, work, f.art, nil, func() time.Time { return f.now })
			if err != nil || v.Observation.WorkReconciled != "NOT_VERIFIED" {
				t.Fatalf("unknown DB evidence must not become PASS/FAIL: %+v, %v", v, err)
			}
			wantInventory := "NOT_VERIFIED"
			if kind == "all-zero" {
				wantInventory = "PASS"
			}
			if v.Observation.WorkInventory != wantInventory {
				t.Fatal("DB inventory classification changed", v)
			}
			if v.ExecutionGate != "HOLD" || v.RecoveryAuthorized || v.CloudVerified {
				t.Fatal("collection authorized recovery", v)
			}
		})
	}
}

func TestCollectorKnownDBBacklogFailSurvivesCollectionExpiryAndActiveVerifier(t *testing.T) {
	f, plan, pods, work := collectorFixture(t)
	work.v.Operations = 1
	calls := 0
	now := func() time.Time {
		calls++
		if calls >= 4 {
			return f.now.Add(time.Minute)
		}
		return f.now
	}
	v, err := Collect(t.Context(), f.p, f.i, plan, pods, work, f.art, nil, now)
	if err != nil || v.Observation.WorkReconciled != "FAIL" || v.Observation.WorkInventory != "NOT_VERIFIED" {
		t.Fatalf("expiry must preserve the already observed FAIL: %+v, %v", v, err)
	}
	// A signed observation remains unusable for ACTIVE; collection is read-only.
	raw := signedFixture(t, v.Observation, "observer", "observation", f.ob)
	f.i.Evidence.SHA256, f.i.Evidence.Size = Hash(raw), int64(len(raw))
	f.i.Request.PreconditionsSHA256 = Hash(raw)
	f.art.values["ob-version"] = raw
	verifier, err := NewVerifier(f.p, f.i, f.art, func() time.Time { return f.now })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = verifier.RecheckResume(t.Context(), f.i.Request); !errors.Is(err, wc.ErrHold) {
		t.Fatal("known backlog did not hold ACTIVE", err)
	}
}

func TestKubernetesReadAdapterFixedClusterPaginationAndReadback(t *testing.T) {
	f, p, _, _ := collectorFixture(t)
	calls := 0
	runner := runnerFunc(func(_ context.Context, exe string, args []string) ([]byte, error) {
		calls++
		joined := strings.Join(args, " ")
		if exe != p.KubectlExecutable || !strings.Contains(joined, "--server "+p.APIServer) || !strings.Contains(joined, "--context "+p.Context) || !strings.Contains(joined, "--kubeconfig "+p.Kubeconfig) || !strings.Contains(joined, "get --raw") || !strings.Contains(joined, "--insecure-skip-tls-verify=false") || !strings.Contains(joined, "--tls-server-name cluster.example.test") || !strings.Contains(joined, "--certificate-authority "+p.CAFile) || strings.Contains(joined, " exec ") || strings.Contains(joined, "apply") {
			t.Fatal("unsafe request", args)
		}
		path := args[len(args)-1]
		switch {
		case path == "/api/v1/namespaces/kube-system":
			return []byte(`{"metadata":{"uid":"system-uid"}}`), nil
		case strings.Contains(path, "/proxy/dr-control/status"):
			_, _, k, _ := collectorFixture(t)
			v := k.process
			v.Binding.Scope = f.p.Target
			b, _ := json.Marshal(v)
			return b, nil
		case strings.Contains(path, "continue=token"):
			return []byte(`{"metadata":{"resourceVersion":"rv"},"items":[]}`), nil
		default:
			return []byte(`{"metadata":{"resourceVersion":"rv","continue":"token"},"items":[{"metadata":{"name":"backend-1","uid":"pod-uid","resourceVersion":"12"},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}],"containerStatuses":[{"name":"backend","imageID":"` + p.Pods[0].ImageID + `","ready":true,"restartCount":0}]}}]}`), nil
		}
	})
	k, e := NewKubeCLI(p, runner)
	if e != nil {
		t.Fatal(e)
	}
	s, e := k.Snapshot(t.Context())
	if e != nil || !snapshotMatches(p, s) || calls != 3 {
		t.Fatal(s, e, calls)
	}
	if _, _, e = k.Process(t.Context(), "rogue"); e == nil || calls != 3 {
		t.Fatal("unreviewed pod read")
	}
	if _, hash, e := k.Process(t.Context(), "backend-1"); e != nil || !sha256Pattern.MatchString(hash) {
		t.Fatal(e)
	}
}
func TestKubernetesReaderRejectsWrongClusterLoopAndFailure(t *testing.T) {
	for _, kind := range []string{"cluster", "loop", "version", "error", "oversize", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			_, p, _, _ := collectorFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			count := 0
			k, _ := NewKubeCLI(p, runnerFunc(func(context.Context, string, []string) ([]byte, error) {
				count++
				if kind == "error" {
					return nil, ErrUnavailable
				}
				if kind == "oversize" {
					return []byte(strings.Repeat("x", MaxDocumentBytes+1)), nil
				}
				if kind == "cancelled" {
					cancel()
					return []byte(`{}`), nil
				}
				if count == 1 {
					if kind == "cluster" {
						return []byte(`{"metadata":{"uid":"other"}}`), nil
					}
					return []byte(`{"metadata":{"uid":"system-uid"}}`), nil
				}
				rv := "rv"
				if kind == "version" && count > 2 {
					rv = "new"
				}
				return []byte(`{"metadata":{"resourceVersion":"` + rv + `","continue":"loop"},"items":[]}`), nil
			}))
			if _, e := k.Snapshot(ctx); e == nil {
				t.Fatal("unsafe list accepted")
			}
			if count > 4 {
				t.Fatal("unbounded retry", count)
			}
		})
	}
}
func TestExternalSafetyRequiresBoundImmutableSources(t *testing.T) {
	for _, kind := range []string{"valid", "missing-source", "wrong-hash", "wrong-target", "expired", "wrong-purpose", "provider-unsettled", "provider-not-verified", "provider-missing", "provider-invalid", "confirmed-fail-with-sql-error"} {
		t.Run(kind, func(t *testing.T) {
			f, p, k, w := collectorFixture(t)
			raw := []byte(`{"kind":"synthetic-fencing-readback","mutations":0}`)
			ref := Reference{f.p.ArtifactBucket, f.p.ArtifactPrefix + "/raw.json", "raw-version", Hash(raw), int64(len(raw))}
			f.art.values[ref.VersionID] = raw
			a := SafetyAttestation{Version: Version, Source: "LIVE_READBACK", DBResourceID: f.p.Database.ResourceID, Binding: Bind(f.i.Request), ObservedAt: f.now, ValidUntil: f.now.Add(5 * time.Second), SingleWriter: "PASS", Fencing: "PASS", ProviderReconciled: "PASS", Sources: []Reference{ref}}
			purpose := "safety-attestation"
			switch kind {
			case "missing-source":
				a.Sources = nil
			case "wrong-hash":
				a.Sources[0].SHA256 = strings.Repeat("0", 64)
			case "wrong-target":
				a.DBResourceID = "other"
			case "expired":
				a.ValidUntil = f.now
			case "wrong-purpose":
				purpose = "observation"
			case "provider-unsettled":
				w.v.ProviderUnresolved = 1
			case "provider-not-verified":
				a.ProviderReconciled = "NOT_VERIFIED"
			case "provider-missing":
				a.ProviderReconciled = ""
			case "provider-invalid":
				a.ProviderReconciled = "unexpected"
			case "confirmed-fail-with-sql-error":
				a.SingleWriter = "FAIL"
				a.Fencing = "FAIL"
				a.ProviderReconciled = "FAIL"
				w.err = context.DeadlineExceeded
			}
			b := signedFixture(t, a, "observer", purpose, f.ob)
			r := Reference{f.p.ArtifactBucket, f.p.ArtifactPrefix + "/safety.json", "safety-version", Hash(b), int64(len(b))}
			f.art.values[r.VersionID] = b
			v, e := Collect(t.Context(), f.p, f.i, p, k, w, f.art, &r, func() time.Time { return f.now })
			if e != nil {
				t.Fatal(e)
			}
			o := v.Observation
			if kind == "confirmed-fail-with-sql-error" {
				if o.SingleWriter != "FAIL" || o.Fencing != "FAIL" || o.WorkReconciled != "FAIL" {
					t.Fatal("confirmed FAIL lost", v)
				}
			} else if strings.HasPrefix(kind, "provider-") && kind != "provider-unsettled" {
				if o.WorkReconciled != "NOT_VERIFIED" {
					t.Fatal("missing Provider evidence misclassified", v)
				}
			} else if kind == "valid" {
				if o.SingleWriter != "PASS" || o.Fencing != "PASS" || o.WorkReconciled != "PASS" {
					t.Fatal(v)
				}
			} else if kind == "provider-unsettled" {
				if o.WorkReconciled != "FAIL" {
					t.Fatal(v)
				}
			} else if o.Fencing == "PASS" || o.SingleWriter == "PASS" {
				t.Fatal("unverified fencing accepted", v)
			}
			if v.ExecutionGate != "HOLD" || v.RecoveryAuthorized || v.CloudVerified {
				t.Fatal("synthetic evidence changed global gate")
			}
		})
	}
}
