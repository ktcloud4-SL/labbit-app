//go:build integration

package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal/terminaltest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/tracecontext"
)

// LBT-144: 실제 PostgreSQL, 실제 HTTP API, 실제 Connector Control WSS(contract peer), Terminal Relay를 모두 거친 경로의 Trace다.
//
//	Browser HTTP request → HTTP server Span → TERMINAL_OPEN(client Span) → Connector peer → TERMINAL_OPEN_RESULT(수신 Span)
//
// 이 경로가 현재 repository에 실제로 있는 중앙 Trace 경계이며, Operation HTTP/Worker(LBT-17/LBT-18)는 아직 없으므로 여기서 검증하지 않는다.
// Connector peer는 실제 OpenStack/SSH/PTY가 없는 contract peer다.

const (
	serverSpanName = "HTTP POST /api/v1/lab-instances/{labInstanceId}/terminal-sessions"
	deleteSpanName = "HTTP DELETE /api/v1/terminal-sessions/{terminalSessionId}"
)

func newRecordingTracer(t *testing.T) (*tracetest.SpanRecorder, trace.Tracer) {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	return recorder, provider.Tracer("trace-integration-test")
}

func withTracer(tracer trace.Tracer) func(*stackOptions) {
	return func(o *stackOptions) { o.Tracer = tracer }
}

func withIncomingTrace(traceparent, tracestate string) func(*http.Request) {
	return func(r *http.Request) {
		if traceparent != "" {
			r.Header.Set("Traceparent", traceparent)
		}
		if tracestate != "" {
			r.Header.Set("Tracestate", tracestate)
		}
	}
}

func spansNamed(recorder *tracetest.SpanRecorder, name string) []sdktrace.ReadOnlySpan {
	var out []sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		if span.Name() == name {
			out = append(out, span)
		}
	}
	return out
}

func onlySpanNamed(t *testing.T, recorder *tracetest.SpanRecorder, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	spans := spansNamed(recorder, name)
	if len(spans) != 1 {
		t.Fatalf("Span %q = %d개, want 1 (전체: %v)", name, len(spans), spanNames(recorder))
	}
	return spans[0]
}

func spanNames(recorder *tracetest.SpanRecorder) []string {
	var names []string
	for _, span := range recorder.Ended() {
		names = append(names, span.Name())
	}
	return names
}

// spanForSession은 name인 Span 중 labbit.terminal_session_id가 sessionID인 하나다.
func spanForSession(t *testing.T, recorder *tracetest.SpanRecorder, name, sessionID string) sdktrace.ReadOnlySpan {
	t.Helper()
	var found []sdktrace.ReadOnlySpan
	for _, span := range spansNamed(recorder, name) {
		if attrString(span, "labbit.terminal_session_id") == sessionID {
			found = append(found, span)
		}
	}
	if len(found) != 1 {
		t.Fatalf("TerminalSession %s의 Span %q = %d개, want 1", sessionID, name, len(found))
	}
	return found[0]
}

func attrString(span sdktrace.ReadOnlySpan, key string) string {
	for _, kv := range span.Attributes() {
		if string(kv.Key) == key {
			return kv.Value.Emit()
		}
	}
	return ""
}

// dumpSpans는 기록된 모든 Span의 이름, attribute, event, status, resource를 하나의 문자열로 만든다.
func dumpSpans(spans []sdktrace.ReadOnlySpan) string {
	var b strings.Builder
	for _, span := range spans {
		fmt.Fprintf(&b, "span %s kind=%s status=%s/%q\n", span.Name(), span.SpanKind(), span.Status().Code, span.Status().Description)
		for _, kv := range span.Attributes() {
			fmt.Fprintf(&b, "  attr %s=%s\n", kv.Key, kv.Value.Emit())
		}
		for _, event := range span.Events() {
			fmt.Fprintf(&b, "  event %s\n", event.Name)
			for _, kv := range event.Attributes {
				fmt.Fprintf(&b, "  event-attr %s=%s\n", kv.Key, kv.Value.Emit())
			}
		}
		for _, link := range span.Links() {
			fmt.Fprintf(&b, "  link %s\n", link.SpanContext.TraceID())
		}
		for _, kv := range span.Resource().Attributes() {
			fmt.Fprintf(&b, "  resource %s=%s\n", kv.Key, kv.Value.Emit())
		}
	}
	return b.String()
}

