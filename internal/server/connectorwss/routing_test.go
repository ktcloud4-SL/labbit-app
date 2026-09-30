package connectorwss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
)

const (
	testTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	testUnsampled   = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00"
	testTracestate  = "vendor=opaque"
)

// eventLog는 Router가 넘긴 event를 순서대로 모은다. HandleEvent는 read loop goroutine에서 호출된다.
type eventLog struct {
	mu     sync.Mutex
	events []connector.Event
}

func (e *eventLog) HandleEvent(event connector.Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, event)
}

func (e *eventLog) all() []connector.Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]connector.Event(nil), e.events...)
}

// waitCount는 event가 n개 이상 모일 때까지 기다린다.
func (e *eventLog) waitCount(t *testing.T, n int) []connector.Event {
	t.Helper()
	waitFor(t, fmt.Sprintf("%d개 event", n), func() bool { return len(e.all()) >= n })
	return e.all()
}

// routedHarness는 Router가 연결된 harness다.
type routedHarness struct {
	*harness
	router *connector.Router
	events *eventLog
}

func newRoutedHarness(t *testing.T, mutate ...func(*Options)) *routedHarness {
	t.Helper()
	events := &eventLog{}
	rh := &routedHarness{events: events}
	withRouter := func(o *Options) {
		router, err := connector.NewRouter(connector.RouterOptions{Registry: o.Registry, Sink: events, Logger: o.Logger})
		if err != nil {
			t.Fatalf("NewRouter() error = %v", err)
		}
		rh.router = router
		o.Router = router
	}
	rh.harness = newHarness(t, append([]func(*Options){withRouter}, mutate...)...)
	return rh
}

// waitReady는 connectorID의 current Session이 protocol-ready(HELLO_ACK 후 route 등록)가 될 때까지 기다린다. 아무것도 보내지 않는다.
func (h *harness) waitReady(connectorID uuid.UUID) {
	h.t.Helper()
	waitFor(h.t, "protocol-ready route", func() bool {
		return h.registry.WithReadyRoute(connectorID, func(connector.Session, connector.Route) error { return nil }) == nil
	})
}

// commands는 peer가 받은 OPERATION_COMMAND frame이 n개 이상 모일 때까지 기다려 decode한다.
func (p *peer) commands(n int) []protocol.OperationCommandMessage {
	p.t.Helper()
	var out []protocol.OperationCommandMessage
	waitFor(p.t, fmt.Sprintf("OPERATION_COMMAND %d개", n), func() bool {
		out = p.commandFrames()
		return len(out) >= n
	})
	return out
}

func (p *peer) commandFrames() []protocol.OperationCommandMessage {
	var out []protocol.OperationCommandMessage
	for _, frame := range p.received() {
		var msg protocol.OperationCommandMessage
		if json.Unmarshal([]byte(frame), &msg) == nil && msg.Type == protocol.MessageTypeOperationCommand {
			out = append(out, msg)
		}
	}
	return out
}

func (p *peer) framesOfType(kind string) []string {
	var out []string
	for _, frame := range p.received() {
		var msg struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(frame), &msg) == nil && msg.Type == kind {
			out = append(out, frame)
		}
	}
	return out
}

// sync는 지금까지 보낸 frame을 서버 read loop가 모두 처리했음을 보장한다. ping은 frame 순서대로 읽을 때 pong을 받는다.
func (p *peer) sync() {
	p.t.Helper()
	before := p.pongs.Load()
	p.ping()
	waitFor(p.t, "pong", func() bool { return p.pongs.Load() > before })
}

// asConnector는 다른 Connector를 이 harness에 추가하고 그 Credential로 HELLO_ACK까지 마친 peer를 반환한다.
func (h *harness) asConnector(credential string, principal connector.Principal) *peer {
	h.t.Helper()
	h.auth.add(credential, principal)
	p := h.establishWith(credential)
	h.waitReady(principal.ConnectorID)
	return p
}

func provisionCommand(connectorID uuid.UUID, operationID, labInstanceID string, generation int64) connector.OperationCommand {
	return connector.OperationCommand{
		ConnectorID: connectorID,
		RequestID:   "request-" + labInstanceID,
		Correlation: connector.Correlation{OperationID: operationID, LabInstanceID: labInstanceID, Generation: generation},
		Payload: protocol.OperationCommandPayload{
			MutationType: protocol.MutationTypeProvision,
			CreationSnapshot: &protocol.CreationSnapshot{
				ProviderConnectionID: "provider-connection-1",
				VMs: []protocol.ResolvedVmSpec{{
					VMKey: "vm-1", Role: "workspace", InstanceIndex: 0, ImageID: "image-1", FlavorID: "flavor-1",
					FlavorSpec: &protocol.ResolvedFlavorSpec{VCPUs: 1, RAMMiB: 512, DiskGiB: 0},
				}},
				WorkspaceVMKey: "vm-1",
			},
		},
		Trace: connector.TraceContext{Traceparent: testTraceparent, Tracestate: testTracestate},
	}
}

func send(t *testing.T, h *routedHarness, cmd connector.OperationCommand) connector.SentMessage {
	t.Helper()
	sent, err := h.router.SendOperationCommand(context.Background(), cmd)
	if err != nil {
		t.Fatalf("SendOperationCommand() error = %v", err)
	}
	return sent
}

func pending(h *routedHarness, connectorID uuid.UUID) int {
	ops, _ := h.router.PendingCount(connectorID)
	return ops
}

// envelopeFor는 Connector가 command에 응답할 때의 공통 Envelope다. 실제 Connector처럼 requestId와 Trace를 그대로 돌려준다.
func envelopeFor(cmd protocol.OperationCommandMessage, kind string) map[string]any {
	msg := map[string]any{
		"type":          kind,
		"messageId":     uuid.NewString(),
		"sentAt":        time.Now().UTC().Format(time.RFC3339Nano),
		"operationId":   cmd.OperationID,
		"labInstanceId": cmd.LabInstanceID,
		"generation":    cmd.Generation,
	}
	if cmd.RequestID != "" {
		msg["requestId"] = cmd.RequestID
	}
	if cmd.TraceParent != "" {
		msg["traceparent"] = cmd.TraceParent
	}
	if cmd.TraceState != "" {
		msg["tracestate"] = cmd.TraceState
	}
	return msg
}

func ackFor(cmd protocol.OperationCommandMessage, accepted bool) map[string]any {
	msg := envelopeFor(cmd, protocol.MessageTypeOperationAck)
	msg["replyToMessageId"] = cmd.MessageID
	msg["payload"] = map[string]any{"accepted": accepted}
	return msg
}

func progressFor(cmd protocol.OperationCommandMessage, stage string) map[string]any {
	msg := envelopeFor(cmd, protocol.MessageTypeOperationProgress)
	msg["payload"] = map[string]any{"stage": stage}
	return msg
}

