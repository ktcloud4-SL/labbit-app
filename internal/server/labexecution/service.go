// Package labexecution은 Provision 접수(LabExecution 시작)와 LabExecution/LabInstance 조회 use case다.
//
// Provision 접수는 실제 Provider 작업을 실행하지 않는다. LabExecution, immutable resolved CreationSnapshot, 강사·선택 학생의
// LabInstance, PROVISION Operation과 LabInstance별 OperationItem을 하나의 PostgreSQL transaction으로 등록할 뿐이며(D-10, D-19, D-20,
// 기능 명세 F-LAB-02) Worker dispatch와 Connector 호출은 이 package의 범위가 아니다(경계 test가 전이 의존까지 검사).
//
// 권한(D-11):
//   - Provision: 대상 Class의 현재 INSTRUCTOR Membership이 필요하다. organizationRole=ADMIN은 이를 대신하지 않는다.
//     LabSpec은 owner만 사용할 수 있다(Organization 단위 공유는 후속 범위). 다른 Organization의 LabSpec은 없는 것과 같다.
//   - LabExecution 조회: 그 Class의 현재 INSTRUCTOR Membership이 필요하다.
//   - LabInstance 조회: 그 Class의 현재 Membership이 있으면서 LabInstance의 owner이거나 그 Class의 INSTRUCTOR여야 한다.
//
// HTTP status와 Problem Details는 알지 못한다.
package labexecution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/operation"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// 저장 상태 값이다. 접수만으로 READY나 SUCCEEDED를 기록하지 않는다(시스템 설계 S3/S4: LabInstance는 PENDING으로 시작).
const (
	executionStatusProvisioning = "PROVISIONING"
	instanceStatusPending       = "PENDING"
	operationTypeProvision      = "PROVISION"
)

var (
	// ErrNotFound는 Class, LabSpec, LabExecution, LabInstance 중 요청한 대상이 없음을 나타낸다.
	// 서버가 발급하지 않은 형식의 ID와 다른 Organization의 LabSpec도 같다.
	ErrNotFound = errors.New("labexecution: 찾을 수 없음")
	// ErrForbidden은 대상은 있지만 현재 사용자가 그 동작을 할 수 없음을 나타낸다.
	ErrForbidden = errors.New("labexecution: 권한 없음")
	// ErrInvalidRequest는 Idempotency-Key나 대상 학생 목록의 형식(빈 목록, 중복)이 올바르지 않음을 나타낸다.
	ErrInvalidRequest = errors.New("labexecution: 요청 형식 오류")
	// ErrInvalidTargets는 대상 학생 중 이 Class의 현재 STUDENT Membership이 아닌 사용자가 있음을 나타낸다.
	ErrInvalidTargets = errors.New("labexecution: 대상 학생이 올바르지 않음")
	// ErrLabSpecNotProvisionable은 저장된 LabSpec의 Image/Flavor mapping을 지금 실제 Provider 값으로 resolve할 수 없음을 나타낸다
	// (비활성 mapping, 서로 다른 ProviderConnection, VM 수 한도 초과). mapping을 임의로 고르지 않는다.
	ErrLabSpecNotProvisionable = errors.New("labexecution: LabSpec을 생성 기준으로 확정할 수 없음")
	// ErrActiveExecution은 Class에 이미 active LabExecution이 있음을 나타낸다(D-10: Cleanup 후 새 실행).
	ErrActiveExecution = errors.New("labexecution: active LabExecution이 이미 있음")
	// ErrIdempotencyConflict는 같은 Idempotency-Key를 다른 의미의 Provision 요청에 사용했음을 나타낸다.
	ErrIdempotencyConflict = errors.New("labexecution: Idempotency-Key 재사용 충돌")
	// ErrInconsistentData는 저장소가 반환한 관계가 서로 모순됨을 나타낸다. 권한 상태로 해석하지 않는다(fail closed).
	ErrInconsistentData = errors.New("labexecution: 저장된 관계가 서로 모순됨")
)

