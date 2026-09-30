package connectorwss

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
)

// terminalEvents는 Router가 넘긴 TerminalSession event를 순서대로 모은다. HandleTerminalEvent는 read loop goroutine에서 호출된다.
type terminalEvents struct {
	mu     sync.Mutex
	events []connector.TerminalEvent
}

func (e *terminalEvents) HandleTerminalEvent(event connector.TerminalEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, event)
}

func (e *terminalEvents) all() []connector.TerminalEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]connector.TerminalEvent(nil), e.events...)
}

func (e *terminalEvents) waitCount(t *testing.T, n int) []connector.TerminalEvent {
	t.Helper()
	waitFor(t, "terminal event", func() bool { return len(e.all()) >= n })
	return e.all()
}

type terminalHarness struct {
	*harness
	router *connector.Router
	events *terminalEvents
}

func newTerminalHarness(t *testing.T) *terminalHarness {
	t.Helper()
	events := &terminalEvents{}
	th := &terminalHarness{events: events}
	th.harness = newHarness(t, func(o *Options) {
		router, err := connector.NewRouter(connector.RouterOptions{Registry: o.Registry, TerminalSink: events, Logger: o.Logger})
		if err != nil {
			t.Fatalf("NewRouter() error = %v", err)
		}
		th.router = router
		o.Router = router
	})
	return th
}

func (th *terminalHarness) open(session string) connector.SentMessage {
	th.t.Helper()
	sent, err := th.router.SendTerminalOpen(context.Background(), connector.TerminalOpen{
		ConnectorID: th.principal.ConnectorID, RequestID: "request-1",
		Correlation: connector.TerminalCorrelation{TerminalSessionID: session, LabInstanceID: "lab-1", Generation: 2},
		TargetVMKey: "workspace", ProviderServerID: "srv-1", Cols: json.RawMessage("80"), Rows: json.RawMessage("24"),
	})
	if err != nil {
		th.t.Fatalf("SendTerminalOpen() error = %v", err)
	}
	return sent
}

// terminalMessage는 Connector가 보내는 terminal-control.schema.json message다. fields로 값을 덮어쓴다(nil이면 삭제).
func terminalMessage(kind string, payload map[string]any, fields map[string]any) map[string]any {
	msg := map[string]any{
		"type": kind, "messageId": "inbound-" + kind, "sentAt": time.Now().UTC().Format(time.RFC3339),
		"terminalSessionId": "session-1", "labInstanceId": "lab-1", "generation": 2,
		"payload": payload,
	}
	for k, v := range fields {
		if v == nil {
			delete(msg, k)
		} else {
			msg[k] = v
		}
	}
	return msg
}

func openResult(replyTo string, outcome string, fields map[string]any) map[string]any {
	merged := map[string]any{"replyToMessageId": replyTo}
	for k, v := range fields {
		merged[k] = v
	}
	return terminalMessage("TERMINAL_OPEN_RESULT", map[string]any{"outcome": outcome}, merged)
}