func resultFor(cmd protocol.OperationCommandMessage, outcome string) map[string]any {
	msg := envelopeFor(cmd, protocol.MessageTypeOperationResult)
	msg["replyToMessageId"] = cmd.MessageID
	msg["payload"] = map[string]any{"outcome": outcome, "providerResources": []any{
		map[string]any{"resourceType": "SERVER", "providerId": "srv-" + cmd.LabInstanceID, "generation": cmd.Generation, "observedState": "ACTIVE"},
	}}
	return msg
}

func TestNewRejectsRouterFromDifferentRegistry(t *testing.T) {
	router, err := connector.NewRouter(connector.RouterOptions{Registry: connector.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(Options{Auth: &fakeAuth{}, Heartbeats: &fakeHeartbeats{}, Registry: connector.NewRegistry(), Router: router})
	if err == nil {
		t.Fatal("다른 Registry의 Router로 handler가 만들어짐")
	}
}

// 인증과 Upgrade만 마친 current connection에는 command를 보내지 않는다. HELLO_ACK 뒤에야 그 connection으로 나간다.
func TestCommandIsNotSentUntilHelloAckCompletes(t *testing.T) {
	h := newRoutedHarness(t)
	p := h.upgradeWith(testCredential) // current지만 HELLO 전이다.
	h.waitRegistered()

	cmd := provisionCommand(h.principal.ConnectorID, "op-1", "lab-A", 1)
	if _, err := h.router.SendOperationCommand(context.Background(), cmd); !errors.Is(err, connector.ErrNotReady) {
		t.Fatalf("HELLO 전 전송 = %v, want ErrNotReady", err)
	}
	if pending(h, h.principal.ConnectorID) != 0 || len(p.received()) != 0 {
		t.Fatalf("HELLO 전에 frame이 나가거나 pending이 생김: frames %v, pending %d", p.received(), pending(h, h.principal.ConnectorID))
	}

	p.hello()
	h.waitReady(h.principal.ConnectorID)
	send(t, h, cmd)
	if got := p.commands(1); len(got) != 1 || got[0].OperationID != "op-1" {
		t.Fatalf("HELLO_ACK 뒤 command = %+v", got)
	}
}

// A가 ready인 상태에서 B가 Upgrade하면 A로는 더 보내지 않고 B는 HELLO_ACK 전까지 받지 못한다. B의 HELLO_ACK 뒤에는 B로만 나간다.
func TestReplacementBlocksBothSessionsUntilNewHelloAck(t *testing.T) {
	h := newRoutedHarness(t)
	a := h.establish()
	h.waitReady(h.principal.ConnectorID)
	before, _ := h.registry.Current(h.principal.ConnectorID)

	b := h.upgradeWith(testCredential) // A를 교체하지만 B는 HELLO 전이다.
	waitFor(t, "새 Session이 current", func() bool {
		s, ok := h.registry.Current(h.principal.ConnectorID)
		return ok && s != before
	})

	cmd := provisionCommand(h.principal.ConnectorID, "op-1", "lab-A", 1)
	if _, err := h.router.SendOperationCommand(context.Background(), cmd); !errors.Is(err, connector.ErrNotReady) {
		t.Fatalf("교체 직후 전송 = %v, want ErrNotReady", err)
	}
	a.waitClosed(5 * time.Second)
	if len(a.commandFrames()) != 0 || len(b.commandFrames()) != 0 || pending(h, h.principal.ConnectorID) != 0 {
		t.Fatalf("교체된 A(%v)나 HELLO 전 B(%v)로 나가거나 pending이 생김", a.received(), b.received())
	}

	b.hello()
	h.waitReady(h.principal.ConnectorID)
	send(t, h, cmd)
	if got := b.commands(1); len(got) != 1 {
		t.Fatalf("B command = %+v", got)
	}
	if len(a.commandFrames()) != 0 {
		t.Fatal("교체된 A가 command를 받음")
	}
}

// send와 replacement가 경쟁해도 성공한 send는 정확히 한 connection에 한 번만 나가고, HELLO 전의 B에는 나가지 않으며,
// 교체가 끝난 것을 관측한 뒤 시작한 send는 이전 connection으로 나가지 않는다.
func TestSendRacingWithReplacementNeverDuplicatesOrReachesUnreadySession(t *testing.T) {
	const rounds = 8
	for round := 0; round < rounds; round++ {
		h := newRoutedHarness(t)
		a := h.establish()
		h.waitReady(h.principal.ConnectorID)
		aSession, _ := h.registry.Current(h.principal.ConnectorID)

		type result struct {
			sent            connector.SentMessage
			startedAfterNew bool
		}
		var (
			mu          sync.Mutex
			results     []result
			newObserved = make(chan struct{})
			stop        = make(chan struct{})
			done        sync.WaitGroup
			counter     int64
		)
		done.Add(1)
		go func() {
			defer done.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				afterNew := false
				select {
				case <-newObserved:
					afterNew = true
				default:
				}
				counter++
				sent, err := h.router.SendOperationCommand(context.Background(),
					provisionCommand(h.principal.ConnectorID, "op-race", fmt.Sprintf("lab-%d", counter), 1))
				if err == nil {
					mu.Lock()
					results = append(results, result{sent, afterNew})
					mu.Unlock()
				} else if !errors.Is(err, connector.ErrConnectorUnavailable) {
					t.Errorf("예상하지 못한 전송 오류: %v", err)
					return
				}
			}
		}()

		time.Sleep(5 * time.Millisecond) // 전송이 몇 번 성공한 뒤 교체한다.
		b := h.upgradeWith(testCredential)
		waitFor(t, "새 Session이 current", func() bool {
			s, ok := h.registry.Current(h.principal.ConnectorID)
			return ok && s != aSession
		})
		close(newObserved)
		a.waitClosed(5 * time.Second)

		// B의 HELLO 전까지 B는 어떤 command도 받지 못한다.
		time.Sleep(20 * time.Millisecond)
		if got := b.commandFrames(); len(got) != 0 {
			t.Fatalf("round %d: HELLO 전 B가 command %d개를 받음", round, len(got))
		}
		b.hello()
		h.waitReady(h.principal.ConnectorID)
		close(stop)
		done.Wait()
		b.sync()

		onA, onB := map[string]int{}, map[string]int{}
		for _, m := range a.commandFrames() {
			onA[m.MessageID]++
		}
		for _, m := range b.commandFrames() {
			onB[m.MessageID]++
		}
		mu.Lock()
		for _, r := range results {
			id := r.sent.MessageID
			if onA[id]+onB[id] != 1 {
				t.Fatalf("round %d: 성공한 command %s가 A %d번, B %d번 전송됨, want 정확히 한 번", round, id, onA[id], onB[id])
			}
			if r.startedAfterNew && onA[id] != 0 {
				t.Fatalf("round %d: 교체 뒤에 시작한 command %s가 이전 connection A로 나감", round, id)
			}
		}
		if len(onA)+len(onB) != len(results) {
			t.Fatalf("round %d: 전송 실패로 보고한 command가 실제로 나감: frames %d, 성공 %d", round, len(onA)+len(onB), len(results))
		}
		mu.Unlock()
	}
}

// command는 인증된 Connector의 connection으로만 나간다. 다른 Connector의 connection으로 전달되지 않는다.
func TestCommandReachesOnlyTheTargetConnector(t *testing.T) {
	h := newRoutedHarness(t)
	a := h.establish()
	h.waitReady(h.principal.ConnectorID)
	other := connector.Principal{ConnectorID: uuid.New(), OrganizationID: uuid.New(), CredentialID: uuid.New()}
	b := h.asConnector("credential-of-connector-b-unique-77aa", other)

	send(t, h, provisionCommand(other.ConnectorID, "op-1", "lab-B", 1))
	if got := b.commands(1); len(got) != 1 || got[0].LabInstanceID != "lab-B" {
		t.Fatalf("B command = %+v", got)
	}
	a.sync()
	if got := a.commandFrames(); len(got) != 0 {
		t.Fatalf("다른 Connector의 command가 A에 전달됨: %+v", got)
	}
}

// 실제 wire로 ACK-2, PROGRESS-1, PROGRESS-2, RESULT-2, RESULT-1이 섞여 도착해도 각 event는 정확한 command로만 연결된다.
// 다른 Connector가 같은 ID를 보내도 들어오지 않는다. requestId와 Trace는 command에 실은 값이 event에 유지된다.
func TestInterleavedResponsesAreRoutedToTheirOwnCommands(t *testing.T) {
	h := newRoutedHarness(t)
	a := h.establish()
	h.waitReady(h.principal.ConnectorID)
	other := connector.Principal{ConnectorID: uuid.New(), OrganizationID: uuid.New(), CredentialID: uuid.New()}
	b := h.asConnector("credential-of-connector-b-unique-77aa", other)

	sent1 := send(t, h, provisionCommand(h.principal.ConnectorID, "op-1", "lab-A", 1))
	sent2 := send(t, h, provisionCommand(h.principal.ConnectorID, "op-1", "lab-B", 3))
	cmds := a.commands(2)
	byLab := map[string]protocol.OperationCommandMessage{}
	for _, c := range cmds {
		byLab[c.LabInstanceID] = c
	}
	c1, c2 := byLab["lab-A"], byLab["lab-B"]
	if c1.MessageID != sent1.MessageID || c2.MessageID != sent2.MessageID {
		t.Fatalf("wire messageId가 반환값과 다름: %q/%q vs %q/%q", c1.MessageID, c2.MessageID, sent1.MessageID, sent2.MessageID)
	}

	// Connector B가 A의 command와 같은 ID를 보낸다.
	b.send(progressFor(c1, "SPOOF"))
	b.sync()
	events := h.events.waitCount(t, 1)
	if u, ok := events[0].(connector.UnmatchedEvent); !ok || u.ConnectorID != other.ConnectorID || u.Reason != connector.ReasonNoPending {
		t.Fatalf("다른 Connector의 message = %+v, want unmatched", events[0])
	}

	a.send(ackFor(c2, true))
	a.send(progressFor(c1, "STAGE-1"))
	a.send(progressFor(c2, "STAGE-2"))
	a.send(resultFor(c2, protocol.OutcomeSucceeded))
	a.send(resultFor(c1, protocol.OutcomeFailed))
	events = h.events.waitCount(t, 6)[1:]

	type want struct {
		lab        string
		generation int64
		requestMsg string
		requestID  string
		detail     string
	}
	wants := []want{
		{"lab-B", 3, sent2.MessageID, "request-lab-B", "ack"},
		{"lab-A", 1, sent1.MessageID, "request-lab-A", "progress:STAGE-1"},
		{"lab-B", 3, sent2.MessageID, "request-lab-B", "progress:STAGE-2"},
		{"lab-B", 3, sent2.MessageID, "request-lab-B", "result:SUCCEEDED"},
		{"lab-A", 1, sent1.MessageID, "request-lab-A", "result:FAILED"},
	}
	for i, w := range wants {
		var routed connector.Routed
		var detail string
		switch e := events[i].(type) {
		case connector.OperationAckEvent:
			routed, detail = e.Routed, "ack"
		case connector.OperationProgressEvent:
			routed, detail = e.Routed, "progress:"+e.Payload.Stage
		case connector.OperationResultEvent:
			routed, detail = e.Routed, "result:"+e.Payload.Outcome
			if len(e.Payload.ProviderResources) != 1 || e.Payload.ProviderResources[0].ProviderID != "srv-"+w.lab {
				t.Fatalf("event %d의 ProviderResource = %+v", i, e.Payload.ProviderResources)
			}
		default:
			t.Fatalf("event %d = %T", i, events[i])
		}
		got := want{routed.Correlation.LabInstanceID, routed.Correlation.Generation, routed.RequestMessageID, routed.RequestID, detail}
		if got != w || routed.ConnectorID != h.principal.ConnectorID || routed.Correlation.OperationID != "op-1" {
			t.Fatalf("event %d = %+v (connector %s, op %s), want %+v", i, got, routed.ConnectorID, routed.Correlation.OperationID, w)
		}
		if routed.Trace.Traceparent != testTraceparent || routed.Trace.Tracestate != testTracestate {
			t.Fatalf("event %d의 Trace = %+v, want command의 유효한 Context 유지", i, routed.Trace)
		}
	}
	if pending(h, h.principal.ConnectorID) != 0 {
		t.Fatalf("모두 끝난 뒤 pending = %d", pending(h, h.principal.ConnectorID))
	}
}

// Trace는 업무 성공 조건이 아니다. 잘못된 Trace field는 그 field만 버리고 message는 정상 routing한다.
func TestInvalidInboundTraceIsDiscardedWithoutRejectingTheMessage(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(msg map[string]any)
		wantParent string
		wantState  string
	}{
		{"valid", func(map[string]any) {}, testTraceparent, testTracestate},
		{"not sampled preserved", func(m map[string]any) { m["traceparent"] = testUnsampled }, testUnsampled, testTracestate},
		{"invalid traceparent drops tracestate", func(m map[string]any) { m["traceparent"] = "garbage" }, "", ""},
		{"traceparent wrong type", func(m map[string]any) { m["traceparent"] = 12345 }, "", ""},
		{"traceparent too long", func(m map[string]any) { m["traceparent"] = testTraceparent + strings.Repeat("0", 600) }, "", ""},
		{"invalid tracestate only", func(m map[string]any) { m["tracestate"] = "not valid" }, testTraceparent, ""},
		{"tracestate wrong type", func(m map[string]any) { m["tracestate"] = []string{"a"} }, testTraceparent, ""},
		{"absent", func(m map[string]any) { delete(m, "traceparent"); delete(m, "tracestate") }, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel() // 각 case가 독립된 harness를 쓴다.
			h := newRoutedHarness(t)
			p := h.establish()
			h.waitReady(h.principal.ConnectorID)
			send(t, h, provisionCommand(h.principal.ConnectorID, "op-1", "lab-A", 1))
			cmd := p.commands(1)[0]

			msg := resultFor(cmd, protocol.OutcomeSucceeded)
			tt.mutate(msg)
			p.send(msg)
			event, ok := h.events.waitCount(t, 1)[0].(connector.OperationResultEvent)
			if !ok {
				t.Fatalf("Trace 문제로 결과가 routing되지 않음: %+v", h.events.all())
			}
			if event.Trace.Traceparent != tt.wantParent || event.Trace.Tracestate != tt.wantState {
				t.Fatalf("event Trace = %+v, want {%q %q}", event.Trace, tt.wantParent, tt.wantState)
			}
			p.sync()
			if got := p.framesOfType(protocol.MessageTypeError); len(got) != 0 {
				t.Fatalf("Trace 문제로 ERROR를 보냄: %v", got)
			}
		})
	}
}

