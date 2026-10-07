package connector

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

// previewRecorder는 Router가 넘긴 Preview event를 순서대로 기록한다.
type previewRecorder struct {
	mu     sync.Mutex
	events []PreviewEvent
}

func (r *previewRecorder) HandlePreviewEvent(e PreviewEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *previewRecorder) all() []PreviewEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]PreviewEvent(nil), r.events...)
}

func (r *previewRecorder) only(t *testing.T) PreviewEvent {
	t.Helper()
	events := r.all()
	if len(events) != 1 {
		t.Fatalf("event %d개 = %+v, want 1개", len(events), events)
	}
	return events[0]
}

type previewFixture struct {
	t        *testing.T
	registry *Registry
	sink     *previewRecorder
	router   *Router
	logs     *syncLog
}

func newPreviewFixture(t *testing.T) *previewFixture {
	t.Helper()
	registry := NewRegistry()
	sink := &previewRecorder{}
	logs := &syncLog{}
	router, err := NewRouter(RouterOptions{
		Registry:    registry,
		PreviewSink: sink,
		Logger:      slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &previewFixture{t: t, registry: registry, sink: sink, router: router, logs: logs}
}

// connect는 principal의 Connector를 HELLO에서 capabilities를 선언한 protocol-ready Session으로 연결한다.
func (f *previewFixture) connect(principal Principal, capabilities []string) (*Registration, *frameLog) {
	f.t.Helper()
	frames := &frameLog{}
	registration := f.registry.Register(principal, nil)
	registration.SetCapabilities(capabilities)
	if !registration.MarkReady(frames.route) {
		f.t.Fatal("MarkReady가 거절됨")
	}
	return registration, frames
}

var previewV1 = []string{protocol.CapabilityPreviewV1}

func previewCorr(session string) PreviewCorrelation {
	return PreviewCorrelation{PreviewSessionID: session, LabInstanceID: "lab-1", Generation: 3}
}

func previewOpenFor(connectorID uuid.UUID, session string) PreviewOpen {
	return PreviewOpen{
		ConnectorID: connectorID, RequestID: "request-1", Correlation: previewCorr(session),
		TargetVMKey: "vk-web", ProviderServerID: "srv-web-g3", TargetPort: 5173,
	}
}

func previewInboundFor(replyTo string, c PreviewCorrelation) PreviewInbound {
	return PreviewInbound{MessageID: "inbound-1", ReplyToMessageID: replyTo, Correlation: c}
}

// PREVIEW_OPEN은 preview-control.schema.json의 Envelope와 payload(resolved target과 승인된 port뿐)를 그대로 싣는다.
// Cookie, bootstrap credential, 사설 IP는 Control에 실리지 않는다.
func TestSendPreviewOpenWritesContractMessage(t *testing.T) {
	f := newPreviewFixture(t)
	p := newPrincipal()
	_, frames := f.connect(p, previewV1)

	sent, err := f.router.SendPreviewOpen(context.Background(), previewOpenFor(p.ConnectorID, "preview-1"))
	if err != nil {
		t.Fatalf("SendPreviewOpen() error = %v", err)
	}
	if frames.count() != 1 {
		t.Fatalf("frame = %d, want 1", frames.count())
	}
	raw := frames.last(t)
	msg := decodeFrame(t, raw)
	payload, _ := msg["payload"].(map[string]any)
	if msg["type"] != "PREVIEW_OPEN" || msg["messageId"] != sent.MessageID || msg["requestId"] != "request-1" ||
		msg["previewSessionId"] != "preview-1" || msg["labInstanceId"] != "lab-1" || msg["generation"] != float64(3) {
		t.Fatalf("envelope = %v", msg)
	}
	if _, err := uuid.Parse(sent.MessageID); err != nil {
		t.Fatalf("messageId = %q", sent.MessageID)
	}
	if payload["targetVmKey"] != "vk-web" || payload["providerServerId"] != "srv-web-g3" || payload["targetPort"] != float64(5173) || len(payload) != 3 {
		t.Fatalf("payload = %v, want only the resolved target and the approved port", payload)
	}
	// connectorId는 인증된 connection에서 SaaS가 정한다. message에 싣지 않는다.
	if strings.Contains(string(raw), p.ConnectorID.String()) || msg["connectorId"] != nil {
		t.Fatalf("PREVIEW_OPEN이 connector id를 실음: %s", raw)
	}
	if f.router.PendingPreviewOpens(p.ConnectorID) != 1 {
		t.Fatalf("PendingPreviewOpens = %d, want 1", f.router.PendingPreviewOpens(p.ConnectorID))
	}
}

func TestSendPreviewOpenCarriesAValidTraceContextOnly(t *testing.T) {
	f := newPreviewFixture(t)
	p := newPrincipal()
	_, frames := f.connect(p, previewV1)

	open := previewOpenFor(p.ConnectorID, "preview-1")
	open.Trace = TraceContext{Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}
	if _, err := f.router.SendPreviewOpen(context.Background(), open); err != nil {
		t.Fatal(err)
	}
	if got := decodeFrame(t, frames.last(t))["traceparent"]; got != open.Trace.Traceparent {
		t.Fatalf("traceparent = %v", got)
	}

	open = previewOpenFor(p.ConnectorID, "preview-2")
	open.Trace = TraceContext{Traceparent: "not-a-trace-parent", Tracestate: "k=v"}
	if _, err := f.router.SendPreviewOpen(context.Background(), open); err != nil {
		t.Fatalf("유효하지 않은 Trace가 요청을 실패시킴: %v", err)
	}
	msg := decodeFrame(t, frames.last(t))
	if _, ok := msg["traceparent"]; ok {
		t.Fatalf("유효하지 않은 traceparent를 전달함: %v", msg)
	}
	if _, ok := msg["tracestate"]; ok {
		t.Fatalf("traceparent 없는 tracestate를 전달함: %v", msg)
	}
}

// SaaS는 HELLO에서 preview-v1을 선언한 Connector에게만 Preview Control message를 보낸다. 버전으로 추론하지 않는다.
func TestPreviewControlOnlyGoesToConnectorsThatDeclaredTheCapability(t *testing.T) {
	cases := []struct {
		name         string
		capabilities []string
	}{
		{"capabilities field가 없는 기존 Connector", nil},
		{"빈 capabilities", []string{}},
		{"다른 capability만 선언", []string{"terminal", "file-v1"}},
		{"비슷한 이름", []string{"preview", "preview-v2", "PREVIEW-V1", "preview-v1 "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newPreviewFixture(t)
			p := newPrincipal()
			_, frames := f.connect(p, tc.capabilities)

			_, err := f.router.SendPreviewOpen(context.Background(), previewOpenFor(p.ConnectorID, "preview-1"))
			if !errors.Is(err, ErrCapabilityUnsupported) {
				t.Fatalf("SendPreviewOpen() error = %v, want ErrCapabilityUnsupported", err)
			}
			// 연결되어 있으므로 Connector 단절이 아니다. 호출자가 둘을 구분할 수 있어야 한다.
			if errors.Is(err, ErrConnectorUnavailable) {
				t.Fatal("capability 미선언을 Connector 단절로 보고함")
			}
			if _, err := f.router.SendPreviewClose(context.Background(), PreviewClose{ConnectorID: p.ConnectorID, Correlation: previewCorr("preview-1"), Reason: "SESSION_CLOSED"}); !errors.Is(err, ErrCapabilityUnsupported) {
				t.Fatalf("SendPreviewClose() error = %v, want ErrCapabilityUnsupported", err)
			}
			if frames.count() != 0 {
				t.Fatalf("capability 없는 Connector에 frame %d개를 보냄", frames.count())
			}
			if f.router.PendingPreviewOpens(p.ConnectorID) != 0 {
				t.Fatal("보내지 않은 PREVIEW_OPEN의 pending이 남음")
			}
			if !strings.Contains(f.logs.String(), "capability_unsupported") {
				t.Fatalf("거절 사유가 기록되지 않음: %s", f.logs.String())
			}
		})
	}
}

// file-v1만 선언한 Connector는 Preview를 지원하지 않는 것이다. capability는 기능마다 독립이다.
func TestPreviewCapabilityIsIndependentOfFileCapability(t *testing.T) {
	f := newPreviewFixture(t)
	p := newPrincipal()
	_, frames := f.connect(p, []string{protocol.CapabilityFileV1})
	if _, err := f.router.SendPreviewOpen(context.Background(), previewOpenFor(p.ConnectorID, "preview-1")); !errors.Is(err, ErrCapabilityUnsupported) {
		t.Fatalf("SendPreviewOpen() error = %v, want ErrCapabilityUnsupported", err)
	}
	if frames.count() != 0 {
		t.Fatal("frame을 보냄")
	}
}

// capability는 connection(Session)마다 HELLO로 선언한다. 재접속한 새 Session이 선언하지 않았다면 이전 Session의 선언을 물려받지 않는다.
func TestPreviewCapabilityBelongsToTheSessionThatDeclaredIt(t *testing.T) {
	f := newPreviewFixture(t)
	p := newPrincipal()
	f.connect(p, previewV1)
	if _, err := f.router.SendPreviewOpen(context.Background(), previewOpenFor(p.ConnectorID, "preview-1")); err != nil {
		t.Fatalf("SendPreviewOpen() error = %v", err)
	}

	_, frames := f.connect(p, nil)
	_, err := f.router.SendPreviewOpen(context.Background(), previewOpenFor(p.ConnectorID, "preview-2"))
	if !errors.Is(err, ErrCapabilityUnsupported) {
		t.Fatalf("SendPreviewOpen() error = %v, want ErrCapabilityUnsupported", err)
	}
	if frames.count() != 0 {
		t.Fatal("capability를 선언하지 않은 새 Session에 frame을 보냄")
	}
}

func TestPreviewOpenNeedsAProtocolReadySession(t *testing.T) {
	f := newPreviewFixture(t)
	p := newPrincipal()
	if _, err := f.router.SendPreviewOpen(context.Background(), previewOpenFor(p.ConnectorID, "preview-1")); !errors.Is(err, ErrNotConnected) || !errors.Is(err, ErrConnectorUnavailable) {
		t.Fatalf("미연결 error = %v", err)
	}

	registration := f.registry.Register(p, nil)
	registration.SetCapabilities(previewV1)
	if _, err := f.router.SendPreviewOpen(context.Background(), previewOpenFor(p.ConnectorID, "preview-1")); !errors.Is(err, ErrNotReady) || !errors.Is(err, ErrConnectorUnavailable) {
		t.Fatalf("HELLO_ACK 전 error = %v", err)
	}
	if f.router.PendingPreviewOpens(p.ConnectorID) != 0 {
		t.Fatal("보내지 않은 PREVIEW_OPEN의 pending이 남음")
	}
}

func TestPreviewOpenWithAnInvalidRequestIsNotSent(t *testing.T) {
	cases := map[string]func(*PreviewOpen){
		"previewSessionId 없음": func(o *PreviewOpen) { o.Correlation.PreviewSessionID = "" },
		"labInstanceId 없음":    func(o *PreviewOpen) { o.Correlation.LabInstanceID = "" },
		"generation 0":        func(o *PreviewOpen) { o.Correlation.Generation = 0 },
		"generation 음수":       func(o *PreviewOpen) { o.Correlation.Generation = -1 },
		"targetVmKey 없음":      func(o *PreviewOpen) { o.TargetVMKey = "" },
		"providerServerId 없음": func(o *PreviewOpen) { o.ProviderServerID = "" },
		"targetPort 0":        func(o *PreviewOpen) { o.TargetPort = 0 },
		"targetPort 음수":       func(o *PreviewOpen) { o.TargetPort = -1 },
		"targetPort 65536":    func(o *PreviewOpen) { o.TargetPort = 65536 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newPreviewFixture(t)
			p := newPrincipal()
			_, frames := f.connect(p, previewV1)
			open := previewOpenFor(p.ConnectorID, "preview-1")
			mutate(&open)
			if _, err := f.router.SendPreviewOpen(context.Background(), open); !errors.Is(err, ErrInvalidCommand) {
				t.Fatalf("SendPreviewOpen() error = %v, want ErrInvalidCommand", err)
			}
			if frames.count() != 0 || f.router.PendingPreviewOpens(p.ConnectorID) != 0 {
				t.Fatal("계약에 맞지 않는 PREVIEW_OPEN을 보냈거나 pending을 남김")
			}
		})
	}
}

func TestPreviewOpenWithACanceledContextIsNotSent(t *testing.T) {
	f := newPreviewFixture(t)
	p := newPrincipal()
	_, frames := f.connect(p, previewV1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.router.SendPreviewOpen(ctx, previewOpenFor(p.ConnectorID, "preview-1")); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if frames.count() != 0 || f.router.PendingPreviewOpens(p.ConnectorID) != 0 {
		t.Fatal("취소된 요청을 보냈거나 pending을 남김")
	}
}

func TestDuplicatePreviewSessionIDIsRejectedWithoutASecondFrame(t *testing.T) {
	f := newPreviewFixture(t)
	p := newPrincipal()
	_, frames := f.connect(p, previewV1)
	if _, err := f.router.SendPreviewOpen(context.Background(), previewOpenFor(p.ConnectorID, "preview-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.router.SendPreviewOpen(context.Background(), previewOpenFor(p.ConnectorID, "preview-1")); !errors.Is(err, ErrDuplicateCorrelation) {
		t.Fatalf("중복 PREVIEW_OPEN error = %v, want ErrDuplicateCorrelation", err)
	}
	if frames.count() != 1 || f.router.PendingPreviewOpens(p.ConnectorID) != 1 {
		t.Fatalf("frames=%d pending=%d, want 1/1", frames.count(), f.router.PendingPreviewOpens(p.ConnectorID))
	}
}

// write가 실패하면 방금 등록한 pending을 되돌리고 자동으로 재전송하지 않는다.
func TestPreviewOpenWriteFailureRollsBackThePending(t *testing.T) {
	f := newPreviewFixture(t)
	p := newPrincipal()
	writes := 0
	registration := f.registry.Register(p, nil)
	registration.SetCapabilities(previewV1)
	registration.MarkReady(func([]byte) error {
		writes++
		return errors.New("broken pipe")
	})

	_, err := f.router.SendPreviewOpen(context.Background(), previewOpenFor(p.ConnectorID, "preview-1"))
	if !errors.Is(err, ErrSendFailed) {
		t.Fatalf("SendPreviewOpen() error = %v, want ErrSendFailed", err)
	}
	if writes != 1 {
		t.Fatalf("write %d번, 자동 재전송 금지", writes)
	}
	if f.router.PendingPreviewOpens(p.ConnectorID) != 0 {
		t.Fatal("실패한 PREVIEW_OPEN의 pending이 남음")
	}
	if strings.Contains(f.logs.String(), "broken pipe") {
		t.Fatalf("transport 오류 원문을 기록함: %s", f.logs.String())
	}
}

// OnRoute는 PREVIEW_OPEN을 받을 정확한 Control Session으로, write보다 먼저 호출된다. 호출자가 그 Session을 기록한 뒤에 Connector가
// message를 받으므로 Connector가 Data WSS를 붙이는 순간에는 binding이 이미 있다.
func TestPreviewOpenCallsOnRouteWithTheExactSessionBeforeWriting(t *testing.T) {
	f := newPreviewFixture(t)
	p := newPrincipal()

	var order []string
	registration := f.registry.Register(p, nil)
	registration.SetCapabilities(previewV1)
	registration.MarkReady(func([]byte) error {
		order = append(order, "write")
		return nil
	})

	var got Session
	open := previewOpenFor(p.ConnectorID, "preview-1")
	open.OnRoute = func(s Session) error {
		order = append(order, "on_route")
		got = s
		return nil
	}
	if _, err := f.router.SendPreviewOpen(context.Background(), open); err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "on_route,write" {
		t.Fatalf("순서 = %v, want on_route 다음에 write", order)
	}
	if got != registration.Session() || got.ConnectorID != p.ConnectorID || got.CredentialID != p.CredentialID {
		t.Fatalf("OnRoute Session = %+v, want %+v", got, registration.Session())
	}
}

// OnRoute가 오류를 반환하면 아무것도 쓰지 않고 pending도 만들지 않는다.
func TestPreviewOpenOnRouteErrorSendsNothing(t *testing.T) {
	f := newPreviewFixture(t)
	p := newPrincipal()
	_, frames := f.connect(p, previewV1)
	boom := errors.New("not trusted")
	open := previewOpenFor(p.ConnectorID, "preview-1")
	open.OnRoute = func(Session) error { return boom }
	if _, err := f.router.SendPreviewOpen(context.Background(), open); !errors.Is(err, boom) {
		t.Fatalf("error = %v, want OnRoute 오류", err)
	}
	if frames.count() != 0 || f.router.PendingPreviewOpens(p.ConnectorID) != 0 {
		t.Fatal("OnRoute가 거절했는데 보냈거나 pending을 남김")
	}
	// 같은 previewSessionId로 다시 보낼 수 있다(pending이 남지 않았다).
	open.OnRoute = nil
	if _, err := f.router.SendPreviewOpen(context.Background(), open); err != nil {
		t.Fatalf("재시도 error = %v", err)
	}
}

func TestSendPreviewCloseIsAnIdempotentNotificationWithoutPending(t *testing.T) {
	f := newPreviewFixture(t)
	p := newPrincipal()
	_, frames := f.connect(p, previewV1)

	cl := PreviewClose{ConnectorID: p.ConnectorID, RequestID: "request-1", Correlation: previewCorr("preview-1"), Reason: "SESSION_EXPIRED"}
	sent, err := f.router.SendPreviewClose(context.Background(), cl)
	if err != nil {
		t.Fatalf("SendPreviewClose() error = %v", err)
	}
	// 반복 전달해도 오류가 아니다.
	if _, err := f.router.SendPreviewClose(context.Background(), cl); err != nil {
		t.Fatalf("반복 SendPreviewClose() error = %v", err)
	}
	msg := decodeFrame(t, frames.last(t))
	payload, _ := msg["payload"].(map[string]any)
	if msg["type"] != "PREVIEW_CLOSE" || msg["previewSessionId"] != "preview-1" || msg["labInstanceId"] != "lab-1" || msg["generation"] != float64(3) ||
		payload["reason"] != "SESSION_EXPIRED" || len(payload) != 1 || sent.MessageID == "" {
		t.Fatalf("PREVIEW_CLOSE = %v", msg)
	}
	if f.router.PendingPreviewOpens(p.ConnectorID) != 0 {
		t.Fatal("PREVIEW_CLOSE가 pending을 만듦")
	}

	cl.Reason = ""
	if _, err := f.router.SendPreviewClose(context.Background(), cl); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("reason 없는 PREVIEW_CLOSE error = %v, want ErrInvalidCommand", err)
	}
	cl.Reason, cl.Correlation.Generation = "X", 0
	if _, err := f.router.SendPreviewClose(context.Background(), cl); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("generation 없는 PREVIEW_CLOSE error = %v, want ErrInvalidCommand", err)
	}
}

func TestPreviewOpenResultConnectsToItsPendingOpenAndEndsIt(t *testing.T) {
	f := newPreviewFixture(t)
	p := newPrincipal()
	f.connect(p, previewV1)
	sent, err := f.router.SendPreviewOpen(context.Background(), previewOpenFor(p.ConnectorID, "preview-1"))
	if err != nil {
		t.Fatal(err)
	}

	in := previewInboundFor(sent.MessageID, previewCorr("preview-1"))
	if !f.router.RoutePreviewOpenResult(p.ConnectorID, in, PreviewOpenResultPayload{Outcome: PreviewOutcomeFailed, Error: &protocol.SafeError{Code: "APP_NOT_RUNNING"}}) {
		t.Fatal("정상 PREVIEW_OPEN_RESULT가 연결되지 않음")
	}
	event, ok := f.sink.only(t).(PreviewOpenResultEvent)
	if !ok {
		t.Fatalf("event = %+v", f.sink.all()[0])
	}
	if event.ConnectorID != p.ConnectorID || event.RequestMessageID != sent.MessageID || event.RequestID != "request-1" ||
		event.Correlation != previewCorr("preview-1") || event.Payload.Outcome != PreviewOutcomeFailed || event.Payload.Error.Code != "APP_NOT_RUNNING" {
		t.Fatalf("event = %+v", event)
	}
	if f.router.PendingPreviewOpens(p.ConnectorID) != 0 {
		t.Fatal("결과를 받은 PREVIEW_OPEN의 pending이 남음")
	}

	// 같은 결과를 다시 받아도 어떤 PreviewSession도 완료시키지 않는다.
	if f.router.RoutePreviewOpenResult(p.ConnectorID, in, PreviewOpenResultPayload{Outcome: PreviewOutcomeFailed}) {
		t.Fatal("끝난 PREVIEW_OPEN에 중복 결과가 연결됨")
	}
	unmatched, ok := f.sink.all()[1].(PreviewUnmatchedEvent)
	if !ok || unmatched.Reason != ReasonNoPending {
		t.Fatalf("중복 결과 event = %+v", f.sink.all()[1])
	}
}

// 인증된 ConnectorID, previewSessionId, labInstanceId, generation, replyToMessageId가 모두 맞는 pending에만 연결한다.
func TestPreviewOpenResultNeverFallsBackToAnotherPending(t *testing.T) {
	f := newPreviewFixture(t)
	a, b := newPrincipal(), newPrincipal()
	f.connect(a, previewV1)
	f.connect(b, previewV1)
	sentA, err := f.router.SendPreviewOpen(context.Background(), previewOpenFor(a.ConnectorID, "preview-1"))
	if err != nil {
		t.Fatal(err)
	}
	sentOther, err := f.router.SendPreviewOpen(context.Background(), previewOpenFor(a.ConnectorID, "preview-2"))
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name        string
		connectorID uuid.UUID
		in          PreviewInbound
		want        UnmatchedReason
	}{
		{"다른 Connector가 같은 correlation을 주장", b.ConnectorID, previewInboundFor(sentA.MessageID, previewCorr("preview-1")), ReasonNoPending},
		{"알 수 없는 previewSessionId", a.ConnectorID, previewInboundFor(sentA.MessageID, previewCorr("preview-ghost")), ReasonNoPending},
		{"다른 PreviewSession의 replyToMessageId", a.ConnectorID, previewInboundFor(sentOther.MessageID, previewCorr("preview-1")), ReasonReplyMismatch},
		{"replyToMessageId 없음", a.ConnectorID, previewInboundFor("", previewCorr("preview-1")), ReasonReplyMismatch},
		{"다른 LabInstance", a.ConnectorID, previewInboundFor(sentA.MessageID, PreviewCorrelation{PreviewSessionID: "preview-1", LabInstanceID: "lab-2", Generation: 3}), ReasonCorrelationMismatch},
		{"다른 generation", a.ConnectorID, previewInboundFor(sentA.MessageID, PreviewCorrelation{PreviewSessionID: "preview-1", LabInstanceID: "lab-1", Generation: 2}), ReasonCorrelationMismatch},
		{"generation이 int64를 넘음", a.ConnectorID, previewInboundFor(sentA.MessageID, PreviewCorrelation{PreviewSessionID: "preview-1", LabInstanceID: "lab-1", Generation: 0}), ReasonUnrepresentable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := len(f.sink.all())
			if f.router.RoutePreviewOpenResult(tc.connectorID, tc.in, PreviewOpenResultPayload{Outcome: PreviewOutcomeFailed}) {
				t.Fatal("맞지 않는 PREVIEW_OPEN_RESULT가 연결됨")
			}
			events := f.sink.all()
			if len(events) != before+1 {
				t.Fatalf("event %d개 추가, want 1개", len(events)-before)
			}
			unmatched, ok := events[before].(PreviewUnmatchedEvent)
			if !ok || unmatched.Reason != tc.want || unmatched.ConnectorID != tc.connectorID {
				t.Fatalf("event = %+v, want unmatched %q", events[before], tc.want)
			}
			// 어느 pending도 끝나지 않았다.
			if f.router.PendingPreviewOpens(a.ConnectorID) != 2 {
				t.Fatalf("PendingPreviewOpens = %d, want 2", f.router.PendingPreviewOpens(a.ConnectorID))
			}
		})
	}
}

