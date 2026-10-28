package openstackprovider

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

func TestKTCloudCIDRCleanupReturnRestartAndDelayedCommands(t *testing.T) {
	a := pooledKTAdapter(t, nil)
	first := claimCIDR(t, a, "old", 1, false)
	claimCIDR(t, a, "teacher", 1, false)
	request := coreprovider.CleanupRequest{Correlation: cidrCorrelation("old", 1), ProviderResources: []coreprovider.ResourceRef{}}
	result, err := a.Cleanup(context.Background(), request)
	if err != nil || result.Outcome != coreprovider.OutcomeSucceeded {
		t.Fatalf("cleanup: %+v %v", result, err)
	}
	restarted := pooledKTAdapter(t, nil)
	restarted.provision = a.provision
	if pair := claimCIDR(t, restarted, "new", 1, false); pair != first {
		t.Fatal("pair not recycled after restart")
	}
	for _, tc := range []struct {
		gen   int64
		reset bool
	}{{1, false}, {2, true}} {
		result, _ := restarted.withCIDRScope(context.Background(), cidrCorrelation("old", tc.gen), tc.reset, func(*Adapter) (coreprovider.OperationResult, error) {
			t.Fatal("retired owner ran")
			return coreprovider.OperationResult{}, nil
		})
		if result.Outcome != coreprovider.OutcomeUnknown {
			t.Fatal("retired request not fenced")
		}
	}
	// A retry is answered from the tombstone, without provider IO or releasing new.
	restarted.ktNetwork.client.Endpoint += "unavailable/"
	result, _ = restarted.Cleanup(context.Background(), request)
	if result.Outcome != coreprovider.OutcomeSucceeded {
		t.Fatalf("duplicate cleanup: %+v", result)
	}
	if pair, err := restarted.loadCIDRAssignment("new"); err != nil || pair != first {
		t.Fatal("old cleanup freed new owner")
	}
	request.ProviderResources = []coreprovider.ResourceRef{{ResourceType: coreprovider.ResourceTypeTier, ProviderID: "foreign", Generation: 1, LogicalName: "lab-tier"}}
	result, _ = restarted.Cleanup(context.Background(), request)
	if result.Outcome != coreprovider.OutcomeUnknown {
		t.Fatal("tombstone attested an unrecorded resource")
	}
}