// maxSnapshotVMs는 LabInstance 하나의 CreationSnapshot에 담을 VM 수 상한이다. LabSpec의 count는 PostgreSQL integer 범위까지
// 저장할 수 있으므로 무제한으로 펼치지 않기 위한 구현 값이며 계약 수치가 아니다.
const maxSnapshotVMs = 64

// errReplayed와 errActiveConflict는 transaction을 rollback시키기 위한 내부 신호다. 밖으로 반환하지 않는다.
var (
	errReplayed       = errors.New("labexecution: replay")
	errActiveConflict = errors.New("labexecution: active LabExecution unique 충돌")
)

// Store는 Service가 사용하는 persistence 경계다. postgres.Store가 구현한다.
type Store interface {
	repository.Transactor
	repository.OperationRepository
	repository.ClassRepository
	repository.LabExecutionRepository
	// LabInstanceByID는 repository.TerminalRepository와 같은 의미다.
	LabInstanceByID(ctx context.Context, id uuid.UUID) (repository.LabInstance, error)
}

// Service는 Provision 접수와 조회 use case다.
type Service struct {
	store Store
	newID func() uuid.UUID
	now   func() time.Time
}

func NewService(store Store) *Service {
	return &Service{store: store, newID: uuid.New, now: time.Now}
}

// ProvisionInput은 Provision 요청이다. ID는 Browser가 보낸 opaque 문자열 그대로다.
type ProvisionInput struct {
	ClassID          string
	LabSpecID        string
	TargetStudentIDs []string
	IdempotencyKey   string
	// RequestID는 운영 correlation 값이며 fingerprint에 포함하지 않는다.
	RequestID string
}

// Accepted는 접수되었거나 replay된 Provision Operation과 그 대상 LabExecution이다.
type Accepted struct {
	OperationID    uuid.UUID
	LabExecutionID uuid.UUID
	// Replayed는 같은 사용자·같은 key·같은 의미의 기존 Operation을 반환했음을 나타낸다. 새 row를 만들지 않았다.
	Replayed bool
}

