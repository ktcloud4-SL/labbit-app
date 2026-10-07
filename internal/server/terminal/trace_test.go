package terminal

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
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

// 기다리는 Create(pending command)가 이미 사라진 late TERMINAL_OPEN_RESULT에서
// 유효한 inbound Trace Context가 전달되면 실패 보고 로그의 trace_id로 보존하고 결과 Span은 생성하지 않는다(NEW BLOCKER B 회귀 방지).
func TestHandleTerminalEventNoPendingWaiterPreservesValidInboundTraceInLogWithoutResultSpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tracer := tp.Tracer("test-terminal")

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

	s := &Service{
		logger: logger,
		tracer: tracer,
		opens:  make(map[string]*openWaiter),
	}

	const (
		validTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
		wantTraceID      = "4bf92f3577b34da6a3ce929d0e0e4736"
		requestID        = "req-late-123"
	)

	event := connector.TerminalOpenResultEvent{
		ConnectorID: uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		Correlation: connector.TerminalCorrelation{
			TerminalSessionID: "sess-late-result",
			LabInstanceID:     "00000000-0000-0000-0000-000000000002",
			Generation:        1,
		},
		RequestID: requestID,
		Payload: connector.TerminalOpenResultPayload{
			Outcome: connector.TerminalOutcomeFailed,
			Error:   &protocol.SafeError{Code: "START_FAILED"},
		},
		Trace: connector.TraceContext{Traceparent: validTraceparent},
	}

	s.HandleTerminalEvent(event)

	// 1. Result Span은 생성되지 않아야 한다 (기다리는 command가 없음).
	endedSpans := recorder.Ended()
	if len(endedSpans) != 0 {
		t.Fatalf("기다리는 command가 없는데 결과 Span이 생성됨: %d개", len(endedSpans))
	}

	// 2. 실패 보고 로그가 존재하고, 유효한 inbound TraceID를 correlation으로 유지해야 한다.
	out := logBuf.String()
	if !strings.Contains(out, "Connector TERMINAL_OPEN 실패 보고") {
		t.Fatalf("실패 보고 로그가 없음:\n%s", out)
	}
	if !strings.Contains(out, `"trace_id":"`+wantTraceID+`"`) {
		t.Fatalf("로그에 inbound trace_id(%s)가 누락됨:\n%s", wantTraceID, out)
	}
	if !strings.Contains(out, `"request_id":"`+requestID+`"`) {
		t.Fatalf("로그에 request_id가 누락됨:\n%s", out)
	}
	if strings.Contains(out, validTraceparent) {
		t.Fatalf("로그에 raw traceparent 원문이 누출됨:\n%s", out)
	}
}

// 기다리는 Create가 없고 유효하지 않은 inbound Trace가 온 경우, panic 없이 처리되고 trace_id가 남지 않으며 원문이 누출되지 않는다.
func TestHandleTerminalEventNoPendingWaiterWithInvalidTraceDoesNotPanicOrLeak(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tracer := tp.Tracer("test-terminal")

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

	s := &Service{
		logger: logger,
		tracer: tracer,
		opens:  make(map[string]*openWaiter),
	}

	const (
		invalidTraceparent = "00-INVALID-SENTINEL-TRACE-NOT-HEX-01"
		requestID          = "req-invalid-late-456"
	)

	event := connector.TerminalOpenResultEvent{
		ConnectorID: uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		Correlation: connector.TerminalCorrelation{
			TerminalSessionID: "sess-invalid-late",
			LabInstanceID:     "00000000-0000-0000-0000-000000000002",
			Generation:        1,
		},
		RequestID: requestID,
		Payload: connector.TerminalOpenResultPayload{
			Outcome: connector.TerminalOutcomeFailed,
			Error:   &protocol.SafeError{Code: "START_FAILED"},
		},
		Trace: connector.TraceContext{Traceparent: invalidTraceparent},
	}

	// panic 없이 안전하게 처리되어야 한다.
	s.HandleTerminalEvent(event)

	// 1. 결과 Span은 생성되지 않아야 한다.
	if ended := recorder.Ended(); len(ended) != 0 {
		t.Fatalf("결과 Span이 생성됨: %d개", len(ended))
	}

	// 2. 실패 보고 로그는 존재하되 trace_id가 없어야 하고 sentinel이 누출되지 않아야 한다.
	out := logBuf.String()
	if !strings.Contains(out, "Connector TERMINAL_OPEN 실패 보고") {
		t.Fatalf("실패 보고 로그가 없음:\n%s", out)
	}
	if strings.Contains(out, "trace_id") {
		t.Fatalf("유효하지 않은 inbound trace인데 log에 trace_id가 포함됨:\n%s", out)
	}
	if strings.Contains(out, invalidTraceparent) {
		t.Fatalf("로그에 invalid traceparent 원문이 누출됨:\n%s", out)
	}
}

