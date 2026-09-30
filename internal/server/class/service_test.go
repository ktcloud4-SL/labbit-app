package class

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// fakeStore는 Service가 사용하는 Class query만 구현한 in-memory Store다.
// 실제 Repository처럼 접근 가능 여부는 판단하지 않고 저장된 값을 그대로 반환한다.
type fakeStore struct {
	classes     map[uuid.UUID]repository.Class
	memberships map[[2]uuid.UUID]repository.ClassMembership // key: class ID, user ID

	// 설정하면 해당 query가 이 오류를 반환한다.
	classErr, membershipErr, listErr error

	calls int // 모든 query 호출 수
}

var _ Store = (*fakeStore)(nil)

func newFakeStore() *fakeStore {
	return &fakeStore{
		classes:     map[uuid.UUID]repository.Class{},
		memberships: map[[2]uuid.UUID]repository.ClassMembership{},
	}
}

func (f *fakeStore) addClass(orgID uuid.UUID, name string) repository.Class {
	class := repository.Class{ID: uuid.New(), OrganizationID: orgID, Name: name, CreatedAt: time.Now()}
	f.classes[class.ID] = class
	return class
}

func (f *fakeStore) addMembership(class repository.Class, user repository.User, role repository.ClassRole) {
	f.memberships[[2]uuid.UUID{class.ID, user.ID}] = repository.ClassMembership{
		OrganizationID: class.OrganizationID, ClassID: class.ID, UserID: user.ID, Role: role,
	}
}

func (f *fakeStore) ClassByID(_ context.Context, id uuid.UUID) (repository.Class, error) {
	f.calls++
	if f.classErr != nil {
		return repository.Class{}, f.classErr
	}
	class, ok := f.classes[id]
	if !ok {
		return repository.Class{}, repository.ErrNotFound
	}
	return class, nil
}

func (f *fakeStore) ClassMembership(_ context.Context, classID, userID uuid.UUID) (repository.ClassMembership, error) {
	f.calls++
	if f.membershipErr != nil {
		return repository.ClassMembership{}, f.membershipErr
	}
	membership, ok := f.memberships[[2]uuid.UUID{classID, userID}]
	if !ok {
		return repository.ClassMembership{}, repository.ErrNotFound
	}
	return membership, nil
}

// ClassesByUser는 실제 query처럼 Membership이 있는 Class만 이름, ID 순서로 반환한다.
func (f *fakeStore) ClassesByUser(_ context.Context, userID uuid.UUID) ([]repository.ClassWithRole, error) {
	f.calls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	var items []repository.ClassWithRole
	for key, membership := range f.memberships {
		if key[1] == userID {
			items = append(items, repository.ClassWithRole{Class: f.classes[membership.ClassID], Role: membership.Role})
		}
	}
	sortByName(items)
	return items, nil
}

func sortByName(items []repository.ClassWithRole) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].Class.Name < items[j-1].Class.Name; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

func newUser(orgID uuid.UUID, role repository.OrganizationRole) repository.User {
	return repository.User{ID: uuid.New(), OrganizationID: orgID, OrganizationRole: role, Username: "user"}
}

// internalRepoError는 Cause에 driver 원문이 든 Repository 오류다.
func internalRepoError(op string) error {
	return &repository.Error{Kind: repository.KindInternal, Op: op, Cause: errors.New("dial tcp 10.0.0.7:5432: password=hunter2")}
}

func assertNoRawCause(t *testing.T, err error) {
	t.Helper()
	for _, leak := range []string{"hunter2", "10.0.0.7", "dial tcp", "password"} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("오류 문자열이 Repository 원문(%q)을 포함합니다: %v", leak, err)
		}
	}
}

func TestListReturnsOnlyClassesWithMembershipAndTheirRoles(t *testing.T) {
	org := uuid.New()
	store := newFakeStore()
	user := newUser(org, repository.OrganizationRoleMember)
	instructing := store.addClass(org, "A1 Algorithms")
	studying := store.addClass(org, "A2 Databases")
	notJoined := store.addClass(org, "A3 Networks")
	otherOrg := store.addClass(uuid.New(), "B1 Other Org")
	store.addMembership(instructing, user, repository.ClassRoleInstructor)
	store.addMembership(studying, user, repository.ClassRoleStudent)
	// 다른 사용자의 Membership은 결과에 영향을 주지 않는다.
	store.addMembership(notJoined, newUser(org, repository.OrganizationRoleMember), repository.ClassRoleInstructor)

	got, err := NewService(store).List(t.Context(), user)

	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	want := []View{
		{ID: instructing.ID, Name: "A1 Algorithms", MyRole: repository.ClassRoleInstructor},
		{ID: studying.ID, Name: "A2 Databases", MyRole: repository.ClassRoleStudent},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("List() = %+v, want %+v (A3=%s, B1=%s는 포함되면 안 됩니다)", got, want, notJoined.ID, otherOrg.ID)
	}
}

