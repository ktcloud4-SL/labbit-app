package connector

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

const reconciliationInventorySchema = "labbit.dr.reconciliation-inventory.v2"

var (
	inventoryLabel   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	inventoryProject = regexp.MustCompile(`^[a-f0-9]{32}$`)
	inventorySHA     = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type ReconciliationInventoryScope struct {
	RunID        string    `json:"run_id"`
	RequestSHA   string    `json:"request_sha256"`
	Region       string    `json:"region"`
	ConnectorID  uuid.UUID `json:"connector_id"`
	ProjectID    string    `json:"provider_project_id"`
	ConnectionID uuid.UUID `json:"connection_id"`
}

type ReconciliationInventoryOperation struct {
	Correlation
	Resources []protocol.ProviderResourceRef `json:"resources"`
}

type ReconciliationInventory struct {
	Schema      string                          `json:"inventory_schema"`
	Scope       ReconciliationInventoryScope    `json:"scope"`
	ConnectorID uuid.UUID                       `json:"connector_id"`
	SessionID   uuid.UUID                       `json:"hello_ack_session_id"`
	OwnerBefore ReconciliationLease             `json:"owner_before"`
	OwnerAfter  ReconciliationLease             `json:"owner_after"`
	StartedAt   time.Time                       `json:"started_at_utc"`
	ObservedAt  time.Time                       `json:"observed_at_utc"`
	Complete    bool                            `json:"coverage_complete"`
	Operations  []reconciliationInventoryResult `json:"operations"`
}

type reconciliationInventoryResult struct {
	Type          string                         `json:"type"`
	OperationID   string                         `json:"operationId"`
	LabInstanceID string                         `json:"labInstanceId"`
	Generation    int64                          `json:"generation"`
	SessionID     uuid.UUID                      `json:"sessionId"`
	RequestID     string                         `json:"requestMessageId"`
	ReplyToID     string                         `json:"replyToMessageId"`
	ResultID      string                         `json:"resultMessageId"`
	OwnerPodID    string                         `json:"ownerPodId"`
	OwnerEpoch    int64                          `json:"ownerEpoch"`
	StartedAt     time.Time                      `json:"startedAtUtc"`
	ObservedAt    time.Time                      `json:"observedAtUtc"`
	Resources     []protocol.ResourceObservation `json:"resources"`
}

// ObserveReconciliationOwner discovers the server-assigned session without
// sending a frame to the Connector. The inventory checks still verify the
// requested process, durable state and lease.
func (r *Router) ObserveReconciliationOwner(ctx context.Context, scope ReconciliationInventoryScope,
	expected workercontrol.Binding, locator ReconciliationOwnerLocator) (ReconciliationInventory, error) {
	var sessionID uuid.UUID
	err := r.registry.WithReadyRoute(scope.ConnectorID, func(current Session, _ Route) error {
		sessionID = current.ID
		return ctx.Err()
	})
	if err != nil {
		return ReconciliationInventory{}, ErrReconciliationReadback
	}
	return r.CollectReconciliationInventory(ctx, scope, expected, sessionID, []ReconciliationInventoryOperation{}, locator)
}

// CollectReconciliationInventory is for a trusted, post-promotion caller. The
// caller verifies the RUN, independent DB scope and exact current process binding
// before passing the approved resource list. Complete covers that list only.
// An explicit empty list returns session ownership evidence with Complete=false.
func (r *Router) CollectReconciliationInventory(ctx context.Context, scope ReconciliationInventoryScope,
	expected workercontrol.Binding, sessionID uuid.UUID, operations []ReconciliationInventoryOperation,
	locator ReconciliationOwnerLocator) (ReconciliationInventory, error) {
	process, ok := r.workerControl.ProcessBinding()
	requests, err := inventoryRequests(scope, sessionID, operations)
	pausedOwnerRead := process.Mode == workercontrol.PauseNew && len(requests) == 0
	if err != nil || !ok || process != expected || process.Scope.Region != scope.Region ||
		(process.Mode != workercontrol.ReconcileOnly && !pausedOwnerRead) ||
		locator == nil || locator.PodID() != process.PodID {
		return ReconciliationInventory{}, ErrReconciliationReadback
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	evidence := ReconciliationInventory{
		Schema: reconciliationInventorySchema, Scope: scope, ConnectorID: scope.ConnectorID,
		SessionID: sessionID, StartedAt: time.Now().UTC(), Complete: len(requests) > 0,
		Operations: make([]reconciliationInventoryResult, 0, len(requests)),
	}
	leaseRequest := ReconcileRequest{ConnectorID: scope.ConnectorID, SessionID: sessionID}
	if len(requests) == 0 {
		var epoch int64
		err := r.registry.WithReadyRoute(scope.ConnectorID, func(current Session, _ Route) error {
			if current.ID != sessionID || current.OwnerEpoch <= 0 {
				return ErrReconciliationReadback
			}
			epoch = current.OwnerEpoch
			return nil
		})
		if err != nil {
			return ReconciliationInventory{}, ErrReconciliationReadback
		}
		evidence.OwnerBefore, err = observeLease(ctx, locator, leaseRequest, epoch)
		if err != nil {
			return ReconciliationInventory{}, ErrReconciliationReadback
		}
		evidence.OwnerAfter = evidence.OwnerBefore
	}
	messageIDs := make(map[string]struct{}, 2*len(requests))
	for _, req := range requests {
		result, err := r.ReadReconciliation(ctx, req, locator)
		if err != nil || result.RequestID != req.RequestID || !inventoryLabel.MatchString(result.RequestMessageID) ||
			!inventoryLabel.MatchString(result.ResultMessageID) || result.RequestMessageID == result.ResultMessageID {
			return ReconciliationInventory{}, ErrReconciliationReadback
		}
		for _, id := range []string{result.RequestMessageID, result.ResultMessageID} {
			if _, reused := messageIDs[id]; reused {
				return ReconciliationInventory{}, ErrReconciliationReadback
			}
			messageIDs[id] = struct{}{}
		}
		if len(evidence.Operations) == 0 {
			evidence.OwnerBefore = result.OwnerBefore
		}
		first := evidence.OwnerBefore
		if result.OwnerBefore.PodID != first.PodID || result.OwnerBefore.Epoch != first.Epoch ||
			!result.ObservedAt.Before(first.ExpiresAt) {
			return ReconciliationInventory{}, ErrReconciliationReadback
		}
		evidence.OwnerAfter = result.OwnerAfter
		row := reconciliationInventoryResult{
			Type: protocol.MessageTypeReconcileResult, OperationID: result.Correlation.OperationID,
			LabInstanceID: result.Correlation.LabInstanceID, Generation: result.Correlation.Generation,
			SessionID: result.SessionID, RequestID: result.RequestMessageID, ReplyToID: result.RequestMessageID,
			ResultID: result.ResultMessageID, OwnerPodID: first.PodID, OwnerEpoch: first.Epoch,
			StartedAt: result.StartedAt, ObservedAt: result.ObservedAt,
		}
		var complete bool
		row.Resources, complete = knownInventoryObservations(req.Payload.KnownResources, result.Payload.Observations)
		evidence.Complete = evidence.Complete && complete
		evidence.Operations = append(evidence.Operations, row)
	}
	after, err := observeLease(ctx, locator, leaseRequest, evidence.OwnerAfter.Epoch)
	if err != nil || after.PodID != process.PodID || after.ObservedAt.Before(evidence.OwnerAfter.ObservedAt) {
		return ReconciliationInventory{}, ErrReconciliationReadback
	}
	evidence.OwnerAfter = after
	// Observe authoritative control after the potentially blocking final lease
	// read. A durable change while waiting on Valkey invalidates the readback.
	report, err := r.workerControl.DurableReport(ctx)
	if err != nil || !report.State.Matches(process) || (pausedOwnerRead && !report.Drained()) || ctx.Err() != nil {
		return ReconciliationInventory{}, ErrReconciliationReadback
	}
	err = r.registry.WithReadyRoute(scope.ConnectorID, func(current Session, _ Route) error {
		evidence.ObservedAt = time.Now().UTC()
		local := r.workerControl.Snapshot()
		if current.ID != sessionID || current.OwnerEpoch != after.Epoch ||
			local.Unverified || local.Mode != process.Mode ||
			(len(requests) == 0 && !evidence.ObservedAt.Before(evidence.OwnerBefore.ExpiresAt)) ||
			!evidence.ObservedAt.Before(after.ExpiresAt) {
			return ErrReconciliationReadback
		}
		return ctx.Err()
	})
	if err != nil {
		return ReconciliationInventory{}, ErrReconciliationReadback
	}
	return evidence, nil
}

func inventoryRequests(scope ReconciliationInventoryScope, sessionID uuid.UUID, operations []ReconciliationInventoryOperation) ([]ReconcileRequest, error) {
	if scope.Region != "ap-northeast-3" || scope.ConnectorID == uuid.Nil || scope.ConnectionID == uuid.Nil ||
		sessionID == uuid.Nil || !inventoryLabel.MatchString(scope.RunID) || !inventorySHA.MatchString(scope.RequestSHA) ||
		!inventoryProject.MatchString(scope.ProjectID) || operations == nil || len(operations) > 24 {
		return nil, ErrReconciliationReadback
	}
	total := 0
	seen := make(map[Correlation]struct{}, len(operations))
	requests := make([]ReconcileRequest, 0, len(operations))
	discover := true
	for _, operation := range operations {
		c := operation.Correlation
		if _, duplicate := seen[c]; duplicate || !inventoryLabel.MatchString(c.OperationID) ||
			!inventoryLabel.MatchString(c.LabInstanceID) || c.Generation < 1 || operation.Resources == nil {
			return nil, ErrReconciliationReadback
		}
		total += len(operation.Resources)
		if total > 128 {
			return nil, ErrReconciliationReadback
		}
		ids := make(map[[2]string]struct{}, len(operation.Resources))
		for _, resource := range operation.Resources {
			key := [2]string{resource.ResourceType, resource.ProviderID}
			if _, duplicate := ids[key]; duplicate || !inventoryLabel.MatchString(resource.ResourceType) ||
				len(resource.ProviderID) > 255 || !inventoryIdentity(resource.ProviderID, false) ||
				!inventoryIdentity(resource.LogicalName, true) || resource.Generation < 1 || resource.Generation > c.Generation {
				return nil, ErrReconciliationReadback
			}
			ids[key] = struct{}{}
		}
		seen[c] = struct{}{}
		requests = append(requests, ReconcileRequest{
			ConnectorID: scope.ConnectorID, SessionID: sessionID, RequestID: scope.RunID, Correlation: c,
			Payload: protocol.ReconcileRequestPayload{KnownResources: slices.Clone(operation.Resources), DiscoverCandidates: &discover},
		})
	}
	return requests, nil
}

func inventoryIdentity(value string, allowEmpty bool) bool {
	return (allowEmpty || value != "") && strings.TrimSpace(value) == value &&
		strings.IndexFunc(value, func(r rune) bool { return !unicode.IsPrint(r) }) == -1
}

func knownInventoryObservations(refs []protocol.ProviderResourceRef, observations []protocol.ResourceObservation) ([]protocol.ResourceObservation, bool) {
	pending := make(map[protocol.ProviderResourceRef]struct{}, len(refs))
	for _, resource := range refs {
		pending[resource] = struct{}{}
	}
	completed := make([]protocol.ResourceObservation, 0, len(refs))
	seen := make(map[[2]string]struct{}, len(refs))
	complete := true
	for _, observation := range observations {
		if observation.Source == "KNOWN_RESOURCE" {
			key := [2]string{observation.ResourceType, observation.ProviderID}
			if _, duplicate := seen[key]; duplicate {
				return []protocol.ResourceObservation{}, false
			}
			seen[key] = struct{}{}
		}
		ref := protocol.ProviderResourceRef{
			ResourceType: observation.ResourceType, ProviderID: observation.ProviderID,
			Generation: observation.Generation, LogicalName: observation.LogicalName,
		}
		_, expected := pending[ref]
		absent := observation.ObservedState == "ABSENT" || observation.ObservedState == "DELETED" || observation.ObservedState == "MISSING"
		if !expected || observation.Source != "KNOWN_RESOURCE" ||
			(observation.Exists && (!inventoryLabel.MatchString(observation.ObservedState) || observation.ObservedState == "UNKNOWN" || absent)) ||
			(!observation.Exists && observation.ObservedState != "" && !absent) {
			complete = false
			continue
		}
		delete(pending, ref)
		if !observation.Exists && observation.ObservedState == "" {
			observation.ObservedState = "ABSENT"
		}
		completed = append(completed, observation)
	}
	return completed, complete && len(pending) == 0
}
