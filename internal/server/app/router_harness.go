//go:build backend_router_harness

package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
)

var errHarnessStop = errors.New("C1: stopped; preserve ledger, reconcile/manual review required")

type routerHarness struct {
	dir     string
	events  *c1Events
	started sync.Once
}

func newRouterHarness() *routerHarness {
	h := &routerHarness{}
	if os.Getenv("LABBIT_C1_HARNESS") == "WAIT_FOR_OPERATOR" {
		h.dir = os.Getenv("LABBIT_C1_DIRECTORY")
		h.events = &c1Events{ch: make(chan connector.Event, 32)}
	}
	return h
}

func (h *routerHarness) sink() connector.EventSink {
	if h.events == nil {
		return nil
	}
	return h.events
}

// Only the tagged binary can wait for an operator file submitted AFTER the
// exact Pod and authenticated protocol-ready Session have been identified.
// There is no endpoint, background worker, retry, or automatic cleanup defer.
func (h *routerHarness) start(ctx context.Context, stack *controlStack, logger *slog.Logger) func() {
	stop := func() {}
	if h.events == nil {
		return stop
	}
	h.started.Do(func() {
		ctx, stop = context.WithCancel(ctx)
		go func() {
			if stack == nil {
				logger.Warn("C1 harness refused", "error_code", "C1_NO_CONTROL_STACK")
				return
			}
			root, err := c1OpenRoot(h.dir)
			if err != nil {
				logger.Warn("C1 harness refused", "error_code", "C1_GATE_INVALID")
				return
			}
			defer root.Close()
			// Fail before waiting for any new approval when a previous attempt exists.
			if _, err := root.Lstat("attempt.jsonl"); !os.IsNotExist(err) {
				logger.Warn("C1 harness refused", "error_code", "C1_LEDGER_EXISTS")
				return
			}
			waitCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
			defer cancel()
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			logger.Info("C1 harness waiting for operator approval")
			for {
				select {
				case <-waitCtx.Done():
					return
				case <-ticker.C:
					if _, err := root.Lstat("approval.json"); os.IsNotExist(err) {
						continue
					}
					// One submission only, including malformed/expired approvals.
					name, _ := os.Hostname()
					podUID, _ := uuid.Parse(os.Getenv("LABBIT_C1_POD_UID"))
					err := h.run(ctx, stack, root, name, podUID)
					if err != nil {
						logger.Warn("C1 harness stopped; preserve ledger for manual review", "error_code", "C1_STOPPED")
					} else {
						logger.Info("C1 exact-ID absence confirmed; final seven-kind inventory comparison still required")
					}
					return
				}
			}
		}()
	})
	return stop
}

// EventSink returns immediately under the Registry's inbound fence. This is a
// bounded one-attempt mailbox, not a durable Operation event processor.
type c1Events struct {
	mu          sync.RWMutex
	connectorID uuid.UUID
	labID       string
	operations  map[string]bool
	ch          chan connector.Event
	overflow    atomic.Bool
}

func (s *c1Events) HandleEvent(event connector.Event) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var id uuid.UUID
	var c connector.Correlation
	switch e := event.(type) {
	case connector.OperationAckEvent:
		id, c = e.ConnectorID, e.Correlation
	case connector.OperationProgressEvent:
		id, c = e.ConnectorID, e.Correlation
	case connector.OperationResultEvent:
		id, c = e.ConnectorID, e.Correlation
	case connector.ReconcileResultEvent:
		id, c = e.ConnectorID, e.Correlation
	case connector.UnmatchedEvent:
		id, c = e.ConnectorID, connector.Correlation{OperationID: e.OperationID, LabInstanceID: e.LabInstanceID, Generation: e.Generation}
	default:
		return
	}
	if id != s.connectorID || c.LabInstanceID != s.labID || !s.operations[c.OperationID] {
		return
	}
	select {
	case s.ch <- event:
	default:
		s.overflow.Store(true)
	}
}

type c1Entry struct {
	Stage          string                         `json:"stage"`
	ConnectorID    uuid.UUID                      `json:"connectorId,omitempty"`
	SessionID      uuid.UUID                      `json:"sessionId,omitempty"`
	Correlation    connector.Correlation          `json:"correlation"`
	MessageID      string                         `json:"messageId,omitempty"`
	ReplyTo        string                         `json:"replyToMessageId,omitempty"`
	Outcome        string                         `json:"outcome,omitempty"`
	Accepted       *bool                          `json:"accepted,omitempty"`
	Resources      []protocol.ProviderResourceRef `json:"resources,omitempty"`
	ApprovalSHA256 string                         `json:"approvalSha256,omitempty"`
}

