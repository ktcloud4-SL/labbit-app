package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestApplicationMetricsRegistrationByRole(t *testing.T) {
	for _, roles := range [][]string{{"api"}, {"realtime"}, {"worker"}, {"preview"}, {"api", "realtime", "worker"}} {
		t.Run(strings.Join(roles, ","), func(t *testing.T) {
			reg, hm, rm := applicationMetrics(roles)
			hasAPI, hasRealtime := false, false
			for _, role := range roles {
				hasAPI = hasAPI || role == "api"
				hasRealtime = hasRealtime || role == "realtime"
			}
			if (hm != nil) != hasAPI || (rm != nil) != hasRealtime {
				t.Fatal("role metric injection mismatch")
			}
			ready := &atomic.Bool{}
			ready.Store(true)
			admin := adminHandler(ready, promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
			for _, path := range []string{"/metrics", "/livez", "/readyz", "/metrics"} {
				rec := httptest.NewRecorder()
				admin.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
				if rec.Code != http.StatusOK {
					t.Fatalf("%s status = %d", path, rec.Code)
				}
				if path != "/metrics" {
					continue
				}
				body := rec.Body.String()
				if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain; version=0.0.4") {
					t.Fatal("invalid metrics content type")
				}
				if strings.Contains(body, "labbit_http_") != hasAPI || strings.Contains(body, "labbit_realtime_") != hasRealtime {
					t.Fatal("unrelated role metrics exposed")
				}
				for _, forbidden := range []string{"labbit_worker_", "live_subscriber", "go_", "process_", "labbit_http_requests_total"} {
					if strings.Contains(body, forbidden) {
						t.Errorf("unexpected metric/series %q", forbidden)
					}
				}
			}
		})
	}
}

func TestMetricsFailureDoesNotAffectHealth(t *testing.T) {
	ready := &atomic.Bool{}
	ready.Store(true)
	admin := adminHandler(ready, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	for path, want := range map[string]int{"/metrics": 503, "/livez": 200, "/readyz": 200} {
		rec := httptest.NewRecorder()
		admin.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != want {
			t.Errorf("%s = %d, want %d", path, rec.Code, want)
		}
	}
}