// 같은 Connector의 여러 PreviewSession은 독립이다. 결과가 도착하는 순서와 무관하게 각자의 pending에만 연결한다.
func TestConcurrentPreviewSessionsRouteIndependently(t *testing.T) {
	f := newPreviewFixture(t)
	p := newPrincipal()
	f.connect(p, previewV1)

	const n = 8
	sent := make([]SentMessage, n)
	for i := range sent {
		var err error
		sent[i], err = f.router.SendPreviewOpen(context.Background(), previewOpenFor(p.ConnectorID, "preview-"+string(rune('a'+i))))
		if err != nil {
			t.Fatal(err)
		}
	}
	if f.router.PendingPreviewOpens(p.ConnectorID) != n {
		t.Fatalf("pending = %d, want %d", f.router.PendingPreviewOpens(p.ConnectorID), n)
	}

	var wg sync.WaitGroup
	for i := n - 1; i >= 0; i-- { // 보낸 순서의 반대로 응답한다.
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !f.router.RoutePreviewOpenResult(p.ConnectorID, previewInboundFor(sent[i].MessageID, previewCorr("preview-"+string(rune('a'+i)))), PreviewOpenResultPayload{Outcome: PreviewOutcomeFailed}) {
				t.Errorf("PreviewSession %d의 결과가 연결되지 않음", i)
			}
		}()
	}
	wg.Wait()

	seen := map[string]string{}
	for _, e := range f.sink.all() {
		event, ok := e.(PreviewOpenResultEvent)
		if !ok {
			t.Fatalf("event = %+v", e)
		}
		seen[event.Correlation.PreviewSessionID] = event.RequestMessageID
	}
	for i := range sent {
		if seen["preview-"+string(rune('a'+i))] != sent[i].MessageID {
			t.Fatalf("PreviewSession %d의 결과가 다른 PreviewSession의 messageId에 연결됨: %v", i, seen)
		}
	}
	if f.router.PendingPreviewOpens(p.ConnectorID) != 0 {
		t.Fatal("pending이 남음")
	}
}

