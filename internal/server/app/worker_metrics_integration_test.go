//go:build integration

package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/observability"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/labexecution"
	"github.com/ktcloud4-SL/labbit-app/internal/server/labexecution/labexecutiontest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal/terminaltest"
)

type workerMetricsEnv struct {
	t           *testing.T
	dsn         string
	db          *pgx.Conn
	store       *postgres.Store
	f           *labexecutiontest.Fixture
	stack       *controlStack
	server      *httptest.Server
	cookie      string
	events      *routedEvents
	opID        uuid.UUID
	executionID uuid.UUID
	metrics     *observability.WorkerMetrics
	admin       http.Handler
}

func newWorkerMetricsEnv(t *testing.T, timeout time.Duration) *workerMetricsEnv {
	t.Helper()
	dsn := postgrestest.NewDatabase(t)
	postgrestest.Migrate(t, dsn, loadEmbeddedMigrations(t))
	db := postgrestest.Connect(t, dsn)
	f := labexecutiontest.New(t, db)
	hash := sha256.Sum256([]byte(e2eConnectorCredential))
	labexecutiontest.Exec(t, db, `INSERT INTO connector_credentials (id,connector_id,credential_hash) VALUES ($1,$2,$3)`, uuid.New(), f.ConnectorID, hash[:])
	pool, err := postgres.OpenPool(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store := postgres.NewStore(pool)
	events := &routedEvents{}

	reg, _, _, wm := applicationMetrics([]string{"api", "worker"})
	ready := &atomic.Bool{}
	ready.Store(true)
	admin := adminHandler(ready, promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))

	stack, err := newControlStack(store, stackOptions{
		PublicOrigin:                publicOrigin,
		Worker:                      true,
		OperationSink:               events,
		WorkerMetrics:               wm,
		ProvisionLeaseDuration:      300 * time.Millisecond,
		ProvisionResultTimeout:      timeout,
		ProvisionPollInterval:       10 * time.Millisecond,
		ProvisionDispatchRetryDelay: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(applicationHandler(stack.routes()))
	t.Cleanup(func() {
		stack.close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := stack.shutdown(ctx); err != nil {
			t.Error(err)
		}
		server.Close()
	})

	e := &workerMetricsEnv{
		t:       t,
		dsn:     dsn,
		db:      db,
		store:   store,
		f:       f,
		stack:   stack,
		server:  server,
		cookie:  "__Host-labbit-session=" + terminaltest.LoginSession(t, db, f.InstructorID),
		events:  events,
		metrics: wm,
		admin:   admin,
	}
	e.acceptAt(server.URL)
	return e
}

func (e *workerMetricsEnv) acceptAt(application string) {
	t, f := e.t, e.f
	t.Helper()
	// Single target student so there is exactly 1 OperationItem to cleanly assert metrics per attempt
	body := fmt.Sprintf(`{"labSpecId":%q,"targetStudentIds":[%q]}`, f.LabSpecID, f.StudentAID)
	resp := send(t, http.MethodPost, application+"/api/v1/classes/"+f.ClassID.String()+"/lab-executions", body,
		map[string]string{"Cookie": e.cookie, "Origin": publicOrigin, "Content-Type": "application/json", "Idempotency-Key": "worker-metrics-key-" + uuid.New().String()})
	if resp.status != 202 {
		t.Fatalf("register=%d %s", resp.status, resp.body)
	}
	var accepted struct {
		OperationID string
		Target      struct{ ID string }
	}
	if err := json.Unmarshal([]byte(resp.body), &accepted); err != nil {
		t.Fatal(err)
	}
	e.opID = uuid.MustParse(accepted.OperationID)
	e.executionID = uuid.MustParse(accepted.Target.ID)
}

func (e *workerMetricsEnv) scrape() string {
	e.t.Helper()
	rec := httptest.NewRecorder()
	e.admin.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain; version=0.0.4") {
		e.t.Fatalf("scrape status=%d type=%s", rec.Code, rec.Header().Get("Content-Type"))
	}
	return rec.Body.String()
}

func (e *workerMetricsEnv) startMock(mock *provider.MockProvider) {
	e.t.Helper()
	startRoutingConnector(e.t, &lifecycleEnv{url: e.server.URL, registry: e.stack.Registry, router: e.stack.Router, connectorID: e.f.ConnectorID}, mock)
}

func TestWorkerMetricsFullLifecycleAndExposition(t *testing.T) {
	e := newWorkerMetricsEnv(t, 2*time.Second)

	// 1. Initial exposition before any claims
	initial := e.scrape()
	if !strings.Contains(initial, "labbit_worker_active_operations 0") {
		t.Fatalf("initial scrape missing active operations 0: %s", initial)
	}
	if strings.Contains(initial, "labbit_worker_operations_processed_total") {
		t.Fatal("counter series should not be emitted before any attempts finish")
	}

	// Verify admin probes are unaffected
	for _, probe := range []string{"/livez", "/readyz"} {
		rec := httptest.NewRecorder()
		e.admin.ServeHTTP(rec, httptest.NewRequest("GET", probe, nil))
		if rec.Code != http.StatusOK || rec.Body.String() != "ok\n" {
			t.Fatalf("probe %s status=%d body=%s", probe, rec.Code, rec.Body.String())
		}
	}

	// 2. Successful execution attempt
	release := make(chan struct{})
	e.startMock(&provider.MockProvider{ProvisionFunc: func(ctx context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return provider.OperationResult{}, ctx.Err()
		}
		return mockProvisionResult(r, provider.OutcomeSucceeded), nil
	}})

	// RunOne asynchronously to catch in-flight gauge
	done := make(chan error, 1)
	go func() {
		claimed, err := e.stack.Provisions.RunOne(t.Context())
		if !claimed && err == nil {
			err = fmt.Errorf("expected claim")
		}
		done <- err
	}()

	// Gauge must be 1 while in-flight
	eventually(t, "active_operations gauge is 1 during execution", 3*time.Second, func() bool {
		return testutil.ToFloat64(e.metrics.ActiveOperations) == 1
	})
	close(release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunOne failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunOne timeout")
	}

	// Gauge must clean up to 0 after completion
	eventually(t, "active_operations gauge returns to 0", 2*time.Second, func() bool {
		return testutil.ToFloat64(e.metrics.ActiveOperations) == 0
	})

	// Scrape and verify counter and histogram
	body := e.scrape()
	if !strings.Contains(body, `labbit_worker_operations_processed_total{operation_type="PROVISION",result="succeeded"} 1`) {
		t.Fatalf("missing succeeded counter in scrape:\n%s", body)
	}
	if !strings.Contains(body, `labbit_worker_operation_duration_seconds_count{operation_type="PROVISION",result="succeeded"} 1`) {
		t.Fatalf("missing duration histogram in scrape:\n%s", body)
	}

	// 3. Verify Non-leakage: No IDs in labels or metric values
	for _, forbidden := range []string{
		e.opID.String(),
		e.executionID.String(),
		e.f.ConnectorID.String(),
		e.f.LabSpecID.String(),
		e.f.ClassID.String(),
		e.f.InstructorID.String(),
		"operation_id",
		"lab_instance_id",
		"connector_id",
		"request_id",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("forbidden ID or label found in scrape: %s", forbidden)
		}
	}

	// 4. Repeated admin scrape and probes do not count as worker attempts
	for range 3 {
		e.scrape()
	}
	if got := testutil.ToFloat64(e.metrics.OperationsProcessed.WithLabelValues("PROVISION", "succeeded")); got != 1 {
		t.Fatalf("admin scrapes mutated worker counter: got %v, want 1", got)
	}
}