func requireNoneOf(t *testing.T, what, haystack string, needles ...string) {
	t.Helper()
	for _, needle := range needles {
		if needle != "" && strings.Contains(haystack, needle) {
			t.Fatalf("%s에 %q가 있음", what, needle)
		}
	}
}

func wireTraceparent(traceID, spanID string, sampled bool) string {
	flags := "00"
	if sampled {
		flags = "01"
	}
	return "00-" + traceID + "-" + spanID + "-" + flags
}

func (e *terminalEnv) deleteSession(id string, mods ...func(*http.Request)) {
	e.t.Helper()
	resp := e.request(http.MethodDelete, "/api/v1/terminal-sessions/"+id, e.ownerCookie, "", mods...)
	if resp.Status != http.StatusNoContent {
		e.t.Fatalf("DELETE = %d, want 204: %s", resp.Status, resp.Body)
	}
}

// 하나의 Trace가 HTTP request → TERMINAL_OPEN → Connector peer → TERMINAL_OPEN_RESULT, 그리고 DELETE → TERMINAL_CLOSE로 이어진다.
// wire의 traceparent는 실제 current Span의 Context이고, log의 trace_id는 그 Span의 trace ID다.
func TestTerminalTraceConnectsHTTPToConnectorControlThroughTheRealStack(t *testing.T) {
	recorder, tracer := newRecordingTracer(t)
	e := newTerminalEnv(t, withTracer(tracer))
	f := e.fixture

	resp := e.request(http.MethodPost, e.createPath(f.LabInstanceID), e.ownerCookie, createBody, withIncomingTrace(traceSampled, traceStateOK))
	if resp.Status != http.StatusCreated {
		t.Fatalf("TerminalSession 생성 = %d, want 201: %s", resp.Status, resp.Body)
	}
	body := resp.json(t)
	sessionID, token := body["id"].(string), body["sessionToken"].(string)

	server := onlySpanNamed(t, recorder, serverSpanName)
	open := onlySpanNamed(t, recorder, "Connector TERMINAL_OPEN")
	result := onlySpanNamed(t, recorder, "Connector TERMINAL_OPEN_RESULT")

	// 같은 Trace이고 parent 관계가 HTTP → OPEN → RESULT다. incoming parent는 원격 parent다.
	for _, span := range []sdktrace.ReadOnlySpan{server, open, result} {
		if got := span.SpanContext().TraceID().String(); got != traceIDSampled {
			t.Fatalf("%s의 trace ID = %s, want incoming %s", span.Name(), got, traceIDSampled)
		}
	}
	if p := server.Parent(); p.SpanID().String() != "00f067aa0ba902b7" || !p.IsRemote() {
		t.Fatalf("server Span의 parent = %+v, want remote incoming parent", p)
	}
	if open.Parent().SpanID() != server.SpanContext().SpanID() || result.Parent().SpanID() != open.SpanContext().SpanID() {
		t.Fatalf("parent 관계가 HTTP → OPEN → RESULT가 아님: open.parent=%s result.parent=%s", open.Parent().SpanID(), result.Parent().SpanID())
	}
	if server.SpanKind() != trace.SpanKindServer || open.SpanKind() != trace.SpanKindClient || result.SpanKind() != trace.SpanKindConsumer {
		t.Fatalf("Span kind = %v/%v/%v", server.SpanKind(), open.SpanKind(), result.SpanKind())
	}
	for _, span := range []sdktrace.ReadOnlySpan{open, result} {
		if attrString(span, "labbit.terminal_session_id") != sessionID || attrString(span, "labbit.connector_id") != f.ConnectorID.String() ||
			attrString(span, "labbit.lab_instance_id") != f.LabInstanceID.String() {
			t.Fatalf("%s의 correlation attribute가 맞지 않음:\n%s", span.Name(), dumpSpans([]sdktrace.ReadOnlySpan{span}))
		}
	}
	if attrString(open, "labbit.connector.message_type") != "TERMINAL_OPEN" || attrString(open, "labbit.outcome") != "succeeded" ||
		attrString(result, "labbit.connector.message_type") != "TERMINAL_OPEN_RESULT" || attrString(result, "labbit.outcome") != "SUCCEEDED" {
		t.Fatalf("message type/outcome attribute:\n%s", dumpSpans([]sdktrace.ReadOnlySpan{open, result}))
	}

	// Connector peer가 받은 wire Context는 실제 OPEN client Span의 Context다. Connector는 그것을 RESULT까지 보존해 돌려주었고
	// SaaS는 그 사실을 result_trace로 기록한다. 결과 수신은 SaaS가 새로 만든 Span이다.
	opens := e.connector.Opens()
	if len(opens) != 1 {
		t.Fatalf("TERMINAL_OPEN = %d", len(opens))
	}
	if want := wireTraceparent(traceIDSampled, open.SpanContext().SpanID().String(), true); opens[0].Traceparent != want || opens[0].Tracestate != traceStateOK {
		t.Fatalf("wire Trace = {%q %q}, want {%q %q}", opens[0].Traceparent, opens[0].Tracestate, want, traceStateOK)
	}
	if opens[0].Traceparent == traceSampled {
		t.Fatal("incoming traceparent를 그대로 전달함(현재 Span의 Context여야 함)")
	}
	if got := attrString(result, "labbit.connector.result_trace"); got != "same_trace" {
		t.Fatalf("result_trace = %q, want same_trace", got)
	}
	if result.SpanContext().SpanID() == open.SpanContext().SpanID() {
		t.Fatal("결과 수신 Span이 command Span과 같은 Span ID")
	}

	// structured log의 trace_id는 Span의 trace ID와 같고 raw traceparent/tracestate는 log에 없다.
	for _, message := range []string{"TerminalSession 생성", "Connector TERMINAL_OPEN 전송"} {
		requireLogFields(t, e.logEvent(message, sessionID), map[string]any{"trace_id": server.SpanContext().TraceID().String()})
	}
	requireLogFields(t, e.logEvent("TerminalSession 생성", sessionID), map[string]any{"request_id": opens[0].RequestID})
	if got := attrString(server, "labbit.request_id"); got != opens[0].RequestID || got == "" {
		t.Fatalf("server Span의 request_id = %q, wire requestId = %q", got, opens[0].RequestID)
	}

	// Browser가 붙고 Terminal 내용이 오가도 Span은 늘지 않는다(binary frame마다, WSS connection마다 Span을 만들지 않는다).
	spansBeforeTerminalIO := len(recorder.Ended())
	s := created{ID: sessionID, Token: token, cookie: e.ownerCookie}
	b, _ := e.attach(s)
	b.write(websocket.BinaryMessage, []byte(terminalInputMarker))
	eventually(t, "PTY INPUT", 5*time.Second, func() bool { return string(e.connector.Inputs(sessionID)) == terminalInputMarker })
	e.connector.Output(sessionID, []byte(terminalOutputMarker))
	if got := b.binary(); string(got) != terminalOutputMarker {
		t.Fatalf("OUTPUT = %q", got)
	}
	if got := len(recorder.Ended()); got != spansBeforeTerminalIO {
		t.Fatalf("Terminal I/O 중 Span이 %d개 늘었음", got-spansBeforeTerminalIO)
	}

	// DELETE도 같은 Trace에 이어진다. TERMINAL_CLOSE는 그 요청의 server Span 아래 client Span이고 wire Context가 그 Span의 것이다.
	e.deleteSession(sessionID, withIncomingTrace(traceSampled, traceStateOK))
	eventually(t, "TERMINAL_CLOSE", 5*time.Second, func() bool { return len(e.connector.Closes()) == 1 })
	del := onlySpanNamed(t, recorder, deleteSpanName)
	closeSpan := onlySpanNamed(t, recorder, "Connector TERMINAL_CLOSE")
	if closeSpan.Parent().SpanID() != del.SpanContext().SpanID() || closeSpan.SpanContext().TraceID().String() != traceIDSampled || closeSpan.SpanKind() != trace.SpanKindClient {
		t.Fatalf("TERMINAL_CLOSE Span이 DELETE에 이어지지 않음: parent=%s", closeSpan.Parent().SpanID())
	}
	closeMsg := e.connector.Closes()[0]
	if want := wireTraceparent(traceIDSampled, closeSpan.SpanContext().SpanID().String(), true); closeMsg.Traceparent != want {
		t.Fatalf("TERMINAL_CLOSE wire traceparent = %q, want %q", closeMsg.Traceparent, want)
	}
	for _, message := range []string{"TerminalSession 종료", "Connector TERMINAL_CLOSE 전송"} {
		requireLogFields(t, e.logEvent(message, sessionID), map[string]any{"trace_id": traceIDSampled})
	}

	// 이 경로의 Span은 정확히 HTTP 2 + Connector command/result 3이다. Terminal 본문과 Secret은 어느 Span에도 없다.
	if got := len(recorder.Ended()); got != 5 {
		t.Fatalf("Span = %d개 %v, want 5", got, spanNames(recorder))
	}
	requireNoneOf(t, "Span", dumpSpans(recorder.Ended()),
		terminalInputMarker, terminalOutputMarker, token, e.ownerCookie, f.Servers["workspace"].ProviderID, "Cookie", "Authorization", traceStateOK, "password")
	requireNoneOf(t, "log", e.logs.String(), traceSampled, traceStateOK, terminalInputMarker, terminalOutputMarker, token)
}