func (h *routerHarness) run(ctx context.Context, stack *controlStack, root *os.Root, podName string, podUID uuid.UUID) (runErr error) {
	p, digest, err := c1ReadPlan(root)
	if err != nil || p.validate(time.Now(), podName, podUID) != nil {
		return errHarnessGate
	}
	ledger, err := c1Claim(root)
	if err != nil {
		return err
	}
	defer ledger.file.Close()
	provision := connector.Correlation{OperationID: uuid.NewString(), LabInstanceID: uuid.NewString(), Generation: 1}
	cleanup := provision
	cleanup.OperationID = uuid.NewString()
	h.events.mu.Lock()
	h.events.connectorID, h.events.labID = p.ConnectorID, provision.LabInstanceID
	h.events.operations = map[string]bool{provision.OperationID: true, cleanup.OperationID: true}
	h.events.mu.Unlock()
	if err := ledger.record(c1Entry{Stage: "ATTEMPT_CLAIMED", ConnectorID: p.ConnectorID, SessionID: p.SessionID, Correlation: provision, ApprovalSHA256: digest}); err != nil {
		return err
	}
	defer func() {
		if runErr != nil {
			runErr = errors.Join(runErr, ledger.record(c1Entry{Stage: "STOPPED_MANUAL_RECONCILE_REQUIRED", Correlation: provision}))
		}
	}()
	ctx, cancel := context.WithDeadline(ctx, p.ExpiresAt)
	defer cancel()
	if err := c1Ready(stack, p); err != nil {
		return err
	}
	resources, err := h.mutate(ctx, stack, p, ledger, provision, protocol.OperationCommandPayload{MutationType: protocol.MutationTypeProvision, CreationSnapshot: &p.Snapshot})
	if err != nil {
		return err
	}
	if err := c1Owned(p, resources); err != nil {
		return err
	}
	if err := h.reconcile(ctx, stack, p, ledger, provision, resources, true); err != nil {
		return err
	}
	if _, err := h.mutate(ctx, stack, p, ledger, cleanup, protocol.OperationCommandPayload{MutationType: protocol.MutationTypeCleanup, ProviderResources: resources}); err != nil {
		return err
	}
	if err := h.reconcile(ctx, stack, p, ledger, cleanup, resources, false); err != nil {
		return err
	}
	return ledger.record(c1Entry{Stage: "EXACT_ID_ABSENCE_CONFIRMED_INVENTORY_COMPARISON_PENDING", Correlation: cleanup, Resources: resources})
}

func c1Ready(stack *controlStack, p c1Plan) error {
	if stack == nil || stack.Registry == nil || stack.Router == nil || stack.Router.Registry() != stack.Registry {
		return errHarnessStop
	}
	return stack.Registry.WithReadyRoute(p.ConnectorID, func(s connector.Session, _ connector.Route) error {
		if s.ID != p.SessionID {
			return errHarnessStop
		}
		return nil
	})
}

func (h *routerHarness) next(ctx context.Context, stack *controlStack, p c1Plan) (connector.Event, error) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if h.events.overflow.Load() || c1Ready(stack, p) != nil || ctx.Err() != nil {
			return nil, errHarnessStop
		}
		select {
		case <-ctx.Done():
			return nil, errHarnessStop
		case <-ticker.C:
		case event := <-h.events.ch:
			if c1Ready(stack, p) != nil {
				return event, errHarnessStop
			}
			return event, nil
		}
	}
}

func c1Routed(event connector.Event, p c1Plan, c connector.Correlation, sent connector.SentMessage) (connector.Routed, string, error) {
	var routed connector.Routed
	var kind string
	switch e := event.(type) {
	case connector.OperationAckEvent:
		routed, kind = e.Routed, "ACK"
	case connector.OperationProgressEvent:
		routed, kind = e.Routed, "PROGRESS"
	case connector.OperationResultEvent:
		routed, kind = e.Routed, "RESULT"
	case connector.ReconcileResultEvent:
		routed, kind = e.Routed, "RECONCILE_RESULT"
	default:
		return routed, "", errHarnessStop
	}
	if routed.ConnectorID != p.ConnectorID || routed.Correlation != c || routed.RequestMessageID != sent.MessageID || routed.RequestID != c.OperationID {
		return routed, "", errHarnessStop
	}
	if id, err := uuid.Parse(routed.MessageID); err != nil || id == uuid.Nil {
		return routed, "", errHarnessStop
	}
	return routed, kind, nil
}

