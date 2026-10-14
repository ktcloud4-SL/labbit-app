//go:build integration

package httpapi_test

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

type mappingOptionsBody struct {
	Items []struct {
		ProviderConnectionID string `json:"providerConnectionId"`
		Images               []struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
		} `json:"images"`
		Flavors []struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
			VCPUs       int64  `json:"vcpus"`
			RAMMiB      int64  `json:"ramMiB"`
			DiskGiB     int64  `json:"diskGiB"`
		} `json:"flavors"`
	} `json:"items"`
}

func (f *labSpecFixture) mappingOptions(t *testing.T, token string) mappingOptionsBody {
	t.Helper()
	rec := f.send(http.MethodGet, "/api/v1/lab-specs/mapping-options", "", withCookie(token))
	if rec.Code != http.StatusOK {
		t.Fatalf("mapping-options = %d: %s", rec.Code, rec.Body.String())
	}
	var body mappingOptionsBody
	decodeStrict(t, rec, &body)
	return body
}

func TestMappingOptionsSelectionAndRevalidationAgainstPostgres(t *testing.T) {
	f := newLabSpecFixture(t)
	inactiveFlavor := uuid.New()
	f.exec(t, `INSERT INTO provider_flavor_mappings
		(id, organization_id, provider_connection_id, provider_flavor_id, display_name, vcpus, ram_mib, disk_gib, is_active)
		SELECT $1, organization_id, provider_connection_id, 'retired-flavor', 'Retired', 1, 1024, 0, false
		FROM provider_flavor_mappings WHERE id = $2`, inactiveFlavor, f.flavor1)
	got := f.mappingOptions(t, f.ireneToken)
	if len(got.Items) != 2 || !reflect.DeepEqual(got, f.mappingOptions(t, f.ivanToken)) || !reflect.DeepEqual(got, f.mappingOptions(t, f.ireneToken)) {
		t.Fatalf("unstable or owner-scoped candidates = %+v", got)
	}
	expected := map[string]string{f.image1.String(): f.flavor1.String(), f.image2.String(): f.flavor2.String()}
	for i, group := range got.Items {
		if len(group.Images) != 1 || len(group.Flavors) != 1 || group.Images[0].DisplayName != "Ubuntu" ||
			group.Flavors[0].DisplayName != "Medium" || group.Flavors[0].VCPUs != 2 || group.Flavors[0].RAMMiB != 4096 || group.Flavors[0].DiskGiB != 20 ||
			expected[group.Images[0].ID] != group.Flavors[0].ID || (i > 0 && got.Items[i-1].ProviderConnectionID >= group.ProviderConnectionID) {
			t.Fatalf("group = %+v", group)
		}
		// 응답의 실제 그룹 ID가 두 Mapping의 DB 연결과 일치해야 한다.
		if n := f.count(t, `SELECT count(*) FROM provider_image_mappings i JOIN provider_flavor_mappings f
			ON f.provider_connection_id = i.provider_connection_id
			WHERE i.id = $1 AND f.id = $2 AND i.provider_connection_id = $3`, group.Images[0].ID, group.Flavors[0].ID, group.ProviderConnectionID); n != 1 {
			t.Fatal("Mapping group does not match stored ProviderConnection")
		}
	}
	foreign := f.mappingOptions(t, f.bobToken)
	if len(foreign.Items) != 1 || len(foreign.Items[0].Images) != 1 || len(foreign.Items[0].Flavors) != 1 ||
		foreign.Items[0].Images[0].ID != f.imageB.String() || foreign.Items[0].Flavors[0].ID != f.flavorB.String() {
		t.Fatalf("foreign Organization candidates = %+v", foreign)
	}
	selected := got.Items[0]
	image := uuid.MustParse(selected.Images[0].ID)
	flavor := uuid.MustParse(selected.Flavors[0].ID)
	created := f.mustCreate(t, f.ireneToken, f.body("Selected", image, flavor))
	if created.VMs[0].ImageRef != image.String() || created.VMs[0].SizeRef != flavor.String() {
		t.Fatalf("saved selection = %+v", created)
	}
	// 다른 연결의 조합과 후보 조회 이후 비활성화된 Mapping은 기존 검증이 거절한다.
	mixed := f.createLabSpec(f.ireneToken, f.body("Mixed", image, uuid.MustParse(got.Items[1].Flavors[0].ID)))
	if mixed.Code != http.StatusUnprocessableEntity || problemCode(t, mixed) != "invalid_lab_spec" {
		t.Fatalf("mixed candidates = %d: %s", mixed.Code, mixed.Body.String())
	}
	f.exec(t, `UPDATE provider_image_mappings SET is_active = false WHERE id = $1`, image)
	create := f.createLabSpec(f.ireneToken, f.body("Stale", image, flavor))
	update := f.putLabSpec(f.ireneToken, created.ID, `"1"`, f.body("Stale", image, flavor))
	if create.Code != http.StatusUnprocessableEntity || update.Code != http.StatusUnprocessableEntity ||
		problemCode(t, create) != "invalid_lab_spec" || problemCode(t, update) != "invalid_lab_spec" {
		t.Fatalf("stale selection POST = %d, PUT = %d", create.Code, update.Code)
	}
	// 한쪽 후보만 남은 연결도 표시하고 없는 배열은 []다.
	after := f.mappingOptions(t, f.ireneToken)
	if len(after.Items) != 2 || after.Items[0].Images == nil || len(after.Items[0].Images) != 0 || len(after.Items[0].Flavors) != 1 {
		t.Fatalf("one-sided group = %+v", after)
	}
}

func TestMappingOptionsAuthenticationAuthorizationAndEmptyAgainstPostgres(t *testing.T) {
	f := newLabSpecFixture(t)
	for _, tt := range []struct {
		token  string
		status int
		code   string
	}{
		{"", http.StatusUnauthorized, "unauthenticated"},
		{"invalid-session", http.StatusUnauthorized, "unauthenticated"},
		{f.samToken, http.StatusForbidden, "forbidden"},
		{f.adminT, http.StatusForbidden, "forbidden"},
	} {
		rec := f.send(http.MethodGet, "/api/v1/lab-specs/mapping-options", "", withCookie(tt.token))
		if rec.Code != tt.status || problemCode(t, rec) != tt.code {
			t.Fatalf("auth boundary = %d: %s", rec.Code, rec.Body.String())
		}
	}
	f.exec(t, `DELETE FROM class_memberships WHERE user_id = $1`, f.irene)
	rec := f.send(http.MethodGet, "/api/v1/lab-specs/mapping-options", "", withCookie(f.ireneToken))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("former instructor = %d", rec.Code)
	}
	f.exec(t, `UPDATE provider_image_mappings SET is_active = false WHERE organization_id = $1`, f.orgA)
	f.exec(t, `UPDATE provider_flavor_mappings SET is_active = false WHERE organization_id = $1`, f.orgA)
	got := f.mappingOptions(t, f.ivanToken)
	if got.Items == nil || len(got.Items) != 0 {
		t.Fatalf("empty candidates = %+v", got)
	}
}
