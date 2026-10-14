package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ClassMemberRepository는 Class 참여자 목록과 사용자의 Class 역할 보유 여부를 조회한다. 접근 가능 여부는 판단하지 않는다.
type ClassMemberRepository interface {
	// ClassMembers는 Class의 모든 ClassMembership을 INSTRUCTOR, STUDENT 순서로, 같은 역할 안에서는 username, user ID 순서로 반환한다.
	// Class가 없거나 참여자가 없으면 빈 목록이며 오류가 아니다. Local Account가 없는 User의 Username은 빈 문자열이다.
	// disabled User도 필터링하지 않고 그대로 반환한다.
	ClassMembers(ctx context.Context, classID uuid.UUID) ([]ClassMember, error)
	// HasClassRole은 user가 어떤 Class에서든 role의 ClassMembership을 하나 이상 가지면 true다.
	HasClassRole(ctx context.Context, userID uuid.UUID, role ClassRole) (bool, error)
}

// LabSpecRepository는 LabSpec 정의와 그 정의가 참조하는 Image/Flavor mapping의 query와 변경이다.
// 조회 권한, owner 여부, revision 일치 여부, 입력의 의미 검증은 판단하지 않는다.
// LabSpec 저장은 정의만 저장하며 Operation, ProviderResource, Connector 호출과 무관하다.
type LabSpecRepository interface {
	// ActiveImageMappingOptions는 Organization의 활성 Image 후보를 ProviderConnection ID, 표시 이름, Mapping ID 순서로 잠금 없이 조회한다.
	ActiveImageMappingOptions(ctx context.Context, organizationID uuid.UUID) ([]ImageMappingOption, error)
	// ActiveFlavorMappingOptions는 같은 범위와 순서의 Flavor 후보다. ProviderConnection 상태는 필터링하지 않는다.
	ActiveFlavorMappingOptions(ctx context.Context, organizationID uuid.UUID) ([]FlavorMappingOption, error)
	// LabSpecSummariesByOwner는 Organization에서 ownerUserID가 owner인 LabSpec 요약을 이름, ID 순서로 반환한다. 없으면 빈 목록이다.
	LabSpecSummariesByOwner(ctx context.Context, organizationID, ownerUserID uuid.UUID) ([]LabSpecSummary, error)
	// LabSpecByID는 Organization과 무관하게 ID로 LabSpec과 VM Role을 잠그지 않고 반환한다. 없으면 ErrNotFound다.
	LabSpecByID(ctx context.Context, id uuid.UUID) (LabSpec, error)
	// LabSpecForUpdate는 lab_specs row를 FOR UPDATE로 잠그고 LabSpec과 VM Role을 반환한다. 없으면 ErrNotFound다.
	// transaction 안에서 사용하며 잠금은 commit/rollback까지 유지되어 같은 LabSpec의 동시 수정이 순서대로 revision을 확인한다.
	LabSpecForUpdate(ctx context.Context, id uuid.UUID) (LabSpec, error)

	// ImageMappingsForShare는 organizationID에 속한 ids의 provider_image_mappings를 FOR SHARE로 잠그고 반환한다.
	// 없거나 다른 Organization의 ID는 결과에서 빠지며 오류가 아니다. 활성 여부는 필터링하지 않는다.
	// 잠금은 commit까지 유지되어 그 사이 mapping의 비활성화나 ProviderConnection 변경이 기다린다.
	ImageMappingsForShare(ctx context.Context, organizationID uuid.UUID, ids []uuid.UUID) ([]ProviderMapping, error)
	// FlavorMappingsForShare는 provider_flavor_mappings에 대해 ImageMappingsForShare와 같다.
	FlavorMappingsForShare(ctx context.Context, organizationID uuid.UUID, ids []uuid.UUID) ([]ProviderMapping, error)

	// CreateLabSpec은 revision 1의 LabSpec과 VM Role을 저장한다. 여러 statement이므로 transaction 안에서 사용한다.
	// 같은 ID가 있거나 VM Role이 중복되면 ErrConflict, 다른 Organization의 owner/mapping 참조나 Check 위반은 ErrConstraintViolation이다.
	CreateLabSpec(ctx context.Context, spec NewLabSpec) (LabSpec, error)
	// UpdateLabSpec은 (ID, Organization, ExpectedRevision)이 모두 일치하는 LabSpec만 본문을 바꾸고 revision을 1 올린 뒤
	// VM Role을 전달된 목록으로 교체한다. 여러 statement이므로 transaction 안에서 사용한다.
	// 일치하는 row가 없으면 아무것도 바꾸지 않고 ok=false이며 오류가 아니다(없는 row와 revision 불일치를 구분하지 않음).
	UpdateLabSpec(ctx context.Context, update LabSpecUpdate) (spec LabSpec, ok bool, err error)
}