func (h *routerHarness) mutate(ctx context.Context, stack *controlStack, p c1Plan, ledger *c1Ledger, c connector.Correlation, payload protocol.OperationCommandPayload) ([]protocol.ProviderResourceRef, error) {
	if ctx.Err() != nil || c1Ready(stack, p) != nil {
		return nil, errHarnessStop
	}
	if err := ledger.record(c1Entry{Stage: payload.MutationType + "_INTENT", Correlation: c, Resources: payload.ProviderResources}); err != nil {
		return nil, err
	}
	sent, err := stack.Router.SendOperationCommand(ctx, connector.OperationCommand{ConnectorID: p.ConnectorID, SessionID: p.SessionID, RequestID: c.OperationID, Correlation: c, Payload: payload})
	if err != nil {
		return nil, errHarnessStop
	} // Intent is retained, including uncertain writes. Never resend.
	defer stack.Router.ForgetOperation(p.ConnectorID, c)
	if err := ledger.record(c1Entry{Stage: payload.MutationType + "_SENT", Correlation: c, MessageID: sent.MessageID}); err != nil {
		return nil, err
	}
	acked := false
	seenMessages := map[string]bool{}
	for {
		event, err := h.next(ctx, stack, p)
		if err != nil {
			// A valid result may already have been delivered under the old inbound
			// fence just before replacement. Preserve those confirmed IDs, but never
			// continue to Cleanup on the replacement Session.
			if event != nil {
				err = errors.Join(err, c1StoppedResult(ledger, event, p, c, sent))
			}
			for remaining := cap(h.events.ch); remaining > 0; remaining-- {
				select {
				case queued := <-h.events.ch:
					err = errors.Join(err, c1StoppedResult(ledger, queued, p, c, sent))
				default:
					return nil, err
				}
			}
			return nil, err
		}
		routed, kind, err := c1Routed(event, p, c, sent)
		if err != nil {
			return nil, err
		}
		if seenMessages[routed.MessageID] {
			return nil, errHarnessStop
		}
		seenMessages[routed.MessageID] = true
		entry := c1Entry{Stage: kind, Correlation: c, MessageID: routed.MessageID, ReplyTo: routed.RequestMessageID}
		switch e := event.(type) {
		case connector.OperationAckEvent:
			entry.Accepted = &e.Payload.Accepted
			if acked || !e.Payload.Accepted {
				return nil, errors.Join(errHarnessStop, ledger.record(entry))
			}
			acked = true
		case connector.OperationProgressEvent:
			if !acked {
				return nil, errHarnessStop
			}
		case connector.OperationResultEvent:
			// Persist safe confirmed IDs even on FAILED/UNKNOWN before stopping.
			entry.Resources = c1SafeRefs(e.Payload.ProviderResources)
			if e.Payload.Outcome == protocol.OutcomeSucceeded || e.Payload.Outcome == protocol.OutcomeFailed || e.Payload.Outcome == protocol.OutcomeUnknown {
				entry.Outcome = e.Payload.Outcome
			}
			if err := ledger.record(entry); err != nil {
				return nil, err
			}
			if !acked || e.Payload.Outcome != protocol.OutcomeSucceeded || e.Payload.Error != nil || len(entry.Resources) != len(e.Payload.ProviderResources) {
				return nil, errHarnessStop
			}
			if payload.MutationType == protocol.MutationTypeCleanup && !c1SameRefs(payload.ProviderResources, entry.Resources) {
				return nil, errHarnessStop
			}
			return entry.Resources, nil
		default:
			return nil, errHarnessStop
		}
		if err := ledger.record(entry); err != nil {
			return nil, err
		}
	}
}

func c1StoppedResult(ledger *c1Ledger, event connector.Event, p c1Plan, c connector.Correlation, sent connector.SentMessage) error {
	if e, ok := event.(connector.OperationResultEvent); ok {
		if routed, _, err := c1Routed(e, p, c, sent); err == nil {
			return ledger.record(c1Entry{Stage: "RESULT_BEFORE_STOP", Correlation: c, MessageID: routed.MessageID, ReplyTo: routed.RequestMessageID, Resources: c1SafeRefs(e.Payload.ProviderResources)})
		}
	}
	return nil
}

