//go:build integration

package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestApplicationMetricsExpositionOverProductStack(t *testing.T) {
	reg, hm, rm := applicationMetrics([]string{"api", "realtime"})
	e := newTerminalEnv(t, func(o *stackOptions) { o.HTTPMetrics, o.RealtimeMetrics = hm, rm })
	ready := &atomic.Bool{}
	ready.Store(true)
	admin := adminHandler(ready, promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	scrape := func() string {
		t.Helper()
		rec := httptest.NewRecorder()
		admin.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain; version=0.0.4") {
			t.Fatalf("metrics response = %d, %q", rec.Code, rec.Header().Get("Content-Type"))
		}
		return rec.Body.String()
	}
	before := scrape()
	if strings.Contains(before, "labbit_http_requests_total") || !strings.Contains(before, `labbit_realtime_connections{channel="browser_terminal"} 0`) {
		t.Fatal("unexpected metrics before product traffic")
	}
	for _, cookie := range []string{e.ownerCookie, ""} {
		want := http.StatusOK
		if cookie == "" {
			want = http.StatusUnauthorized
		}
		if resp := e.request(http.MethodGet, "/api/v1/me", cookie, ""); resp.Status != want {
			t.Fatalf("me status = %d", resp.Status)
		}
	}
	s := e.createSession(e.ownerCookie, e.fixture.LabInstanceID)
	b, _ := e.attach(s)
	eventually(t, "bound connections", 5*time.Second, func() bool {
		return testutil.ToFloat64(rm.BrowserConnections) == 1 && testutil.ToFloat64(rm.ConnectorConnections) == 1
	})
	b.close()
	eventually(t, "Browser gauge released", 5*time.Second, func() bool { return testutil.ToFloat64(rm.BrowserConnections) == 0 })
	b, _ = e.attach(s)
	e.connector.DropData(s.ID)
	eventually(t, "Data gauge released", 5*time.Second, func() bool { return testutil.ToFloat64(rm.ConnectorConnections) == 0 })
	if !e.connector.ReattachData(s.ID) {
		t.Fatal("Data reconnect failed")
	}
	eventually(t, "resume counters", 5*time.Second, func() bool {
		return testutil.ToFloat64(rm.BrowserReconnects) == 1 && testutil.ToFloat64(rm.ConnectorReconnects) == 1
	})
	if _, resp, err := e.dialBrowser("", terminalTrustedOrigin); err == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatal("unauthenticated Upgrade was not rejected")
	}
	active := scrape()
	for name, kind := range map[string]string{
		"labbit_http_requests_total": "counter", "labbit_http_request_duration_seconds": "histogram", "labbit_http_inflight_requests": "gauge",
		"labbit_realtime_connections": "gauge", "labbit_realtime_reconnects_total": "counter", "labbit_realtime_rejected_connections_total": "counter", "labbit_realtime_draining": "gauge",
	} {
		if !strings.Contains(active, "# HELP "+name+" ") || !strings.Contains(active, "# TYPE "+name+" "+kind+"\n") {
			t.Errorf("missing HELP/TYPE for %s", name)
		}
	}
	for _, series := range []string{
		`labbit_http_requests_total{method="GET",route="/api/v1/me",status_class="2xx"} 1`,
		`labbit_http_requests_total{method="GET",route="/api/v1/me",status_class="4xx"} 1`,
		`labbit_http_requests_total{method="POST",route="/api/v1/lab-instances/{labInstanceId}/terminal-sessions",status_class="2xx"} 1`,
		`labbit_http_request_duration_seconds_count{method="GET",route="/api/v1/me"} 2`,
		`labbit_http_inflight_requests 0`,
		`labbit_realtime_connections{channel="browser_terminal"} 1`,
		`labbit_realtime_connections{channel="connector_terminal"} 1`,
		`labbit_realtime_reconnects_total{channel="browser_terminal"} 1`,
		`labbit_realtime_reconnects_total{channel="connector_terminal"} 1`,
		`labbit_realtime_rejected_connections_total{reason="unauthorized"} 1`,
		`labbit_realtime_draining 0`,
	} {
		if !strings.Contains(active, series+"\n") {
			t.Errorf("missing series %s", series)
		}
		t.Log(series)
	}
	for _, forbidden := range []string{"labbit_worker_", "live_subscriber", s.ID, s.Token, e.ownerCookie,
		e.fixture.LabInstanceID.String(), e.fixture.ConnectorID.String(), "terminal_session_id", "connector_id", "request_id"} {
		if strings.Contains(active, forbidden) {
			t.Fatal("metrics contain a forbidden label/value or excluded product metric")
		}
	}
	// Health and repeated scrapes do not count as product API requests.
	for _, path := range []string{"/metrics", "/livez", "/readyz"} {
		admin.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
	}
	if got := testutil.ToFloat64(hm.Requests.WithLabelValues("GET", "/api/v1/me", "2xx")); got != 1 {
		t.Fatal("admin requests changed product request counter")
	}
	if resp := e.request(http.MethodDelete, "/api/v1/terminal-sessions/"+s.ID, e.ownerCookie, ""); resp.Status != http.StatusNoContent {
		t.Fatalf("close status = %d", resp.Status)
	}
	b.close()
	eventually(t, "both connection gauges released", 5*time.Second, func() bool {
		return testutil.ToFloat64(rm.BrowserConnections) == 0 && testutil.ToFloat64(rm.ConnectorConnections) == 0
	})
	e.stack.close()
	if body := scrape(); !strings.Contains(body, "labbit_realtime_draining 1\n") {
		t.Fatal("draining not exposed")
	}
	t.Log("closed: browser_terminal=0 connector_terminal=0 draining=1; Worker and Live metrics absent")
}
