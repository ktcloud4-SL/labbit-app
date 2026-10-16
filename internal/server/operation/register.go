// Package operation provides durable registration, authorized status reads and
// the claim/lease execution boundary. Provider dispatch is supplied by its caller.
package operation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
	"github.com/ktcloud4-SL/labbit-app/internal/server/tracecontext"
)

var (
	ErrConflict         = errors.New("operation: idempotency conflict")
	ErrInvalid          = errors.New("operation: invalid registration")
	ErrNotFound         = errors.New("operation: not found")
	ErrForbidden        = errors.New("operation: forbidden")
	ErrInconsistentData = errors.New("operation: inconsistent data")
)

// Fingerprint is SHA-256 of caller-defined canonical semantic bytes. The caller must
// include endpoint/target and every business-affecting payload value, normalize equivalent
// inputs (e.g. unordered student sets), and exclude key, trace, request ID, timestamps and
// newly allocated resource IDs. No Provision payload interpretation lives here.
type Fingerprint [sha256.Size]byte

func SemanticFingerprint(canonical []byte) Fingerprint { return sha256.Sum256(canonical) }

// HashKey hashes the exact opaque key, without trimming or storing its raw value.
func HashKey(key string) ([]byte, error) {
	if !utf8.ValidString(key) {
		return nil, ErrInvalid
	}
	length := utf8.RuneCountInString(key)
	if length < 1 || length > 255 {
		return nil, ErrInvalid
	}
	h := sha256.Sum256([]byte(key))
	return h[:], nil
}

type Item struct {
	LabInstanceID uuid.UUID
	Generation    int64
}
type Registration struct {
	OrganizationID, RequestedByUserID uuid.UUID
	Type, IdempotencyKey              string
	Fingerprint                       Fingerprint
	LabExecutionID, LabInstanceID     *uuid.UUID
	RequestID                         string
	Items                             []Item
}
type Result struct {
	Operation repository.Operation
	Replayed  bool
}

// Replay allows callers to detect committed replays before allocating business resources.
// It is only a precheck: Register must still arbitrate concurrent requests using the DB unique key.
// The caller must authorize the request before either call and roll back all newly allocated
// business rows if Register returns Replayed. Callback errors must not be swallowed.
func Replay(ctx context.Context, repos repository.OperationRepository, user uuid.UUID, kind, key string, fp Fingerprint) (Result, error) {
	hash, err := HashKey(key)
	if err != nil {
		return Result{}, err
	}
	o, err := repos.OperationByIdempotency(ctx, user, kind, hash)
	if err != nil {
		return Result{}, err
	}
	if !bytes.Equal(o.RequestFingerprint, fp[:]) {
		return Result{}, ErrConflict
	}
	return Result{Operation: o, Replayed: true}, nil
}

// Register must run on Repositories supplied by the caller's existing Transactor.
// A replay inserts no items and preserves the original request/trace/timestamps. Any error
// must roll back the enclosing business transaction. createdAt is server registration time.
func Register(ctx context.Context, repos repository.OperationRepository, in Registration, createdAt time.Time) (Result, error) {
	hash, err := HashKey(in.IdempotencyKey)
	if err != nil {
		return Result{}, err
	}
	targetValid := func(p *uuid.UUID) bool { return p != nil && *p != uuid.Nil }
	if in.OrganizationID == uuid.Nil || in.RequestedByUserID == uuid.Nil || createdAt.IsZero() || in.Fingerprint == (Fingerprint{}) || len(in.Items) == 0 {
		return Result{}, ErrInvalid
	}
	switch in.Type {
	case "PROVISION", "CLEANUP":
		if !targetValid(in.LabExecutionID) || in.LabInstanceID != nil {
			return Result{}, ErrInvalid
		}
	case "RESET":
		if !targetValid(in.LabInstanceID) || in.LabExecutionID != nil {
			return Result{}, ErrInvalid
		}
	default:
		return Result{}, ErrInvalid
	}
	seen := map[uuid.UUID]bool{}
	for _, i := range in.Items {
		if i.LabInstanceID == uuid.Nil || i.Generation < 1 || seen[i.LabInstanceID] {
			return Result{}, ErrInvalid
		}
		if in.Type == "RESET" && (i.LabInstanceID != *in.LabInstanceID || len(in.Items) != 1) {
			return Result{}, ErrInvalid
		}
		seen[i.LabInstanceID] = true
	}
	tc := tracecontext.FromContext(ctx)
	tc = tracecontext.Normalize(tc.Traceparent, tc.Tracestate)
	o := repository.NewOperation{ID: uuid.New(), OrganizationID: in.OrganizationID, RequestedByUserID: in.RequestedByUserID,
		Type: in.Type, LabExecutionID: in.LabExecutionID, LabInstanceID: in.LabInstanceID, RequestID: in.RequestID,
		IdempotencyKeyHash: hash, RequestFingerprint: in.Fingerprint[:], CreatedAt: createdAt, Traceparent: tc.Traceparent, Tracestate: tc.Tracestate}
	inserted, err := repos.InsertOperation(ctx, o)
	if err != nil {
		return Result{}, err
	}
	if !inserted {
		result, err := Replay(ctx, repos, in.RequestedByUserID, in.Type, in.IdempotencyKey, in.Fingerprint)
		if err == nil && result.Operation.OrganizationID != in.OrganizationID {
			return Result{}, ErrConflict
		}
		return result, err
	}
	for _, i := range in.Items {
		err = repos.CreateOperationItem(ctx, repository.NewOperationItem{ID: uuid.New(), OrganizationID: in.OrganizationID, OperationID: o.ID, LabInstanceID: i.LabInstanceID, Generation: i.Generation, CreatedAt: createdAt})
		if err != nil {
			return Result{}, err
		}
	}
	persisted, err := repos.OperationByID(ctx, o.ID)
	return Result{Operation: persisted}, err
}
