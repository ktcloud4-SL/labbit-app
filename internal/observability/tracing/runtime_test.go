package tracing

import (
	"context"
	"encoding/hex"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/ktcloud4-SL/labbit-app/internal/server/tracecontext"
)

const (
	sampledParent   = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	unsampledParent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-00"

	// headerSecret은 OTLP header Secret이다. 어떤 log, Span, resource에도 나타나면 안 된다.
	headerSecret = "HEADER-SECRET-SENTINEL-7f3a"
)

func startSpan(rt *Runtime, ctx context.Context, name string) (context.Context, trace.Span) {
	return rt.Tracer().Start(ctx, name)
}

// none은 export만 끈다. Span과 valid SpanContext는 그대로 만들고, 외부 exporter는 만들지 않는다.
func TestNoneModeCreatesSpansWithoutExporter(t *testing.T) {
	isolateOTelEnv(t)
	var logs lockedBuffer
	rt := Start(context.Background(), ConfigFromEnv(envMap{EnvExporter: "none"}.get), newTestLogger(&logs))

	if rt.Exporting() {
		t.Fatal("none인데 exporter가 연결됨")
	}
	_, span := startSpan(rt, context.Background(), "unit")
	sc := span.SpanContext()
	span.End()
	if !sc.IsValid() || !sc.TraceID().IsValid() || !sc.SpanID().IsValid() {
		t.Fatalf("none인데 SpanContext가 유효하지 않음: %+v", sc)
	}
	if !span.IsRecording() && !sc.IsSampled() {
		t.Fatal("부모가 없는 root Span이 샘플링되지 않음")
	}
	if elapsed, err := shutdownWithin(t, rt, time.Second); err != nil || elapsed > 500*time.Millisecond {
		t.Fatalf("none Shutdown = %v, %v", elapsed, err)
	}
	if !strings.Contains(logs.String(), `"exporter":"none"`) {
		t.Fatalf("none이 로그로 확인되지 않음:\n%s", logs.String())
	}
}

// none에서도 W3C Context 전파가 유지된다. 부모의 trace ID를 이어받고 sampled flag를 임의로 바꾸지 않는다.
func TestNoneModeContinuesRemoteParentAndKeepsSampledFlag(t *testing.T) {
	isolateOTelEnv(t)
	rt := Start(context.Background(), ConfigFromEnv(envMap{}.get), newTestLogger(&lockedBuffer{}))
	defer shutdownWithin(t, rt, time.Second)

	tests := []struct {
		name        string
		traceparent string
		tracestate  string
		wantTrace   string
		wantSampled bool
	}{
		{"sampled parent", sampledParent, "vendor=opaque", "4bf92f3577b34da6a3ce929d0e0e4736", true},
		{"unsampled parent는 sampled=1로 바뀌지 않음", unsampledParent, "vendor=opaque", "0af7651916cd43dd8448eb211c80319c", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parent := tracecontext.NewContext(context.Background(), tracecontext.Normalize(tt.traceparent, tt.tracestate))
			ctx, span := startSpan(rt, parent, "child")
			defer span.End()
			sc := span.SpanContext()
			if sc.TraceID().String() != tt.wantTrace {
				t.Fatalf("trace ID = %s, want %s", sc.TraceID(), tt.wantTrace)
			}
			if sc.IsSampled() != tt.wantSampled {
				t.Fatalf("sampled = %v, want %v", sc.IsSampled(), tt.wantSampled)
			}
			if sc.TraceState().String() != tt.tracestate {
				t.Fatalf("tracestate = %q, want %q", sc.TraceState().String(), tt.tracestate)
			}
			// 이 Span의 Context가 그대로 직렬화되어 Connector wire로 나간다.
			wire := tracecontext.FromContext(ctx)
			if wire.TraceID() != tt.wantTrace || strings.HasSuffix(wire.Traceparent, "-01") != tt.wantSampled {
				t.Fatalf("직렬화한 Context = %+v", wire)
			}
			if wire.Traceparent == tt.traceparent {
				t.Fatal("자식 Span의 traceparent가 부모와 같음(새 Span ID가 필요함)")
			}
		})
	}
}

