package provision

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

type workerStoreFake struct {
	claim func(context.Context, uuid.UUID, time.Duration) (repository.OperationItemClaim, error)
	renew func(context.Context, repository.OperationItemLease, time.Duration) (time.Time, error)
}

func (f workerStoreFake) ClaimOperationItem(ctx context.Context, owner uuid.UUID, d time.Duration) (repository.OperationItemClaim, error) {
	return f.claim(ctx, owner, d)
}
func (f workerStoreFake) RenewOperationItemLease(ctx context.Context, l repository.OperationItemLease, d time.Duration) (time.Time, error) {
	return f.renew(ctx, l, d)
}

type provisionStoreFake struct {
	workerStoreFake
	prepare func(context.Context, repository.OperationItemLease) (repository.ProvisionDispatch, error)
	write   func(context.Context, repository.ProvisionUpdate) error
}

func (f provisionStoreFake) ClaimProvisionOperationItem(ctx context.Context, owner uuid.UUID, d time.Duration) (repository.OperationItemClaim, error) {
	return f.workerStoreFake.ClaimOperationItem(ctx, owner, d)
}
func (f provisionStoreFake) PrepareProvision(ctx context.Context, l repository.OperationItemLease) (repository.ProvisionDispatch, error) {
	return f.prepare(ctx, l)
}
func (f provisionStoreFake) WriteProvision(ctx context.Context, u repository.ProvisionUpdate) error {
	return f.write(ctx, u)
}

type provisionRouterFake struct {
	send func(context.Context, connector.OperationCommand) (connector.SentMessage, error)
}

func (f provisionRouterFake) SendOperationCommand(ctx context.Context, c connector.OperationCommand) (connector.SentMessage, error) {
	return f.send(ctx, c)
}
func (f provisionRouterFake) ForgetOperation(uuid.UUID, connector.Correlation) bool { return true }

func provisionTestStore(t *testing.T, writes chan repository.ProvisionUpdate) provisionStoreFake {
	t.Helper()
	d := repository.ProvisionDispatch{OrganizationID: uuid.New(), OperationID: uuid.New(), LabInstanceID: uuid.New(), LabExecutionID: uuid.New(), ProviderConnectionID: uuid.New(), ConnectorID: uuid.New(), Generation: 1}
	d.Snapshot, _ = json.Marshal(protocol.CreationSnapshot{ProviderConnectionID: d.ProviderConnectionID.String(), WorkspaceVMKey: "vm-1", VMs: []protocol.ResolvedVmSpec{{VMKey: "vm-1", Role: "web", ImageID: "immutable-image", FlavorID: "immutable-flavor", FlavorSpec: &protocol.ResolvedFlavorSpec{VCPUs: 1, RAMMiB: 512}}}})
	var mu sync.Mutex
	claimed := false
	f := provisionStoreFake{workerStoreFake: workerStoreFake{
		claim: func(_ context.Context, owner uuid.UUID, _ time.Duration) (repository.OperationItemClaim, error) {
			mu.Lock()
			defer mu.Unlock()
			if claimed {
				return repository.OperationItemClaim{}, repository.ErrNotFound
			}
			claimed = true
			return repository.OperationItemClaim{Operation: repository.Operation{ID: d.OperationID, Type: "PROVISION"}, Lease: repository.OperationItemLease{ItemID: uuid.New(), Owner: owner}}, nil
		},
		renew: func(context.Context, repository.OperationItemLease, time.Duration) (time.Time, error) {
			return time.Now().Add(time.Minute), nil
		},
	}, prepare: func(_ context.Context, l repository.OperationItemLease) (repository.ProvisionDispatch, error) {
		copy := d
		copy.Lease = l
		return copy, nil
	},
		write: func(_ context.Context, u repository.ProvisionUpdate) error { writes <- u; return nil },
	}
	return f
}

func routedFor(c connector.OperationCommand) connector.Routed {
	return connector.Routed{ConnectorID: c.ConnectorID, Correlation: c.Correlation, MessageID: uuid.NewString(), RequestMessageID: "outbound-message"}
}

