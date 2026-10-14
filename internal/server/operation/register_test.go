package operation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
	"github.com/ktcloud4-SL/labbit-app/internal/server/tracecontext"
)

type registrationStore struct {
	repository.OperationRepository
	saved   repository.Operation
	items   []repository.NewOperationItem
	itemErr error
}

func (f *registrationStore) InsertOperation(_ context.Context, o repository.NewOperation) (bool, error) {
	if f.saved.ID != uuid.Nil {
		return false, nil
	}
	f.saved = repository.Operation{ID: o.ID, OrganizationID: o.OrganizationID, Type: o.Type, Status: "PENDING", RequestedByUserID: o.RequestedByUserID, LabExecutionID: o.LabExecutionID, LabInstanceID: o.LabInstanceID,
		IdempotencyKeyHash: o.IdempotencyKeyHash, RequestFingerprint: o.RequestFingerprint, CreatedAt: o.CreatedAt, UpdatedAt: o.CreatedAt, Traceparent: o.Traceparent, Tracestate: o.Tracestate, RequestID: o.RequestID}
	return true, nil
}
func (f *registrationStore) OperationByID(context.Context, uuid.UUID) (repository.Operation, error) {
	return f.saved, nil
}
func (f *registrationStore) OperationByIdempotency(context.Context, uuid.UUID, string, []byte) (repository.Operation, error) {
	if f.saved.ID == uuid.Nil {
		return repository.Operation{}, repository.ErrNotFound
	}
	return f.saved, nil
}
func (f *registrationStore) CreateOperationItem(_ context.Context, i repository.NewOperationItem) error {
	f.items = append(f.items, i)
	return f.itemErr
}
func input() Registration {
	execution, instance := uuid.New(), uuid.New()
	return Registration{OrganizationID: uuid.New(), RequestedByUserID: uuid.New(), Type: "PROVISION", LabExecutionID: &execution, IdempotencyKey: "opaque-key-dummy", Fingerprint: SemanticFingerprint([]byte("canonical-target-and-payload")), Items: []Item{{LabInstanceID: instance, Generation: 1}}}
}
func TestKeyAndSemanticFingerprint(t *testing.T) {
	h, err := HashKey("opaque-key-dummy")
	want := sha256.Sum256([]byte("opaque-key-dummy"))
	if err != nil || !bytes.Equal(h, want[:]) || bytes.Equal(h, []byte("opaque-key-dummy")) {
		t.Fatal("unsafe or incorrect key hash")
	}
	other, _ := HashKey(" opaque-key-dummy")
	if bytes.Equal(h, other) {
		t.Fatal("opaque key was normalized")
	}
	for _, key := range []string{"", strings.Repeat("k", 256)} {
		if _, err := HashKey(key); !errors.Is(err, ErrInvalid) {
			t.Fatalf("key length: %v", err)
		}
	}
	if _, err := HashKey(strings.Repeat("k", 255)); err != nil {
		t.Fatal(err)
	}
	if SemanticFingerprint([]byte("target A")) == SemanticFingerprint([]byte("target B")) {
		t.Fatal("different semantics collide")
	}
}
func TestRegisterReplayConflictAndTrace(t *testing.T) {
	for _, tc := range []tracecontext.Context{{Traceparent: "invalid", Tracestate: "bad"}, tracecontext.Normalize("00-0123456789abcdef0123456789abcdef-0123456789abcdef-01", "vendor=opaque")} {
		f := &registrationStore{}
		in := input()
		at := time.Now()
		ctx := tracecontext.NewContext(t.Context(), tc)
		first, err := Register(ctx, f, in, at)
		if err != nil || first.Replayed {
			t.Fatalf("first: %+v %v", first, err)
		}
		valid := tracecontext.Normalize(tc.Traceparent, tc.Tracestate)
		if first.Operation.Traceparent != valid.Traceparent || first.Operation.Tracestate != valid.Tracestate {
			t.Fatal("trace normalization failed")
		}
		replay, err := Register(t.Context(), f, in, at.Add(time.Hour))
		if err != nil || !replay.Replayed || replay.Operation.ID != first.Operation.ID || len(f.items) != 1 || replay.Operation.Traceparent != first.Operation.Traceparent || !replay.Operation.UpdatedAt.Equal(at) {
			t.Fatalf("replay mutated registration: %+v %v", replay, err)
		}
		in.Fingerprint = SemanticFingerprint([]byte("different target"))
		if _, err := Register(t.Context(), f, in, at); !errors.Is(err, ErrConflict) {
			t.Fatalf("conflict: %v", err)
		}
	}
}
func TestRegistrationRejectsInvalidAndPropagatesItemFailure(t *testing.T) {
	for _, change := range []func(*Registration){func(i *Registration) { i.Items = nil }, func(i *Registration) { i.Items = append(i.Items, i.Items[0]) }, func(i *Registration) { i.Items[0].Generation = 0 }, func(i *Registration) { i.LabExecutionID = nil }, func(i *Registration) { i.Type = "UNKNOWN" }} {
		f := &registrationStore{}
		in := input()
		change(&in)
		if _, err := Register(t.Context(), f, in, time.Now()); !errors.Is(err, ErrInvalid) || f.saved.ID != uuid.Nil {
			t.Fatalf("invalid registered: %v", err)
		}
	}
	f := &registrationStore{itemErr: repository.ErrConflict}
	if _, err := Register(t.Context(), f, input(), time.Now()); !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("item failure: %v", err)
	}
}

func TestHashKeyUnicodeContract(t *testing.T) {
	tests := []struct {
		name, key string
		valid     bool
	}{
		{"empty", "", false},
		{"single Korean code point", "한", true},
		{"review regression 200 Korean code points", strings.Repeat("한", 200), true},
		{"255 Korean code points", strings.Repeat("한", 255), true},
		{"256 Korean code points", strings.Repeat("한", 256), false},
		{"255 supplementary code points", strings.Repeat("😀", 255), true},
		{"256 supplementary code points", strings.Repeat("😀", 256), false},
		{"combining sequence at 255 code points", strings.Repeat("e\u0301", 127) + "x", true},
		{"128 combining graphemes are 256 code points", strings.Repeat("e\u0301", 128), false},
		{"valid replacement code point", strings.Repeat("\ufffd", 255), true},
		{"invalid leading byte", string([]byte{0xff}), false},
		{"invalid continuation byte", string([]byte{0x80}), false},
		{"truncated sequence", string([]byte{0xe2, 0x82}), false},
		{"overlong encoding", string([]byte{0xc0, 0xaf}), false},
		{"surrogate", string([]byte{0xed, 0xa0, 0x80}), false},
		{"above Unicode maximum", string([]byte{0xf4, 0x90, 0x80, 0x80}), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash, err := HashKey(tt.key)
			if !tt.valid {
				if !errors.Is(err, ErrInvalid) || hash != nil {
					t.Fatalf("invalid key returned hash/error %x/%v", hash, err)
				}
				return
			}
			want := sha256.Sum256([]byte(tt.key))
			if err != nil || !bytes.Equal(hash, want[:]) {
				t.Fatalf("valid key hash/error %x/%v", hash, err)
			}
		})
	}
	composed, _ := HashKey("é")
	decomposed, _ := HashKey("e\u0301")
	if bytes.Equal(composed, decomposed) {
		t.Fatal("opaque key was Unicode normalized")
	}
}