func TestWorkerMetricsDeferredAndRetryOutcomes(t *testing.T) {
	// Without starting a mock connector, the connector is offline -> ErrConnectorUnavailable
	e := newWorkerMetricsEnv(t, 2*time.Second)

	// Attempt 1: RunOne when connector is unavailable -> deferred
	claimed, err := e.stack.Provisions.RunOne(t.Context())
	if !claimed || err != nil {
		t.Fatalf("claimed=%v, err=%v", claimed, err)
	}

	// Gauge must return to 0
	if got := testutil.ToFloat64(e.metrics.ActiveOperations); got != 0 {
		t.Fatalf("active_operations after defer = %v, want 0", got)
	}

	// Counter must record deferred
	if got := testutil.ToFloat64(e.metrics.OperationsProcessed.WithLabelValues("PROVISION", "deferred")); got != 1 {
		t.Fatalf("operations_processed PROVISION deferred = %v, want 1", got)
	}

	// Verify in database: item is PENDING with dispatch_not_before set
	var status string
	err = e.db.QueryRow(t.Context(), `SELECT status FROM operation_items WHERE operation_id=$1`, e.opID).Scan(&status)
	if err != nil || status != "PENDING" {
		t.Fatalf("item status=%s, err=%v, want PENDING", status, err)
	}

	// Attempt 2 (Retry): Reset dispatch_not_before to simulate time passing, start mock connector, and attempt again
	labexecutiontest.Exec(t, e.db, `UPDATE operation_items SET dispatch_not_before=NULL WHERE operation_id=$1`, e.opID)

	e.startMock(&provider.MockProvider{ProvisionFunc: func(_ context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
		return mockProvisionResult(r, provider.OutcomeSucceeded), nil
	}})

	claimed, err = e.stack.Provisions.RunOne(t.Context())
	if !claimed || err != nil {
		t.Fatalf("claimed retry=%v, err=%v", claimed, err)
	}

	// Active operations gauge is 0
	if got := testutil.ToFloat64(e.metrics.ActiveOperations); got != 0 {
		t.Fatalf("active_operations after retry = %v, want 0", got)
	}

	// Counter records the second attempt as succeeded!
	if got := testutil.ToFloat64(e.metrics.OperationsProcessed.WithLabelValues("PROVISION", "succeeded")); got != 1 {
		t.Fatalf("operations_processed PROVISION succeeded = %v, want 1", got)
	}

	// Total attempts processed is now 2 (1 deferred + 1 succeeded)!
	body := e.scrape()
	if !strings.Contains(body, `labbit_worker_operations_processed_total{operation_type="PROVISION",result="deferred"} 1`) {
		t.Fatalf("missing deferred in scrape:\n%s", body)
	}
	if !strings.Contains(body, `labbit_worker_operations_processed_total{operation_type="PROVISION",result="succeeded"} 1`) {
		t.Fatalf("missing succeeded in scrape:\n%s", body)
	}
}

