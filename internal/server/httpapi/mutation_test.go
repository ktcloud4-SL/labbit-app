package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/labexecution"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

const (
	resetPath   = "/api/v1/lab-instances/" + acceptedInstanceID + "/reset"
	cleanupPath = "/api/v1/lab-executions/" + acceptedExecutionID + "/cleanup"
)

// Reset/Cleanup은 계약에 request body가 없다. Frontend client처럼 Content-Type 없이 Origin과 Idempotency-Key만 보낸다.
func unsafeNoBody(mods ...func(*http.Request)) []func(*http.Request) {
	return append([]func(*http.Request){withHeader("Origin", trustedOrigin)}, mods...)
}

func TestResetAndCleanupAcceptWith202LocationAndReplayWith200(t *testing.T) {
	for _, tt := range []struct {
		name, path, targetType, targetID string
	}{
		{"reset", resetPath, "LAB_INSTANCE", acceptedInstanceID},
		{"cleanup", cleanupPath, "LAB_EXECUTION", acceptedExecutionID},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeLabExecutions{mutation: labexecution.MutationAccepted{OperationID: uuid.MustParse(acceptedOperationID), TargetID: uuid.MustParse(tt.targetID)}}
			h := newLabExecutionHarness(t, fake)

			rec := h.send(http.MethodPost, tt.path, "", unsafeNoBody(h.signIn(), withHeader("Idempotency-Key", "key-1"))...)
			want := `{"operationId":"` + acceptedOperationID + `","target":{"type":"` + tt.targetType + `","id":"` + tt.targetID + `"}}`
			if rec.Code != http.StatusAccepted || rec.Header().Get("Location") != "/api/v1/operations/"+acceptedOperationID || strings.TrimSpace(rec.Body.String()) != want {
				t.Fatalf("status=%d location=%q body=%s", rec.Code, rec.Header().Get("Location"), rec.Body)
			}

			fake.mutation.Replayed = true
			replay := h.send(http.MethodPost, tt.path, "", unsafeNoBody(h.signIn(), withHeader("Idempotency-Key", "key-1"))...)
			if replay.Code != http.StatusOK || replay.Header().Get("Location") != "" || strings.TrimSpace(replay.Body.String()) != want {
				t.Fatalf("replay status=%d location=%q body=%s", replay.Code, replay.Header().Get("Location"), replay.Body)
			}

			// path의 opaque ID와 key를 그대로 넘기고 request ID를 correlation으로 함께 넘긴다.
			switch tt.name {
			case "reset":
				in := fake.resets[0]
				if in.LabInstanceID != acceptedInstanceID || in.IdempotencyKey != "key-1" || in.RequestID == "" || len(fake.cleanups) != 0 {
					t.Fatalf("reset input=%+v cleanups=%d", in, len(fake.cleanups))
				}
			case "cleanup":
				in := fake.cleanups[0]
				if in.LabExecutionID != acceptedExecutionID || in.IdempotencyKey != "key-1" || in.RequestID == "" || len(fake.resets) != 0 {
					t.Fatalf("cleanup input=%+v resets=%d", in, len(fake.resets))
				}
			}
		})
	}
}

func TestResetAndCleanupRejectMissingOrMalformedKeyBeforeUseCase(t *testing.T) {
	for _, path := range []string{resetPath, cleanupPath} {
		for name, mods := range map[string][]func(*http.Request){
			"missing key":   nil,
			"empty key":     {withHeader("Idempotency-Key", "")},
			"two keys":      {func(r *http.Request) { r.Header.Add("Idempotency-Key", "a"); r.Header.Add("Idempotency-Key", "b") }},
			"oversized key": {withHeader("Idempotency-Key", strings.Repeat("k", 4*255+1))},
		} {
			t.Run(path+" "+name, func(t *testing.T) {
				fake := &fakeLabExecutions{}
				h := newLabExecutionHarness(t, fake)
				rec := h.send(http.MethodPost, path, "", unsafeNoBody(append([]func(*http.Request){h.signIn()}, mods...)...)...)
				requireProblem(t, rec, http.StatusBadRequest, codeInvalidRequest)
				if fake.calls != 0 {
					t.Fatalf("use case called %d times", fake.calls)
				}
			})
		}
	}
}

func TestResetAndCleanupRequireSessionAndOrigin(t *testing.T) {
	for _, path := range []string{resetPath, cleanupPath} {
		fake := &fakeLabExecutions{}
		h := newLabExecutionHarness(t, fake)
		requireProblem(t, h.send(http.MethodPost, path, "", unsafeNoBody(withHeader("Idempotency-Key", "k"))...), http.StatusUnauthorized, codeUnauthenticated)
		requireProblem(t, h.send(http.MethodPost, path, "", h.signIn(), withHeader("Idempotency-Key", "k"), withoutSource), http.StatusForbidden, codeCSRFRejected)
		if fake.calls != 0 {
			t.Fatalf("%s use case called %d times", path, fake.calls)
		}
	}
}

func TestResetAndCleanupErrorsMapToStableProblems(t *testing.T) {
	const leak = "dial tcp 10.0.0.7:5432 password=hunter2 glance-image-raw"
	for _, tt := range []struct {
		err    error
		status int
		code   string
	}{
		{labexecution.ErrInvalidRequest, 400, codeInvalidRequest},
		{labexecution.ErrNotFound, 404, codeNotFound},
		{labexecution.ErrForbidden, 403, codeForbidden},
		{labexecution.ErrIdempotencyConflict, 409, "idempotency_key_reused"},
		{labexecution.ErrMutationInProgress, 409, "operation_in_progress"},
		{labexecution.ErrExecutionFinished, 409, "lab_execution_finished"},
		{labexecution.ErrInvalidResetTarget, 422, "invalid_reset_target"},
		{labexecution.ErrInconsistentData, 500, codeInternal},
		// FK/Check 위반이나 generation 불일치는 409로 숨기지 않는다.
		{&repository.Error{Kind: repository.KindConstraintViolation, Op: "CreateOperationItem"}, 500, codeInternal},
		{&repository.Error{Kind: repository.KindInternal, Op: "LabExecutionForShare", Cause: errors.New(leak)}, 500, codeInternal},
	} {
		for _, path := range []string{resetPath, cleanupPath} {
			h := newLabExecutionHarness(t, &fakeLabExecutions{err: tt.err})
			rec := h.send(http.MethodPost, path, "", unsafeNoBody(h.signIn(), withHeader("Idempotency-Key", "k"))...)
			requireProblem(t, rec, tt.status, tt.code)
			if rec.Header().Get("Location") != "" || strings.Contains(rec.Body.String(), "hunter2") || strings.Contains(h.logs.String(), "hunter2") || strings.Contains(h.logs.String(), "glance-image-raw") {
				t.Fatalf("%s: location=%q body=%s logs=%s", path, rec.Header().Get("Location"), rec.Body, h.logs)
			}
		}
	}
}
