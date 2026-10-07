package tracing

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"

	"github.com/ktcloud4-SL/labbit-app/internal/observability"
)

// exporter가 환경변수로 읽는 모든 OTEL 설정을 비운다. 개발자 PC나 CI의 값이 test에 섞이지 않게 하고, t.Setenv라 test 뒤 원복한다.
// t.Setenv를 쓰므로 이 package의 test는 병렬로 실행하지 않는다(process-global OpenTelemetry handler도 공유한다).
func isolateOTelEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"OTEL_SERVICE_NAME", "OTEL_TRACES_EXPORTER", "OTEL_TRACES_SAMPLER", "OTEL_TRACES_SAMPLER_ARG", "OTEL_RESOURCE_ATTRIBUTES",
		"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_EXPORTER_OTLP_TIMEOUT",
		"OTEL_EXPORTER_OTLP_CERTIFICATE", "OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE", "OTEL_EXPORTER_OTLP_CLIENT_KEY",
		"OTEL_EXPORTER_OTLP_COMPRESSION", "OTEL_EXPORTER_OTLP_INSECURE",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_HEADERS", "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL",
		"OTEL_EXPORTER_OTLP_TRACES_TIMEOUT", "OTEL_EXPORTER_OTLP_TRACES_CERTIFICATE", "OTEL_EXPORTER_OTLP_TRACES_CLIENT_CERTIFICATE",
		"OTEL_EXPORTER_OTLP_TRACES_CLIENT_KEY", "OTEL_EXPORTER_OTLP_TRACES_COMPRESSION", "OTEL_EXPORTER_OTLP_TRACES_INSECURE",
		"OTEL_BSP_SCHEDULE_DELAY", "OTEL_BSP_EXPORT_TIMEOUT", "OTEL_BSP_MAX_QUEUE_SIZE", "OTEL_BSP_MAX_EXPORT_BATCH_SIZE",
	} {
		// t.Setenv가 원래 값의 복원을 등록한다. SDK는 LookupEnv로 읽으므로(빈 값 = 지원하지 않는 sampler 오류) 비우지 않고 unset한다.
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
}

// envMap은 ConfigFromEnv에 넘기는 환경이다.
type envMap map[string]string

func (e envMap) get(key string) string { return e[key] }

// lockedBuffer는 비동기 export 진단이 동시에 쓰는 log 출력이다.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newTestLogger(out *lockedBuffer) Options {
	return Options{
		Environment: "test-env",
		Component:   "api,realtime",
		Logger:      observability.NewJSONLoggerTo(out, "labbit-server", "bootstrap", "test-env", "debug"),
	}
}

// collector는 OTLP receiver test double이다. production backend 추상화가 아니며 Tempo API를 흉내 내지 않는다.
type collector struct {
	mu       sync.Mutex
	requests []*collectortrace.ExportTraceServiceRequest
	headers  []http.Header
	paths    []string
}

func (c *collector) record(req *collectortrace.ExportTraceServiceRequest, header http.Header, path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, req)
	c.headers = append(c.headers, header.Clone())
	c.paths = append(c.paths, path)
}

func (c *collector) snapshot() ([]*collectortrace.ExportTraceServiceRequest, []http.Header, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*collectortrace.ExportTraceServiceRequest(nil), c.requests...),
		append([]http.Header(nil), c.headers...), append([]string(nil), c.paths...)
}

// spans는 받은 모든 Span을 평평하게 반환한다.
func (c *collector) spans() []*tracepb.Span {
	requests, _, _ := c.snapshot()
	var out []*tracepb.Span
	for _, req := range requests {
		for _, rs := range req.ResourceSpans {
			for _, ss := range rs.ScopeSpans {
				out = append(out, ss.Spans...)
			}
		}
	}
	return out
}

// resourceAttributes는 받은 첫 resource의 attribute를 string 값으로 반환한다.
func (c *collector) resourceAttributes() map[string]string {
	requests, _, _ := c.snapshot()
	attrs := map[string]string{}
	for _, req := range requests {
		for _, rs := range req.ResourceSpans {
			for _, kv := range rs.Resource.GetAttributes() {
				attrs[kv.Key] = kv.Value.GetStringValue()
			}
			return attrs
		}
	}
	return attrs
}

// newHTTPCollector는 OTLP/HTTP protobuf receiver다. status가 200이 아니면 그 status와 body로 응답한다.
func newHTTPCollector(t *testing.T, c *collector, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("collector read body: %v", err)
			return
		}
		if r.Header.Get("Content-Encoding") == "gzip" {
			zr, err := gzip.NewReader(bytes.NewReader(raw))
			if err != nil {
				t.Errorf("collector gzip: %v", err)
				return
			}
			if raw, err = io.ReadAll(zr); err != nil {
				t.Errorf("collector gunzip: %v", err)
				return
			}
		}
		if status != http.StatusOK {
			http.Error(w, body, status)
			return
		}
		req := &collectortrace.ExportTraceServiceRequest{}
		if err := proto.Unmarshal(raw, req); err != nil {
			t.Errorf("collector decode: %v", err)
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		c.record(req, r.Header, r.URL.Path)
		out, _ := proto.Marshal(&collectortrace.ExportTraceServiceResponse{})
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(out)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newHangingHTTPCollector는 요청을 받고 응답하지 않는 receiver다. test가 끝나면 풀린다.
func newHangingHTTPCollector(t *testing.T) *httptest.Server {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(release)
		srv.CloseClientConnections()
		srv.Close()
	})
	return srv
}

// closedPortURL은 연결하면 거절되는 주소다.
func closedPortURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return "http://" + addr
}

type grpcCollector struct {
	collectortrace.UnimplementedTraceServiceServer
	c *collector

	mu       sync.Mutex
	metadata []metadata.MD
}

func (g *grpcCollector) Export(ctx context.Context, req *collectortrace.ExportTraceServiceRequest) (*collectortrace.ExportTraceServiceResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	g.mu.Lock()
	g.metadata = append(g.metadata, md)
	g.mu.Unlock()
	g.c.record(req, http.Header{}, "")
	return &collectortrace.ExportTraceServiceResponse{}, nil
}

// newGRPCCollector는 실제 OTLP/gRPC TraceService receiver다. 반환한 URL은 plaintext(http) endpoint다.
func newGRPCCollector(t *testing.T, c *collector) (string, *grpcCollector) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	impl := &grpcCollector{c: c}
	srv := grpc.NewServer()
	collectortrace.RegisterTraceServiceServer(srv, impl)
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(srv.Stop)
	return "http://" + l.Addr().String(), impl
}

func (g *grpcCollector) authorization() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for _, md := range g.metadata {
		out = append(out, md.Get("authorization")...)
	}
	return out
}

func attributeString(attrs []*commonpb.KeyValue, key string) (string, bool) {
	for _, kv := range attrs {
		if kv.Key == key {
			return kv.Value.GetStringValue(), true
		}
	}
	return "", false
}

func shutdownWithin(t *testing.T, r *Runtime, budget time.Duration) (time.Duration, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	start := time.Now()
	err := r.Shutdown(ctx)
	return time.Since(start), err
}

func mustNotContain(t *testing.T, what, haystack string, needles ...string) {
	t.Helper()
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			t.Fatalf("%s에 %q가 있음:\n%s", what, needle, haystack)
		}
	}
}
