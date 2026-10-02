package connectorwss

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
)

// fileEvents는 Router가 넘긴 File event를 순서대로 모은다. HandleFileEvent는 read loop goroutine에서 호출된다.
type fileEvents struct {
	mu     sync.Mutex
	events []connector.FileEvent
}

func (e *fileEvents) HandleFileEvent(event connector.FileEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, event)
}

func (e *fileEvents) all() []connector.FileEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]connector.FileEvent(nil), e.events...)
}

func (e *fileEvents) waitCount(t *testing.T, n int) []connector.FileEvent {
	t.Helper()
	waitFor(t, "file event", func() bool { return len(e.all()) >= n })
	return e.all()
}

type fileHarness struct {
	*harness
	router *connector.Router
	events *fileEvents
}

func newFileHarness(t *testing.T) *fileHarness {
	t.Helper()
	events := &fileEvents{}
	fh := &fileHarness{events: events}
	fh.harness = newHarness(t, func(o *Options) {
		router, err := connector.NewRouter(connector.RouterOptions{Registry: o.Registry, FileSink: events, Logger: o.Logger})
		if err != nil {
			t.Fatalf("NewRouter() error = %v", err)
		}
		fh.router = router
		o.Router = router
	})
	return fh
}

// establishDeclaring은 mutate로 바꾼 HELLO를 보내고 HELLO_ACK를 받은 뒤 protocol-ready가 될 때까지 기다린다.
func (h *harness) establishDeclaring(mutate func(map[string]any)) *peer {
	h.t.Helper()
	conn, _, err := h.dial(bearer(testCredential), protocol.SubprotocolControl)
	if err != nil {
		h.t.Fatalf("Dial() error = %v", err)
	}
	h.t.Cleanup(func() { _ = conn.Close() })

	hello := validHello()
	if mutate != nil {
		mutate(hello)
	}
	sendJSON(h.t, conn, hello)
	if ack := readJSON(h.t, conn); ack["type"] != "HELLO_ACK" {
		h.t.Fatalf("첫 응답 = %v, want HELLO_ACK", ack)
	}
	p := &peer{t: h.t, conn: conn, done: make(chan struct{})}
	conn.SetPongHandler(func(string) error {
		p.pongs.Add(1)
		return nil
	})
	go p.readLoop()
	h.waitReady(h.principal.ConnectorID)
	return p
}

// declare는 HELLO가 capabilities를 선언하게 한다. 인자가 없으면 빈 배열이다(field 누락이나 null이 아니다).
func declare(capabilities ...string) func(map[string]any) {
	return func(hello map[string]any) { payloadOf(hello)["capabilities"] = append([]string{}, capabilities...) }
}

func (fh *fileHarness) openFile(request string) (connector.SentMessage, error) {
	return fh.router.SendFileOpen(context.Background(), connector.FileOpen{
		ConnectorID: fh.principal.ConnectorID, RequestID: "request-1",
		Correlation: connector.FileCorrelation{FileRequestID: request, LabInstanceID: "lab-1", Generation: 3},
		Operation:   connector.FileOperationTree, TargetVMKey: "vk-web", ProviderServerID: "srv-1",
	})
}

// fileMessage는 Connector가 보내는 file-control.schema.json message다. fields로 값을 덮어쓴다(nil이면 삭제).
func fileMessage(kind string, payload map[string]any, fields map[string]any) map[string]any {
	msg := map[string]any{
		"type": kind, "messageId": "inbound-" + kind, "sentAt": time.Now().UTC().Format(time.RFC3339),
		"fileRequestId": "file-1", "labInstanceId": "lab-1", "generation": 3,
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

func fileOpenResult(replyTo, outcome string, fields map[string]any) map[string]any {
	merged := map[string]any{"replyToMessageId": replyTo}
	for k, v := range fields {
		merged[k] = v
	}
	return fileMessage("FILE_OPEN_RESULT", map[string]any{"outcome": outcome}, merged)
}

// HELLO가 선언한 capability를 Session이 기억하고, SaaS는 file-v1을 선언한 Connector에만 FILE_OPEN을 보낸다.
func TestFileOpenOnlyReachesConnectorsThatDeclaredFileV1(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		declare func(map[string]any)
		want    bool
	}{
		{"file-v1 선언", declare("file-v1"), true},
		{"다른 capability와 함께 선언", declare("terminal", "file-v1", "preview"), true},
		{"capabilities field 없음(기존 Connector)", nil, false},
		{"빈 capabilities", declare(), false},
		{"다른 capability만 선언", declare("terminal", "preview"), false},
		{"버전만 높음", func(hello map[string]any) { payloadOf(hello)["connectorVersion"] = "99.0.0" }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fh := newFileHarness(t)
			p := fh.establishDeclaring(tc.declare)

			_, err := fh.openFile("file-1")
			if tc.want {
				if err != nil {
					t.Fatalf("SendFileOpen() error = %v", err)
				}
				waitFor(t, "FILE_OPEN", func() bool { return len(p.framesOfType("FILE_OPEN")) == 1 })
				return
			}
			if !errors.Is(err, connector.ErrCapabilityUnsupported) {
				t.Fatalf("SendFileOpen() error = %v, want ErrCapabilityUnsupported", err)
			}
			p.sync()
			if frames := p.framesOfType("FILE_OPEN"); len(frames) != 0 {
				t.Fatalf("capability 없는 Connector가 FILE_OPEN을 받음: %v", frames)
			}
		})
	}
}