func TestWorkerMetricsUnknownAndCancellationOutcomes(t *testing.T) {
	// When connector returns OutcomeUnknown or result unconfirmed, outcome must be unknown
	e := newWorkerMetricsEnv(t, 2*time.Second)

	e.startMock(&provider.MockProvider{ProvisionFunc: func(_ context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
		return provider.OperationResult{Outcome: provider.OutcomeUnknown}, nil
	}})

	claimed, err := e.stack.Provisions.RunOne(t.Context())
	if !claimed || err != nil {
		t.Fatalf("claimed=%v, err=%v", claimed, err)
	}

	if got := testutil.ToFloat64(e.metrics.ActiveOperations); got != 0 {
		t.Fatalf("active_operations after unknown = %v, want 0", got)
	}
	if got := testutil.ToFloat64(e.metrics.OperationsProcessed.WithLabelValues("PROVISION", "unknown")); got != 1 {
		t.Fatalf("operations_processed PROVISION unknown = %v, want 1", got)
	}

	// Verify cancellation outcome and gauge cleanup
	cancelEnv := newWorkerMetricsEnv(t, 2*time.Second)
	principal, err := connector.NewService(cancelEnv.store).Authenticate(t.Context(), connector.Credential(e2eConnectorCredential))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	registration := cancelEnv.stack.Registry.Register(principal, nil)
	t.Cleanup(func() { registration.Release() })
	registration.MarkReady(func([]byte) error { cancel(); return nil })

	claimed, err = cancelEnv.stack.Provisions.RunOne(ctx)
	if !claimed || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected claimed=true and context.Canceled, got claimed=%v err=%v", claimed, err)
	}

	// Gauge must be cleaned up
	if got := testutil.ToFloat64(cancelEnv.metrics.ActiveOperations); got != 0 {
		t.Fatalf("active_operations after cancel = %v, want 0", got)
	}
	if got := testutil.ToFloat64(cancelEnv.metrics.OperationsProcessed.WithLabelValues("PROVISION", "cancel")); got != 1 {
		t.Fatalf("operations_processed PROVISION cancel = %v, want 1", got)
	}
}