// Schema-invalid 응답은 어떤 pending에도 연결하지 않고 non-fatal ERROR만 받는다. 입력 값은 되돌려주지 않고 연결과 pending은 유지된다.
// Schema의 property 이름은 대소문자를 구분한다.
func TestSchemaInvalidResponsesAreRejectedNonFatallyAndNeverRouted(t *testing.T) {
	type mutation struct {
		name  string
		build func(cmd protocol.OperationCommandMessage) map[string]any
	}
	ack := func(mut func(m map[string]any)) func(protocol.OperationCommandMessage) map[string]any {
		return func(c protocol.OperationCommandMessage) map[string]any { m := ackFor(c, true); mut(m); return m }
	}
	progress := func(mut func(m map[string]any)) func(protocol.OperationCommandMessage) map[string]any {
		return func(c protocol.OperationCommandMessage) map[string]any { m := progressFor(c, "S"); mut(m); return m }
	}
	result := func(mut func(m map[string]any)) func(protocol.OperationCommandMessage) map[string]any {
		return func(c protocol.OperationCommandMessage) map[string]any {
			m := resultFor(c, protocol.OutcomeSucceeded)
			mut(m)
			return m
		}
	}
	payloadOf := func(m map[string]any) map[string]any { return m["payload"].(map[string]any) }
	item := func(m map[string]any) map[string]any {
		return payloadOf(m)["providerResources"].([]any)[0].(map[string]any)
	}
	reconcile := func(mut func(m map[string]any)) func(protocol.OperationCommandMessage) map[string]any {
		return func(c protocol.OperationCommandMessage) map[string]any {
			m := envelopeFor(c, protocol.MessageTypeReconcileResult)
			m["replyToMessageId"] = c.MessageID
			m["payload"] = map[string]any{"observations": []any{
				map[string]any{"resourceType": "SERVER", "providerId": "srv-1", "exists": true, "source": "KNOWN_RESOURCE"},
			}}
			mut(m)
			return m
		}
	}
	observation := func(m map[string]any) map[string]any {
		return payloadOf(m)["observations"].([]any)[0].(map[string]any)
	}

	tests := []mutation{
		// OPERATION_ACK
		{"ack missing replyToMessageId", ack(func(m map[string]any) { delete(m, "replyToMessageId") })},
		{"ack missing operationId", ack(func(m map[string]any) { delete(m, "operationId") })},
		{"ack empty labInstanceId", ack(func(m map[string]any) { m["labInstanceId"] = "" })},
		{"ack missing generation", ack(func(m map[string]any) { delete(m, "generation") })},
		{"ack generation zero", ack(func(m map[string]any) { m["generation"] = 0 })},
		{"ack generation negative", ack(func(m map[string]any) { m["generation"] = -1 })},
		{"ack generation string", ack(func(m map[string]any) { m["generation"] = "1" })},
		{"ack generation fractional", ack(func(m map[string]any) { m["generation"] = 1.5 })},
		{"ack missing accepted", ack(func(m map[string]any) { payloadOf(m)["accepted"] = nil; delete(payloadOf(m), "accepted") })},
		{"ack accepted string", ack(func(m map[string]any) { payloadOf(m)["accepted"] = "true" })},
		{"ack accepted null", ack(func(m map[string]any) { payloadOf(m)["accepted"] = nil })},
		{"ack wrong-case property name", ack(func(m map[string]any) { delete(payloadOf(m), "accepted"); payloadOf(m)["ACCEPTED"] = true })},
		{"ack wrong-case correlation name", ack(func(m map[string]any) { m["OperationId"] = m["operationId"]; delete(m, "operationId") })},
		{"ack error not object", ack(func(m map[string]any) { payloadOf(m)["error"] = "boom" })},
		{"ack error without code", ack(func(m map[string]any) { payloadOf(m)["error"] = map[string]any{"message": "x"} })},
		{"ack error empty code", ack(func(m map[string]any) { payloadOf(m)["error"] = map[string]any{"code": ""} })},
		{"ack payload not object", ack(func(m map[string]any) { m["payload"] = "x" })},
		{"ack missing messageId", ack(func(m map[string]any) { delete(m, "messageId") })},
		{"ack invalid sentAt", ack(func(m map[string]any) { m["sentAt"] = "yesterday" })},
		{"ack empty requestId", ack(func(m map[string]any) { m["requestId"] = "" })},
		// OPERATION_PROGRESS
		{"progress missing stage", progress(func(m map[string]any) { delete(payloadOf(m), "stage") })},
		{"progress empty stage", progress(func(m map[string]any) { payloadOf(m)["stage"] = "" })},
		{"progress stage not string", progress(func(m map[string]any) { payloadOf(m)["stage"] = 7 })},
		{"progress missing labInstanceId", progress(func(m map[string]any) { delete(m, "labInstanceId") })},
		{"progress empty replyToMessageId when present", progress(func(m map[string]any) { m["replyToMessageId"] = "" })},
		// OPERATION_RESULT
		{"result invalid outcome", result(func(m map[string]any) { payloadOf(m)["outcome"] = "DONE" })},
		{"result lower-case outcome", result(func(m map[string]any) { payloadOf(m)["outcome"] = "succeeded" })},
		{"result missing outcome", result(func(m map[string]any) { delete(payloadOf(m), "outcome") })},
		{"result missing providerResources", result(func(m map[string]any) { delete(payloadOf(m), "providerResources") })},
		{"result providerResources null", result(func(m map[string]any) { payloadOf(m)["providerResources"] = nil })},
		{"result providerResources not array", result(func(m map[string]any) { payloadOf(m)["providerResources"] = map[string]any{} })},
		{"result resource not object", result(func(m map[string]any) { payloadOf(m)["providerResources"] = []any{"srv"} })},
		{"result resource missing providerId", result(func(m map[string]any) { delete(item(m), "providerId") })},
		{"result resource missing generation", result(func(m map[string]any) { delete(item(m), "generation") })},
		{"result resource generation zero", result(func(m map[string]any) { item(m)["generation"] = 0 })},
		{"result resource generation fractional", result(func(m map[string]any) { item(m)["generation"] = 2.5 })},
		{"result resource observedState not string", result(func(m map[string]any) { item(m)["observedState"] = 1 })},
		{"result error invalid", result(func(m map[string]any) { payloadOf(m)["error"] = []any{} })},
		// RECONCILE_RESULT (routing 전 검증이므로 command를 가리키는 replyTo 대신 fixture를 그대로 쓴다)
		{"reconcile missing replyToMessageId", reconcile(func(m map[string]any) { delete(m, "replyToMessageId") })},
		{"reconcile missing observations", reconcile(func(m map[string]any) { delete(payloadOf(m), "observations") })},
		{"reconcile observation missing exists", reconcile(func(m map[string]any) { delete(observation(m), "exists") })},
		{"reconcile observation invalid source", reconcile(func(m map[string]any) { observation(m)["source"] = "GUESS" })},
		{"reconcile observation generation zero", reconcile(func(m map[string]any) { observation(m)["generation"] = 0 })},
		{"reconcile observation empty providerId", reconcile(func(m map[string]any) { observation(m)["providerId"] = "" })},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel() // 각 case가 독립된 harness를 쓴다.
			h := newRoutedHarness(t)
			p := h.establish()
			h.waitReady(h.principal.ConnectorID)
			send(t, h, provisionCommand(h.principal.ConnectorID, "op-1", "lab-A", 1))
			cmd := p.commands(1)[0]

			msg := tt.build(cmd)
			// 입력 값이 ERROR에 되돌아오지 않는지 보기 위한 표식을 검증 대상이 아닌 field에 싣는다.
			msg["unknownFutureField"] = echoCheckMarker
			p.send(msg)
			p.sync()

			if events := h.events.all(); len(events) != 0 {
				t.Fatalf("Schema-invalid message가 event로 전달됨: %+v", events)
			}
			frames := p.framesOfType(protocol.MessageTypeError)
			if len(frames) != 1 {
				t.Fatalf("ERROR frame %d개 = %v, want 1개", len(frames), frames)
			}
			var errMsg protocol.ProtocolErrorMessage
			if err := json.Unmarshal([]byte(frames[0]), &errMsg); err != nil {
				t.Fatal(err)
			}
			if errMsg.Payload.Code != errorCodeInvalidMessage || errMsg.Payload.Fatal || errMsg.ReplyToMessageID != "" {
				t.Fatalf("ERROR = %+v, want non-fatal INVALID_MESSAGE(입력 값 없음)", errMsg)
			}
			if strings.Contains(frames[0], echoCheckMarker) || strings.Contains(frames[0], cmd.MessageID) || strings.Contains(frames[0], "lab-A") {
				t.Fatalf("ERROR가 입력 값을 되돌려줌: %s", frames[0])
			}
			if strings.Contains(h.logs.String(), echoCheckMarker) {
				t.Fatalf("log가 입력 값을 남김: %s", h.logs.String())
			}
			if p.closedNow() {
				t.Fatal("message 하나가 잘못됐다는 이유로 연결이 끊김")
			}
			if pending(h, h.principal.ConnectorID) != 1 {
				t.Fatalf("pending = %d, want 1(어느 pending도 소비하지 않는다)", pending(h, h.principal.ConnectorID))
			}

			// 연결은 그대로 쓸 수 있다. 올바른 응답은 계속 연결된다.
			p.send(resultFor(cmd, protocol.OutcomeSucceeded))
			if _, ok := h.events.waitCount(t, 1)[0].(connector.OperationResultEvent); !ok {
				t.Fatalf("잘못된 message 뒤 정상 결과가 연결되지 않음: %+v", h.events.all())
			}
		})
	}
}