func TestForgetPreviewOpenDropsTheLocalPendingOnly(t *testing.T) {
	f := newPreviewFixture(t)
	p := newPrincipal()
	_, frames := f.connect(p, previewV1)
	sent, err := f.router.SendPreviewOpen(context.Background(), previewOpenFor(p.ConnectorID, "preview-1"))
	if err != nil {
		t.Fatal(err)
	}
	if !f.router.ForgetPreviewOpen(p.ConnectorID, "preview-1") {
		t.Fatal("pending이 제거되지 않음")
	}
	if f.router.ForgetPreviewOpen(p.ConnectorID, "preview-1") {
		t.Fatal("없는 pending을 제거했다고 보고함")
	}
	// 로컬 추적만 지운다. Connector에는 아무것도 보내지 않는다(PREVIEW_CLOSE는 호출자가 보낸다).
	if frames.count() != 1 {
		t.Fatalf("frame = %d, want 1 (PREVIEW_OPEN만)", frames.count())
	}
	// 늦은 결과는 unmatched다.
	if f.router.RoutePreviewOpenResult(p.ConnectorID, previewInboundFor(sent.MessageID, previewCorr("preview-1")), PreviewOpenResultPayload{Outcome: PreviewOutcomeFailed}) {
		t.Fatal("정리한 PREVIEW_OPEN에 늦은 결과가 연결됨")
	}
}

