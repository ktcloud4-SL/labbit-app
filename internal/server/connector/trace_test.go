package connector

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

const (
	traceID        = "4bf92f3577b34da6a3ce929d0e0e4736"
	spanID         = "00f067aa0ba902b7"
	sampledParent  = "00-" + traceID + "-" + spanID + "-01"
	unsampledPrnt  = "00-" + traceID + "-" + spanID + "-00"
	validTraceStat = "vendor=opaque,other=value"
)

func TestNormalizeTraceKeepsValidContext(t *testing.T) {
	tests := []struct {
		name                     string
		traceparent, tracestate  string
		wantParent, wantTracestt string
	}{
		{"sampled", sampledParent, "", sampledParent, ""},
		// 미샘플링 Context도 그대로 유지하며 sampled=1로 바꾸지 않는다.
		{"not sampled stays not sampled", unsampledPrnt, "", unsampledPrnt, ""},
		{"with tracestate", sampledParent, validTraceStat, sampledParent, validTraceStat},
		{"not sampled with tracestate", unsampledPrnt, validTraceStat, unsampledPrnt, validTraceStat},
		// 빈 list-member는 W3C가 허용하며 표준 parser가 버리고 정규화한다. 직접 만든 parser가 아니라 표준 parser를 쓰는 근거다.
		{"tracestate with empty member", sampledParent, "vendor=a,,other=b", sampledParent, "vendor=a,other=b"},
		// 미래 version은 표준 parser가 받아들이고 version 00 형식으로 다시 직렬화한다. trace/span id와 sampled는 그대로다.
		{"future version with extra data", "01-" + traceID + "-" + spanID + "-01-extra", "", sampledParent, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeTrace(tt.traceparent, tt.tracestate)
			if got.Traceparent != tt.wantParent || got.Tracestate != tt.wantTracestt || !got.Valid() {
				t.Fatalf("NormalizeTrace() = %+v, want {%q %q}", got, tt.wantParent, tt.wantTracestt)
			}
		})
	}
}

// 유효하지 않은 traceparent는 tracestate가 유효해도 함께 버린다. 업무 처리는 영향받지 않으므로 오류가 아니라 zero value다.
func TestNormalizeTraceDropsInvalidTraceparentAndItsTracestate(t *testing.T) {
	tests := map[string]string{
		"empty":                        "",
		"garbage":                      "not-a-traceparent",
		"too short":                    "00-" + traceID + "-" + spanID,
		"upper case hex":               "00-" + strings.ToUpper(traceID) + "-" + spanID + "-01",
		"all zero trace id":            "00-" + strings.Repeat("0", 32) + "-" + spanID + "-01",
		"all zero span id":             "00-" + traceID + "-" + strings.Repeat("0", 16) + "-01",
		"forbidden version ff":         "ff-" + traceID + "-" + spanID + "-01",
		"version 00 with extra fields": sampledParent + "-extra",
		"version 00 reserved flag bit": "00-" + traceID + "-" + spanID + "-ff",
		"non hex":                      "00-" + strings.Repeat("g", 32) + "-" + spanID + "-01",
		"longer than schema limit":     "00-" + traceID + "-" + spanID + "-01" + strings.Repeat("0", 512),
		"with whitespace":              " " + sampledParent,
	}
	for name, traceparent := range tests {
		t.Run(name, func(t *testing.T) {
			if got := NormalizeTrace(traceparent, validTraceStat); got != (TraceContext{}) || got.Valid() {
				t.Fatalf("NormalizeTrace(%q) = %+v, want zero value", name, got)
			}
		})
	}
}

// traceparent가 유효하고 tracestate만 유효하지 않으면 tracestate만 버린다. traceparent(와 sampled)는 유지한다.
func TestNormalizeTraceDropsOnlyInvalidTracestate(t *testing.T) {
	tests := map[string]string{
		"no equals":         "vendor",
		"upper case key":    "Vendor=x",
		"too many members":  strings.Join(manyMembers(33), ","),
		"duplicate key":     "vendor=a,vendor=b",
		"longer than limit": "vendor=" + strings.Repeat("a", maxTracestateLen),
	}
	for name, tracestate := range tests {
		t.Run(name, func(t *testing.T) {
			for _, parent := range []string{sampledParent, unsampledPrnt} {
				got := NormalizeTrace(parent, tracestate)
				if got.Traceparent != parent || got.Tracestate != "" {
					t.Fatalf("NormalizeTrace() = %+v, want traceparent 유지 + tracestate 폐기", got)
				}
			}
		})
	}
}

func manyMembers(n int) []string {
	members := make([]string, n)
	for i := range members {
		members[i] = "k" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + "=v"
	}
	return members
}

func TestTraceFromContext(t *testing.T) {
	if got := TraceFromContext(context.Background()); got != (TraceContext{}) {
		t.Fatalf("Context가 없을 때 = %+v, want zero value(가짜 Trace ID를 만들지 않는다)", got)
	}
	if got := TraceFromContext(trace.ContextWithSpanContext(context.Background(), trace.SpanContext{})); got != (TraceContext{}) {
		t.Fatalf("유효하지 않은 SpanContext = %+v, want zero value", got)
	}

	tid, _ := trace.TraceIDFromHex(traceID)
	sid, _ := trace.SpanIDFromHex(spanID)
	ts, err := trace.ParseTraceState(validTraceStat)
	if err != nil {
		t.Fatal(err)
	}
	for _, flags := range []trace.TraceFlags{trace.FlagsSampled, 0} {
		sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: flags, TraceState: ts})
		got := TraceFromContext(trace.ContextWithSpanContext(context.Background(), sc))
		want := "00-" + traceID + "-" + spanID + "-00"
		if flags == trace.FlagsSampled {
			want = sampledParent
		}
		if got.Traceparent != want || got.Tracestate != validTraceStat {
			t.Fatalf("flags %v: TraceFromContext() = %+v, want {%q %q}", flags, got, want, validTraceStat)
		}
	}
}
