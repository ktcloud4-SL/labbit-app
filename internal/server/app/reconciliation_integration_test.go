//go:build integration

package app

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/server/labexecution/labexecutiontest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

func knownObservations(r provider.ReconcileRequest, exists bool) provider.ReconcileResult {
	out := provider.ReconcileResult{Observations: make([]provider.ResourceObservation, 0, len(r.KnownResources))}
	for _, ref := range r.KnownResources {
		state := "ABSENT"
		if exists {
			state = "ACTIVE"
		}
		out.Observations = append(out.Observations, provider.ResourceObservation{ResourceType: ref.ResourceType, ProviderID: ref.ProviderID, Generation: ref.Generation, LogicalName: ref.LogicalName, Source: provider.SourceKnownResource, Exists: exists, ObservedState: state})
	}
	return out
}

func TestReconciliationUnknownObservationAndHold(t *testing.T) {
	for _, scenario := range []string{"confirmed presence", "confirmed absence", "candidate", "lookup error", "missing observation", "wrong generation", "duplicate", "wrong logical name"} {
		t.Run(scenario, func(t *testing.T) {
			e := newProvisionEnv(t, time.Second)
			var mutations, queries atomic.Int32
			e.startMock(&provider.MockProvider{
				ProvisionFunc: func(_ context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
					mutations.Add(1)
					return mockProvisionResult(r, provider.OutcomeUnknown), nil
				},
				ReconcileFunc: func(_ context.Context, r provider.ReconcileRequest) (provider.ReconcileResult, error) {
					queries.Add(1)
					if !r.DiscoverCandidates || len(r.KnownResources) != 4 {
						t.Error("exact tracked set/discovery missing")
					}
					out := knownObservations(r, true)
					switch scenario {
					case "confirmed absence":
						out = knownObservations(r, false)
					case "candidate":
						out.Observations = append(out.Observations, provider.ResourceObservation{ResourceType: "SERVER", ProviderID: "unowned-candidate", Generation: 1, Exists: true, Source: provider.SourceDiscoveredCandidate})
					case "lookup error":
						out = knownObservations(r, false)
						out.Error = &provider.SafeError{Code: "PROVIDER_RECONCILE_UNAVAILABLE", Message: "sensitive-lookup-payload"}
					case "missing observation":
						out.Observations = out.Observations[:1]
					case "wrong generation":
						out.Observations[0].Generation++
					case "duplicate":
						out.Observations = append(out.Observations, out.Observations[0])
					case "wrong logical name":
						out.Observations[0].LogicalName = "another-owner"
					}
					return out, nil
				},
			})
			for range 3 {
				e.runOneMustClaim()
			}
			for range 3 {
				e.runOneMustClaim()
			}
			if mutations.Load() != 3 || queries.Load() != 3 {
				t.Fatalf("mutation/query=%d/%d", mutations.Load(), queries.Load())
			}
			var hold, resources, missing int
			if err := e.db.QueryRow(t.Context(), `SELECT count(*) FROM operation_items WHERE operation_id=$1 AND status='RECONCILING' AND stage='HOLD' AND lease_owner IS NULL AND finished_at IS NULL`, e.opID).Scan(&hold); err != nil {
				t.Fatal(err)
			}
			if err := e.db.QueryRow(t.Context(), `SELECT count(*),count(*) FILTER(WHERE lifecycle_status='MISSING') FROM provider_resources`).Scan(&resources, &missing); err != nil {
				t.Fatal(err)
			}
			if hold != 3 || resources != 12 || e.operationStatus() != "RECONCILING" {
				t.Fatalf("hold=%d resources=%d status=%s", hold, resources, e.operationStatus())
			}
			wantMissing := 0
			if scenario == "confirmed absence" {
				wantMissing = 12
			}
			if missing != wantMissing {
				t.Fatalf("unconfirmed observation persisted: missing=%d", missing)
			}
			if claimed, err := e.stack.Provisions.RunOne(t.Context()); err != nil || claimed {
				t.Fatalf("HOLD cooldown claimed=%v err=%v", claimed, err)
			}
			e.assertHTTPMatchesDatabase()
		})
	}
}