// Router pending이 ForgetPreviewOpen으로 정리되면 동일 previewSessionId의 후속 SendPreviewOpen(sequential tunnel)이 성공하고,
// 이전 attempt의 늦은 결과는 새 attempt를 방해하거나 취소시키지 않는다.
func TestSequentialPreviewOpenAfterForgetSucceeds(t *testing.T) {
	f := newPreviewFixture(t)
	p := newPrincipal()
	_, frames := f.connect(p, previewV1)

	// Attempt A: 첫 번째 tunnel의 PREVIEW_OPEN
	sentA, err := f.router.SendPreviewOpen(context.Background(), previewOpenFor(p.ConnectorID, "preview-1"))
	if err != nil {
		t.Fatalf("SendPreviewOpen A error = %v", err)
	}
	if f.router.PendingPreviewOpens(p.ConnectorID) != 1 {
		t.Fatalf("PendingPreviewOpens want 1, got %d", f.router.PendingPreviewOpens(p.ConnectorID))
	}

	// PREVIEW_OPEN_RESULT 없이 Data attach 성공 또는 timeout/cancel로 pending 정리
	if !f.router.ForgetPreviewOpen(p.ConnectorID, "preview-1") {
		t.Fatal("ForgetPreviewOpen A returned false")
	}
	if f.router.PendingPreviewOpens(p.ConnectorID) != 0 {
		t.Fatalf("PendingPreviewOpens want 0, got %d", f.router.PendingPreviewOpens(p.ConnectorID))
	}

	// Attempt B: 동일 previewSessionId의 후속 PREVIEW_OPEN 전송 (ErrDuplicateCorrelation 없이 성공)
	sentB, err := f.router.SendPreviewOpen(context.Background(), previewOpenFor(p.ConnectorID, "preview-1"))
	if err != nil {
		t.Fatalf("SendPreviewOpen B error = %v, want success", err)
	}
	if f.router.PendingPreviewOpens(p.ConnectorID) != 1 {
		t.Fatalf("PendingPreviewOpens want 1, got %d", f.router.PendingPreviewOpens(p.ConnectorID))
	}
	if frames.count() != 2 {
		t.Fatalf("frames = %d, want 2", frames.count())
	}

	// Attempt A의 늦은 결과 도착: replyToMessageId가 sentA와 일치하므로 attempt B(sentB)와 mismatch
	routedA := f.router.RoutePreviewOpenResult(p.ConnectorID, previewInboundFor(sentA.MessageID, previewCorr("preview-1")), PreviewOpenResultPayload{Outcome: PreviewOutcomeSucceeded})
	if routedA {
		t.Fatal("late result A가 attempt B에 잘못 연결됨")
	}
	// attempt B의 pending은 늦은 결과 A에 의해 제거되지 않고 유지됨
	if f.router.PendingPreviewOpens(p.ConnectorID) != 1 {
		t.Fatalf("PendingPreviewOpens after late result A want 1, got %d", f.router.PendingPreviewOpens(p.ConnectorID))
	}

	// Attempt B의 결과 도착: 정상 매칭 및 pending 정리
	routedB := f.router.RoutePreviewOpenResult(p.ConnectorID, previewInboundFor(sentB.MessageID, previewCorr("preview-1")), PreviewOpenResultPayload{Outcome: PreviewOutcomeSucceeded})
	if !routedB {
		t.Fatal("result B가 attempt B에 연결되지 않음")
	}
	if f.router.PendingPreviewOpens(p.ConnectorID) != 0 {
		t.Fatalf("PendingPreviewOpens after result B want 0, got %d", f.router.PendingPreviewOpens(p.ConnectorID))
	}
}