// Provision은 Provision 요청을 durable하게 접수한다.
//
// 판정 순서: Idempotency-Key 형식 → Class 존재(ErrNotFound) → Class 권한(ErrForbidden) → LabSpec ID·학생 목록 형식 →
// committed replay 확인 → transaction 안에서 강사 Membership 재확인(FOR SHARE, ErrForbidden) → active LabExecution → LabSpec 존재·권한 → 대상 학생 → mapping resolve → INSERT.
// 같은 의미의 replay는 Class 권한을 확인한 뒤에만 반환하며 이후 LabSpec·Membership·mapping 변화와 무관하게 기존 Operation이다.
//
// 동시성: Class당 active LabExecution partial unique index가 경쟁을 결정한다. 같은 key의 동시 요청은 한 요청이 commit할 때까지
// 다른 요청의 LabExecution INSERT가 기다린 뒤 unique 충돌이 되므로, 그 경우 rollback한 다음 commit된 Operation을 다시 조회해
// 같은 의미면 replay, 다른 의미면 ErrIdempotencyConflict, 다른 key면 ErrActiveExecution으로 판정한다.
// 어느 단계에서 실패해도 LabExecution·Snapshot·LabInstance·Operation·OperationItem은 함께 rollback된다.
func (s *Service) Provision(ctx context.Context, user repository.User, in ProvisionInput) (Accepted, error) {
	if _, err := operation.HashKey(in.IdempotencyKey); err != nil {
		return Accepted{}, ErrInvalidRequest
	}
	class, err := s.requireClassRole(ctx, user, in.ClassID, func(m repository.ClassMembership) bool {
		return m.Role == repository.ClassRoleInstructor
	})
	if err != nil {
		return Accepted{}, err
	}
	labSpecID, ok := parseID(in.LabSpecID)
	if !ok {
		return Accepted{}, ErrNotFound
	}
	students, err := parseStudents(in.TargetStudentIDs)
	if err != nil {
		return Accepted{}, err
	}
	fingerprint := provisionFingerprint(class.ID, labSpecID, students)

	if accepted, done, err := s.committedReplay(ctx, user, in.IdempotencyKey, fingerprint); done {
		return accepted, err
	}

	now := s.now()
	executionID := s.newID()
	var registered operation.Result
	err = s.store.WithinTransaction(ctx, func(ctx context.Context, repos repository.Repositories) error {
		// transaction 밖의 Class 권한 확인과 commit 사이에 Membership이 삭제되거나 역할이 바뀔 수 있다. 등록 전에 요청 강사의
		// 현재 Membership을 FOR SHARE로 다시 확인하고 잠가, commit까지 그 Membership의 삭제·역할 변경이 기다리게 한다.
		if err := requireInstructorForShare(ctx, repos, class, user); err != nil {
			return err
		}
		// D-10: 이미 active LabExecution이 있으면 LabSpec·대상 검증보다 먼저 Cleanup이 필요하다고 알린다. 경쟁의 최종 판정은
		// 아래 CreateLabExecution의 partial unique index다.
		if _, err := repos.ActiveLabExecutionByClass(ctx, class.ID); err == nil {
			return errActiveConflict
		} else if !errors.Is(err, repository.ErrNotFound) {
			return fmt.Errorf("labexecution: active LabExecution 조회: %w", err)
		}
		spec, err := repos.LabSpecForShare(ctx, labSpecID)
		if errors.Is(err, repository.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("labexecution: LabSpec 조회: %w", err)
		}
		if spec.OrganizationID != user.OrganizationID {
			return ErrNotFound
		}
		if spec.OwnerUserID != user.ID {
			return ErrForbidden
		}
		if err := requireStudents(ctx, repos, class, students); err != nil {
			return err
		}
		snapshot, connectionID, err := resolveSnapshot(ctx, repos, user.OrganizationID, spec)
		if err != nil {
			return err
		}

		err = repos.CreateLabExecution(ctx, repository.NewLabExecution{
			ID: executionID, OrganizationID: user.OrganizationID, ClassID: class.ID, LabSpecID: spec.ID,
			InstructorUserID: user.ID, Status: executionStatusProvisioning, CreatedAt: now,
		})
		if errors.Is(err, repository.ErrConflict) {
			return errActiveConflict
		}
		if err != nil {
			return fmt.Errorf("labexecution: LabExecution 저장: %w", err)
		}
		if err := repos.CreateCreationSnapshot(ctx, repository.NewCreationSnapshot{
			ID: s.newID(), OrganizationID: user.OrganizationID, LabExecutionID: executionID, SourceLabSpecID: spec.ID,
			SourceLabSpecRevision: spec.Revision, ProviderConnectionID: connectionID, Snapshot: snapshot, CreatedAt: now,
		}); err != nil {
			return fmt.Errorf("labexecution: CreationSnapshot 저장: %w", err)
		}

		participants := append([]participant{{user.ID, repository.ClassRoleInstructor}}, studentParticipants(students)...)
		items := make([]operation.Item, 0, len(participants))
		for _, p := range participants {
			instanceID := s.newID()
			if err := repos.CreateLabInstance(ctx, repository.NewLabInstance{
				ID: instanceID, OrganizationID: user.OrganizationID, LabExecutionID: executionID, UserID: p.userID,
				ParticipantRole: p.role, Status: instanceStatusPending, CreatedAt: now,
			}); err != nil {
				return fmt.Errorf("labexecution: LabInstance 저장: %w", err)
			}
			items = append(items, operation.Item{LabInstanceID: instanceID, Generation: 1})
		}

		registered, err = operation.Register(ctx, repos, operation.Registration{
			OrganizationID: user.OrganizationID, RequestedByUserID: user.ID, Type: operationTypeProvision,
			IdempotencyKey: in.IdempotencyKey, Fingerprint: fingerprint, LabExecutionID: &executionID,
			RequestID: in.RequestID, Items: items,
		}, now)
		if err != nil {
			return err
		}
		if registered.Replayed {
			// 다른 Class의 LabExecution을 만들고 있던 같은 key 요청이 먼저 commit했다. 새로 만든 row는 모두 버린다.
			return errReplayed
		}
		return nil
	})
	switch {
	case err == nil:
		return Accepted{OperationID: registered.Operation.ID, LabExecutionID: executionID}, nil
	case errors.Is(err, errReplayed):
		return acceptedFromReplay(registered.Operation)
	case errors.Is(err, errActiveConflict):
		// rollback된 뒤다. 경쟁한 요청이 이미 commit했으므로 그 결과가 같은 key의 같은 의미인지 권위 있는 DB 상태로 판정한다.
		if accepted, done, err := s.committedReplay(ctx, user, in.IdempotencyKey, fingerprint); done {
			return accepted, err
		}
		return Accepted{}, ErrActiveExecution
	case errors.Is(err, operation.ErrConflict):
		return Accepted{}, ErrIdempotencyConflict
	default:
		return Accepted{}, err
	}
}

