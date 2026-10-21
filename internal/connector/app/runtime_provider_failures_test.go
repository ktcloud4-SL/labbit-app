package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	openstackprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider/openstack"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
)

const diagnosticOperationID = "18600000-0000-4000-8000-000000000001"
const diagnosticInstanceID = "18600000-0000-4000-8000-000000000002"

func localProvisionCommand(attempt int) []byte {
	return []byte(fmt.Sprintf(`{"type":"OPERATION_COMMAND","messageId":"command-%d","sentAt":"2026-10-21T00:00:00Z","operationId":%q,"labInstanceId":%q,"generation":1,"payload":{"mutationType":"PROVISION","creationSnapshot":{"providerConnectionId":"connection-1","workspaceVmKey":"workspace","internetOutbound":false,"vms":[{"vmKey":"workspace","role":"workspace","instanceIndex":0,"imageId":"image-1","flavorId":"flavor-1","flavorSpec":{"vcpus":1,"ramMiB":1024,"diskGiB":0}}]}}}`, attempt, diagnosticOperationID, diagnosticInstanceID))
}

func TestProviderInitializationFailuresAreIndistinguishableOnOperationWire(t *testing.T) {
	for _, scenario := range []string{"missing provider configuration", "authentication rejected", "service catalog missing"} {
		t.Run(scenario, func(t *testing.T) {
			var authCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v3/auth/tokens" {
					t.Errorf("unexpected provider request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				authCalls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if scenario == "authentication rejected" {
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(`{"error":{"message":"fake-sensitive-provider-detail"}}`))
					return
				}
				w.Header().Set("X-Subject-Token", "local-fake-token")
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]any{"token": map[string]any{
					"methods": []string{"password"}, "expires_at": "2099-01-01T00:00:00Z", "catalog": []any{},
					"user": map[string]string{"id": "local-user"}, "project": map[string]string{"id": "local-project"},
				}})
			}))
			defer server.Close()
			cfg := openstackprovider.Config{}
			want := openstackprovider.ErrConfigRequired
			if scenario != "missing provider configuration" {
				path := filepath.Join(t.TempDir(), "clouds.yaml")
				config := fmt.Sprintf("clouds:\n  local:\n    auth:\n      auth_url: %s/v3\n      username: local-user\n      password: local-dummy\n      project_name: local\n      user_domain_name: Default\n      project_domain_name: Default\n", server.URL)
				if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
					t.Fatal(err)
				}
				cfg = openstackprovider.Config{File: path, CloudName: "local"}
				want = openstackprovider.ErrAuthentication
				if scenario == "service catalog missing" {
					want = openstackprovider.ErrServiceUnavailable
				}
			}
			var factoryCalls int
			var logs bytes.Buffer
			lazy := newLazyProvider("connection-1", func(ctx context.Context) (runtimeProvider, error) {
				factoryCalls++
				p, err := openstackprovider.New(ctx, cfg)
				if !errors.Is(err, want) {
					t.Errorf("constructor error=%v want=%v", err, want)
				}
				return p, err
			})
			lazy.setDiagnosticLogger(slog.New(slog.NewJSONHandler(&logs, nil)))
			// Use the real command validator/dispatcher and wire result conversion.
			// Repeating a failed initialization is a new attempt, not a cached success.
			for attempt := range 2 {
				sender := &recordSender{}
				handler := wss.NewHandler(lazy, sender)
				raw := localProvisionCommand(attempt)
				if err := handler.HandleMessage(t.Context(), raw); err != nil {
					t.Fatal(err)
				}
				if len(sender.sent) != 2 {
					t.Fatalf("ACK and result count=%d", len(sender.sent))
				}
				ack, ok := sender.sent[0].(protocol.OperationAckMessage)
				if !ok || !ack.Payload.Accepted {
					t.Fatal("valid command was not acknowledged")
				}
				result, ok := sender.sent[1].(protocol.OperationResultMessage)
				if !ok || result.Payload.Outcome != "FAILED" || result.Payload.Error == nil || result.Payload.Error.Code != "ERR_INFRA_OPENSTACK" || result.Payload.Error.Message != "OpenStack Provider connection is unavailable" || len(result.Payload.ProviderResources) != 0 {
					t.Fatalf("unexpected safe failure: %+v", result.Payload)
				}
				encoded, err := json.Marshal(result)
				if err != nil {
					t.Fatal(err)
				}
				for _, forbidden := range []string{"local-dummy", "local-fake-token", "fake-sensitive-provider-detail", server.URL} {
					if strings.Contains(string(encoded), forbidden) {
						t.Fatal("initialization error leaked protected provider details")
					}
				}
			}
			wantAuthCalls := int32(2)
			if scenario == "missing provider configuration" {
				wantAuthCalls = 0
			}
			if factoryCalls != 2 || authCalls.Load() != wantAuthCalls {
				t.Fatalf("factory calls=%d auth calls=%d", factoryCalls, authCalls.Load())
			}
			category := map[string]string{"missing provider configuration": "config", "authentication rejected": "auth", "service catalog missing": "catalog"}[scenario]
			records := initializationRecords(t, logs.String())
			if len(records) != 2 {
				t.Fatalf("initialization diagnostics=%d", len(records))
			}
			for _, record := range records {
				if record["failure_category"] != category || record["operation_id"] != diagnosticOperationID || record["lab_instance_id"] != diagnosticInstanceID {
					t.Fatalf("diagnostic=%+v", record)
				}
			}
			for _, forbidden := range []string{"local-dummy", "local-fake-token", "fake-sensitive-provider-detail", server.URL} {
				if strings.Contains(logs.String(), forbidden) {
					t.Fatal("diagnostic exposed protected provider details")
				}
			}
			t.Logf("constructor classification=%s; two wire results FAILED/ERR_INFRA_OPENSTACK, no resource API mutation", want)
		})
	}
}

