package provision

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

type mutationStoreFake struct {
	provisionStoreFake
	kind string
}

func (f mutationStoreFake) PrepareMutation(ctx context.Context, l repository.OperationItemLease) (repository.ProvisionDispatch, error) {
	d, err := f.prepare(ctx, l)
	d.MutationType = f.kind
	d.Generation = 2
	d.Resources = []repository.ProvisionResource{{Type: "SERVER", ProviderID: "previous-exact-ID", LogicalName: "vm-1", Generation: 1}}
	return d, err
}
func (f mutationStoreFake) WriteMutation(ctx context.Context, u repository.ProvisionUpdate) error {
	return f.write(ctx, u)
}
func (f mutationStoreFake) DeferUnsentMutation(ctx context.Context, d repository.ProvisionDispatch, delay time.Duration) error {
	return f.deferUnsent(ctx, d, delay)
}

func TestMutationExecutorUsesSharedUncertaintyAndCorrelationBoundary(t *testing.T) {
	for _, kind := range []string{"RESET", "CLEANUP"} {
		for _, scenario := range []string{"success", "unknown", "timeout", "ambiguous send", "unsent", "lifecycle failure"} {
			t.Run(kind+"/"+scenario, func(t *testing.T) {
				writes := make(chan repository.ProvisionUpdate, 10)
				base := provisionTestStore(t, writes)
				original := base.claim
				base.claim = func(ctx context.Context, owner uuid.UUID, duration time.Duration) (repository.OperationItemClaim, error) {
					c, err := original(ctx, owner, duration)
					c.Operation.Type = kind
					return c, err
				}
				deferred := 0
				base.deferUnsent = func(context.Context, repository.ProvisionDispatch, time.Duration) error { deferred++; return nil }
				store := mutationStoreFake{base, kind}
				closed := false
				calls := 0
				var executor *Executor
				var err error
				executor, err = NewExecutor(Options{Store: base, Mutations: store, ResultTimeout: 10 * time.Millisecond,
					CloseForMutation: func(context.Context, repository.ProvisionDispatch) error {
						closed = true
						if scenario == "lifecycle failure" {
							return repository.ErrOperationLeaseLost
						}
						return nil
					},
					Router: provisionRouterFake{send: func(_ context.Context, c connector.OperationCommand) (connector.SentMessage, error) {
						calls++
						if !closed || c.Payload.MutationType != kind || len(c.Payload.ProviderResources) != 1 || c.Payload.ProviderResources[0].ProviderID != "previous-exact-ID" || c.Correlation.Generation != 2 {
							t.Error("mutation dispatch lost lifecycle/resource/correlation")
						}
						if kind == "RESET" && (c.Payload.CreationSnapshot == nil || c.Payload.CreationSnapshot.VMs[0].ImageID != "immutable-image") {
							t.Error("reset snapshot not preserved")
						}
						if kind == "CLEANUP" && c.Payload.CreationSnapshot != nil {
							t.Error("cleanup contains snapshot")
						}
						switch scenario {
						case "ambiguous send":
							return connector.SentMessage{}, connector.ErrSendFailed
						case "unsent":
							return connector.SentMessage{}, connector.ErrNotConnected
						case "timeout":
							return connector.SentMessage{MessageID: "outbound-message"}, nil
						}
						outcome := protocol.OutcomeSucceeded
						if scenario == "unknown" {
							outcome = protocol.OutcomeUnknown
						}
						executor.HandleEvent(connector.OperationResultEvent{Routed: routedFor(c), Payload: protocol.OperationResultPayload{Outcome: outcome}})
						return connector.SentMessage{MessageID: "outbound-message"}, nil
					}}})
				if err != nil {
					t.Fatal(err)
				}
				_, err = executor.RunOne(t.Context())
				if scenario == "lifecycle failure" {
					if err == nil || calls != 0 || len(writes) != 0 {
						t.Fatalf("dispatched after failed lifecycle close: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "unsent" {
					if deferred != 1 || len(writes) != 0 {
						t.Fatal("guaranteed unsent not deferred")
					}
				} else {
					var last repository.ProvisionUpdate
					for len(writes) > 0 {
						last = <-writes
					}
					want := "RECONCILING"
					if scenario == "success" {
						want = "SUCCEEDED"
					}
					if last.Status != want || last.Dispatch.MutationType != kind {
						t.Fatalf("result=%+v", last)
					}
				}
				if claimed, err := executor.RunOne(t.Context()); claimed || err != nil || calls != 1 {
					t.Fatalf("blind retry=%v %v calls=%d", claimed, err, calls)
				}
			})
		}
	}
}
