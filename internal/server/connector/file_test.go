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

// fileRecorder는 Router가 넘긴 File event를 순서대로 기록한다.
type fileRecorder struct {
	mu     sync.Mutex
	events []FileEvent
}

func (r *fileRecorder) HandleFileEvent(e FileEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *fileRecorder) all() []FileEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]FileEvent(nil), r.events...)
}

func (r *fileRecorder) only(t *testing.T) FileEvent {
	t.Helper()
	events := r.all()
	if len(events) != 1 {
		t.Fatalf("event %d개 = %+v, want 1개", len(events), events)
	}
	return events[0]
}

type fileFixture struct {
	t        *testing.T
	registry *Registry
	sink     *fileRecorder
	router   *Router
	logs     *syncLog
}

func newFileFixture(t *testing.T) *fileFixture {
	t.Helper()
	registry := NewRegistry()
	sink := &fileRecorder{}
	logs := &syncLog{}
	router, err := NewRouter(RouterOptions{
		Registry: registry,
		FileSink: sink,
		Logger:   slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &fileFixture{t: t, registry: registry, sink: sink, router: router, logs: logs}
}

// connect는 principal의 Connector를 HELLO에서 capabilities를 선언한 protocol-ready Session으로 연결한다.
func (f *fileFixture) connect(principal Principal, capabilities []string) (*Registration, *frameLog) {
	f.t.Helper()
	frames := &frameLog{}
	registration := f.registry.Register(principal, nil)
	registration.SetCapabilities(capabilities)
	if !registration.MarkReady(frames.route) {
		f.t.Fatal("MarkReady가 거절됨")
	}
	return registration, frames
}

var fileV1 = []string{protocol.CapabilityFileV1}

func fileCorr(request string) FileCorrelation {
	return FileCorrelation{FileRequestID: request, LabInstanceID: "lab-1", Generation: 3}
}

func fileOpenFor(connectorID uuid.UUID, request string) FileOpen {
	return FileOpen{
		ConnectorID: connectorID, RequestID: "request-1", Correlation: fileCorr(request),
		Operation: FileOperationRead, TargetVMKey: "vk-web", ProviderServerID: "srv-web-g3",
	}
}

func fileInboundFor(replyTo string, c FileCorrelation) FileInbound {
	return FileInbound{MessageID: "inbound-1", ReplyToMessageID: replyTo, Correlation: c}
}

// FILE_OPEN은 file-control.schema.json의 Envelope와 payload(resolved target과 operation뿐)를 그대로 싣는다.
// 경로, 디렉터리 목록, 본문은 Control에 실리지 않는다.
func TestSendFileOpenWritesContractMessage(t *testing.T) {
	f := newFileFixture(t)
	p := newPrincipal()
	_, frames := f.connect(p, fileV1)

	sent, err := f.router.SendFileOpen(context.Background(), fileOpenFor(p.ConnectorID, "file-1"))
	if err != nil {
		t.Fatalf("SendFileOpen() error = %v", err)
	}
	if frames.count() != 1 {
		t.Fatalf("frame = %d, want 1", frames.count())
	}
	raw := frames.last(t)
	msg := decodeFrame(t, raw)
	payload, _ := msg["payload"].(map[string]any)
	if msg["type"] != "FILE_OPEN" || msg["messageId"] != sent.MessageID || msg["requestId"] != "request-1" ||
		msg["fileRequestId"] != "file-1" || msg["labInstanceId"] != "lab-1" || msg["generation"] != float64(3) {
		t.Fatalf("envelope = %v", msg)
	}
	if _, err := uuid.Parse(sent.MessageID); err != nil {
		t.Fatalf("messageId = %q", sent.MessageID)
	}
	if payload["operation"] != "READ" || payload["targetVmKey"] != "vk-web" || payload["providerServerId"] != "srv-web-g3" || len(payload) != 3 {
		t.Fatalf("payload = %v, want only operation and the resolved target", payload)
	}
	// connectorId는 인증된 connection에서 SaaS가 정한다. message에 싣지 않는다.
	if strings.Contains(string(raw), p.ConnectorID.String()) || msg["connectorId"] != nil {
		t.Fatalf("FILE_OPEN이 connector id를 실음: %s", raw)
	}
	if f.router.PendingFileOpens(p.ConnectorID) != 1 {
		t.Fatalf("PendingFileOpens = %d, want 1", f.router.PendingFileOpens(p.ConnectorID))
	}
}

func TestSendFileOpenCarriesAValidTraceContextOnly(t *testing.T) {
	f := newFileFixture(t)
	p := newPrincipal()
	_, frames := f.connect(p, fileV1)

	open := fileOpenFor(p.ConnectorID, "file-1")
	open.Trace = TraceContext{Traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}
	if _, err := f.router.SendFileOpen(context.Background(), open); err != nil {
		t.Fatal(err)
	}
	if got := decodeFrame(t, frames.last(t))["traceparent"]; got != open.Trace.Traceparent {
		t.Fatalf("traceparent = %v", got)
	}

	open = fileOpenFor(p.ConnectorID, "file-2")
	open.Trace = TraceContext{Traceparent: "not-a-trace-parent", Tracestate: "k=v"}
	if _, err := f.router.SendFileOpen(context.Background(), open); err != nil {
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

// SaaS는 HELLO에서 file-v1을 선언한 Connector에게만 File Control message를 보낸다. 버전으로 추론하지 않는다.
func TestFileControlOnlyGoesToConnectorsThatDeclaredTheCapability(t *testing.T) {
	cases := []struct {
		name         string
		capabilities []string
	}{
		{"capabilities field가 없는 기존 Connector", nil},
		{"빈 capabilities", []string{}},
		{"다른 capability만 선언", []string{"terminal", "preview"}},
		{"비슷한 이름", []string{"file", "file-v2", "FILE-V1", "file-v1 "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFileFixture(t)
			p := newPrincipal()
			_, frames := f.connect(p, tc.capabilities)

			_, err := f.router.SendFileOpen(context.Background(), fileOpenFor(p.ConnectorID, "file-1"))
			if !errors.Is(err, ErrCapabilityUnsupported) {
				t.Fatalf("SendFileOpen() error = %v, want ErrCapabilityUnsupported", err)
			}
			// 연결되어 있으므로 Connector 단절이 아니다. 호출자가 둘을 구분할 수 있어야 한다.
			if errors.Is(err, ErrConnectorUnavailable) {
				t.Fatal("capability 미선언을 Connector 단절로 보고함")
			}
			if _, err := f.router.SendFileClose(context.Background(), FileClose{ConnectorID: p.ConnectorID, Correlation: fileCorr("file-1"), Reason: "REQUEST_CANCELED"}); !errors.Is(err, ErrCapabilityUnsupported) {
				t.Fatalf("SendFileClose() error = %v, want ErrCapabilityUnsupported", err)
			}
			if frames.count() != 0 {
				t.Fatalf("capability 없는 Connector에 frame %d개를 보냄", frames.count())
			}
			if f.router.PendingFileOpens(p.ConnectorID) != 0 {
				t.Fatal("보내지 않은 FILE_OPEN의 pending이 남음")
			}
			if !strings.Contains(f.logs.String(), "capability_unsupported") {
				t.Fatalf("거절 사유가 기록되지 않음: %s", f.logs.String())
			}
		})
	}
}

// capability는 connection(Session)마다 HELLO로 선언한다. 재접속한 새 Session이 선언하지 않았다면 이전 Session의 선언을 물려받지 않는다.
func TestCapabilityBelongsToTheSessionThatDeclaredIt(t *testing.T) {
	f := newFileFixture(t)
	p := newPrincipal()
	f.connect(p, fileV1)
	if _, err := f.router.SendFileOpen(context.Background(), fileOpenFor(p.ConnectorID, "file-1")); err != nil {
		t.Fatalf("SendFileOpen() error = %v", err)
	}

	// 같은 Connector가 file-v1 없이 다시 연결한다.
	_, frames := f.connect(p, nil)
	_, err := f.router.SendFileOpen(context.Background(), fileOpenFor(p.ConnectorID, "file-2"))
	if !errors.Is(err, ErrCapabilityUnsupported) {
		t.Fatalf("SendFileOpen() error = %v, want ErrCapabilityUnsupported", err)
	}
	if frames.count() != 0 {
		t.Fatal("capability를 선언하지 않은 새 Session에 frame을 보냄")
	}
}

func TestSetCapabilitiesAfterRetireIsIgnored(t *testing.T) {
	f := newFileFixture(t)
	p := newPrincipal()
	old := f.registry.Register(p, nil)
	// 같은 Connector의 새 connection이 이전 Session을 교체한다.
	_, frames := f.connect(p, nil)

	old.SetCapabilities(fileV1)
	if old.MarkReady((&frameLog{}).route) {
		t.Fatal("교체된 Session이 ready가 됨")
	}
	if _, err := f.router.SendFileOpen(context.Background(), fileOpenFor(p.ConnectorID, "file-1")); !errors.Is(err, ErrCapabilityUnsupported) {
		t.Fatalf("SendFileOpen() error = %v", err)
	}
	if frames.count() != 0 {
		t.Fatal("frame을 보냄")
	}
}

// Connector가 없거나 HELLO_ACK 전이면 capability와 무관하게 사용 불가다.
func TestFileOpenNeedsAProtocolReadySession(t *testing.T) {
	f := newFileFixture(t)
	p := newPrincipal()
	if _, err := f.router.SendFileOpen(context.Background(), fileOpenFor(p.ConnectorID, "file-1")); !errors.Is(err, ErrNotConnected) || !errors.Is(err, ErrConnectorUnavailable) {
		t.Fatalf("미연결 error = %v", err)
	}

	registration := f.registry.Register(p, nil)
	registration.SetCapabilities(fileV1)
	if _, err := f.router.SendFileOpen(context.Background(), fileOpenFor(p.ConnectorID, "file-1")); !errors.Is(err, ErrNotReady) || !errors.Is(err, ErrConnectorUnavailable) {
		t.Fatalf("HELLO_ACK 전 error = %v", err)
	}
	if f.router.PendingFileOpens(p.ConnectorID) != 0 {
		t.Fatal("보내지 않은 FILE_OPEN의 pending이 남음")
	}
}

func TestFileOpenWithAnInvalidRequestIsNotSent(t *testing.T) {
	cases := map[string]func(*FileOpen){
		"fileRequestId 없음":    func(o *FileOpen) { o.Correlation.FileRequestID = "" },
		"labInstanceId 없음":    func(o *FileOpen) { o.Correlation.LabInstanceID = "" },
		"generation 0":        func(o *FileOpen) { o.Correlation.Generation = 0 },
		"generation 음수":       func(o *FileOpen) { o.Correlation.Generation = -1 },
		"operation 없음":        func(o *FileOpen) { o.Operation = "" },
		"operation 알 수 없음":    func(o *FileOpen) { o.Operation = "DELETE" },
		"operation 소문자":       func(o *FileOpen) { o.Operation = "read" },
		"targetVmKey 없음":      func(o *FileOpen) { o.TargetVMKey = "" },
		"providerServerId 없음": func(o *FileOpen) { o.ProviderServerID = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFileFixture(t)
			p := newPrincipal()
			_, frames := f.connect(p, fileV1)
			open := fileOpenFor(p.ConnectorID, "file-1")
			mutate(&open)
			if _, err := f.router.SendFileOpen(context.Background(), open); !errors.Is(err, ErrInvalidCommand) {
				t.Fatalf("SendFileOpen() error = %v, want ErrInvalidCommand", err)
			}
			if frames.count() != 0 || f.router.PendingFileOpens(p.ConnectorID) != 0 {
				t.Fatal("계약에 맞지 않는 FILE_OPEN을 보냈거나 pending을 남김")
			}
		})
	}
}

func TestFileOpenWithACanceledContextIsNotSent(t *testing.T) {
	f := newFileFixture(t)
	p := newPrincipal()
	_, frames := f.connect(p, fileV1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.router.SendFileOpen(ctx, fileOpenFor(p.ConnectorID, "file-1")); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if frames.count() != 0 || f.router.PendingFileOpens(p.ConnectorID) != 0 {
		t.Fatal("취소된 요청을 보냈거나 pending을 남김")
	}
}

func TestDuplicateFileRequestIDIsRejectedWithoutASecondFrame(t *testing.T) {
	f := newFileFixture(t)
	p := newPrincipal()
	_, frames := f.connect(p, fileV1)
	if _, err := f.router.SendFileOpen(context.Background(), fileOpenFor(p.ConnectorID, "file-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.router.SendFileOpen(context.Background(), fileOpenFor(p.ConnectorID, "file-1")); !errors.Is(err, ErrDuplicateCorrelation) {
		t.Fatalf("중복 FILE_OPEN error = %v, want ErrDuplicateCorrelation", err)
	}
	if frames.count() != 1 || f.router.PendingFileOpens(p.ConnectorID) != 1 {
		t.Fatalf("frames=%d pending=%d, want 1/1", frames.count(), f.router.PendingFileOpens(p.ConnectorID))
	}
}

// write가 실패하면 방금 등록한 pending을 되돌리고 자동으로 재전송하지 않는다.
func TestFileOpenWriteFailureRollsBackThePending(t *testing.T) {
	f := newFileFixture(t)
	p := newPrincipal()
	writes := 0
	registration := f.registry.Register(p, nil)
	registration.SetCapabilities(fileV1)
	registration.MarkReady(func([]byte) error {
		writes++
		return errors.New("broken pipe")
	})

	_, err := f.router.SendFileOpen(context.Background(), fileOpenFor(p.ConnectorID, "file-1"))
	if !errors.Is(err, ErrSendFailed) {
		t.Fatalf("SendFileOpen() error = %v, want ErrSendFailed", err)
	}
	if writes != 1 {
		t.Fatalf("write %d번, 자동 재전송 금지", writes)
	}
	if f.router.PendingFileOpens(p.ConnectorID) != 0 {
		t.Fatal("실패한 FILE_OPEN의 pending이 남음")
	}
	if strings.Contains(f.logs.String(), "broken pipe") {
		t.Fatalf("transport 오류 원문을 기록함: %s", f.logs.String())
	}
}

func TestSendFileCloseIsAnIdempotentNotificationWithoutPending(t *testing.T) {
	f := newFileFixture(t)
	p := newPrincipal()
	_, frames := f.connect(p, fileV1)

	cl := FileClose{ConnectorID: p.ConnectorID, RequestID: "request-1", Correlation: fileCorr("file-1"), Reason: "REQUEST_TIMEOUT"}
	sent, err := f.router.SendFileClose(context.Background(), cl)
	if err != nil {
		t.Fatalf("SendFileClose() error = %v", err)
	}
	// 반복 전달해도 오류가 아니다.
	if _, err := f.router.SendFileClose(context.Background(), cl); err != nil {
		t.Fatalf("반복 SendFileClose() error = %v", err)
	}
	msg := decodeFrame(t, frames.last(t))
	payload, _ := msg["payload"].(map[string]any)
	if msg["type"] != "FILE_CLOSE" || msg["fileRequestId"] != "file-1" || msg["labInstanceId"] != "lab-1" || msg["generation"] != float64(3) ||
		payload["reason"] != "REQUEST_TIMEOUT" || len(payload) != 1 || sent.MessageID == "" {
		t.Fatalf("FILE_CLOSE = %v", msg)
	}
	if f.router.PendingFileOpens(p.ConnectorID) != 0 {
		t.Fatal("FILE_CLOSE가 pending을 만듦")
	}

	cl.Reason = ""
	if _, err := f.router.SendFileClose(context.Background(), cl); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("reason 없는 FILE_CLOSE error = %v, want ErrInvalidCommand", err)
	}
	cl.Reason, cl.Correlation.Generation = "X", 0
	if _, err := f.router.SendFileClose(context.Background(), cl); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("generation 없는 FILE_CLOSE error = %v, want ErrInvalidCommand", err)
	}
}

func TestFileOpenResultConnectsToItsPendingOpenAndEndsIt(t *testing.T) {
	f := newFileFixture(t)
	p := newPrincipal()
	f.connect(p, fileV1)
	sent, err := f.router.SendFileOpen(context.Background(), fileOpenFor(p.ConnectorID, "file-1"))
	if err != nil {
		t.Fatal(err)
	}

	in := fileInboundFor(sent.MessageID, fileCorr("file-1"))
	if !f.router.RouteFileOpenResult(p.ConnectorID, in, FileOpenResultPayload{Outcome: FileOutcomeFailed, Error: &protocol.SafeError{Code: "UNAVAILABLE"}}) {
		t.Fatal("정상 FILE_OPEN_RESULT가 연결되지 않음")
	}
	event, ok := f.sink.only(t).(FileOpenResultEvent)
	if !ok {
		t.Fatalf("event = %+v", f.sink.all()[0])
	}
	if event.ConnectorID != p.ConnectorID || event.RequestMessageID != sent.MessageID || event.RequestID != "request-1" ||
		event.Correlation != fileCorr("file-1") || event.Payload.Outcome != FileOutcomeFailed || event.Payload.Error.Code != "UNAVAILABLE" {
		t.Fatalf("event = %+v", event)
	}
	if f.router.PendingFileOpens(p.ConnectorID) != 0 {
		t.Fatal("결과를 받은 FILE_OPEN의 pending이 남음")
	}

	// 같은 결과를 다시 받아도 어떤 요청도 완료시키지 않는다.
	if f.router.RouteFileOpenResult(p.ConnectorID, in, FileOpenResultPayload{Outcome: FileOutcomeFailed}) {
		t.Fatal("끝난 FILE_OPEN에 중복 결과가 연결됨")
	}
	unmatched, ok := f.sink.all()[1].(FileUnmatchedEvent)
	if !ok || unmatched.Reason != ReasonNoPending {
		t.Fatalf("중복 결과 event = %+v", f.sink.all()[1])
	}
}

// 인증된 ConnectorID, fileRequestId, labInstanceId, generation, replyToMessageId가 모두 맞는 pending에만 연결한다.
func TestFileOpenResultNeverFallsBackToAnotherPending(t *testing.T) {
	f := newFileFixture(t)
	a, b := newPrincipal(), newPrincipal()
	f.connect(a, fileV1)
	f.connect(b, fileV1)
	sentA, err := f.router.SendFileOpen(context.Background(), fileOpenFor(a.ConnectorID, "file-1"))
	if err != nil {
		t.Fatal(err)
	}
	sentOther, err := f.router.SendFileOpen(context.Background(), fileOpenFor(a.ConnectorID, "file-2"))
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name        string
		connectorID uuid.UUID
		in          FileInbound
		want        UnmatchedReason
	}{
		{"다른 Connector가 같은 correlation을 주장", b.ConnectorID, fileInboundFor(sentA.MessageID, fileCorr("file-1")), ReasonNoPending},
		{"알 수 없는 fileRequestId", a.ConnectorID, fileInboundFor(sentA.MessageID, fileCorr("file-ghost")), ReasonNoPending},
		{"다른 요청의 replyToMessageId", a.ConnectorID, fileInboundFor(sentOther.MessageID, fileCorr("file-1")), ReasonReplyMismatch},
		{"replyToMessageId 없음", a.ConnectorID, fileInboundFor("", fileCorr("file-1")), ReasonReplyMismatch},
		{"다른 LabInstance", a.ConnectorID, fileInboundFor(sentA.MessageID, FileCorrelation{FileRequestID: "file-1", LabInstanceID: "lab-2", Generation: 3}), ReasonCorrelationMismatch},
		{"다른 generation", a.ConnectorID, fileInboundFor(sentA.MessageID, FileCorrelation{FileRequestID: "file-1", LabInstanceID: "lab-1", Generation: 2}), ReasonCorrelationMismatch},
		{"generation이 int64를 넘음", a.ConnectorID, fileInboundFor(sentA.MessageID, FileCorrelation{FileRequestID: "file-1", LabInstanceID: "lab-1", Generation: 0}), ReasonUnrepresentable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := len(f.sink.all())
			if f.router.RouteFileOpenResult(tc.connectorID, tc.in, FileOpenResultPayload{Outcome: FileOutcomeFailed}) {
				t.Fatal("맞지 않는 FILE_OPEN_RESULT가 연결됨")
			}
			events := f.sink.all()
			if len(events) != before+1 {
				t.Fatalf("event %d개 추가, want 1개", len(events)-before)
			}
			unmatched, ok := events[before].(FileUnmatchedEvent)
			if !ok || unmatched.Reason != tc.want || unmatched.ConnectorID != tc.connectorID {
				t.Fatalf("event = %+v, want unmatched %q", events[before], tc.want)
			}
			// 어느 pending도 끝나지 않았다.
			if f.router.PendingFileOpens(a.ConnectorID) != 2 {
				t.Fatalf("PendingFileOpens = %d, want 2", f.router.PendingFileOpens(a.ConnectorID))
			}
		})
	}
}

// 같은 Connector의 여러 요청은 독립이다. 결과가 도착하는 순서와 무관하게 각자의 pending에만 연결한다.
func TestConcurrentFileRequestsRouteIndependently(t *testing.T) {
	f := newFileFixture(t)
	p := newPrincipal()
	f.connect(p, fileV1)

	const n = 8
	sent := make([]SentMessage, n)
	for i := range sent {
		var err error
		sent[i], err = f.router.SendFileOpen(context.Background(), fileOpenFor(p.ConnectorID, "file-"+string(rune('a'+i))))
		if err != nil {
			t.Fatal(err)
		}
	}
	if f.router.PendingFileOpens(p.ConnectorID) != n {
		t.Fatalf("pending = %d, want %d", f.router.PendingFileOpens(p.ConnectorID), n)
	}

	var wg sync.WaitGroup
	for i := n - 1; i >= 0; i-- { // 보낸 순서의 반대로 응답한다.
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !f.router.RouteFileOpenResult(p.ConnectorID, fileInboundFor(sent[i].MessageID, fileCorr("file-"+string(rune('a'+i)))), FileOpenResultPayload{Outcome: FileOutcomeFailed}) {
				t.Errorf("요청 %d의 결과가 연결되지 않음", i)
			}
		}()
	}
	wg.Wait()

	seen := map[string]string{}
	for _, e := range f.sink.all() {
		event, ok := e.(FileOpenResultEvent)
		if !ok {
			t.Fatalf("event = %+v", e)
		}
		seen[event.Correlation.FileRequestID] = event.RequestMessageID
	}
	for i := range sent {
		if seen["file-"+string(rune('a'+i))] != sent[i].MessageID {
			t.Fatalf("요청 %d의 결과가 다른 요청의 messageId에 연결됨: %v", i, seen)
		}
	}
	if f.router.PendingFileOpens(p.ConnectorID) != 0 {
		t.Fatal("pending이 남음")
	}
}

