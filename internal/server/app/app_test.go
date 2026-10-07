package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestAdminHandlerReadiness(t *testing.T) {
	errDatabase := errors.New("database unavailable")
	tests := []struct {
		name          string
		startupDone   bool
		checkDatabase func(context.Context) error
		wantReady     int
	}{
		{
			name:        "startup not complete",
			startupDone: false,
			checkDatabase: func(context.Context) error {
				return nil
			},
			wantReady: http.StatusServiceUnavailable,
		},
		{
			name:        "role without database dependency",
			startupDone: true,
			wantReady:   http.StatusOK,
		},
		{
			name:        "database and schema usable",
			startupDone: true,
			checkDatabase: func(context.Context) error {
				return nil
			},
			wantReady: http.StatusOK,
		},
		{
			name:        "database or schema unusable",
			startupDone: true,
			checkDatabase: func(context.Context) error {
				return errDatabase
			},
			wantReady: http.StatusServiceUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ready := &atomic.Bool{}
			ready.Store(tt.startupDone)
			handler := adminHandler(ready, emptyMetricsHandler(), tt.checkDatabase)

			readyz := httptest.NewRecorder()
			handler.ServeHTTP(readyz, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if readyz.Code != tt.wantReady {
				t.Fatalf("/readyz status = %d, want %d", readyz.Code, tt.wantReady)
			}
			if strings.Contains(readyz.Body.String(), errDatabase.Error()) {
				t.Fatalf("/readyz body exposes dependency error: %q", readyz.Body.String())
			}

			// liveness는 PostgreSQL 장애와 무관하게 process 자체 상태만 나타낸다.
			livez := httptest.NewRecorder()
			handler.ServeHTTP(livez, httptest.NewRequest(http.MethodGet, "/livez", nil))
			if livez.Code != http.StatusOK {
				t.Fatalf("/livez status = %d, want %d", livez.Code, http.StatusOK)
			}
		})
	}
}

func TestAdminHandlerReadinessCheckHasDeadline(t *testing.T) {
	ready := &atomic.Bool{}
	ready.Store(true)
	var hasDeadline bool
	handler := adminHandler(ready, emptyMetricsHandler(), func(ctx context.Context) error {
		_, hasDeadline = ctx.Deadline()
		return nil
	})

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if !hasDeadline {
		t.Fatal("readiness database check must be bounded by a deadline")
	}
}

func TestLoadConfigDatabaseRequirementByRole(t *testing.T) {
	tests := []struct {
		name        string
		environment string
		roles       string
		dsnEnv      string
		wantDSN     string
		wantErr     string
	}{
		{
			name:        "api requires database",
			environment: "development",
			roles:       "api",
			wantErr:     "api/worker role에는 PostgreSQL이 필요합니다",
		},
		{
			name:        "worker requires database",
			environment: "development",
			roles:       "worker",
			wantErr:     "api/worker role에는 PostgreSQL이 필요합니다",
		},
		{
			name:        "realtime and preview do not require database",
			environment: "development",
			roles:       "realtime,preview",
		},
		{
			name:        "api uses development env DSN",
			environment: "development",
			roles:       "api,realtime",
			dsnEnv:      "postgres://labbit:dummy@localhost:5432/labbit",
			wantDSN:     "postgres://labbit:dummy@localhost:5432/labbit",
		},
		{
			name:        "production api requires file DSN",
			environment: "production",
			roles:       "api",
			dsnEnv:      "postgres://labbit:dummy@localhost:5432/labbit",
			wantErr:     "production에서는 LABBIT_DATABASE_DSN_FILE이 필요합니다",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LABBIT_ENVIRONMENT", tt.environment)
			t.Setenv("LABBIT_RUNTIME_ROLES", tt.roles)
			t.Setenv("LABBIT_SHUTDOWN_GRACE", "5s")
			t.Setenv("LABBIT_DATABASE_DSN_FILE", "")
			t.Setenv("LABBIT_DATABASE_DSN", tt.dsnEnv)
			t.Setenv("LABBIT_PUBLIC_ORIGIN", "http://localhost:5173")
			// preview role은 PostgreSQL DSN을 요구하지 않지만 허용 port, TTL, Origin template은 명시해야 한다(fail closed).
			t.Setenv("LABBIT_PREVIEW_ALLOWED_PORTS", "3000")
			t.Setenv("LABBIT_PREVIEW_SESSION_TTL", "1h")
			t.Setenv("LABBIT_PREVIEW_ORIGIN_TEMPLATE", "http://{sessionId}.localhost:5174")

			cfg, err := LoadConfig()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("LoadConfig() error = %v, want containing %q", err, tt.wantErr)
				}
				if tt.dsnEnv != "" && strings.Contains(err.Error(), tt.dsnEnv) {
					t.Fatalf("LoadConfig() error exposes DSN: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			if cfg.DatabaseDSN != tt.wantDSN {
				t.Fatalf("DatabaseDSN = %q, want %q", cfg.DatabaseDSN, tt.wantDSN)
			}
		})
	}
}