func TestKTCloudCIDRCleanupPreservesUncertainReservations(t *testing.T) {
	for _, scenario := range []string{"unknown create", "legacy intent", "crashed completion", "server without root", "inventory occupied", "inventory failure", "unlisted live resource"} {
		t.Run(scenario, func(t *testing.T) {
			tiers := []ktCloudTier{}
			if scenario == "inventory occupied" {
				tiers = append(tiers, ktCloudTier{ID: "foreign", CIDR: "172.25.240.0/24"})
			}
			a := pooledKTAdapter(t, nil)
			correlation := cidrCorrelation("old", 1)
			result, _ := a.withCIDRScope(context.Background(), correlation, false, func(scoped *Adapter) (coreprovider.OperationResult, error) {
				if err := scoped.startCIDRMutation(correlation); err != nil {
					t.Fatal(err)
				}
				out := coreprovider.OperationResult{Outcome: coreprovider.OutcomeSucceeded}
				if scenario == "unknown create" {
					out.Outcome = coreprovider.OutcomeUnknown
				}
				if scenario == "server without root" {
					out.Outcome = coreprovider.OutcomeFailed
					out.ProviderResources = resourceResults([]coreprovider.ResourceRef{{ResourceType: coreprovider.ResourceTypeServer, ProviderID: "server", Generation: 1, LogicalName: "vm"}}, "ERROR")
				}
				if scenario == "unlisted live resource" {
					out.ProviderResources = resourceResults([]coreprovider.ResourceRef{{ResourceType: coreprovider.ResourceTypeTier, ProviderID: "live", Generation: 1, LogicalName: "lab-tier"}}, "ACTIVE")
				}
				return out, nil
			})
			if result.Outcome == "" {
				t.Fatal("claim did not run")
			}
			if scenario == "legacy intent" || scenario == "crashed completion" {
				if err := os.Remove(filepath.Join(a.provision.KTCloudCIDRStateDir, completionName(0, 1))); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "crashed completion" {
				if err := os.WriteFile(filepath.Join(a.provision.KTCloudCIDRStateDir, completionName(0, 1)), []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "unlisted live resource" {
				tiers = append(tiers, ktCloudTier{ID: "live", CIDR: "172.25.250.0/24"})
			}
			observer := pooledKTAdapter(t, tiers)
			observer.provision = a.provision
			if scenario == "inventory failure" {
				observer.ktNetwork.client.Endpoint += "unavailable/"
			}
			observer.Cleanup(context.Background(), coreprovider.CleanupRequest{Correlation: correlation, ProviderResources: []coreprovider.ResourceRef{}})
			if _, err := observer.loadCIDRAssignment("old"); err != nil {
				t.Fatal("uncertain reservation was released")
			}
			if _, err := os.Lstat(filepath.Join(a.provision.KTCloudCIDRStateDir, retiredName("old"))); !os.IsNotExist(err) {
				t.Fatal("uncertain owner was retired")
			}
		})
	}
}

func TestKTCloudCIDRCleanupAllGenerationsAndStaleCleanup(t *testing.T) {
	a := pooledKTAdapter(t, nil)
	pair := claimCIDR(t, a, "old", 1, false)
	claimCIDR(t, a, "old", 2, true)
	result, _ := a.Cleanup(context.Background(), coreprovider.CleanupRequest{Correlation: cidrCorrelation("old", 1), ProviderResources: []coreprovider.ResourceRef{}})
	if result.Outcome != coreprovider.OutcomeUnknown {
		t.Fatal("stale cleanup accepted")
	}
	if current, err := a.loadCIDRAssignment("old"); err != nil || current != pair {
		t.Fatal("reset lost pair")
	}
	result, _ = a.Cleanup(context.Background(), coreprovider.CleanupRequest{Correlation: cidrCorrelation("old", 2), ProviderResources: []coreprovider.ResourceRef{}})
	if result.Outcome != coreprovider.OutcomeSucceeded {
		t.Fatalf("cleanup: %+v", result)
	}
	if current := claimCIDR(t, a, "new", 1, false); current != pair {
		t.Fatal("multi-generation pair not returned")
	}
	retired, err := readCIDRRetired(ktCIDRPool{Pairs: []ktCIDRPair{pair}}, a.provision.KTCloudCIDRStateDir, retiredName("old"))
	if err != nil || len(retired.Completions) != 2 {
		t.Fatal("generation history lost")
	}
}

func TestKTCloudCIDRCleanupInterruptedReleaseAndExclusiveLock(t *testing.T) {
	for _, scenario := range []string{"tombstone before removal", "lock"} {
		t.Run(scenario, func(t *testing.T) {
			a := pooledKTAdapter(t, nil)
			pair := claimCIDR(t, a, "old", 1, false)
			dir := a.provision.KTCloudCIDRStateDir
			if scenario == "lock" {
				if err := writeCIDRFile(dir, "LOCK", cidrCorrelation("other", 1)); err != nil {
					t.Fatal(err)
				}
			} else {
				retired := ktCIDRRetired{Assignment: ktCIDRAssignment{Instance: "old", Pair: pair}, Slot: 0, Cleanup: cidrCorrelation("old", 1)}
				if err := writeCIDRFile(dir, retiredName("old"), retired); err != nil {
					t.Fatal(err)
				}
			}
			result, _ := a.Cleanup(context.Background(), coreprovider.CleanupRequest{Correlation: cidrCorrelation("old", 1), ProviderResources: []coreprovider.ResourceRef{}})
			if result.Outcome != coreprovider.OutcomeUnknown {
				t.Fatal("incomplete release did not HOLD")
			}
			if _, err := a.loadCIDRAssignment("old"); err != nil {
				t.Fatal("held assignment removed")
			}
		})
	}
}

func TestKTCloudCIDRCleanupPartialDeleteKeepsReservation(t *testing.T) {
	seed := pooledKTAdapter(t, nil)
	claimCIDR(t, seed, "old", 1, false)
	a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(403)
	})
	a.provision = seed.provision
	result, _ := a.Cleanup(context.Background(), coreprovider.CleanupRequest{Correlation: cidrCorrelation("old", 1), ProviderResources: []coreprovider.ResourceRef{{ResourceType: coreprovider.ResourceTypeTier, ProviderID: "owned", Generation: 1, LogicalName: "lab-tier"}}})
	if result.Outcome != coreprovider.OutcomeFailed {
		t.Fatalf("rejected cleanup: %+v", result)
	}
	if _, err := a.loadCIDRAssignment("old"); err != nil {
		t.Fatal("partial cleanup released reservation")
	}
}

func TestKTCloudCIDRCleanupSerializesWithAllocation(t *testing.T) {
	seed := pooledKTAdapter(t, nil)
	claimCIDR(t, seed, "old", 1, false)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	a := ktTopologyAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/compute/v2/nsm/network" {
			t.Errorf("unexpected cloud call: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
			return
		}
		once.Do(func() { close(entered); <-release })
		writeJSON(t, w, 200, map[string]any{"httpStatus": 200, "pagination": map[string]int{"total": 0, "offset": 0}, "data": []any{}})
	})
	a.provision = seed.provision
	done := make(chan coreprovider.OperationResult, 1)
	go func() {
		result, _ := a.Cleanup(context.Background(), coreprovider.CleanupRequest{Correlation: cidrCorrelation("old", 1), ProviderResources: []coreprovider.ResourceRef{}})
		done <- result
	}()
	<-entered
	result, _ := a.withCIDRScope(context.Background(), cidrCorrelation("new", 1), false, func(*Adapter) (coreprovider.OperationResult, error) {
		t.Error("allocation raced Cleanup")
		return coreprovider.OperationResult{}, nil
	})
	if result.Outcome != coreprovider.OutcomeFailed {
		t.Error("concurrent allocation was not rejected")
	}
	close(release)
	if result := <-done; result.Outcome != coreprovider.OutcomeSucceeded {
		t.Fatalf("cleanup: %+v", result)
	}
	if pair := claimCIDR(t, a, "new", 1, false); pair.Lab != "172.25.240.0/24" {
		t.Fatal("pair unavailable after lock release")
	}
}
