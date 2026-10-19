package operation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
	"github.com/ktcloud4-SL/labbit-app/internal/server/tracecontext"
)

type workerStoreFake struct {
	claim func(context.Context, uuid.UUID, time.Duration) (repository.OperationItemClaim, error)
	renew func(context.Context, repository.OperationItemLease, time.Duration) (time.Time, error)
}

func (f workerStoreFake) ClaimOperationItem(ctx context.Context, owner uuid.UUID, d time.Duration) (repository.OperationItemClaim, error) {
	return f.claim(ctx, owner, d)
}
func (f workerStoreFake) RenewOperationItemLease(ctx context.Context, lease repository.OperationItemLease, d time.Duration) (time.Time, error) {
	return f.renew(ctx, lease, d)
}

func TestWorkerDoesNotExecuteWithoutCommittedOwnership(t *testing.T) {
	commitFailed := errors.New("commit outcome unavailable")
	for _, scenario := range []string{"empty", "commit failed", "lease lost", "cancel before claim", "cancel after claim"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			store := workerStoreFake{
				claim: func(context.Context, uuid.UUID, time.Duration) (repository.OperationItemClaim, error) {
					switch scenario {
					case "empty":
						return repository.OperationItemClaim{}, repository.ErrNotFound
					case "commit failed":
						return repository.OperationItemClaim{Item: repository.OperationItem{ID: uuid.New()}}, commitFailed
					case "cancel after claim":
						cancel()
					case "cancel before claim":
						t.Error("cancelled worker claimed new work")
					}
					return repository.OperationItemClaim{}, nil
				},
				renew: func(context.Context, repository.OperationItemLease, time.Duration) (time.Time, error) {
					return time.Time{}, repository.ErrOperationLeaseLost
				},
			}
			w, err := NewWorker(WorkerOptions{Store: store, LeaseDuration: time.Minute,
				Execute: func(context.Context, repository.OperationItemClaim) error {
					t.Error("callback ran without committed live ownership")
					return nil
				}})
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "cancel before claim" {
				cancel()
			}
			claimed, err := w.RunOne(ctx)
			wantClaimed := scenario == "lease lost" || scenario == "cancel after claim"
			wantErr := map[string]error{"commit failed": commitFailed, "lease lost": repository.ErrOperationLeaseLost,
				"cancel before claim": context.Canceled, "cancel after claim": context.Canceled}[scenario]
			if claimed != wantClaimed || !errors.Is(err, wantErr) {
				t.Fatalf("claimed=%v err=%v want=%v/%v", claimed, err, wantClaimed, wantErr)
			}
		})
	}
}

func TestWorkerUsesFreshClaimOwnerAndDoesNotRetryCallback(t *testing.T) {
	var owners []uuid.UUID
	calls := 0
	callbackErr := errors.New("external outcome unknown")
	store := workerStoreFake{
		claim: func(_ context.Context, owner uuid.UUID, _ time.Duration) (repository.OperationItemClaim, error) {
			owners = append(owners, owner)
			return repository.OperationItemClaim{Lease: repository.OperationItemLease{ItemID: uuid.New(), Owner: owner}}, nil
		},
		renew: func(context.Context, repository.OperationItemLease, time.Duration) (time.Time, error) {
			return time.Now().Add(time.Minute), nil
		},
	}
	w, err := NewWorker(WorkerOptions{Store: store, LeaseDuration: time.Minute,
		Execute: func(context.Context, repository.OperationItemClaim) error { calls++; return callbackErr }})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if claimed, err := w.RunOne(t.Context()); !claimed || !errors.Is(err, callbackErr) {
			t.Fatalf("run=%v/%v", claimed, err)
		}
	}
	if calls != 2 || len(owners) != 2 || owners[0] == uuid.Nil || owners[1] == uuid.Nil || owners[0] == owners[1] {
		t.Fatalf("callbacks=%d owners=%v", calls, owners)
	}
}

func TestWorkerRestoresDurableTraceOnItsLifecycle(t *testing.T) {
	for _, parent := range []string{"00-0123456789abcdef0123456789abcdef-0123456789abcdef-01", "invalid", ""} {
		t.Run(parent, func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			defer tp.Shutdown(context.Background())
			tracer := tp.Tracer("worker-test")
			ctx, caller := tracer.Start(t.Context(), "incidental caller")
			defer caller.End()
			id := uuid.New()
			sentinel := errors.New("provider-raw-payload-marker")
			store := workerStoreFake{
				claim: func(_ context.Context, owner uuid.UUID, _ time.Duration) (repository.OperationItemClaim, error) {
					return repository.OperationItemClaim{Operation: repository.Operation{ID: id, Type: "PROVISION", Traceparent: parent, Tracestate: "vendor=dummy"},
						Item: repository.OperationItem{ID: uuid.New()}, Lease: repository.OperationItemLease{ItemID: uuid.New(), Owner: owner}}, nil
				},
				renew: func(context.Context, repository.OperationItemLease, time.Duration) (time.Time, error) {
					return time.Now().Add(time.Minute), nil
				},
			}
			w, err := NewWorker(WorkerOptions{Store: store, LeaseDuration: time.Minute, Tracer: tracer,
				Execute: func(execution context.Context, claim repository.OperationItemClaim) error {
					if claim.Operation.ID != id || execution.Err() != nil || !trace.SpanContextFromContext(execution).IsValid() {
						t.Error("missing execution context/correlation")
					}
					return sentinel
				}})
			if err != nil {
				t.Fatal(err)
			}
			if claimed, err := w.RunOne(ctx); !claimed || !errors.Is(err, sentinel) {
				t.Fatalf("run=%v/%v", claimed, err)
			}
			spans := recorder.Ended()
			if len(spans) != 1 {
				t.Fatalf("worker spans=%d", len(spans))
			}
			span := spans[0]
			durable := tracecontext.Normalize(parent, "vendor=dummy")
			if durable.Valid() {
				want := trace.SpanContextFromContext(tracecontext.NewContext(context.Background(), durable))
				if !span.Parent().Equal(want) || span.SpanContext().TraceID() != want.TraceID() || span.SpanContext().SpanID() == want.SpanID() {
					t.Error("worker span did not continue durable parent with fresh span ID")
				}
			} else if span.Parent().IsValid() || span.SpanContext().TraceID() == caller.SpanContext().TraceID() {
				t.Error("invalid/absent durable parent used incidental caller trace")
			}
			if span.Status().Code != codes.Error || span.Status().Description == sentinel.Error() || len(span.Events()) != 0 {
				t.Error("external error was not safely summarized")
			}
		})
	}
}

func TestWorkerRequiresExplicitExecutionAndLeaseDuration(t *testing.T) {
	for _, opts := range []WorkerOptions{{}, {Store: workerStoreFake{}, LeaseDuration: time.Minute},
		{Store: workerStoreFake{}, Execute: func(context.Context, repository.OperationItemClaim) error { return nil }, LeaseDuration: time.Nanosecond}} {
		if _, err := NewWorker(opts); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid worker options=%v", err)
		}
	}
}