func TestPreviewUnrepresentableIsReportedAsUnmatched(t *testing.T) {
	f := newPreviewFixture(t)
	p := newPrincipal()
	f.connect(p, previewV1)
	f.router.RoutePreviewUnrepresentable(p.ConnectorID, protocol.MessageTypePreviewOpenResult, PreviewInbound{Correlation: PreviewCorrelation{PreviewSessionID: strings.Repeat("x", 500), LabInstanceID: "lab-1"}})
	unmatched, ok := f.sink.only(t).(PreviewUnmatchedEvent)
	if !ok || unmatched.Reason != ReasonUnrepresentable || len(unmatched.PreviewSessionID) > maxLoggedIDLen {
		t.Fatalf("event = %+v", f.sink.all())
	}
}

type sessionRecorder struct {
	mu      sync.Mutex
	retired []retiredSession
}

type retiredSession struct {
	session Session
	reason  CloseReason
}

func (r *sessionRecorder) SessionRetired(session Session, reason CloseReason) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.retired = append(r.retired, retiredSession{session, reason})
}

func (r *sessionRecorder) all() []retiredSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]retiredSession(nil), r.retired...)
}

// 새 Control connection이 이전 Session을 교체하면 이전 Session이 통지된다. 새 Session은 통지되지 않는다.
func TestSessionObserverIsToldAboutReplacement(t *testing.T) {
	registry := NewRegistry()
	observer := &sessionRecorder{}
	registry.SetSessionObserver(observer)
	p := newPrincipal()

	first := registry.Register(p, nil)
	if len(observer.all()) != 0 {
		t.Fatal("첫 Session 등록이 통지됨")
	}
	second := registry.Register(p, nil)
	got := observer.all()
	if len(got) != 1 || got[0].session != first.Session() || got[0].reason != CloseReplaced {
		t.Fatalf("통지 = %+v, want 교체된 첫 Session(CloseReplaced)", got)
	}
	if first.Session().ID == second.Session().ID {
		t.Fatal("교체된 Session의 ID가 새 Session과 같음")
	}
}

