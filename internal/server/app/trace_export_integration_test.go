//go:build integration

package app

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/ktcloud4-SL/labbit-app/internal/observability"
	"github.com/ktcloud4-SL/labbit-app/internal/observability/tracing"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
)

// LBT-144: exporter 쪽 Evidence다. 실제 OTLP HTTP/protobuf code path를 network 경계까지 확인하고, Collector 장애와 종료가 업무·Probe로
// 전파되지 않음을 확인한다. receiver는 test double이며 Tempo/Alloy를 대신하지도, 운영 backend 계약을 정의하지도 않는다.
// 이 test들은 Application → 실제 Alloy → Tempo(LBT-105)의 E2E가 아니다.

// isolateOTelEnv는 개발자 PC나 CI의 OTEL_* 설정이 test에 섞이지 않게 한다. SDK는 LookupEnv로 읽으므로 비우지 않고 unset한다.
func isolateOTelEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"OTEL_SERVICE_NAME", "OTEL_TRACES_EXPORTER", "OTEL_TRACES_SAMPLER", "OTEL_TRACES_SAMPLER_ARG", "OTEL_RESOURCE_ATTRIBUTES",
		"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_EXPORTER_OTLP_TIMEOUT",
		"OTEL_EXPORTER_OTLP_CERTIFICATE", "OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE", "OTEL_EXPORTER_OTLP_CLIENT_KEY",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_HEADERS", "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL",
		"OTEL_EXPORTER_OTLP_TRACES_TIMEOUT", "OTEL_EXPORTER_OTLP_TRACES_CERTIFICATE", "OTEL_EXPORTER_OTLP_TRACES_CLIENT_CERTIFICATE",
		"OTEL_EXPORTER_OTLP_TRACES_CLIENT_KEY", "OTEL_BSP_SCHEDULE_DELAY", "OTEL_BSP_EXPORT_TIMEOUT",
	} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
}

// otlpReceiver는 OTLP/HTTP protobuf receiver test double이다.
type otlpReceiver struct {
	srv *httptest.Server

	mu       sync.Mutex
	hits     int
	paths    []string
	headers  []http.Header
	spans    []*tracepb.Span
	resource map[string]string
}

// newOTLPReceiver는 정상 receiver를 만든다. hang이면 요청을 받고 응답하지 않는다.
func newOTLPReceiver(t *testing.T, hang bool) *otlpReceiver {
	t.Helper()
	r := &otlpReceiver{resource: map[string]string{}}
	release := make(chan struct{})
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		raw, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.hits++
		r.paths = append(r.paths, req.URL.Path)
		r.headers = append(r.headers, req.Header.Clone())
		r.mu.Unlock()
		if hang {
			select {
			case <-release:
			case <-req.Context().Done():
			}
			return
		}
		export := &collectortrace.ExportTraceServiceRequest{}
		if err := proto.Unmarshal(raw, export); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		r.mu.Lock()
		for _, rs := range export.ResourceSpans {
			for _, kv := range rs.Resource.GetAttributes() {
				r.resource[kv.Key] = kv.Value.GetStringValue()
			}
			for _, ss := range rs.ScopeSpans {
				r.spans = append(r.spans, ss.Spans...)
			}
		}
		r.mu.Unlock()
		out, _ := proto.Marshal(&collectortrace.ExportTraceServiceResponse{})
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(out)
	}))
	t.Cleanup(func() {
		close(release)
		r.srv.CloseClientConnections()
		r.srv.Close()
	})
	return r
}

func (r *otlpReceiver) endpoint() string { return r.srv.URL + "/v1/traces" }

func (r *otlpReceiver) hitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hits
}

func (r *otlpReceiver) exportedSpans() []*tracepb.Span {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*tracepb.Span(nil), r.spans...)
}

func (r *otlpReceiver) resourceAttr(key string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.resource[key]
}

func (r *otlpReceiver) spanNamed(t *testing.T, name string) *tracepb.Span {
	t.Helper()
	var found []*tracepb.Span
	for _, span := range r.exportedSpans() {
		if span.Name == name {
			found = append(found, span)
		}
	}
	if len(found) != 1 {
		t.Fatalf("export된 Span %q = %d개, want 1", name, len(found))
	}
	return found[0]
}

func closedPortEndpoint(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return "http://" + addr + "/v1/traces"
}

