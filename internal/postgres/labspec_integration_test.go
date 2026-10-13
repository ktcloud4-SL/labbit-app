//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// labSpecEnv는 sample Organization 하나와 ProviderConnection 둘(conn, other), 그리고 각 연결의 mapping이다.
type labSpecEnv struct {
	db                                    *testDB
	s                                     sample
	conn, otherConn                       uuid.UUID
	image, flavor, otherImage, otherFlavr uuid.UUID
}

func newLabSpecEnv(t *testing.T) *labSpecEnv {
	t.Helper()
	db := newTestDB(t)
	e := &labSpecEnv{db: db, s: bootstrapSample(t, db, 1)}
	e.conn, e.image, e.flavor = insertConnectionWithMappings(t, db, e.s.OrganizationID, "primary")
	e.otherConn, e.otherImage, e.otherFlavr = insertConnectionWithMappings(t, db, e.s.OrganizationID, "secondary")
	return e
}

// insertConnectionWithMappings는 Connector, ProviderConnection, 활성 Image/Flavor mapping 하나씩을 만든다.
// 운영 경로에서 이 데이터는 기관 관리 기능이 만든다. 이 test는 LabSpec 참조만 검증하므로 SQL로 준비한다.
func insertConnectionWithMappings(t *testing.T, db *testDB, orgID uuid.UUID, name string) (conn, image, flavor uuid.UUID) {
	t.Helper()
	connector := uuid.New()
	conn, image, flavor = uuid.New(), uuid.New(), uuid.New()
	mustExec(t, db, `INSERT INTO connectors (id, organization_id, name) VALUES ($1, $2, $3)`, connector, orgID, name)
	mustExec(t, db, `INSERT INTO provider_connections (id, organization_id, connector_id, name) VALUES ($1, $2, $3, $4)`, conn, orgID, connector, name)
	mustExec(t, db, `INSERT INTO provider_image_mappings (id, organization_id, provider_connection_id, provider_image_id, display_name)
		VALUES ($1, $2, $3, $4, 'Ubuntu')`, image, orgID, conn, "img-"+name)
	mustExec(t, db, `INSERT INTO provider_flavor_mappings (id, organization_id, provider_connection_id, provider_flavor_id, display_name, vcpus, ram_mib, disk_gib)
		VALUES ($1, $2, $3, $4, 'Medium', 2, 4096, 20)`, flavor, orgID, conn, "flv-"+name)
	return conn, image, flavor
}

func strp(s string) *string { return &s }

func (e *labSpecEnv) definition(name string) repository.LabSpecDefinition {
	return repository.LabSpecDefinition{
		Name: name, Description: strp("desc"), InternetOutbound: true, StartupScript: strp("#!/bin/sh"),
		WorkspaceRole: "control", WorkspaceInstanceIndex: 0,
		VMRoles: []repository.NewLabSpecVMRole{
			{ID: uuid.New(), Role: "worker", ImageMappingID: e.image, FlavorMappingID: e.flavor, VMCount: 2},
			{ID: uuid.New(), Role: "control", DisplayName: strp("Control"), ImageMappingID: e.image, FlavorMappingID: e.flavor, VMCount: 1},
		},
	}
}

func (e *labSpecEnv) create(t *testing.T, owner uuid.UUID, def repository.LabSpecDefinition) repository.LabSpec {
	t.Helper()
	var created repository.LabSpec
	err := e.db.store.WithinTransaction(t.Context(), func(ctx context.Context, repos repository.Repositories) error {
		var err error
		created, err = repos.CreateLabSpec(ctx, repository.NewLabSpec{
			ID: uuid.New(), OrganizationID: e.s.OrganizationID, OwnerUserID: owner, LabSpecDefinition: def,
		})
		return err
	})
	if err != nil {
		t.Fatalf("CreateLabSpec() error = %v", err)
	}
	return created
}

