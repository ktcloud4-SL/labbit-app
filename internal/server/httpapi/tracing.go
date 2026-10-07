package httpapi

import (
	"context"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/ktcloud4-SL/labbit-app/internal/observability/spanattr"
	"github.com/ktcloud4-SL/labbit-app/internal/server/tracecontext"
)

// withTracing은 HTTP request마다 server Span을 만든다(Runtime Contract saas.observability.tracing minimum_boundaries의 HTTP request).
//
// OpenTelemetry HTTP 자동 계측(otelhttp)을 쓰지 않고 직접 만든다. 자동 계측은 url.path와 url.query를 기본으로 수집하는데, Workspace File API는
// query에 파일 경로(path)를 받고 Runtime Contract는 파일 경로·본문·목록을 trace에 남기지 않도록 정했다. 그래서 Span 이름과 attribute는
// 등록된 route template(예: /api/v1/lab-instances/{labInstanceId}/files/content)과 method, 응답 status, request ID만 쓴다.
// raw URL, query, header, Cookie, body, client 주소는 기록하지 않는다.
//
// tracer가 SDK Tracer가 아니어도(noop) 요청의 W3C Context는 handler로 이어진다.
func withTracing(tracer trace.Tracer, mux *http.ServeMux, routes map[string]string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := metricMethod(r.Method)
		route := routeTemplate(mux, routes, r)

		ctx := extractTraceContext(r.Context(), r.Header)
		attrs := []attribute.KeyValue{semconv.HTTPRequestMethodKey.String(semconvMethod(method))}
		if route != unmatchedRoute {
			attrs = append(attrs, semconv.HTTPRouteKey.String(route))
		}
		if id := requestIDFrom(ctx); id != "" {
			attrs = append(attrs, spanattr.AttrRequestID.String(id))
		}
		ctx, span := tracer.Start(ctx, "HTTP "+method+" "+route,
			trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(attrs...))

		response := &metricResponse{ResponseWriter: w}
		completed := false
		defer func() {
			status := response.status
			if status == 0 {
				status = http.StatusOK
				if !completed {
					status = http.StatusInternalServerError
				}
			}
			span.SetAttributes(semconv.HTTPResponseStatusCodeKey.Int(status))
			if status >= 500 {
				// 서버 Span은 5xx만 오류다. 설명에 오류 문자열을 넣지 않는다.
				span.SetStatus(codes.Error, "")
			}
			span.End()
		}()
		next.ServeHTTP(response, r.WithContext(ctx))
		completed = true
	})
}

// unmatchedRoute는 등록된 pattern과 맞지 않은 요청의 route label이다. Metric label과 같은 값이다.
const unmatchedRoute = "unmatched"

// routeTemplate은 요청이 맞는 등록 pattern의 route template을 반환한다. 요청 경로에서 만든 값을 쓰지 않는다.
// ServeMux가 요청에서 파생한 redirect pattern도 routes에 없으므로 unmatched다.
func routeTemplate(mux *http.ServeMux, routes map[string]string, r *http.Request) string {
	_, pattern := mux.Handler(r)
	if route := routes[pattern]; route != "" {
		return route
	}
	return unmatchedRoute
}

// semconvMethod는 알 수 없는 method를 semantic convention의 _OTHER로 바꾼다.
func semconvMethod(method string) string {
	if method == "unknown" {
		return "_OTHER"
	}
	return method
}

// extractTraceContext는 요청의 W3C traceparent/tracestate가 유효하면 원격 parent로 ctx에 담는다.
// Connector Control과 같은 정상화 규칙(tracecontext.Normalize)을 쓴다. 유효하지 않으면 그 metadata만 버리고 요청은 그대로 처리한다.
//
//   - traceparent가 없거나 유효하지 않으면 tracestate도 쓰지 않는다. 호출한 쪽은 새 Trace를 시작한다.
//   - traceparent가 여러 개이면 W3C 규칙대로 유효하지 않은 것으로 본다.
//   - tracestate만 유효하지 않으면 tracestate만 버리고 traceparent는 유지한다.
//   - sampled flag는 입력 그대로다. 잘못된 값 원문은 기록하지 않는다.
func extractTraceContext(ctx context.Context, header http.Header) context.Context {
	parents := header.Values("Traceparent")
	if len(parents) != 1 {
		return ctx
	}
	state := strings.Join(header.Values("Tracestate"), ",")
	return tracecontext.NewContext(ctx, tracecontext.Normalize(parents[0], state))
}

// traceLogAttrs는 ctx에 유효한 Span이 있으면 log에 붙일 trace_id를 반환한다. 없으면 nil이며 가짜 값을 만들지 않는다.
// raw traceparent/tracestate는 기록하지 않는다.
func traceLogAttrs(ctx context.Context) []any {
	if id := tracecontext.FromContext(ctx).TraceID(); id != "" {
		return []any{"trace_id", id}
	}
	return nil
}