func TestForgetFileOpenDropsTheLocalPendingOnly(t *testing.T) {
	f := newFileFixture(t)
	p := newPrincipal()
	_, frames := f.connect(p, fileV1)
	sent, err := f.router.SendFileOpen(context.Background(), fileOpenFor(p.ConnectorID, "file-1"))
	if err != nil {
		t.Fatal(err)
	}
	if !f.router.ForgetFileOpen(p.ConnectorID, "file-1") {
		t.Fatal("pending이 제거되지 않음")
	}
	if f.router.ForgetFileOpen(p.ConnectorID, "file-1") {
		t.Fatal("없는 pending을 제거했다고 보고함")
	}
	// 로컬 추적만 지운다. Connector에는 아무것도 보내지 않는다(FILE_CLOSE는 호출자가 보낸다).
	if frames.count() != 1 {
		t.Fatalf("frame = %d, want 1 (FILE_OPEN만)", frames.count())
	}
	// 늦은 결과는 unmatched다.
	if f.router.RouteFileOpenResult(p.ConnectorID, fileInboundFor(sent.MessageID, fileCorr("file-1")), FileOpenResultPayload{Outcome: FileOutcomeFailed}) {
		t.Fatal("정리한 FILE_OPEN에 늦은 결과가 연결됨")
	}
}

func TestFileUnrepresentableIsReportedAsUnmatched(t *testing.T) {
	f := newFileFixture(t)
	p := newPrincipal()
	f.connect(p, fileV1)
	f.router.RouteFileUnrepresentable(p.ConnectorID, protocol.MessageTypeFileOpenResult, FileInbound{Correlation: FileCorrelation{FileRequestID: strings.Repeat("x", 500), LabInstanceID: "lab-1"}})
	unmatched, ok := f.sink.only(t).(FileUnmatchedEvent)
	if !ok || unmatched.Reason != ReasonUnrepresentable || len(unmatched.FileRequestID) > maxLoggedIDLen {
		t.Fatalf("event = %+v", f.sink.all())
	}
}