func (e *workerMetricsEnv) seedMutation() uuid.UUID {
	e.t.Helper()
	for {
		claimed, err := e.stack.Provisions.RunOne(e.t.Context())
		if err != nil {
			e.t.Fatalf("seed claim err: %v", err)
		}
		if !claimed {
			break
		}
	}
	var id uuid.UUID
	if err := e.db.QueryRow(e.t.Context(), `SELECT id FROM lab_instances WHERE lab_execution_id=$1 AND user_id=$2`, e.executionID, e.f.StudentAID).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	labexecutiontest.Exec(e.t, e.db, `UPDATE lab_instances SET status='READY' WHERE lab_execution_id=$1`, e.executionID)
	labexecutiontest.Exec(e.t, e.db, `UPDATE lab_executions SET status='ACTIVE' WHERE id=$1`, e.executionID)
	return id
}

func (e *workerMetricsEnv) reset(id uuid.UUID, key string) {
	e.t.Helper()
	user, err := e.store.UserByID(e.t.Context(), e.f.InstructorID)
	if err != nil {
		e.t.Fatal(err)
	}
	a, err := labexecution.NewService(e.store).Reset(e.t.Context(), user, labexecution.ResetInput{LabInstanceID: id.String(), IdempotencyKey: key})
	if err != nil {
		e.t.Fatal(err)
	}
	e.opID = a.OperationID
}

func (e *workerMetricsEnv) cleanup(key string) {
	e.t.Helper()
	user, err := e.store.UserByID(e.t.Context(), e.f.InstructorID)
	if err != nil {
		e.t.Fatal(err)
	}
	a, err := labexecution.NewService(e.store).Cleanup(e.t.Context(), user, labexecution.CleanupInput{LabExecutionID: e.executionID.String(), IdempotencyKey: key})
	if err != nil {
		e.t.Fatal(err)
	}
	e.opID = a.OperationID
}

