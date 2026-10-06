package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// setPreviewEnv는 preview role이 enabled된 process의 필수 설정을 유효한 값으로 채운다. 각 test가 한 항목씩 바꾼다.
func setPreviewEnv(t *testing.T, environment, roles string) {
	t.Helper()
	t.Setenv("LABBIT_ENVIRONMENT", environment)
	t.Setenv("LABBIT_RUNTIME_ROLES", roles)
	t.Setenv("LABBIT_SHUTDOWN_GRACE", "5s")
	t.Setenv("LABBIT_DATABASE_DSN_FILE", "")
	t.Setenv("LABBIT_DATABASE_DSN", "postgres://labbit:dummy@localhost:5432/labbit")
	t.Setenv("LABBIT_PUBLIC_ORIGIN", "https://labbit.example.com")
	t.Setenv("LABBIT_PREVIEW_ALLOWED_PORTS", "3000,5173")
	t.Setenv("LABBIT_PREVIEW_SESSION_TTL", "45m")
	t.Setenv("LABBIT_PREVIEW_ORIGIN_TEMPLATE", "http://{sessionId}.preview.localhost:8080")
}

func TestLoadConfigReadsThePreviewSettings(t *testing.T) {
	setPreviewEnv(t, "development", "api,preview")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.PreviewSessionTTL != 45*time.Minute {
		t.Errorf("PreviewSessionTTL = %v", cfg.PreviewSessionTTL)
	}
	if got := cfg.PreviewOrigin.String(); got != "http://{sessionId}.preview.localhost:8080" {
		t.Errorf("PreviewOrigin = %q", got)
	}
	for _, port := range []int{3000, 5173} {
		if err := cfg.PreviewAllowedPorts.Approve(port); err != nil {
			t.Errorf("Approve(%d) error = %v", port, err)
		}
	}
	// 목록에 없는 port와 SSH 관리 port는 승인하지 않는다.
	for _, port := range []int{22, 80, 5000, 8080} {
		if err := cfg.PreviewAllowedPorts.Approve(port); err == nil {
			t.Errorf("목록에 없는 %d를 승인함", port)
		}
	}
}

