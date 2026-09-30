package tracecontext

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

const (
	traceID        = "4bf92f3577b34da6a3ce929d0e0e4736"
	spanID         = "00f067aa0ba902b7"
	sampled        = "00-" + traceID + "-" + spanID + "-01"
	unsampled      = "00-" + traceID + "-" + spanID + "-00"
	validTracestat = "vendor=opaque,other=value"
)

func TestNormalizeKeepsValidContext(t *testing.T) {
	tests := []struct {
		name                    string
		traceparent, tracestate string
		wantParent, wantState   string
	}{
		{"sampled", sampled, "", sampled, ""},
		// 미샘플링 Context도 그대로 유지하며 sampled=1로 바꾸지 않는다.
		{"not sampled stays not sampled", unsampled, "", unsampled, ""},
		{"with tracestate", sampled, validTracestat, sampled, validTracestat},
		{"not sampled with tracestate", unsampled, validTracestat, unsampled, validTracestat},
		// 빈 list-member는 W3C가 허용하며 표준 parser가 버리고 정규화한다. 직접 만든 parser가 아니라 표준 parser를 쓰는 근거다.
		{"tracestate with empty member", sampled, "vendor=a,,other=b", sampled, "vendor=a,other=b"},
		// 미래 version은 표준 parser가 받아들이고 version 00 형식으로 다시 직렬화한다. trace/span id와 sampled는 그대로다.
		{"future version with extra data", "01-" + traceID + "-" + spanID + "-01-extra", "", sampled, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Normalize(tt.traceparent, tt.tracestate)
			if got.Traceparent != tt.wantParent || got.Tracestate != tt.wantState || !got.Valid() {
				t.Fatalf("Normalize() = %+v, want {%q %q}", got, tt.wantParent, tt.wantState)
			}
		})
	}
}

// 유효하지 않은 traceparent는 tracestate가 유효해도 함께 버린다. 업무 처리는 영향받지 않으므로 오류가 아니라 zero value다.
func TestNormalizeDropsInvalidTraceparentAndItsTracestate(t *testing.T) {
	tests := map[string]string{
		"empty":                        "",
		"garbage":                      "not-a-traceparent",
		"too short":                    "00-" + traceID + "-" + spanID,
		"upper case hex":               "00-" + strings.ToUpper(traceID) + "-" + spanID + "-01",
		"all zero trace id":            "00-" + strings.Repeat("0", 32) + "-" + spanID + "-01",
		"all zero span id":             "00-" + traceID + "-" + strings.Repeat("0", 16) + "-01",
		"forbidden version ff":         "ff-" + traceID + "-" + spanID + "-01",
		"version 00 with extra fields": sampled + "-extra",
		"version 00 reserved flag bit": "00-" + traceID + "-" + spanID + "-ff",
		"non hex":                      "00-" + strings.Repeat("g", 32) + "-" + spanID + "-01",
		"longer than schema limit":     sampled + strings.Repeat("0", MaxTraceparentLen),
		"with whitespace":              " " + sampled,
	}
	for name, traceparent := range tests {
		t.Run(name, func(t *testing.T) {
			if got := Normalize(traceparent, validTracestat); got != (Context{}) || got.Valid() {
				t.Fatalf("Normalize(%q) = %+v, want zero value", name, got)
			}
		})
	}
}

// traceparent가 유효하고 tracestate만 유효하지 않으면 tracestate만 버린다. traceparent(와 sampled)는 유지한다.
func TestNormalizeDropsOnlyInvalidTracestate(t *testing.T) {
	members := make([]string, 33)
	for i := range members {
		members[i] = "k" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + "=v"
	}
	tests := map[string]string{
		"no equals":         "vendor",
		"upper case key":    "Vendor=x",
		"too many members":  strings.Join(members, ","),
		"duplicate key":     "vendor=a,vendor=b",
		"longer than limit": "vendor=" + strings.Repeat("a", MaxTracestateLen),
	}
	for name, tracestate := range tests {
		t.Run(name, func(t *testing.T) {
			for _, parent := range []string{sampled, unsampled} {
				got := Normalize(parent, tracestate)
				if got.Traceparent != parent || got.Tracestate != "" {
					t.Fatalf("Normalize() = %+v, want traceparent 유지 + tracestate 폐기", got)
				}
			}
		})
	}
}

func TestFromContext(t *testing.T) {
	if got := FromContext(context.Background()); got != (Context{}) {
		t.Fatalf("Context가 없을 때 = %+v, want zero value(가짜 Trace ID를 만들지 않는다)", got)
	}
	if got := FromContext(trace.ContextWithSpanContext(context.Background(), trace.SpanContext{})); got != (Context{}) {
		t.Fatalf("유효하지 않은 SpanContext = %+v, want zero value", got)
	}

	tid, _ := trace.TraceIDFromHex(traceID)
	sid, _ := trace.SpanIDFromHex(spanID)
	ts, err := trace.ParseTraceState(validTracestat)
	if err != nil {
		t.Fatal(err)
	}
	for _, flags := range []trace.TraceFlags{trace.FlagsSampled, 0} {
		sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: flags, TraceState: ts})
		got := FromContext(trace.ContextWithSpanContext(context.Background(), sc))
		want := unsampled
		if flags == trace.FlagsSampled {
			want = sampled
		}
		if got.Traceparent != want || got.Tracestate != validTracestat {
			t.Fatalf("flags %v: FromContext() = %+v, want {%q %q}", flags, got, want, validTracestat)
		}
	}
}

// NewContext로 담은 Context를 FromContext로 그대로 꺼낼 수 있다. 미샘플링도 sampled로 바뀌지 않는다.
func TestNewContextRoundTrips(t *testing.T) {
	for _, want := range []Context{
		{Traceparent: sampled},
		{Traceparent: unsampled},
		{Traceparent: sampled, Tracestate: validTracestat},
		{Traceparent: unsampled, Tracestate: validTracestat},
	} {
		got := FromContext(NewContext(context.Background(), want))
		if got != want {
			t.Fatalf("round trip = %+v, want %+v", got, want)
		}
	}
}

// 유효하지 않은(zero) Context는 ctx를 바꾸지 않는다. 기존에 ctx에 있던 유효한 Trace를 지우지도 않는다.
func TestNewContextWithZeroValueKeepsExistingContext(t *testing.T) {
	ctx := NewContext(context.Background(), Context{Traceparent: sampled})
	if got := NewContext(ctx, Context{}); FromContext(got).Traceparent != sampled {
		t.Fatalf("zero Context가 기존 Trace를 바꿈: %+v", FromContext(got))
	}
	if got := NewContext(context.Background(), Context{}); FromContext(got) != (Context{}) {
		t.Fatalf("zero Context로 가짜 Trace가 생김: %+v", FromContext(got))
	}
}

func TestTraceID(t *testing.T) {
	if got := Normalize(sampled, validTracestat).TraceID(); got != traceID {
		t.Fatalf("TraceID() = %q, want %q", got, traceID)
	}
	if got := Normalize(unsampled, "").TraceID(); got != traceID {
		t.Fatalf("미샘플링 TraceID() = %q, want %q", got, traceID)
	}
	for _, c := range []Context{{}, Normalize("garbage", validTracestat)} {
		if got := c.TraceID(); got != "" {
			t.Fatalf("TraceID(%+v) = %q, want 빈 문자열(가짜 값을 만들지 않는다)", c, got)
		}
	}
}
