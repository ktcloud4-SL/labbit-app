package postgres

import (
	"encoding/json"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
	"testing"
)

func TestReadinessRequiresCompleteCurrentSnapshotAndExactWorkspace(t *testing.T) {
	for _, scenario := range []string{"valid", "legacy", "failed", "unknown", "stale", "wrong workspace", "wrong server", "missing VM", "duplicate VM", "unexpected VM", "inactive VM", "unknown resource"} {
		t.Run(scenario, func(t *testing.T) {
			d := repository.ProvisionDispatch{Generation: 2, Snapshot: json.RawMessage(`{"workspaceVmKey":"workspace","vms":[{"vmKey":"workspace"},{"vmKey":"worker"}]}`)}
			u := repository.ProvisionUpdate{Status: "SUCCEEDED", Readiness: &repository.Readiness{Generation: 2, WorkspaceVMKey: "workspace", ServerID: "workspace-id"}, Resources: []repository.ProvisionResource{
				{Type: "SERVER", LogicalName: "workspace", ProviderID: "workspace-id", Generation: 2, ObservedState: "ACTIVE"},
				{Type: "SERVER", LogicalName: "worker", ProviderID: "worker-id", Generation: 2, ObservedState: "ACTIVE"},
			}}
			switch scenario {
			case "legacy":
				u.Readiness = nil
			case "failed":
				u.Status = "FAILED"
			case "unknown":
				u.Status = "RECONCILING"
			case "stale":
				u.Readiness.Generation = 1
			case "wrong workspace":
				u.Readiness.WorkspaceVMKey = "worker"
			case "wrong server":
				u.Readiness.ServerID = "worker-id"
			case "missing VM":
				u.Resources = u.Resources[:1]
			case "duplicate VM":
				u.Resources = append(u.Resources, u.Resources[1])
			case "unexpected VM":
				u.Resources[1].LogicalName = "other"
			case "inactive VM":
				u.Resources[1].ObservedState = "BUILD"
			case "unknown resource":
				u.Resources = append(u.Resources, repository.ProvisionResource{Type: "PORT", Generation: 2, ObservedState: "UNKNOWN"})
			}
			if got := validReadiness(d, u); got != (scenario == "valid") {
				t.Fatalf("readiness=%v", got)
			}
		})
	}
}