func (h *routerHarness) reconcile(ctx context.Context, stack *controlStack, p c1Plan, ledger *c1Ledger, c connector.Correlation, refs []protocol.ProviderResourceRef, exists bool) error {
	if ctx.Err() != nil || c1Ready(stack, p) != nil {
		return errHarnessStop
	}
	if err := ledger.record(c1Entry{Stage: "RECONCILE_INTENT", Correlation: c, Resources: refs}); err != nil {
		return err
	}
	discover := false
	sent, err := stack.Router.SendReconcileRequest(ctx, connector.ReconcileRequest{ConnectorID: p.ConnectorID, SessionID: p.SessionID, RequestID: c.OperationID, Correlation: c, Payload: protocol.ReconcileRequestPayload{KnownResources: refs, DiscoverCandidates: &discover}})
	if err != nil {
		return errHarnessStop
	}
	defer stack.Router.ForgetReconcile(p.ConnectorID, sent.MessageID)
	if err := ledger.record(c1Entry{Stage: "RECONCILE_SENT", Correlation: c, MessageID: sent.MessageID}); err != nil {
		return err
	}
	event, err := h.next(ctx, stack, p)
	if err != nil {
		return err
	}
	routed, kind, err := c1Routed(event, p, c, sent)
	if err != nil {
		return err
	}
	e, ok := event.(connector.ReconcileResultEvent)
	if err := ledger.record(c1Entry{Stage: kind + "_RECEIVED", Correlation: c, MessageID: routed.MessageID, ReplyTo: routed.RequestMessageID}); err != nil {
		return err
	}
	if !ok || e.Payload.Error != nil || c1Confirm(refs, e.Payload.Observations, exists) != nil {
		return errHarnessStop
	}
	stage := "RECONCILE_PRESENCE_CONFIRMED"
	if !exists {
		stage = "RECONCILE_ABSENCE_CONFIRMED"
	}
	return ledger.record(c1Entry{Stage: stage, Correlation: c, MessageID: routed.MessageID, ReplyTo: routed.RequestMessageID, Resources: refs})
}

var c1ResourceLabels = map[string]string{"lab-tier": "KT_TIER", "workspace:management-tier": "KT_TIER", "workspace:management-ssh": "KT_FIREWALL_POLICY", "workspace": "SERVER", "workspace:root-volume": "VOLUME"}

func c1SafeRefs(results []protocol.ProviderResourceResult) []protocol.ProviderResourceRef {
	var refs []protocol.ProviderResourceRef
	for _, item := range results {
		id, err := uuid.Parse(item.ProviderID)
		if err == nil && id != uuid.Nil && id.String() == item.ProviderID && item.Generation == 1 && c1ResourceLabels[item.LogicalName] == item.ResourceType {
			refs = append(refs, item.ProviderResourceRef)
		}
	}
	return refs
}

func c1SameRefs(expected, actual []protocol.ProviderResourceRef) bool {
	if len(expected) != len(actual) {
		return false
	}
	pending := map[protocol.ProviderResourceRef]bool{}
	for _, ref := range expected {
		pending[ref] = true
	}
	for _, ref := range actual {
		if !pending[ref] {
			return false
		}
		delete(pending, ref)
	}
	return len(pending) == 0
}

func c1Owned(p c1Plan, refs []protocol.ProviderResourceRef) error {
	if len(refs) != len(c1ResourceLabels) {
		return errHarnessStop
	}
	seen, ids := map[string]bool{}, map[string]bool{}
	for _, ref := range refs {
		if c1ResourceLabels[ref.LogicalName] != ref.ResourceType || seen[ref.LogicalName] || ids[ref.ResourceType+ref.ProviderID] {
			return errHarnessStop
		}
		for _, baselineID := range p.Baseline[ref.ResourceType] {
			baselineUUID, parseErr := uuid.Parse(baselineID)
			if ref.ProviderID == baselineID || (parseErr == nil && ref.ProviderID == baselineUUID.String()) {
				return errHarnessStop
			}
		}
		seen[ref.LogicalName], ids[ref.ResourceType+ref.ProviderID] = true, true
	}
	return nil
}

// Same exact-ID rule as tools/ktcloud-d1-acceptance/scope.go: cardinality,
// generation, logical name, KNOWN_RESOURCE and explicit state must all agree.
func c1Confirm(refs []protocol.ProviderResourceRef, observations []protocol.ResourceObservation, exists bool) error {
	if len(refs) == 0 || len(refs) != len(observations) {
		return errHarnessStop
	}
	pending := map[protocol.ProviderResourceRef]bool{}
	for _, ref := range refs {
		if pending[ref] {
			return errHarnessStop
		}
		pending[ref] = true
	}
	for _, o := range observations {
		ref := protocol.ProviderResourceRef{ResourceType: o.ResourceType, ProviderID: o.ProviderID, Generation: o.Generation, LogicalName: o.LogicalName}
		if !pending[ref] || o.Source != "KNOWN_RESOURCE" || o.Exists != exists ||
			(!o.Exists && o.ObservedState != "ABSENT") || (o.Exists && (o.ObservedState == "" || o.ObservedState == "UNKNOWN" || o.ObservedState == "ABSENT")) {
			return errHarnessStop
		}
		delete(pending, ref)
	}
	return nil
}
