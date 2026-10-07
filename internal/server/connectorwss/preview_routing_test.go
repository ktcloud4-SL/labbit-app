package connectorwss

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
)

// previewEvents는 Router가 넘긴 Preview event를 순서대로 모은다. HandlePreviewEvent는 read loop goroutine에서 호출된다.
type previewEvents struct {
	mu     sync.Mutex
	events []connector.PreviewEvent
}

func (e *previewEvents) HandlePreviewEvent(event connector.PreviewEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, event)
}

func (e *previewEvents) all() []connector.PreviewEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]connector.PreviewEvent(nil), e.events...)
}

func (e *previewEvents) waitCount(t *testing.T, n int) []connector.PreviewEvent {
	t.Helper()
	waitFor(t, "preview event", func() bool { return len(e.all()) >= n })
	return e.all()
}

type retiredSessions struct {
	mu       sync.Mutex
	sessions []connector.Session
	reasons  []connector.CloseReason
}

func (r *retiredSessions) SessionRetired(session connector.Session, reason connector.CloseReason) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions = append(r.sessions, session)
	r.reasons = append(r.reasons, reason)
}

func (r *retiredSessions) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sessions)
}

type previewHarness struct {
	*harness
	router  *connector.Router
	events  *previewEvents
	retired *retiredSessions
}

func newPreviewHarness(t *testing.T) *previewHarness {
	t.Helper()
	ph := &previewHarness{events: &previewEvents{}, retired: &retiredSessions{}}
	ph.harness = newHarness(t, func(o *Options) {
		o.Registry.SetSessionObserver(ph.retired)
		router, err := connector.NewRouter(connector.RouterOptions{Registry: o.Registry, PreviewSink: ph.events, Logger: o.Logger})
		if err != nil {
			t.Fatalf("NewRouter() error = %v", err)
		}
		ph.router = router
		o.Router = router
	})
	return ph
}

func (ph *previewHarness) openPreview(session string) (connector.SentMessage, error) {
	return ph.router.SendPreviewOpen(context.Background(), connector.PreviewOpen{
		ConnectorID: ph.principal.ConnectorID, RequestID: "request-1",
		Correlation: connector.PreviewCorrelation{PreviewSessionID: session, LabInstanceID: "lab-1", Generation: 3},
		TargetVMKey: "vk-web", ProviderServerID: "srv-1", TargetPort: 5173,
	})
}

// previewMessage는 Connector가 보내는 preview-control.schema.json message다. fields로 값을 덮어쓴다(nil이면 삭제).
func previewMessage(kind string, payload map[string]any, fields map[string]any) map[string]any {
	msg := map[string]any{
		"type": kind, "messageId": "inbound-" + kind, "sentAt": time.Now().UTC().Format(time.RFC3339),
		"previewSessionId": "preview-1", "labInstanceId": "lab-1", "generation": 3,
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

func previewOpenResult(replyTo, outcome string, fields map[string]any) map[string]any {
	merged := map[string]any{"replyToMessageId": replyTo}
	for k, v := range fields {
		merged[k] = v
	}
	return previewMessage("PREVIEW_OPEN_RESULT", map[string]any{"outcome": outcome}, merged)
}

// HELLO가 선언한 capability를 Session이 기억하고, SaaS는 preview-v1을 선언한 Connector에만 PREVIEW_OPEN을 보낸다.
func TestPreviewOpenOnlyReachesConnectorsThatDeclaredPreviewV1(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		declare func(map[string]any)
		want    bool
	}{
		{"preview-v1 선언", declare("preview-v1"), true},
		{"다른 capability와 함께 선언", declare("terminal", "file-v1", "preview-v1"), true},
		{"capabilities field 없음(기존 Connector)", nil, false},
		{"빈 capabilities", declare(), false},
		{"file-v1만 선언", declare("file-v1"), false},
		{"비슷한 이름", declare("preview", "preview-v2"), false},
		{"버전만 높음", func(hello map[string]any) { payloadOf(hello)["connectorVersion"] = "99.0.0" }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ph := newPreviewHarness(t)
			p := ph.establishDeclaring(tc.declare)

			_, err := ph.openPreview("preview-1")
			if tc.want {
				if err != nil {
					t.Fatalf("SendPreviewOpen() error = %v", err)
				}
				waitFor(t, "PREVIEW_OPEN", func() bool { return len(p.framesOfType("PREVIEW_OPEN")) == 1 })
				return
			}
			if !errors.Is(err, connector.ErrCapabilityUnsupported) {
				t.Fatalf("SendPreviewOpen() error = %v, want ErrCapabilityUnsupported", err)
			}
			p.sync()
			if frames := p.framesOfType("PREVIEW_OPEN"); len(frames) != 0 {
				t.Fatalf("capability 없는 Connector가 PREVIEW_OPEN을 받음: %v", frames)
			}
		})
	}
}

