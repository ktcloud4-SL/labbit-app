// Package workercontrol fences short claim/dispatch boundaries. The default is
// process-local; an explicit durable backend coordinates participating instances
// on one authoritative Store. Neither backend authorizes or proves a Writer.
package workercontrol

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Mode uint8

const (
	Active Mode = iota
	PauseNew
	ReconcileOnly
)

var (
	ErrInvalidMode = errors.New("workercontrol: invalid mode")
	ErrPaused      = errors.New("workercontrol: new work is paused")
)

func ParseMode(raw string) (Mode, error) {
	switch strings.TrimSpace(raw) {
	case "", "ACTIVE":
		return Active, nil
	case "PAUSE_NEW":
		return PauseNew, nil
	case "RECONCILE_ONLY":
		return ReconcileOnly, nil
	default:
		return 0, ErrInvalidMode
	}
}

func (m Mode) Validate() error {
	if m != Active && m != PauseNew && m != ReconcileOnly {
		return ErrInvalidMode
	}
	return nil
}

// Control must be constructed with New and shared by the Executor and Router.
// A zero/nil Control fails closed. Pause is one-way for this process lifetime;
// there is deliberately no resume API or implied approval/epoch mechanism.
type Control struct {
	mu          sync.Mutex
	initialized bool
	mode        Mode
	inflight    uint64
	changed     chan struct{}
	durable     *durableControl
	unverified  bool
}

type Snapshot struct {
	Mode       Mode
	InFlight   uint64 // short claim/prepare/send boundaries, not Provider work
	Quiesced   bool   // PAUSE_NEW and all local boundaries drained, never global
	Unverified bool   // durable entry/close failed; no automatic reopening
}

func New(mode Mode) (*Control, error) {
	if err := mode.Validate(); err != nil {
		return nil, err
	}
	return &Control{initialized: true, mode: mode, changed: make(chan struct{})}, nil
}

func (c *Control) Snapshot() Snapshot {
	if c == nil {
		return Snapshot{Mode: PauseNew}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.initialized {
		return Snapshot{Mode: PauseNew}
	}
	return Snapshot{Mode: c.mode, InFlight: c.inflight, Quiesced: c.mode == PauseNew && c.inflight == 0 && !c.unverified, Unverified: c.unverified}
}

func (c *Control) AcquireMutation(ctx context.Context) (func(), error) {
	return c.AcquireMutationFor(ctx, MutationDispatch)
}

func (c *Control) AcquireReconciliation(ctx context.Context) (func(), error) {
	return c.AcquireReconciliationFor(ctx, ReconciliationDispatch)
}

func (c *Control) AcquireMutationFor(ctx context.Context, phase Phase) (func(), error) {
	if !phase.Matches(MutationBoundary) {
		return nil, ErrInvalidBinding
	}
	return c.acquire(ctx, false, phase)
}
func (c *Control) AcquireReconciliationFor(ctx context.Context, phase Phase) (func(), error) {
	if !phase.Matches(ReconciliationBoundary) {
		return nil, ErrInvalidBinding
	}
	return c.acquire(ctx, true, phase)
}
func (c *Control) acquire(ctx context.Context, reconciliation bool, phase Phase) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil {
		return nil, ErrPaused
	}
	c.mu.Lock()
	if !c.initialized || (c.mode != Active && !(reconciliation && c.mode == ReconcileOnly)) {
		c.mu.Unlock()
		return nil, ErrPaused
	}
	if c.unverified {
		c.mu.Unlock()
		return nil, ErrUnavailable
	}
	c.inflight++
	c.mu.Unlock()
	var once sync.Once
	localRelease := func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.inflight--
			close(c.changed)
			c.changed = make(chan struct{})
		})
	}
	if c.durable == nil {
		return localRelease, nil
	}
	kind := MutationBoundary
	if reconciliation {
		kind = ReconciliationBoundary
	}
	ticket := Ticket{Binding: c.durable.binding, ID: uuid.New(), Kind: kind, Phase: phase}
	// An ambiguous Enter is never closed or retried: work must not begin and its
	// possibly committed intent remains visible to authoritative drain checks.
	entryCtx, cancelEntry := context.WithTimeout(ctx, durableQueryTimeout)
	entryErr := c.durable.store.Enter(entryCtx, ticket)
	cancelEntry()
	if err := entryErr; err != nil {
		c.mu.Lock()
		c.unverified = true
		c.mu.Unlock()
		localRelease()
		if err == ErrStale || err == ErrPaused {
			return nil, err
		}
		return nil, ErrUnavailable
	}
	var durableOnce sync.Once
	release := func() {
		durableOnce.Do(func() {
			closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if err := c.durable.store.Leave(closeCtx, ticket); err != nil {
				c.mu.Lock()
				c.unverified = true
				c.mu.Unlock()
			}
			localRelease()
		})
	}
	c.mu.Lock()
	allowed := !c.unverified && (c.mode == Active || (reconciliation && c.mode == ReconcileOnly))
	c.mu.Unlock()
	if !allowed {
		release()
		return nil, ErrPaused
	}
	return release, nil
}

// Pause first latches PAUSE_NEW, then waits for local boundaries already entered.
// Timeout/cancellation after latching leaves the pause in place; it does not
// assert quiescence. Already sent Provider work and result/lease writes continue.
// Never call synchronously from inside a held boundary: use a separate caller.
func (c *Control) Pause(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil {
		return ErrPaused
	}
	c.mu.Lock()
	if !c.initialized {
		c.mu.Unlock()
		return ErrPaused
	}
	c.mode = PauseNew
	c.mu.Unlock()
	// CAS failure cannot undo the local latch and cannot claim a global pause.
	if c.durable != nil {
		pauseCtx, cancelPause := context.WithTimeout(ctx, durableQueryTimeout)
		pauseErr := c.durable.store.Pause(pauseCtx, c.durable.binding, c.durable.pauseID)
		cancelPause()
		if err := pauseErr; err != nil {
			return ErrUnavailable
		}
	}
	c.mu.Lock()
	for c.inflight != 0 {
		changed := c.changed
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
		c.mu.Lock()
	}
	c.mu.Unlock()
	if c.durable != nil {
		report, err := c.DurableReport(ctx)
		if err != nil {
			return err
		}
		if report.State.Epoch != c.durable.binding.Epoch || report.State.Mode != PauseNew {
			return ErrStale
		}
		if !report.Drained() {
			return ErrHold
		}
	}
	return nil
}
