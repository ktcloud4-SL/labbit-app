//go:build integration

package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/bootstrap"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// labSpecFixture는 실제 PostgreSQL 위의 두 Organization이다.
//
//	Organization A (ProviderConnection conn1: image1/flavor1 활성, inactiveImage 비활성 / conn2: image2/flavor2)
//	  irene (MEMBER): A1 INSTRUCTOR
//	  ivan  (MEMBER): A2 INSTRUCTOR
//	  sam   (MEMBER): A1 STUDENT
//	  admin (ADMIN):  Membership 없음
//	Organization B (ProviderConnection connB: imageB/flavorB)
//	  bob   (MEMBER): B1 INSTRUCTOR
type labSpecFixture struct {
	*stack
	orgA, orgB                       uuid.UUID
	a1, a2, b1                       uuid.UUID
	irene, ivan, sam, admin, bob     uuid.UUID
	image1, flavor1, image2, flavor2 uuid.UUID
	inactiveImage, imageB, flavorB   uuid.UUID
	ireneToken, ivanToken, samToken  string
	adminT, bobToken                 string
}

func newLabSpecFixture(t *testing.T) *labSpecFixture {
	t.Helper()
	f := &labSpecFixture{
		orgA: uuid.New(), orgB: uuid.New(), a1: uuid.New(), a2: uuid.New(), b1: uuid.New(),
		irene: uuid.New(), ivan: uuid.New(), sam: uuid.New(), admin: uuid.New(), bob: uuid.New(),
	}
	f.stack = newStackWith(t, func(hash repository.PasswordHash) []bootstrap.Spec {
		user := func(id uuid.UUID, username string, role repository.OrganizationRole) bootstrap.User {
			return bootstrap.User{ID: id, Username: username, PasswordHash: hash, OrganizationRole: role}
		}
		return []bootstrap.Spec{
			{
				Organization: bootstrap.Organization{ID: f.orgA, Name: "Org A"},
				Users: []bootstrap.User{
					user(f.irene, "irene", repository.OrganizationRoleMember),
					user(f.ivan, "ivan", repository.OrganizationRoleMember),
					user(f.sam, "sam", repository.OrganizationRoleMember),
					user(f.admin, "admin", repository.OrganizationRoleAdmin),
				},
				Classes: []bootstrap.Class{{ID: f.a1, Name: "A1"}, {ID: f.a2, Name: "A2"}},
				Memberships: []bootstrap.Membership{
					{ClassID: f.a1, UserID: f.irene, Role: repository.ClassRoleInstructor},
					{ClassID: f.a2, UserID: f.ivan, Role: repository.ClassRoleInstructor},
					{ClassID: f.a1, UserID: f.sam, Role: repository.ClassRoleStudent},
				},
			},
			{
				Organization: bootstrap.Organization{ID: f.orgB, Name: "Org B"},
				Users:        []bootstrap.User{user(f.bob, "bob", repository.OrganizationRoleMember)},
				Classes:      []bootstrap.Class{{ID: f.b1, Name: "B1"}},
				Memberships:  []bootstrap.Membership{{ClassID: f.b1, UserID: f.bob, Role: repository.ClassRoleInstructor}},
			},
		}
	})
	f.image1, f.flavor1 = f.connection(t, f.orgA, "conn1")
	f.image2, f.flavor2 = f.connection(t, f.orgA, "conn2")
	f.imageB, f.flavorB = f.connection(t, f.orgB, "connB")
	f.inactiveImage = uuid.New()
	// conn1과 같은 ProviderConnection의 비활성 mapping
	f.exec(t, `INSERT INTO provider_image_mappings (id, organization_id, provider_connection_id, provider_image_id, display_name, is_active)
		SELECT $1, organization_id, provider_connection_id, 'img-retired', 'Retired', false FROM provider_image_mappings WHERE id = $2`,
		f.inactiveImage, f.image1)

	f.ireneToken = f.loginAs(t, "irene")
	f.ivanToken = f.loginAs(t, "ivan")
	f.samToken = f.loginAs(t, "sam")
	f.adminT = f.loginAs(t, "admin")
	f.bobToken = f.loginAs(t, "bob")
	return f
}