func TestLabSpecCreateAndReadRoundTrip(t *testing.T) {
	e := newLabSpecEnv(t)

	created := e.create(t, e.s.InstructorID, e.definition("Bravo Spec"))
	e.create(t, e.s.InstructorID, repository.LabSpecDefinition{
		Name: "Alpha Spec", WorkspaceRole: "node", VMRoles: []repository.NewLabSpecVMRole{
			{ID: uuid.New(), Role: "node", ImageMappingID: e.image, FlavorMappingID: e.flavor, VMCount: 1},
		},
	})

	got, err := e.db.store.LabSpecByID(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("LabSpecByID() error = %v", err)
	}
	if got.Revision != 1 || got.OrganizationID != e.s.OrganizationID || got.OwnerUserID != e.s.InstructorID ||
		got.Name != "Bravo Spec" || *got.Description != "desc" || *got.StartupScript != "#!/bin/sh" || !got.InternetOutbound ||
		got.WorkspaceRole != "control" || got.WorkspaceInstanceIndex != 0 {
		t.Fatalf("LabSpecByID() = %+v", got)
	}
	// VM Role은 role 순서다.
	if len(got.VMRoles) != 2 || got.VMRoles[0].Role != "control" || got.VMRoles[1].Role != "worker" ||
		*got.VMRoles[0].DisplayName != "Control" || got.VMRoles[1].DisplayName != nil || got.VMRoles[1].VMCount != 2 ||
		got.VMRoles[0].ImageMappingID != e.image || got.VMRoles[0].FlavorMappingID != e.flavor {
		t.Fatalf("VMRoles = %+v", got.VMRoles)
	}
	if created.Revision != got.Revision || len(created.VMRoles) != 2 {
		t.Fatalf("CreateLabSpec() 반환 = %+v", created)
	}

	summaries, err := e.db.store.LabSpecSummariesByOrganization(t.Context(), e.s.OrganizationID)
	if err != nil || len(summaries) != 2 || summaries[0].Name != "Alpha Spec" || summaries[0].Description != nil || summaries[1].ID != created.ID {
		t.Fatalf("LabSpecSummariesByOrganization() = %+v, %v", summaries, err)
	}
	other, err := e.db.store.LabSpecSummariesByOrganization(t.Context(), uuid.New())
	if err != nil || len(other) != 0 {
		t.Fatalf("다른 Organization 목록 = %+v, %v", other, err)
	}

	_, err = e.db.store.LabSpecByID(t.Context(), uuid.New())
	assertOnlyKind(t, err, repository.ErrNotFound)
}

// 하나라도 실패하면 transaction 전체가 rollback되어 LabSpec이나 일부 VM Role이 남지 않는다.
func TestLabSpecCreateFailureRollsBackWholeDefinition(t *testing.T) {
	e := newLabSpecEnv(t)
	other := bootstrapSample(t, e.db, 2)
	_, foreignImage, _ := insertConnectionWithMappings(t, e.db, other.OrganizationID, "foreign")

	tests := []struct {
		name   string
		mutate func(d *repository.LabSpecDefinition, owner *uuid.UUID)
		want   error
	}{
		{"다른 Organization의 Image mapping", func(d *repository.LabSpecDefinition, _ *uuid.UUID) { d.VMRoles[1].ImageMappingID = foreignImage }, repository.ErrConstraintViolation},
		{"다른 Organization의 owner", func(_ *repository.LabSpecDefinition, owner *uuid.UUID) { *owner = other.InstructorID }, repository.ErrConstraintViolation},
		{"중복 role", func(d *repository.LabSpecDefinition, _ *uuid.UUID) { d.VMRoles[1].Role = "worker" }, repository.ErrConflict},
		{"vm_count 0", func(d *repository.LabSpecDefinition, _ *uuid.UUID) { d.VMRoles[1].VMCount = 0 }, repository.ErrConstraintViolation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := e.definition("Broken")
			owner := e.s.InstructorID
			tt.mutate(&def, &owner)

			err := e.db.store.WithinTransaction(t.Context(), func(ctx context.Context, repos repository.Repositories) error {
				_, err := repos.CreateLabSpec(ctx, repository.NewLabSpec{
					ID: uuid.New(), OrganizationID: e.s.OrganizationID, OwnerUserID: owner, LabSpecDefinition: def,
				})
				return err
			})

			assertOnlyKind(t, err, tt.want)
			if n := countRows(t, e.db, `SELECT count(*) FROM lab_specs`); n != 0 {
				t.Fatalf("lab_specs = %d, want 0", n)
			}
			if n := countRows(t, e.db, `SELECT count(*) FROM lab_spec_vm_roles`); n != 0 {
				t.Fatalf("lab_spec_vm_roles = %d, want 0", n)
			}
		})
	}
}

