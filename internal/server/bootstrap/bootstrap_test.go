package bootstrap_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/bootstrap"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// fakeTransactor는 commit/rollback 없이 callback만 실행한다. transaction 의미는 PostgreSQL Integration Test가 검증한다.
type fakeTransactor struct {
	repos repository.Repositories
	calls int
}

func (f *fakeTransactor) WithinTransaction(ctx context.Context, fn func(context.Context, repository.Repositories) error) error {
	f.calls++
	return fn(ctx, f.repos)
}

// recordingRepos는 Bootstrap이 사용하는 Create*만 구현한다. 나머지는 nil interface라 호출되면 panic한다.
type recordingRepos struct {
	repository.Repositories

	organizations []repository.NewOrganization
	users         []repository.NewUser
	accounts      []repository.NewLocalAccount
	classes       []repository.NewClass
	memberships   []repository.NewClassMembership

	accountErr error
}

func (r *recordingRepos) CreateOrganization(_ context.Context, v repository.NewOrganization) error {
	r.organizations = append(r.organizations, v)
	return nil
}

func (r *recordingRepos) CreateUser(_ context.Context, v repository.NewUser) error {
	r.users = append(r.users, v)
	return nil
}

func (r *recordingRepos) CreateLocalAccount(_ context.Context, v repository.NewLocalAccount) error {
	if r.accountErr != nil {
		return r.accountErr
	}
	r.accounts = append(r.accounts, v)
	return nil
}

func (r *recordingRepos) CreateClass(_ context.Context, v repository.NewClass) error {
	r.classes = append(r.classes, v)
	return nil
}

func (r *recordingRepos) CreateClassMembership(_ context.Context, v repository.NewClassMembership) error {
	r.memberships = append(r.memberships, v)
	return nil
}

var (
	organizationID = uuid.MustParse("00000000-0000-4000-8000-000000000001")
	instructorID   = uuid.MustParse("00000000-0000-4000-8000-000000000011")
	studentID      = uuid.MustParse("00000000-0000-4000-8000-000000000012")
	classID        = uuid.MustParse("00000000-0000-4000-8000-000000000021")
)

func validSpec() bootstrap.Spec {
	return bootstrap.Spec{
		Organization: bootstrap.Organization{ID: organizationID, Name: "Labbit Academy"},
		Users: []bootstrap.User{
			{ID: instructorID, Username: "instructor", PasswordHash: "dummy-phc-1", OrganizationRole: repository.OrganizationRoleAdmin},
			{ID: studentID, Username: "student", PasswordHash: "dummy-phc-2", OrganizationRole: repository.OrganizationRoleMember},
		},
		Classes: []bootstrap.Class{{ID: classID, Name: "Linux 101"}},
		Memberships: []bootstrap.Membership{
			{ClassID: classID, UserID: instructorID, Role: repository.ClassRoleInstructor},
			{ClassID: classID, UserID: studentID, Role: repository.ClassRoleStudent},
		},
	}
}

func TestRunRejectsInvalidSpecBeforeOpeningTransaction(t *testing.T) {
	unknown := uuid.MustParse("00000000-0000-4000-8000-0000000000ff")
	tests := []struct {
		name   string
		mutate func(*bootstrap.Spec)
	}{
		{"Organization ID 없음", func(s *bootstrap.Spec) { s.Organization.ID = uuid.Nil }},
		{"Organization 이름 공백", func(s *bootstrap.Spec) { s.Organization.Name = " \t" }},
		{"User ID 없음", func(s *bootstrap.Spec) { s.Users[0].ID = uuid.Nil }},
		{"username 공백", func(s *bootstrap.Spec) { s.Users[0].Username = "  " }},
		{"password hash 없음", func(s *bootstrap.Spec) { s.Users[0].PasswordHash = "" }},
		{"organizationRole 오류", func(s *bootstrap.Spec) { s.Users[0].OrganizationRole = "OWNER" }},
		{"Class ID 없음", func(s *bootstrap.Spec) { s.Classes[0].ID = uuid.Nil }},
		{"Class 이름 공백", func(s *bootstrap.Spec) { s.Classes[0].Name = "" }},
		{"spec에 없는 Class 참조", func(s *bootstrap.Spec) { s.Memberships[0].ClassID = unknown }},
		{"spec에 없는 User 참조", func(s *bootstrap.Spec) { s.Memberships[0].UserID = unknown }},
		{"Class role 오류", func(s *bootstrap.Spec) { s.Memberships[0].Role = "ADMIN" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repos := &recordingRepos{}
			tx := &fakeTransactor{repos: repos}
			spec := validSpec()
			tt.mutate(&spec)

			err := bootstrap.Run(t.Context(), tx, spec)

			if !errors.Is(err, bootstrap.ErrInvalidSpec) {
				t.Fatalf("Run() error = %v, want ErrInvalidSpec", err)
			}
			if tx.calls != 0 || len(repos.organizations) != 0 {
				t.Fatalf("잘못된 spec이 transaction을 열었거나 저장했습니다: calls=%d", tx.calls)
			}
		})
	}
}