// connection은 Connector, ProviderConnection, 활성 Image/Flavor mapping을 만든다. 운영에서는 기관 관리 기능이 만드는 데이터다.
func (f *labSpecFixture) connection(t *testing.T, orgID uuid.UUID, name string) (image, flavor uuid.UUID) {
	t.Helper()
	connector, conn := uuid.New(), uuid.New()
	image, flavor = uuid.New(), uuid.New()
	f.exec(t, `INSERT INTO connectors (id, organization_id, name) VALUES ($1, $2, $3)`, connector, orgID, name)
	f.exec(t, `INSERT INTO provider_connections (id, organization_id, connector_id, name) VALUES ($1, $2, $3, $4)`, conn, orgID, connector, name)
	f.exec(t, `INSERT INTO provider_image_mappings (id, organization_id, provider_connection_id, provider_image_id, display_name)
		VALUES ($1, $2, $3, $4, 'Ubuntu')`, image, orgID, conn, "img-"+name)
	f.exec(t, `INSERT INTO provider_flavor_mappings (id, organization_id, provider_connection_id, provider_flavor_id, display_name, vcpus, ram_mib, disk_gib)
		VALUES ($1, $2, $3, $4, 'Medium', 2, 4096, 20)`, flavor, orgID, conn, "flv-"+name)
	return image, flavor
}

func (f *labSpecFixture) body(name string, image, flavor uuid.UUID) string {
	return fmt.Sprintf(`{"name":%q,"description":"desc","vms":[`+
		`{"role":"worker","imageRef":%q,"sizeRef":%q,"count":2},`+
		`{"role":"control","name":"Control Plane","imageRef":%q,"sizeRef":%q,"count":1}],`+
		`"workspaceVm":{"role":"worker","instanceIndex":1},"internetOutbound":true,"startupScript":"#!/bin/sh"}`,
		name, image, flavor, image, flavor)
}

func (s *stack) createLabSpec(token, body string) *httptest.ResponseRecorder {
	return s.send(http.MethodPost, "/api/v1/lab-specs", body,
		withCookie(token), withHeader("Origin", trustedOrigin), withHeader("Content-Type", "application/json"))
}

func (s *stack) getLabSpec(token, id string) *httptest.ResponseRecorder {
	return s.send(http.MethodGet, "/api/v1/lab-specs/"+id, "", withCookie(token))
}

func (s *stack) putLabSpec(token, id, etag, body string) *httptest.ResponseRecorder {
	return s.send(http.MethodPut, "/api/v1/lab-specs/"+id, body,
		withCookie(token), withHeader("Origin", trustedOrigin), withHeader("Content-Type", "application/json"), withHeader("If-Match", etag))
}

// labSpecBody는 OpenAPI LabSpec이다. 알 수 없는 field가 있으면 decodeStrict가 실패한다.
type labSpecBody struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	OwnerUserID string  `json:"ownerUserId"`
	VMs         []struct {
		Role     string  `json:"role"`
		Name     *string `json:"name"`
		ImageRef string  `json:"imageRef"`
		SizeRef  string  `json:"sizeRef"`
		Count    int64   `json:"count"`
	} `json:"vms"`
	WorkspaceVM struct {
		Role          string `json:"role"`
		InstanceIndex int64  `json:"instanceIndex"`
	} `json:"workspaceVm"`
	InternetOutbound bool    `json:"internetOutbound"`
	StartupScript    *string `json:"startupScript"`
}

func decodeLabSpec(t *testing.T, rec *httptest.ResponseRecorder) labSpecBody {
	t.Helper()
	var body labSpecBody
	decodeStrict(t, rec, &body)
	return body
}

func (f *labSpecFixture) mustCreate(t *testing.T, token, body string) labSpecBody {
	t.Helper()
	rec := f.createLabSpec(token, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /lab-specs = %d: %s", rec.Code, rec.Body.String())
	}
	return decodeLabSpec(t, rec)
}

