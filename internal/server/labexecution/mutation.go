package labexecution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/operation"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// Reset/Cleanup 접수(F-LAB-03/04, D-19, D-20)는 Provision 접수와 같이 durable 등록만 한다. 실제 Provider Preflight, 기존 리소스
// 삭제, Terminal/Live 종료, generation 전환, LabExecution 종료(finished_at)는 Worker의 책임이며 이 package는 수행하지 않는다.
//
// 권한(D-11):
//   - Reset: LabInstance가 속한 Class의 현재 INSTRUCTOR Membership이 필요하고 대상은 그 LabExecution의 STUDENT LabInstance다
//     (F-LAB-03 "학생 Lab Reset · 강사"). 강사용 LabInstance Reset과 학생 셀프 Reset은 근거가 없어 허용하지 않는다.
//   - Cleanup: LabExecution이 속한 Class의 현재 INSTRUCTOR Membership이 필요하다.
//   - organizationRole=ADMIN은 둘 다 대신하지 않는다.
//
// 대상과 generation:
//   - Reset은 기존 LabInstance ID 하나에 현재 generation g에 대해 g+1 OperationItem을 등록한다. lab_instances.generation은 g로 두며
//     재현 기준은 그 LabExecution의 기존 immutable CreationSnapshot이다(최신 LabSpec/mapping을 다시 resolve하지 않는다).
//   - Cleanup은 LabExecution에 저장된 LabInstance 전부(강사 1 + 접수 때 고정한 학생)에 각자의 현재 generation으로 등록한다.
//     현재 ClassMembership으로 대상을 다시 계산하지 않는다.
const (
	operationTypeReset   = "RESET"
	operationTypeCleanup = "CLEANUP"
)

var (
	// ErrMutationInProgress는 대상 LabInstance 중 하나에 아직 끝나지 않은(PENDING/RUNNING/RECONCILING) Provision/Reset/Cleanup
	// OperationItem이 있음을 나타낸다(D-20: 대기시키지 않고 거절). 결과 불명(RECONCILING)도 완료로 간주하지 않는다.
	ErrMutationInProgress = errors.New("labexecution: 진행 중인 변경 작업이 있음")
	// ErrExecutionFinished는 이미 종료된(finished_at이 기록된) LabExecution이나 그 LabInstance에 대한 Reset/Cleanup임을 나타낸다.
	ErrExecutionFinished = errors.New("labexecution: 종료된 LabExecution")
	// ErrInvalidResetTarget은 Reset 대상이 STUDENT LabInstance가 아님(강사용 LabInstance)을 나타낸다.
	ErrInvalidResetTarget = errors.New("labexecution: Reset 대상이 학생 LabInstance가 아님")
)

// ResetInput은 Reset 요청이다. ID는 Browser가 보낸 opaque 문자열 그대로다.
type ResetInput struct {
	LabInstanceID  string
	IdempotencyKey string
	// RequestID는 운영 correlation 값이며 fingerprint에 포함하지 않는다.
	RequestID string
}

// CleanupInput은 Cleanup 요청이다. ID는 Browser가 보낸 opaque 문자열 그대로다.
type CleanupInput struct {
	LabExecutionID string
	IdempotencyKey string
	// RequestID는 운영 correlation 값이며 fingerprint에 포함하지 않는다.
	RequestID string
}

// MutationAccepted는 접수되었거나 replay된 Reset/Cleanup Operation이다.
type MutationAccepted struct {
	OperationID uuid.UUID
	// TargetID는 Reset이면 LabInstance, Cleanup이면 LabExecution ID다.
	TargetID uuid.UUID
	// Replayed는 같은 사용자·같은 key·같은 의미의 기존 Operation을 반환했음을 나타낸다. 새 row를 만들지 않았다.
	Replayed bool
}

