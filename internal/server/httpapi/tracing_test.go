package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/ktcloud4-SL/labbit-app/internal/observability"
	"github.com/ktcloud4-SL/labbit-app/internal/server/auth"
	"github.com/ktcloud4-SL/labbit-app/internal/server/tracecontext"
)

const (
	sampledTraceparent   = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	unsampledTraceparent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-00"
	sampledTraceID       = "4bf92f3577b34da6a3ce929d0e0e4736"
	unsampledTraceID     = "0af7651916cd43dd8448eb211c80319c"
	incomingParentSpanID = "00f067aa0ba902b7"

	invalidTraceMarker = "INVALID-TRACE-MARKER-4e1b"
)

// ctxAuth는 handler가 하위 계층에 넘긴 context를 기록한다. Span이 request context로 이어지는지 확인하는 데 쓴다.
type ctxAuth struct {
	*fakeAuth
	contexts []context.Context
}

func (c *ctxAuth) Authenticate(ctx context.Context, token auth.SessionToken) (auth.Principal, error) {
	c.contexts = append(c.contexts, ctx)
	return c.fakeAuth.Authenticate(ctx, token)
}

type tracingHarness struct {
	handler  http.Handler
	recorder *tracetest.SpanRecorder
	auth     *ctxAuth
	files    *fakeFiles
	logs     *bytes.Buffer
}

// newTracingHarness는 SDK Tracer(in-memory recorder)가 연결된 handler를 만든다. tracer가 false이면 Tracer를 넘기지 않는다(noop).
func newTracingHarness(t *testing.T, withTracer bool) *tracingHarness {
	t.Helper()
	fake := newFakeAuth()
	fake.sessions[fileCookie] = fake.principal
	h := &tracingHarness{
		recorder: tracetest.NewSpanRecorder(),
		auth:     &ctxAuth{fakeAuth: fake},
		files:    &fakeFiles{maxBytes: 1 << 20},
		logs:     &bytes.Buffer{},
	}
	opts := Options{
		Auth:         h.auth,
		Classes:      &fakeClasses{},
		Files:        h.files,
		PublicOrigin: trustedOrigin,
		Logger:       observability.NewJSONLoggerTo(h.logs, "labbit-server", "httpapi", "development", "info"),
	}
	if withTracer {
		provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(h.recorder))
		t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
		opts.Tracer = provider.Tracer("httpapi-test")
	}
	handler, err := New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	h.handler = handler
	return h
}

func (h *tracingHarness) do(method, path, body string, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, trustedOrigin+path, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: fileCookie})
	for _, mod := range mods {
		mod(req)
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec
}

func (h *tracingHarness) onlySpan(t *testing.T) sdktrace.ReadOnlySpan {
	t.Helper()
	spans := h.recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("끝난 Span = %d, want 1", len(spans))
	}
	return spans[0]
}

func attrValue(span sdktrace.ReadOnlySpan, key string) (attribute.Value, bool) {
	for _, kv := range span.Attributes() {
		if string(kv.Key) == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

// dumpSpan은 Span의 이름, 모든 attribute, event, status, resource를 하나의 문자열로 만든다. 금지 값이 어디에도 없는지 확인하는 데 쓴다.
func dumpSpan(span sdktrace.ReadOnlySpan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "name=%s kind=%s status=%s/%s\n", span.Name(), span.SpanKind(), span.Status().Code, span.Status().Description)
	for _, kv := range span.Attributes() {
		fmt.Fprintf(&b, "attr %s=%s\n", kv.Key, kv.Value.Emit())
	}
	for _, event := range span.Events() {
		fmt.Fprintf(&b, "event %s\n", event.Name)
		for _, kv := range event.Attributes {
			fmt.Fprintf(&b, "event-attr %s=%s\n", kv.Key, kv.Value.Emit())
		}
	}
	for _, kv := range span.Resource().Attributes() {
		fmt.Fprintf(&b, "resource %s=%s\n", kv.Key, kv.Value.Emit())
	}
	for _, link := range span.Links() {
		fmt.Fprintf(&b, "link %s\n", link.SpanContext.TraceID())
	}
	return b.String()
}

func mustNotContain(t *testing.T, what, haystack string, needles ...string) {
	t.Helper()
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			t.Fatalf("%s에 %q가 있음:\n%s", what, needle, haystack)
		}
	}
}

func withTraceHeaders(traceparent, tracestate string) func(*http.Request) {
	return func(r *http.Request) {
		if traceparent != "" {
			r.Header.Set("Traceparent", traceparent)
		}
		if tracestate != "" {
			r.Header.Set("Tracestate", tracestate)
		}
	}
}

