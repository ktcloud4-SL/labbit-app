package provision

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/operation"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

type Router interface {
	SendOperationCommand(context.Context, connector.OperationCommand) (connector.SentMessage, error)
	ForgetOperation(uuid.UUID, connector.Correlation) bool
}

// Options keeps PROVISION-only callers compatible; Mutations enables the other
// durable operation types on the same Worker/Router/EventSink.
type Options struct {
	Store            repository.ProvisionWorkerStore
	Mutations        repository.MutationWorkerStore
	Reconciliation   repository.ReconciliationStore
	CloseForMutation func(context.Context, repository.ProvisionDispatch) error
	Router           Router
	Tracer           trace.Tracer
	Logger           *slog.Logger
	// Ready checks application/schema compatibility before a new claim.
	Ready                                      func(context.Context) error
	LeaseDuration, ResultTimeout, PollInterval time.Duration
	DispatchRetryDelay                         time.Duration
}

// Executor is also the Router's typed EventSink. Only correlation and
// bounded inbox delivery happen on Control read loops; DB I/O runs in Execute.
type Executor struct {
	opts    Options
	worker  *operation.Worker
	mu      sync.Mutex
	pending map[provisionKey]*provisionInbox
}

type provisionKey struct {
	connectorID uuid.UUID
	correlation connector.Correlation
}

type provisionInbox struct {
	ack       chan connector.OperationAckEvent
	rejected  chan connector.OperationAckEvent
	result    chan connector.OperationResultEvent
	reconcile chan connector.ReconcileResultEvent
}

// The restricted store changes only claim eligibility; Worker.RunOne/Execute/
// Renew and LBT-176's general claim API retain their original behavior.
type provisionClaims struct {
	repository.ProvisionWorkerStore
}

func (s provisionClaims) ClaimOperationItem(ctx context.Context, owner uuid.UUID, duration time.Duration) (repository.OperationItemClaim, error) {
	return s.ClaimProvisionOperationItem(ctx, owner, duration)
}

func NewExecutor(opts Options) (*Executor, error) {
	if opts.Store == nil || opts.Router == nil {
		return nil, operation.ErrInvalid
	}
	if opts.LeaseDuration == 0 {
		opts.LeaseDuration = 30 * time.Second
	}
	if opts.ResultTimeout == 0 {
		opts.ResultTimeout = 15 * time.Minute
	}
	if opts.PollInterval == 0 {
		opts.PollInterval = time.Second
	}
	if opts.DispatchRetryDelay == 0 {
		opts.DispatchRetryDelay = 5 * time.Second
	}
	if opts.LeaseDuration < 3*time.Microsecond || opts.ResultTimeout <= 0 || opts.PollInterval <= 0 || opts.DispatchRetryDelay < time.Microsecond {
		return nil, operation.ErrInvalid
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	p := &Executor{opts: opts, pending: make(map[provisionKey]*provisionInbox)}
	var claims repository.OperationWorkerStore = provisionClaims{opts.Store}
	if opts.Mutations != nil {
		claims = opts.Mutations
	}
	if opts.Reconciliation != nil {
		if _, ok := opts.Router.(reconcileRouter); !ok {
			return nil, operation.ErrInvalid
		}
		claims = reconciliationClaims{OperationWorkerStore: claims, store: opts.Reconciliation}
	}
	w, err := operation.NewWorker(operation.WorkerOptions{Store: claims, LeaseDuration: opts.LeaseDuration, Tracer: opts.Tracer, Execute: p.execute})
	if err != nil {
		return nil, err
	}
	p.worker = w
	return p, nil
}

func (p *Executor) RunOne(ctx context.Context) (bool, error) { return p.worker.RunOne(ctx) }

// Run has a small fixed number of independent slots, no separate deployment or
// queue. Process cancellation stops claims and leaves uncertain work durable.
func (p *Executor) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for ctx.Err() == nil {
				var claimed bool
				var err error
				if p.opts.Ready != nil {
					err = p.opts.Ready(ctx)
				}
				if err == nil {
					claimed, err = p.RunOne(ctx)
				}
				if ctx.Err() != nil {
					return
				}
				if err != nil {
					p.opts.Logger.Warn("Operation execution stopped; durable item retained", "error_code", "operation_execution_unavailable")
				}
				if claimed && err == nil {
					continue
				}
				timer := time.NewTimer(p.opts.PollInterval)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		})
	}
	wg.Wait()
}

func (p *Executor) HandleEvent(event connector.Event) {
	var routed connector.Routed
	switch e := event.(type) {
	case connector.OperationAckEvent:
		routed = e.Routed
	case connector.OperationResultEvent:
		routed = e.Routed
	case connector.ReconcileResultEvent:
		routed = e.Routed
	default:
		return
	}
	if routed.RequestMessageID == "" || routed.MessageID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	inbox := p.pending[provisionKey{routed.ConnectorID, routed.Correlation}]
	if inbox == nil {
		return
	}
	// ACK duplicates cannot consume the RESULT slot. The Router has already
	// authenticated and correlated these events; reply ID is checked again after
	// Send returns, including ACK/RESULT received before the write returned.
	switch e := event.(type) {
	case connector.ReconcileResultEvent:
		select {
		case inbox.reconcile <- e:
		default:
		}
	case connector.OperationAckEvent:
		if !e.Payload.Accepted {
			select {
			case inbox.rejected <- e:
			default:
			}
			return
		}
		select {
		case inbox.ack <- e:
		default:
		}
	case connector.OperationResultEvent:
		select {
		case inbox.result <- e:
		default:
		}
	}
}

