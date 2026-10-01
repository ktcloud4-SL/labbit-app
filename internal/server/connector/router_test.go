package connector

import (
	"bytes"
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

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

// sinkRecorder는 Router가 넘긴 event를 순서대로 기록한다.
type sinkRecorder struct {
	mu     sync.Mutex
	events []Event
}

func (s *sinkRecorder) HandleEvent(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

func (s *sinkRecorder) all() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Event(nil), s.events...)
}

// only는 event가 정확히 하나일 때 그것을 반환한다.
func (s *sinkRecorder) only(t *testing.T) Event {
	t.Helper()
	events := s.all()
	if len(events) != 1 {
		t.Fatalf("event %d개 = %+v, want 1개", len(events), events)
	}
	return events[0]
}

// frameLog는 Route로 쓴 frame을 기록한다.
type frameLog struct {
	mu     sync.Mutex
	frames [][]byte
}

func (f *frameLog) route(data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.frames = append(f.frames, append([]byte(nil), data...))
	return nil
}

func (f *frameLog) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.frames)
}

func (f *frameLog) last(t *testing.T) []byte {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.frames) == 0 {
		t.Fatal("전송된 frame이 없음")
	}
	return f.frames[len(f.frames)-1]
}

type syncLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncLog) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncLog) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type routerFixture struct {
	t        *testing.T
	registry *Registry
	sink     *sinkRecorder
	router   *Router
	logs     *syncLog
}

