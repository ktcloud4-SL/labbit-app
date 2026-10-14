package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ProvisionRepository는 Provision 접수 transaction이 사용하는 잠금 query와 INSERT다. 모두 Transactor 안에서 사용한다.
// 권한, 대상 학생 판정, mapping 선택은 판단하지 않고 저장된 값을 그대로 전달한다.
type ProvisionRepository interface {
	// LabSpecForShare는 lab_specs row를 FOR SHARE로 잠근 뒤 VM Role을 읽는다. 없으면 ErrNotFound다.
	// LabSpec 수정은 같은 row를 FOR UPDATE로 먼저 잠그므로 잠금을 얻은 뒤 읽은 본문(revision)과 VM Role은 commit까지 같은 revision이다.
	LabSpecForShare(ctx context.Context, id uuid.UUID) (LabSpec, error)
	// ResolvedImageMappingsForShare는 organizationID에 속한 ids의 provider_image_mappings를 실제 Provider Image ID와 함께
	// FOR SHARE로 잠그고 반환한다. 없거나 다른 Organization의 ID는 결과에서 빠지며 활성 여부는 필터링하지 않는다.
	ResolvedImageMappingsForShare(ctx context.Context, organizationID uuid.UUID, ids []uuid.UUID) ([]ResolvedImageMapping, error)
	// ResolvedFlavorMappingsForShare는 provider_flavor_mappings에 대해 ResolvedImageMappingsForShare와 같다.
	ResolvedFlavorMappingsForShare(ctx context.Context, organizationID uuid.UUID, ids []uuid.UUID) ([]ResolvedFlavorMapping, error)
	// ClassMembershipsForShare는 classID의 ClassMembership 중 userIDs에 해당하는 row를 FOR SHARE로 잠그고 반환한다.
	// Membership이 없는 user는 결과에서 빠지며 오류가 아니다. 역할은 필터링하지 않는다.
	ClassMembershipsForShare(ctx context.Context, classID uuid.UUID, userIDs []uuid.UUID) ([]ClassMembership, error)

	// CreateLabExecution은 LabExecution을 저장한다. Class에 이미 active(finished_at IS NULL) LabExecution이 있으면 ErrConflict다.
	// 그 active LabExecution이 아직 commit되지 않았으면 commit/rollback을 기다린 뒤 결과를 반환한다.
	CreateLabExecution(ctx context.Context, execution NewLabExecution) error
	// CreateCreationSnapshot은 immutable CreationSnapshot을 저장한다. LabExecution당 하나이며 이미 있으면 ErrConflict다.
	CreateCreationSnapshot(ctx context.Context, snapshot NewCreationSnapshot) error
	// CreateLabInstance는 LabInstance를 저장한다. 같은 LabExecution에 같은 User 또는 두 번째 INSTRUCTOR이면 ErrConflict다.
	CreateLabInstance(ctx context.Context, instance NewLabInstance) error
}

// LabExecutionRepository는 LabExecution/LabInstance 조회다. 접근 가능 여부는 판단하지 않는다.
type LabExecutionRepository interface {
	// LabExecutionByID는 LabExecution과 그 LabInstance를 statement 하나로 읽는다. 없으면 ErrNotFound다.
	// LabInstance는 INSTRUCTOR, STUDENT 순서로, 같은 역할 안에서는 user ID 순서다.
	LabExecutionByID(ctx context.Context, id uuid.UUID) (LabExecution, error)
	// ActiveLabExecutionByClass는 Class의 active(finished_at IS NULL) LabExecution 요약을 반환한다. 없으면 ErrNotFound다.
	ActiveLabExecutionByClass(ctx context.Context, classID uuid.UUID) (LabExecutionSummary, error)
	// LabInstanceByExecutionUser는 LabExecution에서 userID의 LabInstance를 반환한다. 없으면 ErrNotFound다.
	LabInstanceByExecutionUser(ctx context.Context, labExecutionID, userID uuid.UUID) (LabInstanceSummary, error)
}

// ResolvedImageMapping은 생성 시점에 CreationSnapshot으로 고정할 Image mapping이다. ProviderImageID는 실제 Provider 값이므로
// Browser 응답이나 log에 옮기지 않는다.
type ResolvedImageMapping struct {
	ID                   uuid.UUID
	OrganizationID       uuid.UUID
	ProviderConnectionID uuid.UUID
	Active               bool
	ProviderImageID      string
}

// ResolvedFlavorMapping은 생성 시점에 CreationSnapshot으로 고정할 Flavor mapping과 그 사양이다.
type ResolvedFlavorMapping struct {
	ID                   uuid.UUID
	OrganizationID       uuid.UUID
	ProviderConnectionID uuid.UUID
	Active               bool
	ProviderFlavorID     string
	VCPUs                int64
	RAMMiB               int64
	DiskGiB              int64
}

// LabExecutionSummary는 Class 조회의 activeLabExecution projection이다.
type LabExecutionSummary struct {
	ID     uuid.UUID
	Status string
}

// LabInstanceSummary는 lab_instances row 중 조회 응답에 필요한 값이다. status와 generation의 의미 판정은 Application이 한다.
type LabInstanceSummary struct {
	ID              uuid.UUID
	OrganizationID  uuid.UUID
	LabExecutionID  uuid.UUID
	UserID          uuid.UUID
	ParticipantRole ClassRole
	Status          string
	Generation      int64
}

// LabExecution은 lab_executions row와 그 LabInstance다.
type LabExecution struct {
	ID               uuid.UUID
	OrganizationID   uuid.UUID
	ClassID          uuid.UUID
	LabSpecID        uuid.UUID
	InstructorUserID uuid.UUID
	Status           string
	StartedAt        time.Time
	// FinishedAt은 nil이면 active LabExecution이다.
	FinishedAt   *time.Time
	LabInstances []LabInstanceSummary
}

// NewLabExecution은 Provision 접수 시 저장할 active LabExecution이다. 시각은 Application이 정한다.
type NewLabExecution struct {
	ID               uuid.UUID
	OrganizationID   uuid.UUID
	ClassID          uuid.UUID
	LabSpecID        uuid.UUID
	InstructorUserID uuid.UUID
	Status           string
	CreatedAt        time.Time
}

// NewCreationSnapshot은 저장 후 바꾸지 않는 resolved CreationSnapshot이다. Snapshot은 JSON object이며
// 실제 Provider ID와 Startup Script 본문을 담으므로 Browser 응답이나 log에 옮기지 않는다.
type NewCreationSnapshot struct {
	ID                    uuid.UUID
	OrganizationID        uuid.UUID
	LabExecutionID        uuid.UUID
	SourceLabSpecID       uuid.UUID
	SourceLabSpecRevision int64
	ProviderConnectionID  uuid.UUID
	Snapshot              json.RawMessage
	CreatedAt             time.Time
}

// NewLabInstance는 Provision 접수 시 저장할 LabInstance다. generation은 1이다.
type NewLabInstance struct {
	ID              uuid.UUID
	OrganizationID  uuid.UUID
	LabExecutionID  uuid.UUID
	UserID          uuid.UUID
	ParticipantRole ClassRole
	Status          string
	CreatedAt       time.Time
}