// Reset은 학생 LabInstance Reset 요청을 durable하게 접수한다.
//
// 판정 순서: Idempotency-Key 형식 → LabInstance 존재(ErrNotFound) → Organization·현재 INSTRUCTOR Membership(ErrForbidden) →
// committed replay → transaction 안에서 강사 Membership 재확인(FOR SHARE, ErrForbidden) → LabExecution과 LabInstance 잠금(FOR SHARE) →
// 대상이 STUDENT LabInstance인지(ErrInvalidResetTarget) → LabExecution 종료 여부(ErrExecutionFinished) → CreationSnapshot 존재 → 등록.
// 같은 의미의 replay는 권한 확인 뒤에만 반환하며 이후 generation·상태 변화와 무관하게 기존 Operation이다.
//
// 동시성: LabInstance당 active OperationItem partial unique index가 다른 변경 작업과의 경쟁을 결정한다(ErrMutationInProgress).
// 같은 key의 동시 요청은 Operation idempotency unique key에서 기다린 뒤 replay가 된다. 실패하면 Operation과 Item은 함께 rollback된다.
func (s *Service) Reset(ctx context.Context, user repository.User, in ResetInput) (MutationAccepted, error) {
	if _, err := operation.HashKey(in.IdempotencyKey); err != nil {
		return MutationAccepted{}, ErrInvalidRequest
	}
	id, ok := parseID(in.LabInstanceID)
	if !ok {
		return MutationAccepted{}, ErrNotFound
	}
	li, err := s.store.LabInstanceByID(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return MutationAccepted{}, ErrNotFound
	}
	if err != nil {
		return MutationAccepted{}, fmt.Errorf("labexecution: LabInstance 조회: %w", err)
	}
	if user.ID == uuid.Nil || li.OrganizationID != user.OrganizationID {
		return MutationAccepted{}, ErrForbidden
	}
	if err := s.requireMembership(ctx, user, li.ClassID, li.OrganizationID, isInstructor); err != nil {
		return MutationAccepted{}, err
	}
	fingerprint := mutationFingerprint("reset/v1", id)
	if accepted, done, err := s.committedMutationReplay(ctx, user, operationTypeReset, in.IdempotencyKey, fingerprint, id); done {
		return accepted, err
	}

	now := s.now()
	var registered operation.Result
	err = s.store.WithinTransaction(ctx, func(ctx context.Context, repos repository.Repositories) error {
		if err := requireInstructorForShare(ctx, repos, li.ClassID, li.OrganizationID, user); err != nil {
			return err
		}
		e, err := lockExecution(ctx, repos, li.LabExecutionID, li.OrganizationID, li.ClassID)
		if err != nil {
			return err
		}
		var target *repository.LabInstanceSummary
		for i := range e.LabInstances {
			if e.LabInstances[i].ID == id {
				target = &e.LabInstances[i]
			}
		}
		if target == nil || target.UserID != li.UserID {
			return ErrInconsistentData
		}
		if target.ParticipantRole != repository.ClassRoleStudent {
			return ErrInvalidResetTarget
		}
		if e.FinishedAt != nil {
			return ErrExecutionFinished
		}
		// D-19: 재현 기준은 Provision 때 고정한 CreationSnapshot이다. 없으면 최신 LabSpec으로 대신하지 않고 모순으로 거절한다.
		if !e.HasCreationSnapshot {
			return ErrInconsistentData
		}
		registered, err = operation.Register(ctx, repos, operation.Registration{
			OrganizationID: li.OrganizationID, RequestedByUserID: user.ID, Type: operationTypeReset,
			IdempotencyKey: in.IdempotencyKey, Fingerprint: fingerprint, LabInstanceID: &id, RequestID: in.RequestID,
			// 새 generation g+1은 Worker가 기존 generation을 정리하고 생성한 뒤에 활성화한다. 접수는 lab_instances.generation을 바꾸지 않는다.
			Items: []operation.Item{{LabInstanceID: id, Generation: target.Generation + 1}},
		}, now)
		return replaySignal(registered, err)
	})
	return mutationResult(operationTypeReset, id, registered, err)
}