// 재접속한 connection이 file-v1을 선언하지 않으면 이전 connection의 선언을 쓰지 않는다.
func TestReconnectedConnectorMustDeclareFileV1Again(t *testing.T) {
	t.Parallel()
	fh := newFileHarness(t)
	fh.establishDeclaring(declare("file-v1"))
	if _, err := fh.openFile("file-1"); err != nil {
		t.Fatalf("SendFileOpen() error = %v", err)
	}

	// 같은 Credential의 새 connection이 이전 connection을 교체한다. file-v1을 선언하지 않는다.
	conn2, _, err := fh.dial(bearer(testCredential), protocol.SubprotocolControl)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	t.Cleanup(func() { _ = conn2.Close() })
	sendJSON(t, conn2, validHello())
	if ack := readJSON(t, conn2); ack["type"] != "HELLO_ACK" {
		t.Fatalf("첫 응답 = %v, want HELLO_ACK", ack)
	}
	waitFor(t, "새 Session의 capability 반영", func() bool {
		_, err := fh.openFile("file-2")
		return errors.Is(err, connector.ErrCapabilityUnsupported)
	})
}

func TestFileOpenResultRoundTripOverControlWSS(t *testing.T) {
	t.Parallel()
	fh := newFileHarness(t)
	p := fh.establishDeclaring(declare("file-v1"))

	sent, err := fh.openFile("file-1")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "FILE_OPEN", func() bool { return len(p.framesOfType("FILE_OPEN")) == 1 })

	p.send(fileOpenResult(sent.MessageID, "FAILED", map[string]any{}))
	// FAILED는 error가 필요하지 않다(file-control.schema.json: error optional). error를 실은 형태도 확인한다.
	event, ok := fh.events.waitCount(t, 1)[0].(connector.FileOpenResultEvent)
	if !ok {
		t.Fatalf("event = %+v", fh.events.all()[0])
	}
	if event.ConnectorID != fh.principal.ConnectorID || event.RequestMessageID != sent.MessageID || event.RequestID != "request-1" ||
		event.Correlation != (connector.FileCorrelation{FileRequestID: "file-1", LabInstanceID: "lab-1", Generation: 3}) ||
		event.Payload.Outcome != connector.FileOutcomeFailed {
		t.Fatalf("event = %+v", event)
	}
	if fh.router.PendingFileOpens(fh.principal.ConnectorID) != 0 {
		t.Fatal("결과를 받은 FILE_OPEN의 pending이 남음")
	}

	// FILE_CLOSE도 같은 Control connection으로 나가며 경로나 본문이 없다.
	if _, err := fh.router.SendFileClose(context.Background(), connector.FileClose{
		ConnectorID: fh.principal.ConnectorID, Correlation: event.Correlation, Reason: "REQUEST_CANCELED",
	}); err != nil {
		t.Fatalf("SendFileClose() error = %v", err)
	}
	waitFor(t, "FILE_CLOSE", func() bool { return len(p.framesOfType("FILE_CLOSE")) == 1 })
}

func TestFileOpenResultWithAnErrorDecodesTheSafeError(t *testing.T) {
	t.Parallel()
	fh := newFileHarness(t)
	p := fh.establishDeclaring(declare("file-v1"))
	sent, err := fh.openFile("file-1")
	if err != nil {
		t.Fatal(err)
	}
	msg := fileOpenResult(sent.MessageID, "FAILED", nil)
	payloadOf(msg)["error"] = map[string]any{"code": "UNAVAILABLE", "message": "vm unreachable"}
	p.send(msg)

	event := fh.events.waitCount(t, 1)[0].(connector.FileOpenResultEvent)
	if event.Payload.Error == nil || event.Payload.Error.Code != "UNAVAILABLE" || event.Payload.Error.Message != "vm unreachable" {
		t.Fatalf("event = %+v", event)
	}
}