func initializationRecords(t *testing.T, logs string) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record["failure_stage"] == "provider_initialization" {
			records = append(records, record)
		}
	}
	return records
}

func TestProviderInitializationDiagnosticsDiscardRawErrorsAndInvalidIDs(t *testing.T) {
	const sensitive = "https://provider.invalid/?token=fake-sensitive-credential"
	for _, tc := range []struct {
		name, category string
		cause          error
		invalidIDs     bool
	}{
		{"missing config", "config", openstackprovider.ErrConfigRequired, false},
		{"inaccessible config", "config", openstackprovider.ErrConfigUnavailable, false},
		{"invalid config", "config", openstackprovider.ErrConfigInvalid, false},
		{"insecure TLS config", "config", openstackprovider.ErrInsecureTLS, false},
		{"wrapped auth", "auth", openstackprovider.ErrAuthentication, false},
		{"wrapped catalog", "catalog", openstackprovider.ErrServiceUnavailable, false},
		{"unknown SDK error", "internal_init", errors.New(sensitive), false},
		{"invalid correlation", "internal_init", errors.New(sensitive), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connection := "18600000-0000-4000-8000-000000000003"
			corr := provider.Correlation{OperationID: diagnosticOperationID, LabInstanceID: diagnosticInstanceID, Generation: 2}
			if tc.invalidIDs {
				connection, corr.OperationID, corr.LabInstanceID = sensitive, sensitive, sensitive
			}
			p := newLazyProvider(connection, func(context.Context) (runtimeProvider, error) {
				return nil, fmt.Errorf("%s: %w", sensitive, tc.cause)
			})
			var logs bytes.Buffer
			p.setDiagnosticLogger(slog.New(slog.NewJSONHandler(&logs, nil)))
			result, err := p.Provision(t.Context(), provider.ProvisionRequest{Correlation: corr})
			if err != nil || result.Outcome != provider.OutcomeFailed || result.Error == nil || result.Error.Code != "ERR_INFRA_OPENSTACK" {
				t.Fatalf("operation result changed: %+v err=%v", result, err)
			}
			records := initializationRecords(t, logs.String())
			if len(records) != 1 || records[0]["failure_category"] != tc.category || records[0]["generation"] != float64(2) {
				t.Fatalf("diagnostics=%+v", records)
			}
			if tc.invalidIDs {
				for _, field := range []string{"operation_id", "lab_instance_id", "provider_connection_id"} {
					if _, exists := records[0][field]; exists {
						t.Fatalf("unvalidated correlation field logged: %s", field)
					}
				}
			} else if records[0]["operation_id"] != corr.OperationID || records[0]["lab_instance_id"] != corr.LabInstanceID || records[0]["provider_connection_id"] != connection {
				t.Fatalf("correlation=%+v", records[0])
			}
			if strings.Contains(logs.String(), sensitive) || strings.Contains(logs.String(), tc.cause.Error()) {
				t.Fatal("raw error/endpoint/credential leaked to diagnostic")
			}
		})
	}
}

func TestProviderSharedInitializationLogsEachOperationAndKeepsRetryCategory(t *testing.T) {
	started := make(chan struct{})
	release, unblock := providerReleaseGate()
	defer unblock()
	var attempts atomic.Int32
	p := newLazyProvider("18600000-0000-4000-8000-000000000003", func(context.Context) (runtimeProvider, error) {
		if attempts.Add(1) == 1 {
			close(started)
			<-release
			return nil, openstackprovider.ErrAuthentication
		}
		return nil, openstackprovider.ErrConfigInvalid
	})
	var logs bytes.Buffer
	p.setDiagnosticLogger(slog.New(slog.NewJSONHandler(&logs, nil)))
	first := provider.Correlation{OperationID: diagnosticOperationID, LabInstanceID: diagnosticInstanceID, Generation: 1}
	second := provider.Correlation{OperationID: "18600000-0000-4000-8000-000000000004", LabInstanceID: "18600000-0000-4000-8000-000000000005", Generation: 2}
	done := make(chan struct{}, 2)
	go func() {
		_, _ = p.Provision(t.Context(), provider.ProvisionRequest{Correlation: first})
		done <- struct{}{}
	}()
	awaitProviderSignal(t, started)
	waiter := &observedWaitContext{Context: t.Context(), waiting: make(chan struct{})}
	go func() { _, _ = p.Reset(waiter, provider.ResetRequest{Correlation: second}); done <- struct{}{} }()
	awaitProviderSignal(t, waiter.waiting)
	unblock()
	for range 2 {
		awaitProviderSignal(t, done)
	}
	_, _ = p.Cleanup(t.Context(), provider.CleanupRequest{Correlation: first})
	_, _ = p.Reconcile(t.Context(), provider.ReconcileRequest{Correlation: second})
	records := initializationRecords(t, logs.String())
	if len(records) != 4 || attempts.Load() != 3 {
		t.Fatalf("records=%d initialization attempts=%d", len(records), attempts.Load())
	}
	seen := map[string]bool{}
	for _, record := range records[:2] {
		if record["failure_category"] != "auth" {
			t.Fatalf("shared attempt category=%+v", record)
		}
		seen[record["operation_id"].(string)] = true
	}
	if !seen[first.OperationID] || !seen[second.OperationID] {
		t.Fatal("shared initialization mixed operation correlations")
	}
	for _, record := range records[2:] {
		if record["failure_category"] != "config" {
			t.Fatalf("retry reused stale category: %+v", record)
		}
	}
}