func TestListWithoutMembershipIsEmptyNotError(t *testing.T) {
	org := uuid.New()
	store := newFakeStore()
	store.addClass(org, "Unjoined")
	user := newUser(org, repository.OrganizationRoleMember)

	got, err := NewService(store).List(t.Context(), user)

	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("List() = %#v, want non-nil empty slice", got)
	}
}

// ADMIN은 Organization 관리 권한일 뿐 Class Membership을 대신하지 않는다.
func TestListForAdminDoesNotExpandBeyondMemberships(t *testing.T) {
	org := uuid.New()
	store := newFakeStore()
	admin := newUser(org, repository.OrganizationRoleAdmin)
	joined := store.addClass(org, "Joined")
	store.addClass(org, "Same Org But Not Joined")
	store.addMembership(joined, admin, repository.ClassRoleStudent)

	got, err := NewService(store).List(t.Context(), admin)

	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 1 || got[0].ID != joined.ID || got[0].MyRole != repository.ClassRoleStudent {
		t.Fatalf("ADMIN List() = %+v, want only the joined Class as STUDENT", got)
	}
}

func TestListFailsClosedOnInconsistentRepositoryData(t *testing.T) {
	org := uuid.New()
	user := newUser(org, repository.OrganizationRoleMember)
	tests := []struct {
		name string
		item repository.ClassWithRole
	}{
		{name: "다른 Organization의 Class", item: repository.ClassWithRole{
			Class: repository.Class{ID: uuid.New(), OrganizationID: uuid.New(), Name: "Foreign"}, Role: repository.ClassRoleInstructor}},
		{name: "알 수 없는 role", item: repository.ClassWithRole{
			Class: repository.Class{ID: uuid.New(), OrganizationID: org, Name: "Bad Role"}, Role: repository.ClassRole("OWNER")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &listOnlyStore{items: []repository.ClassWithRole{tt.item}}

			got, err := NewService(store).List(t.Context(), user)

			if !errors.Is(err, ErrInconsistentData) || got != nil {
				t.Fatalf("List() = %+v, %v, want nil, ErrInconsistentData", got, err)
			}
		})
	}
}

// listOnlyStore는 ClassesByUser가 미리 정한 값을 그대로 반환한다. 실제 DB에서는 불가능한 조합을 만들기 위한 것이다.
type listOnlyStore struct {
	repository.ClassRepository
	items []repository.ClassWithRole
}

func (s *listOnlyStore) ClassesByUser(context.Context, uuid.UUID) ([]repository.ClassWithRole, error) {
	return s.items, nil
}

func TestListRepositoryFailureIsInternalWithoutRawCause(t *testing.T) {
	store := newFakeStore()
	store.listErr = internalRepoError("ClassesByUser")

	got, err := NewService(store).List(t.Context(), newUser(uuid.New(), repository.OrganizationRoleMember))

	if err == nil || got != nil {
		t.Fatalf("List() = %+v, %v, want nil and error", got, err)
	}
	if errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) {
		t.Fatalf("저장소 장애가 권한/부재 의미로 바뀌었습니다: %v", err)
	}
	if !errors.Is(err, repository.ErrInternal) {
		t.Fatalf("errors.Is(err, repository.ErrInternal) = false: %v", err)
	}
	assertNoRawCause(t, err)
}

