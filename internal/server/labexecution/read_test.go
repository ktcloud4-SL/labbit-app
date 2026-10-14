package labexecution

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

func (w *world) storedExecution() repository.LabExecution {
	e := repository.LabExecution{ID: uuid.New(), OrganizationID: w.org, ClassID: w.class.ID, LabSpecID: w.spec.ID, InstructorUserID: w.instructor.ID, Status: "PROVISIONING"}
	e.LabInstances = []repository.LabInstanceSummary{
		{ID: uuid.New(), OrganizationID: w.org, LabExecutionID: e.ID, UserID: w.instructor.ID, ParticipantRole: repository.ClassRoleInstructor, Status: "PENDING", Generation: 1},
		{ID: uuid.New(), OrganizationID: w.org, LabExecutionID: e.ID, UserID: w.studentA.ID, ParticipantRole: repository.ClassRoleStudent, Status: "PENDING", Generation: 1},
	}
	w.store.readExecutions[e.ID] = e
	for _, i := range e.LabInstances {
		w.store.readInstances[i.ID] = repository.LabInstance{ID: i.ID, OrganizationID: w.org, LabExecutionID: e.ID, ClassID: w.class.ID, UserID: i.UserID, Status: i.Status, Generation: i.Generation}
	}
	return e
}

// 대상 학생 집합은 저장된 학생 LabInstance에서 읽는다. 지금 Class Membership을 바꿔도 응답의 targetUserIds는 그대로다.
func TestExecutionReadReturnsFixedTargetsForInstructorsOnly(t *testing.T) {
	w := newWorld(t)
	e := w.storedExecution()
	delete(w.store.committed.memberships, [2]uuid.UUID{w.class.ID, w.studentB.ID})

	for _, reader := range []repository.User{w.instructor, w.coInstructor} {
		got, err := w.svc.Execution(t.Context(), reader, e.ID.String())
		if err != nil || got.ID != e.ID || got.ClassID != w.class.ID || got.LabSpecID != w.spec.ID || got.InstructorUserID != w.instructor.ID ||
			got.Status != "PROVISIONING" || len(got.TargetUserIDs) != 1 || got.TargetUserIDs[0] != w.studentA.ID || len(got.LabInstances) != 2 {
			t.Fatalf("Execution() = %+v, %v", got, err)
		}
	}
	for name, tt := range map[string]struct {
		user repository.User
		id   string
		want error
	}{
		"student":            {w.studentA, e.ID.String(), ErrForbidden},
		"ADMIN no member":    {w.admin, e.ID.String(), ErrForbidden},
		"other organization": {repository.User{ID: uuid.New(), OrganizationID: uuid.New()}, e.ID.String(), ErrForbidden},
		"missing":            {w.instructor, uuid.NewString(), ErrNotFound},
		"malformed":          {w.instructor, "execution-1", ErrNotFound},
	} {
		if _, err := w.svc.Execution(t.Context(), tt.user, tt.id); !errors.Is(err, tt.want) {
			t.Fatalf("%s: err=%v want %v", name, err, tt.want)
		}
	}

	broken := e
	broken.ID = uuid.New()
	broken.LabInstances = broken.LabInstances[1:]
	w.store.readExecutions[broken.ID] = broken
	if _, err := w.svc.Execution(t.Context(), w.instructor, broken.ID.String()); !errors.Is(err, ErrInconsistentData) {
		t.Fatalf("missing instructor instance err=%v", err)
	}
}

func TestInstanceReadAllowsOwnerAndClassInstructor(t *testing.T) {
	w := newWorld(t)
	e := w.storedExecution()
	studentInstance := e.LabInstances[1]
	for _, reader := range []repository.User{w.studentA, w.instructor, w.coInstructor} {
		got, err := w.svc.Instance(t.Context(), reader, studentInstance.ID.String())
		if err != nil || got.ID != studentInstance.ID || got.UserID != w.studentA.ID || got.LabExecutionID != e.ID || got.Status != "PENDING" || got.Generation != 1 {
			t.Fatalf("Instance() = %+v, %v", got, err)
		}
	}
	for name, tt := range map[string]struct {
		user repository.User
		id   string
		want error
	}{
		"other student":      {w.studentB, studentInstance.ID.String(), ErrForbidden},
		"student on teacher": {w.studentA, e.LabInstances[0].ID.String(), ErrForbidden},
		"other class member": {w.outsider, studentInstance.ID.String(), ErrForbidden},
		"ADMIN no member":    {w.admin, studentInstance.ID.String(), ErrForbidden},
		"missing":            {w.instructor, uuid.NewString(), ErrNotFound},
	} {
		if _, err := w.svc.Instance(t.Context(), tt.user, tt.id); !errors.Is(err, tt.want) {
			t.Fatalf("%s: err=%v want %v", name, err, tt.want)
		}
	}
	// owner라도 현재 Class Membership이 없으면 볼 수 없다.
	delete(w.store.committed.memberships, [2]uuid.UUID{w.class.ID, w.studentA.ID})
	if _, err := w.svc.Instance(t.Context(), w.studentA, studentInstance.ID.String()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("owner without membership err=%v", err)
	}
}
