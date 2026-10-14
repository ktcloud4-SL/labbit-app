// Package labexecutiontest는 Provision Integration Test가 쓰는 PostgreSQL Fixture다. 실제 Provider 값처럼 보이는 marker는
// Browser 응답이나 log에 나타나면 안 되는 값이다.
package labexecutiontest

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	ImageMarker    = "glance-image-marker-6b0e2f"
	ImageMarker2   = "glance-image-marker-second-77aa"
	FlavorMarker   = "nova-flavor-marker-31c9d4"
	ScriptMarker   = "startup-script-marker-e4b1"
	StartupScript  = "#!/bin/sh\necho " + ScriptMarker + "\n"
	WorkspaceRole  = "web"
	WorkspaceIndex = 1
)

// Fixture는 시딩한 데이터의 ID다.
//
//	Organization
//	  ├─ Class ─ INSTRUCTOR Instructor, INSTRUCTOR CoInstructor, STUDENT StudentA/B/C
//	  ├─ OtherClass ─ INSTRUCTOR Instructor, STUDENT Outsider
//	  ├─ Admin(organizationRole=ADMIN, Membership 없음)
//	  ├─ Connector ─ ProviderConnection ─ Image(active), Image2(active), Flavor(active)
//	  └─ LabSpec(owner Instructor, revision 1): web×2(Image, Flavor), db×1(Image, Flavor), workspace web[1], Startup Script
//	OtherOrganization ─ Foreign
type Fixture struct {
	OrganizationID, OtherOrganizationID              uuid.UUID
	ClassID, OtherClassID                            uuid.UUID
	InstructorID, CoInstructorID                     uuid.UUID
	StudentAID, StudentBID, StudentCID               uuid.UUID
	OutsiderID, AdminID, ForeignID                   uuid.UUID
	ConnectorID, ProviderConnectionID                uuid.UUID
	ImageMappingID, ImageMapping2ID, FlavorMappingID uuid.UUID
	LabSpecID                                        uuid.UUID
}

// Users는 세션을 만들 사용자 목록이다.
func (f *Fixture) Users() []uuid.UUID {
	return []uuid.UUID{f.InstructorID, f.CoInstructorID, f.StudentAID, f.StudentBID, f.StudentCID, f.OutsiderID, f.AdminID, f.ForeignID}
}

// New는 conn이 가리키는 (Migration이 적용된) database에 Fixture를 만든다.
func New(t *testing.T, conn *pgx.Conn) *Fixture {
	t.Helper()
	f := &Fixture{
		OrganizationID: uuid.New(), OtherOrganizationID: uuid.New(), ClassID: uuid.New(), OtherClassID: uuid.New(),
		InstructorID: uuid.New(), CoInstructorID: uuid.New(), StudentAID: uuid.New(), StudentBID: uuid.New(), StudentCID: uuid.New(),
		OutsiderID: uuid.New(), AdminID: uuid.New(), ForeignID: uuid.New(), ConnectorID: uuid.New(), ProviderConnectionID: uuid.New(),
		ImageMappingID: uuid.New(), ImageMapping2ID: uuid.New(), FlavorMappingID: uuid.New(), LabSpecID: uuid.New(),
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		Exec(t, conn, sql, args...)
	}
	exec(`INSERT INTO organizations (id, name) VALUES ($1, 'Provision Academy'), ($2, 'Other Academy')`, f.OrganizationID, f.OtherOrganizationID)
	for _, id := range []uuid.UUID{f.InstructorID, f.CoInstructorID, f.StudentAID, f.StudentBID, f.StudentCID, f.OutsiderID} {
		exec(`INSERT INTO users (id, organization_id, organization_role) VALUES ($1, $2, 'MEMBER')`, id, f.OrganizationID)
	}
	exec(`INSERT INTO users (id, organization_id, organization_role) VALUES ($1, $2, 'ADMIN')`, f.AdminID, f.OrganizationID)
	exec(`INSERT INTO users (id, organization_id, organization_role) VALUES ($1, $2, 'MEMBER')`, f.ForeignID, f.OtherOrganizationID)
	exec(`INSERT INTO classes (id, organization_id, name) VALUES ($1, $3, 'Class'), ($2, $3, 'Other Class')`, f.ClassID, f.OtherClassID, f.OrganizationID)
	for _, m := range []struct {
		class, user uuid.UUID
		role        string
	}{
		{f.ClassID, f.InstructorID, "INSTRUCTOR"}, {f.ClassID, f.CoInstructorID, "INSTRUCTOR"},
		{f.ClassID, f.StudentAID, "STUDENT"}, {f.ClassID, f.StudentBID, "STUDENT"}, {f.ClassID, f.StudentCID, "STUDENT"},
		{f.OtherClassID, f.InstructorID, "INSTRUCTOR"}, {f.OtherClassID, f.OutsiderID, "STUDENT"},
	} {
		exec(`INSERT INTO class_memberships (organization_id, class_id, user_id, role) VALUES ($1, $2, $3, $4)`, f.OrganizationID, m.class, m.user, m.role)
	}
	exec(`INSERT INTO connectors (id, organization_id, name) VALUES ($1, $2, 'Connector')`, f.ConnectorID, f.OrganizationID)
	exec(`INSERT INTO provider_connections (id, organization_id, connector_id, name) VALUES ($1, $2, $3, 'Provider')`, f.ProviderConnectionID, f.OrganizationID, f.ConnectorID)
	exec(`INSERT INTO provider_image_mappings (id, organization_id, provider_connection_id, provider_image_id, display_name)
	      VALUES ($1, $3, $4, $5, 'Ubuntu'), ($2, $3, $4, $6, 'Ubuntu next')`, f.ImageMappingID, f.ImageMapping2ID, f.OrganizationID, f.ProviderConnectionID, ImageMarker, ImageMarker2)
	exec(`INSERT INTO provider_flavor_mappings (id, organization_id, provider_connection_id, provider_flavor_id, display_name, vcpus, ram_mib, disk_gib)
	      VALUES ($1, $2, $3, $4, 'Small', 2, 4096, 40)`, f.FlavorMappingID, f.OrganizationID, f.ProviderConnectionID, FlavorMarker)
	exec(`INSERT INTO lab_specs (id, organization_id, owner_user_id, name, internet_outbound, startup_script, workspace_role, workspace_instance_index)
	      VALUES ($1, $2, $3, 'Spec', true, $4, $5, $6)`, f.LabSpecID, f.OrganizationID, f.InstructorID, StartupScript, WorkspaceRole, WorkspaceIndex)
	exec(`INSERT INTO lab_spec_vm_roles (id, organization_id, lab_spec_id, role, image_mapping_id, flavor_mapping_id, vm_count)
	      VALUES ($1, $3, $4, 'web', $5, $6, 2), ($2, $3, $4, 'db', $5, $6, 1)`, uuid.New(), uuid.New(), f.OrganizationID, f.LabSpecID, f.ImageMappingID, f.FlavorMappingID)
	return f
}

// Exec는 Fixture 밖의 test SQL을 실행한다.
func Exec(t *testing.T, conn *pgx.Conn, sql string, args ...any) {
	t.Helper()
	if _, err := conn.Exec(t.Context(), sql, args...); err != nil {
		t.Fatalf("fixture SQL 실패: %v\n%s", err, sql)
	}
}
