package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	migrationfiles "github.com/ktcloud4-SL/labbit-app/db/migrations"
	"github.com/ktcloud4-SL/labbit-app/internal/observability"
	"github.com/ktcloud4-SL/labbit-app/internal/observability/tracing"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connectorwss"
	"github.com/ktcloud4-SL/labbit-app/internal/server/filetransport"
	"github.com/ktcloud4-SL/labbit-app/internal/server/httpapi"
	"github.com/ktcloud4-SL/labbit-app/internal/server/preview"
	"github.com/ktcloud4-SL/labbit-app/internal/server/previewsession"
	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
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
	// PublicOrigin은 api role의 Browser unsafe-method Origin 검증과 realtime role의 Browser WSS Upgrade Origin 검증에 쓰는
	// trusted origin이다. LABBIT_PUBLIC_ORIGIN을 httpapi.ParseOrigin으로 정규화한 값이며 request Host에서 만들지 않는다.
	PublicOrigin string
	// 아래는 preview role이 enabled된 process의 Preview 설정이다. role이 없으면 zero value다.
	//
	// PreviewAllowedPorts는 Backend의 명시적 허용 port 목록(LABBIT_PREVIEW_ALLOWED_PORTS)이다. 숫자 범위로 자동 승인하지 않고 기본 port가 없다.
	PreviewAllowedPorts previewsession.Policy
	// PreviewSessionTTL은 PreviewSession의 절대 TTL(LABBIT_PREVIEW_SESSION_TTL)이다. 기본값이 없다.
	PreviewSessionTTL time.Duration
	// PreviewOrigin은 사용자 코드 Preview Origin template(LABBIT_PREVIEW_ORIGIN_TEMPLATE)이다. SaaS 본 서비스 Origin과 다르다.
	PreviewOrigin preview.OriginTemplate

	// Tracing은 OTEL_* Trace 설정이다. 업무 필수 설정이 아니므로 오류가 있어도 LoadConfig는 실패하지 않고, Run이 안전한 진단을 남기고
	// export 없이 계속한다. 값의 zero value는 export 없는 none이다.
	Tracing tracing.Config
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

	// Browser HTTP/Auth 경계(api)와 Browser Terminal/Live WSS 경계(realtime) 모두 strict Origin 검증이 필요하다.
	// realtime 때문에 필요해지는 것이며 DB DSN 요구는 추가하지 않는다.
	var publicOrigin string
	if slices.Contains(roles, "api") || slices.Contains(roles, "realtime") {
		raw := strings.TrimSpace(os.Getenv("LABBIT_PUBLIC_ORIGIN"))
		if raw == "" {
			if slices.Contains(roles, "api") {
				return Config{}, errors.New("api role에는 LABBIT_PUBLIC_ORIGIN이 필요합니다")
			}
			return Config{}, errors.New("realtime role에는 LABBIT_PUBLIC_ORIGIN이 필요합니다")
		}
		publicOrigin, err = httpapi.ParseOrigin(raw)
		if err != nil {
			return Config{}, fmt.Errorf("LABBIT_PUBLIC_ORIGIN 형식 오류: %w", err)
		}
	}

	cfg := Config{
		Environment:   environment,
		Roles:         roles,
		HTTPAddr:      envOrDefault("LABBIT_HTTP_ADDR", defaultHTTPAddr),
		AdminAddr:     envOrDefault("LABBIT_ADMIN_ADDR", defaultAdminAddr),
		LogLevel:      envOrDefault("LABBIT_LOG_LEVEL", "info"),
		ShutdownGrace: grace,
		DatabaseDSN:   databaseDSN,
		PublicOrigin:  publicOrigin,
		Tracing:       tracing.ConfigFromEnv(os.Getenv),
	}

	// preview role이 enabled되면 허용 port, TTL, Origin template이 모두 명시되어야 한다. 하나라도 없거나 올바르지 않으면 startup에 실패한다.
	// 일부만 설정된 채 동작하는 Preview를 만들지 않는다(fail closed). PostgreSQL DSN을 요구하지는 않는다.
	if slices.Contains(roles, "preview") {
		if err := loadPreviewConfig(&cfg); err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
}