func TestLabSpecCreateGetUpdateAgainstPostgres(t *testing.T) {
	f := newLabSpecFixture(t)

	rec := f.createLabSpec(f.ireneToken, f.body("Kubernetes", f.image1, f.flavor1))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST = %d: %s", rec.Code, rec.Body.String())
	}
	created := decodeLabSpec(t, rec)
	if loc := rec.Header().Get("Location"); loc != "/api/v1/lab-specs/"+created.ID {
		t.Fatalf("Location = %q, id = %s", loc, created.ID)
	}
	if created.OwnerUserID != f.irene.String() || created.Name != "Kubernetes" || len(created.VMs) != 2 ||
		created.VMs[0].Role != "control" || created.VMs[0].ImageRef != f.image1.String() || created.VMs[1].SizeRef != f.flavor1.String() ||
		created.WorkspaceVM.Role != "worker" || created.WorkspaceVM.InstanceIndex != 1 || !created.InternetOutbound || *created.StartupScript != "#!/bin/sh" {
		t.Fatalf("POST body = %+v", created)
	}
	// 정의만 저장한다. LabExecution, Operation, ProviderResource는 생기지 않는다.
	for _, table := range []string{"lab_executions", "creation_snapshots", "lab_instances", "operations", "operation_items", "provider_resources"} {
		if n := f.count(t, `SELECT count(*) FROM `+table); n != 0 {
			t.Fatalf("LabSpec 저장 뒤 %s = %d, want 0", table, n)
		}
	}

	get := f.getLabSpec(f.ireneToken, created.ID)
	if get.Code != http.StatusOK || get.Header().Get("ETag") != `"1"` {
		t.Fatalf("GET = %d ETag %q: %s", get.Code, get.Header().Get("ETag"), get.Body.String())
	}
	if got := decodeLabSpec(t, get); got.ID != created.ID || len(got.VMs) != 2 {
		t.Fatalf("GET body = %+v", got)
	}

	// 수정: conn2 mapping 하나의 role로 바꾸고 description/startupScript를 지운다.
	update := fmt.Sprintf(`{"name":"Renamed","vms":[{"role":"db","imageRef":%q,"sizeRef":%q,"count":3}],`+
		`"workspaceVm":{"role":"db","instanceIndex":2},"internetOutbound":false}`, f.image2, f.flavor2)
	put := f.putLabSpec(f.ireneToken, created.ID, `"1"`, update)
	if put.Code != http.StatusOK || put.Header().Get("ETag") != `"2"` {
		t.Fatalf("PUT = %d ETag %q: %s", put.Code, put.Header().Get("ETag"), put.Body.String())
	}
	updated := decodeLabSpec(t, put)
	if updated.Name != "Renamed" || updated.Description != nil || updated.StartupScript != nil || updated.InternetOutbound ||
		len(updated.VMs) != 1 || updated.VMs[0].Count != 3 || updated.VMs[0].ImageRef != f.image2.String() || updated.OwnerUserID != f.irene.String() {
		t.Fatalf("PUT body = %+v", updated)
	}
	if n := f.count(t, `SELECT count(*) FROM lab_spec_vm_roles WHERE lab_spec_id = $1`, created.ID); n != 1 {
		t.Fatalf("VM Role rows = %d, want 1", n)
	}

	// 이전 ETag로 다시 수정하면 412이고 현재 값은 그대로다.
	stale := f.putLabSpec(f.ireneToken, created.ID, `"1"`, f.body("Clobber", f.image1, f.flavor1))
	if stale.Code != http.StatusPreconditionFailed || problemCode(t, stale) != "stale_revision" {
		t.Fatalf("stale PUT = %d: %s", stale.Code, stale.Body.String())
	}
	again := decodeLabSpec(t, f.getLabSpec(f.ireneToken, created.ID))
	if again.Name != "Renamed" || len(again.VMs) != 1 {
		t.Fatalf("stale PUT 뒤 = %+v", again)
	}
}