func TestProviderInitializationInternalFailuresAndCanceledCaller(t *testing.T) {
	for _, tc := range []struct {
		name, connection, category string
		factory                    providerFactory
	}{
		{"missing factory", "connection-1", "internal_init", nil},
		{"missing connection", "", "config", func(context.Context) (runtimeProvider, error) {
			t.Fatal("factory called without connection")
			return nil, nil
		}},
		{"nil initialized provider", "connection-1", "internal_init", func(context.Context) (runtimeProvider, error) { return nil, nil }},
		{"wrong initialized connection", "connection-1", "internal_init", func(context.Context) (runtimeProvider, error) {
			return &provider.MockProvider{ConnectionID: "other-connection"}, nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newLazyProvider(tc.connection, tc.factory)
			var logs bytes.Buffer
			p.setDiagnosticLogger(slog.New(slog.NewJSONHandler(&logs, nil)))
			_, _ = p.Provision(t.Context(), provider.ProvisionRequest{Correlation: provider.Correlation{OperationID: diagnosticOperationID, LabInstanceID: diagnosticInstanceID, Generation: 1}})
			records := initializationRecords(t, logs.String())
			if len(records) != 1 || records[0]["failure_category"] != tc.category {
				t.Fatalf("diagnostics=%+v", records)
			}
		})
	}
	p := newLazyProvider("connection-1", func(context.Context) (runtimeProvider, error) {
		t.Fatal("canceled caller initialized Provider")
		return nil, nil
	})
	var logs bytes.Buffer
	p.setDiagnosticLogger(slog.New(slog.NewJSONHandler(&logs, nil)))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _ = p.Provision(ctx, provider.ProvisionRequest{})
	if logs.Len() != 0 {
		t.Fatal("caller cancellation was misreported as Provider initialization failure")
	}
}

func TestRunControlProviderFailureObservability(t *testing.T) {
	t.Setenv("LABBIT_ENVIRONMENT", "development")
	saas := mock.NewMockSaaS("local-connector-dummy")
	defer saas.Close()
	lazy := newLazyProvider("connection-1", func(context.Context) (runtimeProvider, error) {
		return nil, errors.New("fake-sensitive-provider-detail")
	})
	var logs bytes.Buffer
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runControl(ctx, wss.Config{BaseURL: saas.URL(), Credential: "local-connector-dummy", RuntimeID: "local-runtime", AllowInsecure: true}, lazy, "local-connector", slog.New(slog.NewJSONHandler(&logs, nil)))
	}()
	waitForMessageType(t, saas, protocol.MessageTypeHello, 2*time.Second)
	if err := saas.SendRaw(json.RawMessage(localProvisionCommand(0))); err != nil {
		t.Fatal(err)
	}
	raw := waitForMessageType(t, saas, protocol.MessageTypeOperationResult, 2*time.Second)
	var result protocol.OperationResultMessage
	if err := json.Unmarshal(raw, &result); err != nil || result.Payload.Outcome != "FAILED" {
		t.Fatalf("operation result outcome=%s err=%v", result.Payload.Outcome, err)
	}
	if saas.ActiveConn() == nil {
		t.Error("Provider failure disconnected Control")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Control did not shut down")
	}
	if strings.Contains(logs.String(), "fake-sensitive-provider-detail") || strings.Contains(logs.String(), "local-connector-dummy") {
		t.Fatal("runtime log exposed protected details")
	}
	correlated := 0
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if row["operation_id"] == diagnosticOperationID {
			if row["lab_instance_id"] != diagnosticInstanceID || row["generation"] != float64(1) || row["failure_category"] != "internal_init" {
				t.Fatalf("incorrect initialization diagnostic: %+v", row)
			}
			correlated++
		}
	}
	if correlated != 1 {
		t.Fatalf("expected one safe Operation-correlated initialization diagnostic, got %d", correlated)
	}
	t.Logf("FAILED result delivered while online; operation-correlated runtime log records=%d", correlated)
}
