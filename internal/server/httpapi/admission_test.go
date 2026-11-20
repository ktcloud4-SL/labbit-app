package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/ktcloud4-SL/labbit-app/internal/server/labexecution"
)

func TestMutationAdmissionBlockedHTTPContract(t *testing.T) {
	for _, tt := range []struct{ name, path, body string }{
		{"provision", classPath, validProvisionBody},
		{"reset", "/api/v1/lab-instances/" + acceptedInstanceID + "/reset", ""},
		{"cleanup", "/api/v1/lab-executions/" + acceptedExecutionID + "/cleanup", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeLabExecutions{err: fmt.Errorf("private-dsn-marker: %w", labexecution.ErrMaintenance)}
			h := newLabExecutionHarness(t, fake)
			rec := h.send(http.MethodPost, tt.path, tt.body, unsafeJSON(h.signIn(), withHeader("Idempotency-Key", "key"), withHeader("X-Mutation-Admission-Mode", "OPEN"))...)
			requireProblem(t, rec, http.StatusServiceUnavailable, "maintenance")
			if fake.calls != 1 || rec.Header().Get("Location") != "" || rec.Header().Get("Retry-After") != "" {
				t.Fatalf("calls=%d headers=%v", fake.calls, rec.Header())
			}
			if strings.Contains(rec.Body.String(), acceptedOperationID) || strings.Contains(rec.Body.String(), "private-dsn-marker") || strings.Contains(h.logs.String(), "private-dsn-marker") {
				t.Fatal("blocked response exposed accepted operation or internal cause")
			}
			before := fake.calls
			requireProblem(t, h.send(http.MethodPost, tt.path, tt.body, unsafeJSON(withHeader("Idempotency-Key", "key"))...), http.StatusUnauthorized, codeUnauthenticated)
			if fake.calls != before {
				t.Fatal("unauthenticated request reached application")
			}
		})
	}
}