func TestReconciliationExpiredWorkerLeaseFencesOriginalWriter(t *testing.T) {
	e := newProvisionEnv(t, time.Second)
	c, err := e.store.ClaimOperationItem(t.Context(), uuid.New(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	d, err := e.store.PrepareProvision(t.Context(), c.Lease)
	if err != nil {
		t.Fatal(err)
	}
	labexecutiontest.Exec(t, e.db, `UPDATE operation_items SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, c.Item.ID)
	// Recovery uses a fresh executor, inbox, Registry and Router.
	fresh, err := newControlStack(e.store, stackOptions{PublicOrigin: publicOrigin, Worker: true, ProvisionLeaseDuration: 300 * time.Millisecond, ProvisionResultTimeout: time.Second, ProvisionDispatchRetryDelay: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(applicationHandler(fresh.routes()))
	t.Cleanup(func() {
		fresh.close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = fresh.shutdown(ctx)
		server.Close()
	})
	e.stack = fresh
	e.server = server
	var mutations, queries atomic.Int32
	e.startMock(&provider.MockProvider{
		ProvisionFunc: func(_ context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
			mutations.Add(1)
			return mockProvisionResult(r, provider.OutcomeSucceeded), nil
		},
		ReconcileFunc: func(_ context.Context, r provider.ReconcileRequest) (provider.ReconcileResult, error) {
			queries.Add(1)
			return knownObservations(r, false), nil
		},
	})
	for range 3 {
		e.runOneMustClaim()
	}
	if mutations.Load() != 2 || queries.Load() != 1 {
		t.Fatalf("expired mutation replayed: %d/%d", mutations.Load(), queries.Load())
	}
	if err = e.store.WriteProvision(t.Context(), repository.ProvisionUpdate{Dispatch: d, Status: "SUCCEEDED"}); !errors.Is(err, repository.ErrOperationLeaseLost) {
		t.Fatalf("old writer=%v", err)
	}
	if _, err = e.store.RenewOperationItemLease(t.Context(), c.Lease, time.Second); !errors.Is(err, repository.ErrOperationLeaseLost) {
		t.Fatalf("old renew=%v", err)
	}
	var status, stage string
	if err = e.db.QueryRow(t.Context(), `SELECT status,stage FROM operation_items WHERE id=$1`, c.Item.ID).Scan(&status, &stage); err != nil || status != "RECONCILING" || stage != "HOLD" {
		t.Fatalf("expired item=%s/%s %v", status, stage, err)
	}
}

func TestReconciliationCleanupResidualAndConfirmedAbsence(t *testing.T) {
	for _, scenario := range []string{"residual", "candidate", "lookup error", "missing observation", "wrong generation"} {
		t.Run(scenario, func(t *testing.T) {
			e := newProvisionEnv(t, time.Second)
			var mutations, queries atomic.Int32
			var residual atomic.Bool
			residual.Store(true)
			e.startMock(&provider.MockProvider{
				ProvisionFunc: func(_ context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
					return mockProvisionResult(r, provider.OutcomeSucceeded), nil
				},
				CleanupFunc: func(_ context.Context, r provider.CleanupRequest) (provider.OperationResult, error) {
					mutations.Add(1)
					return provider.OperationResult{Outcome: provider.OutcomeUnknown, ProviderResources: deletedRefs(r.ProviderResources)}, nil
				},
				ReconcileFunc: func(_ context.Context, r provider.ReconcileRequest) (provider.ReconcileResult, error) {
					queries.Add(1)
					if len(r.KnownResources) != 4 {
						t.Error("previously deleted references omitted")
					}
					out := knownObservations(r, false)
					if residual.Load() {
						switch scenario {
						case "residual":
							out.Observations[0].Exists = true
							out.Observations[0].ObservedState = "ACTIVE"
						case "candidate":
							out.Observations = append(out.Observations, provider.ResourceObservation{ResourceType: "SERVER", ProviderID: "unowned-cleanup-candidate", Exists: true, Source: provider.SourceDiscoveredCandidate})
						case "lookup error":
							out.Error = &provider.SafeError{Code: "PROVIDER_RECONCILE_UNAVAILABLE", Message: "sensitive-provider-error"}
						case "missing observation":
							out.Observations = out.Observations[:1]
						case "wrong generation":
							out.Observations[0].Generation++
						}
					}
					return out, nil
				},
			})
			e.seedMutation()
			e.cleanup("reconcile-cleanup")
			for range 6 {
				e.runOneMustClaim()
			}
			if e.operationStatus() != "RECONCILING" {
				t.Fatal("residual released cleanup")
			}
			wantPresent := 0
			if scenario == "residual" {
				wantPresent = 3
			}
			var present int
			if err := e.db.QueryRow(t.Context(), `SELECT count(*) FROM provider_resources WHERE lifecycle_status='PRESENT'`).Scan(&present); err != nil || present != wantPresent {
				t.Fatalf("residual=%d %v", present, err)
			}
			residual.Store(false)
			labexecutiontest.Exec(t, e.db, `UPDATE operation_items SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE operation_id=$1`, e.opID)
			for range 3 {
				e.runOneMustClaim()
			}
			if mutations.Load() != 3 || queries.Load() != 6 || e.operationStatus() != "SUCCEEDED" {
				t.Fatalf("cleanup mutation/query=%d/%d status=%s", mutations.Load(), queries.Load(), e.operationStatus())
			}
			var status string
			var finished *time.Time
			if err := e.db.QueryRow(t.Context(), `SELECT status,finished_at FROM lab_executions WHERE id=$1`, e.executionID).Scan(&status, &finished); err != nil || status != "COMPLETED" || finished == nil {
				t.Fatalf("execution=%s %v %v", status, finished, err)
			}
			var deletionHistory int
			if err := e.db.QueryRow(t.Context(), `SELECT count(*) FROM provider_resources WHERE deleted_at IS NOT NULL`).Scan(&deletionHistory); err != nil {
				t.Fatal(err)
			}
			wantHistory := 12
			if scenario == "residual" {
				wantHistory = 9
			}
			if deletionHistory != wantHistory {
				t.Fatalf("confirmed deletion history=%d want=%d", deletionHistory, wantHistory)
			}
			e.assertHTTPMatchesDatabase()
		})
	}
}

func TestReconciliationConcurrentClaimAndStaleObserver(t *testing.T) {
	e := newProvisionEnv(t, time.Second)
	c, err := e.store.ClaimOperationItem(t.Context(), uuid.New(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.PrepareProvision(t.Context(), c.Lease); err != nil {
		t.Fatal(err)
	}
	labexecutiontest.Exec(t, e.db, `UPDATE operation_items SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, c.Item.ID)
	var wg sync.WaitGroup
	claims := make(chan repository.OperationItemClaim, 8)
	for range 8 {
		wg.Go(func() {
			c, err := e.store.ClaimReconciliation(t.Context(), uuid.New(), time.Minute)
			if err == nil {
				claims <- c
			} else if !errors.Is(err, repository.ErrNotFound) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	close(claims)
	var first repository.OperationItemClaim
	count := 0
	for c := range claims {
		first = c
		count++
	}
	if count != 1 {
		t.Fatalf("concurrent claims=%d", count)
	}
	d, err := e.store.PrepareReconciliation(t.Context(), first.Lease)
	if err != nil {
		t.Fatal(err)
	}
	labexecutiontest.Exec(t, e.db, `UPDATE operation_items SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, c.Item.ID)
	second, err := e.store.ClaimReconciliation(t.Context(), uuid.New(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if first.Lease.Owner == second.Lease.Owner {
		t.Fatal("claim owner reused")
	}
	if err = e.store.WriteReconciliation(t.Context(), repository.ReconciliationUpdate{Dispatch: d, RetryDelay: time.Second}); !errors.Is(err, repository.ErrOperationLeaseLost) {
		t.Fatalf("stale observer write=%v", err)
	}
	if _, err = e.store.RenewReconciliation(t.Context(), first.Lease, time.Minute); !errors.Is(err, repository.ErrOperationLeaseLost) {
		t.Fatalf("stale observer renew=%v", err)
	}
}

func TestReconciliationResetRetainsGenerationAndNeverReplays(t *testing.T) {
	e := newProvisionEnv(t, time.Second)
	var provisionCalls, resetCalls, queries atomic.Int32
	e.startMock(&provider.MockProvider{
		ProvisionFunc: func(_ context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
			provisionCalls.Add(1)
			return mockProvisionResult(r, provider.OutcomeSucceeded), nil
		},
		ResetFunc: func(_ context.Context, r provider.ResetRequest) (provider.OperationResult, error) {
			resetCalls.Add(1)
			out := mockProvisionResult(provider.ProvisionRequest{Correlation: r.Correlation, CreationSnapshot: r.CreationSnapshot}, provider.OutcomeUnknown)
			for i := range out.ProviderResources {
				out.ProviderResources[i].ProviderID += "/reset"
			}
			out.ProviderResources = append(deletedRefs(r.ProviderResources), out.ProviderResources...)
			return out, nil
		},
		ReconcileFunc: func(_ context.Context, r provider.ReconcileRequest) (provider.ReconcileResult, error) {
			queries.Add(1)
			if r.Generation != 2 || len(r.KnownResources) != 8 {
				t.Error("reset generation/history lost")
			}
			return knownObservations(r, false), nil
		},
	})
	instance := e.seedMutation()
	e.reset(instance, "reset-reconcile")
	e.runOneMustClaim()
	e.runOneMustClaim()
	var generation int64
	if err := e.db.QueryRow(t.Context(), `SELECT generation FROM lab_instances WHERE id=$1`, instance).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	if generation != 1 || e.operationStatus() != "RECONCILING" || provisionCalls.Load() != 3 || resetCalls.Load() != 1 || queries.Load() != 1 {
		t.Fatalf("generation=%d status=%s calls=%d/%d/%d", generation, e.operationStatus(), provisionCalls.Load(), resetCalls.Load(), queries.Load())
	}
}