// committedReplay는 commit된 같은 사용자·PROVISION·같은 key의 Operation을 찾는다. 없으면 done=false다.
func (s *Service) committedReplay(ctx context.Context, user repository.User, key string, fingerprint operation.Fingerprint) (Accepted, bool, error) {
	result, err := operation.Replay(ctx, s.store, user.ID, operationTypeProvision, key, fingerprint)
	switch {
	case errors.Is(err, repository.ErrNotFound):
		return Accepted{}, false, nil
	case errors.Is(err, operation.ErrConflict):
		return Accepted{}, true, ErrIdempotencyConflict
	case err != nil:
		return Accepted{}, true, fmt.Errorf("labexecution: Idempotency 조회: %w", err)
	}
	if result.Operation.OrganizationID != user.OrganizationID {
		return Accepted{}, true, ErrInconsistentData
	}
	accepted, err := acceptedFromReplay(result.Operation)
	return accepted, true, err
}

func acceptedFromReplay(o repository.Operation) (Accepted, error) {
	if o.Type != operationTypeProvision || o.LabExecutionID == nil || *o.LabExecutionID == uuid.Nil {
		return Accepted{}, ErrInconsistentData
	}
	return Accepted{OperationID: o.ID, LabExecutionID: *o.LabExecutionID, Replayed: true}, nil
}

// provisionFingerprint는 요청 의미의 canonical 표현이다. 대상 Class, LabSpec, 정렬한 대상 학생 집합만 담고
// 새 ID, Trace Context, request ID, 시각, Idempotency-Key는 담지 않는다.
func provisionFingerprint(classID, labSpecID uuid.UUID, students []uuid.UUID) operation.Fingerprint {
	ids := make([]string, len(students))
	for i, id := range students {
		ids[i] = id.String()
	}
	sort.Strings(ids)
	canonical, err := json.Marshal(struct {
		Operation        string   `json:"operation"`
		ClassID          string   `json:"classId"`
		LabSpecID        string   `json:"labSpecId"`
		TargetStudentIDs []string `json:"targetStudentIds"`
	}{"provision/v1", classID.String(), labSpecID.String(), ids})
	if err != nil {
		// string field만 있는 struct라 실패할 수 없다.
		panic(err)
	}
	return operation.SemanticFingerprint(canonical)
}