func TestLabSpecAuthorizationBoundariesAgainstPostgres(t *testing.T) {
	f := newLabSpecFixture(t)
	irenes := f.mustCreate(t, f.ireneToken, f.body("Irene Spec", f.image1, f.flavor1))
	ivans := f.mustCreate(t, f.ivanToken, f.body("Ivan Spec", f.image2, f.flavor2))
	bobs := f.mustCreate(t, f.bobToken, f.body("Bob Spec", f.imageB, f.flavorB))

	listIDs := func(token string) []string {
		rec := f.send(http.MethodGet, "/api/v1/lab-specs", "", withCookie(token))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /lab-specs = %d", rec.Code)
		}
		var list struct {
			Items []struct {
				ID          string  `json:"id"`
				Name        string  `json:"name"`
				Description *string `json:"description"`
				OwnerUserID string  `json:"ownerUserId"`
			} `json:"items"`
		}
		decodeStrict(t, rec, &list)
		if list.Items == nil {
			t.Fatalf("items가 null입니다")
		}
		ids := make([]string, 0, len(list.Items))
		for _, item := range list.Items {
			ids = append(ids, item.ID)
		}
		return ids
	}
	// 목록은 자신이 owner인 LabSpec만이다. 같은 Organization의 다른 강사 것(D-11: Organization 단위 공유는 후속 범위)과
	// 다른 Organization의 것은 없다.
	if got := listIDs(f.ireneToken); len(got) != 1 || got[0] != irenes.ID {
		t.Fatalf("irene 목록 = %v, want [%s]", got, irenes.ID)
	}
	if got := listIDs(f.ivanToken); len(got) != 1 || got[0] != ivans.ID {
		t.Fatalf("ivan 목록 = %v, want [%s]", got, ivans.ID)
	}
	if got := listIDs(f.bobToken); len(got) != 1 || got[0] != bobs.ID {
		t.Fatalf("bob 목록 = %v", got)
	}
	// 학생과 Membership 없는 ADMIN은 볼 수 있는 LabSpec이 없다.
	for _, token := range []string{f.samToken, f.adminT} {
		if got := listIDs(token); len(got) != 0 {
			t.Fatalf("비강사 목록 = %v, want []", got)
		}
	}

	tests := []struct {
		name   string
		rec    *httptest.ResponseRecorder
		status int
		code   string
	}{
		{"owner GET", f.getLabSpec(f.ireneToken, irenes.ID), http.StatusOK, ""},
		{"같은 Organization 다른 강사 GET", f.getLabSpec(f.ivanToken, irenes.ID), http.StatusForbidden, "forbidden"},
		{"학생 GET", f.getLabSpec(f.samToken, irenes.ID), http.StatusForbidden, "forbidden"},
		{"ADMIN GET", f.getLabSpec(f.adminT, irenes.ID), http.StatusForbidden, "forbidden"},
		{"다른 Organization GET", f.getLabSpec(f.bobToken, irenes.ID), http.StatusNotFound, "not_found"},
		{"없는 ID GET", f.getLabSpec(f.ireneToken, uuid.NewString()), http.StatusNotFound, "not_found"},
		{"형식이 다른 ID GET", f.getLabSpec(f.ireneToken, "not-an-id"), http.StatusNotFound, "not_found"},
		{"다른 강사 PUT", f.putLabSpec(f.ivanToken, irenes.ID, `"1"`, f.body("Hijack", f.image1, f.flavor1)), http.StatusForbidden, "forbidden"},
		{"학생 PUT", f.putLabSpec(f.samToken, irenes.ID, `"1"`, f.body("Hijack", f.image1, f.flavor1)), http.StatusForbidden, "forbidden"},
		{"다른 Organization PUT", f.putLabSpec(f.bobToken, irenes.ID, `"1"`, f.body("Hijack", f.imageB, f.flavorB)), http.StatusNotFound, "not_found"},
		{"학생 POST", f.createLabSpec(f.samToken, f.body("S", f.image1, f.flavor1)), http.StatusForbidden, "forbidden"},
		{"ADMIN POST", f.createLabSpec(f.adminT, f.body("A", f.image1, f.flavor1)), http.StatusForbidden, "forbidden"},
	}
	for _, tt := range tests {
		if tt.rec.Code != tt.status {
			t.Fatalf("%s = %d, want %d: %s", tt.name, tt.rec.Code, tt.status, tt.rec.Body.String())
		}
		if tt.code != "" && problemCode(t, tt.rec) != tt.code {
			t.Fatalf("%s code = %s, want %s", tt.name, tt.rec.Body.String(), tt.code)
		}
	}
	// 거절된 요청은 아무것도 바꾸지 않았다.
	if got := decodeLabSpec(t, f.getLabSpec(f.ireneToken, irenes.ID)); got.Name != "Irene Spec" {
		t.Fatalf("거절 뒤 = %+v", got)
	}
	if n := f.count(t, `SELECT count(*) FROM lab_specs`); n != 3 {
		t.Fatalf("lab_specs = %d, want 3", n)
	}
	if n := f.count(t, `SELECT max(revision) FROM lab_specs`); n != 1 {
		t.Fatalf("max revision = %d, want 1", n)
	}

	unauth := f.send(http.MethodGet, "/api/v1/lab-specs", "")
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("Cookie 없는 GET = %d", unauth.Code)
	}
}