func TestRuntimeResourceIdentifiesServiceEnvironmentAndComponent(t *testing.T) {
	isolateOTelEnv(t)
	var c collector
	srv := newHTTPCollector(t, &c, http.StatusOK, "")
	cfg := ConfigFromEnv(envMap{
		EnvServiceName: "labbit-test-service", EnvExporter: "otlp", EnvProtocol: "http/protobuf", EnvEndpoint: srv.URL + "/v1/traces",
	}.get)
	var logs lockedBuffer
	rt := Start(context.Background(), cfg, newTestLogger(&logs))

	_, span := startSpan(rt, context.Background(), "resource-check")
	span.End()
	if _, err := shutdownWithin(t, rt, 5*time.Second); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	got := c.resourceAttributes()
	want := map[string]string{
		"service.name":                "labbit-test-service",
		"deployment.environment.name": "test-env",
		"labbit.component":            "api,realtime",
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("resource %s = %q, want %q (resource = %v)", key, got[key], value, got)
		}
	}
}

// 실제 OTLP HTTP/protobuf exporter code path가 network 경계까지 간다. 최종 Trace URL(/v1/traces)로 요청하고 표준 환경변수의 header를 쓴다.
func TestOTLPHTTPProtobufExportsSpansToCollector(t *testing.T) {
	isolateOTelEnv(t)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "authorization=Bearer "+headerSecret+",x-tenant=labbit")
	var c collector
	srv := newHTTPCollector(t, &c, http.StatusOK, "")
	cfg := ConfigFromEnv(envMap{EnvExporter: "otlp", EnvProtocol: "http/protobuf", EnvEndpoint: srv.URL + "/v1/traces"}.get)
	var logs lockedBuffer
	rt := Start(context.Background(), cfg, newTestLogger(&logs))
	if !rt.Exporting() {
		t.Fatalf("otlp인데 exporter가 연결되지 않음:\n%s", logs.String())
	}

	parent := tracecontext.NewContext(context.Background(), tracecontext.Normalize(sampledParent, ""))
	ctx, server := startSpan(rt, parent, "HTTP POST /api/v1/example")
	_, child := startSpan(rt, ctx, "Connector EXAMPLE")
	child.End()
	server.End()
	wantTrace, _ := hex.DecodeString("4bf92f3577b34da6a3ce929d0e0e4736")

	if _, err := shutdownWithin(t, rt, 5*time.Second); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	_, headers, paths := c.snapshot()
	if len(paths) == 0 || paths[0] != "/v1/traces" {
		t.Fatalf("Collector 요청 경로 = %v, want /v1/traces", paths)
	}
	if got := headers[0].Get("Authorization"); got != "Bearer "+headerSecret {
		t.Fatalf("exporter가 환경변수의 header를 쓰지 않음: Authorization = %q", got)
	}
	if got := headers[0].Get("X-Tenant"); got != "labbit" {
		t.Fatalf("X-Tenant = %q", got)
	}
	if got := headers[0].Get("Content-Type"); got != "application/x-protobuf" {
		t.Fatalf("Content-Type = %q", got)
	}
	spans := c.spans()
	if len(spans) != 2 {
		t.Fatalf("Collector가 받은 Span = %d, want 2", len(spans))
	}
	names := map[string][]byte{}
	for _, s := range spans {
		names[s.Name] = s.TraceId
		if string(s.TraceId) != string(wantTrace) {
			t.Fatalf("Span %q의 trace ID = %x, want incoming parent의 trace", s.Name, s.TraceId)
		}
	}
	if _, ok := names["HTTP POST /api/v1/example"]; !ok {
		t.Fatalf("server Span이 없음: %v", names)
	}
	// header Secret은 log에도 resource에도 Span에도 없다.
	mustNotContain(t, "log", logs.String(), headerSecret)
	requests, _, _ := c.snapshot()
	for _, req := range requests {
		mustNotContain(t, "수집된 Trace", req.String(), headerSecret)
	}
}