// 재접속한 connection이 preview-v1을 선언하지 않으면 이전 connection의 선언을 쓰지 않는다.
func TestReconnectedConnectorMustDeclarePreviewV1Again(t *testing.T) {
	t.Parallel()
	ph := newPreviewHarness(t)
	ph.establishDeclaring(declare("preview-v1"))
	if _, err := ph.openPreview("preview-1"); err != nil {
		t.Fatalf("SendPreviewOpen() error = %v", err)
	}

	// 같은 Credential의 새 connection이 이전 connection을 교체한다. preview-v1을 선언하지 않는다.
	conn2, _, err := ph.dial(bearer(testCredential), "labbit.connector.v1")
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	t.Cleanup(func() { _ = conn2.Close() })
	sendJSON(t, conn2, validHello())
	if ack := readJSON(t, conn2); ack["type"] != "HELLO_ACK" {
		t.Fatalf("첫 응답 = %v, want HELLO_ACK", ack)
	}
	waitFor(t, "새 Session의 capability 반영", func() bool {
		_, err := ph.openPreview("preview-2")
		return errors.Is(err, connector.ErrCapabilityUnsupported)
	})
}

// Control connection의 교체는 Registry의 SessionObserver로 통지된다. PreviewSession이 이전 Control Session에 묶인 trust를 거두는 근거다.
func TestControlSessionReplacementIsObserved(t *testing.T) {
	t.Parallel()
	ph := newPreviewHarness(t)
	ph.establishDeclaring(declare("preview-v1"))
	first, ok := ph.registry.Current(ph.principal.ConnectorID)
	if !ok {
		t.Fatal("current Session이 없음")
	}

	conn2, _, err := ph.dial(bearer(testCredential), "labbit.connector.v1")
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	t.Cleanup(func() { _ = conn2.Close() })
	sendJSON(t, conn2, validHello())
	if ack := readJSON(t, conn2); ack["type"] != "HELLO_ACK" {
		t.Fatalf("첫 응답 = %v, want HELLO_ACK", ack)
	}
	waitFor(t, "교체 통지", func() bool { return ph.retired.count() >= 1 })

	ph.retired.mu.Lock()
	defer ph.retired.mu.Unlock()
	if ph.retired.sessions[0] != first || ph.retired.reasons[0] != connector.CloseReplaced {
		t.Fatalf("통지 = %+v/%v, want 교체된 첫 Session(CloseReplaced)", ph.retired.sessions[0], ph.retired.reasons[0])
	}
}

