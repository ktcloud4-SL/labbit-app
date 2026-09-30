// Package tracecontext는 W3C Trace Context(traceparent/tracestate) 전파 metadata의 정상화와 직렬화다.
//
// Connector Control(contracts/connector/README.md §9, D-25)과 Terminal JSON control(contracts/realtime/README.md)이 같은 규칙을 쓰므로
// 두 경계가 공유한다. 이 package는 Span을 만들지 않고 exporter나 SDK를 초기화하지 않는다. 표준 OpenTelemetry propagator로 유효성을
// 판정하고 직렬화할 뿐이며 PostgreSQL, repository, connector 같은 어떤 제품 package도 import하지 않는다(realtime package가 쓸 수 있어야 한다).
package tracecontext

import (
	"context"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// JSON Schema의 traceparent/tracestate 길이 상한이다. 이보다 긴 값은 관측 metadata로 쓰지 않는다.
const (
	MaxTraceparentLen = 512
	MaxTracestateLen  = 1024
)

// Context는 W3C Trace Context의 전파 metadata다. 표준 parser를 통과한 유효한 값만 담고, 유효하지 않으면 zero value다.
// Trace는 인증·tenant 판정·멱등성 key도, 업무 correlation key도 아니다. 없거나 유효하지 않아도 업무 처리는 실패하지 않는다.
type Context struct {
	// Traceparent가 비어 있으면 Trace Context가 없는 것이다. 이때 Tracestate도 항상 비어 있다.
	Traceparent string
	Tracestate  string
}

// Valid는 유효한 traceparent가 있는지 반환한다.
func (c Context) Valid() bool { return c.Traceparent != "" }

// TraceID는 유효한 Context의 trace-id(소문자 hex 32자)다. 없으면 빈 문자열이다. log에 남기는 용도이며 가짜 값을 만들지 않는다.
// Normalize가 만든 값은 항상 "00-<trace-id>-<span-id>-<flags>" 형식이다.
func (c Context) TraceID() string {
	const start, end = 3, 35
	if !c.Valid() || len(c.Traceparent) < end {
		return ""
	}
	return c.Traceparent[start:end]
}

// propagator는 W3C Trace Context의 표준 OpenTelemetry propagator다.
var propagator = propagation.TraceContext{}

// Normalize는 traceparent/tracestate 원문을 표준 propagator로 검사해 유효한 값만 남긴다.
//
//   - traceparent가 없거나 유효하지 않으면 tracestate도 함께 버린다(zero value).
//   - traceparent가 유효하고 tracestate만 유효하지 않으면 tracestate만 버린다.
//   - 유효한 값은 표준 형식으로 다시 직렬화한다. sampled flag는 입력 그대로이며 sampled=1로 바꾸지 않는다.
//
// 유효하지 않은 원문은 반환하지도 기록하지도 않는다. 가짜 Trace ID를 만들지 않는다.
func Normalize(traceparent, tracestate string) Context {
	if traceparent == "" || len(traceparent) > MaxTraceparentLen {
		return Context{}
	}
	carrier := propagation.MapCarrier{"traceparent": traceparent}
	if tracestate != "" && len(tracestate) <= MaxTracestateLen {
		carrier["tracestate"] = tracestate
	}
	return FromContext(propagator.Extract(context.Background(), carrier))
}

// FromContext는 ctx에 담긴 유효한 SpanContext를 Context로 직렬화한다. 유효한 SpanContext가 없으면 zero value다.
// Span을 새로 만들지 않으므로 caller가 이미 가진 Trace만 전달한다.
func FromContext(ctx context.Context) Context {
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return Context{}
	}
	out := propagation.MapCarrier{}
	propagator.Inject(ctx, out)
	return Context{Traceparent: out.Get("traceparent"), Tracestate: out.Get("tracestate")}
}

// NewContext는 c가 유효하면 그 Trace Context를 원격 parent로 담은 ctx를 반환한다. 유효하지 않으면 ctx를 그대로 반환한다.
// FromContext로 같은 값을 다시 꺼낼 수 있어 control event의 Context를 호출 경계(Control 등)로 넘기는 데 쓴다.
// Span을 새로 만들지 않는다.
func NewContext(ctx context.Context, c Context) context.Context {
	if !c.Valid() {
		return ctx
	}
	carrier := propagation.MapCarrier{"traceparent": c.Traceparent}
	if c.Tracestate != "" {
		carrier["tracestate"] = c.Tracestate
	}
	return propagator.Extract(ctx, carrier)
}