// 판단에 쓰는 값은 정확한 property 이름에서 읽는다. 대소문자만 다른 key가 정확한 key의 값을 덮어쓰지 못한다.
func TestExactCasePropertyIsAuthoritativeOverCaseVariants(t *testing.T) {
	h := newRoutedHarness(t)
	p := h.establish()
	h.waitReady(h.principal.ConnectorID)
	send(t, h, provisionCommand(h.principal.ConnectorID, "op-1", "lab-A", 1))
	cmd := p.commands(1)[0]

	msg := resultFor(cmd, protocol.OutcomeSucceeded)
	msg["OPERATIONID"] = "op-other" // 대소문자만 다른 key는 무시한다.
	msg["LabInstanceId"] = "lab-other"
	msg["payload"].(map[string]any)["OUTCOME"] = protocol.OutcomeFailed
	msg["payload"].(map[string]any)["PROVIDERRESOURCES"] = []any{}
	p.send(msg)

	event, ok := h.events.waitCount(t, 1)[0].(connector.OperationResultEvent)
	if !ok {
		t.Fatalf("event = %+v", h.events.all())
	}
	if event.Correlation.OperationID != "op-1" || event.Correlation.LabInstanceID != "lab-A" ||
		event.Payload.Outcome != protocol.OutcomeSucceeded || len(event.Payload.ProviderResources) != 1 {
		t.Fatalf("대소문자만 다른 key가 값을 덮어씀: %+v", event)
	}
}

