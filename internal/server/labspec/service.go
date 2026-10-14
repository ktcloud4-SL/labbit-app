// Package labspec는 LabSpec 정의의 목록·상세·생성·수정 use case다.
//
// LabSpec 저장은 정의만 영속한다. Operation, ProviderResource, Connector 호출을 시작하지 않으며 이 package는 그 경계를 import하지 않는다.
// LabSpec은 Class에 속하지 않고 Organization과 owner User에 속한다(D-11).
//
// 권한(D-11, 기능 명세 F-LAB-01):
//   - LabSpec을 작성하는 사용자는 강사다. 강사는 같은 Organization의 어떤 Class에서든 INSTRUCTOR ClassMembership을 가진 User다.
//     users.organization_role(ADMIN|MEMBER)은 이 판정에 쓰지 않는다.
//   - 목록·상세: 자신이 owner인 LabSpec만 볼 수 있다. D-11은 Organization 단위 공유를 후속 범위로 두었으므로 같은 Organization의
//     다른 강사 LabSpec도 볼 수 없다(ErrForbidden). 다른 Organization의 LabSpec은 존재하지 않는 것과 같다(ErrNotFound).
//   - 생성: 강사만. owner는 요청한 사용자다.
//   - 수정: 강사이면서 owner인 사용자만. 공동 편집과 Organization 단위 공유는 없다.
//
// HTTP status와 Problem Details는 알지 못한다.
package labspec

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

var (
	// ErrNotFound는 LabSpec이 없거나 다른 Organization의 것이다. 서버가 발급하지 않은 형식의 ID도 같다.
	ErrNotFound = errors.New("labspec: 찾을 수 없음")
	// ErrForbidden은 같은 Organization의 LabSpec이지만 현재 사용자가 보거나 수정할 수 없거나, 생성 권한이 없음을 나타낸다.
	ErrForbidden = errors.New("labspec: 권한 없음")
	// ErrStaleRevision은 If-Match가 현재 revision과 다름을 나타낸다. 아무것도 바꾸지 않았다.
	ErrStaleRevision = errors.New("labspec: revision 불일치")
	// ErrInvalid는 형식은 맞지만 의미 검증을 통과하지 못한 정의다. 구체 사유는 *ValidationError로 꺼낸다.
	ErrInvalid = errors.New("labspec: 의미 검증 실패")
	// ErrInconsistentData는 저장소가 반환한 관계가 서로 모순됨을 나타낸다. 권한 상태로 해석하지 않는다(fail closed).
	ErrInconsistentData = errors.New("labspec: 저장된 관계가 서로 모순됨")
)

// Store는 Service가 사용하는 persistence 경계다. postgres.Store가 구현한다.
type Store interface {
	repository.Transactor
	repository.ClassMemberRepository
	repository.LabSpecRepository
}

// Service는 LabSpec use case다.
type Service struct {
	store Store
	newID func() uuid.UUID
}

func NewService(store Store) *Service {
	return &Service{store: store, newID: uuid.New}
}

// Summary는 목록의 LabSpec이다.
type Summary struct {
	ID          uuid.UUID
	OwnerUserID uuid.UUID
	Name        string
	Description *string
}

// VM은 저장된 VM Role이다. ImageRef와 SizeRef는 Organization의 Image/Flavor mapping ID다.
type VM struct {
	Role     string
	Name     *string
	ImageRef string
	SizeRef  string
	Count    int64
}

// WorkspaceVM은 Editor와 기본 Preview의 기준 VM 선택이다.
type WorkspaceVM struct {
	Role          string
	InstanceIndex int64
}

// Definition은 저장된 LabSpec 상세와 그 revision이다. VMs는 role 순서다.
type Definition struct {
	Summary
	Revision         int64
	VMs              []VM
	WorkspaceVM      WorkspaceVM
	InternetOutbound bool
	StartupScript    *string
}

// Write는 생성·수정 입력이다. 형식(필수 field, 타입, 최소 길이·개수, 정수 하한)은 transport가 이미 확인했다고 가정하지 않고
// 의미 규칙과 함께 다시 검증한다.
type Write struct {
	Name             string
	Description      *string
	VMs              []VM
	WorkspaceVM      WorkspaceVM
	InternetOutbound bool
	StartupScript    *string
}

// List는 user가 owner인 LabSpec을 이름, ID 순서로 반환한다. 없으면 빈 목록이며 오류가 아니다.
func (s *Service) List(ctx context.Context, user repository.User) ([]Summary, error) {
	items, err := s.store.LabSpecSummariesByOwner(ctx, user.OrganizationID, user.ID)
	if err != nil {
		return nil, fmt.Errorf("labspec: 목록 조회: %w", err)
	}
	out := make([]Summary, 0, len(items))
	for _, item := range items {
		if item.OrganizationID != user.OrganizationID || item.OwnerUserID != user.ID {
			return nil, ErrInconsistentData
		}
		out = append(out, Summary{ID: item.ID, OwnerUserID: item.OwnerUserID, Name: item.Name, Description: item.Description})
	}
	return out, nil
}