// startTraceRuntime은 환경변수(os.Getenv)에서 Trace 설정을 읽어 Runtime을 시작한다. Run과 같은 경로다.
func startTraceRuntime(t *testing.T, logs io.Writer) *tracing.Runtime {
	t.Helper()
	return tracing.Start(context.Background(), tracing.ConfigFromEnv(os.Getenv), tracing.Options{
		Environment: "development", Component: "api,realtime",
		Logger: observability.NewJSONLoggerTo(logs, "labbit-server", "bootstrap", "development", "debug"),
	})
}

func setTraceEnv(t *testing.T, exporter, protocol, endpoint string) {
	t.Helper()
	t.Setenv("OTEL_TRACES_EXPORTER", exporter)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", protocol)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", endpoint)
}

// none은 export만 끈다. 실제 stack에서 Span, wire Context, log trace_id는 그대로이고 Collector는 호출되지 않는다.
// none != tracing 끄기를 고정하는 regression test다.
func TestNoneModeKeepsTracingContextOnTheRealStackAndNeverCallsACollector(t *testing.T) {
	isolateOTelEnv(t)
	receiver := newOTLPReceiver(t, false)
	// none이어도 exporter가 읽는 표준 환경변수가 Collector를 가리키는 최악의 경우다. 그래도 호출하면 안 된다.
	setTraceEnv(t, "none", "http/protobuf", receiver.endpoint())
	var runtimeLogs lockedBuffer
	rt := startTraceRuntime(t, &runtimeLogs)
	if rt.Exporting() {
		t.Fatal("none인데 exporter가 연결됨")
	}
	e := newTerminalEnv(t, withTracer(rt.Tracer()))

	s := e.createSession(e.ownerCookie, e.fixture.LabInstanceID)
	opens := e.connector.Opens()
	if len(opens) != 1 || opens[0].Traceparent == "" {
		t.Fatalf("none인데 Connector가 Trace Context를 받지 못함: %+v", opens)
	}
	wire := strings.Split(opens[0].Traceparent, "-")
	if len(wire) != 4 || wire[0] != "00" || len(wire[1]) != 32 || len(wire[2]) != 16 || wire[3] != "01" {
		t.Fatalf("wire traceparent = %q", opens[0].Traceparent)
	}
	requireLogFields(t, e.logEvent("TerminalSession 생성", s.ID), map[string]any{"trace_id": wire[1]})
	requireLogFields(t, e.logEvent("Connector TERMINAL_OPEN 전송", s.ID), map[string]any{"trace_id": wire[1]})

	flushCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rt.Shutdown(flushCtx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if hits := receiver.hitCount(); hits != 0 {
		t.Fatalf("none인데 Collector가 %d번 호출됨", hits)
	}
}

// otlp 모드에서 실제 product path(HTTP → TERMINAL_OPEN → RESULT, DELETE → CLOSE)의 Span이 OTLP HTTP/protobuf로 Collector에 도착한다.
// Connector peer가 받은 wire traceparent는 export된 Span의 trace/span ID와 같다(실제 current Span Context에서 나온 값이다).
func TestOTLPHTTPProtobufExportsTheRealProductPath(t *testing.T) {
	isolateOTelEnv(t)
	receiver := newOTLPReceiver(t, false)
	t.Setenv("OTEL_SERVICE_NAME", "labbit-server-integration")
	setTraceEnv(t, "otlp", "http/protobuf", receiver.endpoint())
	var runtimeLogs lockedBuffer
	rt := startTraceRuntime(t, &runtimeLogs)
	if !rt.Exporting() {
		t.Fatalf("otlp인데 exporter가 연결되지 않음:\n%s", runtimeLogs.String())
	}
	e := newTerminalEnv(t, withTracer(rt.Tracer()))

	resp := e.request(http.MethodPost, e.createPath(e.fixture.LabInstanceID), e.ownerCookie, createBody, withIncomingTrace(traceSampled, traceStateOK))
	if resp.Status != http.StatusCreated {
		t.Fatalf("status = %d: %s", resp.Status, resp.Body)
	}
	sessionID := resp.json(t)["id"].(string)
	e.deleteSession(sessionID, withIncomingTrace(traceSampled, traceStateOK))
	eventually(t, "TERMINAL_CLOSE", 5*time.Second, func() bool { return len(e.connector.Closes()) == 1 })

	flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rt.Shutdown(flushCtx); err != nil {
		t.Fatalf("Shutdown() error = %v\n%s", err, runtimeLogs.String())
	}

	receiver.mu.Lock()
	paths := append([]string(nil), receiver.paths...)
	receiver.mu.Unlock()
	if len(paths) == 0 || paths[0] != "/v1/traces" {
		t.Fatalf("Collector 요청 경로 = %v, want /v1/traces", paths)
	}
	wantTrace := mustDecodeHex(t, traceIDSampled)
	for _, name := range []string{serverSpanName, "Connector TERMINAL_OPEN", "Connector TERMINAL_OPEN_RESULT", deleteSpanName, "Connector TERMINAL_CLOSE"} {
		span := receiver.spanNamed(t, name)
		if string(span.TraceId) != string(wantTrace) {
			t.Fatalf("export된 Span %q의 trace ID = %x, want incoming trace", name, span.TraceId)
		}
	}
	open, closeSpan := receiver.spanNamed(t, "Connector TERMINAL_OPEN"), receiver.spanNamed(t, "Connector TERMINAL_CLOSE")
	if want := wireTraceparent(traceIDSampled, hex.EncodeToString(open.SpanId), true); e.connector.Opens()[0].Traceparent != want {
		t.Fatalf("TERMINAL_OPEN wire = %q, want export된 OPEN Span의 Context %q", e.connector.Opens()[0].Traceparent, want)
	}
	if want := wireTraceparent(traceIDSampled, hex.EncodeToString(closeSpan.SpanId), true); e.connector.Closes()[0].Traceparent != want {
		t.Fatalf("TERMINAL_CLOSE wire = %q, want %q", e.connector.Closes()[0].Traceparent, want)
	}
	if string(receiver.spanNamed(t, "Connector TERMINAL_OPEN_RESULT").ParentSpanId) != string(open.SpanId) {
		t.Fatal("export된 결과 Span의 parent가 OPEN Span이 아님")
	}
	for key, want := range map[string]string{
		"service.name": "labbit-server-integration", "deployment.environment.name": "development", "labbit.component": "api,realtime",
	} {
		if got := receiver.resourceAttr(key); got != want {
			t.Fatalf("resource %s = %q, want %q", key, got, want)
		}
	}

	// export된 payload 전체에도 Secret, Cookie, token, Terminal 내용이 없다.
	receiver.mu.Lock()
	var exported strings.Builder
	for _, span := range receiver.spans {
		exported.WriteString(span.String())
	}
	receiver.mu.Unlock()
	requireNoneOf(t, "export된 Span", exported.String(), e.ownerCookie, terminalInputMarker, terminalOutputMarker, e.fixture.Servers["workspace"].ProviderID)
	requireNoneOf(t, "runtime log", runtimeLogs.String(), receiver.srv.URL)
}

func mustDecodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Collector가 내려가 있어도 업무 흐름은 계약대로 동작한다. 요청이 export를 기다리지 않고, Connector command가 재시도되지 않으며,
// 진단은 분류 code뿐이다. Shutdown은 주어진 budget 안에서 끝난다.
func TestCollectorOutageDoesNotAffectTheTerminalBusinessFlow(t *testing.T) {
	isolateOTelEnv(t)
	endpoint := closedPortEndpoint(t)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "authorization=Bearer OUTAGE-HEADER-SECRET-41ac")
	setTraceEnv(t, "otlp", "http/protobuf", endpoint)
	var runtimeLogs lockedBuffer
	rt := startTraceRuntime(t, &runtimeLogs)
	e := newTerminalEnv(t, withTracer(rt.Tracer()))

	started := time.Now()
	s := e.createSession(e.ownerCookie, e.fixture.LabInstanceID)
	b, _ := e.attach(s)
	e.connector.Output(s.ID, []byte(terminalOutputMarker))
	if got := b.binary(); string(got) != terminalOutputMarker {
		t.Fatalf("OUTPUT = %q", got)
	}
	e.deleteSession(s.ID)
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("Collector 장애 중 생성·attach·종료가 %v 걸림", elapsed)
	}
	if opens := len(e.connector.Opens()); opens != 1 {
		t.Fatalf("Trace 장애 때문에 TERMINAL_OPEN이 %d번 전송됨(provider command 재시도 금지)", opens)
	}
	if row := e.row(s.ID); row.Status != "ENDED" || row.EndReason == nil || *row.EndReason != "SESSION_CLOSED" {
		t.Fatalf("종료 기록 = %+v", row)
	}

	flushCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	begin := time.Now()
	_ = rt.Shutdown(flushCtx)
	if elapsed := time.Since(begin); elapsed > 3*time.Second {
		t.Fatalf("Shutdown이 %v 걸림(budget 500ms)", elapsed)
	}
	out := runtimeLogs.String()
	if !strings.Contains(out, `"error_code":"collector_unreachable"`) {
		t.Fatalf("Collector 장애가 진단되지 않음:\n%s", out)
	}
	requireNoneOf(t, "runtime log", out, "OUTAGE-HEADER-SECRET", endpoint, "127.0.0.1")
	requireNoneOf(t, "application log", e.logs.String(), "OUTAGE-HEADER-SECRET", endpoint)
}