func TestUpdateLabSpecIsConditionalOnRevisionAndOrganization(t *testing.T) {
	e := newLabSpecEnv(t)
	spec := e.create(t, e.s.InstructorID, e.definition("Original"))
	newDef := repository.LabSpecDefinition{
		Name: "Renamed", InternetOutbound: false, WorkspaceRole: "db", WorkspaceInstanceIndex: 2,
		VMRoles: []repository.NewLabSpecVMRole{
			{ID: uuid.New(), Role: "db", ImageMappingID: e.otherImage, FlavorMappingID: e.otherFlavr, VMCount: 3},
		},
	}
	update := func(orgID uuid.UUID, revision int64) (repository.LabSpec, bool, error) {
		var got repository.LabSpec
		var ok bool
		err := e.db.store.WithinTransaction(t.Context(), func(ctx context.Context, repos repository.Repositories) error {
			var err error
			got, ok, err = repos.UpdateLabSpec(ctx, repository.LabSpecUpdate{
				ID: spec.ID, OrganizationID: orgID, ExpectedRevision: revision, LabSpecDefinition: newDef,
			})
			return err
		})
		return got, ok, err
	}

	for _, tt := range []struct {
		name     string
		org      uuid.UUID
		revision int64
	}{{"stale revision", e.s.OrganizationID, 2}, {"다른 Organization", uuid.New(), 1}} {
		if _, ok, err := update(tt.org, tt.revision); ok || err != nil {
			t.Fatalf("%s: UpdateLabSpec() ok=%v err=%v, want false, nil", tt.name, ok, err)
		}
		unchanged, _ := e.db.store.LabSpecByID(t.Context(), spec.ID)
		if unchanged.Revision != 1 || unchanged.Name != "Original" || len(unchanged.VMRoles) != 2 {
			t.Fatalf("%s: 불일치 수정이 반영되었습니다: %+v", tt.name, unchanged)
		}
	}

	got, ok, err := update(e.s.OrganizationID, 1)
	if err != nil || !ok {
		t.Fatalf("UpdateLabSpec() ok=%v err=%v", ok, err)
	}
	if got.Revision != 2 || got.Name != "Renamed" || got.Description != nil || got.StartupScript != nil || got.InternetOutbound ||
		got.WorkspaceRole != "db" || len(got.VMRoles) != 1 || got.VMRoles[0].ImageMappingID != e.otherImage || got.VMRoles[0].VMCount != 3 {
		t.Fatalf("UpdateLabSpec() = %+v", got)
	}
	if !got.UpdatedAt.After(spec.UpdatedAt) && !got.UpdatedAt.Equal(spec.UpdatedAt) {
		t.Fatalf("updated_at = %v, created %v", got.UpdatedAt, spec.UpdatedAt)
	}
	if n := countRows(t, e.db, `SELECT count(*) FROM lab_spec_vm_roles WHERE lab_spec_id = $1`, spec.ID); n != 1 {
		t.Fatalf("교체 후 VM Role = %d, want 1", n)
	}
}

