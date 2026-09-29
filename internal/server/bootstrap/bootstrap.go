// Package bootstrap은 D-11의 trusted operator Bootstrap use case다.
//
// Organization, User(Local Account), Class, ClassMembership을 하나의 transaction으로 사전 구성한다.
// Schema Migration과 운영 데이터 Bootstrap은 분리하므로 db/migrations에는 이 데이터를 INSERT하지 않는다.
//
// Password 원문을 받거나 해시하지 않는다. PasswordHash는 호출자가 준비한 PHC 문자열이다.
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// ErrInvalidSpec은 DB에 접근하기 전에 확인할 수 있는 잘못된 입력이다.
// 메시지에는 Password hash 같은 입력 값을 포함하지 않는다.
var ErrInvalidSpec = errors.New("bootstrap: 잘못된 spec")

// Spec은 하나의 Organization과 그 소속 데이터다. 모든 ID는 호출자가 생성한다.
// User, Class, ClassMembership의 Organization은 항상 Organization으로 정해지므로 별도로 받지 않는다.
type Spec struct {
	Organization Organization
	Users        []User
	Classes      []Class
	Memberships  []Membership
}

type Organization struct {
	ID   uuid.UUID
	Name string
}

// User는 사전 생성되는 Local Account 사용자다.
type User struct {
	ID               uuid.UUID
	Username         string
	PasswordHash     repository.PasswordHash
	OrganizationRole repository.OrganizationRole
}

type Class struct {
	ID   uuid.UUID
	Name string
}

// Membership은 Spec 안의 User와 Class를 ID로 연결한다.
type Membership struct {
	ClassID uuid.UUID
	UserID  uuid.UUID
	Role    repository.ClassRole
}

// Run은 spec 전체를 하나의 transaction으로 저장한다. 어느 단계든 실패하면 아무것도 남기지 않는다.
// 중복이나 제약 위반은 repository.ErrConflict, repository.ErrConstraintViolation으로 반환한다.
func Run(ctx context.Context, tx repository.Transactor, spec Spec) error {
	if err := spec.validate(); err != nil {
		return err
	}

	return tx.WithinTransaction(ctx, func(ctx context.Context, repos repository.Repositories) error {
		organizationID := spec.Organization.ID
		if err := repos.CreateOrganization(ctx, repository.NewOrganization{
			ID:   organizationID,
			Name: spec.Organization.Name,
		}); err != nil {
			return err
		}
		for _, user := range spec.Users {
			if err := repos.CreateUser(ctx, repository.NewUser{
				ID:               user.ID,
				OrganizationID:   organizationID,
				OrganizationRole: user.OrganizationRole,
			}); err != nil {
				return err
			}
			if err := repos.CreateLocalAccount(ctx, repository.NewLocalAccount{
				UserID:       user.ID,
				Username:     user.Username,
				PasswordHash: user.PasswordHash,
			}); err != nil {
				return err
			}
		}
		for _, class := range spec.Classes {
			if err := repos.CreateClass(ctx, repository.NewClass{
				ID:             class.ID,
				OrganizationID: organizationID,
				Name:           class.Name,
			}); err != nil {
				return err
			}
		}
		for _, membership := range spec.Memberships {
			if err := repos.CreateClassMembership(ctx, repository.NewClassMembership{
				OrganizationID: organizationID,
				ClassID:        membership.ClassID,
				UserID:         membership.UserID,
				Role:           membership.Role,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// validate는 DB가 마지막 안전망으로 막더라도 operator가 즉시 이해할 수 있는 입력 오류를 먼저 거른다.
// Username 중복처럼 DB 상태에 의존하는 규칙은 DB 제약이 판단한다.
func (s Spec) validate() error {
	if s.Organization.ID == uuid.Nil {
		return invalid("Organization ID가 필요합니다")
	}
	if blank(s.Organization.Name) {
		return invalid("Organization 이름이 필요합니다")
	}

	users := make(map[uuid.UUID]bool, len(s.Users))
	for i, user := range s.Users {
		switch {
		case user.ID == uuid.Nil:
			return invalid("users[%d] ID가 필요합니다", i)
		case blank(user.Username):
			return invalid("users[%d] username이 필요합니다", i)
		case blank(string(user.PasswordHash)):
			return invalid("users[%d] password hash가 필요합니다", i)
		case !user.OrganizationRole.Valid():
			return invalid("users[%d] organizationRole이 올바르지 않습니다", i)
		}
		users[user.ID] = true
	}

	classes := make(map[uuid.UUID]bool, len(s.Classes))
	for i, class := range s.Classes {
		switch {
		case class.ID == uuid.Nil:
			return invalid("classes[%d] ID가 필요합니다", i)
		case blank(class.Name):
			return invalid("classes[%d] 이름이 필요합니다", i)
		}
		classes[class.ID] = true
	}

	for i, membership := range s.Memberships {
		switch {
		case !classes[membership.ClassID]:
			return invalid("memberships[%d]가 spec에 없는 Class를 참조합니다", i)
		case !users[membership.UserID]:
			return invalid("memberships[%d]가 spec에 없는 User를 참조합니다", i)
		case !membership.Role.Valid():
			return invalid("memberships[%d] role이 올바르지 않습니다", i)
		}
	}
	return nil
}

func blank(value string) bool {
	return strings.TrimSpace(value) == ""
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidSpec, fmt.Sprintf(format, args...))
}
