package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/server/livesession"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// LiveSession 관련 Problem code다.
const (
	codeLiveSessionActive     = "live_session_active"
	codeLiveSourceUnavailable = "live_source_unavailable"
	codeLiveUnavailable       = "live_unavailable"
)

// LiveSessions는 handler가 사용하는 LiveSession use case다. *livesession.Service가 구현한다.
type LiveSessions interface {
	Create(ctx context.Context, user repository.User, sourceTerminalSessionID string) (repository.LiveSession, error)
	GetActive(ctx context.Context, user repository.User, classID string) (repository.LiveSession, error)
	Close(ctx context.Context, user repository.User, liveSessionID string) error
}

type unavailableLiveSessions struct{}

func (unavailableLiveSessions) Create(context.Context, repository.User, string) (repository.LiveSession, error) {
	return repository.LiveSession{}, livesession.ErrUnavailable
}

func (unavailableLiveSessions) GetActive(context.Context, repository.User, string) (repository.LiveSession, error) {
	return repository.LiveSession{}, livesession.ErrUnavailable
}

func (unavailableLiveSessions) Close(context.Context, repository.User, string) error {
	return livesession.ErrUnavailable
}

type liveSessionResponse struct {
	ID                      string    `json:"id"`
	ClassID                 string    `json:"classId"`
	SourceTerminalSessionID string    `json:"sourceTerminalSessionId"`
	CreatedAt               time.Time `json:"createdAt"`
}

func (a *api) createLiveSession(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFrom(r.Context())
	if !ok {
		a.internalError(w, r, "create_live_session", errors.New("인증 context가 없습니다"))
		return
	}

	session, err := a.liveSessions.Create(r.Context(), principal.User, r.PathValue("terminalSessionId"))
	if err != nil {
		a.liveError(w, r, "create_live_session", err)
		return
	}

	writeJSON(w, http.StatusCreated, liveSessionResponse{
		ID:                      session.ID.String(),
		ClassID:                 session.ClassID.String(),
		SourceTerminalSessionID: session.SourceTerminalSessionID.String(),
		CreatedAt:               session.CreatedAt,
	})
}

func (a *api) getActiveLiveSession(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFrom(r.Context())
	if !ok {
		a.internalError(w, r, "get_active_live_session", errors.New("인증 context가 없습니다"))
		return
	}

	session, err := a.liveSessions.GetActive(r.Context(), principal.User, r.PathValue("classId"))
	if err != nil {
		a.liveError(w, r, "get_active_live_session", err)
		return
	}

	writeJSON(w, http.StatusOK, liveSessionResponse{
		ID:                      session.ID.String(),
		ClassID:                 session.ClassID.String(),
		SourceTerminalSessionID: session.SourceTerminalSessionID.String(),
		CreatedAt:               session.CreatedAt,
	})
}

func (a *api) closeLiveSession(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFrom(r.Context())
	if !ok {
		a.internalError(w, r, "close_live_session", errors.New("인증 context가 없습니다"))
		return
	}

	if err := a.liveSessions.Close(r.Context(), principal.User, r.PathValue("liveSessionId")); err != nil {
		a.liveError(w, r, "close_live_session", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (a *api) liveError(w http.ResponseWriter, r *http.Request, op string, err error) {
	switch {
	case errors.Is(err, livesession.ErrNotFound):
		writeProblem(w, r, http.StatusNotFound, codeNotFound, "요청한 리소스를 찾을 수 없습니다.")
	case errors.Is(err, livesession.ErrForbidden):
		writeProblem(w, r, http.StatusForbidden, codeForbidden, "이 리소스에 접근할 권한이 없습니다.")
	case errors.Is(err, livesession.ErrLiveActive):
		writeProblem(w, r, http.StatusConflict, codeLiveSessionActive, "해당 강의실에 이미 활성화된 Live 세션이 존재합니다.")
	case errors.Is(err, livesession.ErrSourceUnavailable):
		writeProblem(w, r, http.StatusConflict, codeLiveSourceUnavailable, "Live의 원본 터미널 세션을 사용할 수 없습니다.")
	case errors.Is(err, livesession.ErrUnavailable):
		writeProblem(w, r, http.StatusServiceUnavailable, codeLiveUnavailable, "이 환경에서는 Live 세션을 사용할 수 없습니다.")
	default:
		a.internalError(w, r, op, err)
	}
}
