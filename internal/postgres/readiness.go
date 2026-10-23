package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// Readiness is trusted only from the authenticated, correlated Connector result,
// then checked against immutable snapshot and exact current-generation resources.
func validReadiness(d repository.ProvisionDispatch, u repository.ProvisionUpdate) bool {
	r := u.Readiness
	if u.Status != "SUCCEEDED" || r == nil || r.Generation != d.Generation || r.ServerID == "" || !resetVMsPresent(d.Snapshot, u.Resources, d.Generation) {
		return false
	}
	var snapshot struct {
		WorkspaceVMKey string `json:"workspaceVmKey"`
	}
	if json.Unmarshal(d.Snapshot, &snapshot) != nil || snapshot.WorkspaceVMKey == "" || snapshot.WorkspaceVMKey != r.WorkspaceVMKey {
		return false
	}
	matches := 0
	for _, resource := range u.Resources {
		if resource.Generation == d.Generation && resource.Type == "SERVER" && resource.LogicalName == r.WorkspaceVMKey && resource.ProviderID == r.ServerID && resource.ObservedState == "ACTIVE" {
			matches++
		}
	}
	return matches == 1
}

func (q queries) aggregateReadiness(ctx context.Context, id uuid.UUID, at time.Time) error {
	_, err := q.db.Exec(ctx, `UPDATE lab_executions e SET status=CASE
 WHEN EXISTS(SELECT 1 FROM lab_instances WHERE lab_execution_id=e.id AND status='ERROR') THEN 'ERROR'
 WHEN NOT EXISTS(SELECT 1 FROM lab_instances WHERE lab_execution_id=e.id AND status<>'READY') THEN 'ACTIVE'
 ELSE 'PROVISIONING' END,updated_at=$2 WHERE e.id=$1`, id, at)
	return normalize("AggregateReadiness", err)
}
