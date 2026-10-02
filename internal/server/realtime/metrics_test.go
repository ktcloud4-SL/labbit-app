package realtime_test

import (
	"context"
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
	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
)

func TestTerminalMetricsConnectionLifecycle(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := observability.NewRealtimeMetrics(reg)
	e := newEnv(t, func(o *realtime.Options) { o.Metrics = m })
	s, d := e.liveSession()
	b := e.connectBrowser(s)
	d.readJSON() // initial resize
	waitMetric(t, m.BrowserConnections, 1)
	waitMetric(t, m.ConnectorConnections, 1)
	waitMetric(t, m.BrowserReconnects, 0)
	waitMetric(t, m.ConnectorReconnects, 0)

	b.close()
	waitMetric(t, m.BrowserConnections, 0)
	b = e.connectBrowser(s)
	d.readJSON()
	waitMetric(t, m.BrowserReconnects, 1)

	d.close()
	waitMetric(t, m.ConnectorConnections, 0)
	d = e.connectData(s)
	waitMetric(t, m.ConnectorReconnects, 1)
	waitMetric(t, m.ConnectorConnections, 1)

	// Replacing a live connection must retire only the old gauge contribution.
	b2 := e.connectBrowser(s)
	d.readJSON()
	if got := b.expectClose(); got != 4004 {
		t.Fatalf("replaced Browser close = %d", got)
	}
	waitMetric(t, m.BrowserConnections, 1)
	waitMetric(t, m.BrowserReconnects, 2)
	d2 := e.connectData(s)
	if got := d.expectClose(); got != 1000 {
		t.Fatalf("replaced Data close = %d", got)
	}
	waitMetric(t, m.ConnectorConnections, 1)
	waitMetric(t, m.ConnectorReconnects, 2)

	e.relay.Close()
	e.relay.Close()
	waitMetric(t, m.Draining, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Start peer readers so normal shutdown can complete its close handshakes.
	b2.start.Do(func() { go b2.readLoop() })
	d2.start.Do(func() { go d2.readLoop() })
	if err := e.relay.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	waitMetric(t, m.BrowserConnections, 0)
	waitMetric(t, m.ConnectorConnections, 0)

	rec := httptest.NewRecorder()
	promhttp.HandlerFor(reg, promhttp.HandlerOpts{}).ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	for _, forbidden := range []string{s.ID, s.LabID, s.Token, ownerCookie, connectorCred, connectorID1,
		"terminal_session_id", "connector_id", "live_subscriber", "labbit_worker_", "labbit_http_"} {
		if strings.Contains(rec.Body.String(), forbidden) {
			t.Errorf("metrics contain forbidden value/label %q", forbidden)
		}
	}
}

func TestTerminalMetricsRejectedConnectionsAreNotActive(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := observability.NewRealtimeMetrics(reg)
	e := newEnv(t, func(o *realtime.Options) { o.Metrics = m })
	if _, resp, err := e.dialBrowser("", trustedOrigin); err == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatal("Browser without Cookie was not rejected")
	}
	if _, resp, err := e.dialData(""); err == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatal("Data without credential was not rejected")
	}
	waitMetric(t, m.Unauthorized, 2)
	if _, resp, err := e.dialBrowser(ownerCookie, "https://untrusted.test"); err == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatal("untrusted Origin was not rejected")
	}
	waitMetric(t, m.Forbidden, 1)

	// Valid Upgrade is not a bound connection, and invalid attach remains a rejection.
	p, _, err := e.dialData(connectorCred)
	if err != nil {
		t.Fatal(err)
	}
	waitMetric(t, m.ConnectorConnections, 0)
	p.writeBinary([]byte("invalid-first-frame"))
	if got := p.expectClose(); got != 1008 {
		t.Fatalf("invalid attach close = %d", got)
	}
	waitMetric(t, m.InvalidRequest, 1)

	// Gorilla's own handshake rejection must preserve its response and count once.
	req := httptest.NewRequest("GET", realtime.DataPath, nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Authorization", "Bearer "+connectorCred)
	req.Header.Set("Sec-WebSocket-Protocol", realtime.DataSubprotocol)
	req.Header.Set("Sec-WebSocket-Version", "12")
	rec := httptest.NewRecorder()
	e.relay.DataHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || rec.Header().Get("Sec-WebSocket-Version") != "13" {
		t.Fatal("handshake rejection response changed")
	}
	waitMetric(t, m.InvalidRequest, 2)

	s, _ := e.liveSession()
	b, _, err := e.dialBrowser(ownerCookie, trustedOrigin)
	if err != nil {
		t.Fatal(err)
	}
	b.writeText(browserAttachMessage(t, s, nil, map[string]any{"sessionToken": "invalid-token-marker"}))
	if got := b.expectClose(); got != 4001 {
		t.Fatalf("invalid token close = %d", got)
	}
	waitMetric(t, m.Unauthorized, 3)
	waitMetric(t, m.BrowserConnections, 0)
	waitMetric(t, m.BrowserReconnects, 0)
	e.control.mu.Lock()
	e.control.authorizeErr = errors.New("raw-dependency-error-marker")
	e.control.mu.Unlock()
	b, _, err = e.dialBrowser(ownerCookie, trustedOrigin)
	if err != nil {
		t.Fatal(err)
	}
	b.writeText(browserAttachMessage(t, s, nil, nil))
	if got := b.expectClose(); got != 1011 {
		t.Fatalf("unavailable attach close = %d", got)
	}
	waitMetric(t, m.Unavailable, 1)
	waitMetric(t, m.BrowserConnections, 0)

	e.relay.Close()
	if _, resp, err := e.dialBrowser(ownerCookie, trustedOrigin); err == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatal("draining Browser was not rejected")
	}
	if _, resp, err := e.dialData(connectorCred); err == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatal("draining Data was not rejected")
	}
	waitMetric(t, m.DrainingRejected, 2)
}

func waitMetric(t *testing.T, metric prometheus.Collector, want float64) {
	t.Helper()
	eventually(t, "metric lifecycle", func() bool { return testutil.ToFloat64(metric) == want })
}