func TestPreviewOpenResultRoundTripOverControlWSS(t *testing.T) {
	t.Parallel()
	ph := newPreviewHarness(t)
	p := ph.establishDeclaring(declare("preview-v1"))

	sent, err := ph.openPreview("preview-1")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "PREVIEW_OPEN", func() bool { return len(p.framesOfType("PREVIEW_OPEN")) == 1 })

	// FAILED는 error가 필요하지 않다(preview-control.schema.json: error optional).
	p.send(previewOpenResult(sent.MessageID, "FAILED", map[string]any{}))
	event, ok := ph.events.waitCount(t, 1)[0].(connector.PreviewOpenResultEvent)
	if !ok {
		t.Fatalf("event = %+v", ph.events.all()[0])
	}
	if event.ConnectorID != ph.principal.ConnectorID || event.RequestMessageID != sent.MessageID || event.RequestID != "request-1" ||
		event.Correlation != (connector.PreviewCorrelation{PreviewSessionID: "preview-1", LabInstanceID: "lab-1", Generation: 3}) ||
		event.Payload.Outcome != connector.PreviewOutcomeFailed {
		t.Fatalf("event = %+v", event)
	}
	if ph.router.PendingPreviewOpens(ph.principal.ConnectorID) != 0 {
		t.Fatal("결과를 받은 PREVIEW_OPEN의 pending이 남음")
	}

	// PREVIEW_CLOSE도 같은 Control connection으로 나가며 경로나 본문이 없다.
	if _, err := ph.router.SendPreviewClose(context.Background(), connector.PreviewClose{
		ConnectorID: ph.principal.ConnectorID, Correlation: event.Correlation, Reason: "SESSION_CLOSED",
	}); err != nil {
		t.Fatalf("SendPreviewClose() error = %v", err)
	}
	waitFor(t, "PREVIEW_CLOSE", func() bool { return len(p.framesOfType("PREVIEW_CLOSE")) == 1 })
}

func TestPreviewOpenResultWithAnErrorDecodesTheSafeError(t *testing.T) {
	t.Parallel()
	ph := newPreviewHarness(t)
	p := ph.establishDeclaring(declare("preview-v1"))
	sent, err := ph.openPreview("preview-1")
	if err != nil {
		t.Fatal(err)
	}
	msg := previewOpenResult(sent.MessageID, "FAILED", nil)
	payloadOf(msg)["error"] = map[string]any{"code": "APP_NOT_RUNNING", "message": "nothing listening"}
	p.send(msg)

	event := ph.events.waitCount(t, 1)[0].(connector.PreviewOpenResultEvent)
	if event.Payload.Error == nil || event.Payload.Error.Code != "APP_NOT_RUNNING" || event.Payload.Error.Message != "nothing listening" {
		t.Fatalf("event = %+v", event)
	}
}

// Schema를 만족하지 않는 PREVIEW_OPEN_RESULT는 어떤 pending에도 넘기지 않는다. non-fatal ERROR만 보내고 연결과 pending은 유지한다.
func TestSchemaInvalidPreviewOpenResultIsRejectedAndKeepsThePending(t *testing.T) {
	t.Parallel()
	ph := newPreviewHarness(t)
	p := ph.establishDeclaring(declare("preview-v1"))
	sent, err := ph.openPreview("preview-1")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		msg  map[string]any
	}{
		{"replyToMessageId 없음", previewOpenResult(sent.MessageID, "FAILED", map[string]any{"replyToMessageId": nil})},
		{"replyToMessageId 빈 문자열", previewOpenResult("", "FAILED", map[string]any{"replyToMessageId": ""})},
		{"previewSessionId 없음", previewOpenResult(sent.MessageID, "FAILED", map[string]any{"previewSessionId": nil})},
		{"previewSessionId 빈 문자열", previewOpenResult(sent.MessageID, "FAILED", map[string]any{"previewSessionId": ""})},
		{"previewSessionId가 문자열이 아님", previewOpenResult(sent.MessageID, "FAILED", map[string]any{"previewSessionId": 7})},
		{"labInstanceId 없음", previewOpenResult(sent.MessageID, "FAILED", map[string]any{"labInstanceId": nil})},
		{"generation 없음", previewOpenResult(sent.MessageID, "FAILED", map[string]any{"generation": nil})},
		{"generation 0", previewOpenResult(sent.MessageID, "FAILED", map[string]any{"generation": 0})},
		{"generation 문자열", previewOpenResult(sent.MessageID, "FAILED", map[string]any{"generation": "3"})},
		{"outcome 알 수 없음", previewOpenResult(sent.MessageID, "UNKNOWN", nil)},
		{"outcome 소문자", previewOpenResult(sent.MessageID, "failed", nil)},
		{"outcome 없음", previewMessage("PREVIEW_OPEN_RESULT", map[string]any{}, map[string]any{"replyToMessageId": sent.MessageID})},
		{"error가 object가 아님", previewMessage("PREVIEW_OPEN_RESULT", map[string]any{"outcome": "FAILED", "error": "x"}, map[string]any{"replyToMessageId": sent.MessageID})},
		{"error.code 없음", previewMessage("PREVIEW_OPEN_RESULT", map[string]any{"outcome": "FAILED", "error": map[string]any{}}, map[string]any{"replyToMessageId": sent.MessageID})},
		{"payload가 object가 아님", previewOpenResult(sent.MessageID, "FAILED", map[string]any{"payload": "x"})},
		{"sentAt 형식 오류", previewOpenResult(sent.MessageID, "FAILED", map[string]any{"sentAt": "yesterday"})},
	}
	for _, tc := range cases {
		p.send(tc.msg)
	}
	p.sync()

	if events := ph.events.all(); len(events) != 0 {
		t.Fatalf("Schema 위반 message가 routing됨: %+v", events)
	}
	if ph.router.PendingPreviewOpens(ph.principal.ConnectorID) != 1 {
		t.Fatal("Schema 위반 message가 pending을 끝냈거나 지움")
	}
	if got := len(p.framesOfType("ERROR")); got != len(cases) {
		t.Fatalf("ERROR %d개, want %d개(message마다 non-fatal ERROR)", got, len(cases))
	}
	if p.closedNow() {
		t.Fatal("message 하나의 오류로 연결을 닫음")
	}

	// 연결과 pending이 유지되므로 올바른 결과는 그대로 연결된다.
	p.send(previewOpenResult(sent.MessageID, "FAILED", nil))
	ph.events.waitCount(t, 1)
}

