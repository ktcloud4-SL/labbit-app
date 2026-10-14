package labexecution

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// InstanceView는 LabInstance 요약이다. 실제 Provider 값이나 CreationSnapshot은 담지 않는다.
type InstanceView struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	LabExecutionID uuid.UUID
	Status         string
	Generation     int64
}

// ExecutionView는 LabExecution 상세다. TargetUserIDs는 접수 때 고정한 학생 집합이며 저장된 학생 LabInstance에서 읽는다
// (현재 ClassMembership으로 다시 계산하지 않는다). LabInstances는 강사, 학생 순서다.
type ExecutionView struct {
	ID               uuid.UUID
	ClassID          uuid.UUID
	LabSpecID        uuid.UUID
	InstructorUserID uuid.UUID
	TargetUserIDs    []uuid.UUID
	Status           string
	LabInstances     []InstanceView
}

// Execution은 LabExecution 상세를 반환한다. 그 Class의 현재 INSTRUCTOR만 볼 수 있다.
// 없으면 ErrNotFound, 다른 Organization이거나 INSTRUCTOR Membership이 없으면 ErrForbidden이다.
func (s *Service) Execution(ctx context.Context, user repository.User, labExecutionID string) (ExecutionView, error) {
	id, ok := parseID(labExecutionID)
	if !ok {
		return ExecutionView{}, ErrNotFound
	}
	e, err := s.store.LabExecutionByID(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return ExecutionView{}, ErrNotFound
	}
	if err != nil {
		return ExecutionView{}, fmt.Errorf("labexecution: LabExecution 조회: %w", err)
	}
	if user.ID == uuid.Nil || e.OrganizationID != user.OrganizationID {
		return ExecutionView{}, ErrForbidden
	}
	if err := s.requireMembership(ctx, user, e.ClassID, e.OrganizationID, func(m repository.ClassMembership) bool {
		return m.Role == repository.ClassRoleInstructor
	}); err != nil {
		return ExecutionView{}, err
	}

	view := ExecutionView{
		ID: e.ID, ClassID: e.ClassID, LabSpecID: e.LabSpecID, InstructorUserID: e.InstructorUserID, Status: e.Status,
		TargetUserIDs: make([]uuid.UUID, 0, len(e.LabInstances)), LabInstances: make([]InstanceView, 0, len(e.LabInstances)),
	}
	instructors := 0
	for _, i := range e.LabInstances {
		if i.OrganizationID != e.OrganizationID || i.LabExecutionID != e.ID || i.Generation < 1 {
			return ExecutionView{}, ErrInconsistentData
		}
		switch i.ParticipantRole {
		case repository.ClassRoleInstructor:
			if i.UserID != e.InstructorUserID {
				return ExecutionView{}, ErrInconsistentData
			}
			instructors++
		case repository.ClassRoleStudent:
			view.TargetUserIDs = append(view.TargetUserIDs, i.UserID)
		default:
			return ExecutionView{}, ErrInconsistentData
		}
		view.LabInstances = append(view.LabInstances, instanceView(i.ID, i.UserID, i.LabExecutionID, i.Status, i.Generation))
	}
	if instructors != 1 {
		return ExecutionView{}, ErrInconsistentData
	}
	return view, nil
}

// Instance는 LabInstance 상세를 반환한다. 그 Class의 현재 Membership이 있으면서 owner이거나 INSTRUCTOR여야 한다.
// 없으면 ErrNotFound, 다른 Organization이거나 조건을 만족하지 않으면 ErrForbidden이다. organizationRole=ADMIN은 대신하지 않는다.
func (s *Service) Instance(ctx context.Context, user repository.User, labInstanceID string) (InstanceView, error) {
	id, ok := parseID(labInstanceID)
	if !ok {
		return InstanceView{}, ErrNotFound
	}
	li, err := s.store.LabInstanceByID(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return InstanceView{}, ErrNotFound
	}
	if err != nil {
		return InstanceView{}, fmt.Errorf("labexecution: LabInstance 조회: %w", err)
	}
	if user.ID == uuid.Nil || li.OrganizationID != user.OrganizationID {
		return InstanceView{}, ErrForbidden
	}
	if err := s.requireMembership(ctx, user, li.ClassID, li.OrganizationID, func(m repository.ClassMembership) bool {
		return li.UserID == user.ID || m.Role == repository.ClassRoleInstructor
	}); err != nil {
		return InstanceView{}, err
	}
	if li.Generation < 1 {
		return InstanceView{}, ErrInconsistentData
	}
	return instanceView(li.ID, li.UserID, li.LabExecutionID, li.Status, li.Generation), nil
}

func instanceView(id, userID, executionID uuid.UUID, status string, generation int64) InstanceView {
	return InstanceView{ID: id, UserID: userID, LabExecutionID: executionID, Status: status, Generation: generation}
}