func TestLabSpecSemanticValidationAgainstPostgres(t *testing.T) {
	f := newLabSpecFixture(t)
	spec := f.mustCreate(t, f.ireneToken, f.body("Valid", f.image1, f.flavor1))
	mixed := fmt.Sprintf(`{"name":"Mixed","vms":[{"role":"a","imageRef":%q,"sizeRef":%q,"count":1},{"role":"b","imageRef":%q,"sizeRef":%q,"count":1}],`+
		`"workspaceVm":{"role":"a","instanceIndex":0},"internetOutbound":true}`, f.image1, f.flavor1, f.image2, f.flavor2)

	tests := map[string]string{
		"다른 Organization mapping":      f.body("Cross", f.imageB, f.flavorB),
		"비활성 Image":                    f.body("Retired", f.inactiveImage, f.flavor1),
		"없는 mapping":                   f.body("Missing", uuid.New(), f.flavor1),
		"논리 이름 imageRef":               strings.ReplaceAll(f.body("Name", f.image1, f.flavor1), f.image1.String(), "ubuntu-24.04"),
		"ProviderConnection 혼합":        mixed,
		"workspace index = count":      strings.Replace(f.body("Index", f.image1, f.flavor1), `"instanceIndex":1`, `"instanceIndex":2`, 1),
		"중복 role":                      strings.Replace(f.body("Dup", f.image1, f.flavor1), `"role":"control"`, `"role":"worker"`, 1),
		"공백 name":                      f.body("   ", f.image1, f.flavor1),
		"count가 PostgreSQL integer 초과": strings.Replace(f.body("Huge", f.image1, f.flavor1), `"count":2`, `"count":2147483648`, 1),
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			post := f.createLabSpec(f.ireneToken, body)
			if post.Code != http.StatusUnprocessableEntity || problemCode(t, post) != "invalid_lab_spec" {
				t.Fatalf("POST = %d: %s", post.Code, post.Body.String())
			}
			put := f.putLabSpec(f.ireneToken, spec.ID, `"1"`, body)
			if put.Code != http.StatusUnprocessableEntity || problemCode(t, put) != "invalid_lab_spec" {
				t.Fatalf("PUT = %d: %s", put.Code, put.Body.String())
			}
		})
	}
	if n := f.count(t, `SELECT count(*) FROM lab_specs`); n != 1 {
		t.Fatalf("lab_specs = %d, want 1", n)
	}
	if got := decodeLabSpec(t, f.getLabSpec(f.ireneToken, spec.ID)); got.Name != "Valid" || len(got.VMs) != 2 {
		t.Fatalf("검증 실패 뒤 = %+v", got)
	}
	if n := f.count(t, `SELECT revision FROM lab_specs WHERE id = $1`, spec.ID); n != 1 {
		t.Fatalf("revision = %d, want 1", n)
	}
}

