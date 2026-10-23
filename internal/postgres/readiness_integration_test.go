//go:build integration

package postgres_test

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
	"testing"
	"time"
)

func TestProvisionReadinessWriterAndAggregate(t *testing.T) {
	p, op := pendingProvision(t)
	for i := 0; i < 3; i++ {
		d := prepareProvision(t, p)
		u := provisionUpdate(d, "SUCCEEDED", uuid.NewString())
		var spec struct {
			WorkspaceVMKey string `json:"workspaceVmKey"`
			VMs            []struct {
				VMKey string `json:"vmKey"`
			} `json:"vms"`
		}
		if err := json.Unmarshal(d.Snapshot, &spec); err != nil {
			t.Fatal(err)
		}
		u.Resources = nil
		for _, vm := range spec.VMs {
			id := uuid.NewString()
			u.Resources = append(u.Resources, repository.ProvisionResource{Type: "SERVER", LogicalName: vm.VMKey, ProviderID: id, Generation: d.Generation, ObservedState: "ACTIVE"})
			if vm.VMKey == spec.WorkspaceVMKey {
				u.Readiness = &repository.Readiness{WorkspaceVMKey: vm.VMKey, ServerID: id, Generation: d.Generation}
			}
		}
		if err := p.store.WriteProvision(t.Context(), u); err != nil {
			t.Fatal(err)
		}
		li, err := p.store.LabInstanceByID(t.Context(), d.LabInstanceID)
		if err != nil || li.Status != "READY" {
			t.Fatalf("instance=%+v err=%v", li, err)
		}
		var state string
		if err := p.pool.QueryRow(t.Context(), `SELECT status FROM lab_executions WHERE id=$1`, d.LabExecutionID).Scan(&state); err != nil {
			t.Fatal(err)
		}
		want := "PROVISIONING"
		if i == 2 {
			want = "READY"
		}
		if state != want {
			t.Fatalf("execution=%s want=%s", state, want)
		}
	}
	parent, err := p.store.OperationByID(t.Context(), op)
	if err != nil || parent.Stage != "READY" || parent.Status != "SUCCEEDED" {
		t.Fatalf("parent=%+v err=%v", parent, err)
	}
}

func TestResetReadinessMovesOnlyConfirmedReplacementGeneration(t *testing.T) {
	for _, scenario := range []string{"ready", "legacy", "stale proof", "missing old absence", "failed"} {
		t.Run(scenario, func(t *testing.T) {
			db, f := mutationFixture(t)
			if _, err := registerOperation(t.Context(), db, generationInput(f, "RESET", 2), at(1)); err != nil {
				t.Fatal(err)
			}
			claim, err := db.store.ClaimOperationItem(t.Context(), uuid.New(), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			d, err := db.store.PrepareMutation(t.Context(), claim.Lease)
			if err != nil {
				t.Fatal(err)
			}
			u := repository.ProvisionUpdate{Dispatch: d, Status: "SUCCEEDED", Stage: "VERIFYING_READINESS", Readiness: &repository.Readiness{WorkspaceVMKey: "vm-1", ServerID: "replacement", Generation: 2}}
			for _, r := range d.Resources {
				r.ObservedState = "DELETED"
				u.Resources = append(u.Resources, r)
			}
			var spec struct {
				WorkspaceVMKey string `json:"workspaceVmKey"`
				VMs            []struct {
					VMKey string `json:"vmKey"`
				} `json:"vms"`
			}
			if err = json.Unmarshal(d.Snapshot, &spec); err != nil {
				t.Fatal(err)
			}
			for _, vm := range spec.VMs {
				id := "replacement-" + vm.VMKey
				u.Resources = append(u.Resources, repository.ProvisionResource{Type: "SERVER", LogicalName: vm.VMKey, ProviderID: id, Generation: 2, ObservedState: "ACTIVE"})
				if vm.VMKey == spec.WorkspaceVMKey {
					u.Readiness = &repository.Readiness{WorkspaceVMKey: vm.VMKey, ServerID: id, Generation: 2}
				}
			}
			switch scenario {
			case "legacy":
				u.Readiness = nil
			case "stale proof":
				u.Readiness.Generation = 1
			case "missing old absence":
				u.Resources = u.Resources[1:]
			case "failed":
				u.Status = "FAILED"
			}
			err = db.store.WriteMutation(t.Context(), u)
			if scenario == "stale proof" || scenario == "missing old absence" || scenario == "failed" {
				if err != nil {
					t.Fatal(err)
				}
				li, e := db.store.LabInstanceByID(t.Context(), d.LabInstanceID)
				if e != nil || li.Status == "READY" || li.Generation != 1 {
					t.Fatalf("unsafe instance=%+v err=%v", li, e)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			li, err := db.store.LabInstanceByID(t.Context(), d.LabInstanceID)
			if err != nil {
				t.Fatal(err)
			}
			want := "READY"
			if scenario == "legacy" {
				want = "PROVISIONING"
			}
			if li.Status != want || li.Generation != 2 {
				t.Fatalf("instance=%+v", li)
			}
		})
	}
}
