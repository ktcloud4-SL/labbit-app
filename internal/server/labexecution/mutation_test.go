package labexecution

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// mutationWorld는 Provision이 이미 끝난(active item 없음) LabExecution 하나를 가진다. 강사, studentA, studentB의 LabInstance이며
// generation은 서로 다르다(Reset을 거친 학생 포함).
type mutationWorld struct {
	*world
	execution              uuid.UUID
	instructorLI, aLI, bLI uuid.UUID
	generations            map[uuid.UUID]int64
	foreignOrgUser         repository.User
}

func newMutationWorld(t *testing.T) *mutationWorld {
	t.Helper()
	w := &mutationWorld{world: newWorld(t), execution: uuid.New(), instructorLI: uuid.New(), aLI: uuid.New(), bLI: uuid.New()}
	w.generations = map[uuid.UUID]int64{w.instructorLI: 1, w.aLI: 3, w.bLI: 1}
	w.foreignOrgUser = repository.User{ID: uuid.New(), OrganizationID: uuid.New(), OrganizationRole: repository.OrganizationRoleMember}
	summary := func(id uuid.UUID, user repository.User, role repository.ClassRole) repository.LabInstanceSummary {
		return repository.LabInstanceSummary{ID: id, OrganizationID: w.org, LabExecutionID: w.execution, UserID: user.ID, ParticipantRole: role,
			Status: "READY", Generation: w.generations[id]}
	}
	instances := []repository.LabInstanceSummary{
		summary(w.instructorLI, w.instructor, repository.ClassRoleInstructor),
		summary(w.aLI, w.studentA, repository.ClassRoleStudent),
		summary(w.bLI, w.studentB, repository.ClassRoleStudent),
	}
	e := repository.LabExecution{ID: w.execution, OrganizationID: w.org, ClassID: w.class.ID, LabSpecID: w.spec.ID, InstructorUserID: w.instructor.ID,
		Status: "READY", StartedAt: time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC), LabInstances: instances}
	w.store.readExecutions[w.execution] = e
	w.store.committed.locked[w.execution] = repository.LockedLabExecution{LabExecution: e, HasCreationSnapshot: true}
	for _, i := range instances {
		w.store.readInstances[i.ID] = repository.LabInstance{ID: i.ID, OrganizationID: w.org, LabExecutionID: w.execution, ClassID: w.class.ID,
			UserID: i.UserID, Status: i.Status, Generation: i.Generation}
	}
	return w
}

func (w *mutationWorld) reset(user repository.User, instance uuid.UUID, key string) (MutationAccepted, error) {
	return w.svc.Reset(context.Background(), user, ResetInput{LabInstanceID: instance.String(), IdempotencyKey: key, RequestID: "request-" + key})
}

func (w *mutationWorld) cleanup(user repository.User, key string) (MutationAccepted, error) {
	return w.svc.Cleanup(context.Background(), user, CleanupInput{LabExecutionID: w.execution.String(), IdempotencyKey: key, RequestID: "request-" + key})
}

// setLocked는 commit된 LabExecution 상태를 바꾼다(Worker가 나중에 바꾼 것처럼).
func (w *mutationWorld) setLocked(change func(*repository.LockedLabExecution)) {
	e := w.store.committed.locked[w.execution]
	e.LabInstances = append([]repository.LabInstanceSummary(nil), e.LabInstances...)
	change(&e)
	w.store.committed.locked[w.execution] = e
}

// addActiveItem은 다른 변경 작업(예: 아직 끝나지 않은 PROVISION)의 item을 commit된 상태로 넣는다.
func (w *mutationWorld) addActiveItem(instance uuid.UUID) {
	s := w.store.committed
	op := repository.Operation{ID: uuid.New(), OrganizationID: w.org, RequestedByUserID: w.instructor.ID, Type: "PROVISION", Status: "RECONCILING",
		LabExecutionID: &w.execution, IdempotencyKeyHash: []byte("other"), RequestFingerprint: []byte("other")}
	s.operations = append(s.operations, op)
	s.items = append(s.items, repository.NewOperationItem{ID: uuid.New(), OrganizationID: w.org, OperationID: op.ID, LabInstanceID: instance, Generation: 1})
}