// ClassMember는 Class 참여자와 그 Class에서의 역할이다.
type ClassMember struct {
	OrganizationID uuid.UUID
	UserID         uuid.UUID
	Username       string
	Role           ClassRole
}

// LabSpecSummary는 LabSpec 목록에 필요한 값이다.
type LabSpecSummary struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	OwnerUserID    uuid.UUID
	Name           string
	// Description은 저장하지 않았으면 nil이다.
	Description *string
}

// LabSpec은 lab_specs row와 그 VM Role이다. VMRoles는 role 순서다.
type LabSpec struct {
	LabSpecSummary
	Revision         int64
	InternetOutbound bool
	// StartupScript는 저장하지 않았으면 nil이다.
	StartupScript          *string
	WorkspaceRole          string
	WorkspaceInstanceIndex int64
	VMRoles                []LabSpecVMRole
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// LabSpecVMRole은 lab_spec_vm_roles row다.
type LabSpecVMRole struct {
	ID   uuid.UUID
	Role string
	// DisplayName은 저장하지 않았으면 nil이다.
	DisplayName     *string
	ImageMappingID  uuid.UUID
	FlavorMappingID uuid.UUID
	VMCount         int64
}

// ProviderMapping은 provider_image_mappings 또는 provider_flavor_mappings 중 LabSpec 참조 검증에 필요한 값이다.
// Provider의 실제 Image/Flavor ID는 담지 않는다.
type ProviderMapping struct {
	ID                   uuid.UUID
	OrganizationID       uuid.UUID
	ProviderConnectionID uuid.UUID
	Active               bool
}

// ImageMappingOption은 후보 선택에 필요한 저장값이며 실제 Provider 리소스 ID는 담지 않는다.
type ImageMappingOption struct {
	ID                   uuid.UUID
	ProviderConnectionID uuid.UUID
	DisplayName          string
}

// FlavorMappingOption은 Flavor 후보와 저장된 기본 사양이다.
type FlavorMappingOption struct {
	ID                   uuid.UUID
	ProviderConnectionID uuid.UUID
	DisplayName          string
	VCPUs                int64
	RAMMiB               int64
	DiskGiB              int64
}

// LabSpecDefinition은 LabSpec에서 사용자가 편집할 수 있는 본문이다.
type LabSpecDefinition struct {
	Name                   string
	Description            *string
	InternetOutbound       bool
	StartupScript          *string
	WorkspaceRole          string
	WorkspaceInstanceIndex int64
	VMRoles                []NewLabSpecVMRole
}

// NewLabSpecVMRole은 저장할 VM Role이다. ID는 Application이 정한다.
type NewLabSpecVMRole struct {
	ID              uuid.UUID
	Role            string
	DisplayName     *string
	ImageMappingID  uuid.UUID
	FlavorMappingID uuid.UUID
	VMCount         int64
}

// NewLabSpec은 새로 저장할 LabSpec이다. ID는 Application이 정하고 revision은 1이다.
type NewLabSpec struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	OwnerUserID    uuid.UUID
	LabSpecDefinition
}

// LabSpecUpdate는 기존 LabSpec을 ExpectedRevision일 때만 Definition으로 바꾸는 조건부 수정이다.
type LabSpecUpdate struct {
	ID               uuid.UUID
	OrganizationID   uuid.UUID
	ExpectedRevision int64
	LabSpecDefinition
}