// preview role이 enabled되면 세 설정이 모두 명시되어야 한다. 하나라도 없으면 startup에 실패한다(fail closed). 기본값이 없다.
func TestLoadConfigRequiresEveryPreviewSettingWhenThePreviewRoleIsEnabled(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		value   string
		wantErr string
	}{
		{"허용 port 목록 없음", "LABBIT_PREVIEW_ALLOWED_PORTS", "", "LABBIT_PREVIEW_ALLOWED_PORTS(허용 port 목록)가 필요합니다"},
		{"허용 port 목록이 공백뿐", "LABBIT_PREVIEW_ALLOWED_PORTS", "  ", "LABBIT_PREVIEW_ALLOWED_PORTS(허용 port 목록)가 필요합니다"},
		{"허용 port 범위 표기", "LABBIT_PREVIEW_ALLOWED_PORTS", "3000-3010", "LABBIT_PREVIEW_ALLOWED_PORTS 형식 오류"},
		{"허용 port 빈 항목", "LABBIT_PREVIEW_ALLOWED_PORTS", "3000,,80", "LABBIT_PREVIEW_ALLOWED_PORTS 형식 오류"},
		{"허용 port 범위 초과", "LABBIT_PREVIEW_ALLOWED_PORTS", "70000", "LABBIT_PREVIEW_ALLOWED_PORTS 형식 오류"},
		{"허용 port가 숫자가 아님", "LABBIT_PREVIEW_ALLOWED_PORTS", "http", "LABBIT_PREVIEW_ALLOWED_PORTS 형식 오류"},
		{"TTL 없음", "LABBIT_PREVIEW_SESSION_TTL", "", "LABBIT_PREVIEW_SESSION_TTL이 필요합니다"},
		{"TTL 0", "LABBIT_PREVIEW_SESSION_TTL", "0s", "LABBIT_PREVIEW_SESSION_TTL은 0보다 커야 합니다"},
		{"TTL 음수", "LABBIT_PREVIEW_SESSION_TTL", "-5m", "LABBIT_PREVIEW_SESSION_TTL은 0보다 커야 합니다"},
		{"TTL이 duration이 아님", "LABBIT_PREVIEW_SESSION_TTL", "forever", "LABBIT_PREVIEW_SESSION_TTL 형식 오류"},
		{"TTL 숫자만", "LABBIT_PREVIEW_SESSION_TTL", "3600", "LABBIT_PREVIEW_SESSION_TTL 형식 오류"},
		{"Origin template 없음", "LABBIT_PREVIEW_ORIGIN_TEMPLATE", "", "LABBIT_PREVIEW_ORIGIN_TEMPLATE이 필요합니다"},
		{"Origin template에 placeholder 없음", "LABBIT_PREVIEW_ORIGIN_TEMPLATE", "https://preview.example.com", "LABBIT_PREVIEW_ORIGIN_TEMPLATE 형식 오류"},
		{"Origin template path", "LABBIT_PREVIEW_ORIGIN_TEMPLATE", "https://{sessionId}.preview.example.com/app", "LABBIT_PREVIEW_ORIGIN_TEMPLATE 형식 오류"},
		{"Origin template이 SaaS 본 서비스 Origin과 겹침", "LABBIT_PREVIEW_ORIGIN_TEMPLATE", "https://{sessionId}.example.com", "LABBIT_PREVIEW_ORIGIN_TEMPLATE은 LABBIT_PUBLIC_ORIGIN과 다른 Origin이어야 합니다"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setPreviewEnv(t, "development", "api,preview")
			t.Setenv(tc.key, tc.value)
			_, err := LoadConfig()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("LoadConfig() error = %v, want containing %q", err, tc.wantErr)
			}
			// 오류 문구는 설정 값 원문을 되풀이하지 않는다.
			if tc.value != "" && strings.TrimSpace(tc.value) != "" && strings.Contains(err.Error(), tc.value) {
				t.Fatalf("오류가 설정 값 %q를 되풀이함: %v", tc.value, err)
			}
		})
	}
}

// production에서는 Preview Origin이 https여야 한다. 사용자 코드가 평문 http Origin에서 실행되지 않는다.
func TestLoadConfigRequiresAnHTTPSPreviewOriginInProduction(t *testing.T) {
	// preview role만 enabled해 PostgreSQL DSN 없이 Origin 검증만 확인한다.
	setPreviewEnv(t, "production", "preview")
	t.Setenv("LABBIT_PUBLIC_ORIGIN", "")
	t.Setenv("LABBIT_DATABASE_DSN", "")
	t.Setenv("LABBIT_PREVIEW_ORIGIN_TEMPLATE", "http://{sessionId}.preview.example.com")
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "LABBIT_PREVIEW_ORIGIN_TEMPLATE 형식 오류") {
		t.Fatalf("production http template: LoadConfig() error = %v", err)
	}
}

// preview role이 없으면 Preview 설정을 읽지도 검증하지도 않는다(api만 enabled된 process는 Preview를 제공하지 않을 뿐 시작에 실패하지 않는다).
func TestLoadConfigIgnoresPreviewSettingsWithoutThePreviewRole(t *testing.T) {
	setPreviewEnv(t, "development", "api")
	t.Setenv("LABBIT_PREVIEW_ALLOWED_PORTS", "not-a-port-list")
	t.Setenv("LABBIT_PREVIEW_SESSION_TTL", "forever")
	t.Setenv("LABBIT_PREVIEW_ORIGIN_TEMPLATE", "not a template")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !cfg.PreviewOrigin.IsZero() || cfg.PreviewSessionTTL != 0 || len(cfg.PreviewAllowedPorts.Ports()) != 0 {
		t.Fatalf("preview role이 없는데 Preview 설정을 읽음: %+v", cfg)
	}
}