// 수정 transaction이 VM Role 교체 뒤에 실패하면 본문, revision, VM Role이 모두 원래대로다.
func TestUpdateLabSpecRollbackPreservesOriginal(t *testing.T) {
	e := newLabSpecEnv(t)
	spec := e.create(t, e.s.InstructorID, e.definition("Original"))
	boom := errors.New("이후 단계 실패")

	err := e.db.store.WithinTransaction(t.Context(), func(ctx context.Context, repos repository.Repositories) error {
		def := e.definition("Changed")
		def.VMRoles = def.VMRoles[:1]
		def.WorkspaceRole = "worker"
		if _, ok, err := repos.UpdateLabSpec(ctx, repository.LabSpecUpdate{
			ID: spec.ID, OrganizationID: e.s.OrganizationID, ExpectedRevision: 1, LabSpecDefinition: def,
		}); err != nil || !ok {
			t.Fatalf("UpdateLabSpec() ok=%v err=%v", ok, err)
		}
		return boom
	})

	if !errors.Is(err, boom) {
		t.Fatalf("WithinTransaction() error = %v", err)
	}
	got, _ := e.db.store.LabSpecByID(t.Context(), spec.ID)
	if got.Revision != 1 || got.Name != "Original" || len(got.VMRoles) != 2 || got.WorkspaceRole != "control" {
		t.Fatalf("rollback 뒤 = %+v", got)
	}

	// 실패한 VM Role INSERT(다른 Organization mapping)도 본문 UPDATE까지 되돌린다.
	other := bootstrapSample(t, e.db, 2)
	_, foreignImage, _ := insertConnectionWithMappings(t, e.db, other.OrganizationID, "foreign")
	err = e.db.store.WithinTransaction(t.Context(), func(ctx context.Context, repos repository.Repositories) error {
		def := e.definition("Changed")
		def.VMRoles[0].ImageMappingID = foreignImage
		_, _, err := repos.UpdateLabSpec(ctx, repository.LabSpecUpdate{
			ID: spec.ID, OrganizationID: e.s.OrganizationID, ExpectedRevision: 1, LabSpecDefinition: def,
		})
		return err
	})
	assertOnlyKind(t, err, repository.ErrConstraintViolation)
	got, _ = e.db.store.LabSpecByID(t.Context(), spec.ID)
	if got.Revision != 1 || got.Name != "Original" || len(got.VMRoles) != 2 {
		t.Fatalf("FK 실패 뒤 = %+v", got)
	}
}

// LabSpecForUpdate는 commit까지 row를 잠가 두 번째 수정이 첫 번째의 결과(새 revision)를 보고 판단하게 한다.
func TestLabSpecForUpdateSerializesConcurrentEditors(t *testing.T) {
	e := newLabSpecEnv(t)
	spec := e.create(t, e.s.InstructorID, e.definition("Original"))

	locked := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = e.db.store.WithinTransaction(context.Background(), func(ctx context.Context, repos repository.Repositories) error {
			if _, err := repos.LabSpecForUpdate(ctx, spec.ID); err != nil {
				return err
			}
			close(locked)
			<-release
			_, _, err := repos.UpdateLabSpec(ctx, repository.LabSpecUpdate{
				ID: spec.ID, OrganizationID: e.s.OrganizationID, ExpectedRevision: 1, LabSpecDefinition: e.definition("First"),
			})
			return err
		})
	}()
	<-locked

	seen := make(chan int64, 1)
	go func() {
		_ = e.db.store.WithinTransaction(context.Background(), func(ctx context.Context, repos repository.Repositories) error {
			current, err := repos.LabSpecForUpdate(ctx, spec.ID)
			if err == nil {
				seen <- current.Revision
			}
			return err
		})
	}()
	waitForLockWait(t, e.db.pool)
	select {
	case rev := <-seen:
		t.Fatalf("두 번째 LabSpecForUpdate가 잠금을 기다리지 않았습니다(revision %d)", rev)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	wg.Wait()
	if rev := <-seen; rev != 2 {
		t.Fatalf("두 번째 transaction이 본 revision = %d, want 2", rev)
	}
}

