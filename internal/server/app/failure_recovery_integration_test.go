//go:build integration

package app

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

// recoveryProvider keeps existence separate from the OperationResult reported
// over Control WSS. Protected resources never belong to this execution.
type recoveryProvider struct {
	mu              sync.Mutex
	known, present  map[string]provider.ResourceRef
	owners          map[string]string
	provisions      map[string]int
	deletes         map[string]int
	partialInstance string
	blockedID       string
}

func newRecoveryProvider() *recoveryProvider {
	protected := map[string]provider.ResourceRef{
		"shared-baseline": {ResourceType: "KT_TIER", ProviderID: "shared-baseline", Generation: 1, LogicalName: "shared"},
		"other-fixture":   {ResourceType: "SERVER", ProviderID: "other-fixture", Generation: 1, LogicalName: "workspace"},
	}
	return &recoveryProvider{known: maps.Clone(protected), present: maps.Clone(protected), owners: map[string]string{}, provisions: map[string]int{}, deletes: map[string]int{}}
}

func (p *recoveryProvider) mock(t *testing.T) *provider.MockProvider {
	return &provider.MockProvider{
		ProvisionFunc: func(_ context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.provisions[r.LabInstanceID]++
			if p.provisions[r.LabInstanceID] != 1 {
				t.Error("Provision was blindly replayed")
			}
			result := mockProvisionResult(r, provider.OutcomeSucceeded)
			if r.LabInstanceID == p.partialInstance {
				result.Outcome = provider.OutcomeFailed
				result.ProviderResources = result.ProviderResources[:2]
			}
			for _, resource := range result.ProviderResources {
				p.known[resource.ProviderID], p.present[resource.ProviderID] = resource.ResourceRef, resource.ResourceRef
				p.owners[resource.ProviderID] = r.LabInstanceID
			}
			return result, nil
		},
		CleanupFunc: func(_ context.Context, r provider.CleanupRequest) (provider.OperationResult, error) {
			p.mu.Lock()
			defer p.mu.Unlock()
			result := provider.OperationResult{Outcome: provider.OutcomeSucceeded}
			blocked := false
			for _, ref := range r.ProviderResources {
				if p.known[ref.ProviderID] != ref || p.owners[ref.ProviderID] != r.LabInstanceID {
					t.Error("Cleanup expanded the exact-ID ownership boundary")
					return provider.OperationResult{Outcome: provider.OutcomeFailed}, nil
				}
				state := "DELETED"
				switch {
				case blocked:
					state = "DELETE_NOT_ATTEMPTED"
				case ref.ProviderID == p.blockedID:
					state, blocked, result.Outcome = "DELETE_FAILED", true, provider.OutcomeFailed
				default:
					if _, exists := p.present[ref.ProviderID]; exists {
						delete(p.present, ref.ProviderID)
						p.deletes[ref.ProviderID]++
					}
				}
				result.ProviderResources = append(result.ProviderResources, provider.ResourceResult{ResourceRef: ref, ObservedState: state})
			}
			return result, nil
		},
	}
}

func (p *recoveryProvider) inventory() map[string]provider.ResourceRef {
	p.mu.Lock()
	defer p.mu.Unlock()
	return maps.Clone(p.present)
}

func (e *provisionEnv) cleanupHTTP(key string) uuid.UUID {
	e.t.Helper()
	previous := e.opID
	resp := send(e.t, http.MethodPost, e.server.URL+"/api/v1/lab-executions/"+e.executionID.String()+"/cleanup", "",
		map[string]string{"Cookie": e.cookie, "Origin": publicOrigin, "Idempotency-Key": key})
	if resp.status != http.StatusAccepted {
		e.t.Fatalf("Cleanup HTTP status=%d body=%s", resp.status, resp.body)
	}
	var accepted struct{ OperationID uuid.UUID }
	if err := json.Unmarshal([]byte(resp.body), &accepted); err != nil || accepted.OperationID == uuid.Nil || accepted.OperationID == previous {
		e.t.Fatalf("new Cleanup operation=%v error=%v", accepted.OperationID, err)
	}
	e.opID = accepted.OperationID
	if e.operationStatus() != "PENDING" {
		e.t.Fatal("Cleanup performed work within the HTTP request")
	}
	return previous
}

