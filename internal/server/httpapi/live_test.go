package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/livesession"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

const liveCookie = "live-test-session-cookie"

type fakeLiveSessions struct {
	created   repository.LiveSession
	createErr error

	active    repository.LiveSession
	activeErr error

	closeErr error
}

func (f *fakeLiveSessions) Create(_ context.Context, _ repository.User, _ string) (repository.LiveSession, error) {
	return f.created, f.createErr
}

func (f *fakeLiveSessions) GetActive(_ context.Context, _ repository.User, _ string) (repository.LiveSession, error) {
	return f.active, f.activeErr
}

func (f *fakeLiveSessions) Close(_ context.Context, _ repository.User, _ string) error {
	return f.closeErr
}

type liveHarness struct {
	liveSessions *fakeLiveSessions
	liveID       uuid.UUID
	classID      uuid.UUID
	termID       uuid.UUID
}

func newLiveHarness(t *testing.T, liveSvc LiveSessions) (*liveHarness, http.Handler) {
	t.Helper()
	fake := newFakeAuth()
	fake.sessions[liveCookie] = fake.principal
	classes := &fakeClasses{}
	logs := &bytes.Buffer{}
	h := &liveHarness{
		liveID:  uuid.New(),
		classID: uuid.New(),
		termID:  uuid.New(),
	}
	if fakeSvc, ok := liveSvc.(*fakeLiveSessions); ok {
		h.liveSessions = fakeSvc
	}
	handler, err := New(Options{
		Auth:         fake,
		Classes:      classes,
		LiveSessions: liveSvc,
		PublicOrigin: trustedOrigin,
		Logger:       slog.New(slog.NewJSONHandler(logs, nil)),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return h, handler
}

func TestCreateLiveSession(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)

	t.Run("성공: 201 Created", func(t *testing.T) {
		fake := &fakeLiveSessions{}
		h, handler := newLiveHarness(t, fake)
		fake.created = repository.LiveSession{
			ID:                      h.liveID,
			ClassID:                 h.classID,
			SourceTerminalSessionID: h.termID,
			CreatedAt:               now,
		}

		req := httptest.NewRequest(http.MethodPost, "/api/v1/terminal-sessions/"+h.termID.String()+"/live-sessions", nil)
		req.Header.Set("Origin", trustedOrigin)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: liveCookie})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201", rec.Code)
		}

		var resp liveSessionResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal error = %v", err)
		}
		if resp.ID != h.liveID.String() || resp.ClassID != h.classID.String() || resp.SourceTerminalSessionID != h.termID.String() {
			t.Fatalf("unexpected body: %+v", resp)
		}
	})

	t.Run("실패: 401 Unauthenticated", func(t *testing.T) {
		fake := &fakeLiveSessions{}
		h, handler := newLiveHarness(t, fake)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/terminal-sessions/"+h.termID.String()+"/live-sessions", nil)
		req.Header.Set("Origin", trustedOrigin)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("실패: 403 Forbidden", func(t *testing.T) {
		fake := &fakeLiveSessions{createErr: livesession.ErrForbidden}
		h, handler := newLiveHarness(t, fake)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/terminal-sessions/"+h.termID.String()+"/live-sessions", nil)
		req.Header.Set("Origin", trustedOrigin)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: liveCookie})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("실패: 404 Not Found", func(t *testing.T) {
		fake := &fakeLiveSessions{createErr: livesession.ErrNotFound}
		h, handler := newLiveHarness(t, fake)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/terminal-sessions/"+h.termID.String()+"/live-sessions", nil)
		req.Header.Set("Origin", trustedOrigin)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: liveCookie})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("실패: 409 Conflict (live_session_active)", func(t *testing.T) {
		fake := &fakeLiveSessions{createErr: livesession.ErrLiveActive}
		h, handler := newLiveHarness(t, fake)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/terminal-sessions/"+h.termID.String()+"/live-sessions", nil)
		req.Header.Set("Origin", trustedOrigin)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: liveCookie})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", rec.Code)
		}

		var prob problemDetails
		_ = json.Unmarshal(rec.Body.Bytes(), &prob)
		if prob.Code != codeLiveSessionActive {
			t.Fatalf("prob.Code = %q, want %q", prob.Code, codeLiveSessionActive)
		}
	})

	t.Run("실패: 409 Conflict (live_source_unavailable)", func(t *testing.T) {
		fake := &fakeLiveSessions{createErr: livesession.ErrSourceUnavailable}
		h, handler := newLiveHarness(t, fake)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/terminal-sessions/"+h.termID.String()+"/live-sessions", nil)
		req.Header.Set("Origin", trustedOrigin)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: liveCookie})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", rec.Code)
		}

		var prob problemDetails
		_ = json.Unmarshal(rec.Body.Bytes(), &prob)
		if prob.Code != codeLiveSourceUnavailable {
			t.Fatalf("prob.Code = %q, want %q", prob.Code, codeLiveSourceUnavailable)
		}
	})

	t.Run("실패: 503 Service Unavailable (live_unavailable)", func(t *testing.T) {
		// LiveSessions not configured (nil)
		h, handler := newLiveHarness(t, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/terminal-sessions/"+h.termID.String()+"/live-sessions", nil)
		req.Header.Set("Origin", trustedOrigin)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: liveCookie})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", rec.Code)
		}

		var prob problemDetails
		_ = json.Unmarshal(rec.Body.Bytes(), &prob)
		if prob.Code != codeLiveUnavailable {
			t.Fatalf("prob.Code = %q, want %q", prob.Code, codeLiveUnavailable)
		}
	})
}

