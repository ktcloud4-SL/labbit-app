package operation

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
	"github.com/ktcloud4-SL/labbit-app/internal/server/tracecontext"
)

type WorkerOptions struct {
	Store         repository.OperationWorkerStore
	LeaseDuration time.Duration
	// Execute runs after claim commit, on the worker lifecycle context. The
	// caller supplies the external execution boundary; RunOne never retries it
	// or interprets its error as a confirmed Provider outcome.
	Execute func(context.Context, repository.OperationItemClaim) error
	Tracer  trace.Tracer
}

// Worker is a single-item execution boundary, with no scheduler or polling loop.
// The provision executor connects execution to the api process's Connector Router.
type Worker struct{ opts WorkerOptions }

func NewWorker(opts WorkerOptions) (*Worker, error) {
	if opts.Store == nil || opts.Execute == nil || opts.LeaseDuration < time.Microsecond {
		return nil, ErrInvalid
	}
	if opts.Tracer == nil {
		opts.Tracer = noop.NewTracerProvider().Tracer("")
	}
	return &Worker{opts: opts}, nil
}

// RunOne must receive a process/worker lifecycle context, never an HTTP request
// context. true means an item was durably claimed, including when execution fails.
// Failure/cancellation leaves the RUNNING item intact for later reconciliation.
func (w *Worker) RunOne(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	claim, err := w.opts.Store.ClaimOperationItem(ctx, uuid.New(), w.opts.LeaseDuration)
	if errors.Is(err, repository.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	// Recheck committed ownership immediately before entering the external
	// boundary. A delayed commit response must not authorize expired execution.
	claim.ExpiresAt, err = w.Renew(ctx, claim.Lease)
	if err != nil {
		return true, err
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	// The durable parent, including an absent/invalid context, is authoritative;
	// an incidental caller span must not become this operation's parent.
	ctx = trace.ContextWithSpanContext(ctx, trace.SpanContext{})
	tc := tracecontext.Normalize(claim.Operation.Traceparent, claim.Operation.Tracestate)
	ctx = tracecontext.NewContext(ctx, tc)
	ctx, span := w.opts.Tracer.Start(ctx, "OperationItem execute", trace.WithAttributes(
		attribute.String("operation_id", claim.Operation.ID.String()),
		attribute.String("operation_item_id", claim.Item.ID.String()),
		attribute.String("operation_type", claim.Operation.Type),
	))
	defer span.End()
	err = w.opts.Execute(ctx, claim)
	if err != nil {
		// Callback errors may contain Provider payloads; do not record their text.
		span.SetStatus(codes.Error, "execution boundary failed")
	}
	return true, err
}

// Renew is explicit: long-running execution must maintain ownership and fence
// later DB writes with the same lease. Renewal never dispatches or changes status.
func (w *Worker) Renew(ctx context.Context, lease repository.OperationItemLease) (time.Time, error) {
	return w.opts.Store.RenewOperationItemLease(ctx, lease, w.opts.LeaseDuration)
}