func TestExecutorRetainsImmediateResultBeforeSendReturns(t *testing.T) {
	for _, sendFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "sent", true: "write returned error"}[sendFails], func(t *testing.T) {
			writes := make(chan repository.ProvisionUpdate, 10)
			var p *Executor
			router := provisionRouterFake{send: func(_ context.Context, c connector.OperationCommand) (connector.SentMessage, error) {
				if c.Payload.CreationSnapshot.VMs[0].ImageID != "immutable-image" {
					t.Error("snapshot changed")
				}
				for range 1000 {
					p.HandleEvent(connector.OperationAckEvent{Routed: routedFor(c), Payload: protocol.OperationAckPayload{Accepted: true}})
				}
				p.HandleEvent(connector.OperationResultEvent{Routed: routedFor(c), Payload: protocol.OperationResultPayload{Outcome: "SUCCEEDED", ProviderResources: []protocol.ProviderResourceResult{{ProviderResourceRef: protocol.ProviderResourceRef{ResourceType: "KT_TIER", ProviderID: "exact-ID", Generation: 1, LogicalName: "lab-tier"}}}}})
				if sendFails {
					return connector.SentMessage{}, connector.ErrSendFailed
				}
				return connector.SentMessage{MessageID: "outbound-message"}, nil
			}}
			var err error
			p, err = NewExecutor(Options{Store: provisionTestStore(t, writes), Router: router})
			if err != nil {
				t.Fatal(err)
			}
			if claimed, err := p.RunOne(t.Context()); !claimed || err != nil {
				t.Fatalf("run=%v %v", claimed, err)
			}
			var result repository.ProvisionUpdate
			for len(writes) > 0 {
				u := <-writes
				if u.Status == "SUCCEEDED" {
					result = u
				}
			}
			if result.Status != "SUCCEEDED" || len(result.Resources) != 1 || result.Resources[0].ProviderID != "exact-ID" {
				t.Fatalf("lost early result: %+v", result)
			}
			if claimed, err := p.RunOne(t.Context()); claimed || err != nil {
				t.Fatalf("retry=%v %v", claimed, err)
			}
		})
	}
}