func TestResetRegistersNextGenerationItemForExistingStudentInstance(t *testing.T) {
	w := newMutationWorld(t)
	got, err := w.reset(w.instructor, w.aLI, "reset-1")
	if err != nil || got.Replayed || got.TargetID != w.aLI || got.OperationID == uuid.Nil {
		t.Fatalf("Reset() = %+v, %v", got, err)
	}
	s := w.store.committed
	// 새 LabExecution/Snapshot/LabInstance를 만들지 않고 Operation 1개와 대상 LabInstance item 1개만 등록한다.
	if s.rowCounts() != [5]int{0, 0, 0, 1, 1} {
		t.Fatalf("rows=%v", s.rowCounts())
	}
	o := s.operations[0]
	if o.ID != got.OperationID || o.Type != "RESET" || o.Status != "PENDING" || o.LabInstanceID == nil || *o.LabInstanceID != w.aLI ||
		o.LabExecutionID != nil || o.RequestedByUserID != w.instructor.ID || o.RequestID != "request-reset-1" {
		t.Fatalf("operation=%+v", o)
	}
	if item := s.items[0]; item.OperationID != o.ID || item.LabInstanceID != w.aLI || item.Generation != 4 {
		t.Fatalf("item=%+v, want generation current(3)+1", item)
	}
	// 접수는 LabInstance의 현재 generation을 바꾸지 않는다.
	for _, li := range s.locked[w.execution].LabInstances {
		if li.Generation != w.generations[li.ID] {
			t.Fatalf("instance %s generation=%d", li.ID, li.Generation)
		}
	}
	// 공동 강사(같은 Class의 다른 INSTRUCTOR)도 Reset할 수 있다.
	if got, err := w.reset(w.coInstructor, w.bLI, "reset-co"); err != nil || got.Replayed {
		t.Fatalf("co-instructor Reset() = %+v, %v", got, err)
	}
}

func TestResetReplaysSameKeyRegardlessOfLaterStateAndRejectsOtherMeaning(t *testing.T) {
	w := newMutationWorld(t)
	first, err := w.reset(w.instructor, w.aLI, "reset-1")
	if err != nil {
		t.Fatal(err)
	}
	// 같은 key라도 다른 사용자는 별개의 Idempotency 범위다.
	if _, err := w.reset(w.coInstructor, w.bLI, "reset-1"); err != nil {
		t.Fatalf("other user same key err=%v", err)
	}
	// Worker가 Reset을 끝내 generation이 바뀌고 item이 끝났으며 LabExecution이 종료돼도 같은 key는 기존 Operation이다.
	w.store.committed.settled[w.store.committed.items[0].ID] = true
	w.setLocked(func(e *repository.LockedLabExecution) {
		e.LabInstances[1].Generation = 4
		finished := time.Now()
		e.FinishedAt = &finished
	})
	again, err := w.reset(w.instructor, w.aLI, "reset-1")
	if err != nil || !again.Replayed || again.OperationID != first.OperationID || again.TargetID != w.aLI {
		t.Fatalf("replay=%+v, %v", again, err)
	}
	if _, err := w.reset(w.instructor, w.bLI, "reset-1"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("same key other instance err=%v", err)
	}
	if s := w.store.committed; s.rowCounts() != [5]int{0, 0, 0, 2, 2} {
		t.Fatalf("rows=%v", s.rowCounts())
	}
}