// Schema를 만족하지 않는 FILE_OPEN_RESULT는 어떤 pending에도 넘기지 않는다. non-fatal ERROR만 보내고 연결과 pending은 유지한다.
func TestSchemaInvalidFileOpenResultIsRejectedAndKeepsThePending(t *testing.T) {
	t.Parallel()
	fh := newFileHarness(t)
	p := fh.establishDeclaring(declare("file-v1"))
	sent, err := fh.openFile("file-1")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		msg  map[string]any
	}{
		{"replyToMessageId 없음", fileOpenResult(sent.MessageID, "FAILED", map[string]any{"replyToMessageId": nil})},
		{"replyToMessageId 빈 문자열", fileOpenResult("", "FAILED", map[string]any{"replyToMessageId": ""})},
		{"fileRequestId 없음", fileOpenResult(sent.MessageID, "FAILED", map[string]any{"fileRequestId": nil})},
		{"fileRequestId 빈 문자열", fileOpenResult(sent.MessageID, "FAILED", map[string]any{"fileRequestId": ""})},
		{"fileRequestId가 문자열이 아님", fileOpenResult(sent.MessageID, "FAILED", map[string]any{"fileRequestId": 7})},
		{"labInstanceId 없음", fileOpenResult(sent.MessageID, "FAILED", map[string]any{"labInstanceId": nil})},
		{"generation 없음", fileOpenResult(sent.MessageID, "FAILED", map[string]any{"generation": nil})},
		{"generation 0", fileOpenResult(sent.MessageID, "FAILED", map[string]any{"generation": 0})},
		{"generation 문자열", fileOpenResult(sent.MessageID, "FAILED", map[string]any{"generation": "3"})},
		{"outcome 알 수 없음", fileOpenResult(sent.MessageID, "UNKNOWN", nil)},
		{"outcome 소문자", fileOpenResult(sent.MessageID, "failed", nil)},
		{"outcome 없음", fileMessage("FILE_OPEN_RESULT", map[string]any{}, map[string]any{"replyToMessageId": sent.MessageID})},
		{"error가 object가 아님", fileMessage("FILE_OPEN_RESULT", map[string]any{"outcome": "FAILED", "error": "x"}, map[string]any{"replyToMessageId": sent.MessageID})},
		{"error.code 없음", fileMessage("FILE_OPEN_RESULT", map[string]any{"outcome": "FAILED", "error": map[string]any{}}, map[string]any{"replyToMessageId": sent.MessageID})},
		{"payload가 object가 아님", fileOpenResult(sent.MessageID, "FAILED", map[string]any{"payload": "x"})},
		{"sentAt 형식 오류", fileOpenResult(sent.MessageID, "FAILED", map[string]any{"sentAt": "yesterday"})},
	}
	for _, tc := range cases {
		p.send(tc.msg)
	}
	p.sync()

	if events := fh.events.all(); len(events) != 0 {
		t.Fatalf("Schema 위반 message가 routing됨: %+v", events)
	}
	if fh.router.PendingFileOpens(fh.principal.ConnectorID) != 1 {
		t.Fatal("Schema 위반 message가 pending을 끝냈거나 지움")
	}
	if got := len(p.framesOfType("ERROR")); got != len(cases) {
		t.Fatalf("ERROR %d개, want %d개(message마다 non-fatal ERROR)", got, len(cases))
	}
	if p.closedNow() {
		t.Fatal("message 하나의 오류로 연결을 닫음")
	}

	// 연결과 pending이 유지되므로 올바른 결과는 그대로 연결된다.
	p.send(fileOpenResult(sent.MessageID, "FAILED", nil))
	fh.events.waitCount(t, 1)
}

// Schema는 만족하지만 pending과 맞지 않는 FILE_OPEN_RESULT는 ERROR 없이 unmatched로 알리고 pending을 유지한다.
func TestMismatchedFileOpenResultIsUnmatchedNotCompleted(t *testing.T) {
	t.Parallel()
	fh := newFileHarness(t)
	p := fh.establishDeclaring(declare("file-v1"))
	sent, err := fh.openFile("file-1")
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]map[string]any{
		"다른 fileRequestId":     fileOpenResult(sent.MessageID, "FAILED", map[string]any{"fileRequestId": "file-2"}),
		"다른 labInstanceId":     fileOpenResult(sent.MessageID, "FAILED", map[string]any{"labInstanceId": "lab-2"}),
		"다른 generation":        fileOpenResult(sent.MessageID, "FAILED", map[string]any{"generation": 2}),
		"다른 replyToMessageId":  fileOpenResult("not-the-open", "FAILED", nil),
		"int64를 넘는 generation": fileOpenResult(sent.MessageID, "FAILED", map[string]any{"generation": 1e30}),
	}
	for _, msg := range cases {
		p.send(msg)
	}
	p.sync()

	events := fh.events.waitCount(t, len(cases))
	for _, e := range events {
		if _, ok := e.(connector.FileUnmatchedEvent); !ok {
			t.Fatalf("맞지 않는 message가 연결됨: %+v", e)
		}
	}
	if fh.router.PendingFileOpens(fh.principal.ConnectorID) != 1 {
		t.Fatal("맞지 않는 message가 pending을 끝냈음")
	}
	if got := len(p.framesOfType("ERROR")); got != 0 {
		t.Fatalf("Schema-valid message에 ERROR %d개", got)
	}
}

// Router가 없는 구성에서는 File 결과를 해석하지 않고 버린다. 연결은 유지한다.
func TestFileOpenResultWithoutARouterIsIgnored(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	p := h.establish()
	h.waitReady(h.principal.ConnectorID)
	p.send(fileOpenResult("x", "FAILED", nil))
	p.sync()
	if p.closedNow() {
		t.Fatal("연결이 닫힘")
	}
}