// parseStudents는 대상 학생 목록의 형식을 확인한다. 빈 목록과 같은 문자열의 중복은 형식 오류이고(OpenAPI minItems/uniqueItems),
// 서버가 발급하지 않은 형식의 ID는 존재하지 않는 사용자와 같다.
func parseStudents(raw []string) ([]uuid.UUID, error) {
	if len(raw) == 0 {
		return nil, ErrInvalidRequest
	}
	seen := make(map[string]bool, len(raw))
	ids := make([]uuid.UUID, 0, len(raw))
	for _, value := range raw {
		if value == "" || seen[value] {
			return nil, ErrInvalidRequest
		}
		seen[value] = true
		id, ok := parseID(value)
		if !ok {
			return nil, ErrInvalidTargets
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// requireInstructorForShare는 user가 지금 이 Class의 INSTRUCTOR Membership을 가졌는지 Membership row를 FOR SHARE로 잠근 채
// 확인한다. 진행 중인 삭제·역할 변경이 있으면 그 commit/rollback을 기다린 뒤의 상태로 판정한다. Membership이 없거나 INSTRUCTOR가
// 아니면 ErrForbidden이며 row를 만들지 않는다. organizationRole은 쓰지 않는다.
func requireInstructorForShare(ctx context.Context, repos repository.Repositories, class repository.Class, user repository.User) error {
	memberships, err := repos.ClassMembershipsForShare(ctx, class.ID, []uuid.UUID{user.ID})
	if err != nil {
		return fmt.Errorf("labexecution: 강사 Membership 조회: %w", err)
	}
	switch len(memberships) {
	case 0:
		return ErrForbidden
	case 1:
	default:
		return ErrInconsistentData
	}
	m := memberships[0]
	if m.ClassID != class.ID || m.UserID != user.ID || m.OrganizationID != class.OrganizationID || m.OrganizationID != user.OrganizationID || !m.Role.Valid() {
		return ErrInconsistentData
	}
	if m.Role != repository.ClassRoleInstructor {
		return ErrForbidden
	}
	return nil
}

// requireStudents는 모든 대상이 이 Class의 현재 STUDENT Membership인지 확인한다. Membership row를 FOR SHARE로 잠가
// 접수 transaction이 commit할 때까지 대상 집합의 Membership 변경과 경쟁하지 않는다.
func requireStudents(ctx context.Context, repos repository.Repositories, class repository.Class, students []uuid.UUID) error {
	memberships, err := repos.ClassMembershipsForShare(ctx, class.ID, students)
	if err != nil {
		return fmt.Errorf("labexecution: 대상 학생 Membership 조회: %w", err)
	}
	roles := make(map[uuid.UUID]repository.ClassRole, len(memberships))
	for _, m := range memberships {
		if m.ClassID != class.ID || m.OrganizationID != class.OrganizationID || !m.Role.Valid() {
			return ErrInconsistentData
		}
		roles[m.UserID] = m.Role
	}
	for _, id := range students {
		if roles[id] != repository.ClassRoleStudent {
			return ErrInvalidTargets
		}
	}
	return nil
}

type participant struct {
	userID uuid.UUID
	role   repository.ClassRole
}

func studentParticipants(students []uuid.UUID) []participant {
	out := make([]participant, len(students))
	for i, id := range students {
		out[i] = participant{id, repository.ClassRoleStudent}
	}
	return out
}

// requireClassRole은 Class 존재와 사용자의 현재 Membership을 확인하고 allowed를 만족하는지 판정한다.
// Class가 없으면 ErrNotFound, 다른 Organization이거나 Membership이 없거나 allowed가 false면 ErrForbidden이다.
func (s *Service) requireClassRole(ctx context.Context, user repository.User, rawClassID string, allowed func(repository.ClassMembership) bool) (repository.Class, error) {
	classID, ok := parseID(rawClassID)
	if !ok {
		return repository.Class{}, ErrNotFound
	}
	class, err := s.store.ClassByID(ctx, classID)
	if errors.Is(err, repository.ErrNotFound) {
		return repository.Class{}, ErrNotFound
	}
	if err != nil {
		return repository.Class{}, fmt.Errorf("labexecution: Class 조회: %w", err)
	}
	if user.ID == uuid.Nil || class.OrganizationID != user.OrganizationID {
		return repository.Class{}, ErrForbidden
	}
	if err := s.requireMembership(ctx, user, class.ID, class.OrganizationID, allowed); err != nil {
		return repository.Class{}, err
	}
	return class, nil
}

func (s *Service) requireMembership(ctx context.Context, user repository.User, classID, organizationID uuid.UUID, allowed func(repository.ClassMembership) bool) error {
	m, err := s.store.ClassMembership(ctx, classID, user.ID)
	if errors.Is(err, repository.ErrNotFound) {
		return ErrForbidden
	}
	if err != nil {
		return fmt.Errorf("labexecution: Membership 조회: %w", err)
	}
	if m.ClassID != classID || m.UserID != user.ID || m.OrganizationID != organizationID || !m.Role.Valid() {
		return ErrInconsistentData
	}
	if !allowed(m) {
		return ErrForbidden
	}
	return nil
}

// parseID는 이 서버가 발급하는 canonical UUID 문자열만 받아들인다.
func parseID(raw string) (uuid.UUID, bool) {
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil || id.String() != raw {
		return uuid.UUID{}, false
	}
	return id, true
}
