package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/auth"
	"github.com/ktcloud4-SL/labbit-app/internal/server/operation"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

type fakeOperations struct {
	view    operation.View
	err     error
	gotID   string
	gotUser uuid.UUID
}

func (f *fakeOperations) Get(_ context.Context, user repository.User, id string) (operation.View, error) {
	f.gotID = id
	f.gotUser = user.ID
	return f.view, f.err
}
func TestOperationHTTPContract(t *testing.T) {
	a := newFakeAuth()
	a.sessions[auth.SessionToken("operation-session-dummy")] = a.principal
	at := time.Date(2026, 10, 13, 1, 2, 3, 0, time.UTC)
	id := uuid.New()
	target := uuid.New()
	f := &fakeOperations{view: operation.View{ID: id, Type: "RESET", Status: "PENDING", Target: operation.Target{Type: "LAB_INSTANCE", ID: target}, CreatedAt: at, UpdatedAt: at}}
	h, err := New(Options{Auth: a, Classes: &fakeClasses{}, LabSpecs: &fakeLabSpecs{}, Operations: f, PublicOrigin: trustedOrigin})
	if err != nil {
		t.Fatal(err)
	}
	send := func(cookie bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/v1/operations/"+id.String(), nil)
		if cookie {
			r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "operation-session-dummy"})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := send(false); w.Code != 401 {
		t.Fatalf("auth=%d", w.Code)
	}
	w := send(true)
	var body map[string]json.RawMessage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
		t.Fatalf("response=%d %s", w.Code, w.Body)
	}
	for _, key := range []string{"id", "type", "status", "target", "createdAt", "updatedAt"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("missing required %s", key)
		}
	}
	for _, key := range []string{"stage", "error", "startedAt", "finishedAt", "traceparent", "requestFingerprint"} {
		if _, ok := body[key]; ok {
			t.Fatalf("unexpected %s", key)
		}
	}
	if f.gotID != id.String() || f.gotUser != a.principal.User.ID || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("request boundary")
	}
	var response operationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Target.ID != target.String() || response.Status != "PENDING" || !response.UpdatedAt.Equal(at) {
		t.Fatalf("projection: %+v %v", response, err)
	}
	f.view.Status = "FAILED"
	f.view.Stage = "FINALIZING"
	f.view.StartedAt = &at
	f.view.FinishedAt = &at
	f.view.Error = &operation.SafeError{Code: "operation_failed", Message: "작업을 완료하지 못했습니다."}
	w = send(true)
	if !strings.Contains(w.Body.String(), `"error"`) || !strings.Contains(w.Body.String(), `"startedAt"`) || !strings.Contains(w.Body.String(), `"stage":"FINALIZING"`) {
		t.Fatal(w.Body.String())
	}
	for _, tt := range []struct {
		err    error
		status int
	}{{operation.ErrForbidden, 403}, {operation.ErrNotFound, 404}, {errors.New("password=provider-raw-dummy"), 500}} {
		f.err = tt.err
		w = send(true)
		if w.Code != tt.status || strings.Contains(w.Body.String(), "provider-raw-dummy") || strings.Contains(w.Body.String(), "FINALIZING") {
			t.Fatalf("error: %d %s", w.Code, w.Body)
		}
	}
}
