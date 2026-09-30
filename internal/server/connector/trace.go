package connector

import (
	"context"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// connector.schema.json의 TraceParent/TraceState 길이 상한이다. 이보다 긴 값은 관측 metadata로 쓰지 않는다.
const (
	maxTraceparentLen = 512
	maxTracestateLen  = 1024
)

// TraceContext는 W3C Trace Context의 전파 metadata다(contracts/connector/README.md §9, D-25).
// 표준 parser를 통과한 유효한 값만 담고, 유효하지 않으면 zero value다. Trace는 인증·tenant 판정·멱등성 key도,
// 업무 correlation key도 아니다. 없거나 유효하지 않아도 command/result 처리는 실패하지 않는다.
type TraceContext struct {
	// Traceparent가 비어 있으면 Trace Context가 없는 것이다. 이때 Tracestate도 항상 비어 있다.
	Traceparent string
	Tracestate  string
}

// Valid는 유효한 traceparent가 있는지 반환한다.
func (t TraceContext) Valid() bool { return t.Traceparent != "" }

// traceContextPropagator는 W3C Trace Context의 표준 OpenTelemetry propagator다. 이 package는 Span을 만들지 않고
// exporter나 SDK를 초기화하지 않는다. 유효성 판정과 직렬화에만 사용한다.
var traceContextPropagator = propagation.TraceContext{}

// NormalizeTrace는 traceparent/tracestate 원문을 표준 propagator로 검사해 유효한 값만 남긴다.
//
//   - traceparent가 없거나 유효하지 않으면 tracestate도 함께 버린다(zero value).
//   - traceparent가 유효하고 tracestate만 유효하지 않으면 tracestate만 버린다.
//   - 유효한 값은 표준 형식으로 다시 직렬화한다. sampled flag는 입력 그대로이며 sampled=1로 바꾸지 않는다.
//
// 유효하지 않은 원문은 반환하지도 기록하지도 않는다. 가짜 Trace ID를 만들지 않는다.
func NormalizeTrace(traceparent, tracestate string) TraceContext {
	if traceparent == "" || len(traceparent) > maxTraceparentLen {
		return TraceContext{}
	}
	carrier := propagation.MapCarrier{"traceparent": traceparent}
	if tracestate != "" && len(tracestate) <= maxTracestateLen {
		carrier["tracestate"] = tracestate
	}
	ctx := traceContextPropagator.Extract(context.Background(), carrier)
	return TraceFromContext(ctx)
}

// TraceFromContext는 ctx에 담긴 유효한 SpanContext를 TraceContext로 직렬화한다. 유효한 SpanContext가 없으면 zero value다.
// Span을 새로 만들지 않으므로 caller가 이미 가진 Trace만 전달한다.
func TraceFromContext(ctx context.Context) TraceContext {
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return TraceContext{}
	}
	out := propagation.MapCarrier{}
	traceContextPropagator.Inject(ctx, out)
	return TraceContext{Traceparent: out.Get("traceparent"), Tracestate: out.Get("tracestate")}
}
