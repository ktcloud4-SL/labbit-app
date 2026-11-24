package labexecution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/server/maintenance"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
	"os"
	"path/filepath"
)

type admissionCase struct {
	name string
	w    *mutationWorld
	call func(*Service, repository.User, string) (uuid.UUID, bool, error)
}

// These call the actual application entrypoints, without HTTP, a DB or Provider.
func newAdmissionCase(t *testing.T, name string) admissionCase {
	t.Helper()
	w := newMutationWorld(t)
	c := admissionCase{name: name, w: w}
	switch name {
	case "PROVISION":
		c.call = func(s *Service, u repository.User, key string) (uuid.UUID, bool, error) {
			a, e := s.Provision(t.Context(), u, w.input(key, w.studentA))
			return a.OperationID, a.Replayed, e
		}
	case "RESET":
		c.call = func(s *Service, u repository.User, key string) (uuid.UUID, bool, error) {
			a, e := s.Reset(t.Context(), u, ResetInput{LabInstanceID: w.aLI.String(), IdempotencyKey: key})
			return a.OperationID, a.Replayed, e
		}
	case "CLEANUP":
		c.call = func(s *Service, u repository.User, key string) (uuid.UUID, bool, error) {
			a, e := s.Cleanup(t.Context(), u, CleanupInput{LabExecutionID: w.execution.String(), IdempotencyKey: key})
			return a.OperationID, a.Replayed, e
		}
	default:
		t.Fatal(name)
	}
	return c
}