// Trace가 없는 요청은 SaaS가 새 Trace를 시작한다. 그 Context가 wire로 나가고 log의 trace_id와 Span이 일치한다.
func TestTerminalCreateWithoutIncomingTraceStartsANewTraceThatReachesTheWire(t *testing.T) {
	recorder, tracer := newRecordingTracer(t)
	e := newTerminalEnv(t, withTracer(tracer))

	s := e.createSession(e.ownerCookie, e.fixture.LabInstanceID)
	server := onlySpanNamed(t, recorder, serverSpanName)
	open := onlySpanNamed(t, recorder, "Connector TERMINAL_OPEN")
	if server.Parent().IsValid() || !server.SpanContext().IsValid() {
		t.Fatalf("새 Trace의 root여야 함: parent=%+v", server.Parent())
	}
	traceID := server.SpanContext().TraceID().String()
	wire := tracecontext.Normalize(e.connector.Opens()[0].Traceparent, e.connector.Opens()[0].Tracestate)
	if want := wireTraceparent(traceID, open.SpanContext().SpanID().String(), true); wire.Traceparent != want {
		t.Fatalf("wire traceparent = %q, want %q", wire.Traceparent, want)
	}
	requireLogFields(t, e.logEvent("TerminalSession 생성", s.ID), map[string]any{"trace_id": traceID})
}

