//go:build integration

package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	"github.com/ktcloud4-SL/labbit-app/internal/server/labexecution"
	"github.com/ktcloud4-SL/labbit-app/internal/server/labexecution/labexecutiontest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/operation"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal/terminaltest"
)

func (e *provisionEnv) seedMutation() uuid.UUID {
	e.t.Helper()
	// Complete the original operation through its normal worker before Reset.
	for range 3 {
		e.runOneMustClaim()
	}
	var id uuid.UUID
	if err := e.db.QueryRow(e.t.Context(), `SELECT id FROM lab_instances WHERE lab_execution_id=$1 AND user_id=$2`, e.executionID, e.f.StudentAID).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	labexecutiontest.Exec(e.t, e.db, `UPDATE lab_instances SET status='READY' WHERE lab_execution_id=$1`, e.executionID)
	labexecutiontest.Exec(e.t, e.db, `UPDATE lab_executions SET status='ACTIVE' WHERE id=$1`, e.executionID)
	return id
}

func (e *provisionEnv) reset(id uuid.UUID, key string) {
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
func (e *provisionEnv) cleanup(key string) {
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
func deletedRefs(refs []provider.ResourceRef) []provider.ResourceResult {
	results := make([]provider.ResourceResult, 0, len(refs))
	for _, r := range refs {
		results = append(results, provider.ResourceResult{ResourceRef: r, ObservedState: "DELETED"})
	}
	return results
}

func TestResetSnapshotFailureTransitionAndResourceHistory(t *testing.T) {
	for _, scenario := range []string{"preflight failure", "success", "partial delete", "unknown", "incomplete success", "replacement failure", "replacement empty failure", "MISSING", "ABSENT", "DELETED", "partial replacement", "failed partial replacement", "failed absent replacement", "old residual with replacement", "duplicate VM", "wrong VM key", "unknown replacement", "stale lease"} {
		t.Run(scenario, func(t *testing.T) {
			e := newProvisionEnv(t, 5*time.Second)
			var instance uuid.UUID
			var oldIDs []provider.ResourceRef
			var expectedNew int
			e.startMock(&provider.MockProvider{
				ProvisionFunc: func(_ context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
					return mockProvisionResult(r, provider.OutcomeSucceeded), nil
				},
				ResetFunc: func(_ context.Context, r provider.ResetRequest) (provider.OperationResult, error) {
					if r.LabInstanceID != instance.String() || r.Generation != 2 || r.CreationSnapshot.ProviderConnectionID != e.f.ProviderConnectionID.String() || r.CreationSnapshot.StartupScript.Content != labexecutiontest.StartupScript {
						t.Error("reset correlation/snapshot changed")
					}
					for _, vm := range r.CreationSnapshot.VMs {
						if vm.ImageID != labexecutiontest.ImageMarker || vm.FlavorID != labexecutiontest.FlavorMarker {
							t.Error("mutable mapping used")
						}
					}
					for _, ref := range r.ProviderResources {
						if ref.Generation != 1 || ref.LogicalName == "" {
							t.Error("lost previous generation/name")
						}
					}
					oldIDs = r.ProviderResources
					var generation int64
					// The DB connection is used by this goroutine only while RunOne waits.
					if err := e.db.QueryRow(context.Background(), `SELECT generation FROM lab_instances WHERE id=$1`, instance).Scan(&generation); err != nil || generation != 1 {
						t.Error("generation advanced before deletion")
					}
					switch scenario {
					case "preflight failure":
						refs := deletedRefs(r.ProviderResources)
						for i := range refs {
							refs[i].ObservedState = "DELETE_NOT_ATTEMPTED"
						}
						return provider.OperationResult{Outcome: provider.OutcomeFailed, ProviderResources: refs, Error: &provider.SafeError{Code: "IMAGE_NOT_FOUND", Message: "sensitive-provider-message"}}, nil
					case "partial delete":
						refs := deletedRefs(r.ProviderResources)
						refs[len(refs)-1].ObservedState = "DELETE_FAILED"
						return provider.OperationResult{Outcome: provider.OutcomeFailed, ProviderResources: refs}, nil
					case "replacement empty failure":
						return provider.OperationResult{Outcome: provider.OutcomeFailed, ProviderResources: deletedRefs(r.ProviderResources)}, nil
					case "unknown":
						return provider.OperationResult{Outcome: provider.OutcomeUnknown}, nil
					case "incomplete success":
						return provider.OperationResult{Outcome: provider.OutcomeSucceeded}, nil
					}
					result := mockProvisionResult(provider.ProvisionRequest{Correlation: r.Correlation, CreationSnapshot: r.CreationSnapshot}, provider.OutcomeSucceeded)
					for i := range result.ProviderResources {
						result.ProviderResources[i].ProviderID += "/generation-2"
					}
					switch scenario {
					case "MISSING", "ABSENT", "DELETED", "failed absent replacement":
						state := scenario
						if scenario == "failed absent replacement" {
							state = "MISSING"
							result.Outcome = provider.OutcomeFailed
						}
						for i := range result.ProviderResources {
							result.ProviderResources[i].ObservedState = state
						}
					case "partial replacement", "failed partial replacement":
						result.ProviderResources = result.ProviderResources[:2] // tier + just one of three VMs
						if scenario == "failed partial replacement" {
							result.Outcome = provider.OutcomeFailed
						}
					case "duplicate VM":
						duplicate := result.ProviderResources[1]
						duplicate.ProviderID += "/duplicate"
						result.ProviderResources = append(result.ProviderResources, duplicate)
					case "wrong VM key":
						result.ProviderResources[1].LogicalName = "not-in-snapshot"
					case "unknown replacement":
						result.ProviderResources[1].ObservedState = "UNKNOWN"
					}
					result.ProviderResources = append(deletedRefs(r.ProviderResources), result.ProviderResources...)
					if scenario == "old residual with replacement" {
						result.ProviderResources[0].ObservedState = "ACTIVE"
					}
					if scenario == "stale lease" {
						labexecutiontest.Exec(t, e.db, `UPDATE operation_items SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE operation_id=$1`, e.opID)
					}
					for _, ref := range result.ProviderResources {
						if ref.Generation == r.Generation {
							expectedNew++
						}
					}
					if scenario == "replacement failure" {
						result.Outcome = provider.OutcomeFailed
					}
					return result, nil
				},
			})
			instance = e.seedMutation()
			// Mutable sources disappear/change after the immutable snapshot was saved.
			labexecutiontest.Exec(t, e.db, `UPDATE provider_image_mappings SET provider_image_id='changed-image' WHERE id=$1`, e.f.ImageMappingID)
			labexecutiontest.Exec(t, e.db, `UPDATE provider_flavor_mappings SET provider_flavor_id='changed-flavor' WHERE id=$1`, e.f.FlavorMappingID)
			labexecutiontest.Exec(t, e.db, `UPDATE lab_specs SET startup_script='changed-script',revision=revision+1 WHERE id=$1`, e.f.LabSpecID)
			e.reset(instance, "reset-immutable-snapshot")
			if scenario == "stale lease" {
				claimed, err := e.stack.Provisions.RunOne(t.Context())
				if !claimed || !errors.Is(err, repository.ErrOperationLeaseLost) {
					t.Fatalf("stale result=%v %v", claimed, err)
				}
				li, err := e.store.LabInstanceByID(t.Context(), instance)
				if err != nil || li.Generation != 1 || e.operationStatus() != "RUNNING" {
					t.Fatalf("stale result changed lifecycle: %+v %v", li, err)
				}
				var oldPresent, newCount int
				if err := e.db.QueryRow(t.Context(), `SELECT count(*) FILTER (WHERE generation=1 AND lifecycle_status='PRESENT'),count(*) FILTER (WHERE generation=2) FROM provider_resources WHERE lab_instance_id=$1`, instance).Scan(&oldPresent, &newCount); err != nil || oldPresent != 4 || newCount != 0 {
					t.Fatalf("stale result changed resources: %d/%d %v", oldPresent, newCount, err)
				}
				return
			}
			e.runOneMustClaim()
			wantGeneration := int64(1)
			wantStatus := "FAILED"
			wantInstance := "ERROR"
			switch scenario {
			case "preflight failure":
				wantInstance = "READY"
			case "success":
				wantGeneration = 2
				wantStatus = "SUCCEEDED"
				wantInstance = "PROVISIONING"
			case "replacement failure":
				wantGeneration = 2
			case "unknown", "incomplete success", "replacement empty failure", "MISSING", "ABSENT", "DELETED", "partial replacement", "failed partial replacement", "failed absent replacement", "old residual with replacement", "duplicate VM", "wrong VM key", "unknown replacement":
				wantStatus = "RECONCILING"
				wantInstance = "RESETTING"
			}
			li, err := e.store.LabInstanceByID(t.Context(), instance)
			if err != nil || li.Generation != wantGeneration || li.Status != wantInstance || e.operationStatus() != wantStatus {
				t.Fatalf("instance=%+v op=%s err=%v", li, e.operationStatus(), err)
			}
			for index, ref := range oldIDs {
				var generation int64
				var logical, life string
				var creator uuid.UUID
				err := e.db.QueryRow(t.Context(), `SELECT generation,logical_name,lifecycle_status,created_by_operation_item_id FROM provider_resources WHERE provider_connection_id=$1 AND resource_type=$2 AND provider_id=$3`, e.f.ProviderConnectionID, ref.ResourceType, ref.ProviderID).Scan(&generation, &logical, &life, &creator)
				if err != nil || generation != 1 || logical != ref.LogicalName || creator == uuid.Nil {
					t.Fatalf("old history=%d/%s/%s %v", generation, logical, life, err)
				}
				if scenario == "preflight failure" && life != "PRESENT" {
					t.Fatal("preflight removed existing environment")
				}
				if scenario == "old residual with replacement" {
					expected := "DELETED"
					if index == 0 {
						expected = "PRESENT"
					}
					if life != expected {
						t.Fatalf("old residual history=%s want %s", life, expected)
					}
				}
				if wantGeneration == 2 && life != "DELETED" {
					t.Fatal("old generation not absent")
				}
			}
			var n int
			if err := e.db.QueryRow(t.Context(), `SELECT count(*) FROM provider_resources r JOIN operation_items i ON i.id=r.created_by_operation_item_id WHERE r.lab_instance_id=$1 AND r.generation=2 AND i.operation_id=$2`, instance, e.opID).Scan(&n); err != nil || n != expectedNew {
				t.Fatalf("new history=%d want %d: %v", n, expectedNew, err)
			}

			if scenario == "MISSING" || scenario == "ABSENT" || scenario == "DELETED" || scenario == "failed absent replacement" {
				state := scenario
				if scenario == "failed absent replacement" {
					state = "MISSING"
				}
				var n int
				if err := e.db.QueryRow(t.Context(), `SELECT count(*) FROM provider_resources WHERE lab_instance_id=$1 AND generation=2 AND observed_state=$2 AND lifecycle_status IN ('MISSING','DELETED')`, instance, state).Scan(&n); err != nil || n != 4 {
					t.Fatalf("absence history lost: %d %v", n, err)
				}
			}
			if wantStatus == "RECONCILING" {
				items, err := e.store.OperationItems(t.Context(), e.opID)
				if err != nil || len(items) != 1 || items[0].FinishedAt != nil {
					t.Fatalf("incomplete item finished: %+v %v", items, err)
				}
				op, err := e.store.OperationByID(t.Context(), e.opID)
				if err != nil || op.FinishedAt != nil {
					t.Fatal("incomplete parent finished")
				}
			}
			e.assertHTTPMatchesDatabase()
			if claimed, err := e.stack.Provisions.RunOne(t.Context()); claimed || err != nil {
				t.Fatalf("blind retry=%v %v", claimed, err)
			}
		})
	}
}

func TestCleanupIndependentItemsAndConfirmedAbsence(t *testing.T) {
	for _, scenario := range []string{"success", "partial failure", "unknown", "incomplete success"} {
		t.Run(scenario, func(t *testing.T) {
			e := newProvisionEnv(t, 5*time.Second)
			var target uuid.UUID
			e.startMock(&provider.MockProvider{
				ProvisionFunc: func(_ context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
					return mockProvisionResult(r, provider.OutcomeSucceeded), nil
				},
				CleanupFunc: func(_ context.Context, r provider.CleanupRequest) (provider.OperationResult, error) {
					result := provider.OperationResult{Outcome: provider.OutcomeSucceeded, ProviderResources: deletedRefs(r.ProviderResources)}
					if r.LabInstanceID == target.String() {
						switch scenario {
						case "partial failure":
							result.Outcome = provider.OutcomeFailed
							result.ProviderResources[0].ObservedState = "DELETE_FAILED"
						case "unknown":
							result.Outcome = provider.OutcomeUnknown
							result.ProviderResources = nil
						case "incomplete success":
							result.ProviderResources = nil
						}
					}
					return result, nil
				},
			})
			target = e.seedMutation()
			// A resource from an older generation remains cleanup's responsibility.
			labexecutiontest.Exec(t, e.db, `UPDATE lab_instances SET generation=2 WHERE id=$1`, target)
			e.cleanup("cleanup-confirmed-absence")
			for i := range 3 {
				e.runOneMustClaim()
				var finished bool
				if err := e.db.QueryRow(t.Context(), `SELECT finished_at IS NOT NULL FROM lab_executions WHERE id=$1`, e.executionID).Scan(&finished); err != nil {
					t.Fatal(err)
				}
				if finished != (scenario == "success" && i == 2) {
					t.Fatalf("premature/missing finish: item %d scenario %s finished %v", i, scenario, finished)
				}
			}
			items, err := e.store.OperationItems(t.Context(), e.opID)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range items {
				want := "SUCCEEDED"
				if item.LabInstanceID == target {
					switch scenario {
					case "partial failure":
						want = "FAILED"
					case "unknown", "incomplete success":
						want = "RECONCILING"
					}
				}
				if item.Status != want {
					t.Fatalf("item=%+v want %s", item, want)
				}
			}
			want := "SUCCEEDED"
			switch scenario {
			case "partial failure":
				want = "FAILED"
			case "unknown", "incomplete success":
				want = "RECONCILING"
			}
			if got := e.operationStatus(); got != want {
				t.Fatalf("parent=%s want %s", got, want)
			}
			e.assertHTTPMatchesDatabase()
			if claimed, err := e.stack.Provisions.RunOne(t.Context()); claimed || err != nil {
				t.Fatalf("replay=%v %v", claimed, err)
			}
			if scenario == "partial failure" {
				// A new explicit Cleanup can clean the known remainder without replaying
				// successful items' deletes. The registered instances still all participate.
				e.cleanup("cleanup-explicit-followup")
				for range 3 {
					e.runOneMustClaim()
				}
				if e.operationStatus() != "FAILED" {
					t.Fatalf("failure unexpectedly cleared: %s", e.operationStatus())
				}
			}
		})
	}
}

// The existing Terminal/Live boundary must run even when the subsequent Provider
// outcome is lost. The contract peer handles Terminal traffic and intentionally
// leaves Operation commands unanswered; no provider mutation is retried.
func TestMutationWorkerClosesTerminalAndSourceLive(t *testing.T) {
	for _, kind := range []string{"RESET", "CLEANUP"} {
		t.Run(kind, func(t *testing.T) {
			e := newTerminalEnv(t, func(o *stackOptions) { o.Worker = true; o.ProvisionResultTimeout = 100 * time.Millisecond })
			f := e.fixture
			instance := f.AddLabInstanceWithSnapshot(t, e.conn, strings.ReplaceAll(terminaltest.Snapshot("workspace", terminaltest.DefaultVMs()), terminaltest.SnapshotProviderConnMark, f.ProviderConnectionID.String()))
			f.AddResource(t, e.conn, instance, 1, "SERVER", "workspace", "PRESENT")
			terminaltest.Exec(t, e.conn, `UPDATE lab_executions SET finished_at=clock_timestamp() WHERE id=$1`, f.LabExecutionID)
			terminaltest.Exec(t, e.conn, `UPDATE lab_executions SET finished_at=NULL,status='ACTIVE' WHERE id=(SELECT lab_execution_id FROM lab_instances WHERE id=$1)`, instance)
			cookie := e.ownerCookie
			if kind == "CLEANUP" {
				terminaltest.Exec(t, e.conn, `UPDATE lab_instances SET user_id=$2,participant_role='INSTRUCTOR' WHERE id=$1`, instance, f.InstructorID)
				cookie = terminaltest.LoginSession(t, e.conn, f.InstructorID)
			}
			source := e.createSession(cookie, instance)
			browser, _ := e.attach(source)
			var liveID string
			if kind == "CLEANUP" {
				response := e.request(http.MethodPost, "/api/v1/terminal-sessions/"+source.ID+"/live-sessions", cookie, "")
				if response.Status != http.StatusCreated {
					t.Fatalf("live create=%d %s", response.Status, response.Body)
				}
				liveID = response.json(t)["id"].(string)
			}
			pool, err := postgres.OpenPool(t.Context(), e.dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			store := postgres.NewStore(pool)
			var execution uuid.UUID
			if err := e.conn.QueryRow(t.Context(), `SELECT lab_execution_id FROM lab_instances WHERE id=$1`, instance).Scan(&execution); err != nil {
				t.Fatal(err)
			}
			in := operation.Registration{OrganizationID: f.OrganizationID, RequestedByUserID: f.InstructorID, Type: kind, IdempotencyKey: "mutation-terminal-live-close", Fingerprint: operation.SemanticFingerprint([]byte(kind))}
			generation := int64(1)
			if kind == "RESET" {
				in.LabInstanceID = &instance
				generation = 2
			} else {
				in.LabExecutionID = &execution
			}
			in.Items = []operation.Item{{LabInstanceID: instance, Generation: generation}}
			err = store.WithinTransaction(t.Context(), func(ctx context.Context, r repository.Repositories) error {
				_, err := operation.Register(ctx, r, in, time.Now())
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if claimed, err := e.stack.Provisions.RunOne(t.Context()); !claimed || err != nil {
				t.Fatalf("mutation=%v %v", claimed, err)
			}
			ended := browser.json()
			reason := "LAB_" + kind
			if ended["type"] != "TERMINAL_SESSION_ENDED" || ended["payload"].(map[string]any)["reason"] != reason {
				t.Fatalf("terminal end=%v", ended)
			}
			row := e.row(source.ID)
			if row.Status != "ENDED" || (row.EndReason == nil || *row.EndReason != reason) {
				t.Fatalf("terminal persisted end=%+v", row)
			}
			if liveID != "" {
				var ended bool
				var gotReason string
				if err := e.conn.QueryRow(t.Context(), `SELECT ended_at IS NOT NULL,end_reason FROM live_sessions WHERE id=$1`, liveID).Scan(&ended, &gotReason); err != nil || !ended || gotReason != reason {
					t.Fatalf("live end=%v %s %v", ended, gotReason, err)
				}
			}
		})
	}
}
