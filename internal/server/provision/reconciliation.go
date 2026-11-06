package provision

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

type reconcileRouter interface {
	SendReconcileRequest(context.Context, connector.ReconcileRequest) (connector.SentMessage, error)
	ForgetReconcile(uuid.UUID, string) bool
}

// This adapter reuses Worker.RunOne's commit, renewal, trace and cancellation
// boundary. Reconciliation gets a new claim owner and cannot dispatch mutations.
type reconciliationClaims struct {
	repository.OperationWorkerStore
	store repository.ReconciliationStore
}

func (s reconciliationClaims) ClaimOperationItem(ctx context.Context, owner uuid.UUID, duration time.Duration) (repository.OperationItemClaim, error) {
	c, err := s.OperationWorkerStore.ClaimOperationItem(ctx, owner, duration)
	if errors.Is(err, repository.ErrNotFound) {
		return s.store.ClaimReconciliation(ctx, owner, duration)
	}
	return c, err
}
func (s reconciliationClaims) RenewOperationItemLease(ctx context.Context, lease repository.OperationItemLease, duration time.Duration) (time.Time, error) {
	at, err := s.OperationWorkerStore.RenewOperationItemLease(ctx, lease, duration)
	if errors.Is(err, repository.ErrOperationLeaseLost) {
		return s.store.RenewReconciliation(ctx, lease, duration)
	}
	return at, err
}

func (p *Executor) reconcile(ctx context.Context, claim repository.OperationItemClaim) error {
	d, err := p.opts.Reconciliation.PrepareReconciliation(ctx, claim.Lease)
	if err != nil {
		return err
	}
	router := p.opts.Router.(reconcileRouter)
	corr := connector.Correlation{OperationID: d.OperationID.String(), LabInstanceID: d.LabInstanceID.String(), Generation: d.Generation}
	key := provisionKey{d.ConnectorID, corr}
	inbox := &provisionInbox{reconcile: make(chan connector.ReconcileResultEvent, 1)}
	p.mu.Lock()
	if p.pending[key] != nil {
		p.mu.Unlock()
		return repository.ErrConflict
	}
	p.pending[key] = inbox
	p.mu.Unlock()
	var sent connector.SentMessage
	defer func() {
		p.mu.Lock()
		delete(p.pending, key)
		p.mu.Unlock()
		if sent.MessageID != "" {
			router.ForgetReconcile(d.ConnectorID, sent.MessageID)
		}
	}()
	refs := make([]protocol.ProviderResourceRef, 0, len(d.Resources))
	for _, r := range d.Resources {
		refs = append(refs, protocol.ProviderResourceRef{ResourceType: r.Type, ProviderID: r.ProviderID, Generation: r.Generation, LogicalName: r.LogicalName})
	}
	if _, err = p.worker.Renew(ctx, d.Lease); err != nil {
		return err
	}
	discover := true
	sent, err = router.SendReconcileRequest(ctx, connector.ReconcileRequest{ConnectorID: d.ConnectorID, RequestID: claim.Operation.RequestID, Correlation: corr,
		Payload: protocol.ReconcileRequestPayload{KnownResources: refs, DiscoverCandidates: &discover}, Trace: connector.TraceFromContext(ctx)})
	hold := func() error {
		return p.opts.Reconciliation.WriteReconciliation(ctx, repository.ReconciliationUpdate{Dispatch: d, QueryFailed: true, RetryDelay: p.opts.DispatchRetryDelay})
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return hold()
	}
	ticker := time.NewTicker(p.opts.LeaseDuration / 3)
	defer ticker.Stop()
	timer := time.NewTimer(p.opts.ResultTimeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err = p.worker.Renew(ctx, d.Lease); err != nil {
				return err
			}
		case <-timer.C:
			return hold()
		case e := <-inbox.reconcile:
			if e.RequestMessageID != sent.MessageID {
				continue
			}
			u := repository.ReconciliationUpdate{Dispatch: d, QueryFailed: e.Payload.Error != nil, RetryDelay: p.opts.DispatchRetryDelay}
			for _, o := range e.Payload.Observations {
				u.Observations = append(u.Observations, repository.ResourceObservation{ProvisionResource: repository.ProvisionResource{Type: o.ResourceType, ProviderID: o.ProviderID, Generation: o.Generation, LogicalName: o.LogicalName, ObservedState: o.ObservedState}, Exists: o.Exists, Source: o.Source})
			}
			err = p.opts.Reconciliation.WriteReconciliation(ctx, u)
			if errors.Is(err, repository.ErrConstraintViolation) {
				return hold()
			}
			return err
		}
	}
}