func (p *Executor) execute(ctx context.Context, claim repository.OperationItemClaim) error {
	if claim.Item.Status == "RECONCILING" {
		return p.reconcile(ctx, claim)
	}
	var d repository.ProvisionDispatch
	var err error
	if claim.Operation.Type == "RESET" || claim.Operation.Type == "CLEANUP" {
		if p.opts.Mutations == nil {
			return operation.ErrInvalid
		}
		d, err = p.opts.Mutations.PrepareMutation(ctx, claim.Lease)
	} else {
		d, err = p.opts.Store.PrepareProvision(ctx, claim.Lease)
	}
	if err != nil {
		return err
	}
	var snapshot protocol.CreationSnapshot
	if d.MutationType != "CLEANUP" && json.Unmarshal(d.Snapshot, &snapshot) != nil {
		return p.invalidCommand(ctx, d)
	}
	corr := connector.Correlation{OperationID: d.OperationID.String(), LabInstanceID: d.LabInstanceID.String(), Generation: d.Generation}
	key := provisionKey{d.ConnectorID, corr}
	inbox := &provisionInbox{ack: make(chan connector.OperationAckEvent, 1), rejected: make(chan connector.OperationAckEvent, 1), result: make(chan connector.OperationResultEvent, 1)}
	p.mu.Lock()
	if p.pending[key] != nil {
		p.mu.Unlock()
		return operation.ErrInvalid
	}
	p.pending[key] = inbox
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.pending, key)
		p.mu.Unlock()
		p.opts.Router.ForgetOperation(d.ConnectorID, corr)
	}()
	// Recheck ownership after preparation, before any external I/O. No DB
	// transaction remains open during Send or while waiting for the Connector.
	if _, err = p.worker.Renew(ctx, d.Lease); err != nil {
		return err
	}
	payload := protocol.OperationCommandPayload{MutationType: protocol.MutationTypeProvision, CreationSnapshot: &snapshot}
	if d.MutationType != "" {
		payload.MutationType = d.MutationType
		payload.ProviderResources = make([]protocol.ProviderResourceRef, 0, len(d.Resources))
		for _, r := range d.Resources {
			payload.ProviderResources = append(payload.ProviderResources, protocol.ProviderResourceRef{ResourceType: r.Type, ProviderID: r.ProviderID, LogicalName: r.LogicalName, Generation: r.Generation})
		}
		if d.MutationType == "CLEANUP" {
			payload.CreationSnapshot = nil
		}
		if p.opts.CloseForMutation != nil {
			if err := p.opts.CloseForMutation(ctx, d); err != nil {
				return err
			}
		}
		if _, err := p.worker.Renew(ctx, d.Lease); err != nil {
			return err
		}
	}
	sent, err := p.opts.Router.SendOperationCommand(ctx, connector.OperationCommand{
		ConnectorID: d.ConnectorID, RequestID: claim.Operation.RequestID, Correlation: corr,
		Payload: payload,
		Trace:   connector.TraceFromContext(ctx),
	})
	if err != nil {
		// Even if the write returned an error, an immediate correlated RESULT
		// may prove the actual outcome. Otherwise classify the Router error.
		select {
		case e := <-inbox.result:
			return p.result(ctx, d, e.Payload)
		default:
		}
		select {
		case e := <-inbox.rejected:
			return p.rejected(ctx, d, e.Payload)
		default:
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-inbox.ack:
			return p.unknown(ctx, d, "provision_dispatch_unconfirmed")
		default:
		}
		// Only an explicit no-write guarantee authorizes a new attempt. Wrapped
		// send/duplicate/unknown errors fail closed even if they also match a
		// no-write category. The durable delay prevents polling the same Item hot.
		if !errors.Is(err, connector.ErrSendFailed) && !errors.Is(err, connector.ErrDuplicateCorrelation) {
			if errors.Is(err, connector.ErrInvalidCommand) {
				return p.invalidCommand(ctx, d)
			}
			if errors.Is(err, connector.ErrConnectorUnavailable) {
				return p.deferUnsent(ctx, d)
			}
		}
		return p.unknown(ctx, d, "provision_dispatch_unconfirmed")
	}
	ticker := time.NewTicker(p.opts.LeaseDuration / 3)
	defer ticker.Stop()
	timer := time.NewTimer(p.opts.ResultTimeout)
	defer timer.Stop()
	acked := false
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err = p.worker.Renew(ctx, d.Lease); err != nil {
				return err
			}
		case <-timer.C:
			// Prefer a confirmed result already delivered before the deadline.
			select {
			case e := <-inbox.result:
				if e.RequestMessageID == sent.MessageID {
					return p.result(ctx, d, e.Payload)
				}
			default:
			}
			select {
			case e := <-inbox.rejected:
				if e.RequestMessageID == sent.MessageID {
					return p.rejected(ctx, d, e.Payload)
				}
			default:
			}
			return p.unknown(ctx, d, "provision_response_unconfirmed")
		case e := <-inbox.rejected:
			if e.RequestMessageID != sent.MessageID {
				continue
			}
			return p.rejected(ctx, d, e.Payload)
		case e := <-inbox.ack:
			if e.RequestMessageID != sent.MessageID || acked {
				continue
			}
			acked = true
			if err = p.write(ctx, repository.ProvisionUpdate{Dispatch: d, Status: "RUNNING", Stage: "PROVISIONING"}); err != nil {
				return err
			}
		case e := <-inbox.result:
			if e.RequestMessageID != sent.MessageID {
				continue
			}
			return p.result(ctx, d, e.Payload)
		}
	}
}