func TestGetReturnsMyRoleFromMembership(t *testing.T) {
	tests := []struct {
		name    string
		orgRole repository.OrganizationRole
		role    repository.ClassRole
	}{
		{name: "MEMBER + INSTRUCTOR", orgRole: repository.OrganizationRoleMember, role: repository.ClassRoleInstructor},
		{name: "MEMBER + STUDENT", orgRole: repository.OrganizationRoleMember, role: repository.ClassRoleStudent},
		// Class 역할은 Organization 역할과 독립이다. ADMIN이어도 STUDENT Membership이면 STUDENT다.
		{name: "ADMIN + STUDENT", orgRole: repository.OrganizationRoleAdmin, role: repository.ClassRoleStudent},
		{name: "ADMIN + INSTRUCTOR", orgRole: repository.OrganizationRoleAdmin, role: repository.ClassRoleInstructor},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			org := uuid.New()
			store := newFakeStore()
			user := newUser(org, tt.orgRole)
			class := store.addClass(org, "Algorithms")
			store.addMembership(class, user, tt.role)

			got, err := NewService(store).Get(t.Context(), user, class.ID.String())

			if err != nil {
				t.Fatalf("Get() error = %v", err)
			}
			if want := (View{ID: class.ID, Name: "Algorithms", MyRole: tt.role}); got != want {
				t.Fatalf("Get() = %+v, want %+v", got, want)
			}
		})
	}
}

func TestGetSameUserHasDifferentRolePerClass(t *testing.T) {
	org := uuid.New()
	store := newFakeStore()
	user := newUser(org, repository.OrganizationRoleMember)
	teaching := store.addClass(org, "Teaching")
	studying := store.addClass(org, "Studying")
	store.addMembership(teaching, user, repository.ClassRoleInstructor)
	store.addMembership(studying, user, repository.ClassRoleStudent)
	service := NewService(store)

	gotTeaching, err1 := service.Get(t.Context(), user, teaching.ID.String())
	gotStudying, err2 := service.Get(t.Context(), user, studying.ID.String())

	if err1 != nil || err2 != nil {
		t.Fatalf("Get() errors = %v, %v", err1, err2)
	}
	if gotTeaching.MyRole != repository.ClassRoleInstructor || gotStudying.MyRole != repository.ClassRoleStudent {
		t.Fatalf("roles = %s, %s, want INSTRUCTOR, STUDENT", gotTeaching.MyRole, gotStudying.MyRole)
	}
}

func TestGetMissingClassIsNotFound(t *testing.T) {
	store := newFakeStore()

	_, err := NewService(store).Get(t.Context(), newUser(uuid.New(), repository.OrganizationRoleMember), uuid.NewString())

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}

// client에게 opaque한 ID가 현재 physical ID 형식(canonical UUID)이 아니면 존재하지 않는 Class다.
// 별도 400을 만들지 않으며 저장소도 조회하지 않는다.
func TestGetNonCanonicalIDIsNotFoundWithoutQuery(t *testing.T) {
	org := uuid.New()
	store := newFakeStore()
	user := newUser(org, repository.OrganizationRoleMember)
	class := store.addClass(org, "Algorithms")
	store.addMembership(class, user, repository.ClassRoleInstructor)
	id := class.ID.String()

	for name, raw := range map[string]string{
		"empty":         "",
		"not a uuid":    "not-a-uuid",
		"uppercase":     strings.ToUpper(id),
		"no hyphens":    strings.ReplaceAll(id, "-", ""),
		"urn prefix":    "urn:uuid:" + id,
		"braces":        "{" + id + "}",
		"trailing text": id + "x",
		"leading space": " " + id,
	} {
		t.Run(name, func(t *testing.T) {
			store.calls = 0

			_, err := NewService(store).Get(t.Context(), user, raw)

			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("Get(%q) error = %v, want ErrNotFound", raw, err)
			}
			if store.calls != 0 {
				t.Fatalf("Get(%q)가 저장소를 %d번 조회했습니다", raw, store.calls)
			}
		})
	}
}