// incoming Trace가 없으면 SaaS가 새 Trace를 시작한다. 업무 응답은 그대로다.
func TestHTTPServerSpanStartsNewTraceWithoutIncomingContext(t *testing.T) {
	h := newTracingHarness(t, true)
	rec := h.do(http.MethodGet, "/api/v1/me", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	span := h.onlySpan(t)
	if span.Name() != "HTTP GET /api/v1/me" || span.SpanKind() != trace.SpanKindServer {
		t.Fatalf("name=%q kind=%v", span.Name(), span.SpanKind())
	}
	if !span.SpanContext().IsValid() || span.Parent().IsValid() {
		t.Fatalf("새 root Span이어야 함: ctx=%+v parent=%+v", span.SpanContext(), span.Parent())
	}
	if v, _ := attrValue(span, "http.route"); v.AsString() != "/api/v1/me" {
		t.Fatalf("http.route = %q", v.AsString())
	}
	if v, _ := attrValue(span, "http.response.status_code"); v.AsInt64() != 200 {
		t.Fatalf("status code attribute = %v", v)
	}
	if span.Status().Code == codes.Error {
		t.Fatal("200 응답이 Error status")
	}
}

// 유효한 W3C Context는 원격 parent로 이어진다. sampled도 unsampled도 입력 그대로이며 tracestate가 보존된다.
func TestHTTPServerSpanContinuesValidRemoteParent(t *testing.T) {
	t.Run("sampled", func(t *testing.T) {
		h := newTracingHarness(t, true)
		rec := h.do(http.MethodGet, "/api/v1/me", "", withTraceHeaders(sampledTraceparent, "vendor=opaque"))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		span := h.onlySpan(t)
		if got := span.SpanContext().TraceID().String(); got != sampledTraceID {
			t.Fatalf("trace ID = %s, want 들어온 trace", got)
		}
		parent := span.Parent()
		if parent.SpanID().String() != incomingParentSpanID || !parent.IsRemote() {
			t.Fatalf("parent = %+v, want remote %s", parent, incomingParentSpanID)
		}
		if span.SpanContext().SpanID().String() == incomingParentSpanID {
			t.Fatal("자식 Span이 parent의 Span ID를 그대로 씀")
		}
		if got := span.SpanContext().TraceState().String(); got != "vendor=opaque" {
			t.Fatalf("tracestate = %q", got)
		}
		// 하위 계층(Connector command로 이어지는 service)이 받는 context도 같은 Span이다.
		if len(h.auth.contexts) != 1 {
			t.Fatalf("Authenticate 호출 = %d", len(h.auth.contexts))
		}
		if got := trace.SpanContextFromContext(h.auth.contexts[0]); !got.Equal(span.SpanContext()) {
			t.Fatalf("handler context의 SpanContext = %+v, want server Span %+v", got, span.SpanContext())
		}
	})

	t.Run("unsampled는 sampled=1로 바뀌지 않음", func(t *testing.T) {
		h := newTracingHarness(t, true)
		rec := h.do(http.MethodGet, "/api/v1/me", "", withTraceHeaders(unsampledTraceparent, "vendor=opaque"))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		// SDK는 미샘플링 parent의 자식을 기록하지 않으므로 recorder에는 없다. Context 전파는 handler가 받은 context로 확인한다.
		if spans := h.recorder.Ended(); len(spans) != 0 {
			t.Fatalf("미샘플링 Trace의 Span이 기록됨: %d", len(spans))
		}
		got := trace.SpanContextFromContext(h.auth.contexts[0])
		if got.TraceID().String() != unsampledTraceID || got.IsSampled() {
			t.Fatalf("handler context = %+v, want trace %s unsampled", got, unsampledTraceID)
		}
		wire := tracecontext.FromContext(h.auth.contexts[0])
		if wire.TraceID() != unsampledTraceID || !strings.HasSuffix(wire.Traceparent, "-00") || wire.Tracestate != "vendor=opaque" {
			t.Fatalf("Connector로 직렬화될 Context = %+v", wire)
		}
	})
}

// 유효하지 않은 Trace metadata는 그 field만 버린다. 요청은 정상 처리하고 원문은 어디에도 남지 않는다.
func TestHTTPServerSpanDiscardsInvalidTraceContextWithoutFailingTheRequest(t *testing.T) {
	t.Run("traceparent가 유효하지 않음", func(t *testing.T) {
		h := newTracingHarness(t, true)
		rec := h.do(http.MethodGet, "/api/v1/me", "", withTraceHeaders(invalidTraceMarker, "vendor=opaque"))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		span := h.onlySpan(t)
		if span.Parent().IsValid() || !span.SpanContext().IsValid() {
			t.Fatalf("새 Trace를 시작해야 함: parent=%+v ctx=%+v", span.Parent(), span.SpanContext())
		}
		// traceparent가 없으면 tracestate도 쓰지 않는다.
		if got := span.SpanContext().TraceState().String(); got != "" {
			t.Fatalf("tracestate = %q, want empty", got)
		}
		mustNotContain(t, "Span", dumpSpan(span), invalidTraceMarker, "vendor=opaque")
		mustNotContain(t, "log", h.logs.String(), invalidTraceMarker)
	})

	t.Run("traceparent가 여러 개이면 유효하지 않음", func(t *testing.T) {
		h := newTracingHarness(t, true)
		rec := h.do(http.MethodGet, "/api/v1/me", "", func(r *http.Request) {
			r.Header.Add("Traceparent", sampledTraceparent)
			r.Header.Add("Traceparent", unsampledTraceparent)
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		span := h.onlySpan(t)
		if span.Parent().IsValid() || span.SpanContext().TraceID().String() == sampledTraceID || span.SpanContext().TraceID().String() == unsampledTraceID {
			t.Fatalf("중복 traceparent를 parent로 사용함: %+v", span.Parent())
		}
	})

	t.Run("tracestate만 유효하지 않으면 parent는 유지", func(t *testing.T) {
		h := newTracingHarness(t, true)
		rec := h.do(http.MethodGet, "/api/v1/me", "", withTraceHeaders(sampledTraceparent, invalidTraceMarker+"=!!"))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		span := h.onlySpan(t)
		if span.SpanContext().TraceID().String() != sampledTraceID || span.Parent().SpanID().String() != incomingParentSpanID {
			t.Fatalf("유효한 traceparent를 잃음: parent=%+v", span.Parent())
		}
		if got := span.SpanContext().TraceState().String(); got != "" {
			t.Fatalf("잘못된 tracestate가 남음: %q", got)
		}
		mustNotContain(t, "Span", dumpSpan(span), invalidTraceMarker)
		mustNotContain(t, "log", h.logs.String(), invalidTraceMarker)
	})
}

// Span 이름과 attribute는 route template뿐이다. Workspace File API의 query(path), 요청 경로의 ID, Cookie, body는 trace에 없다.
func TestHTTPSpansNeverRecordQueryPathCookieOrBody(t *testing.T) {
	const (
		pathMarker   = "src/PATH-SENTINEL-8a41/secret.py"
		sourceMarker = "WORKSPACE-SOURCE-SENTINEL-2c97"
		authMarker   = "Bearer AUTHORIZATION-SENTINEL-77d0"
		matchMarker  = "IF-MATCH-SENTINEL-19ab"
	)
	h := newTracingHarness(t, true)
	h.files.file.Path, h.files.file.Content = pathMarker, sourceMarker
	h.files.saved.Path = pathMarker
	byLab := "/api/v1/lab-instances/" + labInstanceID + "/files/"

	requests := []struct {
		method, path, body string
		wantName           string
	}{
		{http.MethodGet, byLab + "tree?path=" + pathMarker, "", "HTTP GET /api/v1/lab-instances/{labInstanceId}/files/tree"},
		{http.MethodGet, byLab + "content?path=" + pathMarker, "", "HTTP GET /api/v1/lab-instances/{labInstanceId}/files/content"},
		{http.MethodPut, byLab + "content?path=" + pathMarker, `{"content":"` + sourceMarker + `"}`, "HTTP PUT /api/v1/lab-instances/{labInstanceId}/files/content"},
	}
	for _, rq := range requests {
		h.do(rq.method, rq.path, rq.body,
			withHeader("Origin", trustedOrigin), withHeader("Content-Type", "application/json"),
			withHeader("Authorization", authMarker), withHeader("If-Match", `"`+matchMarker+`"`),
			withHeader("User-Agent", "UA-SENTINEL-5e02"))
	}
	// 요청이 실제 file handler까지 갔고 경로·본문이 use case로 전달되었다. 그래도 trace에는 없어야 한다는 것이 이 test의 요점이다.
	if len(h.files.treeIn) != 1 || len(h.files.readIn) != 1 || len(h.files.saveIn) != 1 || h.files.readIn[0].Path != pathMarker {
		t.Fatalf("file use case 호출 = tree %d, read %d, save %d (read path %q)", len(h.files.treeIn), len(h.files.readIn), len(h.files.saveIn),
			func() string {
				if len(h.files.readIn) == 0 {
					return ""
				}
				return h.files.readIn[0].Path
			}())
	}
	spans := h.recorder.Ended()
	if len(spans) != len(requests) {
		t.Fatalf("Span = %d, want %d", len(spans), len(requests))
	}
	allowed := map[string]bool{
		"http.request.method": true, "http.route": true, "http.response.status_code": true, "labbit.request_id": true,
	}
	for i, span := range spans {
		if span.Name() != requests[i].wantName {
			t.Fatalf("Span 이름 = %q, want route template %q", span.Name(), requests[i].wantName)
		}
		for _, kv := range span.Attributes() {
			if !allowed[string(kv.Key)] {
				t.Fatalf("허용 목록에 없는 attribute %s=%s", kv.Key, kv.Value.Emit())
			}
		}
		mustNotContain(t, "Span", dumpSpan(span),
			pathMarker, "PATH-SENTINEL", sourceMarker, authMarker, "AUTHORIZATION-SENTINEL", matchMarker, "UA-SENTINEL", fileCookie,
			labInstanceID, "?path", "url.", "user_agent")
	}
	mustNotContain(t, "log", h.logs.String(), pathMarker, sourceMarker, authMarker, matchMarker)
}

func TestHTTPSpanForUnmatchedRouteDoesNotUseTheRequestPath(t *testing.T) {
	h := newTracingHarness(t, true)
	rec := h.do(http.MethodGet, "/api/v1/NO-SUCH-PATH-SENTINEL-6f13/anything", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	span := h.onlySpan(t)
	if span.Name() != "HTTP GET unmatched" {
		t.Fatalf("Span 이름 = %q", span.Name())
	}
	if _, found := attrValue(span, "http.route"); found {
		t.Fatal("매칭되지 않은 요청에 http.route가 있음")
	}
	mustNotContain(t, "Span", dumpSpan(span), "NO-SUCH-PATH-SENTINEL")
}

// 5xx만 Span 오류다. 4xx(Origin 거절, 인증 실패)는 서버 오류가 아니다. request_id는 Span과 Problem의 requestId가 같다.
func TestHTTPSpanStatusFollowsServerErrorsOnly(t *testing.T) {
	t.Run("Origin 거절 403", func(t *testing.T) {
		h := newTracingHarness(t, true)
		rec := h.do(http.MethodPost, "/api/v1/auth/logout", "", withoutSource)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
		span := h.onlySpan(t)
		if span.Status().Code == codes.Error {
			t.Fatal("4xx가 Span 오류가 됨")
		}
		problem := decodeProblem(t, rec)
		if v, _ := attrValue(span, "labbit.request_id"); v.AsString() != problem.RequestID {
			t.Fatalf("Span request_id = %q, Problem requestId = %q", v.AsString(), problem.RequestID)
		}
	})

	t.Run("내부 오류 500", func(t *testing.T) {
		const secret = "INTERNAL-CAUSE-SENTINEL-93be"
		h := newTracingHarness(t, true)
		h.auth.authErr = errors.New("boom " + secret)
		rec := h.do(http.MethodGet, "/api/v1/me", "", withTraceHeaders(sampledTraceparent, ""))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		span := h.onlySpan(t)
		if span.Status().Code != codes.Error || span.Status().Description != "" {
			t.Fatalf("Span status = %+v, want Error with no description", span.Status())
		}
		mustNotContain(t, "Span", dumpSpan(span), secret)

		// 같은 요청의 request_id와 trace_id를 log에서 함께 조사할 수 있고, trace_id는 Span의 것과 같다.
		var record map[string]any
		for _, r := range logRecords(t, h.logs) {
			if r["message"] == "HTTP 요청 처리 실패" {
				record = r
			}
		}
		if record == nil {
			t.Fatalf("내부 오류 log가 없음:\n%s", h.logs)
		}
		problem := decodeProblem(t, rec)
		if record["trace_id"] != span.SpanContext().TraceID().String() || record["trace_id"] != sampledTraceID || record["request_id"] != problem.RequestID {
			t.Fatalf("log correlation = trace_id %v request_id %v, want trace %s request %s", record["trace_id"], record["request_id"], sampledTraceID, problem.RequestID)
		}
		mustNotContain(t, "log", h.logs.String(), secret, sampledTraceparent)
	})
}

// Tracer 없이도(noop) 유효한 Context는 하위 계층에 이어지고, 없던 Trace를 만들지 않으며 log에 가짜 trace_id를 남기지 않는다.
func TestHTTPWithoutTracerStillPropagatesIncomingContextAndInventsNothing(t *testing.T) {
	h := newTracingHarness(t, false)
	h.auth.authErr = errors.New("boom")

	h.do(http.MethodGet, "/api/v1/me", "")
	if got := trace.SpanContextFromContext(h.auth.contexts[0]); got.IsValid() {
		t.Fatalf("Trace를 받지 않았는데 Context가 생김: %+v", got)
	}
	if strings.Contains(h.logs.String(), `"trace_id"`) {
		t.Fatalf("Trace가 없는데 trace_id가 log에 남음:\n%s", h.logs)
	}

	h.do(http.MethodGet, "/api/v1/me", "", withTraceHeaders(sampledTraceparent, ""))
	got := tracecontext.FromContext(h.auth.contexts[1])
	if got.TraceID() != sampledTraceID {
		t.Fatalf("noop Tracer가 유효한 Context를 전달하지 않음: %+v", got)
	}
}