// Schema는 만족하지만 pending과 맞지 않는 PREVIEW_OPEN_RESULT는 ERROR 없이 unmatched로 알리고 pending을 유지한다.
func TestMismatchedPreviewOpenResultIsUnmatchedNotCompleted(t *testing.T) {
	t.Parallel()
	ph := newPreviewHarness(t)
	p := ph.establishDeclaring(declare("preview-v1"))
	sent, err := ph.openPreview("preview-1")
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]map[string]any{
		"다른 previewSessionId":  previewOpenResult(sent.MessageID, "FAILED", map[string]any{"previewSessionId": "preview-2"}),
		"다른 labInstanceId":     previewOpenResult(sent.MessageID, "FAILED", map[string]any{"labInstanceId": "lab-2"}),
		"다른 generation":        previewOpenResult(sent.MessageID, "FAILED", map[string]any{"generation": 2}),
		"다른 replyToMessageId":  previewOpenResult("not-the-open", "FAILED", nil),
		"int64를 넘는 generation": previewOpenResult(sent.MessageID, "FAILED", map[string]any{"generation": 1e30}),
	}
	for _, msg := range cases {
		p.send(msg)
	}
	p.sync()

	events := ph.events.waitCount(t, len(cases))
	for _, e := range events {
		if _, ok := e.(connector.PreviewUnmatchedEvent); !ok {
			t.Fatalf("맞지 않는 message가 연결됨: %+v", e)
		}
	}
	if ph.router.PendingPreviewOpens(ph.principal.ConnectorID) != 1 {
		t.Fatal("맞지 않는 message가 pending을 끝냈음")
	}
	if got := len(p.framesOfType("ERROR")); got != 0 {
		t.Fatalf("Schema-valid message에 ERROR %d개", got)
	}
}

// Router가 없는 구성에서는 Preview 결과를 해석하지 않고 버린다. 연결은 유지한다.
func TestPreviewOpenResultWithoutARouterIsIgnored(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	p := h.establish()
	h.waitReady(h.principal.ConnectorID)
	p.send(previewOpenResult("x", "FAILED", nil))
	p.sync()
	if p.closedNow() {
		t.Fatal("연결이 닫힘")
	}
}
