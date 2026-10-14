package class

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// Class 조회는 active LabExecution 요약과, 그 실행에 자신의 LabInstance가 있을 때만 myLabInstance를 함께 보여 준다.
// 대상 학생 집합은 LabInstance로 고정되므로 Membership이 있어도 선택되지 않은 학생에게는 myLabInstance가 없다.
func TestGetAndListProjectActiveLabExecutionAndOwnInstance(t *testing.T) {
	org := uuid.New()
	store := newFakeStore()
	instructor := newUser(org, repository.OrganizationRoleMember)
	selected := newUser(org, repository.OrganizationRoleMember)
	unselected := newUser(org, repository.OrganizationRoleMember)
	running := store.addClass(org, "A1 Running")
	idle := store.addClass(org, "A2 Idle")
	for _, c := range []repository.Class{running, idle} {
		store.addMembership(c, instructor, repository.ClassRoleInstructor)
		store.addMembership(c, selected, repository.ClassRoleStudent)
		store.addMembership(c, unselected, repository.ClassRoleStudent)
	}
	execution := repository.LabExecutionSummary{ID: uuid.New(), Status: "PROVISIONING"}
	store.executions[running.ID] = execution
	mine := repository.LabInstanceSummary{ID: uuid.New(), OrganizationID: org, LabExecutionID: execution.ID, UserID: selected.ID,
		ParticipantRole: repository.ClassRoleStudent, Status: "PENDING", Generation: 1}
	store.instances[[2]uuid.UUID{execution.ID, selected.ID}] = mine
	svc := NewService(store)

	got, err := svc.Get(t.Context(), selected, running.ID.String())
	if err != nil || got.ActiveLabExecution == nil || *got.ActiveLabExecution != (ExecutionSummary{ID: execution.ID, Status: "PROVISIONING"}) ||
		got.MyLabInstance == nil || *got.MyLabInstance != (InstanceSummary{ID: mine.ID, UserID: selected.ID, Status: "PENDING", Generation: 1}) {
		t.Fatalf("selected student Get() = %+v, %v", got, err)
	}
	got, err = svc.Get(t.Context(), unselected, running.ID.String())
	if err != nil || got.ActiveLabExecution == nil || got.MyLabInstance != nil {
		t.Fatalf("unselected student Get() = %+v, %v", got, err)
	}
	got, err = svc.Get(t.Context(), instructor, idle.ID.String())
	if err != nil || got.ActiveLabExecution != nil || got.MyLabInstance != nil {
		t.Fatalf("idle Class Get() = %+v, %v", got, err)
	}

	list, err := svc.List(t.Context(), instructor)
	if err != nil || len(list) != 2 || list[0].ActiveLabExecution == nil || list[0].ActiveLabExecution.ID != execution.ID ||
		list[1].ActiveLabExecution != nil || list[0].MyLabInstance != nil {
		t.Fatalf("List() = %+v, %v", list, err)
	}
}

func TestGetLabProjectionFailuresAreInternal(t *testing.T) {
	org := uuid.New()
	user := newUser(org, repository.OrganizationRoleMember)
	for name, setup := range map[string]func(*fakeStore, repository.LabExecutionSummary){
		"execution query": func(s *fakeStore, _ repository.LabExecutionSummary) {
			s.executionErr = internalRepoError("ActiveLabExecutionByClass")
		},
		"instance query": func(s *fakeStore, _ repository.LabExecutionSummary) {
			s.instanceErr = internalRepoError("LabInstanceByExecutionUser")
		},
		"other user's instance": func(s *fakeStore, e repository.LabExecutionSummary) {
			s.instances[[2]uuid.UUID{e.ID, user.ID}] = repository.LabInstanceSummary{ID: uuid.New(), OrganizationID: org, LabExecutionID: e.ID, UserID: uuid.New()}
		},
	} {
		t.Run(name, func(t *testing.T) {
			store := newFakeStore()
			c := store.addClass(org, "Class")
			store.addMembership(c, user, repository.ClassRoleStudent)
			execution := repository.LabExecutionSummary{ID: uuid.New(), Status: "PROVISIONING"}
			store.executions[c.ID] = execution
			setup(store, execution)

			_, err := NewService(store).Get(t.Context(), user, c.ID.String())
			if err == nil || errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) {
				t.Fatalf("Get() error = %v, want internal", err)
			}
			assertNoRawCause(t, err)
		})
	}
}
