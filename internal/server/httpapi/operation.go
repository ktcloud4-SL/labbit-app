package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/server/operation"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

type Operations interface {
	Get(context.Context, repository.User, string) (operation.View, error)
}
type operationResponse struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"`
	Status     string                  `json:"status"`
	Stage      string                  `json:"stage,omitempty"`
	Target     operationTargetResponse `json:"target"`
	Error      *operationErrorResponse `json:"error,omitempty"`
	CreatedAt  time.Time               `json:"createdAt"`
	UpdatedAt  time.Time               `json:"updatedAt"`
	StartedAt  *time.Time              `json:"startedAt,omitempty"`
	FinishedAt *time.Time              `json:"finishedAt,omitempty"`
}
type operationTargetResponse struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}
type operationErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

func (a *api) getOperation(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		a.internalError(w, r, "get_operation", errors.New("missing principal"))
		return
	}
	if a.operations == nil {
		a.internalError(w, r, "get_operation", errors.New("missing operation service"))
		return
	}
	v, err := a.operations.Get(r.Context(), p.User, r.PathValue("operationId"))
	switch {
	case err == nil:
		body := operationResponse{ID: v.ID.String(), Type: v.Type, Status: v.Status, Stage: v.Stage,
			Target: operationTargetResponse{Type: v.Target.Type, ID: v.Target.ID.String()}, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, StartedAt: v.StartedAt, FinishedAt: v.FinishedAt}
		if v.Error != nil {
			body.Error = &operationErrorResponse{Code: v.Error.Code, Message: v.Error.Message}
		}
		writeJSON(w, http.StatusOK, body)
	case errors.Is(err, operation.ErrForbidden):
		writeProblem(w, r, http.StatusForbidden, codeForbidden, "이 Operation에 접근할 권한이 없습니다.")
	case errors.Is(err, operation.ErrNotFound):
		writeProblem(w, r, http.StatusNotFound, codeNotFound, "요청한 Operation을 찾을 수 없습니다.")
	default:
		a.internalError(w, r, "get_operation", err)
	}
}
