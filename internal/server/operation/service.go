package operation

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

type Store interface {
	repository.OperationRepository
	repository.ClassRepository
}
type Service struct{ store Store }

func NewService(store Store) *Service { return &Service{store: store} }

type Target struct {
	Type string
	ID   uuid.UUID
}
type SafeError struct{ Code, Message string }
type View struct {
	ID                    uuid.UUID
	Type, Status, Stage   string
	Target                Target
	Error                 *SafeError
	CreatedAt, UpdatedAt  time.Time
	StartedAt, FinishedAt *time.Time
}

func (s *Service) Get(ctx context.Context, user repository.User, id string) (View, error) {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil || parsed.String() != id {
		return View{}, ErrNotFound
	}
	o, err := s.store.OperationByID(ctx, parsed)
	if errors.Is(err, repository.ErrNotFound) {
		return View{}, ErrNotFound
	}
	if err != nil {
		return View{}, err
	}
	if user.ID == uuid.Nil || user.OrganizationID != o.OrganizationID {
		return View{}, ErrForbidden
	}
	target, err := s.store.OperationTarget(ctx, o)
	if errors.Is(err, repository.ErrNotFound) {
		return View{}, ErrInconsistentData
	}
	if err != nil {
		return View{}, err
	}
	if target.OrganizationID != o.OrganizationID {
		return View{}, ErrInconsistentData
	}
	m, err := s.store.ClassMembership(ctx, target.ClassID, user.ID)
	if errors.Is(err, repository.ErrNotFound) {
		return View{}, ErrForbidden
	}
	if err != nil {
		return View{}, err
	}
	if m.OrganizationID != user.OrganizationID || m.ClassID != target.ClassID || m.UserID != user.ID || !m.Role.Valid() {
		return View{}, ErrInconsistentData
	}
	// Class operation reads require current instructor membership. Instance operation reads
	// also permit its owner; Organization ADMIN and the original requester never bypass membership.
	if m.Role != repository.ClassRoleInstructor && (target.OwnerUserID == nil || *target.OwnerUserID != user.ID) {
		return View{}, ErrForbidden
	}
	v := View{ID: o.ID, Type: o.Type, Status: o.Status, Stage: o.Stage, CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt, StartedAt: o.StartedAt, FinishedAt: o.FinishedAt}
	switch o.Type {
	case "RESET":
		if o.LabInstanceID == nil || *o.LabInstanceID == uuid.Nil {
			return View{}, ErrInconsistentData
		}
		v.Target = Target{Type: "LAB_INSTANCE", ID: *o.LabInstanceID}
	case "PROVISION", "CLEANUP":
		if o.LabExecutionID == nil || *o.LabExecutionID == uuid.Nil {
			return View{}, ErrInconsistentData
		}
		v.Target = Target{Type: "LAB_EXECUTION", ID: *o.LabExecutionID}
	default:
		return View{}, ErrInconsistentData
	}
	// Stored error text and arbitrary provider error codes are untrusted. Until workers
	// own a reviewed public error vocabulary, expose only a stable generic safe summary.
	if o.Status == "FAILED" {
		v.Error = &SafeError{Code: "operation_failed", Message: "작업을 완료하지 못했습니다."}
	}
	return v, nil
}
