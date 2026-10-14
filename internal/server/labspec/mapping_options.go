package labspec

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// MappingOptions는 ProviderConnection 하나에 속한 선택 후보다.
type MappingOptions struct {
	ProviderConnectionID uuid.UUID
	Images               []ImageOption
	Flavors              []FlavorOption
}

type ImageOption struct {
	ID          uuid.UUID
	DisplayName string
}

type FlavorOption struct {
	ID          uuid.UUID
	DisplayName string
	VCPUs       int64
	RAMMiB      int64
	DiskGiB     int64
}

// MappingOptions는 작성 권한을 확인하고 현재 Organization의 활성 후보를 연결별로 묶는다.
// 조회 결과는 예약이 아니며 생성·수정·Provision의 기존 검증을 대신하지 않는다.
func (s *Service) MappingOptions(ctx context.Context, user repository.User) ([]MappingOptions, error) {
	if err := requireInstructor(ctx, s.store, user); err != nil {
		return nil, err
	}
	images, err := s.store.ActiveImageMappingOptions(ctx, user.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("labspec: Image 후보 조회: %w", err)
	}
	flavors, err := s.store.ActiveFlavorMappingOptions(ctx, user.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("labspec: Flavor 후보 조회: %w", err)
	}
	groups := make(map[uuid.UUID]*MappingOptions)
	group := func(id uuid.UUID) *MappingOptions {
		if groups[id] == nil {
			groups[id] = &MappingOptions{ProviderConnectionID: id, Images: []ImageOption{}, Flavors: []FlavorOption{}}
		}
		return groups[id]
	}
	for _, image := range images {
		g := group(image.ProviderConnectionID)
		g.Images = append(g.Images, ImageOption{ID: image.ID, DisplayName: image.DisplayName})
	}
	for _, flavor := range flavors {
		g := group(flavor.ProviderConnectionID)
		g.Flavors = append(g.Flavors, FlavorOption{ID: flavor.ID, DisplayName: flavor.DisplayName,
			VCPUs: flavor.VCPUs, RAMMiB: flavor.RAMMiB, DiskGiB: flavor.DiskGiB})
	}
	out := make([]MappingOptions, 0, len(groups))
	for _, g := range groups {
		out = append(out, *g)
	}
	slices.SortFunc(out, func(a, b MappingOptions) int {
		return strings.Compare(a.ProviderConnectionID.String(), b.ProviderConnectionID.String())
	})
	return out, nil
}
