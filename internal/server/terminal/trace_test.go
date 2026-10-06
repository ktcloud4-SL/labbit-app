package terminal

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
)

func TestResultTraceRelation(t *testing.T) {
	tid, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	otherTID, _ := trace.TraceIDFromHex("0af7651916cd43dd8448eb211c80319c")
	sid, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	otherSID, _ := trace.SpanIDFromHex("b7ad6b7169203331")
	ts, _ := trace.ParseTraceState("vendor=opaque")
	otherTS, _ := trace.ParseTraceState("other=different")

	commandSC := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled, TraceState: ts,
	})
	commandProp := connector.TraceFromContext(trace.ContextWithSpanContext(context.Background(), commandSC))

	tests := []struct {
		name    string
		command trace.SpanContext
		got     connector.TraceContext
		want    string
	}{
		{
			name:    "완전 일치 (trace ID, span ID, flags, tracestate)",
			command: commandSC,
			got:     commandProp,
			want:    "same_trace",
		},
		{
			name:    "TraceContext 누락 (zero value)",
			command: commandSC,
			got:     connector.TraceContext{},
			want:    "absent",
		},
		{
			name:    "유효하지 않은 traceparent",
			command: commandSC,
			got:     connector.TraceContext{Traceparent: "not-a-valid-traceparent"},
			want:    "absent",
		},
		{
			name:    "완전히 다른 trace ID",
			command: commandSC,
			got: connector.TraceFromContext(trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
				TraceID: otherTID, SpanID: sid, TraceFlags: trace.FlagsSampled, TraceState: ts,
			}))),
			want: "different_trace",
		},
		{
			name:    "동일 trace ID이지만 span ID가 변경됨 (hardening 검증)",
			command: commandSC,
			got: connector.TraceFromContext(trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
				TraceID: tid, SpanID: otherSID, TraceFlags: trace.FlagsSampled, TraceState: ts,
			}))),
			want: "different_trace",
		},
		{
			name:    "동일 trace ID이지만 flags(sampled)가 변경됨 (hardening 검증)",
			command: commandSC,
			got: connector.TraceFromContext(trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
				TraceID: tid, SpanID: sid, TraceFlags: 0, TraceState: ts,
			}))),
			want: "different_trace",
		},
		{
			name:    "동일 trace ID이지만 tracestate가 변경됨 (hardening 검증)",
			command: commandSC,
			got: connector.TraceFromContext(trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
				TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled, TraceState: otherTS,
			}))),
			want: "different_trace",
		},
		{
			name:    "command 자체가 invalid한 경우",
			command: trace.SpanContext{},
			got:     commandProp,
			want:    "different_trace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resultTraceRelation(tt.command, tt.got)
			if got != tt.want {
				t.Fatalf("resultTraceRelation() = %q, want %q", got, tt.want)
			}
		})
	}
}