// 미샘플링 Context도 전파하며 sampled=1로 바꾸지 않는다. 기록하지 않는 Trace이므로 Span은 없다.
func TestUnsampledIncomingTraceIsPropagatedWithoutBeingForcedToSampled(t *testing.T) {
	recorder, tracer := newRecordingTracer(t)
	e := newTerminalEnv(t, withTracer(tracer))

	resp := e.request(http.MethodPost, e.createPath(e.fixture.LabInstanceID), e.ownerCookie, createBody, withIncomingTrace(traceUnsampled, traceStateOK))
	if resp.Status != http.StatusCreated {
		t.Fatalf("status = %d: %s", resp.Status, resp.Body)
	}
	wire := tracecontext.Normalize(e.connector.Opens()[0].Traceparent, e.connector.Opens()[0].Tracestate)
	if wire.TraceID() != "0af7651916cd43dd8448eb211c80319c" || !strings.HasSuffix(wire.Traceparent, "-00") || wire.Tracestate != traceStateOK {
		t.Fatalf("wire Context = %+v, want 같은 trace의 sampled=0", wire)
	}
	if len(recorder.Ended()) != 0 {
		t.Fatalf("미샘플링 Trace의 Span이 기록됨: %v", spanNames(recorder))
	}
	requireLogFields(t, e.logEvent("TerminalSession 생성", ""), map[string]any{"trace_id": "0af7651916cd43dd8448eb211c80319c"})
}

// 유효하지 않은 incoming Trace는 요청을 실패시키지 않고 새 Trace를 시작한다. 잘못된 원문은 wire, Span, log 어디에도 없다.
func TestInvalidIncomingTraceDoesNotFailTerminalCreateAndLeaksNowhere(t *testing.T) {
	recorder, tracer := newRecordingTracer(t)
	e := newTerminalEnv(t, withTracer(tracer))

	resp := e.request(http.MethodPost, e.createPath(e.fixture.LabInstanceID), e.ownerCookie, createBody, withIncomingTrace(invalidTraceMarker, traceStateOK))
	if resp.Status != http.StatusCreated {
		t.Fatalf("잘못된 Trace 때문에 요청이 실패함: %d %s", resp.Status, resp.Body)
	}
	server := onlySpanNamed(t, recorder, serverSpanName)
	if server.Parent().IsValid() {
		t.Fatalf("잘못된 traceparent를 parent로 사용함: %+v", server.Parent())
	}
	raw := e.connector.Opens()[0].Raw
	requireNoneOf(t, "TERMINAL_OPEN", raw, invalidTraceMarker, traceStateOK)
	requireNoneOf(t, "Span", dumpSpans(recorder.Ended()), invalidTraceMarker, traceStateOK)
	requireNoneOf(t, "log", e.logs.String(), invalidTraceMarker, traceStateOK)
}