// loadPreviewConfig는 preview role의 Runtime Contract 항목(LABBIT_PREVIEW_*)을 읽는다. 오류 문구는 값 원문을 되풀이하지 않는다.
func loadPreviewConfig(cfg *Config) error {
	policy, err := previewsession.ParseAllowedPorts(os.Getenv("LABBIT_PREVIEW_ALLOWED_PORTS"))
	switch {
	case errors.Is(err, previewsession.ErrNoAllowedPorts):
		return errors.New("preview role에는 LABBIT_PREVIEW_ALLOWED_PORTS(허용 port 목록)가 필요합니다")
	case err != nil:
		return fmt.Errorf("LABBIT_PREVIEW_ALLOWED_PORTS 형식 오류: %w", err)
	}

	ttl, err := parsePreviewTTL(os.Getenv("LABBIT_PREVIEW_SESSION_TTL"))
	if err != nil {
		return err
	}

	rawTemplate := strings.TrimSpace(os.Getenv("LABBIT_PREVIEW_ORIGIN_TEMPLATE"))
	if rawTemplate == "" {
		return errors.New("preview role에는 LABBIT_PREVIEW_ORIGIN_TEMPLATE이 필요합니다")
	}
	origin, err := preview.ParseOriginTemplate(rawTemplate, cfg.Environment == "production")
	if err != nil {
		return fmt.Errorf("LABBIT_PREVIEW_ORIGIN_TEMPLATE 형식 오류: %w", err)
	}
	// v0.1 iframe 인증 전제: Preview Origin은 SaaS 본 서비스 Origin(LABBIT_PUBLIC_ORIGIN)과 separate-origin이면서 same-site여야 한다 (Blocker B).
	if cfg.PublicOrigin != "" {
		if err := origin.ValidateSameSite(cfg.PublicOrigin); err != nil {
			return fmt.Errorf("LABBIT_PREVIEW_ORIGIN_TEMPLATE same-site 검증 실패: %w", err)
		}
	}

	cfg.PreviewAllowedPorts, cfg.PreviewSessionTTL, cfg.PreviewOrigin = policy, ttl, origin
	return nil
}

// parsePreviewTTL은 LABBIT_PREVIEW_SESSION_TTL을 읽는다. 기본값이 없으며 0, 음수, 해석할 수 없는 값은 오류다.
func parsePreviewTTL(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, errors.New("preview role에는 LABBIT_PREVIEW_SESSION_TTL이 필요합니다")
	}
	ttl, err := time.ParseDuration(raw)
	if err != nil {
		return 0, errors.New("LABBIT_PREVIEW_SESSION_TTL 형식 오류: Go duration이어야 합니다")
	}
	if ttl <= 0 {
		return 0, errors.New("LABBIT_PREVIEW_SESSION_TTL은 0보다 커야 합니다")
	}
	return ttl, nil
}