func TestResetAuthorizationAndTargetRules(t *testing.T) {
	for _, tt := range []struct {
		name   string
		setup  func(*mutationWorld)
		user   func(*mutationWorld) repository.User
		target func(*mutationWorld) string
		key    string
		want   error
	}{
		{"invalid key", nil, func(w *mutationWorld) repository.User { return w.instructor }, func(w *mutationWorld) string { return w.aLI.String() }, "", ErrInvalidRequest},
		{"malformed id", nil, func(w *mutationWorld) repository.User { return w.instructor }, func(*mutationWorld) string { return "not-an-id" }, "k", ErrNotFound},
		{"missing instance", nil, func(w *mutationWorld) repository.User { return w.instructor }, func(*mutationWorld) string { return uuid.NewString() }, "k", ErrNotFound},
		{"student self reset", nil, func(w *mutationWorld) repository.User { return w.studentA }, func(w *mutationWorld) string { return w.aLI.String() }, "k", ErrForbidden},
		{"ADMIN without membership", nil, func(w *mutationWorld) repository.User { return w.admin }, func(w *mutationWorld) string { return w.aLI.String() }, "k", ErrForbidden},
		{"other class member", nil, func(w *mutationWorld) repository.User { return w.outsider }, func(w *mutationWorld) string { return w.aLI.String() }, "k", ErrForbidden},
		{"other organization", nil, func(w *mutationWorld) repository.User { return w.foreignOrgUser }, func(w *mutationWorld) string { return w.aLI.String() }, "k", ErrForbidden},
		{"instructor instance", nil, func(w *mutationWorld) repository.User { return w.instructor }, func(w *mutationWorld) string { return w.instructorLI.String() }, "k", ErrInvalidResetTarget},
		{"finished execution", func(w *mutationWorld) {
			w.setLocked(func(e *repository.LockedLabExecution) { f := time.Now(); e.FinishedAt = &f })
		}, func(w *mutationWorld) repository.User { return w.instructor }, func(w *mutationWorld) string { return w.aLI.String() }, "k", ErrExecutionFinished},
		{"missing snapshot", func(w *mutationWorld) {
			w.setLocked(func(e *repository.LockedLabExecution) { e.HasCreationSnapshot = false })
		}, func(w *mutationWorld) repository.User { return w.instructor }, func(w *mutationWorld) string { return w.aLI.String() }, "k", ErrInconsistentData},
		{"active provision item", func(w *mutationWorld) { w.addActiveItem(w.aLI) },
			func(w *mutationWorld) repository.User { return w.instructor }, func(w *mutationWorld) string { return w.aLI.String() }, "k", ErrMutationInProgress},
		// 권한 사전 확인 뒤 transaction 전에 강사 Membership이 삭제되었다. transaction 안의 재확인이 거절한다.
		{"membership revoked before transaction", func(w *mutationWorld) {
			w.store.beforeTransaction = func(s *state) { delete(s.memberships, [2]uuid.UUID{w.class.ID, w.instructor.ID}) }
		}, func(w *mutationWorld) repository.User { return w.instructor }, func(w *mutationWorld) string { return w.aLI.String() }, "k", ErrForbidden},
		{"demoted before transaction", func(w *mutationWorld) {
			w.store.beforeTransaction = func(s *state) {
				m := s.memberships[[2]uuid.UUID{w.class.ID, w.instructor.ID}]
				m.Role = repository.ClassRoleStudent
				s.memberships[[2]uuid.UUID{w.class.ID, w.instructor.ID}] = m
			}
		}, func(w *mutationWorld) repository.User { return w.instructor }, func(w *mutationWorld) string { return w.aLI.String() }, "k", ErrForbidden},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := newMutationWorld(t)
			if tt.setup != nil {
				tt.setup(w)
			}
			before := len(w.store.committed.operations)
			_, err := w.svc.Reset(context.Background(), tt.user(w), ResetInput{LabInstanceID: tt.target(w), IdempotencyKey: tt.key})
			if !errors.Is(err, tt.want) {
				t.Fatalf("err=%v want %v", err, tt.want)
			}
			if len(w.store.committed.operations) != before {
				t.Fatal("rejected Reset left an Operation")
			}
		})
	}
}

