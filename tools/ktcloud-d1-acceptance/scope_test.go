package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

func TestInvalidScopeStopsBeforeFixtureRegistration(t *testing.T) {
	for _, value := range []string{"0", "3", "01", " 1", "-1"} {
		t.Run(value, func(t *testing.T) {
			directory := t.TempDir()
			t.Setenv("LABBIT_KTCLOUD_D1_CONTROL_TEST", "1")
			t.Setenv("LBT145_CONTROL_DIRECTORY", directory)
			t.Setenv("LBT145_CONTROL_GENERATIONS", value)
			if run() == nil {
				t.Fatal("invalid scope accepted")
			}
			if _, err := os.Stat(filepath.Join(directory, "control-ledger.json")); !os.IsNotExist(err) {
				t.Fatal("invalid scope registered a fixture")
			}
		})
	}
}

func TestUncertainCleanupObservesBeforeAnyRetry(t *testing.T) {
	known := []protocol.ProviderResourceRef{
		{ResourceType: "SERVER", ProviderID: "server", Generation: 1, LogicalName: "workspace"},
		{ResourceType: "KT_TIER", ProviderID: "tier", Generation: 1, LogicalName: "lab-tier"},
	}
	cases := []struct {
		name        string
		lookupError bool
		state       string
		candidate   bool
		wantError   bool
		wantCleanup bool
	}{
		{name: "response lost but all absent", state: "ABSENT"},
		{name: "only confirmed remaining Tier retried", state: "PRESENT", wantCleanup: true},
		{name: "lookup failed stops mutation", lookupError: true, wantError: true},
		{name: "unknown stops mutation", state: "UNKNOWN", wantError: true},
		{name: "candidate stops mutation", state: "PRESENT", candidate: true, wantError: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			observed, mutated := false, false
			err := cleanupAfterReconcile(context.Background(), known,
				func(_ context.Context, ids []protocol.ProviderResourceRef) ([]protocol.ResourceObservation, error) {
					observed = true
					if len(ids) != 2 || ids[0] != known[0] || ids[1] != known[1] {
						t.Fatal("ownership refs changed")
					}
					if tt.lookupError {
						return nil, errors.New("lookup failed")
					}
					observations := []protocol.ResourceObservation{}
					for i, ref := range ids {
						state, exists, source := "ABSENT", false, "KNOWN_RESOURCE"
						if i == 1 {
							state = tt.state
							exists = state == "PRESENT"
							if tt.candidate {
								source = "DISCOVERED_CANDIDATE"
							}
						}
						observations = append(observations, protocol.ResourceObservation{ResourceType: ref.ResourceType, ProviderID: ref.ProviderID, Generation: ref.Generation, LogicalName: ref.LogicalName, Source: source, ObservedState: state, Exists: exists})
					}
					return observations, nil
				},
				func(_ context.Context, remaining []protocol.ProviderResourceRef) error {
					if !observed || len(remaining) != 1 || remaining[0] != known[1] {
						t.Fatal("cleanup before exact observation or includes absent server")
					}
					mutated = true
					return nil
				})
			if (err != nil) != tt.wantError || mutated != tt.wantCleanup || !observed {
				t.Fatal("unsafe teardown decision")
			}
		})
	}
}

func TestDefaultScopeRetainsReset(t *testing.T) {
	for _, value := range []string{"", "2"} {
		if got, err := plannedGenerations(value); err != nil || got != 2 {
			t.Fatal("default full acceptance scope changed")
		}
	}
	if got, err := plannedGenerations("1"); err != nil || got != 1 {
		t.Fatal("explicit single generation unavailable")
	}
}

func TestKnownAbsenceRejectsFalsePositiveInventory(t *testing.T) {
	known := []protocol.ProviderResourceRef{
		{ResourceType: "KT_TIER", ProviderID: "owned-lab-tier", Generation: 1, LogicalName: "lab-tier"},
		{ResourceType: "KT_TIER", ProviderID: "owned-management-tier", Generation: 1, LogicalName: "workspace:management-tier"},
	}
	baseline := []protocol.ResourceObservation{}
	for _, ref := range known {
		baseline = append(baseline, protocol.ResourceObservation{ResourceType: ref.ResourceType, ProviderID: ref.ProviderID, Generation: ref.Generation, LogicalName: ref.LogicalName, Source: "KNOWN_RESOURCE", ObservedState: "ABSENT"})
	}
	if err := confirmKnownAbsence(known, baseline); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func([]protocol.ResourceObservation) []protocol.ResourceObservation{
		"duplicate hides missing ID": func(o []protocol.ResourceObservation) []protocol.ResourceObservation { o[1] = o[0]; return o },
		"unrelated ID": func(o []protocol.ResourceObservation) []protocol.ResourceObservation {
			o[1].ProviderID = "shared-tier"
			return o
		},
		"wrong generation": func(o []protocol.ResourceObservation) []protocol.ResourceObservation { o[1].Generation = 2; return o },
		"candidate": func(o []protocol.ResourceObservation) []protocol.ResourceObservation {
			o[1].Source = "DISCOVERED_CANDIDATE"
			return o
		},
		"still present": func(o []protocol.ResourceObservation) []protocol.ResourceObservation { o[1].Exists = true; return o },
		"unknown state": func(o []protocol.ResourceObservation) []protocol.ResourceObservation {
			o[1].ObservedState = "UNKNOWN"
			return o
		},
		"wrong logical ownership": func(o []protocol.ResourceObservation) []protocol.ResourceObservation {
			o[1].LogicalName = "other:management-tier"
			return o
		},
		"missing observation": func(o []protocol.ResourceObservation) []protocol.ResourceObservation { return o[:1] },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			observations := append([]protocol.ResourceObservation(nil), baseline...)
			if confirmKnownAbsence(known, change(observations)) == nil {
				t.Fatal("incomplete or mismatched absence accepted")
			}
		})
	}
	if confirmKnownAbsence(nil, nil) == nil || confirmKnownAbsence([]protocol.ProviderResourceRef{known[0], known[0]}, baseline) == nil {
		t.Fatal("empty or duplicate ownership accepted")
	}
}