// SaaS가 보낸 TERMINAL_OPEN을 Connector가 받고, 돌려준 TERMINAL_OPEN_RESULT가 그 OPEN의 pending에 연결된다.
func TestTerminalOpenRoundTripOverControlWSS(t *testing.T) {
	th := newTerminalHarness(t)
	p := th.establish()
	th.waitReady(th.principal.ConnectorID)

	sent := th.open("session-1")
	var opens []string
	waitFor(t, "TERMINAL_OPEN", func() bool { opens = p.framesOfType("TERMINAL_OPEN"); return len(opens) == 1 })
	var open map[string]any
	if err := json.Unmarshal([]byte(opens[0]), &open); err != nil {
		t.Fatal(err)
	}
	if open["messageId"] != sent.MessageID || open["terminalSessionId"] != "session-1" || open["labInstanceId"] != "lab-1" || open["generation"] != float64(2) {
		t.Fatalf("TERMINAL_OPEN = %v", open)
	}

	p.send(openResult(sent.MessageID, "SUCCEEDED", nil))
	event, ok := th.events.waitCount(t, 1)[0].(connector.TerminalOpenResultEvent)
	if !ok {
		t.Fatalf("event = %+v", th.events.all()[0])
	}
	if event.ConnectorID != th.principal.ConnectorID || event.RequestMessageID != sent.MessageID || event.RequestID != "request-1" ||
		event.Correlation != (connector.TerminalCorrelation{TerminalSessionID: "session-1", LabInstanceID: "lab-1", Generation: 2}) ||
		event.Payload.Outcome != connector.TerminalOutcomeSucceeded {
		t.Fatalf("event = %+v", event)
	}
	if th.router.PendingTerminalOpens(th.principal.ConnectorID) != 0 {
		t.Fatal("결과를 받은 OPEN의 pending이 남음")
	}

	// TERMINAL_CLOSE도 같은 Control connection으로 나간다.
	if _, err := th.router.SendTerminalClose(context.Background(), connector.TerminalClose{
		ConnectorID: th.principal.ConnectorID, Correlation: event.Correlation, Reason: "SESSION_CLOSED",
	}); err != nil {
		t.Fatalf("SendTerminalClose() error = %v", err)
	}
	waitFor(t, "TERMINAL_CLOSE", func() bool { return len(p.framesOfType("TERMINAL_CLOSE")) == 1 })
}

// Schema를 만족하지 않는 TERMINAL_OPEN_RESULT는 어떤 pending에도 넘기지 않는다. non-fatal ERROR만 보내고 연결과 pending은 유지한다.
func TestInvalidTerminalOpenResultIsRejectedWithoutRouting(t *testing.T) {
	tests := []struct {
		name string
		msg  func(replyTo string) map[string]any
	}{
		{"replyToMessageId missing", func(string) map[string]any {
			return terminalMessage("TERMINAL_OPEN_RESULT", map[string]any{"outcome": "SUCCEEDED"}, nil)
		}},
		{"replyToMessageId empty", func(string) map[string]any { return openResult("", "SUCCEEDED", nil) }},
		{"outcome missing", func(r string) map[string]any {
			return terminalMessage("TERMINAL_OPEN_RESULT", map[string]any{}, map[string]any{"replyToMessageId": r})
		}},
		{"outcome not in enum", func(r string) map[string]any { return openResult(r, "OK", nil) }},
		{"outcome wrong case", func(r string) map[string]any { return openResult(r, "succeeded", nil) }},
		{"outcome UNKNOWN is not allowed for terminals", func(r string) map[string]any { return openResult(r, "UNKNOWN", nil) }},
		{"payload not an object", func(r string) map[string]any {
			return terminalMessage("TERMINAL_OPEN_RESULT", nil, map[string]any{"replyToMessageId": r, "payload": "x"})
		}},
		{"terminalSessionId missing", func(r string) map[string]any {
			return openResult(r, "SUCCEEDED", map[string]any{"terminalSessionId": nil})
		}},
		{"terminalSessionId empty", func(r string) map[string]any {
			return openResult(r, "SUCCEEDED", map[string]any{"terminalSessionId": ""})
		}},
		{"labInstanceId missing", func(r string) map[string]any { return openResult(r, "SUCCEEDED", map[string]any{"labInstanceId": nil}) }},
		{"generation missing", func(r string) map[string]any { return openResult(r, "SUCCEEDED", map[string]any{"generation": nil}) }},
		{"generation zero", func(r string) map[string]any { return openResult(r, "SUCCEEDED", map[string]any{"generation": 0}) }},
		{"generation fraction", func(r string) map[string]any { return openResult(r, "SUCCEEDED", map[string]any{"generation": 1.5}) }},
		{"generation string", func(r string) map[string]any { return openResult(r, "SUCCEEDED", map[string]any{"generation": "2"}) }},
		{"error not an object", func(r string) map[string]any {
			return terminalMessage("TERMINAL_OPEN_RESULT", map[string]any{"outcome": "FAILED", "error": "x"}, map[string]any{"replyToMessageId": r})
		}},
		{"error without code", func(r string) map[string]any {
			return terminalMessage("TERMINAL_OPEN_RESULT", map[string]any{"outcome": "FAILED", "error": map[string]any{}}, map[string]any{"replyToMessageId": r})
		}},
		{"messageId empty", func(r string) map[string]any { return openResult(r, "SUCCEEDED", map[string]any{"messageId": ""}) }},
		{"sentAt not a date-time", func(r string) map[string]any {
			return openResult(r, "SUCCEEDED", map[string]any{"sentAt": "yesterday"})
		}},
		{"property name with different case", func(r string) map[string]any {
			return openResult(r, "SUCCEEDED", map[string]any{"terminalSessionId": nil, "TerminalSessionId": "session-1"})
		}},
	}
	// 비용을 줄이려고 connection 하나로 모든 경우를 차례로 보낸다. 잘못된 message는 연결을 끊지도 pending을 소비하지도 않으므로
	// 각 경우 뒤의 상태가 같아야 한다.
	th := newTerminalHarness(t)
	p := th.establish()
	th.waitReady(th.principal.ConnectorID)
	sent := th.open("session-1")

	for i, tt := range tests {
		p.send(tt.msg(sent.MessageID))
		p.sync()

		errs := p.framesOfType("ERROR")
		if len(errs) != i+1 {
			t.Fatalf("%s: ERROR frame = %d개, want %d개", tt.name, len(errs), i+1)
		}
		if last := errs[len(errs)-1]; !strings.Contains(last, `"INVALID_MESSAGE"`) || strings.Contains(last, `"fatal":true`) {
			t.Fatalf("%s: ERROR frame = %s, want non-fatal INVALID_MESSAGE", tt.name, last)
		}
		if events := th.events.all(); len(events) != 0 {
			t.Fatalf("%s: 잘못된 message가 event가 됨: %+v", tt.name, events)
		}
		if th.router.PendingTerminalOpens(th.principal.ConnectorID) != 1 {
			t.Fatalf("%s: 잘못된 message가 pending을 소비함", tt.name)
		}
	}
	// 연결은 유지한다. 이어서 온 올바른 결과가 연결된다.
	p.send(openResult(sent.MessageID, "SUCCEEDED", nil))
	th.events.waitCount(t, 1)
}

