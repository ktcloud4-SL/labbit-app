package terminal

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// Target은 Browser가 TerminalSession을 만들 수 있는 LabInstance 안의 VM 하나다.
//
// VMKey는 Browser에게 opaque다. 서버가 immutable CreationSnapshot에서 읽은 값을 그대로 돌려주며, Browser는 이 값을 해석하거나
// role/instanceIndex로 만들지 않고 CreateInput.TargetVMKey로 그대로 되돌려 보낸다. Provider Server ID, Connector ID, private IP,
// 이미지·flavor 같은 resolve된 Provider 정보는 포함하지 않는다.
type Target struct {
	VMKey         string
	Role          string
	InstanceIndex int64
}

// TargetCatalog는 한 LabInstance에서 Terminal을 열 수 있는 VM 전체와 기본 선택 hint다.
type TargetCatalog struct {
	// Generation은 이 catalog를 판정한 시점의 LabInstance generation이다. 이후 TerminalSession 생성의 근거가 아니다.
	// Create는 항상 현재 generation을 다시 확인한다.
	Generation int64
	// WorkspaceVMKey는 Items 중 Workspace VM의 VMKey다. 기본 선택 hint일 뿐 유일한 target이라는 뜻이 아니다.
	WorkspaceVMKey string
	Items          []Target
}

// Targets는 현재 사용자의 LabInstance에서 Terminal을 열 수 있는 VM 목록을 반환한다.
//
// 목록의 원본은 그 LabExecution의 immutable resolved CreationSnapshot이다. 최신 LabSpec이나 Provider topology를 다시 해석하지 않는다.
// 권한은 Create와 같은 판정(authorizeLabInstance)이며, 강사나 Organization ADMIN이어도 본인 LabInstance가 아니면 허용하지 않는다.
//
// 이 목록은 논리적 선택지이지 VM이 지금 사용 가능하다는 보증이 아니다. 실제 생성 가능 여부는 Create가 현재 generation의
// ProviderResource로 다시 판단한다. LabInstance와 snapshot은 하나의 짧은 transaction(FOR SHARE) 안에서 읽어, 반환한
// generation이 이 판정의 값과 같도록 한다. 외부 I/O는 없다.
func (s *Service) Targets(ctx context.Context, user repository.User, labInstanceID string) (TargetCatalog, error) {
	labID, ok := parseID(labInstanceID)
	if !ok {
		return TargetCatalog{}, ErrNotFound
	}

	var catalog TargetCatalog
	err := s.store.WithinTransaction(ctx, func(ctx context.Context, repos repository.Repositories) error {
		lab, err := authorizeLabInstance(ctx, repos, user, labID)
		if err != nil {
			return err
		}
		catalog, err = snapshotTargets(ctx, repos, lab)
		return err
	})
	if err != nil {
		return TargetCatalog{}, err
	}
	return catalog, nil
}

// authorizeLabInstance는 transaction 안에서 LabInstance를 FOR SHARE로 잠그고 TerminalSession과 target 조회가 공유하는 제품 권한 경계를
// 판정한다. 하나라도 맞지 않으면 어떤 side effect도 없이 거절한다.
//
//   - 같은 Organization이고 LabInstance.user_id가 현재 사용자다. 강사가 학생 LabInstance를 여는 기능은 없다.
//   - 해당 Class의 현재 ClassMembership이 있고 저장된 관계가 서로 일치한다(모순이면 fail closed).
//   - LabInstance status가 READY다.
func authorizeLabInstance(ctx context.Context, repos repository.Repositories, user repository.User, labID uuid.UUID) (repository.LabInstance, error) {
	lab, err := repos.LabInstanceForShare(ctx, labID)
	if errors.Is(err, repository.ErrNotFound) {
		return repository.LabInstance{}, ErrNotFound
	}
	if err != nil {
		return repository.LabInstance{}, fmt.Errorf("terminal: LabInstance 조회: %w", err)
	}
	if lab.OrganizationID != user.OrganizationID || lab.UserID != user.ID {
		return repository.LabInstance{}, ErrForbidden
	}

	membership, err := repos.ClassMembership(ctx, lab.ClassID, user.ID)
	if errors.Is(err, repository.ErrNotFound) {
		return repository.LabInstance{}, ErrForbidden
	}
	if err != nil {
		return repository.LabInstance{}, fmt.Errorf("terminal: Membership 조회: %w", err)
	}
	if membership.ClassID != lab.ClassID || membership.UserID != user.ID ||
		membership.OrganizationID != lab.OrganizationID || !membership.Role.Valid() {
		return repository.LabInstance{}, ErrInconsistentData
	}

	if lab.Status != LabInstanceStatusReady {
		return repository.LabInstance{}, ErrLabInstanceNotReady
	}
	return lab, nil
}