func TestCleanupRegistersEveryStoredInstanceAtCurrentGeneration(t *testing.T) {
	w := newMutationWorld(t)
	// 접수 뒤 Class Membership이 바뀌어도(studentB 탈퇴, studentC 대신 coInstructor만 남음) Cleanup 대상은 저장된 LabInstance 전부다.
	delete(w.store.committed.memberships, [2]uuid.UUID{w.class.ID, w.studentB.ID})
	got, err := w.cleanup(w.coInstructor, "cleanup-1")
	if err != nil || got.Replayed || got.TargetID != w.execution {
		t.Fatalf("Cleanup() = %+v, %v", got, err)
	}
	s := w.store.committed
	if s.rowCounts() != [5]int{0, 0, 0, 1, 3} {
		t.Fatalf("rows=%v", s.rowCounts())
	}
	o := s.operations[0]
	if o.Type != "CLEANUP" || o.Status != "PENDING" || o.LabExecutionID == nil || *o.LabExecutionID != w.execution || o.LabInstanceID != nil {
		t.Fatalf("operation=%+v", o)
	}
	seen := map[uuid.UUID]int64{}
	for _, item := range s.items {
		if item.OperationID != o.ID {
			t.Fatalf("item=%+v", item)
		}
		seen[item.LabInstanceID] = item.Generation
	}
	for id, generation := range w.generations {
		if seen[id] != generation {
			t.Fatalf("items=%v want current generations %v", seen, w.generations)
		}
	}
	// 접수는 LabExecution을 종료하거나 상태를 바꾸지 않는다.
	if e := s.locked[w.execution]; e.FinishedAt != nil || e.Status != "READY" {
		t.Fatalf("execution=%+v", e)
	}

	again, err := w.cleanup(w.coInstructor, "cleanup-1")
	if err != nil || !again.Replayed || again.OperationID != got.OperationID {
		t.Fatalf("replay=%+v, %v", again, err)
	}
	if _, err := w.cleanup(w.instructor, "cleanup-2"); !errors.Is(err, ErrMutationInProgress) {
		t.Fatalf("second cleanup err=%v", err)
	}
	if _, err := w.reset(w.instructor, w.aLI, "reset-after-cleanup"); !errors.Is(err, ErrMutationInProgress) {
		t.Fatalf("reset during cleanup err=%v", err)
	}
	if s := w.store.committed; s.rowCounts() != [5]int{0, 0, 0, 1, 3} {
		t.Fatalf("rows=%v", s.rowCounts())
	}
}

func TestCleanupIsAllOrNothing(t *testing.T) {
	t.Run("active item on one instance", func(t *testing.T) {
		w := newMutationWorld(t)
		w.addActiveItem(w.bLI)
		before := w.store.committed.rowCounts()
		if _, err := w.cleanup(w.instructor, "cleanup-1"); !errors.Is(err, ErrMutationInProgress) {
			t.Fatalf("err=%v", err)
		}
		if w.store.committed.rowCounts() != before {
			t.Fatalf("rows=%v before=%v", w.store.committed.rowCounts(), before)
		}
	})
	t.Run("constraint violation is not a conflict", func(t *testing.T) {
		w := newMutationWorld(t)
		// id 순서로 마지막에 등록되는 LabInstance의 INSERT가 실패한다.
		last := sortedUUIDs([]uuid.UUID{w.instructorLI, w.aLI, w.bLI})[2]
		w.store.failItem = map[uuid.UUID]error{last: &repository.Error{Kind: repository.KindConstraintViolation, Op: "CreateOperationItem"}}
		// 실제 Repository처럼 id 순서로 반환한다.
		w.setLocked(func(e *repository.LockedLabExecution) {
			byID := map[uuid.UUID]repository.LabInstanceSummary{}
			for _, i := range e.LabInstances {
				byID[i.ID] = i
			}
			for n, id := range sortedUUIDs([]uuid.UUID{w.instructorLI, w.aLI, w.bLI}) {
				e.LabInstances[n] = byID[id]
			}
		})
		_, err := w.cleanup(w.instructor, "cleanup-1")
		if !errors.Is(err, repository.ErrConstraintViolation) || errors.Is(err, ErrMutationInProgress) {
			t.Fatalf("err=%v", err)
		}
		if c := w.store.committed.rowCounts(); c != [5]int{} {
			t.Fatalf("rows=%v", c)
		}
	})
}