// Regression 1: PROVISION succeeded + invalid readiness -> DB RECONCILING/metric unknown
func TestWorkerMetricsRegressionInvalidProvisionReadiness(t *testing.T) {
	e := newWorkerMetricsEnv(t, 2*time.Second)

	// Mock connector reports OutcomeSucceeded, but readiness attestation is invalid (e.g. wrong Generation / wrong VMKey).
	// WriteProvision commits status='RECONCILING', stage='RECONCILING'.
	e.startMock(&provider.MockProvider{ProvisionFunc: func(_ context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
		res := mockProvisionResult(r, provider.OutcomeSucceeded)
		res.Readiness = &provider.Readiness{
			WorkspaceVMKey: "invalid-vm-key",
			ServerID:       "wrong-server",
			Generation:     999,
		}
		return res, nil
	}})

	claimed, err := e.stack.Provisions.RunOne(t.Context())
	if !claimed || err != nil {
		t.Fatalf("claimed=%v, err=%v", claimed, err)
	}

	// Active gauge must return to 0
	if got := testutil.ToFloat64(e.metrics.ActiveOperations); got != 0 {
		t.Fatalf("active_operations after invalid readiness = %v, want 0", got)
	}

	// DB verification: operation_items status must be committed as RECONCILING, stage RECONCILING
	var status, stage string
	if err := e.db.QueryRow(t.Context(), `SELECT status, COALESCE(stage, '') FROM operation_items WHERE operation_id=$1 AND status='RECONCILING' LIMIT 1`, e.opID).Scan(&status, &stage); err != nil {
		t.Fatal(err)
	}
	if status != "RECONCILING" || stage != "RECONCILING" {
		t.Fatalf("DB status=%q stage=%q, want status=RECONCILING stage=RECONCILING", status, stage)
	}

	// Metric verification: must be unknown, NOT succeeded!
	if got := testutil.ToFloat64(e.metrics.OperationsProcessed.WithLabelValues("PROVISION", "unknown")); got != 1 {
		t.Fatalf("operations_processed PROVISION unknown = %v, want 1", got)
	}
	if got := testutil.ToFloat64(e.metrics.OperationsProcessed.WithLabelValues("PROVISION", "succeeded")); got != 0 {
		t.Fatalf("operations_processed PROVISION succeeded = %v, want 0 (false success prevented!)", got)
	}
}

// Regression 2: RESET/CLEANUP incomplete result -> DB RECONCILING/metric unknown
func TestWorkerMetricsRegressionIncompleteResetMutation(t *testing.T) {
	e := newWorkerMetricsEnv(t, 2*time.Second)

	// Configure mock provider once for both initial provision and subsequent reset
	e.startMock(&provider.MockProvider{
		ProvisionFunc: func(_ context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
			return mockProvisionResult(r, provider.OutcomeSucceeded), nil
		},
		ResetFunc: func(_ context.Context, r provider.ResetRequest) (provider.OperationResult, error) {
			deleted := make([]provider.ResourceResult, 0, len(r.ProviderResources))
			for _, res := range r.ProviderResources {
				deleted = append(deleted, provider.ResourceResult{ResourceRef: res, ObservedState: "DELETED"})
			}
			return provider.OperationResult{Outcome: provider.OutcomeSucceeded, ProviderResources: deleted}, nil
		},
	})

	instanceID := e.seedMutation()

	// Initiate Reset on the instance
	e.reset(instanceID, "reset-incomplete-metrics-key")

	claimed, err := e.stack.Provisions.RunOne(t.Context())
	if !claimed || err != nil {
		t.Fatalf("claimed=%v, err=%v", claimed, err)
	}

	// Active gauge must return to 0
	if got := testutil.ToFloat64(e.metrics.ActiveOperations); got != 0 {
		t.Fatalf("active_operations after incomplete reset = %v, want 0", got)
	}

	// DB verification: operation_items status must be committed as RECONCILING, stage RECONCILING
	var status, stage string
	if err := e.db.QueryRow(t.Context(), `SELECT status, COALESCE(stage, '') FROM operation_items WHERE operation_id=$1`, e.opID).Scan(&status, &stage); err != nil {
		t.Fatal(err)
	}
	if status != "RECONCILING" || stage != "RECONCILING" {
		t.Fatalf("DB status=%q stage=%q, want status=RECONCILING stage=RECONCILING", status, stage)
	}

	// Metric verification: RESET attempt recorded as unknown, NOT succeeded!
	if got := testutil.ToFloat64(e.metrics.OperationsProcessed.WithLabelValues("RESET", "unknown")); got != 1 {
		t.Fatalf("operations_processed RESET unknown = %v, want 1", got)
	}
	if got := testutil.ToFloat64(e.metrics.OperationsProcessed.WithLabelValues("RESET", "succeeded")); got != 0 {
		t.Fatalf("operations_processed RESET succeeded = %v, want 0 (false success prevented!)", got)
	}
}