// generation은 Schema에 maximum이 없는 integer다. 1.0, 1e0 같은 표기는 같은 값으로 연결하고, int64를 넘는 값은 Schema-invalid가 아니라
// 어떤 pending과도 맞지 않는 unmatched이며 ERROR나 연결 종료 없이 pending은 유지된다.
func TestGenerationIsLexicalIntegerNotInt64WireLimit(t *testing.T) {
	h := newRoutedHarness(t)
	p := h.establish()
	h.waitReady(h.principal.ConnectorID)
	send(t, h, provisionCommand(h.principal.ConnectorID, "op-1", "lab-A", 1))
	cmd := p.commands(1)[0]

	raw := func(generation string) []byte {
		return fmt.Appendf(nil, `{"type":"OPERATION_PROGRESS","messageId":%q,"sentAt":%q,"operationId":"op-1","labInstanceId":"lab-A","generation":%s,"payload":{"stage":"S"}}`,
			uuid.NewString(), time.Now().UTC().Format(time.RFC3339Nano), generation)
	}
	for _, generation := range []string{"1", "1.0", "1e0", "10e-1", "100e-2", "0.1e1"} {
		if err := p.conn.WriteMessage(websocket.TextMessage, raw(generation)); err != nil {
			t.Fatal(err)
		}
	}
	events := h.events.waitCount(t, 6)
	for i, event := range events {
		if e, ok := event.(connector.OperationProgressEvent); !ok || e.Correlation.Generation != 1 || e.RequestMessageID != cmd.MessageID {
			t.Fatalf("generation 표기 %d의 event = %+v, want generation 1의 command에 연결", i, event)
		}
	}

	for _, generation := range []string{"9223372036854775808", "123456789012345678901234567890", "1e400", "1e999999999999999999999"} {
		if err := p.conn.WriteMessage(websocket.TextMessage, raw(generation)); err != nil {
			t.Fatal(err)
		}
	}
	events = h.events.waitCount(t, 10)[6:]
	for i, event := range events {
		if u, ok := event.(connector.UnmatchedEvent); !ok || u.Reason != connector.ReasonUnrepresentable || u.Generation != 0 {
			t.Fatalf("큰 generation %d의 event = %+v, want unrepresentable unmatched", i, event)
		}
	}
	p.sync()
	if got := p.framesOfType(protocol.MessageTypeError); len(got) != 0 || p.closedNow() {
		t.Fatalf("Schema-valid한 큰 generation이 거절됨: ERROR %v, closed %v", got, p.closedNow())
	}
	if pending(h, h.principal.ConnectorID) != 1 {
		t.Fatalf("pending = %d, want 1", pending(h, h.principal.ConnectorID))
	}

	// 결과 안의 ProviderResource generation이 int64를 넘어도 같은 방식이다. pending은 끝나지 않는다.
	msg := resultFor(cmd, protocol.OutcomeSucceeded)
	msg["payload"].(map[string]any)["providerResources"].([]any)[0].(map[string]any)["generation"] = json.Number("123456789012345678901234567890")
	p.send(msg)
	events = h.events.waitCount(t, 11)[10:]
	if u, ok := events[0].(connector.UnmatchedEvent); !ok || u.Reason != connector.ReasonUnrepresentable || u.MessageType != protocol.MessageTypeOperationResult {
		t.Fatalf("event = %+v", events[0])
	}
	p.sync()
	if pending(h, h.principal.ConnectorID) != 1 || len(p.framesOfType(protocol.MessageTypeError)) != 0 {
		t.Fatal("표현할 수 없는 결과가 pending을 끝내거나 ERROR를 유발함")
	}
}

