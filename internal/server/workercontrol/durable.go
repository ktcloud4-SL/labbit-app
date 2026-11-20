package workercontrol

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidBinding = errors.New("workercontrol: invalid binding")
	ErrUnavailable    = errors.New("workercontrol: state not verified")
	ErrStale          = errors.New("workercontrol: stale binding")
	ErrHold           = errors.New("workercontrol: unresolved boundaries")
)

const durableQueryTimeout = 2 * time.Second

var labelPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$`)

// Scope is an explicitly configured target, never inferred from a request.
type Scope struct{ ID, Region, Cluster string }

func (s Scope) Validate() error {
	if !labelPattern.MatchString(s.ID) || !labelPattern.MatchString(s.Region) || !labelPattern.MatchString(s.Cluster) {
		return ErrInvalidBinding
	}
	return nil
}

// Binding is immutable for a process incarnation. A new revision/epoch needs
// a new approved deployment; reading a newer value never grants permission.
type Binding struct {
	Scope           Scope
	Revision, Epoch int64
	Mode            Mode
	PodID           string
	BootID          uuid.UUID
}

func (b Binding) Validate() error {
	if b.Scope.Validate() != nil || b.Revision < 1 || b.Epoch < 1 || b.Mode.Validate() != nil || !labelPattern.MatchString(b.PodID) || b.BootID == uuid.Nil {
		return ErrInvalidBinding
	}
	return nil
}

type BoundaryKind string

const (
	MutationBoundary       BoundaryKind = "MUTATION"
	ReconciliationBoundary BoundaryKind = "RECONCILIATION"
)

func (k BoundaryKind) Validate() error {
	if k != MutationBoundary && k != ReconciliationBoundary {
		return ErrInvalidBinding
	}
	return nil
}

type State struct {
	Scope           Scope
	Revision, Epoch int64
	Mode            Mode
}

func (s State) Matches(b Binding) bool {
	return s.Scope == b.Scope && s.Revision == b.Revision && s.Epoch == b.Epoch && s.Mode == b.Mode
}

type Phase string

const (
	MutationClaim          Phase = "MUTATION_CLAIM"
	MutationPrepare        Phase = "MUTATION_PREPARE"
	MutationDispatch       Phase = "MUTATION_DISPATCH"
	MutationRequeue        Phase = "MUTATION_REQUEUE"
	ReconciliationClaim    Phase = "RECONCILIATION_CLAIM"
	ReconciliationPrepare  Phase = "RECONCILIATION_PREPARE"
	ReconciliationDispatch Phase = "RECONCILIATION_DISPATCH"
)

func (p Phase) Matches(k BoundaryKind) bool {
	if k == MutationBoundary {
		return p == MutationClaim || p == MutationPrepare || p == MutationDispatch || p == MutationRequeue
	}
	return k == ReconciliationBoundary && (p == ReconciliationClaim || p == ReconciliationPrepare || p == ReconciliationDispatch)
}

type Ticket struct {
	Binding Binding
	ID      uuid.UUID
	Kind    BoundaryKind
	Phase   Phase
}

func (t Ticket) Validate() error {
	if t.Binding.Validate() != nil || t.ID == uuid.Nil || t.Kind.Validate() != nil || !t.Phase.Matches(t.Kind) {
		return ErrInvalidBinding
	}
	return nil
}

// Report is an authoritative observation, not approval or all-Pod coverage.
// Open intents never expire automatically, even when their Pod has disappeared.
type Report struct {
	State          State
	OpenBoundaries int64
	PendingResumes int64
	ObservedAt     time.Time
}

func (r Report) Drained() bool {
	return r.State.Mode == PauseNew && r.OpenBoundaries == 0 && r.PendingResumes == 0 && !r.ObservedAt.IsZero()
}

// DurableStore commits Enter before returning. Pause and Enter serialize on the
// same scope row. No transaction stays open during claim/Connector I/O. Leave
// closes only this exact ticket and must be idempotent; ambiguous writes retain
// an unresolved intent. No TTL/retry may erase it or replay external work.
type DurableStore interface {
	Enter(context.Context, Ticket) error
	Leave(context.Context, Ticket) error
	Pause(context.Context, Binding, uuid.UUID) error
	Report(context.Context, Scope) (Report, error)
}

type durableControl struct {
	store   DurableStore
	binding Binding
	pauseID uuid.UUID
}

// Persistent reports assembly capability only, never proof of an approved
// binding or Writer. Every boundary must still call the authoritative Store.
func (c *Control) Persistent() bool { return c != nil && c.durable != nil }

func NewDurable(binding Binding, store DurableStore) (*Control, error) {
	if binding.Validate() != nil || store == nil {
		return nil, ErrInvalidBinding
	}
	c, _ := New(binding.Mode)
	c.durable = &durableControl{store: store, binding: binding, pauseID: uuid.New()}
	return c, nil
}

// GuaranteedNoDispatch accepts only bare gate errors returned before entering
// the Router write boundary. A wrapped/joined send failure is still UNKNOWN.
func GuaranteedNoDispatch(err error) bool {
	return err == ErrPaused || err == ErrStale || err == ErrUnavailable || err == ErrHold
}

// DurableReport never uses a cached PASS, local Snapshot, or Pod liveness.
func (c *Control) DurableReport(ctx context.Context) (Report, error) {
	if c == nil || c.durable == nil {
		return Report{}, ErrUnavailable
	}
	queryCtx, cancel := context.WithTimeout(ctx, durableQueryTimeout)
	defer cancel()
	r, err := c.durable.store.Report(queryCtx, c.durable.binding.Scope)
	if err != nil || r.State.Scope != c.durable.binding.Scope || r.State.Revision < 1 || r.State.Epoch < 1 || r.State.Mode.Validate() != nil || r.OpenBoundaries < 0 || r.PendingResumes < 0 || r.ObservedAt.IsZero() {
		return Report{}, ErrUnavailable
	}
	if r.State.Revision < c.durable.binding.Revision || r.State.Epoch != c.durable.binding.Epoch {
		return Report{}, ErrStale
	}
	return r, nil
}