// Schema는 만족하지만 pending과 맞지 않는 message는 unmatched로만 알리고 어떤 pending도 소비하지 않는다.
func TestMismatchedTerminalOpenResultIsUnmatched(t *testing.T) {
	th := newTerminalHarness(t)
	p := th.establish()
	th.waitReady(th.principal.ConnectorID)
	sent := th.open("session-1")

	tests := []struct {
		name   string
		msg    map[string]any
		reason connector.UnmatchedReason
	}{
		{"wrong replyToMessageId", openResult("other-message", "SUCCEEDED", nil), connector.ReasonReplyMismatch},
		{"wrong terminalSessionId", openResult(sent.MessageID, "SUCCEEDED", map[string]any{"terminalSessionId": "session-2"}), connector.ReasonNoPending},
		{"wrong labInstanceId", openResult(sent.MessageID, "SUCCEEDED", map[string]any{"labInstanceId": "lab-2"}), connector.ReasonCorrelationMismatch},
		{"wrong generation", openResult(sent.MessageID, "SUCCEEDED", map[string]any{"generation": 3}), connector.ReasonCorrelationMismatch},
		// Schema에 maximum이 없으므로 int64를 넘는 generation은 Schema 위반이 아니다. ERROR 없이 unmatched로 알린다.
		{"generation beyond int64", openResult(sent.MessageID, "SUCCEEDED", map[string]any{"generation": 1e30}), connector.ReasonUnrepresentable},
	}
	for i, tt := range tests {
		p.send(tt.msg)
		events := th.events.waitCount(t, i+1)
		unmatched, ok := events[i].(connector.TerminalUnmatchedEvent)
		if !ok || unmatched.Reason != tt.reason {
			t.Fatalf("%s: event = %+v, want unmatched %s", tt.name, events[i], tt.reason)
		}
		if th.router.PendingTerminalOpens(th.principal.ConnectorID) != 1 {
			t.Fatalf("%s: pending이 소비됨", tt.name)
		}
	}
	if errs := p.framesOfType("ERROR"); len(errs) != 0 {
		t.Fatalf("Schema-valid message에 ERROR를 보냄: %v", errs)
	}
}