func newRouterFixture(t *testing.T) *routerFixture {
	t.Helper()
	registry := NewRegistry()
	sink := &sinkRecorder{}
	logs := &syncLog{}
	router, err := NewRouter(RouterOptions{
		Registry: registry,
		Sink:     sink,
		Logger:   slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &routerFixture{t: t, registry: registry, sink: sink, router: router, logs: logs}
}

// connect는 principal의 Connector에 Session을 등록하고 protocol-ready로 만든다. 반환한 frameLog가 그 Session의 writer다.
func (f *routerFixture) connect(principal Principal) (*Registration, *frameLog) {
	f.t.Helper()
	frames := &frameLog{}
	registration := f.registry.Register(principal, nil)
	if !registration.MarkReady(frames.route) {
		f.t.Fatal("MarkReady가 거절됨")
	}
	return registration, frames
}

func provisionPayload() protocol.OperationCommandPayload {
	return protocol.OperationCommandPayload{
		MutationType: protocol.MutationTypeProvision,
		CreationSnapshot: &protocol.CreationSnapshot{
			ProviderConnectionID: "pc-1",
			VMs: []protocol.ResolvedVmSpec{{
				VMKey: "vm-1", Role: "workspace", InstanceIndex: 0, ImageID: "img-1", FlavorID: "fl-1",
				FlavorSpec: &protocol.ResolvedFlavorSpec{VCPUs: 1, RAMMiB: 512, DiskGiB: 0},
			}},
			WorkspaceVMKey: "vm-1",
		},
	}
}

func commandFor(connectorID uuid.UUID, c Correlation) OperationCommand {
	return OperationCommand{ConnectorID: connectorID, RequestID: "req-" + c.LabInstanceID, Correlation: c, Payload: provisionPayload()}
}

func corr(op, lab string, generation int64) Correlation {
	return Correlation{OperationID: op, LabInstanceID: lab, Generation: generation}
}

// replyFor는 Connector가 sent에 응답할 때의 inbound header다.
func replyFor(sent SentMessage, c Correlation) Inbound {
	return Inbound{MessageID: uuid.NewString(), ReplyToMessageID: sent.MessageID, OperationID: c.OperationID, LabInstanceID: c.LabInstanceID, Generation: c.Generation}
}

func mustSend(t *testing.T, f *routerFixture, cmd OperationCommand) SentMessage {
	t.Helper()
	sent, err := f.router.SendOperationCommand(context.Background(), cmd)
	if err != nil {
		t.Fatalf("SendOperationCommand() error = %v", err)
	}
	return sent
}

func pendingOps(f *routerFixture, connectorID uuid.UUID) int {
	ops, _ := f.router.PendingCount(connectorID)
	return ops
}

func decodeCommand(t *testing.T, data []byte) protocol.OperationCommandMessage {
	t.Helper()
	var msg protocol.OperationCommandMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("frame이 OPERATION_COMMAND가 아님: %v: %s", err, data)
	}
	return msg
}

func wantUnmatched(t *testing.T, event Event, connectorID uuid.UUID, messageType string, reason UnmatchedReason) UnmatchedEvent {
	t.Helper()
	got, ok := event.(UnmatchedEvent)
	if !ok {
		t.Fatalf("event = %T %+v, want UnmatchedEvent", event, event)
	}
	if got.ConnectorID != connectorID || got.MessageType != messageType || got.Reason != reason {
		t.Fatalf("UnmatchedEvent = %+v, want connector %s, type %s, reason %s", got, connectorID, messageType, reason)
	}
	return got
}

func TestNewRouterRequiresRegistry(t *testing.T) {
	if _, err := NewRouter(RouterOptions{}); err == nil {
		t.Fatal("Registry 없이 Router가 만들어짐")
	}
}

// Registry current만으로는 command를 보내지 않는다. 연결이 없거나 HELLO_ACK 전이면 아무것도 쓰지 않고 pending도 만들지 않는다.
func TestSendRequiresProtocolReadyConnection(t *testing.T) {
	f := newRouterFixture(t)
	principal := principalOf(uuid.New())
	cmd := commandFor(principal.ConnectorID, corr("op-1", "lab-A", 1))

	if _, err := f.router.SendOperationCommand(context.Background(), cmd); !errors.Is(err, ErrNotConnected) || !errors.Is(err, ErrConnectorUnavailable) {
		t.Fatalf("연결 없음 = %v, want ErrNotConnected", err)
	}

	registration := f.registry.Register(principal, nil) // current이지만 HELLO_ACK 전이다.
	if _, err := f.router.SendOperationCommand(context.Background(), cmd); !errors.Is(err, ErrNotReady) || !errors.Is(err, ErrConnectorUnavailable) {
		t.Fatalf("HELLO_ACK 전 = %v, want ErrNotReady", err)
	}
	if ops, recs := f.router.PendingCount(principal.ConnectorID); ops != 0 || recs != 0 {
		t.Fatalf("보내지 못한 command의 pending이 남음: %d, %d", ops, recs)
	}

	frames := &frameLog{}
	registration.MarkReady(frames.route)
	sent := mustSend(t, f, cmd)
	if frames.count() != 1 || pendingOps(f, principal.ConnectorID) != 1 || sent.MessageID == "" {
		t.Fatalf("ready 뒤 writes %d, pending %d, sent %+v", frames.count(), pendingOps(f, principal.ConnectorID), sent)
	}
}

// A가 ready인 상태에서 B가 교체하면 A에도(교체됨) B에도(HELLO_ACK 전) 보내지 않는다. B가 ready가 된 뒤에는 B로만 보낸다.
func TestSendDuringReplacementGoesNowhereUntilNewSessionIsReady(t *testing.T) {
	f := newRouterFixture(t)
	principal := principalOf(uuid.New())
	_, oldFrames := f.connect(principal)

	fresh := f.registry.Register(principal, nil)
	if _, err := f.router.SendOperationCommand(context.Background(), commandFor(principal.ConnectorID, corr("op-1", "lab-A", 1))); !errors.Is(err, ErrNotReady) {
		t.Fatalf("교체 직후 = %v, want ErrNotReady", err)
	}
	if oldFrames.count() != 0 || pendingOps(f, principal.ConnectorID) != 0 {
		t.Fatalf("교체된 A로 나갔거나 pending이 남음: writes %d, pending %d", oldFrames.count(), pendingOps(f, principal.ConnectorID))
	}

	newFrames := &frameLog{}
	fresh.MarkReady(newFrames.route)
	mustSend(t, f, commandFor(principal.ConnectorID, corr("op-1", "lab-A", 1)))
	if oldFrames.count() != 0 || newFrames.count() != 1 {
		t.Fatalf("writes = old %d, new %d, want old 0, new 1", oldFrames.count(), newFrames.count())
	}
}

// 보내는 frame은 Backend가 만든 messageId와 command correlation을 싣고, 내부 routing identity(ConnectorID)는 싣지 않는다.
func TestSendOperationCommandWireContent(t *testing.T) {
	f := newRouterFixture(t)
	principal := principalOf(uuid.New())
	_, frames := f.connect(principal)

	cmd := commandFor(principal.ConnectorID, corr("op-1", "lab-A", 3))
	cmd.Trace = TraceContext{Traceparent: sampledParent, Tracestate: validTraceStat}
	sent := mustSend(t, f, cmd)

	frame := frames.last(t)
	msg := decodeCommand(t, frame)
	if msg.Type != protocol.MessageTypeOperationCommand || msg.MessageID != sent.MessageID || msg.MessageID == "" {
		t.Fatalf("type/messageId = %q/%q, sent %q", msg.Type, msg.MessageID, sent.MessageID)
	}
	if _, err := uuid.Parse(msg.MessageID); err != nil {
		t.Fatalf("messageId %q는 Backend가 만든 UUID여야 함: %v", msg.MessageID, err)
	}
	if msg.OperationID != "op-1" || msg.LabInstanceID != "lab-A" || msg.Generation != 3 || msg.RequestID != "req-lab-A" {
		t.Fatalf("correlation = %+v", msg.BaseEnvelope)
	}
	if msg.SentAt.IsZero() || msg.ReplyToMessageID != "" {
		t.Fatalf("sentAt %v, replyTo %q", msg.SentAt, msg.ReplyToMessageID)
	}
	if msg.TraceParent != sampledParent || msg.TraceState != validTraceStat {
		t.Fatalf("Trace = %q, %q", msg.TraceParent, msg.TraceState)
	}
	if msg.Payload.MutationType != protocol.MutationTypeProvision || msg.Payload.CreationSnapshot == nil {
		t.Fatalf("payload = %+v", msg.Payload)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(frame, &raw); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"connectorId", "ConnectorID", "connector_id"} {
		if _, present := raw[name]; present {
			t.Fatalf("내부 routing identity가 wire에 실림: %s", name)
		}
	}
	if strings.Contains(string(frame), principal.ConnectorID.String()) {
		t.Fatal("ConnectorID가 frame 어디에든 실림")
	}
}

// 첫 VM의 instanceIndex 0도 wire에 실려야 한다(Schema required). omitempty로 빠지면 Schema-invalid command가 된다.
func TestOperationCommandSerializesZeroInstanceIndex(t *testing.T) {
	f := newRouterFixture(t)
	principal := principalOf(uuid.New())
	_, frames := f.connect(principal)
	mustSend(t, f, commandFor(principal.ConnectorID, corr("op-1", "lab-A", 1)))

	var msg struct {
		Payload struct {
			CreationSnapshot struct {
				VMs []map[string]json.RawMessage `json:"vms"`
			} `json:"creationSnapshot"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(frames.last(t), &msg); err != nil {
		t.Fatal(err)
	}
	if got := string(msg.Payload.CreationSnapshot.VMs[0]["instanceIndex"]); got != "0" {
		t.Fatalf("instanceIndex = %q, want 0(required field가 wire에서 빠짐)", got)
	}
}

// Connector는 command를 받자마자 ACK/RESULT를 보낼 수 있다. pending을 write보다 먼저 등록하므로 write 도중 도착한 응답도 연결된다.
func TestPendingIsRegisteredBeforeWriteSoImmediateResponsesAreRouted(t *testing.T) {
	f := newRouterFixture(t)
	principal := principalOf(uuid.New())
	c := corr("op-1", "lab-A", 1)

	var pendingDuringWrite int
	var ackRouted, resultRouted bool
	registration := f.registry.Register(principal, nil)
	registration.MarkReady(func(data []byte) error {
		// write 시점에 이미 등록되어 있고, peer가 write를 관측하자마자 응답해도 연결된다.
		pendingDuringWrite = pendingOps(f, principal.ConnectorID)
		msg := decodeCommand(t, data)
		in := Inbound{ReplyToMessageID: msg.MessageID, OperationID: msg.OperationID, LabInstanceID: msg.LabInstanceID, Generation: msg.Generation}
		ackRouted = f.router.RouteOperationAck(principal.ConnectorID, in, protocol.OperationAckPayload{Accepted: true})
		in.ReplyToMessageID = ""
		resultRouted = f.router.RouteOperationResult(principal.ConnectorID, in, protocol.OperationResultPayload{Outcome: protocol.OutcomeSucceeded})
		return nil
	})

	sent := mustSend(t, f, commandFor(principal.ConnectorID, c))
	if pendingDuringWrite != 1 {
		t.Fatalf("write 시점의 pending = %d, want 1(pending은 write 전에 등록)", pendingDuringWrite)
	}
	if !ackRouted || !resultRouted {
		t.Fatalf("즉시 응답 routing = ack %v, result %v, want 둘 다 true", ackRouted, resultRouted)
	}
	events := f.sink.all()
	if len(events) != 2 {
		t.Fatalf("events = %+v, want ACK, RESULT", events)
	}
	if ack, ok := events[0].(OperationAckEvent); !ok || ack.RequestMessageID != sent.MessageID || ack.Correlation != c {
		t.Fatalf("첫 event = %+v", events[0])
	}
	if res, ok := events[1].(OperationResultEvent); !ok || res.RequestMessageID != sent.MessageID {
		t.Fatalf("둘째 event = %+v", events[1])
	}
	// write 도중 terminal result로 끝난 command는 send가 성공으로 끝나도 되살아나지 않는다.
	if got := pendingOps(f, principal.ConnectorID); got != 0 {
		t.Fatalf("terminal result 뒤 pending = %d, want 0", got)
	}
}

// write가 실패하면 방금 등록한 pending을 그 command identity로 되돌리고, 다른 곳으로 다시 보내거나 재시도하지 않는다.
func TestWriteFailureRollsBackPendingWithoutRetry(t *testing.T) {
	f := newRouterFixture(t)
	principal := principalOf(uuid.New())
	c := corr("op-1", "lab-A", 1)

	var mu sync.Mutex
	writes, fail := 0, true
	var failedMessageID string
	registration := f.registry.Register(principal, nil)
	registration.MarkReady(func(data []byte) error {
		mu.Lock()
		defer mu.Unlock()
		writes++
		if fail {
			failedMessageID = decodeCommand(t, data).MessageID
			return errors.New("write tcp: broken pipe")
		}
		return nil
	})

	_, err := f.router.SendOperationCommand(context.Background(), commandFor(principal.ConnectorID, c))
	if !errors.Is(err, ErrSendFailed) || errors.Is(err, ErrConnectorUnavailable) {
		t.Fatalf("write 실패 = %v, want ErrSendFailed(전송 여부 불명확)", err)
	}
	if writes != 1 || pendingOps(f, principal.ConnectorID) != 0 {
		t.Fatalf("writes %d, pending %d, want 1, 0(자동 재전송 없음, pending 되돌림)", writes, pendingOps(f, principal.ConnectorID))
	}

	// 같은 correlation을 호출자가 다시 보내는 것은 새 command다. 실패한 command의 늦은 ACK는 새 command로 새지 않는다.
	mu.Lock()
	fail = false
	mu.Unlock()
	sent := mustSend(t, f, commandFor(principal.ConnectorID, c))
	late := Inbound{MessageID: "late", ReplyToMessageID: failedMessageID, OperationID: c.OperationID, LabInstanceID: c.LabInstanceID, Generation: c.Generation}
	if f.router.RouteOperationAck(principal.ConnectorID, late, protocol.OperationAckPayload{Accepted: true}) {
		t.Fatal("실패한 command의 ACK가 새 command에 연결됨")
	}
	wantUnmatched(t, f.sink.only(t), principal.ConnectorID, protocol.MessageTypeOperationAck, ReasonReplyMismatch)
	if got := pendingOps(f, principal.ConnectorID); got != 1 || sent.MessageID == failedMessageID {
		t.Fatalf("pending %d, 새 messageId가 실패한 것과 같음 %v", got, sent.MessageID == failedMessageID)
	}
}

// 종료가 시작된 connection은 아무것도 쓰지 않는다. 이는 전송 실패가 아니라 사용 불가이며 pending도 남지 않는다.
func TestRouteClosedIsUnavailableNotSendFailure(t *testing.T) {
	f := newRouterFixture(t)
	principal := principalOf(uuid.New())
	calls := 0
	f.registry.Register(principal, nil).MarkReady(func([]byte) error { calls++; return ErrRouteClosed })

	_, err := f.router.SendOperationCommand(context.Background(), commandFor(principal.ConnectorID, corr("op-1", "lab-A", 1)))
	if !errors.Is(err, ErrConnectionClosing) || !errors.Is(err, ErrConnectorUnavailable) || errors.Is(err, ErrSendFailed) {
		t.Fatalf("종료 중 = %v, want ErrConnectionClosing", err)
	}
	if calls != 1 || pendingOps(f, principal.ConnectorID) != 0 {
		t.Fatalf("writes %d, pending %d", calls, pendingOps(f, principal.ConnectorID))
	}
}

// 한 correlation에는 active command를 하나만 둔다. 중복은 보내지 않고 기존 pending을 건드리지 않는다.
func TestDuplicateActiveCorrelationIsRejected(t *testing.T) {
	f := newRouterFixture(t)
	principal := principalOf(uuid.New())
	_, frames := f.connect(principal)
	c := corr("op-1", "lab-A", 1)
	first := mustSend(t, f, commandFor(principal.ConnectorID, c))

	if _, err := f.router.SendOperationCommand(context.Background(), commandFor(principal.ConnectorID, c)); !errors.Is(err, ErrDuplicateCorrelation) {
		t.Fatalf("중복 correlation = %v, want ErrDuplicateCorrelation", err)
	}
	if frames.count() != 1 || pendingOps(f, principal.ConnectorID) != 1 {
		t.Fatalf("writes %d, pending %d, want 1, 1", frames.count(), pendingOps(f, principal.ConnectorID))
	}
	// 기존 pending은 여전히 첫 command에 묶여 있다.
	if !f.router.RouteOperationAck(principal.ConnectorID, replyFor(first, c), protocol.OperationAckPayload{Accepted: true}) {
		t.Fatal("중복 시도가 기존 pending을 훼손함")
	}

	// operationId가 같아도 LabInstance나 generation이 다르면 다른 command다.
	mustSend(t, f, commandFor(principal.ConnectorID, corr("op-1", "lab-B", 1)))
	mustSend(t, f, commandFor(principal.ConnectorID, corr("op-1", "lab-A", 2)))
	// 끝난 command의 correlation은 다시 쓸 수 있다.
	f.router.RouteOperationResult(principal.ConnectorID, Inbound{OperationID: "op-1", LabInstanceID: "lab-A", Generation: 1}, protocol.OperationResultPayload{Outcome: protocol.OutcomeFailed})
	mustSend(t, f, commandFor(principal.ConnectorID, c))
}

// ACK는 replyToMessageId가 command의 messageId와 정확히 같고 Connector, operationId, labInstanceId, generation이 모두 맞아야 연결된다.
func TestAckRequiresExactCorrelationAndReply(t *testing.T) {
	c := corr("op-1", "lab-A", 1)
	tests := []struct {
		name   string
		mutate func(in *Inbound, other *uuid.UUID)
		reason UnmatchedReason
	}{
		{"wrong operationId", func(in *Inbound, _ *uuid.UUID) { in.OperationID = "op-2" }, ReasonNoPending},
		{"wrong labInstanceId", func(in *Inbound, _ *uuid.UUID) { in.LabInstanceID = "lab-B" }, ReasonNoPending},
		{"wrong generation", func(in *Inbound, _ *uuid.UUID) { in.Generation = 2 }, ReasonNoPending},
		{"generation not representable", func(in *Inbound, _ *uuid.UUID) { in.Generation = 0 }, ReasonUnrepresentable},
		{"wrong replyToMessageId", func(in *Inbound, _ *uuid.UUID) { in.ReplyToMessageID = "other-message" }, ReasonReplyMismatch},
		{"missing replyToMessageId", func(in *Inbound, _ *uuid.UUID) { in.ReplyToMessageID = "" }, ReasonReplyMismatch},
		{"other connector", func(_ *Inbound, other *uuid.UUID) { *other = uuid.New() }, ReasonNoPending},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newRouterFixture(t)
			principal := principalOf(uuid.New())
			f.connect(principal)
			sent := mustSend(t, f, commandFor(principal.ConnectorID, c))

			in, connectorID := replyFor(sent, c), principal.ConnectorID
			tt.mutate(&in, &connectorID)
			if f.router.RouteOperationAck(connectorID, in, protocol.OperationAckPayload{Accepted: true}) {
				t.Fatal("잘못된 correlation의 ACK가 연결됨")
			}
			wantUnmatched(t, f.sink.only(t), connectorID, protocol.MessageTypeOperationAck, tt.reason)
			if got := pendingOps(f, principal.ConnectorID); got != 1 {
				t.Fatalf("pending = %d, want 1(어떤 pending도 소비하거나 훼손하지 않는다)", got)
			}
		})
	}
}

// PROGRESS와 RESULT의 replyToMessageId는 필수가 아니므로 강제하지 않지만, 있으면 command의 messageId와 같아야 한다.
func TestProgressAndResultReplyIsOptionalButMustMatchWhenPresent(t *testing.T) {
	c := corr("op-1", "lab-A", 1)
	for _, kind := range []string{protocol.MessageTypeOperationProgress, protocol.MessageTypeOperationResult} {
		route := func(f *routerFixture, connectorID uuid.UUID, in Inbound) bool {
			if kind == protocol.MessageTypeOperationProgress {
				return f.router.RouteOperationProgress(connectorID, in, protocol.OperationProgressPayload{Stage: "CREATE_VM"})
			}
			return f.router.RouteOperationResult(connectorID, in, protocol.OperationResultPayload{Outcome: protocol.OutcomeSucceeded})
		}
		t.Run(kind+"/without replyTo", func(t *testing.T) {
			f := newRouterFixture(t)
			principal := principalOf(uuid.New())
			f.connect(principal)
			sent := mustSend(t, f, commandFor(principal.ConnectorID, c))
			in := replyFor(sent, c)
			in.ReplyToMessageID = ""
			if !route(f, principal.ConnectorID, in) {
				t.Fatal("replyToMessageId가 없다는 이유로 거절됨(Schema가 required로 정하지 않음)")
			}
		})
		t.Run(kind+"/with matching replyTo", func(t *testing.T) {
			f := newRouterFixture(t)
			principal := principalOf(uuid.New())
			f.connect(principal)
			sent := mustSend(t, f, commandFor(principal.ConnectorID, c))
			if !route(f, principal.ConnectorID, replyFor(sent, c)) {
				t.Fatal("일치하는 replyToMessageId가 거절됨")
			}
		})
		t.Run(kind+"/with mismatching replyTo", func(t *testing.T) {
			f := newRouterFixture(t)
			principal := principalOf(uuid.New())
			f.connect(principal)
			sent := mustSend(t, f, commandFor(principal.ConnectorID, c))
			in := replyFor(sent, c)
			in.ReplyToMessageID = "some-other-command"
			if route(f, principal.ConnectorID, in) {
				t.Fatal("다른 command를 가리키는 replyToMessageId가 연결됨")
			}
			wantUnmatched(t, f.sink.only(t), principal.ConnectorID, kind, ReasonReplyMismatch)
			if got := pendingOps(f, principal.ConnectorID); got != 1 {
				t.Fatalf("mismatch가 pending을 소비함: %d", got)
			}
		})
		t.Run(kind+"/wrong correlation", func(t *testing.T) {
			f := newRouterFixture(t)
			principal := principalOf(uuid.New())
			f.connect(principal)
			mustSend(t, f, commandFor(principal.ConnectorID, c))
			for _, in := range []Inbound{
				{OperationID: "op-2", LabInstanceID: "lab-A", Generation: 1},
				{OperationID: "op-1", LabInstanceID: "lab-B", Generation: 1},
				{OperationID: "op-1", LabInstanceID: "lab-A", Generation: 2},
			} {
				if route(f, principal.ConnectorID, in) {
					t.Fatalf("%+v가 다른 pending에 연결됨", in)
				}
			}
			if got := pendingOps(f, principal.ConnectorID); got != 1 {
				t.Fatalf("pending = %d, want 1", got)
			}
		})
	}
}

// RESULT는 pending을 끝낸다. 같은 command의 중복 terminal result는 이미 끝났으므로 어디에도 연결하지 않는다.
// ACK(accepted=true)와 PROGRESS는 pending을 끝내지 않고, 거절 ACK(accepted=false)는 그 command를 끝낸다.
func TestTerminalMessagesEndPendingCommand(t *testing.T) {
	c := corr("op-1", "lab-A", 1)
	f := newRouterFixture(t)
	principal := principalOf(uuid.New())
	f.connect(principal)
	cid := principal.ConnectorID

	sent := mustSend(t, f, commandFor(cid, c))
	if !f.router.RouteOperationAck(cid, replyFor(sent, c), protocol.OperationAckPayload{Accepted: true}) ||
		!f.router.RouteOperationProgress(cid, replyFor(sent, c), protocol.OperationProgressPayload{Stage: "CREATE_NETWORK"}) ||
		!f.router.RouteOperationProgress(cid, replyFor(sent, c), protocol.OperationProgressPayload{Stage: "CREATE_VM"}) {
		t.Fatal("ACK/PROGRESS가 연결되지 않음")
	}
	if got := pendingOps(f, cid); got != 1 {
		t.Fatalf("ACK/PROGRESS 뒤 pending = %d, want 1", got)
	}
	if !f.router.RouteOperationResult(cid, replyFor(sent, c), protocol.OperationResultPayload{Outcome: protocol.OutcomeUnknown}) {
		t.Fatal("RESULT가 연결되지 않음")
	}
	if got := pendingOps(f, cid); got != 0 {
		t.Fatalf("RESULT 뒤 pending = %d, want 0", got)
	}
	if f.router.RouteOperationResult(cid, replyFor(sent, c), protocol.OperationResultPayload{Outcome: protocol.OutcomeSucceeded}) {
		t.Fatal("이미 끝난 command의 중복 RESULT가 연결됨")
	}
	events := f.sink.all()
	wantUnmatched(t, events[len(events)-1], cid, protocol.MessageTypeOperationResult, ReasonNoPending)

	// 끝난 command 뒤의 늦은 PROGRESS/ACK도 어디에도 연결하지 않는다.
	if f.router.RouteOperationProgress(cid, replyFor(sent, c), protocol.OperationProgressPayload{Stage: "LATE"}) {
		t.Fatal("끝난 command의 늦은 PROGRESS가 연결됨")
	}

	// 거절 ACK는 그 command를 끝낸다. Provider 작업이 시작되지 않았으므로 더 기다릴 result가 없다.
	rejected := mustSend(t, f, commandFor(cid, c))
	if !f.router.RouteOperationAck(cid, replyFor(rejected, c), protocol.OperationAckPayload{Accepted: false, Error: &protocol.SafeError{Code: "INVALID_COMMAND"}}) {
		t.Fatal("거절 ACK가 연결되지 않음")
	}
	if got := pendingOps(f, cid); got != 0 {
		t.Fatalf("거절 ACK 뒤 pending = %d, want 0", got)
	}
	ack, ok := f.sink.all()[len(f.sink.all())-1].(OperationAckEvent)
	if !ok || ack.Payload.Accepted || ack.Payload.Error == nil || ack.Payload.Error.Code != "INVALID_COMMAND" {
		t.Fatalf("거절 ACK event = %+v", ack)
	}
}

// 같은 operationId의 서로 다른 LabInstance/generation command 둘이 ACK-2, PROGRESS-1, PROGRESS-2, RESULT-2, RESULT-1로
// 섞여 도착해도 각 event는 정확한 command로만 전달된다. 다른 Connector가 같은 ID를 보내도 들어오지 않는다.
func TestParallelCommandsKeepCorrelationIsolated(t *testing.T) {
	f := newRouterFixture(t)
	a, b := principalOf(uuid.New()), principalOf(uuid.New())
	f.connect(a)
	f.connect(b)
	c1, c2 := corr("op-1", "lab-A", 1), corr("op-1", "lab-B", 3)

	sent1 := mustSend(t, f, commandFor(a.ConnectorID, c1))
	sent2 := mustSend(t, f, commandFor(a.ConnectorID, c2))
	if sent1.MessageID == sent2.MessageID {
		t.Fatal("서로 다른 command의 messageId가 같음")
	}

	// Connector B가 A의 command와 같은 ID를 보내도 A의 pending에 들어가지 않는다.
	if f.router.RouteOperationProgress(b.ConnectorID, replyFor(sent1, c1), protocol.OperationProgressPayload{Stage: "SPOOF"}) {
		t.Fatal("다른 Connector의 message가 A의 pending에 연결됨")
	}
	wantUnmatched(t, f.sink.only(t), b.ConnectorID, protocol.MessageTypeOperationProgress, ReasonNoPending)

	ok := f.router.RouteOperationAck(a.ConnectorID, replyFor(sent2, c2), protocol.OperationAckPayload{Accepted: true}) &&
		f.router.RouteOperationProgress(a.ConnectorID, replyFor(sent1, c1), protocol.OperationProgressPayload{Stage: "STAGE-1"}) &&
		f.router.RouteOperationProgress(a.ConnectorID, replyFor(sent2, c2), protocol.OperationProgressPayload{Stage: "STAGE-2"}) &&
		f.router.RouteOperationResult(a.ConnectorID, replyFor(sent2, c2), protocol.OperationResultPayload{Outcome: protocol.OutcomeSucceeded}) &&
		f.router.RouteOperationResult(a.ConnectorID, replyFor(sent1, c1), protocol.OperationResultPayload{Outcome: protocol.OutcomeFailed})
	if !ok {
		t.Fatal("interleave된 message가 연결되지 않음")
	}

	events := f.sink.all()[1:] // 첫 event는 위의 Unmatched
	type seen struct {
		routed Routed
		detail string
	}
	var got []seen
	for _, event := range events {
		switch e := event.(type) {
		case OperationAckEvent:
			got = append(got, seen{e.Routed, "ack"})
		case OperationProgressEvent:
			got = append(got, seen{e.Routed, "progress:" + e.Payload.Stage})
		case OperationResultEvent:
			got = append(got, seen{e.Routed, "result:" + e.Payload.Outcome})
		default:
			t.Fatalf("예상하지 못한 event: %T", event)
		}
	}
	want := []struct {
		correlation Correlation
		requestMsg  string
		requestID   string
		detail      string
	}{
		{c2, sent2.MessageID, "req-lab-B", "ack"},
		{c1, sent1.MessageID, "req-lab-A", "progress:STAGE-1"},
		{c2, sent2.MessageID, "req-lab-B", "progress:STAGE-2"},
		{c2, sent2.MessageID, "req-lab-B", "result:SUCCEEDED"},
		{c1, sent1.MessageID, "req-lab-A", "result:FAILED"},
	}
	if len(got) != len(want) {
		t.Fatalf("events = %d개, want %d개", len(got), len(want))
	}
	for i, w := range want {
		g := got[i]
		if g.routed.ConnectorID != a.ConnectorID || g.routed.Correlation != w.correlation ||
			g.routed.RequestMessageID != w.requestMsg || g.routed.RequestID != w.requestID || g.detail != w.detail {
			t.Fatalf("event %d = %+v %q, want %+v", i, g.routed, g.detail, w)
		}
	}
	if got := pendingOps(f, a.ConnectorID); got != 0 {
		t.Fatalf("모두 끝난 뒤 pending = %d", got)
	}
}

// pending은 WebSocket Session이 아니라 Connector와 업무 correlation에 묶인다. 재접속은 command retry가 아니므로
// 새 Session으로 아무 message도 다시 보내지 않고, 재접속한 Connector가 보낸 늦은 결과는 pending이 있으면 연결된다.
func TestReconnectDoesNotResendAndLateResultStillRoutes(t *testing.T) {
	f := newRouterFixture(t)
	principal := principalOf(uuid.New())
	c := corr("op-1", "lab-A", 1)
	_, oldFrames := f.connect(principal)
	sent := mustSend(t, f, commandFor(principal.ConnectorID, c))

	newRegistration, newFrames := f.connect(principal) // 재접속: 이전 Session은 교체된다.
	if oldFrames.count() != 1 || newFrames.count() != 0 {
		t.Fatalf("재접속 뒤 writes = old %d, new %d, want 1, 0(자동 재전송 없음)", oldFrames.count(), newFrames.count())
	}
	if got := pendingOps(f, principal.ConnectorID); got != 1 {
		t.Fatalf("재접속이 pending을 지움: %d", got)
	}

	if !f.router.RouteOperationResult(principal.ConnectorID, replyFor(sent, c), protocol.OperationResultPayload{Outcome: protocol.OutcomeSucceeded}) {
		t.Fatal("재접속 뒤 도착한 늦은 결과가 pending에 연결되지 않음")
	}
	if oldFrames.count() != 1 || newFrames.count() != 0 {
		t.Fatalf("결과 수신이 message를 다시 보냄: old %d, new %d", oldFrames.count(), newFrames.count())
	}
	newRegistration.Release()
}

func reconcileFor(connectorID uuid.UUID, c Correlation) ReconcileRequest {
	return ReconcileRequest{
		ConnectorID: connectorID, RequestID: "req-reconcile", Correlation: c,
		Payload: protocol.ReconcileRequestPayload{KnownResources: []protocol.ProviderResourceRef{{ResourceType: "SERVER", ProviderID: "srv-1", Generation: 1}}},
	}
}

func reconcileReply(sent SentMessage, c Correlation) Inbound {
	return Inbound{MessageID: uuid.NewString(), ReplyToMessageID: sent.MessageID, OperationID: c.OperationID, LabInstanceID: c.LabInstanceID, Generation: c.Generation}
}

func TestReconcileRequestIsRoutedByReplyAndCorrelation(t *testing.T) {
	c := corr("op-1", "lab-A", 1)

	t.Run("matching result ends the request", func(t *testing.T) {
		f := newRouterFixture(t)
		principal := principalOf(uuid.New())
		_, frames := f.connect(principal)
		sent, err := f.router.SendReconcileRequest(context.Background(), reconcileFor(principal.ConnectorID, c))
		if err != nil {
			t.Fatal(err)
		}
		var msg protocol.ReconcileRequestMessage
		if err := json.Unmarshal(frames.last(t), &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Type != protocol.MessageTypeReconcileRequest || msg.MessageID != sent.MessageID || msg.OperationID != "op-1" ||
			msg.LabInstanceID != "lab-A" || msg.Generation != 1 || msg.RequestID != "req-reconcile" {
			t.Fatalf("frame = %+v", msg)
		}
		if _, recs := f.router.PendingCount(principal.ConnectorID); recs != 1 {
			t.Fatalf("reconcile pending = %d, want 1", recs)
		}

		result := protocol.ReconcileResultPayload{Observations: []protocol.ResourceObservation{{ResourceType: "SERVER", ProviderID: "srv-1", Exists: true, Source: "KNOWN_RESOURCE"}}}
		in := reconcileReply(sent, c)
		in.Trace = TraceContext{Traceparent: sampledParent}
		if !f.router.RouteReconcileResult(principal.ConnectorID, in, result) {
			t.Fatal("RECONCILE_RESULT가 연결되지 않음")
		}
		event, ok := f.sink.only(t).(ReconcileResultEvent)
		if !ok || event.RequestMessageID != sent.MessageID || event.Correlation != c || event.RequestID != "req-reconcile" ||
			event.Trace.Traceparent != sampledParent || len(event.Payload.Observations) != 1 {
			t.Fatalf("event = %+v", event)
		}
		if _, recs := f.router.PendingCount(principal.ConnectorID); recs != 0 {
			t.Fatalf("결과 뒤 pending = %d, want 0", recs)
		}
		if f.router.RouteReconcileResult(principal.ConnectorID, in, result) {
			t.Fatal("이미 끝난 reconcile request의 중복 결과가 연결됨")
		}
	})

	tests := []struct {
		name   string
		mutate func(in *Inbound, connectorID *uuid.UUID)
		reason UnmatchedReason
	}{
		{"wrong replyToMessageId", func(in *Inbound, _ *uuid.UUID) { in.ReplyToMessageID = "other" }, ReasonNoPending},
		{"missing replyToMessageId", func(in *Inbound, _ *uuid.UUID) { in.ReplyToMessageID = "" }, ReasonNoPending},
		{"wrong operationId", func(in *Inbound, _ *uuid.UUID) { in.OperationID = "op-2" }, ReasonCorrelationMismatch},
		{"wrong labInstanceId", func(in *Inbound, _ *uuid.UUID) { in.LabInstanceID = "lab-B" }, ReasonCorrelationMismatch},
		{"wrong generation", func(in *Inbound, _ *uuid.UUID) { in.Generation = 2 }, ReasonCorrelationMismatch},
		{"other connector", func(_ *Inbound, id *uuid.UUID) { *id = uuid.New() }, ReasonNoPending},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newRouterFixture(t)
			principal := principalOf(uuid.New())
			f.connect(principal)
			sent, err := f.router.SendReconcileRequest(context.Background(), reconcileFor(principal.ConnectorID, c))
			if err != nil {
				t.Fatal(err)
			}
			in, connectorID := reconcileReply(sent, c), principal.ConnectorID
			tt.mutate(&in, &connectorID)
			if f.router.RouteReconcileResult(connectorID, in, protocol.ReconcileResultPayload{}) {
				t.Fatal("잘못된 RECONCILE_RESULT가 연결됨")
			}
			wantUnmatched(t, f.sink.only(t), connectorID, protocol.MessageTypeReconcileResult, tt.reason)
			if _, recs := f.router.PendingCount(principal.ConnectorID); recs != 1 {
				t.Fatalf("reconcile pending = %d, want 1(훼손되면 안 됨)", recs)
			}
		})
	}
}

// RECONCILE_REQUEST의 knownResources는 required 배열이다. nil은 null이 아니라 []로 보낸다. discoverCandidates는 생략과 false를 구분한다.
func TestReconcileRequestWirePayload(t *testing.T) {
	f := newRouterFixture(t)
	principal := principalOf(uuid.New())
	_, frames := f.connect(principal)
	c := corr("op-1", "lab-A", 1)

	req := reconcileFor(principal.ConnectorID, c)
	req.Payload.KnownResources = nil
	if _, err := f.router.SendReconcileRequest(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	var payload map[string]json.RawMessage
	var envelope struct {
		Payload map[string]json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(frames.last(t), &envelope); err != nil {
		t.Fatal(err)
	}
	payload = envelope.Payload
	if got := string(payload["knownResources"]); got != "[]" {
		t.Fatalf("knownResources = %s, want []", got)
	}
	if _, present := payload["discoverCandidates"]; present {
		t.Fatal("생략한 discoverCandidates가 wire에 실림")
	}

	no := false
	req.Payload.DiscoverCandidates = &no
	if _, err := f.router.SendReconcileRequest(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(frames.last(t), &envelope); err != nil {
		t.Fatal(err)
	}
	if got := string(envelope.Payload["discoverCandidates"]); got != "false" {
		t.Fatalf("discoverCandidates = %s, want 명시적 false 유지", got)
	}
}

// reconcile 요청도 write보다 먼저 pending을 등록하고, 실패하면 되돌린다.
func TestReconcileRequestPendingBeforeWriteAndRollback(t *testing.T) {
	f := newRouterFixture(t)
	principal := principalOf(uuid.New())
	c := corr("op-1", "lab-A", 1)

	var immediate bool
	registration := f.registry.Register(principal, nil)
	registration.MarkReady(func(data []byte) error {
		var msg protocol.ReconcileRequestMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Error(err)
		}
		in := Inbound{ReplyToMessageID: msg.MessageID, OperationID: msg.OperationID, LabInstanceID: msg.LabInstanceID, Generation: msg.Generation}
		immediate = f.router.RouteReconcileResult(principal.ConnectorID, in, protocol.ReconcileResultPayload{})
		return nil
	})
	if _, err := f.router.SendReconcileRequest(context.Background(), reconcileFor(principal.ConnectorID, c)); err != nil {
		t.Fatal(err)
	}
	if !immediate {
		t.Fatal("write 도중 도착한 RECONCILE_RESULT가 연결되지 않음")
	}

	failing := principalOf(uuid.New())
	f.registry.Register(failing, nil).MarkReady(func([]byte) error { return errors.New("broken pipe") })
	if _, err := f.router.SendReconcileRequest(context.Background(), reconcileFor(failing.ConnectorID, c)); !errors.Is(err, ErrSendFailed) {
		t.Fatalf("err = %v, want ErrSendFailed", err)
	}
	if _, recs := f.router.PendingCount(failing.ConnectorID); recs != 0 {
		t.Fatalf("실패한 요청의 pending이 남음: %d", recs)
	}
	if _, err := f.router.SendReconcileRequest(context.Background(), reconcileFor(uuid.New(), c)); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("연결 없음 = %v", err)
	}
}

// 더 기다리지 않을 command는 호출자가 명시적으로 정리한다. 로컬 추적만 지우며 이후 도착은 unmatched가 된다.
func TestForgetRemovesPendingExplicitly(t *testing.T) {
	f := newRouterFixture(t)
	principal := principalOf(uuid.New())
	f.connect(principal)
	cid := principal.ConnectorID
	c := corr("op-1", "lab-A", 1)

	sent := mustSend(t, f, commandFor(cid, c))
	reconcile, err := f.router.SendReconcileRequest(context.Background(), reconcileFor(cid, c))
	if err != nil {
		t.Fatal(err)
	}
	if f.router.ForgetOperation(uuid.New(), c) || f.router.ForgetReconcile(uuid.New(), reconcile.MessageID) {
		t.Fatal("다른 Connector의 pending을 지움")
	}
	if !f.router.ForgetOperation(cid, c) || f.router.ForgetOperation(cid, c) {
		t.Fatal("ForgetOperation 결과가 잘못됨")
	}
	if !f.router.ForgetReconcile(cid, reconcile.MessageID) || f.router.ForgetReconcile(cid, reconcile.MessageID) {
		t.Fatal("ForgetReconcile 결과가 잘못됨")
	}
	if ops, recs := f.router.PendingCount(cid); ops != 0 || recs != 0 {
		t.Fatalf("pending = %d, %d", ops, recs)
	}
	if f.router.RouteOperationResult(cid, replyFor(sent, c), protocol.OperationResultPayload{Outcome: protocol.OutcomeSucceeded}) {
		t.Fatal("정리한 command의 결과가 연결됨")
	}
	mustSend(t, f, commandFor(cid, c)) // 정리한 correlation은 다시 쓸 수 있다.
}

// 유효한 Trace는 command에 전달하고 유효하지 않은 Trace는 버린다. 그 때문에 command를 실패시키거나 가짜 Trace를 만들지 않는다.
func TestOutboundTraceIsPropagatedOrDroppedWithoutFailingCommand(t *testing.T) {
	tests := []struct {
		name       string
		trace      TraceContext
		wantParent string
		wantState  string
	}{
		{"valid sampled", TraceContext{sampledParent, validTraceStat}, sampledParent, validTraceStat},
		{"valid not sampled", TraceContext{unsampledPrnt, ""}, unsampledPrnt, ""},
		{"invalid tracestate only", TraceContext{sampledParent, "not valid"}, sampledParent, ""},
		{"invalid traceparent drops tracestate", TraceContext{"garbage", validTraceStat}, "", ""},
		{"none", TraceContext{}, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newRouterFixture(t)
			principal := principalOf(uuid.New())
			_, frames := f.connect(principal)
			cmd := commandFor(principal.ConnectorID, corr("op-1", "lab-A", 1))
			cmd.Trace = tt.trace
			mustSend(t, f, cmd) // Trace 문제로 실패하지 않는다.

			msg := decodeCommand(t, frames.last(t))
			if msg.TraceParent != tt.wantParent || msg.TraceState != tt.wantState {
				t.Fatalf("frame Trace = %q, %q, want %q, %q", msg.TraceParent, msg.TraceState, tt.wantParent, tt.wantState)
			}
			var raw map[string]json.RawMessage
			_ = json.Unmarshal(frames.last(t), &raw)
			if _, present := raw["traceparent"]; present != (tt.wantParent != "") {
				t.Fatalf("traceparent 존재 = %v, want %v", present, tt.wantParent != "")
			}
		})
	}
}

// Trace는 routing key가 아니다. Trace 없이 도착한 결과도 연결되고 event의 Trace는 비어 있다. 유효한 Trace는 event에 유지된다.
func TestInboundTraceIsCarriedOnEventButNeverDecidesRouting(t *testing.T) {
	c := corr("op-1", "lab-A", 1)
	f := newRouterFixture(t)
	principal := principalOf(uuid.New())
	f.connect(principal)
	cid := principal.ConnectorID

	cmd := commandFor(cid, c)
	cmd.Trace = TraceContext{Traceparent: sampledParent}
	sent := mustSend(t, f, cmd)

	in := replyFor(sent, c)
	in.Trace = TraceContext{Traceparent: unsampledPrnt, Tracestate: validTraceStat}
	f.router.RouteOperationProgress(cid, in, protocol.OperationProgressPayload{Stage: "S"})
	in.Trace = TraceContext{}
	f.router.RouteOperationResult(cid, in, protocol.OperationResultPayload{Outcome: protocol.OutcomeSucceeded})

	events := f.sink.all()
	progress, ok1 := events[0].(OperationProgressEvent)
	result, ok2 := events[1].(OperationResultEvent)
	if !ok1 || !ok2 || progress.Trace.Traceparent != unsampledPrnt || progress.Trace.Tracestate != validTraceStat {
		t.Fatalf("progress event = %+v", events[0])
	}
	if result.Trace.Valid() || result.Correlation != c {
		t.Fatalf("Trace 없는 result event = %+v", result)
	}
}

func TestSendRejectsInvalidCommandsBeforeWritingAnything(t *testing.T) {
	tooBig := provisionPayload()
	tooBig.CreationSnapshot.StartupScript = &protocol.StartupScriptSnapshot{Content: strings.Repeat("x", int(protocol.MaxJSONMessageSize)), SHA256: strings.Repeat("a", 64)}
	noFlavor := provisionPayload()
	noFlavor.CreationSnapshot.VMs[0].FlavorSpec = nil
	withRef := provisionPayload()
	withRef.CreationSnapshot.VMs[0].ImageRef = "img-name"
	noWorkspace := provisionPayload()
	noWorkspace.CreationSnapshot.WorkspaceVMKey = "missing"
	badScript := provisionPayload()
	badScript.CreationSnapshot.StartupScript = &protocol.StartupScriptSnapshot{Content: "echo", SHA256: "abc"}
	badIndex := provisionPayload()
	badIndex.CreationSnapshot.VMs[0].InstanceIndex = -1
	noVMs := provisionPayload()
	noVMs.CreationSnapshot.VMs = nil
	badRef := protocol.OperationCommandPayload{MutationType: protocol.MutationTypeCleanup, ProviderResources: []protocol.ProviderResourceRef{{ResourceType: "SERVER", ProviderID: "srv-1"}}}
	reset := provisionPayload()
	reset.MutationType = protocol.MutationTypeReset
	reset.CreationSnapshot = nil

	tests := []struct {
		name string
		mut  func(cmd *OperationCommand)
	}{
		{"missing operationId", func(cmd *OperationCommand) { cmd.Correlation.OperationID = "" }},
		{"missing labInstanceId", func(cmd *OperationCommand) { cmd.Correlation.LabInstanceID = "" }},
		{"generation zero", func(cmd *OperationCommand) { cmd.Correlation.Generation = 0 }},
		{"generation negative", func(cmd *OperationCommand) { cmd.Correlation.Generation = -1 }},
		{"unknown mutation", func(cmd *OperationCommand) { cmd.Payload.MutationType = "DESTROY" }},
		{"empty mutation", func(cmd *OperationCommand) { cmd.Payload = protocol.OperationCommandPayload{} }},
		{"provision without snapshot", func(cmd *OperationCommand) { cmd.Payload.CreationSnapshot = nil }},
		{"reset without snapshot", func(cmd *OperationCommand) { cmd.Payload = reset }},
		{"cleanup with providerResources missing (nil, not an empty list)", func(cmd *OperationCommand) {
			cmd.Payload = protocol.OperationCommandPayload{MutationType: protocol.MutationTypeCleanup}
		}},
		{"cleanup resource without generation", func(cmd *OperationCommand) { cmd.Payload = badRef }},
		{"snapshot without vms", func(cmd *OperationCommand) { cmd.Payload = noVMs }},
		{"vm without flavorSpec", func(cmd *OperationCommand) { cmd.Payload = noFlavor }},
		{"vm imageRef not in contract", func(cmd *OperationCommand) { cmd.Payload = withRef }},
		{"workspaceVmKey not in vms", func(cmd *OperationCommand) { cmd.Payload = noWorkspace }},
		{"startup script bad sha256", func(cmd *OperationCommand) { cmd.Payload = badScript }},
		{"negative instanceIndex", func(cmd *OperationCommand) { cmd.Payload = badIndex }},
		{"message over 1 MiB", func(cmd *OperationCommand) { cmd.Payload = tooBig }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newRouterFixture(t)
			principal := principalOf(uuid.New())
			_, frames := f.connect(principal)
			cmd := commandFor(principal.ConnectorID, corr("op-1", "lab-A", 1))
			tt.mut(&cmd)
			if _, err := f.router.SendOperationCommand(context.Background(), cmd); !errors.Is(err, ErrInvalidCommand) {
				t.Fatalf("err = %v, want ErrInvalidCommand", err)
			}
			if frames.count() != 0 || pendingOps(f, principal.ConnectorID) != 0 {
				t.Fatalf("잘못된 command가 전송/등록됨: writes %d, pending %d", frames.count(), pendingOps(f, principal.ConnectorID))
			}
		})
	}

	t.Run("reconcile", func(t *testing.T) {
		f := newRouterFixture(t)
		principal := principalOf(uuid.New())
		_, frames := f.connect(principal)
		for _, mut := range []func(*ReconcileRequest){
			func(r *ReconcileRequest) { r.Correlation.Generation = 0 },
			func(r *ReconcileRequest) { r.Correlation.OperationID = "" },
			func(r *ReconcileRequest) { r.Payload.KnownResources[0].Generation = 0 },
			func(r *ReconcileRequest) { r.Payload.KnownResources[0].ProviderID = "" },
		} {
			req := reconcileFor(principal.ConnectorID, corr("op-1", "lab-A", 1))
			mut(&req)
			if _, err := f.router.SendReconcileRequest(context.Background(), req); !errors.Is(err, ErrInvalidCommand) {
				t.Fatalf("err = %v, want ErrInvalidCommand", err)
			}
		}
		if _, recs := f.router.PendingCount(principal.ConnectorID); frames.count() != 0 || recs != 0 {
			t.Fatalf("writes %d, pending %d", frames.count(), recs)
		}
	})
}

// 정상 CLEANUP과 RESET도 계약대로 직렬화된다.
func TestCleanupAndResetCommandsAreSent(t *testing.T) {
	f := newRouterFixture(t)
	principal := principalOf(uuid.New())
	_, frames := f.connect(principal)
	refs := []protocol.ProviderResourceRef{{ResourceType: "SERVER", ProviderID: "srv-1", Generation: 2}}

	cleanup := commandFor(principal.ConnectorID, corr("op-1", "lab-A", 2))
	cleanup.Payload = protocol.OperationCommandPayload{MutationType: protocol.MutationTypeCleanup, ProviderResources: refs}
	mustSend(t, f, cleanup)
	if msg := decodeCommand(t, frames.last(t)); msg.Payload.MutationType != protocol.MutationTypeCleanup || len(msg.Payload.ProviderResources) != 1 {
		t.Fatalf("cleanup frame = %+v", msg.Payload)
	}

	reset := commandFor(principal.ConnectorID, corr("op-2", "lab-B", 3))
	reset.Payload = provisionPayload()
	reset.Payload.MutationType = protocol.MutationTypeReset
	reset.Payload.ProviderResources = refs
	mustSend(t, f, reset)
	if msg := decodeCommand(t, frames.last(t)); msg.Payload.MutationType != protocol.MutationTypeReset || msg.Payload.CreationSnapshot == nil {
		t.Fatalf("reset frame = %+v", msg.Payload)
	}
}

// rawPayload는 frame의 payload member를 raw JSON으로 반환한다. 구조체 decode로는 property가 사라졌는지 null인지 알 수 없다.
func rawPayload(t *testing.T, frame []byte) map[string]json.RawMessage {
	t.Helper()
	var envelope struct {
		Payload map[string]json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(frame, &envelope); err != nil {
		t.Fatalf("frame이 JSON이 아님: %v: %s", err, frame)
	}
	return envelope.Payload
}

// CLEANUP의 providerResources는 property가 required이고 array에 minItems가 없다(connector.schema.json).
// 정리할 추적 리소스가 없는 빈 CLEANUP도 유효하므로 거절하지 않고, wire에 `"providerResources": []`가 실제로 있어야 한다.
func TestEmptyCleanupIsSentWithRequiredEmptyArray(t *testing.T) {
	f := newRouterFixture(t)
	principal := principalOf(uuid.New())
	_, frames := f.connect(principal)
	cid := principal.ConnectorID
	c := corr("op-1", "lab-A", 2)

	cmd := commandFor(cid, c)
	cmd.Payload = protocol.OperationCommandPayload{MutationType: protocol.MutationTypeCleanup, ProviderResources: []protocol.ProviderResourceRef{}}
	sent, err := f.router.SendOperationCommand(context.Background(), cmd)
	if err != nil {
		t.Fatalf("빈 providerResources의 CLEANUP이 거절됨: %v", err)
	}
	if frames.count() != 1 || pendingOps(f, cid) != 1 {
		t.Fatalf("writes %d, pending %d, want 1, 1", frames.count(), pendingOps(f, cid))
	}

	payload := rawPayload(t, frames.last(t))
	raw, present := payload["providerResources"]
	if !present {
		t.Fatalf("providerResources property가 wire에서 사라짐: %v", payload)
	}
	if string(raw) != "[]" { // null이 아니라 빈 배열이어야 한다.
		t.Fatalf("providerResources = %s, want []", raw)
	}
	if string(payload["mutationType"]) != `"CLEANUP"` {
		t.Fatalf("mutationType = %s", payload["mutationType"])
	}
	if _, present := payload["creationSnapshot"]; present {
		t.Fatal("CLEANUP에 creationSnapshot이 실림")
	}

	// 빈 CLEANUP도 기존 pending으로 정확히 돌아온다.
	if !f.router.RouteOperationAck(cid, replyFor(sent, c), protocol.OperationAckPayload{Accepted: true}) ||
		!f.router.RouteOperationResult(cid, replyFor(sent, c), protocol.OperationResultPayload{Outcome: protocol.OutcomeSucceeded, ProviderResources: []protocol.ProviderResourceResult{}}) {
		t.Fatal("빈 CLEANUP의 ACK/RESULT가 pending에 연결되지 않음")
	}
	events := f.sink.all()
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
	if ack, ok := events[0].(OperationAckEvent); !ok || ack.RequestMessageID != sent.MessageID || ack.Correlation != c {
		t.Fatalf("ACK event = %+v", events[0])
	}
	if res, ok := events[1].(OperationResultEvent); !ok || res.RequestMessageID != sent.MessageID || res.Correlation != c {
		t.Fatalf("RESULT event = %+v", events[1])
	}
	if pendingOps(f, cid) != 0 {
		t.Fatalf("terminal result 뒤 pending = %d", pendingOps(f, cid))
	}
}

// startupScript.content는 string이며 minLength가 없다. 빈 content의 script도 유효하므로 거절하지 않고, required인 content와
// sha256이 wire에 그대로 실려야 한다. 이 검증은 sha256의 Schema pattern을 약화하지 않는다.
func TestEmptyStartupScriptContentIsSentAndDigestConstraintIsKept(t *testing.T) {
	f := newRouterFixture(t)
	principal := principalOf(uuid.New())
	_, frames := f.connect(principal)
	cid := principal.ConnectorID
	const emptyDigest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	cmd := commandFor(cid, corr("op-1", "lab-A", 1))
	cmd.Payload.CreationSnapshot.StartupScript = &protocol.StartupScriptSnapshot{Content: "", SHA256: emptyDigest}
	mustSend(t, f, cmd)

	var snapshot map[string]json.RawMessage
	if err := json.Unmarshal(rawPayload(t, frames.last(t))["creationSnapshot"], &snapshot); err != nil {
		t.Fatal(err)
	}
	var script map[string]json.RawMessage
	if err := json.Unmarshal(snapshot["startupScript"], &script); err != nil {
		t.Fatalf("startupScript가 wire에서 사라짐: %v", err)
	}
	if string(script["content"]) != `""` || string(script["sha256"]) != `"`+emptyDigest+`"` {
		t.Fatalf("startupScript = %v, want content \"\" + sha256", script)
	}

	// sha256 제약은 그대로다. 빈 content여도 64자리 hex가 아니면 거절한다.
	for _, digest := range []string{"", "abc", strings.Repeat("g", 64), strings.Repeat("a", 63)} {
		bad := commandFor(cid, corr("op-2", "lab-B", 1))
		bad.Payload.CreationSnapshot.StartupScript = &protocol.StartupScriptSnapshot{Content: "", SHA256: digest}
		if _, err := f.router.SendOperationCommand(context.Background(), bad); !errors.Is(err, ErrInvalidCommand) {
			t.Fatalf("sha256 %q: err = %v, want ErrInvalidCommand", digest, err)
		}
	}
	if frames.count() != 1 {
		t.Fatalf("writes = %d, want 1(잘못된 sha256은 보내지 않는다)", frames.count())
	}
}

// PROVISION/RESET의 providerResources는 optional이다. nil이면 property 자체를 만들지 않고(null도 아님), 값이 있으면 그대로 싣는다.
func TestOptionalProviderResourcesAreOmittedWhenNilAndKeptWhenSet(t *testing.T) {
	f := newRouterFixture(t)
	principal := principalOf(uuid.New())
	_, frames := f.connect(principal)
	cid := principal.ConnectorID

	mustSend(t, f, commandFor(cid, corr("op-1", "lab-A", 1))) // PROVISION, ProviderResources nil
	if payload := rawPayload(t, frames.last(t)); payload["providerResources"] != nil {
		t.Fatalf("PROVISION의 nil providerResources가 wire에 %s로 나감", payload["providerResources"])
	}

	reset := commandFor(cid, corr("op-2", "lab-B", 3))
	reset.Payload.MutationType = protocol.MutationTypeReset
	reset.Payload.ProviderResources = []protocol.ProviderResourceRef{{ResourceType: "SERVER", ProviderID: "srv-old", Generation: 2}}
	mustSend(t, f, reset)
	if got := string(rawPayload(t, frames.last(t))["providerResources"]); got != `[{"resourceType":"SERVER","providerId":"srv-old","generation":2}]` {
		t.Fatalf("RESET의 providerResources = %s", got)
	}
}

func TestCanceledContextSendsNothing(t *testing.T) {
	f := newRouterFixture(t)
	principal := principalOf(uuid.New())
	_, frames := f.connect(principal)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.router.SendOperationCommand(ctx, commandFor(principal.ConnectorID, corr("op-1", "lab-A", 1))); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if _, err := f.router.SendReconcileRequest(ctx, reconcileFor(principal.ConnectorID, corr("op-1", "lab-A", 1))); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if ops, recs := f.router.PendingCount(principal.ConnectorID); frames.count() != 0 || ops != 0 || recs != 0 {
		t.Fatalf("writes %d, pending %d/%d", frames.count(), ops, recs)
	}
}

// sink가 event 안에서 같은 Router로 전송해도 교착하지 않는다(event는 lock 밖에서 전달한다).
func TestSinkMaySendFromInsideEvent(t *testing.T) {
	registry := NewRegistry()
	principal := principalOf(uuid.New())
	frames := &frameLog{}
	registry.Register(principal, nil).MarkReady(frames.route)

	var router *Router
	done := make(chan error, 1)
	sink := sinkFunc(func(e Event) {
		if _, ok := e.(OperationAckEvent); ok {
			_, err := router.SendOperationCommand(context.Background(), commandFor(principal.ConnectorID, corr("op-2", "lab-B", 1)))
			done <- err
		}
	})
	router, err := NewRouter(RouterOptions{Registry: registry, Sink: sink})
	if err != nil {
		t.Fatal(err)
	}
	c := corr("op-1", "lab-A", 1)
	sent, err := router.SendOperationCommand(context.Background(), commandFor(principal.ConnectorID, c))
	if err != nil {
		t.Fatal(err)
	}
	go router.RouteOperationAck(principal.ConnectorID, replyFor(sent, c), protocol.OperationAckPayload{Accepted: true})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("event 안의 전송 = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sink 안에서의 전송이 교착함")
	}
}

type sinkFunc func(Event)

func (f sinkFunc) HandleEvent(e Event) { f(e) }

// 동시에 많은 command를 보내고 즉시 응답이 도착해도 모든 event가 정확히 하나의 command에 한 번씩 연결된다(-race).
func TestConcurrentSendsAndImmediateResponses(t *testing.T) {
	const commands = 64
	f := newRouterFixture(t)
	principal := principalOf(uuid.New())
	registration := f.registry.Register(principal, nil)
	registration.MarkReady(func(data []byte) error {
		msg := decodeCommand(t, data)
		in := Inbound{ReplyToMessageID: msg.MessageID, OperationID: msg.OperationID, LabInstanceID: msg.LabInstanceID, Generation: msg.Generation}
		f.router.RouteOperationAck(principal.ConnectorID, in, protocol.OperationAckPayload{Accepted: true})
		in.ReplyToMessageID = ""
		f.router.RouteOperationResult(principal.ConnectorID, in, protocol.OperationResultPayload{Outcome: protocol.OutcomeSucceeded})
		return nil
	})

	var wg sync.WaitGroup
	for i := 0; i < commands; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := corr("op-1", fmt.Sprintf("lab-%d", i), int64(i+1))
			if _, err := f.router.SendOperationCommand(context.Background(), commandFor(principal.ConnectorID, c)); err != nil {
				t.Errorf("SendOperationCommand() error = %v", err)
			}
		}()
	}
	wg.Wait()

	acks, results := map[Correlation]int{}, map[Correlation]int{}
	for _, event := range f.sink.all() {
		switch e := event.(type) {
		case OperationAckEvent:
			acks[e.Correlation]++
		case OperationResultEvent:
			results[e.Correlation]++
		default:
			t.Fatalf("예상하지 못한 event: %+v", event)
		}
	}
	if len(acks) != commands || len(results) != commands {
		t.Fatalf("ACK %d개, RESULT %d개 correlation, want %d씩", len(acks), len(results), commands)
	}
	for c, n := range acks {
		if n != 1 || results[c] != 1 {
			t.Fatalf("%+v: ACK %d번, RESULT %d번", c, n, results[c])
		}
	}
	if got := pendingOps(f, principal.ConnectorID); got != 0 {
		t.Fatalf("pending = %d, want 0", got)
	}
}

// unmatched 진단은 payload, Connector가 준 error 문구, 긴 ID 전체를 log와 event에 남기지 않는다.
func TestUnmatchedDiagnosticsDoNotLeakPayloadOrUnboundedIDs(t *testing.T) {
	f := newRouterFixture(t)
	principal := principalOf(uuid.New())
	const marker = "provider-raw-secret-marker-7c1e"
	longID := strings.Repeat("L", 100_000)

	f.router.RouteOperationResult(principal.ConnectorID,
		Inbound{OperationID: longID, LabInstanceID: "lab-A", Generation: 1},
		protocol.OperationResultPayload{
			Outcome: protocol.OutcomeFailed,
			Error:   &protocol.SafeError{Code: "PROVIDER_FAILED", Message: marker},
			ProviderResources: []protocol.ProviderResourceResult{
				{ProviderResourceRef: protocol.ProviderResourceRef{ResourceType: "SERVER", ProviderID: marker, Generation: 1}},
			},
		})

	event := wantUnmatched(t, f.sink.only(t), principal.ConnectorID, protocol.MessageTypeOperationResult, ReasonNoPending)
	if len(event.OperationID) > maxLoggedIDLen {
		t.Fatalf("event의 operationId 길이 = %d, want <= %d", len(event.OperationID), maxLoggedIDLen)
	}
	logs := f.logs.String()
	if strings.Contains(logs, marker) || strings.Contains(logs, longID) {
		t.Fatalf("log에 payload 또는 긴 ID가 남음: %.300s", logs)
	}
	if !strings.Contains(logs, "no_pending") {
		t.Fatalf("unmatched 진단이 log에 없음: %s", logs)
	}
}

// 전송 실패 log에는 오류 원문과 command payload를 남기지 않고 고정 분류만 남긴다.
func TestSendFailureLogsOnlyFixedReason(t *testing.T) {
	f := newRouterFixture(t)
	principal := principalOf(uuid.New())
	const secret = "tcp-error-detail-marker-91b3"
	f.registry.Register(principal, nil).MarkReady(func([]byte) error { return errors.New(secret) })
	cmd := commandFor(principal.ConnectorID, corr("op-1", "lab-A", 1))
	cmd.Payload.CreationSnapshot.VMs[0].VMKey = "payload-marker-vm-4d2a"
	cmd.Payload.CreationSnapshot.WorkspaceVMKey = "payload-marker-vm-4d2a"

	if _, err := f.router.SendOperationCommand(context.Background(), cmd); !errors.Is(err, ErrSendFailed) {
		t.Fatalf("err = %v", err)
	}
	logs := f.logs.String()
	if strings.Contains(logs, secret) || strings.Contains(logs, "payload-marker-vm-4d2a") || strings.Contains(logs, "img-1") {
		t.Fatalf("log에 오류 원문 또는 payload가 남음: %s", logs)
	}
	if !strings.Contains(logs, "write_failed") || !strings.Contains(logs, principal.ConnectorID.String()) || !strings.Contains(logs, "op-1") {
		t.Fatalf("log에 correlation 또는 분류가 없음: %s", logs)
	}
}
