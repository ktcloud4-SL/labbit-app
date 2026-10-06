package connector

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/observability"
)

// terminalRecorder는 Router가 넘긴 TerminalSession event를 순서대로 기록한다.
type terminalRecorder struct {
	mu     sync.Mutex
	events []TerminalEvent
}

func (r *terminalRecorder) HandleTerminalEvent(e TerminalEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *terminalRecorder) all() []TerminalEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]TerminalEvent(nil), r.events...)
}

func (r *terminalRecorder) only(t *testing.T) TerminalEvent {
	t.Helper()
	events := r.all()
	if len(events) != 1 {
		t.Fatalf("event %d개 = %+v, want 1개", len(events), events)
	}
	return events[0]
}

type terminalFixture struct {
	t        *testing.T
	registry *Registry
	sink     *terminalRecorder
	router   *Router
	logs     *syncLog
}

func newTerminalFixture(t *testing.T) *terminalFixture {
	t.Helper()
	registry := NewRegistry()
	sink := &terminalRecorder{}
	logs := &syncLog{}
	router, err := NewRouter(RouterOptions{
		Registry:     registry,
		TerminalSink: sink,
		Logger:       slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &terminalFixture{t: t, registry: registry, sink: sink, router: router, logs: logs}
}

func (f *terminalFixture) connect(principal Principal) (*Registration, *frameLog) {
	f.t.Helper()
	frames := &frameLog{}
	registration := f.registry.Register(principal, nil)
	if !registration.MarkReady(frames.route) {
		f.t.Fatal("MarkReady가 거절됨")
	}
	return registration, frames
}

func newPrincipal() Principal {
	return Principal{ConnectorID: uuid.New(), OrganizationID: uuid.New(), CredentialID: uuid.New()}
}

func terminalCorr(session string) TerminalCorrelation {
	return TerminalCorrelation{TerminalSessionID: session, LabInstanceID: "lab-1", Generation: 2}
}

func openFor(connectorID uuid.UUID, session string) TerminalOpen {
	return TerminalOpen{
		ConnectorID: connectorID, RequestID: "request-1", Correlation: terminalCorr(session),
		TargetVMKey: "workspace", ProviderServerID: "srv-1", Cols: json.RawMessage("120"), Rows: json.RawMessage("40"),
	}
}

func decodeFrame(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatalf("frame이 JSON이 아님: %v (%s)", err, data)
	}
	return obj
}

func inboundFor(replyTo string, c TerminalCorrelation) TerminalInbound {
	return TerminalInbound{MessageID: "inbound-1", ReplyToMessageID: replyTo, Correlation: c}
}

// TERMINAL_OPEN은 terminal-control.schema.json의 Envelope와 payload(resolved target만)를 그대로 싣는다.
func TestSendTerminalOpenWritesContractMessage(t *testing.T) {
	f := newTerminalFixture(t)
	p := newPrincipal()
	_, frames := f.connect(p)

	open := openFor(p.ConnectorID, "session-1")
	open.Cols, open.Rows = json.RawMessage("1e2"), json.RawMessage("1.0") // 값을 바꾸지 않고 전달한다.
	sent, err := f.router.SendTerminalOpen(context.Background(), open)
	if err != nil {
		t.Fatalf("SendTerminalOpen() error = %v", err)
	}
	if frames.count() != 1 {
		t.Fatalf("frame = %d, want 1", frames.count())
	}
	raw := frames.last(t)
	msg := decodeFrame(t, raw)
	payload, _ := msg["payload"].(map[string]any)
	if msg["type"] != "TERMINAL_OPEN" || msg["messageId"] != sent.MessageID || msg["requestId"] != "request-1" ||
		msg["terminalSessionId"] != "session-1" || msg["labInstanceId"] != "lab-1" || msg["generation"] != float64(2) {
		t.Fatalf("envelope = %v", msg)
	}
	if _, err := uuid.Parse(sent.MessageID); err != nil {
		t.Fatalf("messageId = %q", sent.MessageID)
	}
	if payload["targetVmKey"] != "workspace" || payload["providerServerId"] != "srv-1" || len(payload) != 4 {
		t.Fatalf("payload = %v, want only the resolved target and size", payload)
	}
	if !strings.Contains(string(raw), `"cols":1e2`) || !strings.Contains(string(raw), `"rows":1.0`) {
		t.Fatalf("cols/rows 원문이 바뀜: %s", raw)
	}
	// Browser Cookie, Password, attach token 같은 값이 message 어디에도 없다.
	for _, forbidden := range []string{"cookie", "token", "password", "sessionToken"} {
		if strings.Contains(strings.ToLower(string(raw)), strings.ToLower(forbidden)) {
			t.Fatalf("message가 %q를 포함함: %s", forbidden, raw)
		}
	}
	if got := f.router.PendingTerminalOpens(p.ConnectorID); got != 1 {
		t.Fatalf("PendingTerminalOpens = %d, want 1", got)
	}
}

func TestSendTerminalOpenPropagatesOnlyValidTrace(t *testing.T) {
	f := newTerminalFixture(t)
	p := newPrincipal()
	_, frames := f.connect(p)

	const traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	open := openFor(p.ConnectorID, "s-valid")
	open.Trace = TraceContext{Traceparent: traceparent, Tracestate: "vendor=value"}
	if _, err := f.router.SendTerminalOpen(context.Background(), open); err != nil {
		t.Fatal(err)
	}
	if msg := decodeFrame(t, frames.last(t)); msg["traceparent"] != traceparent || msg["tracestate"] != "vendor=value" {
		t.Fatalf("Trace Context = %v", msg)
	}

	// 유효하지 않은 Trace는 버린다. 그것 때문에 전송을 실패시키지 않는다.
	open = openFor(p.ConnectorID, "s-invalid")
	open.Trace = TraceContext{Traceparent: "not-a-traceparent", Tracestate: "x=y"}
	if _, err := f.router.SendTerminalOpen(context.Background(), open); err != nil {
		t.Fatalf("유효하지 않은 Trace가 전송을 실패시킴: %v", err)
	}
	if msg := decodeFrame(t, frames.last(t)); msg["traceparent"] != nil || msg["tracestate"] != nil {
		t.Fatalf("유효하지 않은 Trace가 전달됨: %v", msg)
	}
	// 같은 connection의 다음 command가 앞선 Trace/request ID를 재사용하지 않는다.
	open = openFor(p.ConnectorID, "s-none")
	open.RequestID = ""
	if _, err := f.router.SendTerminalOpen(context.Background(), open); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(f.logs.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("log event count = %d, want 3", len(lines))
	}
	for i, line := range lines {
		event := decodeFrame(t, []byte(line))
		if event["connector_id"] != p.ConnectorID.String() || event["lab_instance_id"] != "lab-1" {
			t.Fatal("Terminal command log lacks known correlation")
		}
		if i == 0 {
			if event["trace_id"] != "4bf92f3577b34da6a3ce929d0e0e4736" || event["request_id"] != "request-1" {
				t.Fatal("Terminal command log lacks valid Trace/request ID")
			}
		} else if _, present := event["trace_id"]; present {
			t.Fatal("Terminal command log invented or reused a trace_id")
		}
		if i == 2 {
			if _, present := event["request_id"]; present {
				t.Fatal("Terminal command log invented a request_id")
			}
		}
	}
	for _, raw := range []string{traceparent, "vendor=value", "not-a-traceparent", "x=y", "srv-1"} {
		if strings.Contains(f.logs.String(), raw) {
			t.Fatal("Terminal command log contains raw Trace or payload")
		}
	}
}

func TestSendTerminalOpenRejectsInvalidCommands(t *testing.T) {
	tests := []struct {
		name string
		mod  func(*TerminalOpen)
	}{
		{"terminalSessionId", func(o *TerminalOpen) { o.Correlation.TerminalSessionID = "" }},
		{"labInstanceId", func(o *TerminalOpen) { o.Correlation.LabInstanceID = "" }},
		{"generation zero", func(o *TerminalOpen) { o.Correlation.Generation = 0 }},
		{"generation negative", func(o *TerminalOpen) { o.Correlation.Generation = -1 }},
		{"targetVmKey", func(o *TerminalOpen) { o.TargetVMKey = "" }},
		{"providerServerId", func(o *TerminalOpen) { o.ProviderServerID = "" }},
		{"cols missing", func(o *TerminalOpen) { o.Cols = nil }},
		{"rows invalid json", func(o *TerminalOpen) { o.Rows = json.RawMessage("{") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newTerminalFixture(t)
			p := newPrincipal()
			_, frames := f.connect(p)
			open := openFor(p.ConnectorID, "session-1")
			tt.mod(&open)

			_, err := f.router.SendTerminalOpen(context.Background(), open)
			if !errors.Is(err, ErrInvalidCommand) {
				t.Fatalf("error = %v, want ErrInvalidCommand", err)
			}
			if frames.count() != 0 || f.router.PendingTerminalOpens(p.ConnectorID) != 0 {
				t.Fatalf("잘못된 command가 전송되거나 pending을 만듦: frames %d", frames.count())
			}
		})
	}
}

// Connector가 연결되어 있지 않거나 protocol-ready가 아니면 아무것도 쓰지 않고 pending도 만들지 않는다.
func TestSendTerminalOpenWhenConnectorUnavailable(t *testing.T) {
	f := newTerminalFixture(t)
	p := newPrincipal()

	_, err := f.router.SendTerminalOpen(context.Background(), openFor(p.ConnectorID, "s"))
	if !errors.Is(err, ErrNotConnected) || !errors.Is(err, ErrConnectorUnavailable) {
		t.Fatalf("미연결 error = %v, want ErrNotConnected", err)
	}

	// HELLO_ACK 전(소유만 하고 ready가 아님)
	f.registry.Register(p, nil)
	_, err = f.router.SendTerminalOpen(context.Background(), openFor(p.ConnectorID, "s"))
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("ready 전 error = %v, want ErrNotReady", err)
	}
	if f.router.PendingTerminalOpens(p.ConnectorID) != 0 {
		t.Fatal("전송하지 않은 OPEN의 pending이 남음")
	}

	// 취소된 ctx는 전송을 시작하지 않는다.
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.router.SendTerminalOpen(canceled, openFor(p.ConnectorID, "s")); !errors.Is(err, context.Canceled) {
		t.Fatalf("취소된 ctx error = %v", err)
	}
}