func TestRunInvalidSpecErrorDoesNotEchoPasswordHash(t *testing.T) {
	spec := validSpec()
	spec.Users[0].Username = ""
	spec.Users[0].PasswordHash = "super-secret-phc"

	err := bootstrap.Run(t.Context(), &fakeTransactor{repos: &recordingRepos{}}, spec)

	if err == nil || strings.Contains(err.Error(), "super-secret-phc") {
		t.Fatalf("오류가 password hash를 노출합니다: %v", err)
	}
}

func TestRunStoresEverythingUnderTheSpecOrganization(t *testing.T) {
	repos := &recordingRepos{}
	tx := &fakeTransactor{repos: repos}

	if err := bootstrap.Run(t.Context(), tx, validSpec()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if tx.calls != 1 {
		t.Fatalf("transaction 수 = %d, want 1", tx.calls)
	}
	if len(repos.organizations) != 1 || repos.organizations[0].ID != organizationID || repos.organizations[0].Name != "Labbit Academy" {
		t.Fatalf("organizations = %+v", repos.organizations)
	}
	if len(repos.users) != 2 || len(repos.accounts) != 2 || len(repos.classes) != 1 || len(repos.memberships) != 2 {
		t.Fatalf("저장 개수 users=%d accounts=%d classes=%d memberships=%d", len(repos.users), len(repos.accounts), len(repos.classes), len(repos.memberships))
	}
	// 모든 하위 데이터가 spec의 Organization에 묶여 Organization 경계를 넘는 조합이 만들어지지 않는다.
	for _, user := range repos.users {
		if user.OrganizationID != organizationID {
			t.Errorf("user %s Organization = %s", user.ID, user.OrganizationID)
		}
	}
	for _, class := range repos.classes {
		if class.OrganizationID != organizationID {
			t.Errorf("class %s Organization = %s", class.ID, class.OrganizationID)
		}
	}
	for _, membership := range repos.memberships {
		if membership.OrganizationID != organizationID {
			t.Errorf("membership Organization = %s", membership.OrganizationID)
		}
	}
	// hash는 가공하지 않고 그대로 저장 경계로 전달한다.
	if repos.accounts[0].Username != "instructor" || string(repos.accounts[0].PasswordHash) != "dummy-phc-1" {
		t.Errorf("account = %+v", repos.accounts[0])
	}
	if repos.users[0].OrganizationRole != repository.OrganizationRoleAdmin || repos.memberships[1].Role != repository.ClassRoleStudent {
		t.Errorf("역할이 spec과 다릅니다: %+v %+v", repos.users[0], repos.memberships[1])
	}
}

func TestRunReturnsRepositoryErrorAndStopsAtFirstFailure(t *testing.T) {
	repos := &recordingRepos{accountErr: &repository.Error{Kind: repository.KindConflict, Op: "CreateLocalAccount"}}

	err := bootstrap.Run(t.Context(), &fakeTransactor{repos: repos}, validSpec())

	if !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("Run() error = %v, want repository.ErrConflict", err)
	}
	// rollback은 Transactor 책임이다. use case는 실패 이후 단계를 진행하지 않는다.
	if len(repos.classes) != 0 || len(repos.memberships) != 0 {
		t.Fatalf("실패 이후 단계가 실행되었습니다: classes=%d memberships=%d", len(repos.classes), len(repos.memberships))
	}
}