func TestProvisionEventDeliveryNeverWaitsForDatabaseWriter(t *testing.T) {
	writes := make(chan repository.ProvisionUpdate, 10)
	store := provisionTestStore(t, writes)
	blocked, release := make(chan struct{}), make(chan struct{})
	store.write = func(ctx context.Context, u repository.ProvisionUpdate) error {
		if u.Status == "RUNNING" {
			close(blocked)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		writes <- u
		return nil
	}
	var p *Executor
	var cmd connector.OperationCommand
	p, err := NewExecutor(Options{Store: store, Router: provisionRouterFake{send: func(_ context.Context, c connector.OperationCommand) (connector.SentMessage, error) {
		cmd = c
		p.HandleEvent(connector.OperationAckEvent{Routed: routedFor(c), Payload: protocol.OperationAckPayload{Accepted: true}})
		return connector.SentMessage{MessageID: "outbound-message"}, nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := p.RunOne(t.Context()); done <- err }()
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("ACK writer did not block")
	}
	delivered := make(chan struct{})
	go func() {
		p.HandleEvent(connector.OperationResultEvent{Routed: routedFor(cmd), Payload: protocol.OperationResultPayload{Outcome: "SUCCEEDED"}})
		close(delivered)
	}()
	select {
	case <-delivered:
	case <-time.After(time.Second):
		t.Fatal("read loop blocked on DB")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestExecutorRejectsWrongCorrelationAndReplyID(t *testing.T) {
	writes := make(chan repository.ProvisionUpdate, 10)
	var p *Executor
	p, err := NewExecutor(Options{Store: provisionTestStore(t, writes), ResultTimeout: 20 * time.Millisecond, Router: provisionRouterFake{send: func(_ context.Context, c connector.OperationCommand) (connector.SentMessage, error) {
		for _, wrong := range []string{"connector", "operation", "instance", "generation", "reply"} {
			r := routedFor(c)
			switch wrong {
			case "connector":
				r.ConnectorID = uuid.New()
			case "operation":
				r.Correlation.OperationID = uuid.NewString()
			case "instance":
				r.Correlation.LabInstanceID = uuid.NewString()
			case "generation":
				r.Correlation.Generation = 2
			case "reply":
				r.RequestMessageID = "wrong-message"
			}
			p.HandleEvent(connector.OperationResultEvent{Routed: r, Payload: protocol.OperationResultPayload{Outcome: "SUCCEEDED"}})
		}
		return connector.SentMessage{MessageID: "outbound-message"}, nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := p.RunOne(t.Context()); !claimed || err != nil {
		t.Fatalf("run=%v %v", claimed, err)
	}
	u := <-writes
	if u.Status != "RECONCILING" || u.ErrorCode != "provision_response_unconfirmed" || len(writes) != 0 {
		t.Fatalf("wrong result accepted: %+v", u)
	}
}

func TestExecutorUncertainSendAndAckOnlyNeverReplay(t *testing.T) {
	for _, scenario := range []string{"send failed", "ack only", "rejected", "rejected after accepted", "rejected before send error", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			writes := make(chan repository.ProvisionUpdate, 10)
			calls := 0
			var p *Executor
			p, err := NewExecutor(Options{Store: provisionTestStore(t, writes), ResultTimeout: 20 * time.Millisecond, Router: provisionRouterFake{send: func(_ context.Context, c connector.OperationCommand) (connector.SentMessage, error) {
				calls++
				if scenario == "send failed" {
					return connector.SentMessage{}, connector.ErrSendFailed
				}
				if scenario == "rejected after accepted" {
					p.HandleEvent(connector.OperationAckEvent{Routed: routedFor(c), Payload: protocol.OperationAckPayload{Accepted: true}})
				}
				if scenario == "unknown" {
					p.HandleEvent(connector.OperationResultEvent{Routed: routedFor(c), Payload: protocol.OperationResultPayload{Outcome: "UNKNOWN"}})
				} else {
					p.HandleEvent(connector.OperationAckEvent{Routed: routedFor(c), Payload: protocol.OperationAckPayload{Accepted: scenario == "ack only", Error: &protocol.SafeError{Code: "REJECTED", Message: "raw-sensitive-marker"}}})
				}
				if scenario == "rejected before send error" {
					return connector.SentMessage{}, connector.ErrSendFailed
				}
				return connector.SentMessage{MessageID: "outbound-message"}, nil
			}}})
			if err != nil {
				t.Fatal(err)
			}
			if claimed, err := p.RunOne(t.Context()); !claimed || err != nil {
				t.Fatalf("run=%v %v", claimed, err)
			}
			if claimed, err := p.RunOne(t.Context()); claimed || err != nil || calls != 1 {
				t.Fatalf("blind retry=%v %v calls=%d", claimed, err, calls)
			}
			var last repository.ProvisionUpdate
			for len(writes) > 0 {
				last = <-writes
				if last.Status == "SUCCEEDED" || last.ErrorMessage == "raw-sensitive-marker" {
					t.Fatalf("unconfirmed completion/leak: %+v", last)
				}
			}
			want := "RECONCILING"
			if scenario == "rejected" || scenario == "rejected after accepted" || scenario == "rejected before send error" {
				want = "FAILED"
			}
			if last.Status != want {
				t.Fatalf("final=%+v", last)
			}
		})
	}
}

func TestExecutorDoesNotDispatchAfterActivationLosesLease(t *testing.T) {
	writes := make(chan repository.ProvisionUpdate, 10)
	store := provisionTestStore(t, writes)
	count := 0
	store.renew = func(context.Context, repository.OperationItemLease, time.Duration) (time.Time, error) {
		count++
		if count == 2 {
			return time.Time{}, repository.ErrOperationLeaseLost
		}
		return time.Now().Add(time.Minute), nil
	}
	p, err := NewExecutor(Options{Store: store, Router: provisionRouterFake{send: func(context.Context, connector.OperationCommand) (connector.SentMessage, error) {
		t.Error("stale dispatch")
		return connector.SentMessage{}, nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := p.RunOne(t.Context()); !claimed || !errors.Is(err, repository.ErrOperationLeaseLost) {
		t.Fatalf("run=%v %v", claimed, err)
	}
	if len(writes) != 0 {
		t.Fatal("stale writer")
	}
}
