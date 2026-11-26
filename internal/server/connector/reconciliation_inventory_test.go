package connector

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/valkeyowner"
	"github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
	"github.com/ktcloud4-SL/labbit-app/internal/testsupport/workercontrolfake"
)

func TestReconciliationInventoryRetainsOnlyBoundObservations(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		wantError  bool
		complete   bool
		wantWrites int
		firstRows  int
	}{
		{name: "matched", complete: true, wantWrites: 2, firstRows: 1},
		{name: "absent without state", complete: true, wantWrites: 2, firstRows: 1},
		{name: "candidate", wantWrites: 2, firstRows: 1},
		{name: "missing", wantWrites: 2},
		{name: "duplicate", wantWrites: 2},
		{name: "wrong generation", wantWrites: 2},
		{name: "owner switches", wantError: true, wantWrites: 2},
		{name: "wrong binding", wantError: true},
		{name: "active", wantError: true},
		{name: "paused", wantError: true},
		{name: "missing operations", wantError: true},
		{name: "explicit empty"},
		{name: "discover owner"},
		{name: "discover stale binding", wantError: true},
		{name: "discover session replaced", wantError: true},
		{name: "empty stale binding", wantError: true},
		{name: "empty owner changes", wantError: true},
		{name: "empty session replaced", wantError: true},
		{name: "empty durable report unavailable", wantError: true},
		{name: "ambiguous approved list", wantError: true},
		{name: "unsafe second operation", wantError: true},
		{name: "lost response", wantError: true, wantWrites: 1},
		{name: "paused after last send", wantError: true, wantWrites: 2},
		{name: "unknown ticket close", wantError: true, wantWrites: 2},
		{name: "unavailable final report", wantError: true, wantWrites: 2},
		{name: "owner switches during final recheck", wantError: true, wantWrites: 2},
		{name: "session replaced during final recheck", wantError: true, wantWrites: 2},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := newRouterFixture(t)
			ownerReads, writes := 0, 0
			p := principalOf(uuid.New())
			scope := ReconciliationInventoryScope{
				RunID: "dr-inventory-1", RequestSHA: strings.Repeat("a", 64), Region: "ap-northeast-3",
				ConnectorID: p.ConnectorID, ProjectID: strings.Repeat("d", 32), ConnectionID: uuid.New(),
			}
			mode := workercontrol.ReconcileOnly
			if scenario.name == "active" {
				mode = workercontrol.Active
			}
			if scenario.name == "paused" {
				mode = workercontrol.PauseNew
			}
			binding := workercontrol.Binding{
				Scope: workercontrol.Scope{ID: "osaka-trial", Region: scope.Region, Cluster: "osaka"},
				Epoch: 1, Revision: 1, Mode: mode, PodID: "osaka-pod", BootID: uuid.New(),
			}
			store := workercontrolfake.New(workercontrol.State{Scope: binding.Scope, Epoch: 1, Revision: 1, Mode: mode})
			control, err := workercontrol.NewDurable(binding, store)
			if err != nil {
				t.Fatal(err)
			}
			f.router.workerControl = control
			owner := testReconciliationOwner{read: func(id uuid.UUID) (*valkeyowner.ConnectorOwnerRecord, error) {
				ownerReads++
				pod := binding.PodID
				if scenario.name == "owner switches" && ownerReads == 4 {
					pod = "another-pod"
				}
				if scenario.name == "empty owner changes" && ownerReads == 2 {
					pod = "another-pod"
				}
				if (scenario.name == "empty session replaced" || scenario.name == "discover session replaced") && ownerReads == 2 {
					replacement := f.registry.Register(p, nil)
					if !replacement.MarkReadyWithOwnerEpoch(func([]byte) error { return nil }, 4) {
						t.Fatal("could not make replacement ready")
					}
				}
				if ownerReads == 5 {
					switch scenario.name {
					case "owner switches during final recheck":
						pod = "another-pod"
					case "session replaced during final recheck":
						replacement := f.registry.Register(p, nil)
						if !replacement.MarkReadyWithOwnerEpoch(func([]byte) error { return nil }, 4) {
							t.Fatal("could not make replacement ready")
						}
					}
				}
				return &valkeyowner.ConnectorOwnerRecord{ConnectorID: id, PodID: pod, Epoch: 4,
					ExpiresAt: time.Now().Add(time.Minute)}, nil
			}}
			reg := f.registry.Register(p, nil)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			ref := protocol.ProviderResourceRef{ResourceType: "SERVER", ProviderID: "server-1", Generation: 1}
			if !reg.MarkReadyWithOwnerEpoch(func(data []byte) error {
				writes++
				var msg protocol.ReconcileRequestMessage
				if err := json.Unmarshal(data, &msg); err != nil {
					t.Fatal(err)
				}
				if msg.RequestID != scope.RunID || msg.Payload.DiscoverCandidates == nil || !*msg.Payload.DiscoverCandidates {
					t.Fatal("outbound request lost RUN binding or discovery")
				}
				if scenario.name == "lost response" {
					cancel()
					return nil
				}
				observation := protocol.ResourceObservation{ResourceType: ref.ResourceType, ProviderID: ref.ProviderID,
					Generation: ref.Generation, Exists: true, ObservedState: "ACTIVE", Source: "KNOWN_RESOURCE"}
				if scenario.name == "absent without state" {
					observation.Exists, observation.ObservedState = false, ""
				}
				observations := []protocol.ResourceObservation{observation}
				if writes == 1 {
					switch scenario.name {
					case "candidate":
						observations = append(observations, protocol.ResourceObservation{ProviderID: "candidate", Source: "DISCOVERED_CANDIDATE"})
					case "missing":
						observations = nil
					case "duplicate":
						observations = append(observations, observation)
					case "wrong generation":
						observations[0].Generation++
					}
				}
				c := Correlation{OperationID: msg.OperationID, LabInstanceID: msg.LabInstanceID, Generation: msg.Generation}
				reply := reconcileReply(SentMessage{MessageID: msg.MessageID}, c)
				if !f.router.RouteReconcileResultFromSession(reg.Session(), reply, protocol.ReconcileResultPayload{Observations: observations}) {
					t.Fatal("correlated reply was lost")
				}
				if writes == 2 {
					switch scenario.name {
					case "paused after last send":
						if err := store.Pause(ctx, binding, uuid.New()); err != nil {
							t.Fatal(err)
						}
					case "unknown ticket close":
						store.SetFaults(workercontrolfake.Faults{Leave: true})
					case "unavailable final report":
						store.SetFaults(workercontrolfake.Faults{Report: true})
					}
				}
				return nil
			}, 4) {
				t.Fatal("could not make fake session ready")
			}
			expected := binding
			operations := []ReconciliationInventoryOperation{
				{Correlation: corr("op-1", "lab-1", 2), Resources: []protocol.ProviderResourceRef{ref}},
				{Correlation: corr("op-2", "lab-1", 2), Resources: []protocol.ProviderResourceRef{ref}},
			}
			switch scenario.name {
			case "wrong binding":
				expected.Epoch++
			case "missing operations":
				operations = nil
			case "ambiguous approved list":
				operations[0].Resources = append(operations[0].Resources, ref)
			case "unsafe second operation":
				operations[1].Generation = 0
			}
			if strings.HasPrefix(scenario.name, "empty ") || scenario.name == "explicit empty" {
				operations = []ReconciliationInventoryOperation{}
			}
			switch scenario.name {
			case "empty stale binding", "discover stale binding":
				expected.BootID = uuid.New()
			case "empty durable report unavailable":
				store.SetFaults(workercontrolfake.Faults{Report: true})
			}
			var out ReconciliationInventory
			if strings.HasPrefix(scenario.name, "discover ") {
				out, err = f.router.ObserveReconciliationOwner(ctx, scope, expected, owner)
			} else {
				out, err = f.router.CollectReconciliationInventory(ctx, scope, expected, reg.Session().ID, operations, owner)
			}
			if writes != scenario.wantWrites {
				t.Fatalf("sent %d requests, want %d", writes, scenario.wantWrites)
			}
			if _, pending := f.router.PendingCount(p.ConnectorID); pending != 0 {
				t.Fatalf("left %d requests pending", pending)
			}
			if scenario.wantError {
				if !errors.Is(err, ErrReconciliationReadback) || out.Schema != "" {
					t.Fatalf("unverified batch returned %+v (%v)", out, err)
				}
				return
			}
			if scenario.name == "explicit empty" || scenario.name == "discover owner" {
				if err != nil || ownerReads != 2 || out.Schema != reconciliationInventorySchema || out.Scope != scope ||
					out.SessionID != reg.Session().ID || out.Complete || out.Operations == nil || len(out.Operations) != 0 ||
					out.OwnerBefore.PodID != binding.PodID || out.OwnerAfter.PodID != binding.PodID ||
					out.OwnerBefore.SessionID != out.SessionID || out.OwnerAfter.SessionID != out.SessionID ||
					out.OwnerBefore.Epoch != 4 || out.OwnerAfter.Epoch != 4 || out.ObservedAt.Before(out.OwnerAfter.ObservedAt) {
					t.Fatalf("empty inventory = %+v, owner reads %d (%v)", out, ownerReads, err)
				}
				return
			}
			if err != nil || out.Scope != scope || out.SessionID != reg.Session().ID ||
				out.Complete != scenario.complete || len(out.Operations) != 2 ||
				len(out.Operations[0].Resources) != scenario.firstRows || len(out.Operations[1].Resources) != 1 {
				t.Fatalf("inventory = %+v (%v)", out, err)
			}
			raw, err := json.Marshal(out)
			if err != nil || strings.Contains(string(raw), "DISCOVERED_CANDIDATE") {
				t.Fatalf("unsafe evidence was exported: %s (%v)", raw, err)
			}
			if scenario.name == "absent without state" && out.Operations[0].Resources[0].ObservedState != "ABSENT" {
				t.Fatal("absent resource was not normalized")
			}
			if scenario.name == "matched" || scenario.name == "candidate" || scenario.name == "absent without state" {
				t.Logf("INVENTORY_JSON %s", raw)
			}
		})
	}
}