// 지원하지 않는 type, 다른 방향의 message, 알 수 없는 미래 type은 연결과 기존 pending을 건드리지 않고 ERROR도 만들지 않는다.
func TestUnsupportedMessagesDoNotDisturbConnectionOrPending(t *testing.T) {
	h := newRoutedHarness(t)
	p := h.establish()
	h.waitReady(h.principal.ConnectorID)
	send(t, h, provisionCommand(h.principal.ConnectorID, "op-1", "lab-A", 1))
	cmd := p.commands(1)[0]
	now := time.Now().UTC().Format(time.RFC3339Nano)

	for _, kind := range []string{
		protocol.MessageTypeProviderResponse, protocol.MessageTypeTerminalOpenResult, protocol.MessageTypeTerminalEnded,
		protocol.MessageTypeOperationCommand, protocol.MessageTypeReconcileRequest, protocol.MessageTypeHelloAck,
		protocol.MessageTypeHello, "FUTURE_TYPE_V2",
	} {
		p.send(map[string]any{
			"type": kind, "messageId": uuid.NewString(), "sentAt": now, "operationId": "op-1", "labInstanceId": "lab-A", "generation": 1,
			"replyToMessageId": cmd.MessageID, "payload": map[string]any{"accepted": true, "outcome": "SUCCEEDED", "providerResources": []any{}},
		})
	}
	_ = p.conn.WriteMessage(websocket.TextMessage, []byte("not json"))
	_ = p.conn.WriteMessage(websocket.BinaryMessage, []byte{1, 2, 3})
	p.sync()

	if events := h.events.all(); len(events) != 0 {
		t.Fatalf("지원하지 않는 message가 event로 전달됨: %+v", events)
	}
	if got := p.framesOfType(protocol.MessageTypeError); len(got) != 0 {
		t.Fatalf("지원하지 않는 message에 ERROR를 보냄: %v", got)
	}
	if p.closedNow() || pending(h, h.principal.ConnectorID) != 1 {
		t.Fatalf("connection closed %v, pending %d", p.closedNow(), pending(h, h.principal.ConnectorID))
	}
}

// Connector의 ERROR는 업무 결과가 아니다. pending을 끝내지 않고 event도 만들지 않으며 log에는 안전한 code만 남는다.
func TestInboundErrorIsNotABusinessResultAndLogsOnlySafeCode(t *testing.T) {
	h := newRoutedHarness(t)
	p := h.establish()
	h.waitReady(h.principal.ConnectorID)
	send(t, h, provisionCommand(h.principal.ConnectorID, "op-1", "lab-A", 1))
	cmd := p.commands(1)[0]
	const rawMessage = "raw-internal-error-detail-marker-3f9d"

	for _, code := range []string{"UNSUPPORTED_MESSAGE_TYPE", "not a safe code " + echoCheckMarker} {
		p.send(map[string]any{
			"type": protocol.MessageTypeError, "messageId": uuid.NewString(), "sentAt": time.Now().UTC().Format(time.RFC3339Nano),
			"replyToMessageId": cmd.MessageID, "operationId": "op-1", "labInstanceId": "lab-A", "generation": 1,
			"payload": map[string]any{"code": code, "message": rawMessage},
		})
	}
	p.sync()

	logs := h.logs.String()
	if !strings.Contains(logs, "UNSUPPORTED_MESSAGE_TYPE") || !strings.Contains(logs, "INVALID") {
		t.Fatalf("ERROR code가 log에 없음: %s", logs)
	}
	if strings.Contains(logs, rawMessage) || strings.Contains(logs, echoCheckMarker) {
		t.Fatalf("log에 Connector의 ERROR 문구가 남음: %s", logs)
	}
	if len(h.events.all()) != 0 || pending(h, h.principal.ConnectorID) != 1 || p.closedNow() {
		t.Fatalf("ERROR가 pending/연결에 영향을 줌: events %d, pending %d", len(h.events.all()), pending(h, h.principal.ConnectorID))
	}
}

// 유효한 command 응답 message도 offline deadline을 연장하지 않는다. 오직 유효한 HEARTBEAT만 연장한다.
func TestBusinessMessagesDoNotExtendOfflineDeadline(t *testing.T) {
	h := newRoutedHarness(t, shortHeartbeat)
	p := h.establish()
	h.waitReady(h.principal.ConnectorID)
	send(t, h, provisionCommand(h.principal.ConnectorID, "op-1", "lab-A", 1))
	cmd := p.commands(1)[0]

	stop := make(chan struct{})
	sending := make(chan struct{})
	go func() {
		defer close(sending)
		ticker := time.NewTicker(testHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				_ = p.conn.WriteJSON(progressFor(cmd, "STILL_WORKING"))
			}
		}
	}()
	closeErr := p.waitClosed(5 * time.Second)
	close(stop)
	<-sending

	if closeErr.Code != websocket.CloseGoingAway || closeErr.Text != "heartbeat timeout" {
		t.Fatalf("close = %d %q, want 1001 heartbeat timeout", closeErr.Code, closeErr.Text)
	}
	if got := h.beats.recorded(); len(got) != 0 {
		t.Fatalf("PROGRESS가 heartbeat로 기록됨: %+v", got)
	}
}