// Connector가 Trace를 돌려주지 않거나 다른 Trace를 돌려줘도 업무 결과는 같다. 그 사실은 attribute로만 남고 다른 Trace에 연결하지 않는다.
func TestConnectorResultTraceMismatchNeverChangesTheBusinessResult(t *testing.T) {
	recorder, tracer := newRecordingTracer(t)
	e := newTerminalEnv(t, withTracer(tracer))

	tests := []struct {
		name string
		set  func()
		want string
	}{
		{"Trace를 돌려주지 않음", e.connector.OmitResultTrace, "absent"},
		{"다른 Trace를 돌려줌", func() { e.connector.ReplaceResultTrace(traceUnsampled) }, "different_trace"},
		{"잘못된 Trace를 돌려줌", func() { e.connector.ReplaceResultTrace(invalidTraceMarker) }, "absent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.set()
			s := e.createSession(e.ownerCookie, e.fixture.LabInstanceID) // 201이어야 한다.
			if row := e.row(s.ID); row.Status != "DETACHED" {
				t.Fatalf("TerminalSession 상태 = %s", row.Status)
			}
			open := spanForSession(t, recorder, "Connector TERMINAL_OPEN", s.ID)
			result := spanForSession(t, recorder, "Connector TERMINAL_OPEN_RESULT", s.ID)
			if got := attrString(result, "labbit.connector.result_trace"); got != tt.want {
				t.Fatalf("result_trace = %q, want %q", got, tt.want)
			}
			// 결과 수신 Span은 pending command에 연결된다. Connector가 돌려준 Context로 parent를 바꾸거나 다른 Trace에 붙지 않는다.
			if result.Parent().SpanID() != open.SpanContext().SpanID() || result.SpanContext().TraceID() != open.SpanContext().TraceID() {
				t.Fatalf("결과 Span이 command Span에 연결되지 않음: parent=%s", result.Parent().SpanID())
			}
			if len(result.Links()) != 0 || result.Status().Code == codes.Error {
				t.Fatalf("결과 Span: links=%d status=%v", len(result.Links()), result.Status())
			}
			requireNoneOf(t, "Span", dumpSpans(recorder.Ended()), invalidTraceMarker)
			requireNoneOf(t, "log", e.logs.String(), invalidTraceMarker)
		})
	}
}