func TestGetActiveLiveSession(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)

	t.Run("성공: 200 OK", func(t *testing.T) {
		fake := &fakeLiveSessions{}
		h, handler := newLiveHarness(t, fake)
		fake.active = repository.LiveSession{
			ID:                      h.liveID,
			ClassID:                 h.classID,
			SourceTerminalSessionID: h.termID,
			CreatedAt:               now,
		}

		req := httptest.NewRequest(http.MethodGet, "/api/v1/classes/"+h.classID.String()+"/live-session", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: liveCookie})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}

		var resp liveSessionResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if resp.ID != h.liveID.String() || resp.ClassID != h.classID.String() {
			t.Fatalf("unexpected body: %+v", resp)
		}
	})

	t.Run("실패: 404 Not Found", func(t *testing.T) {
		fake := &fakeLiveSessions{activeErr: livesession.ErrNotFound}
		h, handler := newLiveHarness(t, fake)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/classes/"+h.classID.String()+"/live-session", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: liveCookie})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("실패: 403 Forbidden", func(t *testing.T) {
		fake := &fakeLiveSessions{activeErr: livesession.ErrForbidden}
		h, handler := newLiveHarness(t, fake)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/classes/"+h.classID.String()+"/live-session", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: liveCookie})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})
}

func TestCloseLiveSession(t *testing.T) {
	t.Run("성공: 204 No Content", func(t *testing.T) {
		fake := &fakeLiveSessions{}
		h, handler := newLiveHarness(t, fake)

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/live-sessions/"+h.liveID.String(), nil)
		req.Header.Set("Origin", trustedOrigin)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: liveCookie})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
	})

	t.Run("실패: 403 Forbidden", func(t *testing.T) {
		fake := &fakeLiveSessions{closeErr: livesession.ErrForbidden}
		h, handler := newLiveHarness(t, fake)

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/live-sessions/"+h.liveID.String(), nil)
		req.Header.Set("Origin", trustedOrigin)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: liveCookie})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("실패: 404 Not Found", func(t *testing.T) {
		fake := &fakeLiveSessions{closeErr: livesession.ErrNotFound}
		h, handler := newLiveHarness(t, fake)

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/live-sessions/"+h.liveID.String(), nil)
		req.Header.Set("Origin", trustedOrigin)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: liveCookie})
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}