// Cleanup은 LabExecution Cleanup 요청을 durable하게 접수한다.
//
// 판정 순서: Idempotency-Key 형식 → LabExecution 존재(ErrNotFound) → Organization·현재 INSTRUCTOR Membership(ErrForbidden) →
// committed replay → transaction 안에서 강사 Membership 재확인(FOR SHARE, ErrForbidden) → LabExecution과 모든 LabInstance 잠금
// (FOR SHARE, id 순서) → LabExecution 종료 여부(ErrExecutionFinished) → LabInstance마다 현재 generation으로 등록.
// 한 Item이라도 충돌하거나 실패하면 Operation과 모든 Item이 rollback된다.
//
// 접수는 finished_at, LabExecution/LabInstance 상태, ProviderResource를 바꾸지 않는다. finished_at을 먼저 기록하면 Class당 active
// LabExecution 제약이 풀려 잔여 리소스를 확인하기 전에 다음 Provision이 허용되기 때문이다(D-20).
func (s *Service) Cleanup(ctx context.Context, user repository.User, in CleanupInput) (MutationAccepted, error) {
	if _, err := operation.HashKey(in.IdempotencyKey); err != nil {
		return MutationAccepted{}, ErrInvalidRequest
	}
	id, ok := parseID(in.LabExecutionID)
	if !ok {
		return MutationAccepted{}, ErrNotFound
	}
	e, err := s.store.LabExecutionByID(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return MutationAccepted{}, ErrNotFound
	}
	if err != nil {
		return MutationAccepted{}, fmt.Errorf("labexecution: LabExecution 조회: %w", err)
	}
	if user.ID == uuid.Nil || e.OrganizationID != user.OrganizationID {
		return MutationAccepted{}, ErrForbidden
	}
	if err := s.requireMembership(ctx, user, e.ClassID, e.OrganizationID, isInstructor); err != nil {
		return MutationAccepted{}, err
	}
	fingerprint := mutationFingerprint("cleanup/v1", id)
	if accepted, done, err := s.committedMutationReplay(ctx, user, operationTypeCleanup, in.IdempotencyKey, fingerprint, id); done {
		return accepted, err
	}

	now := s.now()
	var registered operation.Result
	err = s.store.WithinTransaction(ctx, func(ctx context.Context, repos repository.Repositories) error {
		if err := requireInstructorForShare(ctx, repos, e.ClassID, e.OrganizationID, user); err != nil {
			return err
		}
		locked, err := lockExecution(ctx, repos, id, e.OrganizationID, e.ClassID)
		if err != nil {
			return err
		}
		if locked.FinishedAt != nil {
			return ErrExecutionFinished
		}
		items := make([]operation.Item, 0, len(locked.LabInstances))
		for _, i := range locked.LabInstances {
			items = append(items, operation.Item{LabInstanceID: i.ID, Generation: i.Generation})
		}
		registered, err = operation.Register(ctx, repos, operation.Registration{
			OrganizationID: e.OrganizationID, RequestedByUserID: user.ID, Type: operationTypeCleanup,
			IdempotencyKey: in.IdempotencyKey, Fingerprint: fingerprint, LabExecutionID: &id, RequestID: in.RequestID, Items: items,
		}, now)
		return replaySignal(registered, err)
	})
	return mutationResult(operationTypeCleanup, id, registered, err)
}

// lockExecution은 LabExecution과 모든 LabInstance를 FOR SHARE로 잠그고 저장된 관계가 서로 맞는지 확인한다.
// 강사 LabInstance는 정확히 하나이고 LabExecution의 강사여야 하며 LabInstance가 하나도 없으면 모순이다(fail closed).
func lockExecution(ctx context.Context, repos repository.Repositories, id, organizationID, classID uuid.UUID) (repository.LockedLabExecution, error) {
	e, err := repos.LabExecutionForShare(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return repository.LockedLabExecution{}, ErrNotFound
	}
	if err != nil {
		return repository.LockedLabExecution{}, fmt.Errorf("labexecution: LabExecution 잠금: %w", err)
	}
	if e.ID != id || e.OrganizationID != organizationID || e.ClassID != classID {
		return repository.LockedLabExecution{}, ErrInconsistentData
	}
	instructors := 0
	for _, i := range e.LabInstances {
		if i.OrganizationID != e.OrganizationID || i.LabExecutionID != e.ID || i.Generation < 1 {
			return repository.LockedLabExecution{}, ErrInconsistentData
		}
		switch i.ParticipantRole {
		case repository.ClassRoleInstructor:
			if i.UserID != e.InstructorUserID {
				return repository.LockedLabExecution{}, ErrInconsistentData
			}
			instructors++
		case repository.ClassRoleStudent:
		default:
			return repository.LockedLabExecution{}, ErrInconsistentData
		}
	}
	if instructors != 1 {
		return repository.LockedLabExecution{}, ErrInconsistentData
	}
	return e, nil
}