// runTraceServer는 실제 Run으로 labbit-server를 시작한다. 환경변수는 호출 전에 설정한다. stop은 SIGTERM에 해당하는 취소 뒤
// Run이 반환하기까지의 시간과 오류를 반환한다. grace는 LABBIT_SHUTDOWN_GRACE로 쓰는 짧은 test budget이다.
func runTraceServer(t *testing.T, fileDSN string, grace time.Duration) (admin, application string, stop func() (time.Duration, error)) {
	t.Helper()
	dsnFile := filepath.Join(t.TempDir(), "database-dsn")
	if err := os.WriteFile(dsnFile, []byte(fileDSN+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	adminAddr, applicationAddr := freeAddr(t), freeAddr(t)
	t.Setenv("LABBIT_ENVIRONMENT", "development")
	t.Setenv("LABBIT_RUNTIME_ROLES", "api")
	t.Setenv("LABBIT_SHUTDOWN_GRACE", grace.String())
	t.Setenv("LABBIT_HTTP_ADDR", applicationAddr)
	t.Setenv("LABBIT_ADMIN_ADDR", adminAddr)
	t.Setenv("LABBIT_LOG_LEVEL", "error")
	t.Setenv("LABBIT_DATABASE_DSN_FILE", dsnFile)
	t.Setenv("LABBIT_DATABASE_DSN", "")
	t.Setenv("LABBIT_PUBLIC_ORIGIN", "https://labbit.test")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v: Trace 설정 때문에 startup이 실패하면 안 됨", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg) }()

	var (
		once     sync.Once
		duration time.Duration
		runErr   error
	)
	stop = func() (time.Duration, error) {
		once.Do(func() {
			begin := time.Now()
			cancel()
			select {
			case runErr = <-done:
			case <-time.After(20 * time.Second):
				t.Error("server did not shut down")
			}
			duration = time.Since(begin)
		})
		return duration, runErr
	}
	t.Cleanup(func() { _, _ = stop() })
	waitForStatus(t, "http://"+adminAddr+"/livez", http.StatusOK)
	return "http://" + adminAddr, "http://" + applicationAddr, stop
}

func migratedDSN(t *testing.T) string {
	t.Helper()
	dsn := postgrestest.NewDatabase(t)
	postgrestest.Migrate(t, dsn, loadEmbeddedMigrations(t))
	return dsn
}

// Trace 설정 오류와 Collector 장애는 startup, /livez, /readyz, 업무 요청을 실패시키지 않는다. 업무 의존성(PostgreSQL) 실패는 숨기지 않는다.
func TestServerStartupAndProbesIgnoreTraceConfigurationAndCollectorOutage(t *testing.T) {
	dsn := migratedDSN(t)
	tests := []struct {
		name string
		env  map[string]string
	}{
		{"Collector 연결 불가", map[string]string{
			"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL": "http/protobuf", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": closedPortEndpoint(t),
		}},
		{"gRPC Collector 연결 불가", map[string]string{
			"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL": "grpc", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": strings.TrimSuffix(closedPortEndpoint(t), "/v1/traces"),
		}},
		{"잘못된 exporter 값", map[string]string{"OTEL_TRACES_EXPORTER": "zipkin"}},
		{"otlp인데 endpoint와 protocol 누락", map[string]string{"OTEL_TRACES_EXPORTER": "otlp"}},
		{"잘못된 protocol", map[string]string{
			"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL": "http/json", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://127.0.0.1:1/v1/traces",
		}},
		{"읽을 수 없는 CA 인증서", map[string]string{
			"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL": "http/protobuf", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "https://127.0.0.1:1/v1/traces",
			"OTEL_EXPORTER_OTLP_TRACES_CERTIFICATE": "/nonexistent/ca.pem",
		}},
		{"잘못된 sampler 값", map[string]string{
			"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL": "http/protobuf", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://127.0.0.1:1/v1/traces",
			"OTEL_TRACES_SAMPLER": "INVALID-SAMPLER",
		}},
		{"잘못된 sampler arg 값", map[string]string{
			"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL": "http/protobuf", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://127.0.0.1:1/v1/traces",
			"OTEL_TRACES_SAMPLER": "traceidratio", "OTEL_TRACES_SAMPLER_ARG": "INVALID-ARG",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateOTelEnv(t)
			for key, value := range tt.env {
				t.Setenv(key, value)
			}
			admin, application, _ := runTraceServer(t, dsn, 5*time.Second)

			waitForStatus(t, admin+"/readyz", http.StatusOK)
			assertStatus(t, admin+"/livez", http.StatusOK)
			// 업무 HTTP는 계약대로 동작한다(인증 없는 요청은 401 Problem Details).
			resp, err := probeClient.Get(application + "/api/v1/me")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized || !bytes.Contains(body, []byte(`"code":"unauthenticated"`)) {
				t.Fatalf("GET /api/v1/me = %d %s, want 401 unauthenticated", resp.StatusCode, body)
			}
		})
	}

	t.Run("업무 의존성(PostgreSQL) 실패는 Trace 설정 오류와 별개로 /readyz에 드러남", func(t *testing.T) {
		isolateOTelEnv(t)
		t.Setenv("OTEL_TRACES_EXPORTER", "otlp") // endpoint/protocol 누락
		admin, _, _ := runTraceServer(t, unusedDSN, 5*time.Second)
		waitForStatus(t, admin+"/readyz", http.StatusServiceUnavailable)
		assertStatus(t, admin+"/livez", http.StatusOK)
	})
}

// 정상 종료는 대기 중인 Span을 flush한다. 실제 Run 조립(LoadConfig → Run → TracerProvider → HTTP handler)의 Span이 OTLP HTTP로 도착한다.
func TestServerFlushesTheTraceOfARealRequestOnGracefulShutdown(t *testing.T) {
	isolateOTelEnv(t)
	receiver := newOTLPReceiver(t, false)
	t.Setenv("OTEL_SERVICE_NAME", "labbit-server-run")
	setTraceEnv(t, "otlp", "http/protobuf", receiver.endpoint())
	_, application, stop := runTraceServer(t, migratedDSN(t), 5*time.Second)

	req, err := http.NewRequest(http.MethodGet, application+"/api/v1/me?path=QUERY-SENTINEL-8d3f", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Traceparent", traceSampled)
	resp, err := probeClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}

	if _, err := stop(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	span := receiver.spanNamed(t, "HTTP GET /api/v1/me")
	if hex.EncodeToString(span.TraceId) != traceIDSampled || hex.EncodeToString(span.ParentSpanId) != "00f067aa0ba902b7" {
		t.Fatalf("export된 Span의 trace=%x parent=%x", span.TraceId, span.ParentSpanId)
	}
	for key, want := range map[string]string{"service.name": "labbit-server-run", "deployment.environment.name": "development", "labbit.component": "api"} {
		if got := receiver.resourceAttr(key); got != want {
			t.Fatalf("resource %s = %q, want %q", key, got, want)
		}
	}
	requireNoneOf(t, "export된 Span", span.String(), "QUERY-SENTINEL", "?path")
}

// 잘못된 sampler가 주어져도 실제 서버 Run() 기동, /livez, /readyz, 업무 요청은 정상 동작하며,
// external OTLP exporter는 비활성화되어 수신기에 어떤 Span도 전송되지 않는다.
func TestInvalidSamplerDisablesOTLPExportOnRealServer(t *testing.T) {
	isolateOTelEnv(t)
	const invalidSamplerVal = "INVALID-SAMPLER-SENTINEL-real-server-123"
	receiver := newOTLPReceiver(t, false)
	t.Setenv("OTEL_SERVICE_NAME", "labbit-server-invalid-sampler")
	t.Setenv("OTEL_TRACES_SAMPLER", invalidSamplerVal)
	setTraceEnv(t, "otlp", "http/protobuf", receiver.endpoint())

	admin, application, stop := runTraceServer(t, migratedDSN(t), 5*time.Second)

	waitForStatus(t, admin+"/readyz", http.StatusOK)
	assertStatus(t, admin+"/livez", http.StatusOK)

	req, err := http.NewRequest(http.MethodGet, application+"/api/v1/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Traceparent", traceSampled)
	resp, err := probeClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}

	if _, err := stop(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// invalid sampler 때문에 external export가 꺼져 Collector hit count == 0이어야 한다.
	if receiver.hitCount() != 0 {
		t.Fatalf("잘못된 sampler인데 Collector에 Span이 export됨: hitCount = %d", receiver.hitCount())
	}
}

// gRPC exporter는 연결할 수 없는 Collector에 retry backoff로 대기하므로 종료 flush가 남은 budget 전체를 쓸 수 있다(budget은 넘지 않는다).
// 플랫폼은 표준 OTEL_BSP_EXPORT_TIMEOUT으로 그 상한을 줄일 수 있다. 코드 변경이 필요 없다는 것을 확인한다.
func TestServerShutdownFlushCanBeShortenedWithTheStandardBatchExportTimeout(t *testing.T) {
	flushDuration := func(t *testing.T, exportTimeout string) time.Duration {
		isolateOTelEnv(t)
		setTraceEnv(t, "otlp", "grpc", strings.TrimSuffix(closedPortEndpoint(t), "/v1/traces"))
		if exportTimeout != "" {
			t.Setenv("OTEL_BSP_EXPORT_TIMEOUT", exportTimeout)
		}
		_, application, stop := runTraceServer(t, migratedDSN(t), 3*time.Second)
		resp, err := probeClient.Get(application + "/api/v1/me")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		elapsed, err := stop()
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		return elapsed
	}
	var shortened, unbounded time.Duration
	t.Run("OTEL_BSP_EXPORT_TIMEOUT=300(ms)", func(t *testing.T) { shortened = flushDuration(t, "300") })
	t.Run("기본값", func(t *testing.T) { unbounded = flushDuration(t, "") })
	t.Logf("gRPC Collector 불능: OTEL_BSP_EXPORT_TIMEOUT=300(ms) → 종료 %v, 기본값 → 종료 %v (LABBIT_SHUTDOWN_GRACE=3s)", shortened, unbounded)
	if shortened > 1500*time.Millisecond {
		t.Fatalf("OTEL_BSP_EXPORT_TIMEOUT=300(ms)인데 종료가 %v 걸림", shortened)
	}
	if unbounded > 3*time.Second+2*time.Second {
		t.Fatalf("기본값에서도 LABBIT_SHUTDOWN_GRACE(3s)를 넘으면 안 됨: %v", unbounded)
	}
}

// 응답하지 않는 Collector 때문에 종료가 LABBIT_SHUTDOWN_GRACE를 넘지 않는다. 관측 flush는 남은 budget 안에서만 하고,
// 실패해도 Run의 결과(이미 끝난 업무 정리)를 바꾸지 않는다. 짧은 test budget이며 production 값이 아니다.
func TestServerShutdownIsBoundedByTheGraceBudgetWhenTheCollectorHangs(t *testing.T) {
	isolateOTelEnv(t)
	receiver := newOTLPReceiver(t, true)
	setTraceEnv(t, "otlp", "http/protobuf", receiver.endpoint())
	const grace = 700 * time.Millisecond
	_, application, stop := runTraceServer(t, migratedDSN(t), grace)

	resp, err := probeClient.Get(application + "/api/v1/me")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	elapsed, err := stop()
	t.Logf("hanging Collector: LABBIT_SHUTDOWN_GRACE=%v, Run 종료까지 %v, err=%v, Collector 호출=%d", grace, elapsed, err, receiver.hitCount())
	if err != nil {
		t.Fatalf("Run() error = %v: 관측 flush 실패가 업무 종료 결과를 바꾸면 안 됨", err)
	}
	if receiver.hitCount() == 0 {
		t.Fatal("종료 flush가 Collector를 호출하지 않음(test가 hang 경로를 거치지 않았음)")
	}
	if elapsed > grace+2*time.Second {
		t.Fatalf("종료가 %v 걸림, want about LABBIT_SHUTDOWN_GRACE(%v)", elapsed, grace)
	}
}