func TestCleanupAuthorizationAndStateRules(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(*mutationWorld)
		user  func(*mutationWorld) repository.User
		id    func(*mutationWorld) string
		want  error
	}{
		{"malformed id", nil, func(w *mutationWorld) repository.User { return w.instructor }, func(*mutationWorld) string { return "x" }, ErrNotFound},
		{"missing execution", nil, func(w *mutationWorld) repository.User { return w.instructor }, func(*mutationWorld) string { return uuid.NewString() }, ErrNotFound},
		{"student", nil, func(w *mutationWorld) repository.User { return w.studentA }, func(w *mutationWorld) string { return w.execution.String() }, ErrForbidden},
		{"ADMIN without membership", nil, func(w *mutationWorld) repository.User { return w.admin }, func(w *mutationWorld) string { return w.execution.String() }, ErrForbidden},
		{"other organization", nil, func(w *mutationWorld) repository.User { return w.foreignOrgUser }, func(w *mutationWorld) string { return w.execution.String() }, ErrForbidden},
		{"finished", func(w *mutationWorld) {
			w.setLocked(func(e *repository.LockedLabExecution) { f := time.Now(); e.FinishedAt = &f })
		}, func(w *mutationWorld) repository.User { return w.instructor }, func(w *mutationWorld) string { return w.execution.String() }, ErrExecutionFinished},
		{"no instructor instance", func(w *mutationWorld) {
			w.setLocked(func(e *repository.LockedLabExecution) { e.LabInstances = e.LabInstances[1:] })
		}, func(w *mutationWorld) repository.User { return w.instructor }, func(w *mutationWorld) string { return w.execution.String() }, ErrInconsistentData},
		{"membership revoked before transaction", func(w *mutationWorld) {
			w.store.beforeTransaction = func(s *state) { delete(s.memberships, [2]uuid.UUID{w.class.ID, w.instructor.ID}) }
		}, func(w *mutationWorld) repository.User { return w.instructor }, func(w *mutationWorld) string { return w.execution.String() }, ErrForbidden},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := newMutationWorld(t)
			if tt.setup != nil {
				tt.setup(w)
			}
			_, err := w.svc.Cleanup(context.Background(), tt.user(w), CleanupInput{LabExecutionID: tt.id(w), IdempotencyKey: "k"})
			if !errors.Is(err, tt.want) {
				t.Fatalf("err=%v want %v", err, tt.want)
			}
			if len(w.store.committed.operations) != 0 {
				t.Fatal("rejected Cleanup left an Operation")
			}
		})
	}
}

// fingerprint는 요청 종류와 대상만 담는다. 현재 generation·상태, key, request ID를 담지 않는다.
func TestMutationFingerprintIsKindAndTargetOnly(t *testing.T) {
	target := uuid.New()
	if mutationFingerprint("reset/v1", target) != mutationFingerprint("reset/v1", target) {
		t.Fatal("fingerprint is not deterministic")
	}
	if mutationFingerprint("reset/v1", target) == mutationFingerprint("cleanup/v1", target) ||
		mutationFingerprint("reset/v1", target) == mutationFingerprint("reset/v1", uuid.New()) {
		t.Fatal("fingerprint ignores kind or target")
	}
	w := newMutationWorld(t)
	if _, err := w.reset(w.instructor, w.aLI, "fp"); err != nil {
		t.Fatal(err)
	}
	stored := w.store.committed.operations[0].RequestFingerprint
	want := mutationFingerprint("reset/v1", w.aLI)
	if !bytes.Equal(stored, want[:]) {
		t.Fatal("stored fingerprint differs from canonical reset fingerprint")
	}
}
