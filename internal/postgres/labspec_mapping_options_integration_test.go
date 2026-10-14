//go:build integration

package postgres_test

import (
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

func TestActiveMappingOptionsOrganizationActivityAndOrdering(t *testing.T) {
	e := newLabSpecEnv(t)
	foreign := bootstrapSample(t, e.db, 2)
	insertConnectionWithMappings(t, e.db, foreign.OrganizationID, "foreign")
	// 비활성 Image/Flavor와 연결 상태는 독립적이다.
	mustExec(t, e.db, `UPDATE provider_image_mappings SET is_active = false WHERE id = $1`, e.otherImage)
	mustExec(t, e.db, `UPDATE provider_flavor_mappings SET is_active = false WHERE id = $1`, e.otherFlavr)
	mustExec(t, e.db, `UPDATE provider_connections SET status = 'UNVERIFIED' WHERE id = $1`, e.conn)
	// 표시 이름이 같아도 Mapping ID로 순서가 결정된다. 삽입 순서는 의도적으로 반대다.
	low := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	high := uuid.MustParse("00000000-0000-4000-8000-000000000002")
	for _, id := range []uuid.UUID{high, low} {
		mustExec(t, e.db, `INSERT INTO provider_image_mappings
			(id, organization_id, provider_connection_id, provider_image_id, display_name)
			VALUES ($1, $2, $3, $4, 'Alpha')`, id, e.s.OrganizationID, e.conn, "fixture-image-"+id.String())
		mustExec(t, e.db, `INSERT INTO provider_flavor_mappings
			(id, organization_id, provider_connection_id, provider_flavor_id, display_name, vcpus, ram_mib, disk_gib)
			VALUES ($1, $2, $3, $4, 'Alpha', 1, 1024, 0)`, id, e.s.OrganizationID, e.conn, "fixture-flavor-"+id.String())
	}
	images, err := e.db.store.ActiveImageMappingOptions(t.Context(), e.s.OrganizationID)
	wantImages := []repository.ImageMappingOption{
		{ID: low, ProviderConnectionID: e.conn, DisplayName: "Alpha"},
		{ID: high, ProviderConnectionID: e.conn, DisplayName: "Alpha"},
		{ID: e.image, ProviderConnectionID: e.conn, DisplayName: "Ubuntu"},
	}
	if err != nil || !reflect.DeepEqual(images, wantImages) {
		t.Fatalf("images = %+v, %v, want %+v", images, err, wantImages)
	}
	flavors, err := e.db.store.ActiveFlavorMappingOptions(t.Context(), e.s.OrganizationID)
	wantFlavors := []repository.FlavorMappingOption{
		{ID: low, ProviderConnectionID: e.conn, DisplayName: "Alpha", VCPUs: 1, RAMMiB: 1024, DiskGiB: 0},
		{ID: high, ProviderConnectionID: e.conn, DisplayName: "Alpha", VCPUs: 1, RAMMiB: 1024, DiskGiB: 0},
		{ID: e.flavor, ProviderConnectionID: e.conn, DisplayName: "Medium", VCPUs: 2, RAMMiB: 4096, DiskGiB: 20},
	}
	if err != nil || !reflect.DeepEqual(flavors, wantFlavors) {
		t.Fatalf("flavors = %+v, %v, want %+v", flavors, err, wantFlavors)
	}
	for _, org := range []uuid.UUID{uuid.New(), e.s.OrganizationID} {
		if org == e.s.OrganizationID {
			mustExec(t, e.db, `UPDATE provider_image_mappings SET is_active = false WHERE organization_id = $1`, org)
			mustExec(t, e.db, `UPDATE provider_flavor_mappings SET is_active = false WHERE organization_id = $1`, org)
		}
		images, imageErr := e.db.store.ActiveImageMappingOptions(t.Context(), org)
		flavors, flavorErr := e.db.store.ActiveFlavorMappingOptions(t.Context(), org)
		if imageErr != nil || flavorErr != nil || len(images) != 0 || len(flavors) != 0 {
			t.Fatalf("empty candidates = %+v, %+v, %v, %v", images, flavors, imageErr, flavorErr)
		}
	}
}
