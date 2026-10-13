package labspec

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// fakeStore는 Service가 사용하는 query와 변경만 구현한 in-memory Store다. 실제 Repository처럼 권한·revision·의미를 판단하지 않는다.
// WithinTransaction은 fn이 실패하면 변경 전 상태로 되돌린다. 구현하지 않은 method를 부르면 embedded nil interface 때문에 panic한다.
type fakeStore struct {
	repository.Repositories

	instructors map[uuid.UUID]bool
	specs       map[uuid.UUID]repository.LabSpec
	images      map[uuid.UUID]repository.ProviderMapping
	flavors     map[uuid.UUID]repository.ProviderMapping

	roleErr  error
	queryErr error

	created     []repository.NewLabSpec
	updates     []repository.LabSpecUpdate
	lockedForUp int
}

var _ Store = (*fakeStore)(nil)

func newFakeStore() *fakeStore {
	return &fakeStore{
		instructors: map[uuid.UUID]bool{},
		specs:       map[uuid.UUID]repository.LabSpec{},
		images:      map[uuid.UUID]repository.ProviderMapping{},
		flavors:     map[uuid.UUID]repository.ProviderMapping{},
	}
}

func (f *fakeStore) WithinTransaction(ctx context.Context, fn func(context.Context, repository.Repositories) error) error {
	saved := make(map[uuid.UUID]repository.LabSpec, len(f.specs))
	for k, v := range f.specs {
		saved[k] = v
	}
	if err := fn(ctx, f); err != nil {
		f.specs = saved
		return err
	}
	return nil
}

func (f *fakeStore) HasClassRole(_ context.Context, userID uuid.UUID, role repository.ClassRole) (bool, error) {
	if f.roleErr != nil {
		return false, f.roleErr
	}
	return role == repository.ClassRoleInstructor && f.instructors[userID], nil
}