func TestLoadConfigPublicOrigin(t *testing.T) {
	tests := []struct {
		name    string
		roles   string
		origin  string
		want    string
		wantErr string
	}{
		{name: "api requires origin", roles: "api", wantErr: "api role에는 LABBIT_PUBLIC_ORIGIN이 필요합니다"},
		{name: "blank origin is missing", roles: "api", origin: "  ", wantErr: "api role에는 LABBIT_PUBLIC_ORIGIN이 필요합니다"},
		{name: "http origin with port", roles: "api", origin: "http://localhost:5173", want: "http://localhost:5173"},
		{name: "origin is normalized", roles: "api", origin: "HTTPS://Labbit.Example.com:443", want: "https://labbit.example.com"},
		{name: "path is rejected", roles: "api", origin: "https://labbit.example.com/app", wantErr: "LABBIT_PUBLIC_ORIGIN 형식 오류"},
		{name: "trailing slash is rejected", roles: "api", origin: "https://labbit.example.com/", wantErr: "LABBIT_PUBLIC_ORIGIN 형식 오류"},
		{name: "query is rejected", roles: "api", origin: "https://labbit.example.com?x=1", wantErr: "LABBIT_PUBLIC_ORIGIN 형식 오류"},
		{name: "userinfo is rejected", roles: "api", origin: "https://user@labbit.example.com", wantErr: "LABBIT_PUBLIC_ORIGIN 형식 오류"},
		{name: "non-http scheme is rejected", roles: "api", origin: "ftp://labbit.example.com", wantErr: "LABBIT_PUBLIC_ORIGIN 형식 오류"},
		{name: "relative value is rejected", roles: "api", origin: "labbit.example.com", wantErr: "LABBIT_PUBLIC_ORIGIN 형식 오류"},
		{name: "worker alone does not need origin", roles: "worker", want: ""},
		// Browser Terminal/Live WSS Upgrade도 strict Origin 검증이 필요하다. realtime 때문에 필요한 것이며 DB DSN 요구와 무관하다.
		{name: "realtime requires origin", roles: "realtime", wantErr: "realtime role에는 LABBIT_PUBLIC_ORIGIN이 필요합니다"},
		{name: "worker and realtime requires origin", roles: "worker,realtime", wantErr: "realtime role에는 LABBIT_PUBLIC_ORIGIN이 필요합니다"},
		{name: "realtime accepts normalized origin", roles: "realtime", origin: "HTTPS://Labbit.Example.com:443", want: "https://labbit.example.com"},
		{name: "realtime with api reports api requirement first", roles: "api,realtime", wantErr: "api role에는 LABBIT_PUBLIC_ORIGIN이 필요합니다"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LABBIT_ENVIRONMENT", "development")
			t.Setenv("LABBIT_RUNTIME_ROLES", tt.roles)
			t.Setenv("LABBIT_SHUTDOWN_GRACE", "5s")
			t.Setenv("LABBIT_DATABASE_DSN_FILE", "")
			t.Setenv("LABBIT_DATABASE_DSN", "postgres://labbit:dummy@localhost:5432/labbit")
			t.Setenv("LABBIT_PUBLIC_ORIGIN", tt.origin)

			cfg, err := LoadConfig()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("LoadConfig() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			if cfg.PublicOrigin != tt.want {
				t.Fatalf("PublicOrigin = %q, want %q", cfg.PublicOrigin, tt.want)
			}
		})
	}
}