// snapshotTargets는 lab의 immutable CreationSnapshot에서 Terminal target catalog를 만든다.
// 저장된 snapshot을 신뢰하지 않는다. 해석할 수 없거나 모순이면 일부만 반환하거나 값을 만들어 채우지 않고 ErrInconsistentData로 fail closed한다.
func snapshotTargets(ctx context.Context, repos repository.Repositories, lab repository.LabInstance) (TargetCatalog, error) {
	stored, err := repos.CreationSnapshotTargets(ctx, lab.ID)
	if errors.Is(err, repository.ErrNotFound) {
		// LabInstance는 DB 제약상 LabExecution의 CreationSnapshot을 가진다. 없으면 저장된 관계가 모순이다.
		return TargetCatalog{}, fmt.Errorf("%w: CreationSnapshot 없음", ErrInconsistentData)
	}
	if err != nil {
		return TargetCatalog{}, fmt.Errorf("terminal: CreationSnapshot target 조회: %w", err)
	}

	items, err := validateSnapshotTargets(stored)
	if err != nil {
		return TargetCatalog{}, err
	}
	return TargetCatalog{Generation: lab.Generation, WorkspaceVMKey: stored.WorkspaceVMKey, Items: items}, nil
}

// validateSnapshotTargets는 connector.schema.json의 CreationSnapshot과 ResolvedVmSpec이 요구하는 값
// (vms 1개 이상, vmKey와 role은 비어 있지 않음, instanceIndex는 0 이상, workspaceVmKey는 비어 있지 않고 vms 중 하나)을 확인하고
// Browser target의 identity가 모호해지지 않도록 vmKey 중복을 거절한다. 중복은 Schema에 없지만 같은 key로 서로 다른 VM을
// 가리킬 수 없다는 target catalog 자체의 조건이다.
// 오류 문구에는 snapshot의 값을 넣지 않고 위치만 넣는다.
func validateSnapshotTargets(stored repository.CreationSnapshotTargets) ([]Target, error) {
	if len(stored.VMs) == 0 {
		return nil, fmt.Errorf("%w: CreationSnapshot에 vms가 없음", ErrInconsistentData)
	}
	if stored.WorkspaceVMKey == "" {
		return nil, fmt.Errorf("%w: CreationSnapshot에 workspaceVmKey가 없음", ErrInconsistentData)
	}

	items := make([]Target, 0, len(stored.VMs))
	seen := make(map[string]struct{}, len(stored.VMs))
	workspaceFound := false
	for i, vm := range stored.VMs {
		switch {
		case vm.VMKey == "":
			return nil, fmt.Errorf("%w: vms[%d].vmKey가 비어 있음", ErrInconsistentData, i)
		case vm.Role == "":
			return nil, fmt.Errorf("%w: vms[%d].role이 비어 있음", ErrInconsistentData, i)
		case vm.InstanceIndex < 0:
			return nil, fmt.Errorf("%w: vms[%d].instanceIndex가 음수", ErrInconsistentData, i)
		}
		if _, dup := seen[vm.VMKey]; dup {
			return nil, fmt.Errorf("%w: vms[%d].vmKey가 중복됨", ErrInconsistentData, i)
		}
		seen[vm.VMKey] = struct{}{}
		if vm.VMKey == stored.WorkspaceVMKey {
			workspaceFound = true
		}
		items = append(items, Target{VMKey: vm.VMKey, Role: vm.Role, InstanceIndex: vm.InstanceIndex})
	}
	if !workspaceFound {
		return nil, fmt.Errorf("%w: workspaceVmKey가 vms에 없음", ErrInconsistentData)
	}
	return items, nil
}

// hasTarget은 catalog에 vmKey가 있는지 확인한다. Create가 Browser의 targetVmKey를 snapshot membership으로 다시 확인할 때 쓴다.
func (c TargetCatalog) hasTarget(vmKey string) bool {
	for _, item := range c.Items {
		if item.VMKey == vmKey {
			return true
		}
	}
	return false
}