// pending command가 존재할 때는 Connector가 foreign trace를 보내도 local SaaS command trace가 권위 있는 상관관계로 우선한다.
func TestHandleTerminalEventPendingWaiterPrioritizesCommandTraceOverInboundTrace(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tracer := tp.Tracer("test-terminal")

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

	s := &Service{
		logger: logger,
		tracer: tracer,
		opens:  make(map[string]*openWaiter),
	}

	cmdTID, _ := trace.TraceIDFromHex("11111111111111111111111111111111")
	cmdSID, _ := trace.SpanIDFromHex("2222222222222222")
	commandSC := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: cmdTID, SpanID: cmdSID, TraceFlags: trace.FlagsSampled,
	})

	const sessionID = "sess-pending-authority"
	waiter := &openWaiter{
		result:  make(chan openOutcome, 1),
		command: commandSC,
	}
	s.opens[sessionID] = waiter

	const (
		foreignTraceparent = "00-99999999999999999999999999999999-8888888888888888-01"
		foreignTraceID     = "99999999999999999999999999999999"
	)

	event := connector.TerminalOpenResultEvent{
		ConnectorID: uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		Correlation: connector.TerminalCorrelation{
			TerminalSessionID: sessionID,
			LabInstanceID:     "00000000-0000-0000-0000-000000000002",
			Generation:        1,
		},
		RequestID: "req-pending-1",
		Payload: connector.TerminalOpenResultPayload{
			Outcome: connector.TerminalOutcomeFailed,
			Error:   &protocol.SafeError{Code: "START_FAILED"},
		},
		Trace: connector.TraceContext{Traceparent: foreignTraceparent},
	}

	s.HandleTerminalEvent(event)

	// 1. Result Span이 생성되어야 하며, command trace를 따라야 한다.
	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("결과 Span 수 = %d, want 1", len(ended))
	}
	resultSpan := ended[0]
	if resultSpan.SpanContext().TraceID().String() != cmdTID.String() {
		t.Fatalf("결과 Span trace_id = %s, want command trace %s", resultSpan.SpanContext().TraceID().String(), cmdTID.String())
	}
	if resultSpan.Parent().SpanID().String() != cmdSID.String() {
		t.Fatalf("결과 Span parent span_id = %s, want %s", resultSpan.Parent().SpanID().String(), cmdSID.String())
	}

	// 2. 실패 보고 로그의 trace_id도 authoritative SaaS command trace여야 하고 foreign trace면 안 된다.
	out := logBuf.String()
	if !strings.Contains(out, `"trace_id":"`+cmdTID.String()+`"`) {
		t.Fatalf("로그의 trace_id가 SaaS command trace와 일치하지 않음:\n%s", out)
	}
	if strings.Contains(out, foreignTraceID) {
		t.Fatalf("로그의 trace_id가 Connector의 foreign trace로 교체됨:\n%s", out)
	}
}
