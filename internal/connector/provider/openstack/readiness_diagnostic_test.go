package openstackprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/observability"
)

func TestReadinessFailuresKeepClassificationResourcesAndSafeDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, stage, status string
		elapsed, budget     time.Duration
		outcome             coreprovider.Outcome
	}{
		{"ACTIVE deadline", "server_active", "timeout", 6 * time.Minute, 6 * time.Minute, coreprovider.OutcomeFailed},
		{"ERROR", "server_active", "server_error", 0, 6 * time.Minute, coreprovider.OutcomeFailed},
		{"lookup failure", "server_active", "lookup_failed", 0, 6 * time.Minute, coreprovider.OutcomeUnknown},
		{"parent cancel", "server_active", "parent_canceled", 310 * time.Second, 6 * time.Minute, coreprovider.OutcomeUnknown},
		{"SSH deadline", "management_ssh", "timeout", 3 * time.Minute, 3 * time.Minute, coreprovider.OutcomeFailed},
		{"Startup deadline", "startup", "timeout", 5 * time.Minute, 5 * time.Minute, coreprovider.OutcomeFailed},
		{"PTY failure", "workspace_pty", "failed", 0, 3 * time.Minute, coreprovider.OutcomeFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var logs bytes.Buffer
				creates := 0
				a, snapshot := readinessFixture(t, func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Method == "POST" && r.URL.Path == "/compute/v2/servers" {
							creates++
						}
						if r.Method == "GET" && r.URL.Path == "/compute/v2/servers/server" {
							switch tc.name {
							case "ACTIVE deadline", "parent cancel":
								writeJSON(t, w, 200, map[string]any{"server": map[string]any{"id": "server", "status": "BUILD"}})
								return
							case "ERROR":
								writeJSON(t, w, 200, map[string]any{"server": map[string]any{"id": "server", "status": "ERROR"}})
								return
							case "lookup failure":
								http.Error(w, "forbidden-provider-response", 503)
								return
							}
						}
						next.ServeHTTP(w, r)
					})
				})
				a.diagnosticLogger = observability.NewJSONLoggerTo(&logs, "labbit-connector", "bootstrap", "test", "info")
				snapshot.StartupScript = validSnapshot().StartupScript
				a.startupProbe = func(context.Context, string, string) error { return nil }
				sensitive := errors.New("forbidden-credential-endpoint-script-terminal")
				if tc.name == "SSH deadline" {
					a.sshProbe = func(context.Context, string) error { return sensitive }
				}
				if tc.name == "Startup deadline" {
					a.startupProbe = func(context.Context, string, string) error { return sensitive }
				}
				if tc.name == "PTY failure" {
					a.ptyProbe = func(context.Context, string, string) error { return sensitive }
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if tc.name == "parent cancel" {
					go func() { time.Sleep(310 * time.Second); cancel() }()
				}
				corr := coreprovider.Correlation{OperationID: "19700000-0000-4000-8000-000000000001", LabInstanceID: "opaque-forbidden-correlation\nhttps://fixture", Generation: 1}
				result, err := a.Provision(ctx, coreprovider.ProvisionRequest{Correlation: corr, CreationSnapshot: snapshot})
				if err != nil || result.Outcome != tc.outcome || result.Readiness != nil || creates != 1 {
					t.Fatalf("outcome=%s readiness=%v creates=%d err=%v", result.Outcome, result.Readiness, creates, err)
				}
				assertResourceCount(t, result.ProviderResources, coreprovider.ResourceTypeServer, 1)
				if tc.outcome == coreprovider.OutcomeUnknown && result.Error.Code != errorUnknown {
					t.Fatal("uncertain state lost reconciliation meaning")
				}
				records := []map[string]any{}
				for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
					var record map[string]any
					if json.Unmarshal([]byte(line), &record) != nil {
						t.Fatal("invalid production JSON log")
					}
					records = append(records, record)
				}
				last := records[len(records)-1]
				if last["stage"] != tc.stage || last["status"] != tc.status || last["duration_ms"] != float64(tc.elapsed.Milliseconds()) || last["budget_ms"] != float64(tc.budget.Milliseconds()) {
					t.Fatalf("wrong stage timing: %+v", last)
				}
				if last["operation_id"] != corr.OperationID || last["lab_instance_id_sha256"] == nil || last["server_id_sha256"] == nil || last["vm_key_sha256"] == nil || last["service"] != "labbit-connector" || last["timestamp"] == nil {
					t.Fatal("production correlation/base fields missing")
				}
				for _, forbidden := range []string{"forbidden-", "http://", "https://", "172.25.", "#!/bin/", "echo ready", "server-workspace", "local-provider"} {
					if strings.Contains(logs.String(), forbidden) {
						t.Fatalf("protected data in log: %s", forbidden)
					}
				}
			})
		})
	}
}

func TestServerActivePolicyDoesNotExtendOtherWaits(t *testing.T) {
	config := normalizedProvisionConfig(ProvisionConfig{})
	standard := &Adapter{}
	kt := &Adapter{ktNetwork: &ktCloudD1Network{}}
	if standard.serverActiveTimeout(config) != 5*time.Minute || kt.serverActiveTimeout(config) != 6*time.Minute || config.ActiveTimeout != 5*time.Minute || config.SSHReadyTimeout != 3*time.Minute || config.StartupReadyTimeout != 5*time.Minute {
		t.Fatal("readiness policy changed unrelated budgets")
	}
	config.ActiveTimeout = 17 * time.Second
	if standard.serverActiveTimeout(config) != 17*time.Second || kt.serverActiveTimeout(config) != 6*time.Minute {
		t.Fatal("profile isolation failed")
	}
}

// The production constructor must retain the application logger, including its
// Runtime JSON fields, rather than silently discarding readiness events.
func TestNewProviderUsesApplicationDiagnosticLogger(t *testing.T) {
	_, cfg := newReauthPeer(t, "password")
	var logs bytes.Buffer
	cfg.DiagnosticLogger = observability.NewJSONLoggerTo(&logs, "labbit-connector", "bootstrap", "test", "info")
	a, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	err = a.runReadinessStage(context.Background(), cidrCorrelation("instance", 1), "vm", "server", stageServerActive, time.Second, func(context.Context) error { return nil })
	if err != nil || !strings.Contains(logs.String(), `"stage":"server_active"`) || !strings.Contains(logs.String(), `"service":"labbit-connector"`) {
		t.Fatal("application logger was not used")
	}
}
