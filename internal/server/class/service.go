// Package class는 인증된 사용자의 Class 조회와 Class resource authorization use case다.
//
// Class 접근은 사용자의 ClassMembership 관계로만 결정한다. users.organization_role(ADMIN|MEMBER)은
// Organization 관리 권한이므로 Class 접근 판정에 사용하지 않는다. 즉 ADMIN도 대상 Class의 Membership이
// 없으면 접근할 수 없다. Repository는 저장된 값을 그대로 전달할 뿐이며 403/404 의미는 이 package가 판단한다.
// HTTP status와 Problem Details는 알지 못한다.
package class

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

var (
	// ErrNotFound는 요청한 Class가 존재하지 않음을 나타낸다. 존재하지 않는 ID와 이 서버가 발급하지 않은
	// 형식의 ID를 구분하지 않는다.
	ErrNotFound = errors.New("class: 찾을 수 없음")
	// ErrForbidden은 Class는 존재하지만 현재 사용자가 접근할 수 없음을 나타낸다.
	ErrForbidden = errors.New("class: 접근 권한 없음")
	// ErrInconsistentData는 저장소가 반환한 관계가 서로 모순됨을 나타낸다. DB 제약상 발생할 수 없으므로
	// 권한 상태 중 하나로 해석하지 않고 내부 오류로 취급한다(fail closed).
	ErrInconsistentData = errors.New("class: 저장된 관계가 서로 모순됨")
)

// Store는 Service가 사용하는 persistence 경계다. postgres.Store가 구현한다.
type Store interface {
	repository.ClassRepository
}

// Service는 Class 조회 use case다.
type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

// View는 현재 사용자 관점의 Class다. MyRole은 해당 Class의 ClassMembership.role이다.
type View struct {
	ID     uuid.UUID
	Name   string
	MyRole repository.ClassRole
}

// List는 user가 ClassMembership으로 참여 중인 Class만 이름, ID 순서로 반환한다.
// 참여 중인 Class가 없으면 빈 목록이며 오류가 아니다. 같은 Organization의 다른 Class는 user가
// ADMIN이어도 포함하지 않는다.
func (s *Service) List(ctx context.Context, user repository.User) ([]View, error) {
	items, err := s.store.ClassesByUser(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("class: 참여 Class 조회: %w", err)
	}

	views := make([]View, 0, len(items))
	for _, item := range items {
		if item.Class.OrganizationID != user.OrganizationID || !item.Role.Valid() {
			return nil, ErrInconsistentData
		}
		views = append(views, View{ID: item.Class.ID, Name: item.Class.Name, MyRole: item.Role})
	}
	return views, nil
}

// Get은 user가 접근할 수 있는 Class를 반환한다.
//
// classID는 client 입장에서 opaque하다. 현재 physical ID인 UUID의 canonical 형식이 아니면 존재하지 않는
// Class와 같은 ErrNotFound다. Class가 없으면 ErrNotFound, 있지만 다른 Organization의 Class이거나
// Membership이 없으면 ErrForbidden이다.
func (s *Service) Get(ctx context.Context, user repository.User, classID string) (View, error) {
	id, ok := parseID(classID)
	if !ok {
		return View{}, ErrNotFound
	}

	class, err := s.store.ClassByID(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return View{}, ErrNotFound
	}
	if err != nil {
		return View{}, fmt.Errorf("class: Class 조회: %w", err)
	}
	if class.OrganizationID != user.OrganizationID {
		return View{}, ErrForbidden
	}

	membership, err := s.store.ClassMembership(ctx, class.ID, user.ID)
	if errors.Is(err, repository.ErrNotFound) {
		return View{}, ErrForbidden
	}
	if err != nil {
		return View{}, fmt.Errorf("class: Membership 조회: %w", err)
	}
	if membership.ClassID != class.ID || membership.UserID != user.ID ||
		membership.OrganizationID != class.OrganizationID || !membership.Role.Valid() {
		return View{}, ErrInconsistentData
	}
	return View{ID: class.ID, Name: class.Name, MyRole: membership.Role}, nil
}

// parseID는 이 서버가 발급하는 canonical UUID 문자열만 받아들인다. uuid.Parse는 braces, urn: prefix,
// 하이픈 없는 형식, 대문자도 허용하는데, 하나의 Class가 여러 ID 표기로 보이지 않게 한다.
func parseID(raw string) (uuid.UUID, bool) {
	id, err := uuid.Parse(raw)
	if err != nil || id.String() != raw {
		return uuid.UUID{}, false
	}
	return id, true
}
