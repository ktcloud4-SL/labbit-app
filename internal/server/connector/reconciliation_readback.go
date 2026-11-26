package connector

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/valkeyowner"
)

var ErrReconciliationReadback = errors.New("connector: reconciliation readback could not be verified")

// ReconciliationReadback binds a read-only Provider result to the authenticated
// control Session and to the request it answers. It does not assert inventory
// completeness or update the operation journal.
type ReconciliationReadback struct {
	ConnectorID      uuid.UUID
	SessionID        uuid.UUID
	RequestID        string
	RequestMessageID string
	ResultMessageID  string
	Correlation      Correlation
	StartedAt        time.Time
	ObservedAt       time.Time
	OwnerBefore      ReconciliationLease
	OwnerAfter       ReconciliationLease
	Payload          protocol.ReconcileResultPayload
}

// ReconciliationLease omits the internal address and ties the local authenticated
// Session to an authoritative Valkey observation.
type ReconciliationLease struct {
	ConnectorID uuid.UUID `json:"connector_id"`
	PodID       string    `json:"pod_id"`
	Epoch       int64     `json:"epoch"`
	SessionID   uuid.UUID `json:"session_id"`
	ObservedAt  time.Time `json:"observed_at_utc"`
	ExpiresAt   time.Time `json:"expires_at_utc"`
}

type ReconciliationOwnerLocator interface {
	PodID() string
	ObserveConnectorOwner(context.Context, uuid.UUID) (*valkeyowner.ConnectorOwnerRecord, error)
}

// ReadReconciliation is an in-process read-only port. Its caller authorizes the
// RUN, the target Pod and the exact resource scope before invoking it.
func (r *Router) ReadReconciliation(ctx context.Context, req ReconcileRequest, locator ReconciliationOwnerLocator) (ReconciliationReadback, error) {
	if locator == nil || req.ConnectorID == uuid.Nil || req.SessionID == uuid.Nil || strings.TrimSpace(req.RequestID) == "" {
		return ReconciliationReadback{}, ErrReconciliationReadback
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	var epoch int64
	err := r.registry.WithReadyRoute(req.ConnectorID, func(current Session, _ Route) error {
		if current.ID != req.SessionID || current.OwnerEpoch <= 0 {
			return ErrReconciliationReadback
		}
		epoch = current.OwnerEpoch
		return nil
	})
	if err != nil {
		return ReconciliationReadback{}, err
	}

	// Authoritative calls are outside registry locks. Recheck the exact local
	// route inside sendReconcileRequest and again after the final owner lookup.
	before, err := observeLease(ctx, locator, req, epoch)
	if err != nil {
		return ReconciliationReadback{}, err
	}
	result, err := r.readReconciliation(ctx, req, epoch)
	if err != nil {
		return ReconciliationReadback{}, err
	}
	after, err := observeLease(ctx, locator, req, epoch)
	if err != nil || before.PodID != after.PodID || !result.StartedAt.Before(before.ExpiresAt) ||
		!result.ObservedAt.Before(before.ExpiresAt) || result.StartedAt.Before(before.ObservedAt) ||
		result.ObservedAt.Before(result.StartedAt) || after.ObservedAt.Before(result.ObservedAt) {
		return ReconciliationReadback{}, ErrReconciliationReadback
	}
	err = r.registry.WithReadyRoute(req.ConnectorID, func(current Session, _ Route) error {
		if current.ID != req.SessionID || current.OwnerEpoch != epoch || !time.Now().Before(after.ExpiresAt) {
			return ErrReconciliationReadback
		}
		return ctx.Err()
	})
	if err != nil {
		return ReconciliationReadback{}, err
	}
	result.OwnerBefore, result.OwnerAfter = before, after
	return result, nil
}

func observeLease(ctx context.Context, locator ReconciliationOwnerLocator, req ReconcileRequest, epoch int64) (ReconciliationLease, error) {
	owner, err := locator.ObserveConnectorOwner(ctx, req.ConnectorID)
	at := time.Now().UTC()
	if err != nil || ctx.Err() != nil || owner == nil {
		return ReconciliationLease{}, ErrReconciliationReadback
	}
	podID := locator.PodID()
	if podID == "" || owner.PodID != podID ||
		owner.ConnectorID != req.ConnectorID || owner.Epoch != epoch || !at.Before(owner.ExpiresAt) {
		return ReconciliationLease{}, ErrReconciliationReadback
	}
	return ReconciliationLease{ConnectorID: req.ConnectorID, PodID: podID, Epoch: epoch, SessionID: req.SessionID,
		ObservedAt: at, ExpiresAt: owner.ExpiresAt.UTC()}, nil
}

func (r *Router) readReconciliation(ctx context.Context, req ReconcileRequest, epoch int64) (ReconciliationReadback, error) {
	reply := make(chan ReconciliationReadback, 1)
	startedAt := time.Now().UTC()
	sent, err := r.sendReconcileRequest(ctx, req, reply, epoch)
	if err != nil {
		return ReconciliationReadback{}, err
	}
	defer r.ForgetReconcile(req.ConnectorID, sent.MessageID)
	select {
	case <-ctx.Done():
		return ReconciliationReadback{}, ctx.Err()
	case result := <-reply:
		if ctx.Err() != nil {
			return ReconciliationReadback{}, ctx.Err()
		}
		if result.ResultMessageID == "" || result.Payload.Error != nil {
			return ReconciliationReadback{}, ErrReconciliationReadback
		}
		err := r.registry.WithReadyRoute(req.ConnectorID, func(current Session, _ Route) error {
			if current.ID != result.SessionID {
				return ErrReconciliationReadback
			}
			return nil
		})
		if err != nil {
			return ReconciliationReadback{}, err
		}
		if err := ctx.Err(); err != nil {
			return ReconciliationReadback{}, err
		}
		result.StartedAt = startedAt
		return result, nil
	}
}
