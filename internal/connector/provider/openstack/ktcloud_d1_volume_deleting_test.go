package openstackprovider

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

func TestKTCloudRootDeleteRejectionUsesFreshObservationWithoutRetry(t *testing.T) {
	for _, status := range []int{400, 409, 403, 500} {
		for _, after := range []string{"absent", "deleting", "available", "lookupFailure", "wrongID", "attached", "emptyStatus", "staysDeleting"} {
			t.Run(fmt.Sprintf("%d/%s", status, after), func(t *testing.T) {
				var gets, deletes atomic.Int32
				a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
					if !strings.HasSuffix(r.URL.Path, "/volumes/root-id") {
						t.Errorf("unexpected resource %s", r.URL.Path)
						w.WriteHeader(500)
						return
					}
					if r.Method == http.MethodDelete {
						deletes.Add(1)
						w.WriteHeader(status)
						return
					}
					if r.Method != http.MethodGet {
						t.Errorf("unexpected method %s", r.Method)
						w.WriteHeader(500)
						return
					}
					n := gets.Add(1)
					item := map[string]any{"id": "root-id", "status": "available", "attachments": []any{}}
					if n > 1 {
						switch after {
						case "absent":
							w.WriteHeader(404)
							return
						case "lookupFailure":
							w.WriteHeader(503)
							return
						case "deleting", "staysDeleting":
							if n >= 4 && after == "deleting" {
								w.WriteHeader(404)
								return
							}
							item["status"] = "deleting"
						case "wrongID":
							item["id"] = "other"
						case "attached":
							item["attachments"] = []any{map[string]any{"server_id": "other"}}
						case "emptyStatus":
							item["status"] = ""
						}
					}
					writeJSON(t, w, 200, map[string]any{"volume": item})
				})
				a.provision.ActiveTimeout = 200 * time.Millisecond
				result, err := a.Cleanup(context.Background(), coreprovider.CleanupRequest{
					Correlation:       coreprovider.Correlation{OperationID: "cleanup", LabInstanceID: "lab", Generation: 2},
					ProviderResources: []coreprovider.ResourceRef{{ResourceType: coreprovider.ResourceTypeVolume, ProviderID: "root-id", Generation: 2, LogicalName: "workspace:root-volume"}},
				})
				want := coreprovider.OutcomeUnknown
				reobserve := status == 400 || status == 409
				if status == 403 || reobserve && after == "available" {
					want = coreprovider.OutcomeFailed
				} else if reobserve && (after == "absent" || after == "deleting") {
					want = coreprovider.OutcomeSucceeded
				}
				if err != nil || result.Outcome != want || deletes.Load() != 1 || reobserve && gets.Load() < 2 || !reobserve && gets.Load() != 1 {
					t.Fatalf("outcome=%s want=%s err=%v GET=%d DELETE=%d", result.Outcome, want, err, gets.Load(), deletes.Load())
				}
			})
		}
	}
}

func TestKTCloudDeletingRootWaitsForActualAbsenceWithoutDuplicateDelete(t *testing.T) {
	for _, converges := range []bool{true, false} {
		t.Run(map[bool]string{true: "converges", false: "staysDeleting"}[converges], func(t *testing.T) {
			var gets, deletes atomic.Int32
			a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/volumes/root-id") {
					t.Errorf("unexpected resource %s", r.URL.Path)
					w.WriteHeader(500)
					return
				}
				if r.Method == http.MethodDelete {
					deletes.Add(1)
					w.WriteHeader(http.StatusConflict)
					return
				}
				if r.Method != http.MethodGet {
					t.Errorf("unexpected method %s", r.Method)
					w.WriteHeader(500)
					return
				}
				if gets.Add(1) >= 3 && converges {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				writeJSON(t, w, 200, map[string]any{"volume": map[string]any{"id": "root-id", "status": "deleting", "attachments": []any{}}})
			})
			a.provision.ActiveTimeout = 200 * time.Millisecond
			result, err := a.Cleanup(context.Background(), coreprovider.CleanupRequest{
				Correlation:       coreprovider.Correlation{OperationID: "cleanup", LabInstanceID: "lab", Generation: 2},
				ProviderResources: []coreprovider.ResourceRef{{ResourceType: coreprovider.ResourceTypeVolume, ProviderID: "root-id", Generation: 2, LogicalName: "workspace:root-volume"}},
			})
			wantOutcome, wantState := coreprovider.OutcomeSucceeded, stateDeleted
			if !converges {
				wantOutcome, wantState = coreprovider.OutcomeUnknown, stateDeleteUnknown
			}
			if err != nil || result.Outcome != wantOutcome || len(result.ProviderResources) != 1 || result.ProviderResources[0].ObservedState != wantState || deletes.Load() != 0 || gets.Load() < 2 {
				t.Fatalf("outcome=%s err=%v resources=%v GET=%d DELETE=%d", result.Outcome, err, result.ProviderResources, gets.Load(), deletes.Load())
			}
		})
	}
}
