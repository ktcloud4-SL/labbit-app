package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
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
			handler := adminHandler(ready, tt.checkDatabase)

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
	handler := adminHandler(ready, func(ctx context.Context) error {
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
