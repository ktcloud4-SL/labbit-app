package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/ktcloud4-SL/labbit-app/internal/observability"
	"github.com/ktcloud4-SL/labbit-app/internal/server/class"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

func TestHTTPMetricsAtHandlerBoundary(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := observability.NewHTTPMetrics(reg)
	h := newHarness(t, func(o *Options) { o.Metrics = m })
	signedIn := h.signIn()

	if got := h.send(http.MethodGet, "/api/v1/me", "", signedIn).Code; got != http.StatusOK {
		t.Fatalf("me status = %d", got)
	}
	for _, id := range []string{"class-id-one", "class-id-two"} {
		if got := h.send(http.MethodGet, "/api/v1/classes/"+id+"?request=raw-query", "", signedIn,
			func(r *http.Request) { r.Header.Set("X-Request-ID", "raw-request-marker") }).Code; got != http.StatusNotFound {
			t.Fatalf("class status = %d", got)
		}
	}
	h.classes.listErr = errors.New("raw-error-marker")
	if got := h.send(http.MethodGet, "/api/v1/classes", "", signedIn).Code; got != http.StatusInternalServerError {
		t.Fatalf("classes status = %d", got)
	}
	// Origin is rejected before mux dispatch, but the registered route template remains known.
	if got := h.send(http.MethodPost, "/api/v1/lab-instances/raw-lab-id/terminal-sessions", "").Code; got != http.StatusForbidden {
		t.Fatalf("Origin rejection status = %d", got)
	}
	// Unknown paths, nonstandard methods and canonical redirects must not introduce raw labels.
	h.send("RAW-METHOD-MARKER", "/api/v1/raw-path-marker", "")
	h.send(http.MethodGet, "/api/v1/classes/../raw-path-marker", "")
	h.send(http.MethodHead, "/api/v1/me", "", signedIn)
	for _, want := range []struct {
		method, route, status string
		count                 float64
	}{
		{"GET", "/api/v1/me", "2xx", 1},
		{"GET", "/api/v1/classes/{classId}", "4xx", 2},
		{"GET", "/api/v1/classes", "5xx", 1},
		{"POST", "/api/v1/lab-instances/{labInstanceId}/terminal-sessions", "4xx", 1},
		{"unknown", "unmatched", "4xx", 1},
		{"GET", "unmatched", "3xx", 1},
		{"HEAD", "/api/v1/me", "2xx", 1},
	} {
		if got := testutil.ToFloat64(m.Requests.WithLabelValues(want.method, want.route, want.status)); got != want.count {
			t.Errorf("requests{%s,%s,%s} = %g, want %g", want.method, want.route, want.status, got, want.count)
		}
	}
	if got := testutil.ToFloat64(m.Inflight); got != 0 {
		t.Fatalf("inflight = %g", got)
	}
	rec := httptest.NewRecorder()
	promhttp.HandlerFor(reg, promhttp.HandlerOpts{}).ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `labbit_http_request_duration_seconds_count{method="GET",route="/api/v1/classes/{classId}"} 2`) {
		t.Fatal("missing histogram observations for route template")
	}
	for _, forbidden := range []string{"class-id-one", "class-id-two", "raw-", "RAW-METHOD-MARKER", "live-session",
		h.auth.principal.User.ID.String(), h.auth.principal.SessionID.String(), h.auth.principal.User.Username,
		"user_id", "request_id", "session_id", "token", "labbit_worker_", "labbit_realtime_"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("metrics contain forbidden value/label %q", forbidden)
		}
	}
}

func TestHTTPInflightDuringRequestAndAfterCompletion(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := observability.NewHTTPMetrics(reg)
	h := newHarness(t, func(o *Options) { o.Metrics = m })
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	h.classes.detail = func(repository.User, string) (class.View, error) {
		close(entered)
		<-release
		return class.View{}, class.ErrNotFound
	}
	signedIn := h.signIn()
	go func() {
		defer close(done)
		h.send(http.MethodGet, "/api/v1/classes/inflight-id", "", signedIn)
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("handler did not start")
	}
	got := testutil.ToFloat64(m.Inflight)
	close(release)
	<-done
	if got != 1 {
		t.Errorf("inflight during request = %g, want 1", got)
	}
	if got := testutil.ToFloat64(m.Inflight); got != 0 {
		t.Errorf("inflight after request = %g, want 0", got)
	}
}
