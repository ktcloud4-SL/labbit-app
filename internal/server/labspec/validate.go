package labspec

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// Reason은 의미 검증 실패의 안정적인 사유다. 사용자 입력 값을 담지 않는다.
type Reason string

const (
	ReasonNameBlank            Reason = "name_blank"
	ReasonInvalidText          Reason = "invalid_text"
	ReasonVMsEmpty             Reason = "vms_empty"
	ReasonRoleBlank            Reason = "role_blank"
	ReasonDuplicateRole        Reason = "duplicate_role"
	ReasonCountOutOfRange      Reason = "count_out_of_range"
	ReasonWorkspaceRoleUnknown Reason = "workspace_role_unknown"
	ReasonWorkspaceIndexRange  Reason = "workspace_index_out_of_range"
	ReasonImageRefUnknown      Reason = "image_ref_unknown"
	ReasonSizeRefUnknown       Reason = "size_ref_unknown"
	ReasonMixedConnections     Reason = "provider_connection_mismatch"
)

// ValidationError는 ErrInvalid의 구체 사유다. errors.Is(err, ErrInvalid)가 true다.
type ValidationError struct {
	Reason Reason
}

func (e *ValidationError) Error() string        { return "labspec: 의미 검증 실패: " + string(e.Reason) }
func (e *ValidationError) Is(target error) bool { return target == ErrInvalid }

func invalid(reason Reason) error { return &ValidationError{Reason: reason} }