func TestApplicationHandlerMountsAPIOnlyWhenProvided(t *testing.T) {
	api := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	withAPI := applicationHandler(routes{API: api})
	rec := httptest.NewRecorder()
	withAPI.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("api role /api/v1/me status = %d, want %d", rec.Code, http.StatusNoContent)
	}

	withoutAPI := applicationHandler(routes{})
	rec = httptest.NewRecorder()
	withoutAPI.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("non-api role /api/v1/me status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestApplicationHandlerMountsConnectorControlOnlyWhenProvided(t *testing.T) {
	control := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	serve := func(h http.Handler, method string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, "/connector/v1/control", nil))
		return rec.Code
	}

	mounted := applicationHandler(routes{ConnectorControl: control})
	if got := serve(mounted, http.MethodGet); got != http.StatusNoContent {
		t.Fatalf("GET /connector/v1/control status = %d, want %d", got, http.StatusNoContent)
	}
	// WebSocket Upgrade는 GET이다. 다른 method는 method 무관 catch-all("/")로 가므로 handler에 도달하지 않는다.
	if got := serve(mounted, http.MethodPost); got != http.StatusNotFound {
		t.Fatalf("POST /connector/v1/control status = %d, want %d", got, http.StatusNotFound)
	}

	if got := serve(applicationHandler(routes{}), http.MethodGet); got != http.StatusNotFound {
		t.Fatalf("Connector Control 미제공 status = %d, want %d", got, http.StatusNotFound)
	}
}

func TestApplicationHandlerMountsTerminalWSSOnlyWhenProvided(t *testing.T) {
	browser := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })
	data := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNonAuthoritativeInfo) })

	serve := func(h http.Handler, method, path string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		return rec.Code
	}

	mounted := applicationHandler(routes{BrowserTerminal: browser, ConnectorTerminalData: data})
	if got := serve(mounted, http.MethodGet, "/realtime/v1/terminal"); got != http.StatusAccepted {
		t.Fatalf("GET /realtime/v1/terminal status = %d, want %d", got, http.StatusAccepted)
	}
	if got := serve(mounted, http.MethodGet, "/connector/v1/terminal-data"); got != http.StatusNonAuthoritativeInfo {
		t.Fatalf("GET /connector/v1/terminal-data status = %d, want %d", got, http.StatusNonAuthoritativeInfo)
	}
	// WebSocket Upgrade는 GET이다.
	if got := serve(mounted, http.MethodPost, "/realtime/v1/terminal"); got != http.StatusNotFound {
		t.Fatalf("POST /realtime/v1/terminal status = %d, want %d", got, http.StatusNotFound)
	}

	// authority(api role)가 없는 process는 인증 없는 Terminal route를 열지 않는다.
	unmounted := applicationHandler(routes{})
	for _, path := range []string{"/realtime/v1/terminal", "/connector/v1/terminal-data"} {
		if got := serve(unmounted, http.MethodGet, path); got != http.StatusNotFound {
			t.Fatalf("Terminal 미제공 GET %s status = %d, want %d", path, got, http.StatusNotFound)
		}
	}
}

func TestAdminHandlerReadinessRequiresEveryCheck(t *testing.T) {
	ready := &atomic.Bool{}
	ready.Store(true)

	ok := func(context.Context) error { return nil }
	fail := func(context.Context) error { return errors.New("not usable") }

	tests := []struct {
		name   string
		checks []func(context.Context) error
		want   int
	}{
		{name: "no checks", want: http.StatusOK},
		{name: "nil check is skipped", checks: []func(context.Context) error{nil}, want: http.StatusOK},
		{name: "all pass", checks: []func(context.Context) error{ok, ok}, want: http.StatusOK},
		// realtime role만 enabled된 process는 authority가 없어 not-ready다.
		{name: "one failing check", checks: []func(context.Context) error{ok, fail}, want: http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			adminHandler(ready, emptyMetricsHandler(), tt.checks...).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if rec.Code != tt.want {
				t.Fatalf("/readyz status = %d, want %d", rec.Code, tt.want)
			}
			if strings.Contains(rec.Body.String(), "not usable") {
				t.Fatalf("/readyz body exposes the check error: %q", rec.Body.String())
			}
		})
	}
}

func emptyMetricsHandler() http.Handler {
	return promhttp.HandlerFor(prometheus.NewRegistry(), promhttp.HandlerOpts{})
}
