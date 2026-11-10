package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ktcloud4-SL/labbit-app/internal/server/connectorwss"
	"github.com/ktcloud4-SL/labbit-app/internal/server/maintenance"
	"github.com/ktcloud4-SL/labbit-app/internal/server/preview"
	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
)

func TestMaintenanceRouteCoverage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mode")
	if err := os.WriteFile(path, []byte("closed"), 0600); err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	rt := routes{API: next, ConnectorControl: next, BrowserTerminal: next, BrowserLive: next, ConnectorTerminalData: next, ConnectorPreviewData: next, PreviewGateway: next, PreviewMatchesHost: func(host string) bool { return host == "preview.test" }}
	for _, tc := range []struct {
		method, path, host string
		want               int
	}{
		{"POST", "/api/v1/classes/x/lab-executions", "public.test", 503},
		{"POST", "/api/v1/lab-executions/x/cleanup", "service.cluster", 503},
		{"POST", "/api/v1/lab-instances/x/reset", "10.0.0.1", 503},
		{"POST", "/api/v1/lab-instances/x/terminal-sessions", "public.test", 503},
		{"POST", "/api/v1/lab-instances/x/preview-sessions", "public.test", 503},
		{"POST", "/api/v1/terminal-sessions/x/live-sessions", "public.test", 503},
		{"GET", realtime.BrowserPath, "10.0.0.1", 503},
		{"GET", realtime.LivePath, "service.cluster", 503},
		{"GET", "/any/path", "preview.test", 503},
		{"GET", connectorwss.Path, "public.test", 204},
		{"GET", realtime.DataPath, "public.test", 204},
		{"GET", preview.DataPath, "public.test", 204},
		{"GET", "/api/v1/lab-executions/x", "public.test", 204},
		{"DELETE", "/api/v1/terminal-sessions/x", "public.test", 204},
		{"DELETE", "/api/v1/preview-sessions/x", "public.test", 204},
		{"DELETE", "/api/v1/live-sessions/x", "public.test", 204},
	} {
		t.Run(tc.method+tc.path+tc.host, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "http://"+tc.host+tc.path, nil)
			req.Header.Set("Upgrade", "websocket")
			req.Header.Set("Connection", "Upgrade")
			w := httptest.NewRecorder()
			applicationHandler(maintenanceRoutes(rt, maintenance.New(path))).ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("got %d want %d", w.Code, tc.want)
			}
		})
	}
}

func TestMaintenanceRoleCombinations(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	for _, tc := range []struct {
		rt           routes
		method, path string
		want         int
	}{
		{routes{}, "POST", "/api/v1/lab-instances/x/reset", 404},
		{routes{API: next}, "POST", "/api/v1/lab-instances/x/reset", 503},
		{routes{BrowserTerminal: next}, "GET", realtime.BrowserPath, 503},
		{routes{BrowserLive: next}, "GET", realtime.LivePath, 503},
		{routes{PreviewGateway: next, PreviewMatchesHost: func(string) bool { return true }}, "GET", "/", 503},
		{routes{ConnectorControl: next}, "GET", connectorwss.Path, 204},
	} {
		for _, enabled := range []bool{false, true} {
			gate := maintenance.New("")
			if enabled {
				gate = maintenance.New(filepath.Join(t.TempDir(), "missing"))
			}
			w := httptest.NewRecorder()
			applicationHandler(maintenanceRoutes(tc.rt, gate)).ServeHTTP(w, httptest.NewRequest(tc.method, "http://test"+tc.path, nil))
			want := tc.want
			if !enabled && want == 503 {
				want = 204
			}
			if w.Code != want {
				t.Fatalf("enabled=%v %s: got %d want %d", enabled, tc.path, w.Code, want)
			}
		}
	}
}
