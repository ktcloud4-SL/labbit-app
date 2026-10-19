package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type failingEnrollment struct{ Connectors }

func (failingEnrollment) Enroll(_ context.Context, t connector.EnrollmentToken) (connector.EnrollmentCreated, error) {
	return connector.EnrollmentCreated{}, fmt.Errorf("adapter failure with raw input: %s", string(t))
}

func TestEnrollmentFailureDoesNotEchoTokenInResponseLogsOrTrace(t *testing.T) {
	const token = "test-only-enrollment-secret-marker"
	auth := newFakeAuth()
	var logs bytes.Buffer
	spans := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	defer provider.Shutdown(t.Context())
	handler, err := New(Options{Auth: auth, Classes: &fakeClasses{}, LabSpecs: &fakeLabSpecs{}, Connectors: failingEnrollment{}, PublicOrigin: trustedOrigin, Logger: slog.New(slog.NewJSONHandler(&logs, nil)), Tracer: provider.Tracer("enrollment-test")})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", trustedOrigin+"/api/v1/connector-enrollments", strings.NewReader(`{"enrollmentToken":"`+token+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "ignored-cookie"})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 500 {
		t.Fatalf("status=%d", w.Code)
	}
	if auth.authenticates != 0 {
		t.Fatal("Installer used Cookie authentication")
	}
	for _, text := range []string{w.Body.String(), logs.String(), fmt.Sprint(spans.Ended()[0].Attributes(), spans.Ended()[0].Events())} {
		if strings.Contains(text, token) {
			t.Fatal("enrollment error leaked Token")
		}
	}
	// The Origin exception is exact, including method and path. Adjacent and
	// normalized Browser POST paths are still rejected before session lookup.
	for _, path := range []string{"/api/v1/connector-enrollments/", "/api/v1/connector-enrollments/../auth/logout", "/api/v1/connectors/enrollment-tokens", "/api/v1/auth/login"} {
		r := httptest.NewRequest("POST", trustedOrigin+path, strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("Origin exception extended to %s", path)
		}
	}
}