func TestSendTerminalOpenWriteFailureRemovesPending(t *testing.T) {
	f := newTerminalFixture(t)
	p := newPrincipal()

	for name, tt := range map[string]struct {
		routeErr error
		want     error
	}{
		"closing connection": {routeErr: ErrRouteClosed, want: ErrConnectionClosing},
		"write failed":       {routeErr: errors.New("broken pipe"), want: ErrSendFailed},
	} {
		registration := f.registry.Register(p, nil)
		registration.MarkReady(func([]byte) error { return tt.routeErr })

		_, err := f.router.SendTerminalOpen(context.Background(), openFor(p.ConnectorID, "session-"+name))
		if !errors.Is(err, tt.want) {
			t.Fatalf("%s: error = %v, want %v", name, err, tt.want)
		}
		if got := f.router.PendingTerminalOpens(p.ConnectorID); got != 0 {
			t.Fatalf("%s: pending = %d, want 0", name, got)
		}
	}
}

// 같은 Connector의 같은 TerminalSession에 OPEN이 이미 진행 중이면 중복으로 거절하고 새 message를 쓰지 않는다.
func TestSendTerminalOpenDuplicateCorrelation(t *testing.T) {
	f := newTerminalFixture(t)
	p := newPrincipal()
	_, frames := f.connect(p)

	if _, err := f.router.SendTerminalOpen(context.Background(), openFor(p.ConnectorID, "s1")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.router.SendTerminalOpen(context.Background(), openFor(p.ConnectorID, "s1")); !errors.Is(err, ErrDuplicateCorrelation) {
		t.Fatalf("중복 error = %v, want ErrDuplicateCorrelation", err)
	}
	if frames.count() != 1 {
		t.Fatalf("frame = %d, want 1", frames.count())
	}
}

// OPEN_RESULT는 인증된 ConnectorID와 terminalSessionId, labInstanceId, generation, replyToMessageId가 모두 맞는 pending에만 연결한다.
func TestRouteTerminalOpenResultRequiresExactCorrelation(t *testing.T) {
	f := newTerminalFixture(t)
	p := newPrincipal()
	other := newPrincipal()
	f.connect(p)
	f.connect(other)

	sentA, err := f.router.SendTerminalOpen(context.Background(), openFor(p.ConnectorID, "session-A"))
	if err != nil {
		t.Fatal(err)
	}
	sentB, err := f.router.SendTerminalOpen(context.Background(), openFor(p.ConnectorID, "session-B"))
	if err != nil {
		t.Fatal(err)
	}
	result := TerminalOpenResultPayload{Outcome: TerminalOutcomeSucceeded}

	tests := []struct {
		name       string
		connector  uuid.UUID
		in         TerminalInbound
		wantReason UnmatchedReason
	}{
		{"another connector claims the same session", other.ConnectorID, inboundFor(sentA.MessageID, terminalCorr("session-A")), ReasonNoPending},
		{"unknown terminal session", p.ConnectorID, inboundFor(sentA.MessageID, terminalCorr("session-X")), ReasonNoPending},
		{"reply to another session's message", p.ConnectorID, inboundFor(sentB.MessageID, terminalCorr("session-A")), ReasonReplyMismatch},
		{"reply to an unknown message", p.ConnectorID, inboundFor("no-such-message", terminalCorr("session-A")), ReasonReplyMismatch},
		{"reply missing", p.ConnectorID, inboundFor("", terminalCorr("session-A")), ReasonReplyMismatch},
		{"wrong lab instance", p.ConnectorID, inboundFor(sentA.MessageID, TerminalCorrelation{TerminalSessionID: "session-A", LabInstanceID: "lab-other", Generation: 2}), ReasonCorrelationMismatch},
		{"wrong generation", p.ConnectorID, inboundFor(sentA.MessageID, TerminalCorrelation{TerminalSessionID: "session-A", LabInstanceID: "lab-1", Generation: 3}), ReasonCorrelationMismatch},
		{"unrepresentable generation", p.ConnectorID, inboundFor(sentA.MessageID, TerminalCorrelation{TerminalSessionID: "session-A", LabInstanceID: "lab-1", Generation: 0}), ReasonUnrepresentable},
	}
	for _, tt := range tests {
		if f.router.RouteTerminalOpenResult(tt.connector, tt.in, result) {
			t.Fatalf("%s: 잘못된 correlation이 연결됨", tt.name)
		}
		event, ok := f.sink.all()[len(f.sink.all())-1].(TerminalUnmatchedEvent)
		if !ok || event.Reason != tt.wantReason || event.MessageType != protocol.MessageTypeTerminalOpenResult || event.ConnectorID != tt.connector {
			t.Fatalf("%s: event = %+v, want unmatched %s", tt.name, f.sink.all(), tt.wantReason)
		}
		// 어느 pending도 소비하거나 다른 pending으로 넘어가지 않았다.
		if got := f.router.PendingTerminalOpens(p.ConnectorID); got != 2 {
			t.Fatalf("%s: pending = %d, want 2 (변함 없음)", tt.name, got)
		}
	}
	for _, e := range f.sink.all() {
		if _, ok := e.(TerminalOpenResultEvent); ok {
			t.Fatal("불일치 message가 OPEN_RESULT event가 됨")
		}
	}

	// 올바른 correlation은 그 OPEN에만 연결한다. 다른 OPEN의 pending은 그대로다.
	if !f.router.RouteTerminalOpenResult(p.ConnectorID, inboundFor(sentA.MessageID, terminalCorr("session-A")), result) {
		t.Fatal("올바른 OPEN_RESULT가 연결되지 않음")
	}
	last := f.sink.all()[len(f.sink.all())-1].(TerminalOpenResultEvent)
	if last.ConnectorID != p.ConnectorID || last.Correlation != terminalCorr("session-A") || last.RequestMessageID != sentA.MessageID ||
		last.RequestID != "request-1" || last.MessageID != "inbound-1" || last.Payload.Outcome != TerminalOutcomeSucceeded {
		t.Fatalf("event = %+v", last)
	}
	if got := f.router.PendingTerminalOpens(p.ConnectorID); got != 1 {
		t.Fatalf("pending = %d, want 1 (session-B만 남음)", got)
	}

	// 같은 OPEN_RESULT를 다시 받으면(중복, 늦은 결과) pending이 없으므로 어디에도 연결하지 않는다.
	before := len(f.sink.all())
	if f.router.RouteTerminalOpenResult(p.ConnectorID, inboundFor(sentA.MessageID, terminalCorr("session-A")), result) {
		t.Fatal("중복 OPEN_RESULT가 연결됨")
	}
	if ev, ok := f.sink.all()[before].(TerminalUnmatchedEvent); !ok || ev.Reason != ReasonNoPending {
		t.Fatalf("중복 event = %+v", f.sink.all()[before])
	}
	if got := f.router.PendingTerminalOpens(p.ConnectorID); got != 1 {
		t.Fatalf("중복 뒤 pending = %d, want 1 (session-B가 소비되지 않음)", got)
	}
}

// FAILED 결과도 그 OPEN에 연결하고 pending을 끝낸다. 오류 문구는 log에 남기지 않는다.
func TestRouteTerminalOpenResultFailedIsRouted(t *testing.T) {
	f := newTerminalFixture(t)
	p := newPrincipal()
	f.connect(p)
	sent, _ := f.router.SendTerminalOpen(context.Background(), openFor(p.ConnectorID, "s1"))

	payload := TerminalOpenResultPayload{Outcome: TerminalOutcomeFailed, Error: &protocol.SafeError{Code: "SSH_UNREACHABLE", Message: "secret-ssh-detail"}}
	if !f.router.RouteTerminalOpenResult(p.ConnectorID, inboundFor(sent.MessageID, terminalCorr("s1")), payload) {
		t.Fatal("FAILED 결과가 연결되지 않음")
	}
	event := f.sink.only(t).(TerminalOpenResultEvent)
	if event.Payload.Outcome != TerminalOutcomeFailed || event.Payload.Error == nil || event.Payload.Error.Code != "SSH_UNREACHABLE" {
		t.Fatalf("event = %+v", event)
	}
	if strings.Contains(f.logs.String(), "secret-ssh-detail") {
		t.Fatal("Connector가 보낸 오류 문구가 log에 남음")
	}
	if f.router.PendingTerminalOpens(p.ConnectorID) != 0 {
		t.Fatal("결과를 받은 OPEN의 pending이 남음")
	}
}

// 재접속은 command retry가 아니다. Router는 아무것도 다시 보내지 않고, 새 Session으로 온 늦은 결과도 pending이 남아 있으면 연결한다.
func TestTerminalOpenPendingSurvivesReconnectWithoutResend(t *testing.T) {
	f := newTerminalFixture(t)
	p := newPrincipal()
	_, oldFrames := f.connect(p)
	sent, err := f.router.SendTerminalOpen(context.Background(), openFor(p.ConnectorID, "s1"))
	if err != nil {
		t.Fatal(err)
	}

	_, newFrames := f.connect(p) // 같은 Connector의 새 Control connection이 current가 된다.
	if oldFrames.count() != 1 || newFrames.count() != 0 {
		t.Fatalf("재접속 뒤 frame = old %d new %d, want 1 0 (재전송 없음)", oldFrames.count(), newFrames.count())
	}
	if !f.router.RouteTerminalOpenResult(p.ConnectorID, inboundFor(sent.MessageID, terminalCorr("s1")), TerminalOpenResultPayload{Outcome: TerminalOutcomeSucceeded}) {
		t.Fatal("재접속한 Session으로 온 결과가 연결되지 않음")
	}
}

func TestForgetTerminalOpenMakesLateResultUnmatched(t *testing.T) {
	f := newTerminalFixture(t)
	p := newPrincipal()
	f.connect(p)
	sent, _ := f.router.SendTerminalOpen(context.Background(), openFor(p.ConnectorID, "s1"))

	if !f.router.ForgetTerminalOpen(p.ConnectorID, "s1") {
		t.Fatal("ForgetTerminalOpen = false, want true")
	}
	if f.router.ForgetTerminalOpen(p.ConnectorID, "s1") {
		t.Fatal("이미 제거한 pending을 다시 제거했다고 보고함")
	}
	if f.router.RouteTerminalOpenResult(p.ConnectorID, inboundFor(sent.MessageID, terminalCorr("s1")), TerminalOpenResultPayload{Outcome: TerminalOutcomeSucceeded}) {
		t.Fatal("제거한 OPEN의 늦은 결과가 연결됨")
	}
	if ev, ok := f.sink.only(t).(TerminalUnmatchedEvent); !ok || ev.Reason != ReasonNoPending {
		t.Fatalf("event = %+v", f.sink.all())
	}
	// 같은 TerminalSession ID로 다시 OPEN할 수 있다.
	if _, err := f.router.SendTerminalOpen(context.Background(), openFor(p.ConnectorID, "s1")); err != nil {
		t.Fatalf("제거 뒤 OPEN error = %v", err)
	}
}

// TERMINAL_CLOSE는 응답을 기다리지 않는 idempotent lifecycle command다. pending을 만들지 않고 반복해서 보낼 수 있다.
func TestSendTerminalClose(t *testing.T) {
	f := newTerminalFixture(t)
	p := newPrincipal()
	_, frames := f.connect(p)

	cl := TerminalClose{ConnectorID: p.ConnectorID, RequestID: "request-1", OperationID: "op-1", Correlation: terminalCorr("s1"), Reason: "LAB_RESET"}
	sent, err := f.router.SendTerminalClose(context.Background(), cl)
	if err != nil {
		t.Fatalf("SendTerminalClose() error = %v", err)
	}
	msg := decodeFrame(t, frames.last(t))
	payload, _ := msg["payload"].(map[string]any)
	if msg["type"] != "TERMINAL_CLOSE" || msg["messageId"] != sent.MessageID || msg["operationId"] != "op-1" ||
		msg["terminalSessionId"] != "s1" || msg["labInstanceId"] != "lab-1" || msg["generation"] != float64(2) ||
		payload["reason"] != "LAB_RESET" || len(payload) != 1 {
		t.Fatalf("message = %v", msg)
	}
	if got := f.router.PendingTerminalOpens(p.ConnectorID); got != 0 {
		t.Fatalf("CLOSE가 pending을 만듦: %d", got)
	}
	if _, err := f.router.SendTerminalClose(context.Background(), cl); err != nil || frames.count() != 2 {
		t.Fatalf("반복 CLOSE error = %v frames = %d, want nil 2", err, frames.count())
	}

	// operationId는 선택이다.
	cl.OperationID = ""
	if _, err := f.router.SendTerminalClose(context.Background(), cl); err != nil {
		t.Fatal(err)
	}
	if msg := decodeFrame(t, frames.last(t)); msg["operationId"] != nil {
		t.Fatalf("operationId가 비어 있을 때 전달됨: %v", msg)
	}
	for i, line := range strings.Split(strings.TrimSpace(f.logs.String()), "\n") {
		event := decodeFrame(t, []byte(line))
		if event["request_id"] != cl.RequestID {
			t.Fatal("TERMINAL_CLOSE log lost request_id")
		}
		if i < 2 && event["operation_id"] != "op-1" {
			t.Fatal("TERMINAL_CLOSE log lost supplied operation_id")
		}
		if i == 2 {
			if _, present := event["operation_id"]; present {
				t.Fatal("TERMINAL_CLOSE log invented operation_id")
			}
		}
	}
}

// TERMINAL_CLOSE의 requestId는 wire와 log가 같은 effective 값을 쓴다.
// 우선순위는 명시한 RequestID > context request ID > 없음이며, 없으면 requestId를 만들지 않는다.
func TestSendTerminalCloseRequestIDWireAndLog(t *testing.T) {
	tests := []struct {
		name      string
		explicit  string
		ctxID     string
		wantEmpty bool
		want      string
	}{
		{name: "explicit", explicit: "request-1", want: "request-1"},
		{name: "context fallback", ctxID: "http-request-1", want: "http-request-1"},
		{name: "explicit wins over context", explicit: "request-1", ctxID: "http-request-1", want: "request-1"},
		{name: "absent", wantEmpty: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newTerminalFixture(t)
			p := newPrincipal()
			_, frames := f.connect(p)

			ctx := context.Background()
			if tt.ctxID != "" {
				ctx = observability.ContextWithRequestID(ctx, tt.ctxID)
			}
			cl := TerminalClose{ConnectorID: p.ConnectorID, RequestID: tt.explicit, Correlation: terminalCorr("s1"), Reason: "SESSION_CLOSED"}
			if _, err := f.router.SendTerminalClose(ctx, cl); err != nil {
				t.Fatalf("SendTerminalClose() error = %v", err)
			}

			wire := decodeFrame(t, frames.last(t))
			event := decodeFrame(t, []byte(strings.TrimSpace(f.logs.String())))
			if tt.wantEmpty {
				if _, present := wire["requestId"]; present {
					t.Fatalf("wire requestId = %v, want absent", wire["requestId"])
				}
				if _, present := event["request_id"]; present {
					t.Fatalf("log request_id = %v, want absent", event["request_id"])
				}
				return
			}
			if wire["requestId"] != tt.want {
				t.Fatalf("wire requestId = %v, want %q", wire["requestId"], tt.want)
			}
			if event["request_id"] != tt.want {
				t.Fatalf("log request_id = %v, want %q", event["request_id"], tt.want)
			}
		})
	}
}