// Get은 user가 owner인 LabSpec 상세를 반환한다. 다른 Organization의 것이면 ErrNotFound, 같은 Organization의 다른 사용자 것이면
// ErrForbidden이다. 조회는 강사 Membership을 요구하지 않는다(Membership을 잃은 owner도 자신의 정의를 볼 수 있다).
func (s *Service) Get(ctx context.Context, user repository.User, labSpecID string) (Definition, error) {
	id, ok := parseID(labSpecID)
	if !ok {
		return Definition{}, ErrNotFound
	}
	spec, err := s.store.LabSpecByID(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return Definition{}, ErrNotFound
	}
	if err != nil {
		return Definition{}, fmt.Errorf("labspec: 상세 조회: %w", err)
	}
	if spec.OrganizationID != user.OrganizationID {
		return Definition{}, ErrNotFound
	}
	if spec.OwnerUserID != user.ID {
		return Definition{}, ErrForbidden
	}
	return toDefinition(spec)
}

// Create는 user를 owner로 하는 revision 1의 LabSpec을 저장한다.
func (s *Service) Create(ctx context.Context, user repository.User, in Write) (Definition, error) {
	var created repository.LabSpec
	err := s.store.WithinTransaction(ctx, func(ctx context.Context, repos repository.Repositories) error {
		if err := requireInstructor(ctx, repos, user); err != nil {
			return err
		}
		def, err := resolve(ctx, repos, user.OrganizationID, in, s.newID)
		if err != nil {
			return err
		}
		created, err = repos.CreateLabSpec(ctx, repository.NewLabSpec{
			ID: s.newID(), OrganizationID: user.OrganizationID, OwnerUserID: user.ID, LabSpecDefinition: def,
		})
		if err != nil {
			return fmt.Errorf("labspec: 저장: %w", err)
		}
		return nil
	})
	if err != nil {
		return Definition{}, err
	}
	return toDefinition(created)
}

// Update는 ifMatch가 현재 revision일 때만 LabSpec 본문과 VM Role을 한 transaction에서 바꾸고 revision을 1 올린다.
//
// ifMatch는 ETag에서 따옴표를 벗긴 값이다. 판정 순서는 존재(ErrNotFound) → 권한(ErrForbidden) → revision(ErrStaleRevision)
// → 의미 검증(ErrInvalid)이며 어느 단계에서 실패해도 아무것도 바꾸지 않는다. LabSpec row를 잠근 채 revision을 확인하므로 같은
// revision을 가진 동시 수정 중 하나만 성공하고 나머지는 ErrStaleRevision이다.
func (s *Service) Update(ctx context.Context, user repository.User, labSpecID, ifMatch string, in Write) (Definition, error) {
	id, ok := parseID(labSpecID)
	if !ok {
		return Definition{}, ErrNotFound
	}
	var updated repository.LabSpec
	err := s.store.WithinTransaction(ctx, func(ctx context.Context, repos repository.Repositories) error {
		current, err := repos.LabSpecForUpdate(ctx, id)
		if errors.Is(err, repository.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("labspec: 수정 대상 조회: %w", err)
		}
		if current.OrganizationID != user.OrganizationID {
			return ErrNotFound
		}
		if current.OwnerUserID != user.ID {
			return ErrForbidden
		}
		if err := requireInstructor(ctx, repos, user); err != nil {
			return err
		}
		if ifMatch != strconv.FormatInt(current.Revision, 10) {
			return ErrStaleRevision
		}
		def, err := resolve(ctx, repos, user.OrganizationID, in, s.newID)
		if err != nil {
			return err
		}
		var applied bool
		updated, applied, err = repos.UpdateLabSpec(ctx, repository.LabSpecUpdate{
			ID: id, OrganizationID: user.OrganizationID, ExpectedRevision: current.Revision, LabSpecDefinition: def,
		})
		if err != nil {
			return fmt.Errorf("labspec: 수정: %w", err)
		}
		if !applied {
			// row를 잠근 뒤 확인했으므로 일어날 수 없다. 덮어쓰지 않았으니 stale로 성공을 가장하지 않고 내부 오류로 본다.
			return ErrInconsistentData
		}
		return nil
	})
	if err != nil {
		return Definition{}, err
	}
	return toDefinition(updated)
}

func requireInstructor(ctx context.Context, repos repository.Repositories, user repository.User) error {
	instructor, err := repos.HasClassRole(ctx, user.ID, repository.ClassRoleInstructor)
	if err != nil {
		return fmt.Errorf("labspec: 강사 여부 조회: %w", err)
	}
	if !instructor {
		return ErrForbidden
	}
	return nil
}

func toDefinition(spec repository.LabSpec) (Definition, error) {
	def := Definition{
		Summary: Summary{
			ID: spec.ID, OwnerUserID: spec.OwnerUserID, Name: spec.Name, Description: spec.Description,
		},
		Revision:         spec.Revision,
		WorkspaceVM:      WorkspaceVM{Role: spec.WorkspaceRole, InstanceIndex: spec.WorkspaceInstanceIndex},
		InternetOutbound: spec.InternetOutbound,
		StartupScript:    spec.StartupScript,
		VMs:              make([]VM, 0, len(spec.VMRoles)),
	}
	if spec.Revision < 1 || len(spec.VMRoles) == 0 {
		return Definition{}, ErrInconsistentData
	}
	for _, r := range spec.VMRoles {
		def.VMs = append(def.VMs, VM{
			Role: r.Role, Name: r.DisplayName, ImageRef: r.ImageMappingID.String(), SizeRef: r.FlavorMappingID.String(), Count: r.VMCount,
		})
	}
	return def, nil
}

// parseID는 이 서버가 발급하는 canonical UUID 문자열만 받아들인다. 하나의 리소스가 여러 ID 표기로 보이지 않게 한다.
func parseID(raw string) (uuid.UUID, bool) {
	id, err := uuid.Parse(raw)
	if err != nil || id.String() != raw {
		return uuid.UUID{}, false
	}
	return id, true
}
