package connector

import (
	"context"

	"github.com/ktcloud4-SL/labbit-app/internal/server/tracecontext"
)

// W3C Trace Context의 정상화 규칙은 Connector Control과 Terminal JSON control이 공유하므로 tracecontext package가 소유한다.
// 이 file은 기존 호출부가 쓰는 이름을 그 package에 그대로 위임한다. 동작과 규칙은 tracecontext package의 것이다.

// connector.schema.json의 TraceParent/TraceState 길이 상한이다.
const (
	maxTraceparentLen = tracecontext.MaxTraceparentLen
	maxTracestateLen  = tracecontext.MaxTracestateLen
)

// TraceContext는 W3C Trace Context의 전파 metadata다(contracts/connector/README.md §9, D-25).
// 표준 parser를 통과한 유효한 값만 담고, 유효하지 않으면 zero value다.
type TraceContext = tracecontext.Context

// NormalizeTrace는 traceparent/tracestate 원문을 표준 propagator로 검사해 유효한 값만 남긴다. 규칙은 tracecontext.Normalize와 같다.
func NormalizeTrace(traceparent, tracestate string) TraceContext {
	return tracecontext.Normalize(traceparent, tracestate)
}

// TraceFromContext는 ctx에 담긴 유효한 SpanContext를 TraceContext로 직렬화한다. 유효한 SpanContext가 없으면 zero value다.
func TraceFromContext(ctx context.Context) TraceContext {
	return tracecontext.FromContext(ctx)
}