func TestSendTerminalCloseRejectsInvalidAndUnavailable(t *testing.T) {
	f := newTerminalFixture(t)
	p := newPrincipal()

	cl := TerminalClose{ConnectorID: p.ConnectorID, Correlation: terminalCorr("s1"), Reason: "SESSION_CLOSED"}
	if _, err := f.router.SendTerminalClose(context.Background(), cl); !errors.Is(err, ErrConnectorUnavailable) {
		t.Fatalf("미연결 error = %v", err)
	}

	_, frames := f.connect(p)
	for name, mod := range map[string]func(*TerminalClose){
		"reason":            func(c *TerminalClose) { c.Reason = "" },
		"terminalSessionId": func(c *TerminalClose) { c.Correlation.TerminalSessionID = "" },
		"generation":        func(c *TerminalClose) { c.Correlation.Generation = 0 },
	} {
		bad := cl
		mod(&bad)
		if _, err := f.router.SendTerminalClose(context.Background(), bad); !errors.Is(err, ErrInvalidCommand) {
			t.Fatalf("%s: error = %v, want ErrInvalidCommand", name, err)
		}
	}
	if frames.count() != 0 {
		t.Fatalf("잘못된 CLOSE가 전송됨: %d", frames.count())
	}
}

// 종료 통지는 pending에 연결하지 않고 인증된 ConnectorID와 Connector가 주장한 correlation을 그대로 넘긴다.
// 권위 있는 상태와의 대조는 받는 쪽(terminal.Service)이 한다.
func TestRouteTerminalEndedEmitsClaimedCorrelation(t *testing.T) {
	f := newTerminalFixture(t)
	p := newPrincipal()
	code := int64(7)

	f.router.RouteTerminalEnded(p.ConnectorID, TerminalInbound{MessageID: "ended-1", Correlation: terminalCorr("s1")},
		TerminalEndedPayload{Reason: "PTY_EXITED", ExitCode: &code})
	event := f.sink.only(t).(TerminalEndedEvent)
	if event.ConnectorID != p.ConnectorID || event.Correlation != terminalCorr("s1") || event.MessageID != "ended-1" ||
		event.Payload.Reason != "PTY_EXITED" || event.Payload.ExitCode == nil || *event.Payload.ExitCode != 7 {
		t.Fatalf("event = %+v", event)
	}

	f.router.RouteTerminalUnrepresentable(p.ConnectorID, protocol.MessageTypeTerminalEnded, TerminalInbound{Correlation: TerminalCorrelation{TerminalSessionID: "s2", LabInstanceID: "lab-1"}})
	if ev, ok := f.sink.all()[1].(TerminalUnmatchedEvent); !ok || ev.Reason != ReasonUnrepresentable || ev.MessageType != protocol.MessageTypeTerminalEnded {
		t.Fatalf("event = %+v", f.sink.all()[1])
	}
}