// TERMINAL_ENDED는 Connector가 보낸 종료 통지이며 인증된 ConnectorID와 주장한 correlation을 함께 넘긴다.
func TestTerminalEndedIsRoutedAsClaim(t *testing.T) {
	th := newTerminalHarness(t)
	p := th.establish()
	th.waitReady(th.principal.ConnectorID)

	p.send(terminalMessage("TERMINAL_ENDED", map[string]any{"reason": "PTY_EXITED", "exitCode": 130}, nil))
	ended, ok := th.events.waitCount(t, 1)[0].(connector.TerminalEndedEvent)
	if !ok {
		t.Fatalf("event = %+v", th.events.all()[0])
	}
	if ended.ConnectorID != th.principal.ConnectorID || ended.Payload.Reason != "PTY_EXITED" || ended.Payload.ExitCode == nil || *ended.Payload.ExitCode != 130 ||
		ended.Correlation != (connector.TerminalCorrelation{TerminalSessionID: "session-1", LabInstanceID: "lab-1", Generation: 2}) {
		t.Fatalf("event = %+v", ended)
	}

	// exit code를 알려 주지 않았거나 표현할 수 없으면 nil이다. 0으로 만들지 않는다.
	for i, payload := range []map[string]any{
		{"reason": "SSH_DISCONNECTED", "error": map[string]any{"code": "SSH_LOST", "message": "x"}},
		{"reason": "PTY_EXITED", "exitCode": 1e30},
	} {
		p.send(terminalMessage("TERMINAL_ENDED", payload, map[string]any{"messageId": "ended-" + string(rune('a'+i))}))
		got := th.events.waitCount(t, i+2)[i+1].(connector.TerminalEndedEvent)
		if got.Payload.ExitCode != nil {
			t.Fatalf("payload %v: ExitCode = %d, want nil", payload, *got.Payload.ExitCode)
		}
	}
}

func TestInvalidTerminalEndedIsRejectedWithoutRouting(t *testing.T) {
	tests := []struct {
		name string
		msg  map[string]any
	}{
		{"reason missing", terminalMessage("TERMINAL_ENDED", map[string]any{}, nil)},
		{"reason empty", terminalMessage("TERMINAL_ENDED", map[string]any{"reason": ""}, nil)},
		{"reason not a string", terminalMessage("TERMINAL_ENDED", map[string]any{"reason": 1}, nil)},
		{"exitCode fraction", terminalMessage("TERMINAL_ENDED", map[string]any{"reason": "PTY_EXITED", "exitCode": 1.5}, nil)},
		{"exitCode string", terminalMessage("TERMINAL_ENDED", map[string]any{"reason": "PTY_EXITED", "exitCode": "1"}, nil)},
		{"error not an object", terminalMessage("TERMINAL_ENDED", map[string]any{"reason": "PTY_EXITED", "error": 1}, nil)},
		{"terminalSessionId missing", terminalMessage("TERMINAL_ENDED", map[string]any{"reason": "PTY_EXITED"}, map[string]any{"terminalSessionId": nil})},
		{"labInstanceId missing", terminalMessage("TERMINAL_ENDED", map[string]any{"reason": "PTY_EXITED"}, map[string]any{"labInstanceId": nil})},
		{"generation missing", terminalMessage("TERMINAL_ENDED", map[string]any{"reason": "PTY_EXITED"}, map[string]any{"generation": nil})},
		{"generation zero", terminalMessage("TERMINAL_ENDED", map[string]any{"reason": "PTY_EXITED"}, map[string]any{"generation": 0})},
	}
	th := newTerminalHarness(t)
	p := th.establish()
	th.waitReady(th.principal.ConnectorID)

	for i, tt := range tests {
		p.send(tt.msg)
		p.sync()
		errs := p.framesOfType("ERROR")
		if len(errs) != i+1 || !strings.Contains(errs[len(errs)-1], `"INVALID_MESSAGE"`) {
			t.Fatalf("%s: ERROR frame = %v, want INVALID_MESSAGE 추가 1건", tt.name, errs)
		}
		if events := th.events.all(); len(events) != 0 {
			t.Fatalf("%s: 잘못된 message가 event가 됨: %+v", tt.name, events)
		}
	}
}