func (e *provisionEnv) assertRecoveryResources(p *recoveryProvider, absent bool) {
	e.t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	rows, err := e.db.Query(e.t.Context(), `SELECT r.provider_id,r.resource_type,r.logical_name,r.generation,r.lab_instance_id::text,r.lifecycle_status,
 r.organization_id=$2 AND r.provider_connection_id=$3 AND r.created_by_operation_item_id IS NOT NULL
 FROM provider_resources r JOIN lab_instances i ON i.id=r.lab_instance_id WHERE i.lab_execution_id=$1`, e.executionID, e.f.OrganizationID, e.f.ProviderConnectionID)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var ref provider.ResourceRef
		var owner, lifecycle string
		var owned bool
		if err := rows.Scan(&ref.ProviderID, &ref.ResourceType, &ref.LogicalName, &ref.Generation, &owner, &lifecycle, &owned); err != nil {
			e.t.Fatal(err)
		}
		_, exists := p.present[ref.ProviderID]
		if !owned || p.known[ref.ProviderID] != ref || p.owners[ref.ProviderID] != owner || exists != (lifecycle == "PRESENT") || (absent && (exists || lifecycle != "DELETED")) {
			e.t.Fatalf("DB/Fake exact-ID ownership/existence mismatch: id=%s lifecycle=%s exists=%v", ref.ProviderID, lifecycle, exists)
		}
		seen[ref.ProviderID] = true
	}
	if err := rows.Err(); err != nil {
		e.t.Fatal(err)
	}
	if len(seen) != len(p.owners) || len(seen) == 0 {
		e.t.Fatalf("partial resource history lost: DB=%d Fake=%d", len(seen), len(p.owners))
	}
	for id, owner := range p.owners {
		if !seen[id] || p.provisions[owner] != 1 || (absent && p.deletes[id] != 1) {
			e.t.Fatalf("resource omitted/recreated/deleted twice: %s", id)
		}
	}
	for _, id := range []string{"shared-baseline", "other-fixture"} {
		if p.present[id] != p.known[id] || p.deletes[id] != 0 {
			e.t.Fatalf("protected Fake resource changed: %s", id)
		}
	}
}

func (e *provisionEnv) assertRecoveryCompleted(failedOperation uuid.UUID) {
	e.t.Helper()
	if e.operationStatus() != "SUCCEEDED" {
		e.t.Fatal("recovery Cleanup did not succeed")
	}
	old, err := e.store.OperationByID(e.t.Context(), failedOperation)
	if err != nil || old.Status != "FAILED" || old.FinishedAt == nil {
		e.t.Fatalf("failure history overwritten: %+v %v", old, err)
	}
	var completed bool
	var residual, incomplete int
	err = e.db.QueryRow(e.t.Context(), `SELECT status='COMPLETED' AND finished_at IS NOT NULL,
 (SELECT count(*) FROM provider_resources r JOIN lab_instances i ON i.id=r.lab_instance_id WHERE i.lab_execution_id=$1 AND r.lifecycle_status<>'DELETED'),
 (SELECT count(*) FROM lab_instances WHERE lab_execution_id=$1 AND status<>'COMPLETED') FROM lab_executions WHERE id=$1`, e.executionID).Scan(&completed, &residual, &incomplete)
	if err != nil || !completed || residual != 0 || incomplete != 0 {
		e.t.Fatalf("recovery not complete: completed=%v residual=%d instances=%d error=%v", completed, residual, incomplete, err)
	}
	e.assertHTTPMatchesDatabase()
}