func blockedService(t *testing.T, store Store) *Service {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mode")
	if err := os.WriteFile(path, []byte("closed"), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewService(store, maintenance.New(path).Enter)
	s.newID = func() uuid.UUID { t.Fatal("blocked/replayed request allocated a new ID"); return uuid.Nil }
	return s
}

func admissionSnapshot(t *testing.T, f *fakeStore) string {
	t.Helper()
	s := f.committed
	members := make([]repository.ClassMembership, 0, len(s.memberships))
	for _, m := range s.memberships {
		members = append(members, m)
	}
	sort.Slice(members, func(i, j int) bool {
		return members[i].ClassID.String()+members[i].UserID.String() < members[j].ClassID.String()+members[j].UserID.String()
	})
	b, err := json.Marshal([]any{s.classes, members, s.specs, s.images, s.flavors, s.executions, s.snapshots, s.instances, s.operations, s.items, s.locked, s.settled, f.readInstances, f.readExecutions})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestBlockedAdmissionCreatesNoRowsAndDoesNotStartTransaction(t *testing.T) {
	for _, name := range []string{"PROVISION", "RESET", "CLEANUP"} {
		t.Run(name, func(t *testing.T) {
			c := newAdmissionCase(t, name)
			before := admissionSnapshot(t, c.w.store)
			c.w.store.beforeTransaction = func(*state) { t.Fatal("blocked request started registration transaction") }
			s := blockedService(t, c.w.store)
			for i := 0; i < 2; i++ {
				id, replayed, err := c.call(s, c.w.instructor, "new-key")
				if !errors.Is(err, ErrMaintenance) || id != uuid.Nil || replayed {
					t.Fatalf("result=%v %v %v", id, replayed, err)
				}
			}
			if got := admissionSnapshot(t, c.w.store); got != before {
				t.Fatal("blocked request changed durable state")
			}
		})
	}
}

func TestBlockedAdmissionReplaysExistingUnknownWorkWithoutResubmission(t *testing.T) {
	for _, name := range []string{"PROVISION", "RESET", "CLEANUP"} {
		t.Run(name, func(t *testing.T) {
			c := newAdmissionCase(t, name)
			id, _, err := c.call(c.w.svc, c.w.instructor, "accepted-before-block")
			if err != nil {
				t.Fatal(err)
			}
			c.w.store.committed.operations[0].Status = "RECONCILING"
			before := admissionSnapshot(t, c.w.store)
			c.w.store.beforeTransaction = func(*state) { t.Fatal("replay started mutation transaction") }
			// Reconstruct the service, as on a restart with BLOCK_NEW; no in-memory history is used.
			got, replayed, err := c.call(blockedService(t, c.w.store), c.w.instructor, "accepted-before-block")
			if err != nil || !replayed || got != id {
				t.Fatalf("replay=%v %v %v", got, replayed, err)
			}
			if admissionSnapshot(t, c.w.store) != before {
				t.Fatal("replay mutated or lost existing work")
			}
		})
	}
}

func TestBlockedAdmissionPreservesConflictAndAuthorization(t *testing.T) {
	for _, name := range []string{"PROVISION", "RESET", "CLEANUP"} {
		t.Run(name, func(t *testing.T) {
			c := newAdmissionCase(t, name)
			_, _, err := c.call(c.w.svc, c.w.instructor, "existing-key")
			if err != nil {
				t.Fatal(err)
			}
			s := blockedService(t, c.w.store)
			if _, _, err := c.call(s, c.w.studentA, "new-key"); !errors.Is(err, ErrForbidden) {
				t.Fatalf("unauthorized=%v", err)
			}
			if _, _, err := c.call(s, c.w.instructor, ""); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("invalid key=%v", err)
			}
			// A different fingerprint under the same user/type/key must retain 409 semantics while blocked.
			c.w.store.committed.operations[0].RequestFingerprint = []byte("different-fingerprint")
			if _, _, err := c.call(s, c.w.instructor, "existing-key"); !errors.Is(err, ErrIdempotencyConflict) {
				t.Fatalf("conflict=%v", err)
			}
			delete(c.w.store.committed.memberships, [2]uuid.UUID{c.w.class.ID, c.w.instructor.ID})
			if _, _, err := c.call(s, c.w.instructor, "existing-key"); !errors.Is(err, ErrForbidden) {
				t.Fatalf("revoked member=%v", err)
			}
		})
	}
}

type admissionLookupFailure struct {
	Store
	cause error
}

func (s admissionLookupFailure) OperationByIdempotency(context.Context, uuid.UUID, string, []byte) (repository.Operation, error) {
	return repository.Operation{}, s.cause
}

func TestBlockedAdmissionDoesNotMaskReplayLookupFailure(t *testing.T) {
	for _, name := range []string{"PROVISION", "RESET", "CLEANUP"} {
		t.Run(name, func(t *testing.T) {
			c := newAdmissionCase(t, name)
			cause := errors.New("fake lookup unavailable")
			before := admissionSnapshot(t, c.w.store)
			s := blockedService(t, admissionLookupFailure{Store: c.w.store, cause: cause})
			if _, _, err := c.call(s, c.w.instructor, "new-key"); !errors.Is(err, cause) || errors.Is(err, ErrMaintenance) {
				t.Fatalf("lookup=%v", err)
			}
			if admissionSnapshot(t, c.w.store) != before {
				t.Fatal("lookup failure changed state")
			}
		})
	}
}

func TestBlockedAdmissionLeavesExecutionAndInstanceReadsAvailable(t *testing.T) {
	w := newMutationWorld(t)
	s := blockedService(t, w.store)
	if e, err := s.Execution(t.Context(), w.instructor, w.execution.String()); err != nil || e.ID != w.execution {
		t.Fatalf("execution=%+v %v", e, err)
	}
	if i, err := s.Instance(t.Context(), w.instructor, w.aLI.String()); err != nil || i.ID != w.aLI {
		t.Fatalf("instance=%+v %v", i, err)
	}
}

func TestBlockedAdmissionConcurrentDirectCallsNeverRegister(t *testing.T) {
	for _, name := range []string{"PROVISION", "RESET", "CLEANUP"} {
		t.Run(name, func(t *testing.T) {
			c := newAdmissionCase(t, name)
			s := blockedService(t, c.w.store)
			before := admissionSnapshot(t, c.w.store)
			var wg sync.WaitGroup
			errs := make(chan error, 48)
			for i := 0; i < 48; i++ {
				wg.Go(func() {
					id, replayed, err := c.call(s, c.w.instructor, "concurrent-key")
					if !errors.Is(err, ErrMaintenance) || id != uuid.Nil || replayed {
						errs <- fmt.Errorf("unexpected: %v %v %v", id, replayed, err)
					}
				})
			}
			wg.Wait()
			close(errs)
			for e := range errs {
				t.Error(e)
			}
			if admissionSnapshot(t, c.w.store) != before {
				t.Fatal("concurrent blocked calls changed state")
			}
		})
	}
}

func TestBlockedAdmissionPrecedesNewRegistrationSemanticChecks(t *testing.T) {
	for _, name := range []string{"provision-foreign-spec", "provision-missing-spec", "reset-instructor", "cleanup-finished"} {
		t.Run(name, func(t *testing.T) {
			c := newAdmissionCase(t, "PROVISION")
			want := ErrForbidden
			switch name {
			case "provision-foreign-spec":
				spec := c.w.spec
				spec.OwnerUserID = c.w.studentA.ID
				c.w.store.committed.specs[spec.ID] = spec
			case "provision-missing-spec":
				delete(c.w.store.committed.specs, c.w.spec.ID)
				want = ErrNotFound
			case "reset-instructor":
				c = newAdmissionCase(t, "RESET")
				want = ErrInvalidResetTarget
				c.call = func(s *Service, u repository.User, key string) (uuid.UUID, bool, error) {
					a, e := s.Reset(t.Context(), u, ResetInput{LabInstanceID: c.w.instructorLI.String(), IdempotencyKey: key})
					return a.OperationID, a.Replayed, e
				}
			case "cleanup-finished":
				c = newAdmissionCase(t, "CLEANUP")
				want = ErrExecutionFinished
				c.w.setLocked(func(e *repository.LockedLabExecution) { now := time.Now(); e.FinishedAt = &now })
			}
			before := admissionSnapshot(t, c.w.store)
			if _, _, err := c.call(c.w.svc, c.w.instructor, "new-key"); !errors.Is(err, want) {
				t.Fatalf("OPEN semantic result=%v want %v", err, want)
			}
			if _, _, err := c.call(blockedService(t, c.w.store), c.w.instructor, "new-key"); !errors.Is(err, ErrMaintenance) {
				t.Fatalf("BLOCK_NEW result=%v", err)
			}
			if admissionSnapshot(t, c.w.store) != before {
				t.Fatal("semantic rejection changed state")
			}
		})
	}
}