// Runtime Contract가 허용하는 gRPC protocol도 실제 gRPC receiver로 내보낸다.
func TestOTLPGRPCExportsSpansToCollector(t *testing.T) {
	isolateOTelEnv(t)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "authorization=Bearer "+headerSecret)
	var c collector
	endpoint, receiver := newGRPCCollector(t, &c)
	cfg := ConfigFromEnv(envMap{EnvServiceName: "labbit-grpc", EnvExporter: "otlp", EnvProtocol: "grpc", EnvEndpoint: endpoint}.get)
	var logs lockedBuffer
	rt := Start(context.Background(), cfg, newTestLogger(&logs))
	if !rt.Exporting() {
		t.Fatalf("grpc exporter가 연결되지 않음:\n%s", logs.String())
	}

	_, span := startSpan(rt, context.Background(), "HTTP GET /api/v1/example")
	wantTrace := span.SpanContext().TraceID()
	span.End()
	if _, err := shutdownWithin(t, rt, 5*time.Second); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	spans := c.spans()
	if len(spans) != 1 || hex.EncodeToString(spans[0].TraceId) != wantTrace.String() {
		t.Fatalf("gRPC Collector가 받은 Span = %v, want trace %s", spans, wantTrace)
	}
	if got := c.resourceAttributes()["service.name"]; got != "labbit-grpc" {
		t.Fatalf("service.name = %q", got)
	}
	if got := receiver.authorization(); len(got) == 0 || got[0] != "Bearer "+headerSecret {
		t.Fatalf("gRPC metadata authorization = %v, want 환경변수의 header", got)
	}
	mustNotContain(t, "log", logs.String(), headerSecret)
}