// LBT-144 / Review blocker 2: Connector가 실패(FAILED)를 보고할 때도, Connector가 돌려준 Trace Context(echoed/omitted/foreign/invalid)와
// 무관하게 SaaS의 authoritative command trace가 result Span과 failure structured log 양쪽의 trace_id로 일관되게 유지된다.
func TestFailedConnectorResultMaintainsAuthoritativeCommandTraceInSpanAndLog(t *testing.T) {
	const (
		foreignTraceparent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
		foreignTraceID     = "0af7651916cd43dd8448eb211c80319c"
		invalidTraceVal    = "INVALID-RESULT-TRACE-SENTINEL-1a2b"
	)

	tests := []struct {
		name         string
		setupTrace   func(e *terminalEnv)
		wantRelation string
	}{
		{
			name:         "echoed Trace",
			setupTrace:   func(e *terminalEnv) { e.connector.EchoResultTrace() },
			wantRelation: "same_trace",
		},
		{
			name:         "omitted Trace",
			setupTrace:   func(e *terminalEnv) { e.connector.OmitResultTrace() },
			wantRelation: "absent",
		},
		{
			name:         "foreign valid Trace",
			setupTrace:   func(e *terminalEnv) { e.connector.ReplaceResultTrace(foreignTraceparent) },
			wantRelation: "different_trace",
		},
		{
			name:         "invalid Trace",
			setupTrace:   func(e *terminalEnv) { e.connector.ReplaceResultTrace(invalidTraceVal) },
			wantRelation: "absent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder, tracer := newRecordingTracer(t)
			e := newTerminalEnv(t, withTracer(tracer))
			e.connector.SetMode(terminaltest.OpenFails)
			tt.setupTrace(e)

			resp := e.request(http.MethodPost, e.createPath(e.fixture.LabInstanceID), e.ownerCookie, createBody, withIncomingTrace(traceSampled, ""))
			if resp.Status != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503: %s", resp.Status, resp.Body)
			}

			server := onlySpanNamed(t, recorder, serverSpanName)
			open := onlySpanNamed(t, recorder, "Connector TERMINAL_OPEN")
			result := onlySpanNamed(t, recorder, "Connector TERMINAL_OPEN_RESULT")

			cmdTraceID := open.SpanContext().TraceID().String()
			resultTraceID := result.SpanContext().TraceID().String()
			serverTraceID := server.SpanContext().TraceID().String()

			if cmdTraceID != traceIDSampled || resultTraceID != traceIDSampled || serverTraceID != traceIDSampled {
				t.Fatalf("Trace ID 불일치: server=%s, open=%s, result=%s, want %s", serverTraceID, cmdTraceID, resultTraceID, traceIDSampled)
			}
			if result.Parent().SpanID() != open.SpanContext().SpanID() {
				t.Fatalf("결과 Span의 parent(%s) != command Span ID(%s)", result.Parent().SpanID(), open.SpanContext().SpanID())
			}

			if got := attrString(result, "labbit.connector.result_trace"); got != tt.wantRelation {
				t.Fatalf("result_trace = %q, want %q", got, tt.wantRelation)
			}

			sessionID := attrString(open, "labbit.terminal_session_id")
			logEvent := e.logEvent("Connector TERMINAL_OPEN 실패 보고", sessionID)
			if logEvent == nil {
				t.Fatalf("Connector TERMINAL_OPEN 실패 보고 로그를 찾을 수 없음:\n%s", e.logs.String())
			}
			logTraceID, _ := logEvent["trace_id"].(string)
			if logTraceID == "" {
				t.Fatalf("실패 보고 로그에 trace_id가 누락됨 (SaaS authoritative trace가 유지되어야 함)")
			}
			if logTraceID != traceIDSampled {
				t.Fatalf("실패 보고 로그 trace_id = %q, want SaaS command/result trace %q", logTraceID, traceIDSampled)
			}
			if logTraceID == foreignTraceID {
				t.Fatalf("실패 보고 로그 trace_id가 Connector의 foreign trace로 교체됨: %s", foreignTraceID)
			}

			// invalid trace sentinel 원문이 log나 span에 누출되지 않음을 보증
			requireNoneOf(t, "Span", dumpSpans(recorder.Ended()), invalidTraceVal)
			requireNoneOf(t, "log", e.logs.String(), invalidTraceVal)
		})
	}
}

// Connector가 실패를 보고하면 결과 Span과 command Span이 모두 오류로 끝나고 Connector가 준 오류 문구가 아닌 고정 code만 남는다.
func TestFailedOpenResultMarksTheTraceAsErrorWithoutRawDetail(t *testing.T) {
	recorder, tracer := newRecordingTracer(t)
	e := newTerminalEnv(t, withTracer(tracer))
	e.connector.SetMode(terminaltest.OpenFails)

	resp := e.request(http.MethodPost, e.createPath(e.fixture.LabInstanceID), e.ownerCookie, createBody, withIncomingTrace(traceSampled, ""))
	if resp.Status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", resp.Status, resp.Body)
	}
	server := onlySpanNamed(t, recorder, serverSpanName)
	open := onlySpanNamed(t, recorder, "Connector TERMINAL_OPEN")
	result := onlySpanNamed(t, recorder, "Connector TERMINAL_OPEN_RESULT")
	if server.Status().Code != codes.Error || open.Status().Code != codes.Error || result.Status().Code != codes.Error {
		t.Fatalf("status = server %v open %v result %v, want all Error", server.Status(), open.Status(), result.Status())
	}
	if attrString(result, "labbit.outcome") != "FAILED" || attrString(open, "labbit.outcome") != "open_failed" {
		t.Fatalf("outcome:\n%s", dumpSpans([]sdktrace.ReadOnlySpan{open, result}))
	}
	// 생성 실패 정리로 TERMINAL_CLOSE가 나가며 같은 Trace에 이어진다.
	eventually(t, "TERMINAL_CLOSE", 5*time.Second, func() bool { return len(e.connector.Closes()) == 1 })
	closeSpan := onlySpanNamed(t, recorder, "Connector TERMINAL_CLOSE")
	if closeSpan.SpanContext().TraceID().String() != traceIDSampled {
		t.Fatalf("정리 TERMINAL_CLOSE가 다른 Trace임: %s", closeSpan.SpanContext().TraceID())
	}
	for _, span := range recorder.Ended() {
		if span.Status().Description != "" {
			t.Fatalf("Span status description이 있음: %q", span.Status().Description)
		}
	}
}