func (f *fakeStore) LabSpecSummariesByOwner(_ context.Context, orgID, ownerID uuid.UUID) ([]repository.LabSpecSummary, error) {
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	var out []repository.LabSpecSummary
	for _, s := range f.specs {
		if s.OrganizationID == orgID && s.OwnerUserID == ownerID {
			out = append(out, s.LabSpecSummary)
		}
	}
	// 실제 query의 이름, ID 순서
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Name < out[j-1].Name; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

func (f *fakeStore) LabSpecByID(_ context.Context, id uuid.UUID) (repository.LabSpec, error) {
	if f.queryErr != nil {
		return repository.LabSpec{}, f.queryErr
	}
	s, ok := f.specs[id]
	if !ok {
		return repository.LabSpec{}, repository.ErrNotFound
	}
	return s, nil
}

func (f *fakeStore) LabSpecForUpdate(ctx context.Context, id uuid.UUID) (repository.LabSpec, error) {
	f.lockedForUp++
	return f.LabSpecByID(ctx, id)
}

func (f *fakeStore) ImageMappingsForShare(_ context.Context, orgID uuid.UUID, ids []uuid.UUID) ([]repository.ProviderMapping, error) {
	return pick(f.images, orgID, ids), nil
}

func (f *fakeStore) FlavorMappingsForShare(_ context.Context, orgID uuid.UUID, ids []uuid.UUID) ([]repository.ProviderMapping, error) {
	return pick(f.flavors, orgID, ids), nil
}

func pick(all map[uuid.UUID]repository.ProviderMapping, orgID uuid.UUID, ids []uuid.UUID) []repository.ProviderMapping {
	var out []repository.ProviderMapping
	for _, id := range ids {
		if m, ok := all[id]; ok && m.OrganizationID == orgID {
			out = append(out, m)
		}
	}
	return out
}

func toRoles(in []repository.NewLabSpecVMRole) []repository.LabSpecVMRole {
	out := make([]repository.LabSpecVMRole, 0, len(in))
	for _, r := range in {
		out = append(out, repository.LabSpecVMRole{
			ID: r.ID, Role: r.Role, DisplayName: r.DisplayName, ImageMappingID: r.ImageMappingID, FlavorMappingID: r.FlavorMappingID, VMCount: r.VMCount,
		})
	}
	return out
}

func (f *fakeStore) CreateLabSpec(_ context.Context, spec repository.NewLabSpec) (repository.LabSpec, error) {
	f.created = append(f.created, spec)
	d := spec.LabSpecDefinition
	s := repository.LabSpec{
		LabSpecSummary: repository.LabSpecSummary{
			ID: spec.ID, OrganizationID: spec.OrganizationID, OwnerUserID: spec.OwnerUserID, Name: d.Name, Description: d.Description,
		},
		Revision: 1, InternetOutbound: d.InternetOutbound, StartupScript: d.StartupScript,
		WorkspaceRole: d.WorkspaceRole, WorkspaceInstanceIndex: d.WorkspaceInstanceIndex, VMRoles: toRoles(d.VMRoles),
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	f.specs[s.ID] = s
	return s, nil
}

func (f *fakeStore) UpdateLabSpec(_ context.Context, u repository.LabSpecUpdate) (repository.LabSpec, bool, error) {
	f.updates = append(f.updates, u)
	s, ok := f.specs[u.ID]
	if !ok || s.OrganizationID != u.OrganizationID || s.Revision != u.ExpectedRevision {
		return repository.LabSpec{}, false, nil
	}
	d := u.LabSpecDefinition
	s.Name, s.Description, s.InternetOutbound, s.StartupScript = d.Name, d.Description, d.InternetOutbound, d.StartupScript
	s.WorkspaceRole, s.WorkspaceInstanceIndex, s.VMRoles = d.WorkspaceRole, d.WorkspaceInstanceIndex, toRoles(d.VMRoles)
	s.Revision++
	f.specs[s.ID] = s
	return s, true, nil
}

// env는 한 Organization의 강사·학생·ADMIN과 ProviderConnection 하나의 활성 mapping이다.
type env struct {
	store                         *fakeStore
	svc                           *Service
	org                           uuid.UUID
	instructor, other, student    repository.User
	admin                         repository.User
	connection                    uuid.UUID
	image, flavor                 uuid.UUID
	otherConnImage, inactiveImage uuid.UUID
	foreignImage                  uuid.UUID
}

func newEnv() *env {
	e := &env{store: newFakeStore(), org: uuid.New(), connection: uuid.New()}
	e.svc = NewService(e.store)
	user := func(role repository.OrganizationRole) repository.User {
		return repository.User{ID: uuid.New(), OrganizationID: e.org, OrganizationRole: role}
	}
	e.instructor, e.other, e.student = user(repository.OrganizationRoleMember), user(repository.OrganizationRoleMember), user(repository.OrganizationRoleMember)
	// ADMIN은 Organization 관리 권한일 뿐 INSTRUCTOR Membership을 대신하지 않는다.
	e.admin = user(repository.OrganizationRoleAdmin)
	e.store.instructors[e.instructor.ID] = true
	e.store.instructors[e.other.ID] = true

	mapping := func(all map[uuid.UUID]repository.ProviderMapping, org, conn uuid.UUID, active bool) uuid.UUID {
		id := uuid.New()
		all[id] = repository.ProviderMapping{ID: id, OrganizationID: org, ProviderConnectionID: conn, Active: active}
		return id
	}
	e.image = mapping(e.store.images, e.org, e.connection, true)
	e.flavor = mapping(e.store.flavors, e.org, e.connection, true)
	e.otherConnImage = mapping(e.store.images, e.org, uuid.New(), true)
	e.inactiveImage = mapping(e.store.images, e.org, e.connection, false)
	e.foreignImage = mapping(e.store.images, uuid.New(), uuid.New(), true)
	return e
}

func (e *env) write() Write {
	return Write{
		Name: "Kubernetes Basic",
		VMs: []VM{
			{Role: "worker", ImageRef: e.image.String(), SizeRef: e.flavor.String(), Count: 2},
			{Role: "control", Name: ptr("Control Plane"), ImageRef: e.image.String(), SizeRef: e.flavor.String(), Count: 1},
		},
		WorkspaceVM:      WorkspaceVM{Role: "worker", InstanceIndex: 1},
		InternetOutbound: true,
		StartupScript:    ptr("#!/bin/sh"),
	}
}

func ptr(s string) *string { return &s }

func (e *env) create(t *testing.T, owner repository.User) Definition {
	t.Helper()
	def, err := e.svc.Create(t.Context(), owner, e.write())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return def
}

func TestCreateStoresDefinitionOwnedByInstructorAtRevisionOne(t *testing.T) {
	e := newEnv()

	def := e.create(t, e.instructor)

	if def.OwnerUserID != e.instructor.ID || def.Revision != 1 || def.Name != "Kubernetes Basic" {
		t.Fatalf("Create() = %+v", def)
	}
	if len(e.store.created) != 1 || e.store.created[0].OrganizationID != e.org {
		t.Fatalf("저장 = %+v", e.store.created)
	}
	saved := e.store.created[0]
	if saved.WorkspaceRole != "worker" || saved.WorkspaceInstanceIndex != 1 || len(saved.VMRoles) != 2 {
		t.Fatalf("저장된 정의 = %+v", saved.LabSpecDefinition)
	}
	if saved.VMRoles[0].ImageMappingID != e.image || saved.VMRoles[0].FlavorMappingID != e.flavor || saved.VMRoles[0].ID == uuid.Nil {
		t.Fatalf("VM Role mapping = %+v", saved.VMRoles[0])
	}
	if def.VMs[0].ImageRef != e.image.String() || def.VMs[0].SizeRef != e.flavor.String() {
		t.Fatalf("응답 imageRef/sizeRef는 mapping ID여야 합니다: %+v", def.VMs[0])
	}
}

func TestCreateRequiresInstructorMembership(t *testing.T) {
	e := newEnv()
	for _, user := range []repository.User{e.student, e.admin} {
		_, err := e.svc.Create(t.Context(), user, e.write())
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("Create(%s) error = %v, want ErrForbidden", user.OrganizationRole, err)
		}
	}
	if len(e.store.created) != 0 {
		t.Fatal("권한 없는 생성이 저장되었습니다")
	}
}

func TestCreateSemanticValidation(t *testing.T) {
	e := newEnv()
	tests := []struct {
		name   string
		mutate func(w *Write)
		want   Reason
	}{
		{"공백 name", func(w *Write) { w.Name = " \t " }, ReasonNameBlank},
		{"name NUL", func(w *Write) { w.Name = "a\x00b" }, ReasonInvalidText},
		{"startupScript NUL", func(w *Write) { w.StartupScript = ptr("echo\x00") }, ReasonInvalidText},
		{"vm name NUL", func(w *Write) { w.VMs[0].Name = ptr("\x00") }, ReasonInvalidText},
		{"vms 없음", func(w *Write) { w.VMs = nil }, ReasonVMsEmpty},
		{"공백 role", func(w *Write) { w.VMs[0].Role = "  " }, ReasonRoleBlank},
		{"중복 role", func(w *Write) { w.VMs[1].Role = "worker" }, ReasonDuplicateRole},
		{"count 0", func(w *Write) { w.VMs[0].Count = 0 }, ReasonCountOutOfRange},
		{"count integer 범위 초과", func(w *Write) { w.VMs[0].Count = math.MaxInt32 + 1 }, ReasonCountOutOfRange},
		{"workspace role 미선언", func(w *Write) { w.WorkspaceVM.Role = "db" }, ReasonWorkspaceRoleUnknown},
		{"workspace index = count", func(w *Write) { w.WorkspaceVM.InstanceIndex = 2 }, ReasonWorkspaceIndexRange},
		{"workspace index 음수", func(w *Write) { w.WorkspaceVM.InstanceIndex = -1 }, ReasonWorkspaceIndexRange},
		{"imageRef UUID 아님", func(w *Write) { w.VMs[0].ImageRef = "ubuntu-24.04" }, ReasonImageRefUnknown},
		{"imageRef 대문자 UUID", func(w *Write) { w.VMs[0].ImageRef = strings.ToUpper(e.image.String()) }, ReasonImageRefUnknown},
		{"imageRef 없음", func(w *Write) { w.VMs[0].ImageRef = uuid.NewString() }, ReasonImageRefUnknown},
		{"imageRef 비활성", func(w *Write) { w.VMs[0].ImageRef = e.inactiveImage.String() }, ReasonImageRefUnknown},
		{"imageRef 다른 Organization", func(w *Write) { w.VMs[0].ImageRef = e.foreignImage.String() }, ReasonImageRefUnknown},
		{"sizeRef가 Image mapping ID", func(w *Write) { w.VMs[0].SizeRef = e.image.String() }, ReasonSizeRefUnknown},
		{"ProviderConnection 혼합", func(w *Write) { w.VMs[1].ImageRef = e.otherConnImage.String() }, ReasonMixedConnections},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := e.write()
			tt.mutate(&w)

			_, err := e.svc.Create(t.Context(), e.instructor, w)

			var ve *ValidationError
			if !errors.Is(err, ErrInvalid) || !errors.As(err, &ve) || ve.Reason != tt.want {
				t.Fatalf("Create() error = %v, want reason %s", err, tt.want)
			}
		})
	}
	if len(e.store.created) != 0 {
		t.Fatal("검증 실패가 저장되었습니다")
	}
}

