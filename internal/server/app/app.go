package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	migrationfiles "github.com/ktcloud4-SL/labbit-app/db/migrations"
	"github.com/ktcloud4-SL/labbit-app/internal/observability"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
)

const (
	defaultHTTPAddr  = ":8080"
	defaultAdminAddr = ":9090"

	readinessCheckTimeout = 2 * time.Second
)

type Config struct {
	Environment   string
	Roles         []string
	HTTPAddr      string
	AdminAddr     string
	LogLevel      string
	ShutdownGrace time.Duration
	// DatabaseDSN은 Secret이다. 로그·오류 메시지에 기록하지 않는다.
	DatabaseDSN string
}

// LoadConfig는 현재 구현된 role에 필요한 Runtime Contract 항목만 읽는다.
// Secret, role별 세부 설정은 해당 기능을 구현할 때 이 경계에 추가한다.
func LoadConfig() (Config, error) {
	environment := strings.TrimSpace(os.Getenv("LABBIT_ENVIRONMENT"))
	if environment == "" {
		return Config{}, errors.New("LABBIT_ENVIRONMENT가 필요합니다")
	}

	roles, err := parseRoles(os.Getenv("LABBIT_RUNTIME_ROLES"))
	if err != nil {
		return Config{}, err
	}

	grace, err := parseShutdownGrace(environment, os.Getenv("LABBIT_SHUTDOWN_GRACE"))
	if err != nil {
		return Config{}, err
	}

	var databaseDSN string
	if requiresDatabase(roles) {
		databaseDSN, err = postgres.LoadDSN(environment)
		if errors.Is(err, postgres.ErrDSNNotConfigured) {
			return Config{}, fmt.Errorf("api/worker role에는 PostgreSQL이 필요합니다: %w", err)
		}
		if err != nil {
			return Config{}, err
		}
	}

	return Config{
		Environment:   environment,
		Roles:         roles,
		HTTPAddr:      envOrDefault("LABBIT_HTTP_ADDR", defaultHTTPAddr),
		AdminAddr:     envOrDefault("LABBIT_ADMIN_ADDR", defaultAdminAddr),
		LogLevel:      envOrDefault("LABBIT_LOG_LEVEL", "info"),
		ShutdownGrace: grace,
		DatabaseDSN:   databaseDSN,
	}, nil
}

func Run(ctx context.Context, cfg Config) error {
	logger := observability.NewJSONLogger("labbit-server", "bootstrap", cfg.Environment, cfg.LogLevel)
	ready := &atomic.Bool{}

	var checkDatabase func(context.Context) error
	if cfg.DatabaseDSN != "" {
		pool, err := postgres.OpenPool(ctx, cfg.DatabaseDSN)
		if err != nil {
			return err
		}
		defer pool.Close()

		// 같은 build에 포함된 migration을 호환 schema의 기준으로 사용한다. Migration 적용은 하지 않는다.
		migrations, err := postgres.LoadMigrations(migrationfiles.Files)
		if err != nil {
			return err
		}
		checkDatabase = (&databaseReadiness{
			querier:    pool,
			migrations: migrations,
			logger:     logger,
		}).check
	}

	applicationServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           applicationHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	adminServer := &http.Server{
		Addr:              cfg.AdminAddr,
		Handler:           adminHandler(ready, checkDatabase),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 2)
	go serve(logger, "application", applicationServer, errCh)
	go serve(logger, "admin", adminServer, errCh)

	// 두 listener의 Serve goroutine을 시작한 뒤 startup 완료로 전환한다.
	// DB가 필요한 role의 PostgreSQL/schema 조건은 /readyz 요청마다 별도로 확인한다.
	ready.Store(true)
	logger.Info("Labbit 서버 스켈레톤 시작",
		"roles", strings.Join(cfg.Roles, ","),
		"http_addr", cfg.HTTPAddr,
		"admin_addr", cfg.AdminAddr,
	)

	select {
	case <-ctx.Done():
		logger.Info("종료 신호 수신")
	case err := <-errCh:
		if err != nil {
			return err
		}
	}

	// D-22/Runtime Contract에 따라 먼저 readiness를 내리고 신규 트래픽을 받지 않도록 한다.
	ready.Store(false)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
	defer cancel()

	applicationErr := applicationServer.Shutdown(shutdownCtx)
	adminErr := adminServer.Shutdown(shutdownCtx)
	return errors.Join(applicationErr, adminErr)
}

func applicationHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// OpenAPI 기능 구현 전에는 존재하지 않는 endpoint를 임의로 흉내 내지 않는다.
		http.NotFound(w, r)
	})
	return mux
}

// adminHandler의 checkDatabase는 DB가 필요한 role이 없으면 nil이다.
func adminHandler(ready *atomic.Bool, checkDatabase func(context.Context) error) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		// API/worker role은 사용 가능한 PostgreSQL과 호환 schema가 있어야 새 작업을 받을 수 있다.
		// 고객 Connector/OpenStack 상태는 SaaS process readiness 조건에 포함하지 않는다.
		if checkDatabase != nil {
			checkCtx, cancel := context.WithTimeout(r.Context(), readinessCheckTimeout)
			defer cancel()
			if err := checkDatabase(checkCtx); err != nil {
				http.Error(w, "not ready", http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		// 지표 이름과 label은 실제 구현에서 D-23/D-25 기준으로 추가한다.
		// 스켈레톤 단계에서는 endpoint 존재만 보장하고 가짜 제품 지표는 만들지 않는다.
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.WriteHeader(http.StatusOK)
	})

	return mux
}

func serve(logger *slog.Logger, name string, server *http.Server, errCh chan<- error) {
	logger.Info("HTTP listener 시작", "listener", name, "addr", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		errCh <- fmt.Errorf("%s listener: %w", name, err)
	}
}

func parseRoles(raw string) ([]string, error) {
	allowed := map[string]bool{
		"api":      true,
		"worker":   true,
		"realtime": true,
		"preview":  true,
	}

	var roles []string
	seen := map[string]bool{}
	for _, item := range strings.Split(raw, ",") {
		role := strings.TrimSpace(item)
		if role == "" {
			continue
		}
		if !allowed[role] {
			return nil, fmt.Errorf("지원하지 않는 LABBIT_RUNTIME_ROLES 값입니다: %s", role)
		}
		if !seen[role] {
			seen[role] = true
			roles = append(roles, role)
		}
	}

	if len(roles) == 0 {
		return nil, errors.New("LABBIT_RUNTIME_ROLES가 필요합니다")
	}
	return roles, nil
}

// requiresDatabase는 Runtime Contract에서 readiness에 PostgreSQL과 호환 schema를 요구하는 role인지 판단한다.
func requiresDatabase(roles []string) bool {
	for _, role := range roles {
		if role == "api" || role == "worker" {
			return true
		}
	}
	return false
}

func parseShutdownGrace(environment, raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if environment == "production" {
			return 0, errors.New("production에서는 LABBIT_SHUTDOWN_GRACE가 필요합니다")
		}
		// 개발 편의를 위한 로컬 기본값이며 production 계약값이 아니다.
		return 10 * time.Second, nil
	}

	grace, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("LABBIT_SHUTDOWN_GRACE 형식 오류: %w", err)
	}
	if grace <= 0 {
		return 0, errors.New("LABBIT_SHUTDOWN_GRACE는 0보다 커야 합니다")
	}
	return grace, nil
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