func Run(ctx context.Context, cfg Config) error {
	logger := observability.NewJSONLogger("labbit-server", "bootstrap", cfg.Environment, cfg.LogLevel)
	ready := &atomic.Bool{}
	registry, httpMetrics, realtimeMetrics := applicationMetrics(cfg.Roles)

	// Trace는 업무 성공 조건이 아니다. Start는 실패하지 않으며 설정 오류와 exporter 초기화 실패는 안전한 진단 뒤 export만 끈다.
	// exporter가 없어도 Span과 W3C Context 전파, log의 trace_id correlation은 그대로다.
	traceRuntime := tracing.Start(ctx, cfg.Tracing, tracing.Options{
		Environment: cfg.Environment,
		Component:   strings.Join(cfg.Roles, ","),
		Logger:      logger,
	})
	// 종료 flush는 한 번만 한다. 정상 종료는 아래에서 남은 shutdown budget으로 하고, 그 전에 반환하는 시작 실패 경로는 이 defer가 한다.
	var flushTraces sync.Once
	flush := func(flushCtx context.Context) { flushTraces.Do(func() { _ = traceRuntime.Shutdown(flushCtx) }) }
	defer func() {
		earlyCtx, cancelEarly := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
		defer cancelEarly()
		flush(earlyCtx)
	}()

	var (
		checks []func(context.Context) error
		stack  *controlStack
	)
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
		checks = append(checks, (&databaseReadiness{
			querier:    pool,
			migrations: migrations,
			logger:     logger,
		}).check)

		if slices.Contains(cfg.Roles, "api") {
			// api role이 Auth/Class HTTP와 Connector Control WSS를 소유한다. realtime role이 같은 process에서 함께 enabled되면
			// 같은 Connector Registry/Router 위에 Terminal Relay와 TerminalSession 생성/종료를 조립한다.
			// 아직 Operation 결과를 받는 durable Worker(LBT-18)가 없으므로 Operation Sink는 두지 않는다.
			stack, err = newControlStack(postgres.NewStore(pool), stackOptions{
				Logger:          logger,
				PublicOrigin:    cfg.PublicOrigin,
				Realtime:        slices.Contains(cfg.Roles, "realtime"),
				HTTPMetrics:     httpMetrics,
				RealtimeMetrics: realtimeMetrics,
				// preview role이 같은 process에 있으면 Preview Gateway와 PreviewSession use case를 함께 조립한다.
				Preview:       slices.Contains(cfg.Roles, "preview"),
				PreviewPolicy: cfg.PreviewAllowedPorts,
				PreviewTTL:    cfg.PreviewSessionTTL,
				PreviewOrigin: cfg.PreviewOrigin,
				Tracer:        traceRuntime.Tracer(),
			})
			if err != nil {
				return err
			}
		}
	}
	if slices.Contains(cfg.Roles, "realtime") && stack == nil {
		// v0.1의 realtime role은 DB-backed authority(Browser 인증, TerminalSession 권한, Connector Control)를 같은 process의
		// api role에서 받는다. 없으면 인증 없는 Terminal route를 열지 않고 not-ready로 둔다. DB DSN을 새로 요구하지도 않는다.
		logger.Warn("realtime role은 같은 process의 api role 없이는 Terminal을 제공하지 않습니다",
			"roles", strings.Join(cfg.Roles, ","))
		checks = append(checks, func(context.Context) error { return errRealtimeRequiresAPI })
	}

	if slices.Contains(cfg.Roles, "preview") && stack == nil {
		// v0.1의 preview role은 DB-backed authority(PreviewSession 권한, Connector Control)를 같은 process의 api role에서 받는다.
		// 없으면 인증 없는 Preview route를 열지 않고 not-ready로 둔다. DB DSN을 새로 요구하지도 않는다.
		logger.Warn("preview role은 같은 process의 api role 없이는 Preview를 제공하지 않습니다",
			"roles", strings.Join(cfg.Roles, ","))
		checks = append(checks, func(context.Context) error { return errPreviewRequiresAPI })
	}

	var appRoutes routes
	if stack != nil {
		appRoutes = stack.routes()
	}
	applicationServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           applicationHandler(appRoutes),
		ReadHeaderTimeout: 5 * time.Second,
	}
	if stack != nil {
		// Shutdown은 hijack된 WebSocket connection을 닫지 않으므로 handler가 새 Upgrade 거절과 기존 connection drain을 맡는다.
		applicationServer.RegisterOnShutdown(stack.close)
	}
	adminServer := &http.Server{
		Addr:              cfg.AdminAddr,
		Handler:           adminHandler(ready, promhttp.HandlerFor(registry, promhttp.HandlerOpts{}), checks...),
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
	if realtimeMetrics != nil {
		realtimeMetrics.Draining.Set(1)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
	defer cancel()

	applicationErr := applicationServer.Shutdown(shutdownCtx)
	if stack != nil {
		// http.Server.Shutdown은 hijack된 WebSocket을 기다리지 않는다. active TerminalSession을 종료하고 열린 Control/Terminal
		// connection이 정리될 때까지 기다린다.
		applicationErr = errors.Join(applicationErr, stack.shutdown(shutdownCtx))
	}
	adminErr := adminServer.Shutdown(shutdownCtx)

	// 관측 flush는 업무 HTTP/WSS drain과 정리가 끝난 뒤 남은 shutdown budget 안에서만 한다. 별도의 context를 만들지 않으므로
	// Collector가 응답하지 않아도 LABBIT_SHUTDOWN_GRACE를 넘겨 종료를 지연시키지 않는다. flush 실패는 이미 끝난 업무 정리의 결과를
	// 바꾸지 않으므로 반환 오류에 합치지 않는다(Runtime이 error_code만 기록한다).
	flush(shutdownCtx)
	return errors.Join(applicationErr, adminErr)
}

// errRealtimeRequiresAPI는 realtime role만으로는 Terminal을 제공할 수 없음을 readiness에 알리는 사유다. 응답 본문에는 싣지 않는다.
var errRealtimeRequiresAPI = errors.New("realtime role은 같은 process의 api role이 필요합니다")

// errPreviewRequiresAPI는 preview role만으로는 Preview를 제공할 수 없음을 readiness에 알리는 사유다. 응답 본문에는 싣지 않는다.
var errPreviewRequiresAPI = errors.New("preview role은 같은 process의 api role이 필요합니다")

// routes는 application listener에 mount할 handler들이다. 제공하지 않는 endpoint는 nil이며 mount하지 않는다.
type routes struct {
	// API는 /api/v1 Auth, Class, TerminalSession HTTP다(api role).
	API http.Handler
	// ConnectorControl은 Connector Control WSS다(api role이 소유).
	ConnectorControl http.Handler
	// ConnectorFileData는 Workspace File 요청별 File Data WSS다(api role이 소유, Control과 같은 process).
	ConnectorFileData http.Handler
	// BrowserTerminal과 ConnectorTerminalData는 Terminal Relay의 WSS다(realtime role, 같은 process의 api role이 authority를 제공할 때만).
	BrowserTerminal       http.Handler
	BrowserLive           http.Handler
	ConnectorTerminalData http.Handler
	// ConnectorPreviewData는 PreviewSession별 Preview Data WSS다(preview role, 같은 process의 api role이 authority를 제공할 때만).
	ConnectorPreviewData http.Handler
	// PreviewGateway는 Preview Origin의 HTTP handler다. PreviewMatchesHost가 request Host를 Preview Origin template과 대조해 true인 요청만
	// 이 handler로 보낸다. SaaS 본 서비스 Origin의 요청과 섞이지 않는다.
	PreviewGateway     http.Handler
	PreviewMatchesHost func(host string) bool
}

func applicationHandler(rt routes) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// OpenAPI 기능 구현 전에는 존재하지 않는 endpoint를 임의로 흉내 내지 않는다.
		http.NotFound(w, r)
	})
	if rt.API != nil {
		mux.Handle("/api/v1/", rt.API)
	}
	if rt.ConnectorControl != nil {
		mux.Handle("GET "+connectorwss.Path, rt.ConnectorControl)
	}
	if rt.ConnectorFileData != nil {
		mux.Handle("GET "+filetransport.DataPath, rt.ConnectorFileData)
	}
	if rt.BrowserTerminal != nil {
		mux.Handle("GET "+realtime.BrowserPath, rt.BrowserTerminal)
	}
	if rt.BrowserLive != nil {
		mux.Handle("GET "+realtime.LivePath, rt.BrowserLive)
	}
	if rt.ConnectorTerminalData != nil {
		mux.Handle("GET "+realtime.DataPath, rt.ConnectorTerminalData)
	}
	if rt.ConnectorPreviewData != nil {
		mux.Handle("GET "+preview.DataPath, rt.ConnectorPreviewData)
	}
	if rt.PreviewGateway == nil || rt.PreviewMatchesHost == nil {
		return mux
	}
	// Preview Origin은 사용자 코드가 실행되는 별도 Origin이다. request Host가 template에 일치하는 요청은 어떤 경로든 Preview Gateway가 처리하며
	// SaaS 본 서비스 route(/api/v1 등)에 도달하지 못한다. 반대로 본 서비스 host의 요청은 Gateway에 도달하지 않는다.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rt.PreviewMatchesHost(r.Host) {
			rt.PreviewGateway.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// adminHandler의 checks는 role이 새 작업을 안전하게 받을 수 있는지 확인하는 함수들이다(예: DB와 schema 호환성).
// nil은 건너뛴다. 하나라도 실패하면 /readyz가 실패한다.
func adminHandler(ready *atomic.Bool, metrics http.Handler, checks ...func(context.Context) error) http.Handler {
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
		for _, check := range checks {
			if check == nil {
				continue
			}
			checkCtx, cancel := context.WithTimeout(r.Context(), readinessCheckTimeout)
			err := check(checkCtx)
			cancel()
			if err != nil {
				http.Error(w, "not ready", http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	mux.Handle("GET /metrics", metrics)

	return mux
}

func applicationMetrics(roles []string) (*prometheus.Registry, *observability.HTTPMetrics, *observability.RealtimeMetrics) {
	registry := prometheus.NewRegistry()
	var httpMetrics *observability.HTTPMetrics
	var realtimeMetrics *observability.RealtimeMetrics
	if slices.Contains(roles, "api") {
		httpMetrics = observability.NewHTTPMetrics(registry)
	}
	if slices.Contains(roles, "realtime") {
		realtimeMetrics = observability.NewRealtimeMetrics(registry)
	}
	return registry, httpMetrics, realtimeMetrics
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