// replaySignal은 Register 결과를 transaction callback 결과로 바꾼다. replay면 새 row가 없도록 rollback 신호를 반환한다.
func replaySignal(registered operation.Result, err error) error {
	if err != nil {
		return err
	}
	if registered.Replayed {
		return errReplayed
	}
	return nil
}

// mutationResult는 등록 transaction의 결과를 판정한다. 이 transaction의 INSERT 중 unique 충돌이 날 수 있는 것은 OperationItem의
// LabInstance당 active mutation index뿐이므로(Operation의 idempotency 충돌은 Register가 replay로 처리) repository.ErrConflict는
// 진행 중인 변경 작업이다. FK/Check 위반과 generation 불일치(ErrConstraintViolation)는 충돌로 숨기지 않는다.
func mutationResult(kind string, target uuid.UUID, registered operation.Result, err error) (MutationAccepted, error) {
	switch {
	case err == nil:
		return MutationAccepted{OperationID: registered.Operation.ID, TargetID: target}, nil
	case errors.Is(err, errReplayed):
		return mutationFromReplay(registered.Operation, kind, target)
	case errors.Is(err, operation.ErrConflict):
		return MutationAccepted{}, ErrIdempotencyConflict
	case errors.Is(err, repository.ErrConflict):
		return MutationAccepted{}, ErrMutationInProgress
	default:
		return MutationAccepted{}, err
	}
}

// committedMutationReplay는 commit된 같은 사용자·같은 종류·같은 key의 Operation을 찾는다. 없으면 done=false다.
func (s *Service) committedMutationReplay(ctx context.Context, user repository.User, kind, key string, fingerprint operation.Fingerprint, target uuid.UUID) (MutationAccepted, bool, error) {
	result, err := operation.Replay(ctx, s.store, user.ID, kind, key, fingerprint)
	switch {
	case errors.Is(err, repository.ErrNotFound):
		return MutationAccepted{}, false, nil
	case errors.Is(err, operation.ErrConflict):
		return MutationAccepted{}, true, ErrIdempotencyConflict
	case err != nil:
		return MutationAccepted{}, true, fmt.Errorf("labexecution: Idempotency 조회: %w", err)
	}
	if result.Operation.OrganizationID != user.OrganizationID {
		return MutationAccepted{}, true, ErrInconsistentData
	}
	accepted, err := mutationFromReplay(result.Operation, kind, target)
	return accepted, true, err
}

func mutationFromReplay(o repository.Operation, kind string, target uuid.UUID) (MutationAccepted, error) {
	var stored *uuid.UUID
	switch kind {
	case operationTypeReset:
		stored = o.LabInstanceID
	case operationTypeCleanup:
		stored = o.LabExecutionID
	}
	if o.Type != kind || stored == nil || *stored != target {
		return MutationAccepted{}, ErrInconsistentData
	}
	return MutationAccepted{OperationID: o.ID, TargetID: target, Replayed: true}, nil
}

// mutationFingerprint는 Reset/Cleanup 요청 의미의 canonical 표현이다. 요청 종류와 대상 ID만 담는다. key, 새 ID, Trace Context,
// request ID, 시각은 물론 현재 generation이나 lifecycle 상태도 담지 않는다. 그래서 같은 key의 재요청은 그사이 generation이 바뀌어도 replay다.
func mutationFingerprint(kind string, target uuid.UUID) operation.Fingerprint {
	canonical, err := json.Marshal(struct {
		Operation string `json:"operation"`
		TargetID  string `json:"targetId"`
	}{kind, target.String()})
	if err != nil {
		// string field만 있는 struct라 실패할 수 없다.
		panic(err)
	}
	return operation.SemanticFingerprint(canonical)
}

func isInstructor(m repository.ClassMembership) bool { return m.Role == repository.ClassRoleInstructor }