func TestMappingsForShareFiltersByOrganizationAndReportsActiveFlag(t *testing.T) {
	e := newLabSpecEnv(t)
	other := bootstrapSample(t, e.db, 2)
	_, foreignImage, foreignFlavor := insertConnectionWithMappings(t, e.db, other.OrganizationID, "foreign")
	mustExec(t, e.db, `UPDATE provider_image_mappings SET is_active = false WHERE id = $1`, e.otherImage)

	images, err := e.db.store.ImageMappingsForShare(t.Context(), e.s.OrganizationID, []uuid.UUID{e.image, e.otherImage, foreignImage, uuid.New()})
	if err != nil {
		t.Fatalf("ImageMappingsForShare() error = %v", err)
	}
	byID := map[uuid.UUID]repository.ProviderMapping{}
	for _, m := range images {
		byID[m.ID] = m
	}
	if len(images) != 2 || !byID[e.image].Active || byID[e.otherImage].Active || byID[e.image].ProviderConnectionID != e.conn ||
		byID[e.otherImage].ProviderConnectionID != e.otherConn {
		t.Fatalf("ImageMappingsForShare() = %+v", images)
	}
	flavors, err := e.db.store.FlavorMappingsForShare(t.Context(), e.s.OrganizationID, []uuid.UUID{e.flavor, foreignFlavor})
	if err != nil || len(flavors) != 1 || flavors[0].ID != e.flavor {
		t.Fatalf("FlavorMappingsForShare() = %+v, %v", flavors, err)
	}
	empty, err := e.db.store.ImageMappingsForShare(t.Context(), e.s.OrganizationID, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("빈 ID 목록 = %+v, %v", empty, err)
	}
}

func TestClassMembersAndHasClassRole(t *testing.T) {
	db := newTestDB(t)
	s := bootstrapSample(t, db, 1)
	// Local Account가 없는 User도 Membership이면 목록에 있다(username 빈 문자열).
	noAccount := uuid.New()
	mustExec(t, db, `INSERT INTO users (id, organization_id, organization_role) VALUES ($1, $2, 'MEMBER')`, noAccount, s.OrganizationID)
	mustExec(t, db, `INSERT INTO class_memberships (organization_id, class_id, user_id, role) VALUES ($1, $2, $3, 'STUDENT')`,
		s.OrganizationID, s.AlphaID, noAccount)

	members, err := db.store.ClassMembers(t.Context(), s.AlphaID)
	if err != nil {
		t.Fatalf("ClassMembers() error = %v", err)
	}
	want := []repository.ClassMember{
		{OrganizationID: s.OrganizationID, UserID: s.InstructorID, Username: s.instructorUsername(), Role: repository.ClassRoleInstructor},
		{OrganizationID: s.OrganizationID, UserID: noAccount, Username: "", Role: repository.ClassRoleStudent},
		{OrganizationID: s.OrganizationID, UserID: s.StudentID, Username: s.studentUsername(), Role: repository.ClassRoleStudent},
	}
	if len(members) != len(want) {
		t.Fatalf("ClassMembers() = %+v, want %+v", members, want)
	}
	for i := range want {
		if members[i] != want[i] {
			t.Fatalf("ClassMembers()[%d] = %+v, want %+v", i, members[i], want[i])
		}
	}
	if none, err := db.store.ClassMembers(t.Context(), uuid.New()); err != nil || len(none) != 0 {
		t.Fatalf("없는 Class = %+v, %v", none, err)
	}

	for _, tt := range []struct {
		user uuid.UUID
		role repository.ClassRole
		want bool
	}{
		{s.InstructorID, repository.ClassRoleInstructor, true},
		{s.InstructorID, repository.ClassRoleStudent, true},
		{s.StudentID, repository.ClassRoleInstructor, false},
		{s.AdminID, repository.ClassRoleInstructor, false},
	} {
		got, err := db.store.HasClassRole(t.Context(), tt.user, tt.role)
		if err != nil || got != tt.want {
			t.Fatalf("HasClassRole(%s, %s) = %v, %v, want %v", tt.user, tt.role, got, err, tt.want)
		}
	}
}