// 교체된 이전 connection이 뒤늦게 보낸 ACK/PROGRESS/RESULT는 routing하지 않는다. 새 current connection의 message는 연결된다.
func TestReplacedSessionResponsesAreNotRouted(t *testing.T) {
	h := newRoutedHarness(t)
	a := h.establish()
	h.waitReady(h.principal.ConnectorID)
	send(t, h, provisionCommand(h.principal.ConnectorID, "op-1", "lab-A", 1))
	cmd := a.commands(1)[0]

	b := h.establishWith(testCredential) // A를 교체한다.
	h.waitReady(h.principal.ConnectorID)

	// 이전 connection은 4002 종료 요청을 받은 상태에서도 write는 할 수 있다. 여러 번 보내 처리 순서와 무관하게 확인한다.
	for range 5 {
		_ = a.conn.WriteJSON(ackFor(cmd, true))
		_ = a.conn.WriteJSON(progressFor(cmd, "STALE"))
		_ = a.conn.WriteJSON(resultFor(cmd, protocol.OutcomeSucceeded))
	}
	if closeErr := a.waitClosed(5 * time.Second); closeErr.Code != closeReplaced {
		t.Fatalf("A close code = %d, want %d", closeErr.Code, closeReplaced)
	}
	b.sync()
	if events := h.events.all(); len(events) != 0 {
		t.Fatalf("교체된 Session의 message가 routing됨: %+v", events)
	}
	if pending(h, h.principal.ConnectorID) != 1 {
		t.Fatalf("stale message가 pending을 끝냄: %d", pending(h, h.principal.ConnectorID))
	}

	// 재접속한 current connection에서는 같은 pending의 늦은 결과가 연결된다. 그 사이 command는 다시 전송되지 않았다.
	if got := b.commandFrames(); len(got) != 0 {
		t.Fatalf("재접속한 B가 command를 다시 받음: %+v", got)
	}
	b.send(resultFor(cmd, protocol.OutcomeSucceeded))
	if e, ok := h.events.waitCount(t, 1)[0].(connector.OperationResultEvent); !ok || e.RequestMessageID != cmd.MessageID {
		t.Fatalf("event = %+v", h.events.all())
	}
	if len(a.commandFrames()) != 1 {
		t.Fatalf("A가 받은 command = %d, want 1", len(a.commandFrames()))
	}
}

// 교체·revoke된 Registration의 message는 fence에서 막힌다. 교체가 끝난 뒤 시작하는 routing을 결정적으로 재현한다.
func TestRouteInboundFenceRejectsRetiredSession(t *testing.T) {
	registry := connector.NewRegistry()
	events := &eventLog{}
	var logs syncBuffer
	router, err := connector.NewRouter(connector.RouterOptions{Registry: registry, Sink: events})
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
	sent, err := router.SendOperationCommand(context.Background(), provisionCommand(principal.ConnectorID, "op-1", "lab-A", 1))
	if err != nil {
		t.Fatal(err)
	}

	envelope := map[string]json.RawMessage{}
	raw, _ := json.Marshal(map[string]any{
		"type": protocol.MessageTypeOperationResult, "messageId": "r-1", "sentAt": time.Now().UTC().Format(time.RFC3339Nano),
		"replyToMessageId": sent.MessageID, "operationId": "op-1", "labInstanceId": "lab-A", "generation": 1,
		"payload": map[string]any{"outcome": "SUCCEEDED", "providerResources": []any{}},
	})
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	log := newTestLogger(&logs)

	registry.Register(principal, nil) // 교체가 끝났다. old는 더 이상 current가 아니다.
	h.routeInbound(cc, old, principal, log, protocol.MessageTypeOperationResult, envelope)
	if len(events.all()) != 0 {
		t.Fatalf("교체된 Session의 message가 routing됨: %+v", events.all())
	}
	if ops, _ := router.PendingCount(principal.ConnectorID); ops != 1 {
		t.Fatalf("pending = %d, want 1", ops)
	}

	// 같은 message를 current Registration으로 보내면 연결된다(대조군).
	current := registry.Register(principal, nil)
	h.routeInbound(cc, current, principal, log, protocol.MessageTypeOperationResult, envelope)
	if len(events.all()) != 1 {
		t.Fatalf("current Session의 message가 routing되지 않음: %+v", events.all())
	}
}

// command 전송 뒤 연결이 끊기고 Connector가 재접속해도 같은 command를 다시 보내지 않는다.
// 재접속한 connection으로 도착한 늦은 결과는 pending이 남아 있으므로 연결된다. Mock Connector(raw peer)로 frame 수를 직접 센다.
func TestReconnectIsNotRetryAndLateResultRoutesOnCurrentConnection(t *testing.T) {
	h := newRoutedHarness(t)
	a := h.establish()
	h.waitReady(h.principal.ConnectorID)
	first := send(t, h, provisionCommand(h.principal.ConnectorID, "op-1", "lab-A", 1))
	cmdA := a.commands(1)[0]
	if cmdA.MessageID != first.MessageID {
		t.Fatalf("messageId = %q, want %q", cmdA.MessageID, first.MessageID)
	}

	_ = a.conn.Close() // 단절
	registryReleased(t, h.harness)

	b := h.establishWith(testCredential) // 재접속
	h.waitReady(h.principal.ConnectorID)
	b.sync()
	if got := b.commandFrames(); len(got) != 0 {
		t.Fatalf("재접속 직후 command가 자동 재전송됨: %+v", got)
	}
	if pending(h, h.principal.ConnectorID) != 1 {
		t.Fatalf("재접속이 pending을 지움: %d", pending(h, h.principal.ConnectorID))
	}

	// 재접속 뒤 다른 command를 보내면 그 command만 나가고, 이전 command는 여전히 다시 나가지 않는다.
	send(t, h, provisionCommand(h.principal.ConnectorID, "op-2", "lab-B", 1))
	got := b.commands(1)
	b.sync()
	if got = b.commandFrames(); len(got) != 1 || got[0].OperationID != "op-2" {
		t.Fatalf("B가 받은 command = %+v, want op-2 하나뿐", got)
	}

	b.send(ackFor(cmdA, true))
	b.send(resultFor(cmdA, protocol.OutcomeUnknown))
	events := h.events.waitCount(t, 2)
	ack, ok1 := events[0].(connector.OperationAckEvent)
	res, ok2 := events[1].(connector.OperationResultEvent)
	if !ok1 || !ok2 || ack.RequestMessageID != first.MessageID || res.RequestMessageID != first.MessageID || res.Payload.Outcome != protocol.OutcomeUnknown {
		t.Fatalf("늦은 결과 events = %+v", events)
	}
	b.sync()
	if got := b.commandFrames(); len(got) != 1 {
		t.Fatalf("UNKNOWN 결과가 command를 다시 보냄: %+v", got)
	}
	if got := a.commandFrames(); len(got) != 1 {
		t.Fatalf("A command = %d, want 1", len(got))
	}
}