// 같은 ETag로 동시에 보낸 수정 중 정확히 하나만 성공하고 나머지는 412이며, 최종 상태는 성공한 요청 하나의 정의 전체다.
func TestConcurrentLabSpecUpdatesExactlyOneWinsAgainstPostgres(t *testing.T) {
	f := newLabSpecFixture(t)
	spec := f.mustCreate(t, f.ireneToken, f.body("Original", f.image1, f.flavor1))

	const editors = 8
	var wg sync.WaitGroup
	codes := make([]int, editors)
	start := make(chan struct{})
	for i := range editors {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			// 각 수정은 VM Role 개수까지 다르게 해 부분 반영을 구분할 수 있게 한다.
			var vms []string
			for j := 0; j <= i; j++ {
				vms = append(vms, fmt.Sprintf(`{"role":"r%d","imageRef":%q,"sizeRef":%q,"count":1}`, j, f.image1, f.flavor1))
			}
			body := fmt.Sprintf(`{"name":"Editor %d","vms":[%s],"workspaceVm":{"role":"r0","instanceIndex":0},"internetOutbound":false}`,
				i, strings.Join(vms, ","))
			codes[i] = f.putLabSpec(f.ireneToken, spec.ID, `"1"`, body).Code
		}()
	}
	close(start)
	wg.Wait()

	winner := -1
	for i, code := range codes {
		switch code {
		case http.StatusOK:
			if winner != -1 {
				t.Fatalf("둘 이상이 성공했습니다: %v", codes)
			}
			winner = i
		case http.StatusPreconditionFailed:
		default:
			t.Fatalf("예상하지 못한 status: %v", codes)
		}
	}
	if winner == -1 {
		t.Fatalf("아무도 성공하지 못했습니다: %v", codes)
	}
	get := f.getLabSpec(f.ireneToken, spec.ID)
	got := decodeLabSpec(t, get)
	if get.Header().Get("ETag") != `"2"` || got.Name != fmt.Sprintf("Editor %d", winner) || len(got.VMs) != winner+1 {
		t.Fatalf("최종 = ETag %s %+v, winner %d", get.Header().Get("ETag"), got, winner)
	}
	if n := f.count(t, `SELECT count(*) FROM lab_spec_vm_roles WHERE lab_spec_id = $1`, spec.ID); n != winner+1 {
		t.Fatalf("VM Role rows = %d, want %d", n, winner+1)
	}
}

