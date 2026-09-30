//go:build integration

package app

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	migrationfiles "github.com/ktcloud4-SL/labbit-app/db/migrations"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
)

// unusedDSN은 연결하면 실패하는 주소다. file DSN 우선순위와 DB 장애 readiness 확인에 사용한다.
const unusedDSN = "postgres://labbit:unused-dummy@127.0.0.1:1/unused?sslmode=disable&connect_timeout=1"

func TestServerReadinessUsesProductionFileDSNAndCompatibleSchema(t *testing.T) {
	all := loadEmbeddedMigrations(t)
	dsn := postgrestest.NewDatabase(t)
	postgrestest.Migrate(t, dsn, all)

	// production 경계: file DSN만 사용하며 함께 설정된 env DSN(연결 불가 주소)은 무시한다.
	admin := startServer(t, "production", "api", dsn, unusedDSN)

	waitForStatus(t, admin+"/readyz", http.StatusOK)
	assertStatus(t, admin+"/livez", http.StatusOK)
}

func TestServerReadinessFailsUntilRequiredMigrationIsApplied(t *testing.T) {
	all := loadEmbeddedMigrations(t)
	var upTo5 []postgres.Migration
	for _, migration := range all {
		if migration.Version <= 5 {
			upTo5 = append(upTo5, migration)
		}
	}
	dsn := postgrestest.NewDatabase(t)
	postgrestest.Migrate(t, dsn, upTo5)

	admin := startServer(t, "development", "api,worker", dsn, "")

	// 000006을 모르는 schema에서는 새 작업을 받지 않지만 process liveness는 유지한다.
	waitForStatus(t, admin+"/readyz", http.StatusServiceUnavailable)
	assertStatus(t, admin+"/livez", http.StatusOK)

	// application을 재시작하지 않아도 별도 migration 단계가 끝나면 ready로 전환된다.
	postgrestest.Migrate(t, dsn, all)
	waitForStatus(t, admin+"/readyz", http.StatusOK)
}

func TestServerReadinessFailsOnUninitializedSchema(t *testing.T) {
	dsn := postgrestest.NewDatabase(t)

	admin := startServer(t, "development", "api", dsn, "")

	waitForStatus(t, admin+"/readyz", http.StatusServiceUnavailable)
	assertStatus(t, admin+"/livez", http.StatusOK)
}

func TestServerReadinessFailsWhenDatabaseIsUnavailable(t *testing.T) {
	admin := startServer(t, "development", "api", unusedDSN, "")

	waitForStatus(t, admin+"/readyz", http.StatusServiceUnavailable)
	assertStatus(t, admin+"/livez", http.StatusOK)
}

// startServer는 환경변수로 설정을 읽어 labbit-server를 실행하고 admin base URL을 반환한다.
// fileDSN은 platform Secret mount처럼 LABBIT_DATABASE_DSN_FILE이 가리키는 파일로 주입한다.
func startServer(t *testing.T, environment, roles, fileDSN, envDSN string) string {
	t.Helper()
	admin, _ := startServerWithApplication(t, environment, roles, fileDSN, envDSN)
	return admin
}

// startServerWithApplication은 startServer와 같지만 application listener의 base URL도 반환한다.
func startServerWithApplication(t *testing.T, environment, roles, fileDSN, envDSN string) (admin, application string) {
	t.Helper()
	dsnFile := filepath.Join(t.TempDir(), "database-dsn")
	if err := os.WriteFile(dsnFile, []byte(fileDSN+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	adminAddr := freeAddr(t)
	applicationAddr := freeAddr(t)
	t.Setenv("LABBIT_ENVIRONMENT", environment)
	t.Setenv("LABBIT_RUNTIME_ROLES", roles)
	t.Setenv("LABBIT_SHUTDOWN_GRACE", "5s")
	t.Setenv("LABBIT_HTTP_ADDR", applicationAddr)
	t.Setenv("LABBIT_ADMIN_ADDR", adminAddr)
	t.Setenv("LABBIT_LOG_LEVEL", "error")
	t.Setenv("LABBIT_DATABASE_DSN_FILE", dsnFile)
	t.Setenv("LABBIT_DATABASE_DSN", envDSN)
	t.Setenv("LABBIT_PUBLIC_ORIGIN", "https://labbit.test")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, cfg)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run() error = %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("server did not shut down")
		}
	})
	return "http://" + adminAddr, "http://" + applicationAddr
}

func loadEmbeddedMigrations(t *testing.T) []postgres.Migration {
	t.Helper()
	migrations, err := postgres.LoadMigrations(migrationfiles.Files)
	if err != nil {
		t.Fatalf("LoadMigrations() error = %v", err)
	}
	return migrations
}

func freeAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func waitForStatus(t *testing.T, url string, want int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last int
	for time.Now().Before(deadline) {
		if status, err := getStatus(url); err == nil {
			last = status
			if status == want {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("GET %s status = %d, want %d", url, last, want)
}

func assertStatus(t *testing.T, url string, want int) {
	t.Helper()
	status, err := getStatus(url)
	if err != nil {
		t.Fatalf("GET %s error = %v", url, err)
	}
	if status != want {
		t.Fatalf("GET %s status = %d, want %d", url, status, want)
	}
}

// probeClient는 요청마다 연결을 닫는다. keep-alive 재사용 경합으로 요청 없는 연결이 남으면
// http.Server.Shutdown이 그 연결을 기다리느라 test의 shutdown grace를 넘길 수 있다.
var probeClient = &http.Client{
	Timeout:   5 * time.Second,
	Transport: &http.Transport{DisableKeepAlives: true},
}

func getStatus(url string) (int, error) {
	resp, err := probeClient.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}