// generation이 int64를 넘는 TERMINAL_ENDED는 Schema 위반이 아니므로 ERROR 없이 unmatched로 알린다.
func TestTerminalEndedWithUnrepresentableGenerationIsUnmatched(t *testing.T) {
	th := newTerminalHarness(t)
	p := th.establish()
	th.waitReady(th.principal.ConnectorID)

	p.send(terminalMessage("TERMINAL_ENDED", map[string]any{"reason": "PTY_EXITED"}, map[string]any{"generation": 1e30}))
	ev, ok := th.events.waitCount(t, 1)[0].(connector.TerminalUnmatchedEvent)
	if !ok || ev.Reason != connector.ReasonUnrepresentable || ev.MessageType != protocol.MessageTypeTerminalEnded {
		t.Fatalf("event = %+v", th.events.all()[0])
	}
	if errs := p.framesOfType("ERROR"); len(errs) != 0 {
		t.Fatalf("Schema-valid message에 ERROR를 보냄: %v", errs)
	}
}

// Terminal 결과도 그 Session이 아직 current일 때만 routing한다. 같은 Connector의 새 connection이 current가 되면 이전 connection이
// 뒤늦게 보낸 message는 routing하지 않는다. 재접속은 command retry가 아니므로 새 Session으로 온 결과는 남은 pending에 연결된다.
func TestTerminalResultFromReplacedSessionIsNotRouted(t *testing.T) {
	th := newTerminalHarness(t)
	old := th.establish()
	th.waitReady(th.principal.ConnectorID)
	sent := th.open("session-1")

	fresh := th.establish() // 같은 Credential로 새 connection이 current가 되고 이전 connection은 종료를 요청받는다.
	th.waitReady(th.principal.ConnectorID)

	// 이전 connection은 4002 종료 요청을 받은 상태에서도 write는 할 수 있다. 여러 번 보내 처리 순서와 무관하게 확인한다.
	for range 5 {
		_ = old.conn.WriteJSON(openResult(sent.MessageID, "SUCCEEDED", nil))
		_ = old.conn.WriteJSON(terminalMessage("TERMINAL_ENDED", map[string]any{"reason": "PTY_EXITED"}, nil))
	}
	if closeErr := old.waitClosed(5 * time.Second); closeErr.Code != closeReplaced {
		t.Fatalf("이전 connection close code = %d, want %d", closeErr.Code, closeReplaced)
	}
	fresh.sync()
	if events := th.events.all(); len(events) != 0 {
		t.Fatalf("교체된 Session의 message가 routing됨: %+v", events)
	}
	if th.router.PendingTerminalOpens(th.principal.ConnectorID) != 1 {
		t.Fatal("stale message가 pending을 끝냄")
	}
	if got := fresh.framesOfType("TERMINAL_OPEN"); len(got) != 0 {
		t.Fatalf("재접속한 Session이 TERMINAL_OPEN을 다시 받음: %v", got)
	}

	fresh.send(openResult(sent.MessageID, "SUCCEEDED", nil))
	if _, ok := th.events.waitCount(t, 1)[0].(connector.TerminalOpenResultEvent); !ok {
		t.Fatalf("event = %+v", th.events.all())
	}
}

