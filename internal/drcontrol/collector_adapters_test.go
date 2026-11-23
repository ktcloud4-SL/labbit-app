package drcontrol

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

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
				if o.SingleWriter != "FAIL" || o.Fencing != "FAIL" {
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