func TestGetExistingClassWithoutAccessIsForbidden(t *testing.T) {
	org := uuid.New()
	otherOrg := uuid.New()
	tests := []struct {
		name  string
		user  repository.User
		setup func(store *fakeStore, class repository.Class, user repository.User)
		class func(store *fakeStore) repository.Class
	}{
		{
			name:  "같은 Organization이지만 Membership 없음",
			user:  newUser(org, repository.OrganizationRoleMember),
			class: func(s *fakeStore) repository.Class { return s.addClass(org, "Same Org") },
		},
		{
			// ADMIN은 Organization 관리 권한일 뿐 Class Membership bypass가 아니다.
			name:  "ADMIN이지만 Membership 없음",
			user:  newUser(org, repository.OrganizationRoleAdmin),
			class: func(s *fakeStore) repository.Class { return s.addClass(org, "Admin Without Membership") },
		},
		{
			name:  "다른 Organization의 Class",
			user:  newUser(org, repository.OrganizationRoleMember),
			class: func(s *fakeStore) repository.Class { return s.addClass(otherOrg, "Other Org") },
		},
		{
			name:  "다른 Organization의 Class는 ADMIN도 접근 불가",
			user:  newUser(org, repository.OrganizationRoleAdmin),
			class: func(s *fakeStore) repository.Class { return s.addClass(otherOrg, "Other Org") },
		},
		{
			// DB 제약상 불가능한 조합이지만, 있더라도 Organization이 다르면 Membership을 신뢰해 열어 주지 않는다.
			name: "다른 Organization의 Class에 Membership 행이 있어도 거절",
			user: newUser(org, repository.OrganizationRoleMember),
			class: func(s *fakeStore) repository.Class {
				return s.addClass(otherOrg, "Other Org With Stray Membership")
			},
			setup: func(s *fakeStore, class repository.Class, user repository.User) {
				s.memberships[[2]uuid.UUID{class.ID, user.ID}] = repository.ClassMembership{
					OrganizationID: otherOrg, ClassID: class.ID, UserID: user.ID, Role: repository.ClassRoleInstructor,
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			class := tt.class(store)
			if tt.setup != nil {
				tt.setup(store, class, tt.user)
			}

			got, err := NewService(store).Get(t.Context(), tt.user, class.ID.String())

			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("Get() error = %v, want ErrForbidden", err)
			}
			if got != (View{}) {
				t.Fatalf("Get()이 접근 불가 Class 데이터를 반환했습니다: %+v", got)
			}
		})
	}
}

func TestGetRepositoryFailureIsInternalNeverForbiddenOrNotFound(t *testing.T) {
	org := uuid.New()
	tests := []struct {
		name       string
		breakStore func(store *fakeStore)
	}{
		{name: "Class 조회 실패", breakStore: func(s *fakeStore) { s.classErr = internalRepoError("ClassByID") }},
		{name: "Membership 조회 실패", breakStore: func(s *fakeStore) { s.membershipErr = internalRepoError("ClassMembership") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			user := newUser(org, repository.OrganizationRoleMember)
			class := store.addClass(org, "Algorithms")
			store.addMembership(class, user, repository.ClassRoleInstructor)
			tt.breakStore(store)

			got, err := NewService(store).Get(t.Context(), user, class.ID.String())

			if err == nil || got != (View{}) {
				t.Fatalf("Get() = %+v, %v, want zero View and error", got, err)
			}
			if errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) {
				t.Fatalf("저장소 장애가 권한/부재 의미로 바뀌었습니다: %v", err)
			}
			if !errors.Is(err, repository.ErrInternal) {
				t.Fatalf("errors.Is(err, repository.ErrInternal) = false: %v", err)
			}
			assertNoRawCause(t, err)
		})
	}
}

// 저장소가 서로 모순된 Membership을 반환해도 그 값을 권한 상태로 해석하지 않는다.
func TestGetFailsClosedOnInconsistentMembership(t *testing.T) {
	org := uuid.New()
	tests := []struct {
		name   string
		mutate func(m *repository.ClassMembership)
	}{
		{name: "Organization 불일치", mutate: func(m *repository.ClassMembership) { m.OrganizationID = uuid.New() }},
		{name: "다른 Class의 Membership", mutate: func(m *repository.ClassMembership) { m.ClassID = uuid.New() }},
		{name: "다른 User의 Membership", mutate: func(m *repository.ClassMembership) { m.UserID = uuid.New() }},
		{name: "알 수 없는 role", mutate: func(m *repository.ClassMembership) { m.Role = repository.ClassRole("OWNER") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			user := newUser(org, repository.OrganizationRoleMember)
			class := store.addClass(org, "Algorithms")
			membership := repository.ClassMembership{OrganizationID: org, ClassID: class.ID, UserID: user.ID, Role: repository.ClassRoleInstructor}
			tt.mutate(&membership)
			store.memberships[[2]uuid.UUID{class.ID, user.ID}] = membership

			got, err := NewService(store).Get(t.Context(), user, class.ID.String())

			if !errors.Is(err, ErrInconsistentData) || got != (View{}) {
				t.Fatalf("Get() = %+v, %v, want zero View, ErrInconsistentData", got, err)
			}
		})
	}
}