func TestPartialProvisionFailureRecoversThroughCleanupHTTP(t *testing.T) {
	e := newProvisionEnv(t, 5*time.Second)
	p := newRecoveryProvider()
	var target uuid.UUID
	if err := e.db.QueryRow(t.Context(), `SELECT id FROM lab_instances WHERE lab_execution_id=$1 AND user_id=$2`, e.executionID, e.f.StudentAID).Scan(&target); err != nil {
		t.Fatal(err)
	}
	p.partialInstance = target.String()
	e.startMock(p.mock(t))
	for range 3 {
		e.runOneMustClaim()
	}
	instance, err := e.store.LabInstanceByID(t.Context(), target)
	if err != nil || e.operationStatus() != "FAILED" || instance.Status != "ERROR" {
		t.Fatalf("partial Provision not persisted: instance=%+v error=%v", instance, err)
	}
	p.mu.Lock()
	partial := 0
	for _, owner := range p.owners {
		if owner == target.String() {
			partial++
		}
	}
	p.mu.Unlock()
	if partial != 2 {
		t.Fatalf("expected only the created partial set, got %d", partial)
	}
	e.assertRecoveryResources(p, false)
	e.assertHTTPMatchesDatabase()
	before := p.inventory()
	failed := e.cleanupHTTP("partial-provision-cleanup")
	if !maps.Equal(before, p.inventory()) {
		t.Fatal("Cleanup HTTP mutated Fake resources synchronously")
	}
	for range 3 {
		e.runOneMustClaim()
	}
	e.assertRecoveryResources(p, true)
	e.assertRecoveryCompleted(failed)
}

func TestFailedCleanupRecoversThroughNewCleanupHTTP(t *testing.T) {
	e := newProvisionEnv(t, 5*time.Second)
	p := newRecoveryProvider()
	e.startMock(p.mock(t))
	for range 3 {
		e.runOneMustClaim()
	}
	// Block a middle resource so the same Item has both deleted and retained IDs.
	p.mu.Lock()
	for id, ref := range p.known {
		if ref.LogicalName == "vm-2" {
			p.blockedID = id
			break
		}
	}
	blockedID := p.blockedID
	p.mu.Unlock()
	if blockedID == "" {
		t.Fatal("missing resource to inject the fixture-local failure")
	}
	e.cleanupHTTP("cleanup-fails-once")
	for range 3 {
		e.runOneMustClaim()
	}
	if e.operationStatus() != "FAILED" {
		t.Fatal("partial Cleanup was reported as complete")
	}
	e.assertRecoveryResources(p, false)
	var unfinished bool
	var deleted, retained int
	err := e.db.QueryRow(t.Context(), `SELECT finished_at IS NULL,
 (SELECT count(*) FROM provider_resources WHERE lifecycle_status='DELETED'),
 (SELECT count(*) FROM provider_resources WHERE lifecycle_status='PRESENT') FROM lab_executions WHERE id=$1`, e.executionID).Scan(&unfinished, &deleted, &retained)
	if err != nil || !unfinished || deleted == 0 || retained == 0 {
		t.Fatalf("partial deletion not preserved: unfinished=%v deleted=%d retained=%d error=%v", unfinished, deleted, retained, err)
	}
	before := p.inventory()
	if _, exists := before[blockedID]; !exists {
		t.Fatal("failed delete lost the residual resource")
	}
	if claimed, err := e.stack.Provisions.RunOne(t.Context()); claimed || err != nil || !maps.Equal(before, p.inventory()) {
		t.Fatalf("failed Cleanup blindly replayed: claimed=%v error=%v", claimed, err)
	}
	p.mu.Lock()
	p.blockedID = ""
	p.mu.Unlock()
	failed := e.cleanupHTTP("cleanup-after-fixture-recovery")
	if !maps.Equal(before, p.inventory()) {
		t.Fatal("recovery HTTP changed resources before Worker execution")
	}
	for range 3 {
		e.runOneMustClaim()
	}
	e.assertRecoveryResources(p, true)
	e.assertRecoveryCompleted(failed)
}