// D-11은 Organization 단위 공유를 후속 범위로 두었다. 강사도 같은 Organization의 다른 강사 LabSpec을 목록에서 보지 못한다.
func TestListShowsOnlyOwnLabSpecs(t *testing.T) {
	e := newEnv()
	mine := e.create(t, e.instructor)
	e.create(t, e.other)
	// 다른 Organization의 같은 owner ID
	foreign := uuid.New()
	e.store.specs[foreign] = repository.LabSpec{LabSpecSummary: repository.LabSpecSummary{ID: foreign, OrganizationID: uuid.New(), OwnerUserID: e.instructor.ID, Name: "Foreign"}, Revision: 1}
	// 강사 Membership을 잃은 owner도 자기 것은 본다.
	formerOwner := e.student
	formerSpec := uuid.New()
	e.store.specs[formerSpec] = repository.LabSpec{LabSpecSummary: repository.LabSpecSummary{ID: formerSpec, OrganizationID: e.org, OwnerUserID: formerOwner.ID, Name: "Former"}, Revision: 1}

	got, err := e.svc.List(t.Context(), e.instructor)
	if err != nil || len(got) != 1 || got[0].ID != mine.ID {
		t.Fatalf("강사 List() = %+v, %v, want 자신의 것 하나", got, err)
	}
	got, err = e.svc.List(t.Context(), formerOwner)
	if err != nil || len(got) != 1 || got[0].ID != formerSpec {
		t.Fatalf("Membership을 잃은 owner List() = %+v, %v, want 자신의 것만", got, err)
	}
	got, err = e.svc.List(t.Context(), e.admin)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("ADMIN List() = %#v, %v, want empty non-nil", got, err)
	}
}

