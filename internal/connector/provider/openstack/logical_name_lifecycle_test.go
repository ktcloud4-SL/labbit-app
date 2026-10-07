package openstackprovider

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

// Logical names retain the snapshot key, not the bounded Nova/Neutron name.
// Every key accepted by Provision must yield resources its lifecycle accepts.
func TestProvisionLongVMKeyLifecycleRoundTrip(t *testing.T) {
	for _, length := range []int{244, 245, 255} {
		for _, operation := range []string{"Cleanup", "Reconcile", "Reset"} {
			t.Run(fmt.Sprintf("key_%d/%s", length, operation), func(t *testing.T) {
				fake := &m3ResetFake{base: &m2OpenStackFake{t: t}, deleted: make(map[string]bool)}
				adapter := newTestAdapter(t, fake)
				adapter.provision = testProvisionConfig(t)
				adapter.sshProbe = func(context.Context, string) error { return nil }
				adapter.startupProbe = func(context.Context, string, string) error { return nil }
				snapshot := validSnapshot()
				vmKey := strings.Repeat("x", length)
				snapshot.VMs[0].VMKey = vmKey
				snapshot.WorkspaceVMKey = vmKey
				correlation := coreprovider.Correlation{OperationID: "provision-long-key", LabInstanceID: "lab-instance-1", Generation: 1}
				first, err := adapter.Provision(context.Background(), coreprovider.ProvisionRequest{
					Correlation: correlation, CreationSnapshot: snapshot,
				})
				if err != nil || first.Outcome != coreprovider.OutcomeSucceeded || len(first.ProviderResources) != 8 {
					t.Fatalf("Provision() outcome=%s resources=%d safeError=%+v err=%v", first.Outcome, len(first.ProviderResources), first.Error, err)
				}
				resources := make([]coreprovider.ResourceRef, 0, len(first.ProviderResources))
				managementFound := false
				for _, resource := range first.ProviderResources {
					resources = append(resources, resource.ResourceRef)
					if resource.ResourceType == coreprovider.ResourceTypePort && resource.LogicalName == vmKey+":management" {
						managementFound = true
					}
				}
				if !managementFound {
					t.Fatal("Provision lost the snapshot-based management port logical name")
				}
				correlation.OperationID = strings.ToLower(operation) + "-long-key"
				switch operation {
				case "Reset":
					correlation.Generation = 2
					reset, err := adapter.Reset(context.Background(), coreprovider.ResetRequest{
						Correlation: correlation, CreationSnapshot: snapshot, ProviderResources: resources,
					})
					if err != nil || reset.Outcome != coreprovider.OutcomeSucceeded || len(reset.ProviderResources) != 16 {
						t.Fatalf("Reset() outcome=%s resources=%d safeError=%+v err=%v", reset.Outcome, len(reset.ProviderResources), reset.Error, err)
					}
					resources = resources[:0]
					for _, resource := range reset.ProviderResources {
						if resource.Generation == 1 {
							if resource.ObservedState != stateDeleted {
								t.Fatal("Reset did not confirm old-generation deletion")
							}
						} else if resource.Generation == 2 {
							resources = append(resources, resource.ResourceRef)
						} else {
							t.Fatalf("unexpected Reset generation %d", resource.Generation)
						}
					}
					if len(resources) != 8 || len(fake.deleteOrder) != 8 {
						t.Fatal("Reset must delete all old resources and return all new resources")
					}
				case "Reconcile":
					assertLongKeyObservations(t, adapter, correlation, resources, true)
				}
				cleanup, err := adapter.Cleanup(context.Background(), coreprovider.CleanupRequest{
					Correlation: correlation, ProviderResources: resources,
				})
				if err != nil || cleanup.Outcome != coreprovider.OutcomeSucceeded || len(cleanup.ProviderResources) != len(resources) {
					t.Fatalf("Cleanup() outcome=%s resources=%d safeError=%+v err=%v", cleanup.Outcome, len(cleanup.ProviderResources), cleanup.Error, err)
				}
				for _, resource := range cleanup.ProviderResources {
					if resource.ObservedState != stateDeleted {
						t.Fatalf("Cleanup did not confirm deletion: %+v", resource)
					}
				}
				assertLongKeyObservations(t, adapter, correlation, resources, false)
			})
		}
	}
}

func assertLongKeyObservations(t *testing.T, adapter *Adapter, correlation coreprovider.Correlation, resources []coreprovider.ResourceRef, exists bool) {
	t.Helper()
	result, err := adapter.Reconcile(context.Background(), coreprovider.ReconcileRequest{
		Correlation: correlation, KnownResources: resources,
	})
	if err != nil || result.Error != nil || len(result.Observations) != len(resources) {
		t.Fatalf("Reconcile() observations=%d safeError=%+v err=%v", len(result.Observations), result.Error, err)
	}
	for _, observation := range result.Observations {
		if observation.Exists != exists || (!exists && observation.ObservedState != stateAbsent) {
			t.Fatalf("Reconcile() observation=%+v want exists=%v", observation, exists)
		}
	}
}

func TestProvisionOversizedVMKeyStillRejectedBeforeCloudCalls(t *testing.T) {
	calls := 0
	adapter := newTestAdapter(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	adapter.provision = testProvisionConfig(t)
	snapshot := validSnapshot()
	snapshot.VMs[0].VMKey = strings.Repeat("x", 256)
	snapshot.WorkspaceVMKey = snapshot.VMs[0].VMKey
	result, err := adapter.Provision(context.Background(), coreprovider.ProvisionRequest{
		Correlation: coreprovider.Correlation{OperationID: "oversized-key", LabInstanceID: "lab-instance-1", Generation: 1}, CreationSnapshot: snapshot,
	})
	if err != nil || result.Outcome != coreprovider.OutcomeFailed || calls != 0 {
		t.Fatalf("Provision() outcome=%s cloudCalls=%d err=%v", result.Outcome, calls, err)
	}
}
