package connector

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

// ErrInvalidCommand는 outbound command/request가 connector.schema.json 계약에 맞지 않아 보내지 않았음을 나타낸다.
// 전송을 시도하지 않았고 pending도 만들지 않았다.
var ErrInvalidCommand = errors.New("connector: command가 Connector 계약에 맞지 않음")

// startupScriptDigest는 StartupScriptSnapshot.sha256의 Schema pattern이다.
var startupScriptDigest = regexp.MustCompile(`^[A-Fa-f0-9]{64}$`)

func invalidCommand(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidCommand, fmt.Sprintf(format, args...))
}

func validateCorrelation(c Correlation) error {
	if c.OperationID == "" {
		return invalidCommand("operationId가 필요합니다")
	}
	if c.LabInstanceID == "" {
		return invalidCommand("labInstanceId가 필요합니다")
	}
	if c.Generation < 1 {
		return invalidCommand("generation은 1 이상이어야 합니다")
	}
	return nil
}

func validateResourceRefs(refs []protocol.ProviderResourceRef, what string) error {
	for i, ref := range refs {
		if ref.ResourceType == "" || ref.ProviderID == "" {
			return invalidCommand("%s[%d]에 resourceType과 providerId가 필요합니다", what, i)
		}
		if ref.Generation < 1 {
			return invalidCommand("%s[%d].generation은 1 이상이어야 합니다", what, i)
		}
	}
	return nil
}

// validateOperationCommand는 OperationCommandMessage의 required 조건과 mutationType별 조건을 확인한다.
// 공유 protocol type이 Schema에 없는 imageRef/flavorRef를 담을 수 있으므로 계약에 없는 값은 보내지 않는다.
func validateOperationCommand(cmd OperationCommand) error {
	if err := validateCorrelation(cmd.Correlation); err != nil {
		return err
	}
	payload := cmd.Payload
	switch payload.MutationType {
	case protocol.MutationTypeProvision:
		// PROVISION은 정리할 기존 리소스가 없으므로 providerResources를 요구하지 않는다.
		if err := requireCreationSnapshot(payload); err != nil {
			return err
		}
	case protocol.MutationTypeReset:
		if err := requireCreationSnapshot(payload); err != nil {
			return err
		}
		if err := validateResetResources(payload.ProviderResources, cmd.Correlation.Generation); err != nil {
			return err
		}
	case protocol.MutationTypeCleanup:
		// Schema는 CLEANUP의 providerResources property를 required로 두지만 array에 minItems가 없다. 그래서 빈 목록([])은
		// 유효하고(정리할 추적 리소스가 없는 경우) 그대로 보낸다. nil은 property 누락이다. 호출자가 리소스를 조회하지 못한 실수를
		// "정리할 것 없음"으로 바꿔 보내면 잔여 리소스가 남으므로 빈 목록은 명시적인 빈 non-nil 목록으로만 표현한다.
		if payload.ProviderResources == nil {
			return invalidCommand("CLEANUP에는 providerResources가 필요합니다(정리할 것이 없으면 빈 목록을 명시)")
		}
	default:
		return invalidCommand("지원하지 않는 mutationType입니다")
	}
	return validateResourceRefs(payload.ProviderResources, "providerResources")
}

func requireCreationSnapshot(payload protocol.OperationCommandPayload) error {
	if payload.CreationSnapshot == nil {
		return invalidCommand("%s에는 creationSnapshot이 필요합니다", payload.MutationType)
	}
	return validateCreationSnapshot(payload.CreationSnapshot)
}

// validateResetResources는 RESET이 정리할 직전 generation 리소스 목록이 Schema 계약을 만족하는지 확인한다.
// 기존 generation을 확정·정리하지 못한 채 다음 generation을 Provision하면 이전 리소스가 남으므로, 목록이 없거나 비었거나
// 직전 generation의 것이 아니면 Connector로 보내지 않는다. resourceType/providerId는 이어지는 validateResourceRefs가 확인한다.
func validateResetResources(refs []protocol.ProviderResourceRef, generation int64) error {
	if generation < 2 {
		return invalidCommand("RESET의 generation은 2 이상이어야 합니다(직전 generation이 있어야 합니다)")
	}
	if refs == nil {
		return invalidCommand("RESET에는 providerResources가 필요합니다")
	}
	if len(refs) == 0 {
		return invalidCommand("RESET의 providerResources에는 리소스가 하나 이상 필요합니다")
	}
	previous := generation - 1
	for i, ref := range refs {
		if ref.LogicalName == "" {
			return invalidCommand("RESET providerResources[%d]에 logicalName이 필요합니다", i)
		}
		if ref.Generation != previous {
			return invalidCommand("RESET providerResources[%d].generation은 직전 generation(%d)이어야 합니다", i, previous)
		}
	}
	return nil
}

func validateCreationSnapshot(s *protocol.CreationSnapshot) error {
	if s.ProviderConnectionID == "" {
		return invalidCommand("creationSnapshot.providerConnectionId가 필요합니다")
	}
	if len(s.VMs) == 0 {
		return invalidCommand("creationSnapshot.vms에는 VM이 하나 이상 필요합니다")
	}
	if s.WorkspaceVMKey == "" {
		return invalidCommand("creationSnapshot.workspaceVmKey가 필요합니다")
	}
	workspaceFound := false
	for i, vm := range s.VMs {
		if vm.VMKey == "" || vm.Role == "" {
			return invalidCommand("creationSnapshot.vms[%d]에 vmKey와 role이 필요합니다", i)
		}
		if vm.InstanceIndex < 0 {
			return invalidCommand("creationSnapshot.vms[%d].instanceIndex는 0 이상이어야 합니다", i)
		}
		if vm.ImageID == "" || vm.FlavorID == "" {
			return invalidCommand("creationSnapshot.vms[%d]에 imageId와 flavorId가 필요합니다", i)
		}
		if vm.ImageRef != "" || vm.FlavorRef != "" {
			return invalidCommand("creationSnapshot.vms[%d]의 imageRef/flavorRef는 계약에 없는 field입니다", i)
		}
		if vm.FlavorSpec == nil {
			return invalidCommand("creationSnapshot.vms[%d].flavorSpec이 필요합니다", i)
		}
		if vm.FlavorSpec.VCPUs < 1 || vm.FlavorSpec.RAMMiB < 1 || vm.FlavorSpec.DiskGiB < 0 {
			return invalidCommand("creationSnapshot.vms[%d].flavorSpec 값이 범위를 벗어났습니다", i)
		}
		if vm.VMKey == s.WorkspaceVMKey {
			workspaceFound = true
		}
	}
	if !workspaceFound {
		return invalidCommand("creationSnapshot.workspaceVmKey가 vms에 없습니다")
	}
	if s.StartupScript != nil && !startupScriptDigest.MatchString(s.StartupScript.SHA256) {
		return invalidCommand("creationSnapshot.startupScript.sha256은 64자리 hex여야 합니다")
	}
	return nil
}

// validateReconcileRequest는 ReconcileRequestMessage의 required 조건을 확인한다. knownResources는 빈 목록이어도 된다.
func validateReconcileRequest(req ReconcileRequest) error {
	if err := validateCorrelation(req.Correlation); err != nil {
		return err
	}
	return validateResourceRefs(req.Payload.KnownResources, "knownResources")
}
