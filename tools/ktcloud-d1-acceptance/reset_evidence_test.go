package main

import (
	"testing"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

func TestResetRealityRejectsCountOnlyFalsePass(t *testing.T) {
	old := []protocol.ProviderResourceRef{
		{ResourceType: "SERVER", ProviderID: "old-server", Generation: 1, LogicalName: "workspace"},
		{ResourceType: "KT_TIER", ProviderID: "old-tier", Generation: 1, LogicalName: "lab-tier"},
	}
	replacement := []protocol.ProviderResourceRef{
		{ResourceType: "SERVER", ProviderID: "new-server", Generation: 2, LogicalName: "workspace"},
		{ResourceType: "KT_TIER", ProviderID: "new-tier", Generation: 2, LogicalName: "lab-tier"},
	}
	baseline := []protocol.ResourceObservation{}
	for _, ref := range append(append([]protocol.ProviderResourceRef{}, old...), replacement...) {
		exists, state := ref.Generation == 2, "ABSENT"
		if exists {
			state = "ACTIVE"
		}
		baseline = append(baseline, protocol.ResourceObservation{ResourceType: ref.ResourceType, ProviderID: ref.ProviderID, Generation: ref.Generation, LogicalName: ref.LogicalName, Exists: exists, ObservedState: state, Source: "KNOWN_RESOURCE"})
	}
	if err := confirmResetReality(old, replacement, baseline); err != nil {
		t.Fatal(err)
	}
	if err := confirmKnownPresence(replacement, baseline[2:]); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func([]protocol.ResourceObservation) []protocol.ResourceObservation{
		"duplicate g1 hides missing old Tier": func(o []protocol.ResourceObservation) []protocol.ResourceObservation { o[1] = o[0]; return o },
		"duplicate g2 hides missing new Tier": func(o []protocol.ResourceObservation) []protocol.ResourceObservation { o[3] = o[2]; return o },
		"foreign known ID": func(o []protocol.ResourceObservation) []protocol.ResourceObservation {
			o[3].ProviderID = "unrelated-tier"
			return o
		},
		"wrong generation": func(o []protocol.ResourceObservation) []protocol.ResourceObservation { o[3].Generation = 1; return o },
		"wrong logical owner": func(o []protocol.ResourceObservation) []protocol.ResourceObservation {
			o[3].LogicalName = "other"
			return o
		},
		"candidate instead of known": func(o []protocol.ResourceObservation) []protocol.ResourceObservation {
			o[3].Source = "DISCOVERED_CANDIDATE"
			return o
		},
		"unknown replacement state": func(o []protocol.ResourceObservation) []protocol.ResourceObservation {
			o[3].ObservedState = "UNKNOWN"
			return o
		},
		"old still present": func(o []protocol.ResourceObservation) []protocol.ResourceObservation {
			o[0].Exists = true
			o[0].ObservedState = "ACTIVE"
			return o
		},
		"replacement absent": func(o []protocol.ResourceObservation) []protocol.ResourceObservation {
			o[3].Exists = false
			o[3].ObservedState = "ABSENT"
			return o
		},
		"missing observation": func(o []protocol.ResourceObservation) []protocol.ResourceObservation { return o[:3] },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			observations := change(append([]protocol.ResourceObservation{}, baseline...))
			if confirmResetReality(old, replacement, observations) == nil {
				t.Fatal("Reset false-positive inventory accepted")
			}
		})
	}
	if confirmResetReality(nil, replacement, baseline[2:]) == nil {
		t.Fatal("missing old generation accepted")
	}
	for _, index := range []int{2, 3} {
		observations := append([]protocol.ResourceObservation{}, baseline[2:]...)
		observations[index-2].ProviderID = "foreign"
		if confirmKnownPresence(replacement, observations) == nil {
			t.Fatal("foreign replacement passed empty Cleanup protection")
		}
	}
}