// 교체·revoke된 Registration의 message는 fence에서 막힌다. 교체가 끝난 뒤 시작하는 routing을 결정적으로 재현한다.
func TestRouteTerminalInboundFenceRejectsRetiredSession(t *testing.T) {
	registry := connector.NewRegistry()
	events := &terminalEvents{}
	router, err := connector.NewRouter(connector.RouterOptions{Registry: registry, TerminalSink: events})
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(Options{Auth: &fakeAuth{}, Heartbeats: &fakeHeartbeats{}, Registry: registry, Router: router})
	if err != nil {
		t.Fatal(err)
	}

	principal := connector.Principal{ConnectorID: uuid.New(), OrganizationID: uuid.New(), CredentialID: uuid.New()}
	cc := newControlConn(&fakeWS{})
	old := registry.Register(principal, nil)
	old.MarkReady(cc.route)
	sent, err := router.SendTerminalOpen(context.Background(), connector.TerminalOpen{
		ConnectorID: principal.ConnectorID, Correlation: connector.TerminalCorrelation{TerminalSessionID: "session-1", LabInstanceID: "lab-1", Generation: 2},
		TargetVMKey: "workspace", ProviderServerID: "srv-1", Cols: json.RawMessage("80"), Rows: json.RawMessage("24"),
	})
	if err != nil {
		t.Fatal(err)
	}

	raw, _ := json.Marshal(openResult(sent.MessageID, "SUCCEEDED", nil))
	envelope := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	var logs syncBuffer
	log := newTestLogger(&logs)

	registry.Register(principal, nil) // 교체가 끝났다. old는 더 이상 current가 아니다.
	h.routeTerminalInbound(cc, old, principal, log, protocol.MessageTypeTerminalOpenResult, envelope)
	if len(events.all()) != 0 {
		t.Fatalf("교체된 Session의 message가 routing됨: %+v", events.all())
	}
	if router.PendingTerminalOpens(principal.ConnectorID) != 1 {
		t.Fatal("pending이 소비됨")
	}

	// 같은 message를 current Registration으로 보내면 연결된다(대조군).
	current := registry.Register(principal, nil)
	h.routeTerminalInbound(cc, current, principal, log, protocol.MessageTypeTerminalOpenResult, envelope)
	if len(events.all()) != 1 {
		t.Fatalf("current Session의 message가 routing되지 않음: %+v", events.all())
	}
}

// Router가 없으면 Terminal 결과를 해석하지 않고 버린다. ERROR도 보내지 않는다.
func TestTerminalResultsAreIgnoredWithoutRouter(t *testing.T) {
	h := newHarness(t)
	p := h.establish()
	h.waitReady(h.principal.ConnectorID)

	p.send(openResult("m", "SUCCEEDED", nil))
	p.send(terminalMessage("TERMINAL_ENDED", map[string]any{"reason": "PTY_EXITED"}, nil))
	p.send(terminalMessage("TERMINAL_OPEN_RESULT", map[string]any{}, nil)) // 잘못된 message도 해석하지 않는다.
	p.sync()
	if errs := p.framesOfType("ERROR"); len(errs) != 0 {
		t.Fatalf("Router가 없는데 ERROR를 보냄: %v", errs)
	}
}

// Connector가 보낸 message의 오류 문구와 Terminal 본문은 log에 남지 않는다.
func TestTerminalInboundDoesNotLogConnectorSuppliedText(t *testing.T) {
	th := newTerminalHarness(t)
	p := th.establish()
	th.waitReady(th.principal.ConnectorID)
	sent := th.open("session-1")

	p.send(terminalMessage("TERMINAL_OPEN_RESULT", map[string]any{
		"outcome": "FAILED", "error": map[string]any{"code": "SSH_FAILED", "message": "secret-ssh-output"},
	}, map[string]any{"replyToMessageId": sent.MessageID}))
	th.events.waitCount(t, 1)
	p.send(terminalMessage("TERMINAL_ENDED", map[string]any{"reason": "PTY_EXITED", "error": map[string]any{"code": "X", "message": "secret-tail-of-screen"}}, nil))
	th.events.waitCount(t, 2)
	p.sync()

	if logs := th.logs.String(); strings.Contains(logs, "secret-ssh-output") || strings.Contains(logs, "secret-tail-of-screen") {
		t.Fatalf("Connector가 보낸 문구가 log에 남음: %s", logs)
	}
}
