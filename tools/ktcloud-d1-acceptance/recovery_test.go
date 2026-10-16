package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

func recoveryInventory() ([]protocol.ProviderResourceRef, []protocol.ResourceObservation) {
	known := []protocol.ProviderResourceRef{
		{ResourceType: "SERVER", ProviderID: "owned-server", Generation: 2, LogicalName: "workspace"},
		{ResourceType: "KT_TIER", ProviderID: "owned-tier", Generation: 1, LogicalName: "lab-tier"},
	}
	observations := []protocol.ResourceObservation{}
	for _, ref := range known {
		observations = append(observations, protocol.ResourceObservation{ResourceType: ref.ResourceType, ProviderID: ref.ProviderID, Generation: ref.Generation, LogicalName: ref.LogicalName, Source: "KNOWN_RESOURCE", Exists: true, ObservedState: "ACTIVE"})
	}
	return known, observations
}

func TestRecoveryAfterReadOnlyDeletedLedgerRechecksAllIDs(t *testing.T) {
	for _, currentlyPresent := range []bool{false, true} {
		t.Run(map[bool]string{false: "allAbsent", true: "reappeared"}[currentlyPresent], func(t *testing.T) {
			known, baseline := recoveryInventory()
			items := make([]protocol.ProviderResourceResult, 0, len(known))
			for _, ref := range known {
				items = append(items, protocol.ProviderResourceResult{ProviderResourceRef: ref, ObservedState: "DELETED"})
			}
			observes, cleanups := 0, 0
			err := recoverKnownResources(context.Background(), recoveryResourceRefs(items), false, recoveryActions{
				observe: func(_ context.Context, _ string, requested []protocol.ProviderResourceRef) ([]protocol.ResourceObservation, error) {
					if !reflect.DeepEqual(requested, known) {
						t.Fatal("prior DELETED entries disappeared from current exact-ID query")
					}
					observes++
					result := append([]protocol.ResourceObservation(nil), baseline...)
					for i := range result {
						result[i].Exists = false
						result[i].ObservedState = "ABSENT"
					}
					if currentlyPresent && observes == 1 {
						result[0].Exists = true
						result[0].ObservedState = "ACTIVE"
					}
					return result, nil
				},
				cleanup: func(_ context.Context, requested []protocol.ProviderResourceRef) error {
					cleanups++
					if !currentlyPresent || !reflect.DeepEqual(requested, known[:1]) {
						t.Fatal("cleanup did not use fresh presence only")
					}
					return nil
				},
				record: func([]protocol.ResourceObservation) {},
				save:   func(string) error { return nil },
			})
			if err != nil || observes != 2 || cleanups != map[bool]int{false: 0, true: 1}[currentlyPresent] {
				t.Fatalf("recovery=%v observes=%d cleanups=%d", err, observes, cleanups)
			}
		})
	}
}

func TestRecoveryRejectsUncertainInventoryWithoutMutationOrFalseSuccess(t *testing.T) {
	changes := map[string]func([]protocol.ResourceObservation) []protocol.ResourceObservation{
		"empty":     func(o []protocol.ResourceObservation) []protocol.ResourceObservation { return nil },
		"missing":   func(o []protocol.ResourceObservation) []protocol.ResourceObservation { return o[:1] },
		"duplicate": func(o []protocol.ResourceObservation) []protocol.ResourceObservation { o[1] = o[0]; return o },
		"foreign": func(o []protocol.ResourceObservation) []protocol.ResourceObservation {
			o[1].ProviderID = "foreign-tier"
			return o
		},
		"generation": func(o []protocol.ResourceObservation) []protocol.ResourceObservation { o[1].Generation = 2; return o },
		"logical owner": func(o []protocol.ResourceObservation) []protocol.ResourceObservation {
			o[1].LogicalName = "other"
			return o
		},
		"resource type": func(o []protocol.ResourceObservation) []protocol.ResourceObservation {
			o[1].ResourceType = "SERVER"
			return o
		},
		"candidate": func(o []protocol.ResourceObservation) []protocol.ResourceObservation {
			o[1].Source = "DISCOVERED_CANDIDATE"
			return o
		},
		"unknown": func(o []protocol.ResourceObservation) []protocol.ResourceObservation {
			o[1].ObservedState = "UNKNOWN"
			return o
		},
		"empty state": func(o []protocol.ResourceObservation) []protocol.ResourceObservation {
			o[1].ObservedState = ""
			return o
		},
		"contradictory presence": func(o []protocol.ResourceObservation) []protocol.ResourceObservation {
			o[1].Exists = true
			o[1].ObservedState = "ABSENT"
			return o
		},
		"contradictory absence": func(o []protocol.ResourceObservation) []protocol.ResourceObservation {
			o[1].Exists = false
			o[1].ObservedState = "ACTIVE"
			return o
		},
	}
	for name, change := range changes {
		for _, final := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/before", true: "/final"}[final], func(t *testing.T) {
				known, baseline := recoveryInventory()
				cleanupCalls, records, observations := 0, 0, 0
				stages := []string{}
				err := recoverKnownResources(context.Background(), known, false, recoveryActions{
					observe: func(_ context.Context, _ string, requested []protocol.ProviderResourceRef) ([]protocol.ResourceObservation, error) {
						if !reflect.DeepEqual(requested, known) {
							t.Fatal("recovery changed owned query scope")
						}
						observations++
						o := append([]protocol.ResourceObservation{}, baseline...)
						if observations == 2 {
							for i := range o {
								o[i].Exists = false
								o[i].ObservedState = "ABSENT"
							}
						}
						if (observations == 2) == final {
							return change(o), nil
						}
						return o, nil
					},
					cleanup: func(_ context.Context, refs []protocol.ProviderResourceRef) error {
						cleanupCalls++
						if !reflect.DeepEqual(refs, known) {
							t.Fatal("foreign Cleanup scope")
						}
						return nil
					},
					record: func([]protocol.ResourceObservation) { records++ },
					save:   func(stage string) error { stages = append(stages, stage); return nil },
				})
				if err == nil {
					t.Fatal("uncertain inventory passed recovery")
				}
				if !final && (cleanupCalls != 0 || records != 0 || len(stages) != 0) {
					t.Fatal("invalid first observation mutated resources or journal")
				}
				for _, stage := range stages {
					if stage == "RECOVERY_KNOWN_RESOURCE_CLEANUP_ABSENCE_CONFIRMED" {
						t.Fatal("invalid final inventory recorded success")
					}
				}
			})
		}
	}
}