type membershipBody struct {
	UserID   string `json:"userId"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

func TestListClassMembershipsAgainstPostgres(t *testing.T) {
	f := newLabSpecFixture(t)
	want := []membershipBody{
		{UserID: f.irene.String(), Username: "irene", Role: "INSTRUCTOR"},
		{UserID: f.sam.String(), Username: "sam", Role: "STUDENT"},
	}
	// INSTRUCTOR와 STUDENT 모두 같은 목록을 본다.
	for _, token := range []string{f.ireneToken, f.samToken} {
		rec := f.send(http.MethodGet, "/api/v1/classes/"+f.a1.String()+"/memberships", "", withCookie(token))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET memberships = %d: %s", rec.Code, rec.Body.String())
		}
		var list struct {
			Items []membershipBody `json:"items"`
		}
		decodeStrict(t, rec, &list)
		if len(list.Items) != len(want) || list.Items[0] != want[0] || list.Items[1] != want[1] {
			t.Fatalf("items = %+v, want %+v", list.Items, want)
		}
	}

	tests := []struct {
		name   string
		token  string
		class  string
		status int
		code   string
	}{
		// ADMIN은 Membership을 대신하지 않는다.
		{"같은 Organization ADMIN", f.adminT, f.a1.String(), http.StatusForbidden, "forbidden"},
		{"같은 Organization 비참여 강사", f.ivanToken, f.a1.String(), http.StatusForbidden, "forbidden"},
		{"다른 Organization Class", f.bobToken, f.a1.String(), http.StatusForbidden, "forbidden"},
		{"없는 Class", f.ireneToken, uuid.NewString(), http.StatusNotFound, "not_found"},
		{"형식이 다른 classId", f.ireneToken, "nope", http.StatusNotFound, "not_found"},
	}
	for _, tt := range tests {
		rec := f.send(http.MethodGet, "/api/v1/classes/"+tt.class+"/memberships", "", withCookie(tt.token))
		if rec.Code != tt.status || problemCode(t, rec) != tt.code {
			t.Fatalf("%s = %d: %s", tt.name, rec.Code, rec.Body.String())
		}
	}
	if rec := f.send(http.MethodGet, "/api/v1/classes/"+f.a1.String()+"/memberships", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("Cookie 없는 요청 = %d", rec.Code)
	}

	// 응답은 현재 Membership을 반영한다. Membership 변경 기능은 없으므로 DB에서 직접 바꾼다.
	f.exec(t, `DELETE FROM class_memberships WHERE class_id = $1 AND user_id = $2`, f.a1, f.sam)
	rec := f.send(http.MethodGet, "/api/v1/classes/"+f.a1.String()+"/memberships", "", withCookie(f.samToken))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Membership 삭제 뒤 학생 = %d, want 403", rec.Code)
	}
	rec = f.send(http.MethodGet, "/api/v1/classes/"+f.a1.String()+"/memberships", "", withCookie(f.ireneToken))
	var list struct {
		Items []membershipBody `json:"items"`
	}
	decodeStrict(t, rec, &list)
	if len(list.Items) != 1 || list.Items[0] != want[0] {
		t.Fatalf("삭제 뒤 items = %+v", list.Items)
	}
}

// GET은 LabSpec 본문(revision, workspaceVm)과 VM Role을 같은 version에서 읽어야 한다. 수정이 계속 commit되는 동안 읽은 모든 응답이
// 그 ETag의 revision에 실제로 저장된 정의와 정확히 같은지 확인한다. 본문과 VM Role을 서로 다른 snapshot에서 읽으면 이전 ETag와
// 새 VM Role 배열이 섞인 응답이 관측된다.
func TestLabSpecGetIsConsistentWithConcurrentUpdatesAgainstPostgres(t *testing.T) {
	f := newLabSpecFixture(t)
	definition := func(k int) string {
		vms := make([]string, 0, k)
		for j := 0; j < k; j++ {
			vms = append(vms, fmt.Sprintf(`{"role":"r%02d","imageRef":%q,"sizeRef":%q,"count":1}`, j, f.image1, f.flavor1))
		}
		return fmt.Sprintf(`{"name":"v%d","vms":[%s],"workspaceVm":{"role":"r%02d","instanceIndex":0},"internetOutbound":false}`,
			k, strings.Join(vms, ","), k-1)
	}
	type snapshot struct {
		name, workspaceRole string
		roles               int
	}
	created := f.createLabSpec(f.ireneToken, definition(1))
	if created.Code != http.StatusCreated {
		t.Fatalf("POST = %d: %s", created.Code, created.Body.String())
	}
	id := decodeLabSpec(t, created).ID
	// ETag → 그 revision에서 저장한 정의. writer 하나만 쓰므로 응답으로 받은 값이 그 revision의 전부다.
	saved := map[string]snapshot{`"1"`: {name: "v1", workspaceRole: "r00", roles: 1}}

	const writes = 150
	done := make(chan struct{})
	var readers sync.WaitGroup
	type observed struct {
		etag string
		body labSpecBody
	}
	// reader마다 자기 slice에만 기록하고 모두 끝난 뒤 검증한다.
	results := make([][]observed, 4)
	for i := range results {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				rec := f.getLabSpec(f.ireneToken, id)
				if rec.Code != http.StatusOK {
					t.Errorf("GET = %d: %s", rec.Code, rec.Body.String())
					return
				}
				var body labSpecBody
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Errorf("GET body: %v", err)
					return
				}
				results[i] = append(results[i], observed{etag: rec.Header().Get("ETag"), body: body})
			}
		}()
	}

	etag := `"1"`
	for i := 0; i < writes; i++ {
		// 1~6개 사이를 오가며 VM Role 개수가 매번 바뀌게 한다.
		k := i%6 + 1
		rec := f.putLabSpec(f.ireneToken, id, etag, definition(k))
		if rec.Code != http.StatusOK {
			close(done)
			readers.Wait()
			t.Fatalf("PUT %d = %d: %s", i, rec.Code, rec.Body.String())
		}
		etag = rec.Header().Get("ETag")
		saved[etag] = snapshot{name: fmt.Sprintf("v%d", k), workspaceRole: fmt.Sprintf("r%02d", k-1), roles: k}
	}
	close(done)
	readers.Wait()

	n := 0
	for _, r := range slices.Concat(results...) {
		n++
		want, ok := saved[r.etag]
		got := snapshot{name: r.body.Name, workspaceRole: r.body.WorkspaceVM.Role, roles: len(r.body.VMs)}
		if !ok || got != want {
			t.Fatalf("GET ETag %s = %+v, 그 revision에 저장한 정의 = %+v (기록 %v)", r.etag, got, want, ok)
		}
	}
	if n == 0 {
		t.Fatal("GET 응답을 하나도 관측하지 못했습니다")
	}
	t.Logf("PUT %d번 동안 GET %d번이 모두 같은 revision의 정의였습니다", writes, n)
}