// Regression 3: CLEANUP reconcile 불완전/비권위 observation -> DB HOLD/metric unknown
func TestWorkerMetricsRegressionIncompleteCleanupReconciliation(t *testing.T) {
	e := newWorkerMetricsEnv(t, 2*time.Second)

	// Configure mock provider once for provision, cleanup mutation, and reconciliation
	e.startMock(&provider.MockProvider{
		ProvisionFunc: func(_ context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
			return mockProvisionResult(r, provider.OutcomeSucceeded), nil
		},
		CleanupFunc: func(_ context.Context, r provider.CleanupRequest) (provider.OperationResult, error) {
			return provider.OperationResult{Outcome: provider.OutcomeUnknown}, nil
		},
		ReconcileFunc: func(_ context.Context, r provider.ReconcileRequest) (provider.ReconcileResult, error) {
			obs := make([]provider.ResourceObservation, 0, len(r.KnownResources))
			for _, ref := range r.KnownResources {
				obs = append(obs, provider.ResourceObservation{
					ResourceType:  ref.ResourceType,
					ProviderID:    ref.ProviderID,
					Generation:    ref.Generation,
					LogicalName:   ref.LogicalName,
					Source:        provider.SourceDiscoveredCandidate, // Non-authoritative candidate!
					Exists:        false,
					ObservedState: "ABSENT",
				})
			}
			return provider.ReconcileResult{Observations: obs}, nil
		},
	})

	_ = e.seedMutation()

	// Initiate Cleanup
	e.cleanup("cleanup-metrics-key")

	// 1. Initial CLEANUP mutation claims: each item receives OutcomeUnknown -> status='RECONCILING'
	for range 2 {
		claimed, err := e.stack.Provisions.RunOne(t.Context())
		if !claimed || err != nil {
			t.Fatalf("cleanup mutation claim=%v err=%v", claimed, err)
		}
	}

	var reconcilingCount int
	if err := e.db.QueryRow(t.Context(), `SELECT count(*) FROM operation_items WHERE operation_id=$1 AND status='RECONCILING'`, e.opID).Scan(&reconcilingCount); err != nil {
		t.Fatal(err)
	}
	if reconcilingCount != 2 {
		t.Fatalf("reconciling items count=%d, want 2", reconcilingCount)
	}

	// Expire lease so reconciliation worker can immediately claim it
	labexecutiontest.Exec(t, e.db, `UPDATE operation_items SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE operation_id=$1`, e.opID)

	// 2. Reconciliation attempt: Provider returns non-authoritative candidate (Source != KNOWN_RESOURCE)
	// DB commits status='RECONCILING', stage='HOLD'.
	claimed, err := e.stack.Provisions.RunOne(t.Context())
	if !claimed || err != nil {
		t.Fatalf("reconcile claim=%v err=%v", claimed, err)
	}

	// Active gauge must return to 0
	if got := testutil.ToFloat64(e.metrics.ActiveOperations); got != 0 {
		t.Fatalf("active_operations after reconcile hold = %v, want 0", got)
	}

	// DB verification: at least one item must be status='RECONCILING', stage='HOLD'
	var holdCount int
	if err := e.db.QueryRow(t.Context(), `SELECT count(*) FROM operation_items WHERE operation_id=$1 AND status='RECONCILING' AND stage='HOLD'`, e.opID).Scan(&holdCount); err != nil {
		t.Fatal(err)
	}
	if holdCount < 1 {
		t.Fatalf("hold count=%d, want at least 1", holdCount)
	}

	// Metric verification: CLEANUP reconciliation recorded as unknown (2 mutations + 1 reconcile = 3), NOT succeeded!
	if got := testutil.ToFloat64(e.metrics.OperationsProcessed.WithLabelValues("CLEANUP", "unknown")); got != 3 {
		t.Fatalf("operations_processed CLEANUP unknown = %v, want 3", got)
	}
	if got := testutil.ToFloat64(e.metrics.OperationsProcessed.WithLabelValues("CLEANUP", "succeeded")); got != 0 {
		t.Fatalf("operations_processed CLEANUP succeeded = %v, want 0 (false success prevented!)", got)
	}
}