// preview role만 enabled된 process는 PostgreSQL DSN 없이 시작한다(Runtime Contract: preview role은 DB를 요구하지 않는다).
func TestLoadConfigPreviewRoleAloneDoesNotRequireADatabase(t *testing.T) {
	setPreviewEnv(t, "production", "preview")
	t.Setenv("LABBIT_PUBLIC_ORIGIN", "")
	t.Setenv("LABBIT_DATABASE_DSN", "")
	t.Setenv("LABBIT_DATABASE_DSN_FILE", "")
	t.Setenv("LABBIT_PREVIEW_ORIGIN_TEMPLATE", "https://{sessionId}.preview.example.com")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.DatabaseDSN != "" || cfg.PreviewSessionTTL != 45*time.Minute {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestZeroPreviewPolicyApprovesNothing(t *testing.T) {
	var cfg Config
	for _, port := range []int{80, 3000, 5000, 5173, 8080} {
		if err := cfg.PreviewAllowedPorts.Approve(port); err == nil {
			t.Errorf("설정 없는 Config가 %d를 승인함", port)
		}
	}
}

// ---- host 기반 routing ----

func TestApplicationHandlerSeparatesThePreviewOriginFromTheMainService(t *testing.T) {
	mark := func(code int) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) })
	}
	rt := routes{
		API:                  mark(http.StatusNoContent),
		ConnectorPreviewData: mark(http.StatusNonAuthoritativeInfo),
		PreviewGateway:       mark(http.StatusTeapot),
		PreviewMatchesHost:   func(host string) bool { return strings.HasSuffix(host, ".preview.test") },
	}
	h := applicationHandler(rt)
	serve := func(host, method, path string) int {
		req := httptest.NewRequest(method, "http://"+host+path, nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	// Preview Origin의 요청은 경로와 무관하게 Gateway가 처리한다. SaaS 본 서비스 route(/api/v1, Connector WSS)에 도달하지 못한다.
	for _, path := range []string{"/", "/api/v1/me", "/api/v1/auth/login", "/connector/v1/preview-data", "/connector/v1/control", "/realtime/v1/terminal"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			if got := serve("abc.preview.test", method, path); got != http.StatusTeapot {
				t.Errorf("Preview Origin %s %s status = %d, want Gateway(418)", method, path, got)
			}
		}
	}
	// 본 서비스 host의 요청은 Gateway에 도달하지 않는다.
	if got := serve("labbit.example.com", http.MethodGet, "/api/v1/me"); got != http.StatusNoContent {
		t.Errorf("본 서비스 /api/v1/me status = %d, want API(204)", got)
	}
	if got := serve("labbit.example.com", http.MethodGet, "/connector/v1/preview-data"); got != http.StatusNonAuthoritativeInfo {
		t.Errorf("본 서비스 /connector/v1/preview-data status = %d, want Preview Data(203)", got)
	}
	if got := serve("labbit.example.com", http.MethodGet, "/"); got != http.StatusNotFound {
		t.Errorf("본 서비스 / status = %d, want 404", got)
	}
	// Connector Preview Data는 GET(WebSocket Upgrade)만이다.
	if got := serve("labbit.example.com", http.MethodPost, "/connector/v1/preview-data"); got != http.StatusNotFound {
		t.Errorf("POST /connector/v1/preview-data status = %d, want 404", got)
	}
}

// Preview를 제공하지 않는 구성(preview role 없음 또는 authority 없음)은 Preview route를 열지 않는다.
func TestApplicationHandlerWithoutPreviewDoesNotMountPreviewRoutes(t *testing.T) {
	h := applicationHandler(routes{API: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })})
	for _, host := range []string{"abc.preview.test", "labbit.example.com"} {
		req := httptest.NewRequest(http.MethodGet, "http://"+host+"/connector/v1/preview-data", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("host %q Preview route status = %d, want 404", host, rec.Code)
		}
	}
	// 부분 구성(handler만 있고 host 판별이 없음)은 모든 요청을 Gateway로 보내지 않는다.
	partial := applicationHandler(routes{PreviewGateway: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })})
	rec := httptest.NewRecorder()
	partial.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://labbit.example.com/", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("host 판별 없는 부분 구성 status = %d, want 404", rec.Code)
	}
}