// 설정 오류는 startup을 실패시키지 않는다. export만 끄고 Span은 계속 만든다. 진단에는 환경변수 이름과 분류만 있다.
func TestInvalidTraceConfigDisablesExportButKeepsTracing(t *testing.T) {
	isolateOTelEnv(t)
	endpointSecret := "ENDPOINT-CRED-SENTINEL-9c1d"
	cfg := ConfigFromEnv(envMap{
		EnvExporter: "otlp", EnvProtocol: "http/protobuf", EnvEndpoint: "http://user:" + endpointSecret + "@alloy.example.test:4318/v1/traces",
	}.get)
	var logs lockedBuffer
	rt := Start(context.Background(), cfg, newTestLogger(&logs))

	if rt.Exporting() {
		t.Fatal("설정 오류인데 exporter가 연결됨")
	}
	_, span := startSpan(rt, context.Background(), "still-traced")
	if !span.SpanContext().IsValid() {
		t.Fatal("설정 오류 때문에 Span Context가 사라짐")
	}
	span.End()
	out := logs.String()
	for _, want := range []string{`"env":"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"`, `"reason":"invalid_value"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("진단에 %s가 없음:\n%s", want, out)
		}
	}
	mustNotContain(t, "log", out, endpointSecret, "alloy.example.test")
	if _, err := shutdownWithin(t, rt, time.Second); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

// exporter가 환경변수를 해석하다 실패하면 header 한 쌍 같은 원문을 internal logger로 출력한다. 기본 handler라면 stderr로 나갔을 값이다.
// 안전한 logger로 바뀌어 있으므로 원문이 없고, 일부 설정(인증 없는 export)으로 계속하지 않고 export를 끈다.
func TestMalformedHeaderEnvironmentDoesNotLeakAndDisablesExport(t *testing.T) {
	isolateOTelEnv(t)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "Bearer "+headerSecret) // '='가 없는 잘못된 header
	var c collector
	srv := newHTTPCollector(t, &c, http.StatusOK, "")
	cfg := ConfigFromEnv(envMap{EnvExporter: "otlp", EnvProtocol: "http/protobuf", EnvEndpoint: srv.URL + "/v1/traces"}.get)
	var logs lockedBuffer
	rt := Start(context.Background(), cfg, newTestLogger(&logs))

	if rt.Exporting() {
		t.Fatal("header 해석에 실패했는데 export를 계속함")
	}
	_, span := startSpan(rt, context.Background(), "x")
	span.End()
	_, _ = shutdownWithin(t, rt, time.Second)
	out := logs.String()
	if !strings.Contains(out, `"reason":"invalid_headers"`) {
		t.Fatalf("진단에 reason=invalid_headers가 없음:\n%s", out)
	}
	mustNotContain(t, "log", out, headerSecret, srv.URL)
	if got := c.spans(); len(got) != 0 {
		t.Fatalf("비활성화한 exporter가 Span을 보냄: %d", len(got))
	}
}

func TestUnreadableCertificateEnvironmentDisablesExportWithoutLoggingThePath(t *testing.T) {
	isolateOTelEnv(t)
	const pathSecret = "/run/secrets/CA-PATH-SENTINEL-2b8e.pem"
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_CERTIFICATE", pathSecret)
	cfg := ConfigFromEnv(envMap{EnvExporter: "otlp", EnvProtocol: "grpc", EnvEndpoint: "https://collector.example.test:4317"}.get)
	var logs lockedBuffer
	rt := Start(context.Background(), cfg, newTestLogger(&logs))

	if rt.Exporting() {
		t.Fatal("인증서를 읽지 못했는데 시스템 CA로 export를 계속함")
	}
	out := logs.String()
	if !strings.Contains(out, `"reason":"invalid_certificate"`) {
		t.Fatalf("진단에 reason=invalid_certificate가 없음:\n%s", out)
	}
	mustNotContain(t, "log", out, pathSecret, "CA-PATH-SENTINEL")
	_, _ = shutdownWithin(t, rt, time.Second)
}

// 잘못된 sampler 설정은 Runtime Contract failure policy에 따라 안전한 진단 후 외부 export를 비활성화(fail-closed)한다.
// fallback으로 AlwaysSample + OTLP export가 활성화되는 것을 막는다.
func TestInvalidSamplerDisablesExternalExportAndFailsClosed(t *testing.T) {
	isolateOTelEnv(t)
	const (
		invalidSamplerSentinel = "INVALID-SAMPLER-SENTINEL-999"
		invalidArgSentinel     = "INVALID-ARG-SENTINEL-888"
	)

	t.Run("ConfigFromEnv를 통한 잘못된 sampler 차단", func(t *testing.T) {
		isolateOTelEnv(t)
		t.Setenv("OTEL_TRACES_SAMPLER", invalidSamplerSentinel)
		var c collector
		srv := newHTTPCollector(t, &c, http.StatusOK, "")
		cfg := ConfigFromEnv(envMap{
			EnvExporter: "otlp", EnvProtocol: "http/protobuf", EnvEndpoint: srv.URL + "/v1/traces",
			EnvSampler: invalidSamplerSentinel,
		}.get)

		var logs lockedBuffer
		rt := Start(context.Background(), cfg, newTestLogger(&logs))
		if rt.Exporting() {
			t.Fatal("잘못된 sampler인데 exporter가 활성화됨 (fail-closed 위반)")
		}

		// Tracing Context는 사용 가능해야 한다.
		_, span := startSpan(rt, context.Background(), "test-span")
		sc := span.SpanContext()
		span.End()
		if !sc.IsValid() || !sc.TraceID().IsValid() {
			t.Fatalf("Tracing Context가 유효하지 않음: %+v", sc)
		}

		if _, err := shutdownWithin(t, rt, time.Second); err != nil {
			t.Fatalf("Shutdown() error = %v", err)
		}
		if got := c.spans(); len(got) != 0 {
			t.Fatalf("Collector가 Span을 수신함 (hit count != 0): %d개", len(got))
		}

		out := logs.String()
		if !strings.Contains(out, `"reason":"invalid_value"`) || !strings.Contains(out, `"env":"OTEL_TRACES_SAMPLER"`) {
			t.Fatalf("로그에 안전한 진단이 없음:\n%s", out)
		}
		mustNotContain(t, "log", out, invalidSamplerSentinel)
	})

	t.Run("ConfigFromEnv를 통한 잘못된 sampler arg 차단", func(t *testing.T) {
		isolateOTelEnv(t)
		t.Setenv("OTEL_TRACES_SAMPLER", "traceidratio")
		t.Setenv("OTEL_TRACES_SAMPLER_ARG", invalidArgSentinel)
		var c collector
		srv := newHTTPCollector(t, &c, http.StatusOK, "")
		cfg := ConfigFromEnv(envMap{
			EnvExporter: "otlp", EnvProtocol: "http/protobuf", EnvEndpoint: srv.URL + "/v1/traces",
			EnvSampler: "traceidratio", EnvSamplerArg: invalidArgSentinel,
		}.get)

		var logs lockedBuffer
		rt := Start(context.Background(), cfg, newTestLogger(&logs))
		if rt.Exporting() {
			t.Fatal("잘못된 sampler arg인데 exporter가 활성화됨")
		}

		_, span := startSpan(rt, context.Background(), "test-span")
		span.End()

		_, _ = shutdownWithin(t, rt, time.Second)
		if got := c.spans(); len(got) != 0 {
			t.Fatalf("Collector가 Span을 수신함: %d개", len(got))
		}

		out := logs.String()
		if !strings.Contains(out, `"env":"OTEL_TRACES_SAMPLER_ARG"`) {
			t.Fatalf("진단에 OTEL_TRACES_SAMPLER_ARG가 없음:\n%s", out)
		}
		mustNotContain(t, "log", out, invalidArgSentinel)
	})

	t.Run("Start 레벨에서 SDK 내부 env 진단 capture를 통한 export 비활성화 (심층 방어)", func(t *testing.T) {
		isolateOTelEnv(t)
		const directSentinel = "INVALID-DIRECT-SENTINEL-777"
		t.Setenv("OTEL_TRACES_SAMPLER", directSentinel)
		var c collector
		srv := newHTTPCollector(t, &c, http.StatusOK, "")

		// ConfigFromEnv를 거치지 않고 직접 ExporterOTLP로 설정된 Config 전달
		cfg := Config{
			ServiceName: "test",
			Exporter:    ExporterOTLP,
			Protocol:    ProtocolHTTPProtobuf,
			Endpoint:    srv.URL + "/v1/traces",
		}

		var logs lockedBuffer
		rt := Start(context.Background(), cfg, newTestLogger(&logs))
		if rt.Exporting() {
			t.Fatal("SDK 진단에서 sampler 오류가 감지되었는데 exporter가 활성화됨")
		}

		_, span := startSpan(rt, context.Background(), "test-span")
		span.End()

		_, _ = shutdownWithin(t, rt, time.Second)
		if got := c.spans(); len(got) != 0 {
			t.Fatalf("Collector가 Span을 수신함: %d개", len(got))
		}

		out := logs.String()
		if !strings.Contains(out, `"reason":"invalid_sampler"`) {
			t.Fatalf("진단에 reason=invalid_sampler가 없음:\n%s", out)
		}
		mustNotContain(t, "log", out, directSentinel)
	})

	t.Run("set-but-empty sampler arg with traceidratio (upstream LookupEnv 재현)", func(t *testing.T) {
		isolateOTelEnv(t)
		var c collector
		srv := newHTTPCollector(t, &c, http.StatusOK, "")
		t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
		t.Setenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "http/protobuf")
		t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", srv.URL+"/v1/traces")
		t.Setenv("OTEL_TRACES_SAMPLER", "traceidratio")
		t.Setenv("OTEL_TRACES_SAMPLER_ARG", "") // unset이 아닌 명시적 empty env
		cfg := ConfigFromEnv(os.Getenv)

		var logs lockedBuffer
		rt := Start(context.Background(), cfg, newTestLogger(&logs))
		if rt.Exporting() {
			t.Fatal("set-but-empty sampler arg인데 exporter가 활성화됨 (fail-closed 위반)")
		}

		_, span := startSpan(rt, context.Background(), "test-span")
		sc := span.SpanContext()
		span.End()
		if !sc.IsValid() || !sc.TraceID().IsValid() {
			t.Fatalf("Tracing Context가 유효하지 않음: %+v", sc)
		}

		_, _ = shutdownWithin(t, rt, time.Second)
		if got := c.spans(); len(got) != 0 {
			t.Fatalf("Collector가 Span을 수신함: %d개", len(got))
		}

		out := logs.String()
		if !strings.Contains(out, `"reason":"invalid_sampler"`) {
			t.Fatalf("진단에 reason=invalid_sampler가 없음:\n%s", out)
		}
	})

	t.Run("set-but-empty sampler arg with parentbased_traceidratio", func(t *testing.T) {
		isolateOTelEnv(t)
		var c collector
		srv := newHTTPCollector(t, &c, http.StatusOK, "")
		t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
		t.Setenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "http/protobuf")
		t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", srv.URL+"/v1/traces")
		t.Setenv("OTEL_TRACES_SAMPLER", "parentbased_traceidratio")
		t.Setenv("OTEL_TRACES_SAMPLER_ARG", "")
		cfg := ConfigFromEnv(os.Getenv)

		var logs lockedBuffer
		rt := Start(context.Background(), cfg, newTestLogger(&logs))
		if rt.Exporting() {
			t.Fatal("set-but-empty parentbased_traceidratio인데 exporter가 활성화됨")
		}

		_, span := startSpan(rt, context.Background(), "test-span")
		span.End()

		_, _ = shutdownWithin(t, rt, time.Second)
		if got := c.spans(); len(got) != 0 {
			t.Fatalf("Collector가 Span을 수신함: %d개", len(got))
		}

		out := logs.String()
		if !strings.Contains(out, `"reason":"invalid_sampler"`) {
			t.Fatalf("진단에 reason=invalid_sampler가 없음:\n%s", out)
		}
	})
}

// discardProvider는 TracerProvider가 소유한 BatchSpanProcessor 및 exporter 수명을 정상 종료함을 검증한다.
type testTrackingExporter struct {
	mu            sync.Mutex
	shutdownCalls int
}

func (e *testTrackingExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error {
	return nil
}
func (e *testTrackingExporter) Shutdown(context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.shutdownCalls++
	return nil
}

func TestDiscardProviderShutsDownProviderAndOwnedProcessor(t *testing.T) {
	exp := &testTrackingExporter{}
	provider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp))

	startedAt := time.Now()
	discardProvider(provider)
	if elapsed := time.Since(startedAt); elapsed > 2*time.Second {
		t.Fatalf("discardProvider가 bounded timeout 안에 완료되지 않음: %v", elapsed)
	}

	exp.mu.Lock()
	calls := exp.shutdownCalls
	exp.mu.Unlock()

	if calls != 1 {
		t.Fatalf("discardProvider가 exporter를 정확히 1회 종료하지 않음: %d회", calls)
	}
}

// Collector 장애(연결 거절)는 Span 생성과 Shutdown을 막지 않는다.
func TestCollectorConnectionRefusedDoesNotBlockTracing(t *testing.T) {
	isolateOTelEnv(t)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "authorization=Bearer "+headerSecret)
	endpoint := closedPortURL(t)
	cfg := ConfigFromEnv(envMap{EnvExporter: "otlp", EnvProtocol: "http/protobuf", EnvEndpoint: endpoint + "/v1/traces"}.get)
	var logs lockedBuffer

	startedAt := time.Now()
	rt := Start(context.Background(), cfg, newTestLogger(&logs))
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("Collector가 없는데 Start가 %v 걸림", elapsed)
	}
	if !rt.Exporting() {
		t.Fatal("exporter 생성은 Collector 연결에 의존하면 안 됨")
	}

	spanStart := time.Now()
	for i := 0; i < 200; i++ {
		_, span := startSpan(rt, context.Background(), "request")
		span.End()
	}
	if elapsed := time.Since(spanStart); elapsed > time.Second {
		t.Fatalf("Collector 장애 중 Span 200개 생성이 %v 걸림(요청 경로가 export를 기다림)", elapsed)
	}

	elapsed, err := shutdownWithin(t, rt, 700*time.Millisecond)
	if elapsed > 3*time.Second {
		t.Fatalf("Shutdown이 %v 걸림(budget 700ms)", elapsed)
	}
	t.Logf("connection refused: Shutdown %v, err=%v", elapsed, err)
	// flush 시도의 실패는 분류 code로 한 번만 진단되고 endpoint, header Secret, 오류 원문은 없다.
	out := logs.String()
	if !strings.Contains(out, `"message":"OTLP export 실패"`) || !strings.Contains(out, `"error_code":"`+codeCollectorUnreachable+`"`) {
		t.Fatalf("연결 거절이 collector_unreachable로 진단되지 않음:\n%s", out)
	}
	mustNotContain(t, "log", out, headerSecret, endpoint, "127.0.0.1", "connection refused")
}

// Collector가 거절 응답에 본문을 실어도 그 본문(과 URL)은 진단에 나오지 않는다. 분류 code만 남는다.
func TestCollectorRejectionBodyIsNotLogged(t *testing.T) {
	isolateOTelEnv(t)
	const bodySentinel = "COLLECTOR-BODY-SENTINEL-5d20"
	var c collector
	srv := newHTTPCollector(t, &c, http.StatusBadRequest, bodySentinel)
	cfg := ConfigFromEnv(envMap{EnvExporter: "otlp", EnvProtocol: "http/protobuf", EnvEndpoint: srv.URL + "/v1/traces"}.get)
	var logs lockedBuffer
	rt := Start(context.Background(), cfg, newTestLogger(&logs))

	_, span := startSpan(rt, context.Background(), "request")
	span.End()
	if _, err := shutdownWithin(t, rt, 5*time.Second); err != nil {
		t.Logf("Shutdown() error = %v", err)
	}
	out := logs.String()
	if !strings.Contains(out, `"error_code":"export_rejected"`) {
		t.Fatalf("거절 응답이 export_rejected로 분류되지 않음:\n%s", out)
	}
	mustNotContain(t, "log", out, bodySentinel, srv.URL, "/v1/traces")
}

// 응답하지 않는 Collector 때문에 종료가 무기한 지연되지 않는다. 전달한 ctx(남은 shutdown budget)가 상한이다.
func TestShutdownIsBoundedByTheGivenBudgetWhenCollectorHangs(t *testing.T) {
	isolateOTelEnv(t)
	srv := newHangingHTTPCollector(t)
	cfg := ConfigFromEnv(envMap{EnvExporter: "otlp", EnvProtocol: "http/protobuf", EnvEndpoint: srv.URL + "/v1/traces"}.get)
	var logs lockedBuffer
	rt := Start(context.Background(), cfg, newTestLogger(&logs))

	_, span := startSpan(rt, context.Background(), "request")
	span.End()

	const budget = 300 * time.Millisecond
	elapsed, err := shutdownWithin(t, rt, budget)
	t.Logf("hanging Collector: budget=%v elapsed=%v err=%v", budget, elapsed, err)
	if err == nil {
		t.Fatal("응답하지 않는 Collector인데 Shutdown이 성공함")
	}
	if elapsed < budget || elapsed > budget+700*time.Millisecond {
		t.Fatalf("Shutdown elapsed = %v, want about the %v budget", elapsed, budget)
	}
	if !strings.Contains(logs.String(), `"error_code":"export_timeout"`) {
		t.Fatalf("flush 실패 진단이 export_timeout이 아님:\n%s", logs.String())
	}
}

// 이미 끝난 budget으로도 즉시 반환한다. 관측 flush가 업무 정리를 늦추지 않는다.
func TestShutdownWithExhaustedBudgetReturnsImmediately(t *testing.T) {
	isolateOTelEnv(t)
	srv := newHangingHTTPCollector(t)
	cfg := ConfigFromEnv(envMap{EnvExporter: "otlp", EnvProtocol: "http/protobuf", EnvEndpoint: srv.URL + "/v1/traces"}.get)
	rt := Start(context.Background(), cfg, newTestLogger(&lockedBuffer{}))
	_, span := startSpan(rt, context.Background(), "request")
	span.End()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_ = rt.Shutdown(ctx)
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("소진된 budget의 Shutdown이 %v 걸림", elapsed)
	}
}

func TestShutdownIsIdempotent(t *testing.T) {
	isolateOTelEnv(t)
	rt := Start(context.Background(), ConfigFromEnv(envMap{}.get), newTestLogger(&lockedBuffer{}))
	first := rt.Shutdown(context.Background())
	second := rt.Shutdown(context.Background())
	if first != nil || second != nil {
		t.Fatalf("Shutdown() = %v, %v", first, second)
	}
	// Shutdown 뒤에도 요청 경로가 panic하지 않는다. Span은 더 이상 기록되지 않는다.
	_, span := startSpan(rt, context.Background(), "after-shutdown")
	span.End()
}