func (p *Executor) invalidCommand(ctx context.Context, d repository.ProvisionDispatch) error {
	return p.write(ctx, repository.ProvisionUpdate{Dispatch: d, Status: "FAILED", Stage: "REJECTED",
		ErrorCode: "provision_command_invalid", ErrorMessage: "Operation command failed validation"})
}

func (p *Executor) rejected(ctx context.Context, d repository.ProvisionDispatch, ack protocol.OperationAckPayload) error {
	return p.write(ctx, repository.ProvisionUpdate{Dispatch: d, Status: "FAILED", Stage: "REJECTED",
		ErrorCode: safeProvisionCode(ack.Error, "provision_command_rejected"), ErrorMessage: "Connector rejected the operation command"})
}

var provisionErrorCode = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,127}$`)

func safeProvisionCode(err *protocol.SafeError, fallback string) string {
	if err != nil && provisionErrorCode.MatchString(err.Code) {
		return err.Code
	}
	return fallback
}

func (p *Executor) unknown(ctx context.Context, d repository.ProvisionDispatch, code string) error {
	return p.write(ctx, repository.ProvisionUpdate{Dispatch: d, Status: "RECONCILING", Stage: "RECONCILING",
		ErrorCode: code, ErrorMessage: "Operation outcome requires reconciliation"})
}

func (p *Executor) result(ctx context.Context, d repository.ProvisionDispatch, r protocol.OperationResultPayload) error {
	u := repository.ProvisionUpdate{Dispatch: d}
	if r.Readiness != nil {
		u.Readiness = &repository.Readiness{WorkspaceVMKey: r.Readiness.WorkspaceVMKey, ServerID: r.Readiness.ServerID, Generation: r.Readiness.Generation}
	}
	switch r.Outcome {
	case protocol.OutcomeSucceeded:
		u.Status = "SUCCEEDED"
		u.Stage = "VERIFYING_READINESS"
	case protocol.OutcomeFailed:
		u.Status = "FAILED"
		u.Stage = "FAILED"
		u.ErrorCode = safeProvisionCode(r.Error, "provision_failed")
		u.ErrorMessage = "Connector reported operation failure"
	case protocol.OutcomeUnknown:
		u.Status = "RECONCILING"
		u.Stage = "RECONCILING"
		u.ErrorCode = safeProvisionCode(r.Error, "provision_outcome_unknown")
		u.ErrorMessage = "Operation outcome requires reconciliation"
	default:
		return p.unknown(ctx, d, "provision_result_invalid")
	}
	for _, resource := range r.ProviderResources {
		u.Resources = append(u.Resources, repository.ProvisionResource{Type: resource.ResourceType, ProviderID: resource.ProviderID,
			Generation: resource.Generation, LogicalName: resource.LogicalName, ObservedState: resource.ObservedState})
	}
	err := p.write(ctx, u)
	if errors.Is(err, repository.ErrConstraintViolation) {
		return p.unknown(ctx, d, "provision_result_invalid")
	}
	return err
}

func (p *Executor) write(ctx context.Context, u repository.ProvisionUpdate) error {
	if u.Dispatch.MutationType != "" {
		if u.Status == "RUNNING" {
			u.Stage = "RESETTING"
			if u.Dispatch.MutationType == "CLEANUP" {
				u.Stage = "CLEANING_UP"
			}
		}
		return p.opts.Mutations.WriteMutation(ctx, u)
	}
	return p.opts.Store.WriteProvision(ctx, u)
}
func (p *Executor) deferUnsent(ctx context.Context, d repository.ProvisionDispatch) error {
	if d.MutationType != "" {
		return p.opts.Mutations.DeferUnsentMutation(ctx, d, p.opts.DispatchRetryDelay)
	}
	return p.opts.Store.DeferUnsentProvision(ctx, d, p.opts.DispatchRetryDelay)
}
