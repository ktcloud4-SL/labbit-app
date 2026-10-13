package operation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

type readStore struct {
	repository.OperationRepository
	repository.ClassRepository
	operation     repository.Operation
	target        repository.OperationTarget
	membership    repository.ClassMembership
	membershipErr error
}

func (f *readStore) OperationByID(_ context.Context, id uuid.UUID) (repository.Operation, error) {
	if id != f.operation.ID {
		return repository.Operation{}, repository.ErrNotFound
	}
	return f.operation, nil
}
func (f *readStore) OperationTarget(context.Context, repository.Operation) (repository.OperationTarget, error) {
	return f.target, nil
}
func (f *readStore) ClassMembership(context.Context, uuid.UUID, uuid.UUID) (repository.ClassMembership, error) {
	return f.membership, f.membershipErr
}
func TestGetAuthorizationAndSafeProjection(t *testing.T) {
	in := input()
	user := repository.User{ID: in.RequestedByUserID, OrganizationID: in.OrganizationID, OrganizationRole: repository.OrganizationRoleAdmin}
	class := uuid.New()
	at := time.Now()
	f := &readStore{operation: repository.Operation{ID: uuid.New(), OrganizationID: in.OrganizationID, Type: "PROVISION", Status: "FAILED", LabExecutionID: in.LabExecutionID, Stage: "FINALIZING", ErrorCode: "password=provider-raw-dummy", ErrorMessage: "credential=secret-dummy", CreatedAt: at, UpdatedAt: at.Add(time.Second)}, target: repository.OperationTarget{OrganizationID: in.OrganizationID, ClassID: class}, membership: repository.ClassMembership{OrganizationID: in.OrganizationID, ClassID: class, UserID: user.ID, Role: repository.ClassRoleInstructor}}
	s := NewService(f)
	v, err := s.Get(t.Context(), user, f.operation.ID.String())
	if err != nil || v.Error == nil || v.Error.Code != "operation_failed" || v.Error.Message == f.operation.ErrorMessage || v.Target.ID != *in.LabExecutionID || v.Target.Type != "LAB_EXECUTION" || !v.UpdatedAt.Equal(f.operation.UpdatedAt) {
		t.Fatalf("projection: %+v %v", v, err)
	}
	for _, id := range []string{"not-uuid", uuid.Nil.String(), uuid.NewString()} {
		if _, err := s.Get(t.Context(), user, id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("404: %v", err)
		}
	}
	foreign := user
	foreign.OrganizationID = uuid.New()
	if _, err := s.Get(t.Context(), foreign, f.operation.ID.String()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("tenant: %v", err)
	}
	f.membershipErr = repository.ErrNotFound
	if _, err := s.Get(t.Context(), user, f.operation.ID.String()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("membership: %v", err)
	}
	f.membershipErr = repository.ErrInternal
	if _, err := s.Get(t.Context(), user, f.operation.ID.String()); !errors.Is(err, repository.ErrInternal) {
		t.Fatalf("outage is forbidden: %v", err)
	}
	f.membershipErr = nil
	f.membership.Role = repository.ClassRoleStudent
	if _, err := s.Get(t.Context(), user, f.operation.ID.String()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ADMIN bypass: %v", err)
	}
	instance := in.Items[0].LabInstanceID
	f.operation.Type = "RESET"
	f.operation.LabInstanceID = &instance
	f.operation.LabExecutionID = nil
	f.target.OwnerUserID = &user.ID
	v, err = s.Get(t.Context(), user, f.operation.ID.String())
	if err != nil || v.Target.Type != "LAB_INSTANCE" || v.Target.ID != instance {
		t.Fatalf("owner reset: %+v %v", v, err)
	}
	peer := uuid.New()
	f.target.OwnerUserID = &peer
	if _, err := s.Get(t.Context(), user, f.operation.ID.String()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("peer: %v", err)
	}
	f.membership.Role = repository.ClassRoleInstructor
	if _, err := s.Get(t.Context(), user, f.operation.ID.String()); err != nil {
		t.Fatalf("instructor instance read: %v", err)
	}
	f.operation.Status = "SUCCEEDED"
	v, err = s.Get(t.Context(), user, f.operation.ID.String())
	if err != nil || v.Error != nil {
		t.Fatal("stale error leaked on success")
	}
}