// revoke는 Control Session의 유무와 무관하게 통지하되 Session이 있을 때만 SessionRetired를 보낸다.
func TestSessionObserverIsToldAboutRevoke(t *testing.T) {
	registry := NewRegistry()
	observer := &sessionRecorder{}
	registry.SetSessionObserver(observer)
	p := newPrincipal()
	other := newPrincipal()

	session := registry.Register(p, nil)
	otherSession := registry.Register(other, nil)
	if n := registry.RevokeCredential(p.CredentialID); n != 1 {
		t.Fatalf("RevokeCredential = %d, want 1", n)
	}
	got := observer.all()
	if len(got) != 1 || got[0].session != session.Session() || got[0].reason != CloseRevoked {
		t.Fatalf("통지 = %+v, want revoke된 Session(CloseRevoked)", got)
	}

	if !registry.RevokeConnector(other.ConnectorID) {
		t.Fatal("RevokeConnector가 Session을 찾지 못함")
	}
	got = observer.all()
	if len(got) != 2 || got[1].session != otherSession.Session() || got[1].reason != CloseRevoked {
		t.Fatalf("통지 = %+v, want 두 번째로 revoke된 Session", got)
	}

	// Control Session이 없는 revoke는 SessionRetired를 보내지 않는다.
	registry.RevokeCredential(uuid.New())
	registry.RevokeConnector(uuid.New())
	if len(observer.all()) != 2 {
		t.Fatalf("Session 없는 revoke가 통지됨: %+v", observer.all())
	}
}

// connection이 단순히 끊겨 Release된 경우는 교체도 revoke도 아니다(재접속 가능). 통지하지 않는다.
func TestSessionObserverIsNotToldAboutAPlainDisconnect(t *testing.T) {
	registry := NewRegistry()
	observer := &sessionRecorder{}
	registry.SetSessionObserver(observer)
	registration := registry.Register(newPrincipal(), nil)
	registration.Release()
	if got := observer.all(); len(got) != 0 {
		t.Fatalf("단순 disconnect가 통지됨: %+v", got)
	}
}