func TestRecoveryCleansOnlyPresentOwnedResourcesAndConfirmsEveryID(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		for _, allAbsent := range []bool{false, true} {
			known, before := recoveryInventory()
			before[1].Exists, before[1].ObservedState = false, "ABSENT"
			if allAbsent {
				before[0].Exists, before[0].ObservedState = false, "ABSENT"
			}
			observations, cleanups, records := 0, 0, 0
			stage := ""
			err := recoverKnownResources(context.Background(), known, readOnly, recoveryActions{
				observe: func(_ context.Context, _ string, refs []protocol.ProviderResourceRef) ([]protocol.ResourceObservation, error) {
					observations++
					o := append([]protocol.ResourceObservation{}, before...)
					if observations == 2 {
						for i := range o {
							o[i].Exists = false
							o[i].ObservedState = "ABSENT"
						}
					}
					return o, nil
				},
				cleanup: func(_ context.Context, refs []protocol.ProviderResourceRef) error {
					cleanups++
					if !reflect.DeepEqual(refs, known[:1]) {
						t.Fatal("absent or foreign resource selected for deletion")
					}
					return nil
				},
				record: func([]protocol.ResourceObservation) { records++ },
				save:   func(s string) error { stage = s; return nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			if readOnly {
				if cleanups != 0 || observations != 1 || records != 1 || stage != "CURRENT_CONNECTOR_READ_ONLY_RECONCILE_COMPLETE_FULL_ACCEPTANCE_PENDING" {
					t.Fatal("read-only recovery changed resources or claimed acceptance")
				}
			} else if observations != 2 || records != 2 || stage != "RECOVERY_KNOWN_RESOURCE_CLEANUP_ABSENCE_CONFIRMED" || cleanups != map[bool]int{false: 1, true: 0}[allAbsent] {
				t.Fatal("complete recovery evidence missing")
			}
		}
	}
}

func TestRecoveryStopsOnObserveCleanupOrJournalFailure(t *testing.T) {
	for _, failure := range []string{"observe", "save", "cleanup"} {
		known, before := recoveryInventory()
		calls, cleaned, passed := 0, false, false
		err := recoverKnownResources(context.Background(), known, false, recoveryActions{
			observe: func(context.Context, string, []protocol.ProviderResourceRef) ([]protocol.ResourceObservation, error) {
				calls++
				if failure == "observe" {
					return nil, errors.New("unavailable")
				}
				return before, nil
			},
			cleanup: func(context.Context, []protocol.ProviderResourceRef) error {
				cleaned = true
				return errors.New("cleanup uncertain")
			},
			record: func([]protocol.ResourceObservation) {},
			save: func(stage string) error {
				if stage == "RECOVERY_KNOWN_RESOURCE_CLEANUP_ABSENCE_CONFIRMED" {
					passed = true
				}
				if failure == "save" {
					return errors.New("disk unavailable")
				}
				return nil
			},
		})
		if err == nil || passed || calls != 1 || cleaned != (failure == "cleanup") {
			t.Fatal("failure did not stop recovery safely")
		}
	}
}
