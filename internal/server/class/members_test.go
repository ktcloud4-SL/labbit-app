package class

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

func namedUser(orgID uuid.UUID, role repository.OrganizationRole, username string) repository.User {
	u := newUser(orgID, role)
	u.Username = username
	return u
}

func TestMembersReturnsAllParticipantsForInstructorAndStudent(t *testing.T) {
	org := uuid.New()
	store := newFakeStore()
	target := store.addClass(org, "A1")
	other := store.addClass(org, "A2")
	instructor := namedUser(org, repository.OrganizationRoleMember, "irene")
	studentB := namedUser(org, repository.OrganizationRoleMember, "bob")
	studentA := namedUser(org, repository.OrganizationRoleMember, "amy")
	outsider := namedUser(org, repository.OrganizationRoleMember, "zed")
	store.addMembership(target, instructor, repository.ClassRoleInstructor)
	store.addMembership(target, studentB, repository.ClassRoleStudent)
	store.addMembership(target, studentA, repository.ClassRoleStudent)
	// 다른 Class의 Membership은 목록에 들어오지 않는다.
	store.addMembership(other, outsider, repository.ClassRoleStudent)

	want := []Member{
		{UserID: instructor.ID, Username: "irene", Role: repository.ClassRoleInstructor},
		{UserID: studentA.ID, Username: "amy", Role: repository.ClassRoleStudent},
		{UserID: studentB.ID, Username: "bob", Role: repository.ClassRoleStudent},
	}
	for _, caller := range []repository.User{instructor, studentA} {
		got, err := NewService(store).Members(t.Context(), caller, target.ID.String())
		if err != nil {
			t.Fatalf("Members(%s) error = %v", caller.Username, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Members(%s) = %+v, want %+v", caller.Username, got, want)
		}
	}
}

// 접근 판정은 Get과 같다. ADMIN이어도 Membership이 없으면 403이고, 다른 Organization의 Class도 403, 없는 Class는 404다.
func TestMembersUsesClassAccessBoundary(t *testing.T) {
	org := uuid.New()
	store := newFakeStore()
	target := store.addClass(org, "A1")
	foreign := store.addClass(uuid.New(), "B1")
	member := namedUser(org, repository.OrganizationRoleMember, "irene")
	store.addMembership(target, member, repository.ClassRoleInstructor)
	store.addMembership(foreign, namedUser(foreign.OrganizationID, repository.OrganizationRoleMember, "bob"), repository.ClassRoleInstructor)

	tests := []struct {
		name    string
		user    repository.User
		classID string
		want    error
	}{
		{name: "같은 Organization ADMIN이지만 Membership 없음", user: namedUser(org, repository.OrganizationRoleAdmin, "admin"), classID: target.ID.String(), want: ErrForbidden},
		{name: "같은 Organization 비참여 MEMBER", user: namedUser(org, repository.OrganizationRoleMember, "loner"), classID: target.ID.String(), want: ErrForbidden},
		{name: "다른 Organization의 Class", user: member, classID: foreign.ID.String(), want: ErrForbidden},
		{name: "없는 Class", user: member, classID: uuid.NewString(), want: ErrNotFound},
		{name: "canonical이 아닌 ID", user: member, classID: "not-a-uuid", want: ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewService(store).Members(t.Context(), tt.user, tt.classID)
			if !errors.Is(err, tt.want) || got != nil {
				t.Fatalf("Members() = %+v, %v, want nil, %v", got, err, tt.want)
			}
		})
	}
}

func TestMembersFailsClosedOnInconsistentRows(t *testing.T) {
	org := uuid.New()
	tests := []struct {
		name   string
		member repository.ClassMember
	}{
		{name: "다른 Organization의 참여자", member: repository.ClassMember{OrganizationID: uuid.New(), UserID: uuid.New(), Username: "x", Role: repository.ClassRoleStudent}},
		{name: "알 수 없는 role", member: repository.ClassMember{OrganizationID: org, UserID: uuid.New(), Username: "x", Role: "OWNER"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			target := store.addClass(org, "A1")
			caller := namedUser(org, repository.OrganizationRoleMember, "irene")
			store.addMembership(target, caller, repository.ClassRoleInstructor)
			store.members = []repository.ClassMember{tt.member}

			got, err := NewService(store).Members(t.Context(), caller, target.ID.String())

			if !errors.Is(err, ErrInconsistentData) || got != nil {
				t.Fatalf("Members() = %+v, %v, want nil, ErrInconsistentData", got, err)
			}
		})
	}
}

func TestMembersRepositoryFailureIsInternalWithoutRawCause(t *testing.T) {
	org := uuid.New()
	store := newFakeStore()
	target := store.addClass(org, "A1")
	caller := namedUser(org, repository.OrganizationRoleMember, "irene")
	store.addMembership(target, caller, repository.ClassRoleStudent)
	store.membersErr = internalRepoError("ClassMembers")

	got, err := NewService(store).Members(t.Context(), caller, target.ID.String())

	if err == nil || got != nil || errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) {
		t.Fatalf("Members() = %+v, %v, want internal error", got, err)
	}
	if !errors.Is(err, repository.ErrInternal) {
		t.Fatalf("errors.Is(err, repository.ErrInternal) = false: %v", err)
	}
	assertNoRawCause(t, err)
}