func TestListFailsClosedOnRowOfAnotherOwner(t *testing.T) {
	e := newEnv()
	e.create(t, e.other)
	store := &allSummariesStore{fakeStore: e.store}

	got, err := NewService(store).List(t.Context(), e.instructor)

	if !errors.Is(err, ErrInconsistentData) || got != nil {
		t.Fatalf("List() = %+v, %v, want ErrInconsistentData", got, err)
	}
}

// allSummariesStore는 owner 조건을 무시하고 Organization의 모든 LabSpec을 돌려주는 잘못된 저장소다.
type allSummariesStore struct{ *fakeStore }

func (s *allSummariesStore) LabSpecSummariesByOwner(ctx context.Context, orgID, _ uuid.UUID) ([]repository.LabSpecSummary, error) {
	var out []repository.LabSpecSummary
	for _, spec := range s.specs {
		if spec.OrganizationID == orgID {
			out = append(out, spec.LabSpecSummary)
		}
	}
	return out, nil
}

func TestGetBoundaries(t *testing.T) {
	e := newEnv()
	spec := e.create(t, e.other)
	foreign := uuid.New()
	e.store.specs[foreign] = repository.LabSpec{
		LabSpecSummary: repository.LabSpecSummary{ID: foreign, OrganizationID: uuid.New(), OwnerUserID: uuid.New(), Name: "Foreign"},
		Revision:       1, VMRoles: []repository.LabSpecVMRole{{Role: "a", VMCount: 1}},
	}
	foreignOwner := repository.User{ID: e.store.specs[foreign].OwnerUserID, OrganizationID: e.org}

	tests := []struct {
		name string
		user repository.User
		id   string
		want error
	}{
		{"owner", e.other, spec.ID.String(), nil},
		// Organization 단위 공유는 후속 범위다. 다른 강사도 볼 수 없다.
		{"같은 Organization의 다른 강사", e.instructor, spec.ID.String(), ErrForbidden},
		{"같은 Organization 학생", e.student, spec.ID.String(), ErrForbidden},
		{"같은 Organization ADMIN", e.admin, spec.ID.String(), ErrForbidden},
		{"다른 Organization의 LabSpec", e.instructor, foreign.String(), ErrNotFound},
		// owner ID가 같아도 Organization이 다르면 보이지 않는다.
		{"Organization이 다른 같은 owner ID", foreignOwner, foreign.String(), ErrNotFound},
		{"없는 ID", e.instructor, uuid.NewString(), ErrNotFound},
		{"canonical 아닌 ID", e.instructor, strings.ToUpper(spec.ID.String()), ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := e.svc.Get(t.Context(), tt.user, tt.id)
			if !errors.Is(err, tt.want) && !(tt.want == nil && err == nil) {
				t.Fatalf("Get() error = %v, want %v", err, tt.want)
			}
			if tt.want == nil && (got.ID != spec.ID || got.Revision != 1) {
				t.Fatalf("Get() = %+v", got)
			}
		})
	}
}