// resolve는 입력을 검증하고 저장할 정의로 바꾼다.
//
// 규칙(000003 Physical Schema와 db/migrations/README.md의 "Application에서 추가로 검증할 의미"):
//   - name은 공백만으로 이루어질 수 없고, 모든 문자열에 NUL이 없다(PostgreSQL text가 저장할 수 없음).
//   - vms는 하나 이상이며 role은 비어 있지 않고 서로 다르며 count는 1 이상 PostgreSQL integer 범위다.
//   - workspaceVm.role은 vms의 role 중 하나이고 instanceIndex는 0 이상 그 role의 count 미만이다.
//   - imageRef/sizeRef는 같은 Organization의 활성 Image/Flavor mapping ID다. 형식이 다르거나 없거나 다른 Organization의 것이거나
//     비활성이면 구분하지 않고 같은 사유다.
//   - LabSpec이 쓰는 모든 mapping은 하나의 ProviderConnection에 속한다(CreationSnapshot이 ProviderConnection 하나를 고정함).
//
// mapping은 FOR SHARE로 잠그므로 commit 전까지 비활성화나 ProviderConnection 변경과 경쟁하지 않는다.
func resolve(ctx context.Context, repos repository.Repositories, organizationID uuid.UUID, in Write, newID func() uuid.UUID) (repository.LabSpecDefinition, error) {
	if strings.TrimSpace(in.Name) == "" {
		return repository.LabSpecDefinition{}, invalid(ReasonNameBlank)
	}
	texts := []*string{&in.Name, in.Description, in.StartupScript, &in.WorkspaceVM.Role}
	for i := range in.VMs {
		texts = append(texts, &in.VMs[i].Role, in.VMs[i].Name, &in.VMs[i].ImageRef, &in.VMs[i].SizeRef)
	}
	for _, text := range texts {
		if text != nil && strings.ContainsRune(*text, 0) {
			return repository.LabSpecDefinition{}, invalid(ReasonInvalidText)
		}
	}
	if len(in.VMs) == 0 {
		return repository.LabSpecDefinition{}, invalid(ReasonVMsEmpty)
	}

	counts := make(map[string]int64, len(in.VMs))
	imageIDs := make([]uuid.UUID, len(in.VMs))
	flavorIDs := make([]uuid.UUID, len(in.VMs))
	for i, vm := range in.VMs {
		if strings.TrimSpace(vm.Role) == "" {
			return repository.LabSpecDefinition{}, invalid(ReasonRoleBlank)
		}
		if _, dup := counts[vm.Role]; dup {
			return repository.LabSpecDefinition{}, invalid(ReasonDuplicateRole)
		}
		if vm.Count < 1 || vm.Count > math.MaxInt32 {
			return repository.LabSpecDefinition{}, invalid(ReasonCountOutOfRange)
		}
		counts[vm.Role] = vm.Count
		var ok bool
		if imageIDs[i], ok = parseID(vm.ImageRef); !ok {
			return repository.LabSpecDefinition{}, invalid(ReasonImageRefUnknown)
		}
		if flavorIDs[i], ok = parseID(vm.SizeRef); !ok {
			return repository.LabSpecDefinition{}, invalid(ReasonSizeRefUnknown)
		}
	}
	count, declared := counts[in.WorkspaceVM.Role]
	if !declared {
		return repository.LabSpecDefinition{}, invalid(ReasonWorkspaceRoleUnknown)
	}
	if in.WorkspaceVM.InstanceIndex < 0 || in.WorkspaceVM.InstanceIndex >= count {
		return repository.LabSpecDefinition{}, invalid(ReasonWorkspaceIndexRange)
	}

	images, err := repos.ImageMappingsForShare(ctx, organizationID, distinct(imageIDs))
	if err != nil {
		return repository.LabSpecDefinition{}, fmt.Errorf("labspec: Image mapping 조회: %w", err)
	}
	flavors, err := repos.FlavorMappingsForShare(ctx, organizationID, distinct(flavorIDs))
	if err != nil {
		return repository.LabSpecDefinition{}, fmt.Errorf("labspec: Flavor mapping 조회: %w", err)
	}
	connection, err := sameConnection(organizationID, imageIDs, images, ReasonImageRefUnknown, uuid.Nil)
	if err != nil {
		return repository.LabSpecDefinition{}, err
	}
	if _, err := sameConnection(organizationID, flavorIDs, flavors, ReasonSizeRefUnknown, connection); err != nil {
		return repository.LabSpecDefinition{}, err
	}

	def := repository.LabSpecDefinition{
		Name:                   in.Name,
		Description:            in.Description,
		InternetOutbound:       in.InternetOutbound,
		StartupScript:          in.StartupScript,
		WorkspaceRole:          in.WorkspaceVM.Role,
		WorkspaceInstanceIndex: in.WorkspaceVM.InstanceIndex,
		VMRoles:                make([]repository.NewLabSpecVMRole, 0, len(in.VMs)),
	}
	for i, vm := range in.VMs {
		def.VMRoles = append(def.VMRoles, repository.NewLabSpecVMRole{
			ID: newID(), Role: vm.Role, DisplayName: vm.Name,
			ImageMappingID: imageIDs[i], FlavorMappingID: flavorIDs[i], VMCount: vm.Count,
		})
	}
	return def, nil
}

// sameConnection은 wanted의 모든 ID가 found에 활성 mapping으로 있고 모두 같은 ProviderConnection에 속하는지 확인하고 그 ID를 반환한다.
// connection이 uuid.Nil이 아니면 그 ProviderConnection이어야 한다.
func sameConnection(organizationID uuid.UUID, wanted []uuid.UUID, found []repository.ProviderMapping, missing Reason, connection uuid.UUID) (uuid.UUID, error) {
	byID := make(map[uuid.UUID]repository.ProviderMapping, len(found))
	for _, m := range found {
		if m.OrganizationID != organizationID {
			return uuid.Nil, ErrInconsistentData
		}
		byID[m.ID] = m
	}
	for _, id := range wanted {
		m, ok := byID[id]
		if !ok || !m.Active {
			return uuid.Nil, invalid(missing)
		}
		if connection == uuid.Nil {
			connection = m.ProviderConnectionID
		}
		if m.ProviderConnectionID != connection {
			return uuid.Nil, invalid(ReasonMixedConnections)
		}
	}
	return connection, nil
}

func distinct(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