// revoke된 Connector에는 보낼 수 없다. pending은 자동으로 정리되거나 다른 곳으로 옮겨지지 않는다.
func TestRevokeStopsCommandRouting(t *testing.T) {
	h := newRoutedHarness(t)
	p := h.establish()
	h.waitReady(h.principal.ConnectorID)
	send(t, h, provisionCommand(h.principal.ConnectorID, "op-1", "lab-A", 1))
	p.commands(1)

	h.registry.RevokeConnector(h.principal.ConnectorID)
	if closeErr := p.waitClosed(5 * time.Second); closeErr.Code != closeCredentialRevoked {
		t.Fatalf("close code = %d, want %d", closeErr.Code, closeCredentialRevoked)
	}
	if _, err := h.router.SendOperationCommand(context.Background(), provisionCommand(h.principal.ConnectorID, "op-2", "lab-B", 1)); !errors.Is(err, connector.ErrNotConnected) {
		t.Fatalf("revoke 뒤 전송 = %v, want ErrNotConnected", err)
	}
	if pending(h, h.principal.ConnectorID) != 1 {
		t.Fatalf("pending = %d, want 1(호출자가 명시적으로 정리)", pending(h, h.principal.ConnectorID))
	}
}

// RECONCILE_REQUEST/RESULT를 실제 wire로 연결한다. replyToMessageId와 correlation이 모두 맞아야 한다.
func TestReconcileRoundTripOverWSS(t *testing.T) {
	h := newRoutedHarness(t)
	p := h.establish()
	h.waitReady(h.principal.ConnectorID)

	sent, err := h.router.SendReconcileRequest(context.Background(), connector.ReconcileRequest{
		ConnectorID: h.principal.ConnectorID,
		RequestID:   "request-reconcile",
		Correlation: connector.Correlation{OperationID: "op-1", LabInstanceID: "lab-A", Generation: 2},
		Payload:     protocol.ReconcileRequestPayload{KnownResources: []protocol.ProviderResourceRef{{ResourceType: "SERVER", ProviderID: "srv-1", Generation: 2}}},
		Trace:       connector.TraceContext{Traceparent: testTraceparent},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "RECONCILE_REQUEST", func() bool { return len(p.framesOfType(protocol.MessageTypeReconcileRequest)) == 1 })
	var req protocol.ReconcileRequestMessage
	if err := json.Unmarshal([]byte(p.framesOfType(protocol.MessageTypeReconcileRequest)[0]), &req); err != nil {
		t.Fatal(err)
	}
	if req.MessageID != sent.MessageID || req.TraceParent != testTraceparent {
		t.Fatalf("request = %+v", req)
	}

	reply := func(replyTo string, generation int64) map[string]any {
		return map[string]any{
			"type": protocol.MessageTypeReconcileResult, "messageId": uuid.NewString(), "sentAt": time.Now().UTC().Format(time.RFC3339Nano),
			"replyToMessageId": replyTo, "operationId": "op-1", "labInstanceId": "lab-A", "generation": generation, "traceparent": req.TraceParent,
			"payload": map[string]any{"observations": []any{
				map[string]any{"resourceType": "SERVER", "providerId": "srv-1", "generation": 2, "exists": true, "observedState": "ACTIVE", "source": "KNOWN_RESOURCE"},
				map[string]any{"resourceType": "SERVER", "providerId": "srv-orphan", "exists": true, "source": "DISCOVERED_CANDIDATE"},
			}},
		}
	}
	p.send(reply("some-other-request", 2)) // 잘못된 replyTo
	p.send(reply(req.MessageID, 3))        // 잘못된 generation
	events := h.events.waitCount(t, 2)
	for i, want := range []connector.UnmatchedReason{connector.ReasonNoPending, connector.ReasonCorrelationMismatch} {
		if u, ok := events[i].(connector.UnmatchedEvent); !ok || u.Reason != want || u.MessageType != protocol.MessageTypeReconcileResult {
			t.Fatalf("event %d = %+v, want unmatched %s", i, events[i], want)
		}
	}
	if _, recs := h.router.PendingCount(h.principal.ConnectorID); recs != 1 {
		t.Fatalf("reconcile pending = %d, want 1", recs)
	}

	p.send(reply(req.MessageID, 2))
	event, ok := h.events.waitCount(t, 3)[2].(connector.ReconcileResultEvent)
	if !ok || event.RequestMessageID != sent.MessageID || event.Correlation.Generation != 2 || event.RequestID != "request-reconcile" ||
		event.Trace.Traceparent != testTraceparent || len(event.Payload.Observations) != 2 || event.Payload.Observations[1].Source != "DISCOVERED_CANDIDATE" {
		t.Fatalf("event = %+v", event)
	}
	if _, recs := h.router.PendingCount(h.principal.ConnectorID); recs != 0 {
		t.Fatalf("결과 뒤 reconcile pending = %d", recs)
	}
}

// 전체 routing 흐름에서 Credential, Authorization, Connector가 준 error 문구/Provider 식별자, message payload가 log에 남지 않는다.
func TestRoutingLogsNeverContainSecretsOrPayload(t *testing.T) {
	h := newRoutedHarness(t)
	p := h.establish()
	h.waitReady(h.principal.ConnectorID)
	send(t, h, provisionCommand(h.principal.ConnectorID, "op-1", "lab-A", 1))
	cmd := p.commands(1)[0]
	const providerRaw = "provider-raw-response-marker-58e1"

	failed := resultFor(cmd, protocol.OutcomeFailed)
	failed["payload"].(map[string]any)["error"] = map[string]any{"code": "PROVIDER_ERROR", "message": providerRaw}
	failed["payload"].(map[string]any)["providerResources"].([]any)[0].(map[string]any)["providerId"] = providerRaw
	p.send(failed)
	h.events.waitCount(t, 1)
	p.send(failed)                                                       // 중복 result → unmatched
	p.send(map[string]any{"type": "OPERATION_ACK", "note": providerRaw}) // Schema-invalid
	p.sync()

	logs := h.logs.String()
	for _, secret := range []string{providerRaw, testCredential, "Bearer ", "image-1", "provider-connection-1"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("log에 %q가 남음: %s", secret, logs)
		}
	}
	if !strings.Contains(logs, "no_pending") || !strings.Contains(logs, h.principal.ConnectorID.String()) {
		t.Fatalf("correlation 진단이 log에 없음: %s", logs)
	}
}

func newTestLogger(out *syncBuffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