// Connector가 주장한 ID는 log와 event에서 길이를 제한한다.
func TestTerminalUnmatchedEventBoundsClaimedIDs(t *testing.T) {
	f := newTerminalFixture(t)
	p := newPrincipal()
	long := strings.Repeat("x", 100000)

	f.router.RouteTerminalOpenResult(p.ConnectorID, inboundFor("r", TerminalCorrelation{TerminalSessionID: long, LabInstanceID: long, Generation: 1}),
		TerminalOpenResultPayload{Outcome: TerminalOutcomeSucceeded})
	ev := f.sink.only(t).(TerminalUnmatchedEvent)
	if len(ev.TerminalSessionID) > maxLoggedIDLen || len(ev.LabInstanceID) > maxLoggedIDLen {
		t.Fatalf("주장한 ID가 제한되지 않음: %d %d", len(ev.TerminalSessionID), len(ev.LabInstanceID))
	}
	if len(f.logs.String()) > 4096 {
		t.Fatalf("log가 너무 큼: %d bytes", len(f.logs.String()))
	}
}

// TerminalSink가 없으면 결과를 조용히 버린다. Router는 그래도 pending을 정확히 처리한다.
func TestTerminalRoutingWithoutSink(t *testing.T) {
	registry := NewRegistry()
	router, err := NewRouter(RouterOptions{Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	p := newPrincipal()
	registry.Register(p, nil).MarkReady((&frameLog{}).route)

	sent, err := router.SendTerminalOpen(context.Background(), openFor(p.ConnectorID, "s1"))
	if err != nil {
		t.Fatal(err)
	}
	if !router.RouteTerminalOpenResult(p.ConnectorID, inboundFor(sent.MessageID, terminalCorr("s1")), TerminalOpenResultPayload{Outcome: TerminalOutcomeSucceeded}) {
		t.Fatal("sink가 없어도 pending은 연결되어야 한다")
	}
	router.RouteTerminalEnded(p.ConnectorID, inboundFor("", terminalCorr("s1")), TerminalEndedPayload{Reason: "PTY_EXITED"})
}