func TestUpdateAppliesOnlyForOwnerWithCurrentRevision(t *testing.T) {
	e := newEnv()
	spec := e.create(t, e.instructor)
	w := e.write()
	w.Name = "Renamed"
	w.VMs = w.VMs[:1]
	w.WorkspaceVM = WorkspaceVM{Role: "worker", InstanceIndex: 0}

	got, err := e.svc.Update(t.Context(), e.instructor, spec.ID.String(), "1", w)

	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if got.Revision != 2 || got.Name != "Renamed" || len(got.VMs) != 1 || got.OwnerUserID != e.instructor.ID {
		t.Fatalf("Update() = %+v", got)
	}
	if e.store.lockedForUp != 1 || len(e.store.updates) != 1 || e.store.updates[0].ExpectedRevision != 1 {
		t.Fatalf("잠금/조건부 수정 = %d, %+v", e.store.lockedForUp, e.store.updates)
	}
}

func TestUpdateRejectionsChangeNothingAndFollowCheckOrder(t *testing.T) {
	e := newEnv()
	spec := e.create(t, e.instructor)
	foreign := uuid.New()
	e.store.specs[foreign] = repository.LabSpec{LabSpecSummary: repository.LabSpecSummary{ID: foreign, OrganizationID: uuid.New(), OwnerUserID: e.instructor.ID}, Revision: 1}
	invalidWrite := e.write()
	invalidWrite.WorkspaceVM.Role = "missing"
	// 강사 Membership을 잃은 owner
	lapsed := e.store.specs[spec.ID]
	formerOwner := e.student
	lapsedID := uuid.New()
	lapsed.ID, lapsed.OwnerUserID = lapsedID, formerOwner.ID
	e.store.specs[lapsedID] = lapsed

	tests := []struct {
		name    string
		user    repository.User
		id      string
		ifMatch string
		write   Write
		want    error
	}{
		{"다른 Organization", e.instructor, foreign.String(), "1", e.write(), ErrNotFound},
		{"없는 ID", e.instructor, uuid.NewString(), "1", e.write(), ErrNotFound},
		{"형식이 다른 ID", e.instructor, "x", "1", e.write(), ErrNotFound},
		// 다른 강사는 볼 수 있어도 수정할 수 없다. revision이 틀려도 403이 먼저다.
		{"owner 아닌 강사", e.other, spec.ID.String(), "999", invalidWrite, ErrForbidden},
		{"owner 아닌 학생", e.student, spec.ID.String(), "1", e.write(), ErrForbidden},
		{"강사 Membership을 잃은 owner", formerOwner, lapsedID.String(), "1", e.write(), ErrForbidden},
		// precondition은 의미 검증보다 먼저다.
		{"stale revision", e.instructor, spec.ID.String(), "2", invalidWrite, ErrStaleRevision},
		{"숫자가 아닌 revision", e.instructor, spec.ID.String(), "abc", e.write(), ErrStaleRevision},
		{"0으로 시작하는 revision", e.instructor, spec.ID.String(), "01", e.write(), ErrStaleRevision},
		{"의미 검증 실패", e.instructor, spec.ID.String(), "1", invalidWrite, ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := e.store.specs[spec.ID]

			_, err := e.svc.Update(t.Context(), tt.user, tt.id, tt.ifMatch, tt.write)

			if !errors.Is(err, tt.want) {
				t.Fatalf("Update() error = %v, want %v", err, tt.want)
			}
			after := e.store.specs[spec.ID]
			if after.Revision != before.Revision || after.Name != before.Name || len(e.store.updates) != 0 {
				t.Fatalf("거절된 수정이 반영되었습니다: %+v → %+v (%d updates)", before, after, len(e.store.updates))
			}
		})
	}
}

// 같은 revision으로 두 번 수정하면 두 번째는 stale이다(실제 동시성은 PostgreSQL integration test가 검증한다).
func TestUpdateWithSameRevisionTwiceSecondIsStale(t *testing.T) {
	e := newEnv()
	spec := e.create(t, e.instructor)

	if _, err := e.svc.Update(t.Context(), e.instructor, spec.ID.String(), "1", e.write()); err != nil {
		t.Fatalf("첫 Update() error = %v", err)
	}
	_, err := e.svc.Update(t.Context(), e.instructor, spec.ID.String(), "1", e.write())
	if !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("두 번째 Update() error = %v, want ErrStaleRevision", err)
	}
}

func TestRepositoryFailuresAreInternal(t *testing.T) {
	e := newEnv()
	spec := e.create(t, e.instructor)
	internal := &repository.Error{Kind: repository.KindInternal, Op: "x", Cause: errors.New("password=hunter2")}

	e.store.roleErr, e.store.queryErr = internal, internal
	for name, call := range map[string]func() error{
		"List":   func() error { _, err := e.svc.List(t.Context(), e.instructor); return err },
		"Get":    func() error { _, err := e.svc.Get(t.Context(), e.instructor, spec.ID.String()); return err },
		"Create": func() error { _, err := e.svc.Create(t.Context(), e.instructor, e.write()); return err },
		"Update": func() error {
			_, err := e.svc.Update(t.Context(), e.instructor, spec.ID.String(), "1", e.write())
			return err
		},
	} {
		err := call()
		if !errors.Is(err, repository.ErrInternal) || errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) {
			t.Fatalf("%s error = %v, want internal", name, err)
		}
		if strings.Contains(err.Error(), "hunter2") {
			t.Fatalf("%s 오류 문자열에 원문이 있습니다: %v", name, err)
		}
	}
}

func TestInconsistentRowsFailClosed(t *testing.T) {
	e := newEnv()
	spec := e.create(t, e.instructor)
	broken := e.store.specs[spec.ID]
	broken.VMRoles = nil
	e.store.specs[spec.ID] = broken

	if _, err := e.svc.Get(t.Context(), e.instructor, spec.ID.String()); !errors.Is(err, ErrInconsistentData) {
		t.Fatalf("VM Role 없는 LabSpec Get() error = %v, want ErrInconsistentData", err)
	}

	e2 := newEnv()
	m := e2.store.images[e2.image]
	m.OrganizationID = uuid.New()
	// 다른 Organization의 row를 돌려주는 저장소
	e2.store.images[e2.image] = m
	store := &foreignMappingStore{fakeStore: e2.store, foreign: m}
	_, err := NewService(store).Create(t.Context(), e2.instructor, e2.write())
	if !errors.Is(err, ErrInconsistentData) {
		t.Fatalf("다른 Organization mapping을 받은 Create() error = %v, want ErrInconsistentData", err)
	}
}

type foreignMappingStore struct {
	*fakeStore
	foreign repository.ProviderMapping
}

func (s *foreignMappingStore) WithinTransaction(ctx context.Context, fn func(context.Context, repository.Repositories) error) error {
	return fn(ctx, s)
}

func (s *foreignMappingStore) ImageMappingsForShare(context.Context, uuid.UUID, []uuid.UUID) ([]repository.ProviderMapping, error) {
	return []repository.ProviderMapping{s.foreign}, nil
}
