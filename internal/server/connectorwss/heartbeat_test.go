package connectorwss

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

// peer는 HELLO까지 마친 test용 raw Control client다. 별도 goroutine이 read를 진행하므로 close frame과 pong을 관찰할 수 있다.
// write는 test goroutine 하나에서만 한다(ping은 WriteControl이라 어느 goroutine에서든 안전하다).
type peer struct {
	t     *testing.T
	conn  *websocket.Conn
	pongs atomic.Int64

	mu       sync.Mutex
	frames   []string
	closeErr *websocket.CloseError
	readErr  error
	done     chan struct{}
}

// establish는 기본 Credential로 연결해 HELLO_ACK까지 받고 registry에 등록될 때까지 기다린다.
func (h *harness) establish() *peer {
	h.t.Helper()
	p := h.establishWith(testCredential)
	h.waitRegistered()
	return p
}

// establishWith는 credential로 연결해 HELLO를 보내고 HELLO_ACK를 받은 뒤 read를 시작한다.
func (h *harness) establishWith(credential string) *peer {
	h.t.Helper()
	conn, _, err := h.dial(bearer(credential), protocol.SubprotocolControl)
	if err != nil {
		h.t.Fatalf("Dial() error = %v", err)
	}
	h.t.Cleanup(func() { _ = conn.Close() })

	sendJSON(h.t, conn, validHello())
	if ack := readJSON(h.t, conn); ack["type"] != "HELLO_ACK" {
		h.t.Fatalf("첫 응답 = %v, want HELLO_ACK", ack)
	}

	p := &peer{t: h.t, conn: conn, done: make(chan struct{})}
	conn.SetPongHandler(func(string) error {
		p.pongs.Add(1)
		return nil
	})
	go p.readLoop()
	return p
}

func (p *peer) readLoop() {
	defer close(p.done)
	for {
		_, data, err := p.conn.ReadMessage()
		if err != nil {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.readErr = err
			var closeErr *websocket.CloseError
			if errors.As(err, &closeErr) {
				p.closeErr = closeErr
			}
			return
		}
		p.mu.Lock()
		p.frames = append(p.frames, string(data))
		p.mu.Unlock()
	}
}

func (p *peer) send(v any) {
	p.t.Helper()
	sendJSON(p.t, p.conn, v)
}

func (p *peer) heartbeat() {
	p.t.Helper()
	p.send(validHeartbeatMessage())
}

func (p *peer) ping() {
	// 연결이 이미 닫혔을 수 있으므로 오류는 무시한다.
	_ = p.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second))
}

// received는 지금까지 받은 text frame 원문이다.
func (p *peer) received() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.frames...)
}

// closedNow는 read loop가 이미 끝났는지 반환한다.
func (p *peer) closedNow() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// waitClosed는 connection이 close frame과 함께 끝날 때까지 기다리고 close 정보를 반환한다.
func (p *peer) waitClosed(timeout time.Duration) *websocket.CloseError {
	p.t.Helper()
	select {
	case <-p.done:
	case <-time.After(timeout):
		p.t.Fatalf("%v 안에 connection이 닫히지 않음", timeout)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closeErr == nil {
		p.t.Fatalf("close frame 없이 connection이 끝남: %v", p.readErr)
	}
	return p.closeErr
}

func validHeartbeatMessage() map[string]any {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return map[string]any{
		"type":      protocol.MessageTypeHeartbeat,
		"messageId": uuid.NewString(),
		"sentAt":    now,
		"payload":   map[string]any{"observedAt": now},
	}
}

// 유효한 HEARTBEAT는 registry의 current Session에서 기록되고, last_seen 시각은 Connector의 observedAt이 아니라
// Backend가 수신한 서버 시각이다.
func TestHeartbeatRecordsServerReceiptTimeNotObservedAt(t *testing.T) {
	h := newHarness(t)
	p := h.establish()

	hb := validHeartbeatMessage()
	payloadOf(hb)["observedAt"] = "2001-01-01T00:00:00Z"
	before := time.Now()
	p.send(hb)
	waitFor(t, "heartbeat 기록", func() bool { return len(h.beats.recorded()) == 1 })
	after := time.Now()

	got := h.beats.recorded()[0]
	if got.principal != h.principal {
		t.Fatalf("기록된 principal = %+v, want %+v", got.principal, h.principal)
	}
	if got.seenAt.Before(before.Add(-time.Millisecond)) || got.seenAt.After(after.Add(time.Millisecond)) {
		t.Fatalf("seenAt = %v, want 서버 수신 시각 [%v, %v] (observedAt 2001-01-01이 아님)", got.seenAt, before, after)
	}
	if got.seenAt.Location() != time.UTC {
		t.Fatalf("seenAt location = %v, want UTC", got.seenAt.Location())
	}
	if p.closedNow() {
		t.Fatal("유효한 HEARTBEAT 뒤에 connection이 닫힘")
	}
}

func TestHeartbeatsAreRecordedEachTimeInOrder(t *testing.T) {
	h := newHarness(t)
	p := h.establish()

	for range 3 {
		p.heartbeat()
	}
	waitFor(t, "heartbeat 3개 기록", func() bool { return len(h.beats.recorded()) == 3 })

	records := h.beats.recorded()
	for i := 1; i < len(records); i++ {
		if records[i].seenAt.Before(records[i-1].seenAt) {
			t.Fatalf("seenAt이 되돌아감: %v -> %v", records[i-1].seenAt, records[i].seenAt)
		}
	}
}

// 알 수 없는 future field, 잘못된 Trace metadata, 유효한 known optional field가 있는 HEARTBEAT도 정상 message다.
func TestHeartbeatToleratesUnknownFieldsAndInvalidTrace(t *testing.T) {
	tests := map[string]func(map[string]any){
		"알 수 없는 envelope field":  func(m map[string]any) { m["futureField"] = map[string]any{"a": 1} },
		"알 수 없는 payload field":   func(m map[string]any) { payloadOf(m)["futureField"] = []int{1} },
		"타입이 잘못된 traceparent":    func(m map[string]any) { m["traceparent"] = 123 },
		"W3C 형식이 아닌 traceparent": func(m map[string]any) { m["traceparent"] = "not-a-trace" },
		"잘못된 tracestate":         func(m map[string]any) { m["tracestate"] = []string{"x"} },
		"유효한 known optional field": func(m map[string]any) {
			m["requestId"] = "req-1"
			m["operationId"] = "op-1"
			m["labInstanceId"] = "lab-1"
			m["replyToMessageId"] = "msg-0"
			m["generation"] = json.RawMessage("1e2")
		},
		"대소문자만 다른 field는 알 수 없는 field": func(m map[string]any) {
			m["RequestId"] = 42
			m["GENERATION"] = 0
			payloadOf(m)["ObservedAt"] = "not-a-time"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			p := h.establish()

			hb := validHeartbeatMessage()
			mutate(hb)
			p.send(hb)

			waitFor(t, "heartbeat 기록", func() bool { return len(h.beats.recorded()) == 1 })
			if p.closedNow() {
				t.Fatal("정상 HEARTBEAT 뒤에 connection이 닫힘")
			}
		})
	}
}

// 계약에 맞지 않는 HEARTBEAT는 fatal ERROR와 4004로 끝나고 last_seen을 갱신하지 않으며 registry에서 해제된다.
// 입력 값과 Credential은 응답과 log에 되돌려 주지 않는다.
func TestInvalidHeartbeatIsRejectedWithoutRecording(t *testing.T) {
	invalid := map[string]func(map[string]any){
		"observedAt 없음":       func(m map[string]any) { delete(payloadOf(m), "observedAt") },
		"잘못된 observedAt":      func(m map[string]any) { payloadOf(m)["observedAt"] = "yesterday " + echoCheckMarker },
		"zero observedAt":     func(m map[string]any) { payloadOf(m)["observedAt"] = "0001-01-01T00:00:00Z" },
		"messageId 없음":        func(m map[string]any) { delete(m, "messageId") },
		"sentAt 없음":           func(m map[string]any) { delete(m, "sentAt") },
		"payload 없음":          func(m map[string]any) { delete(m, "payload") },
		"잘못된 known optional":  func(m map[string]any) { m["requestId"] = map[string]any{"k": echoCheckMarker} },
		"generation 0":        func(m map[string]any) { m["generation"] = 0 },
		"대소문자만 다른 observedAt": func(m map[string]any) { renameKey(payloadOf(m), "observedAt", "ObservedAt") },
	}
	for name, mutate := range invalid {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			p := h.establish()

			hb := validHeartbeatMessage()
			mutate(hb)
			p.send(hb)

			closeErr := p.waitClosed(5 * time.Second)
			if closeErr.Code != closeProtocolError {
				t.Fatalf("close code = %d, want %d", closeErr.Code, closeProtocolError)
			}
			frames := p.received()
			if len(frames) != 1 {
				t.Fatalf("close 전 frame = %v, want 단일 ERROR", frames)
			}
			var errMsg map[string]any
			if err := json.Unmarshal([]byte(frames[0]), &errMsg); err != nil {
				t.Fatal(err)
			}
			payload := payloadOf(errMsg)
			if errMsg["type"] != "ERROR" || payload["code"] != errorCodeInvalidMessage || payload["fatal"] != true {
				t.Fatalf("ERROR = %v, want fatal %s", errMsg, errorCodeInvalidMessage)
			}
			for _, out := range []string{frames[0], closeErr.Text, h.logs.String()} {
				if strings.Contains(out, echoCheckMarker) || strings.Contains(out, testCredential) {
					t.Fatalf("입력 또는 Credential이 노출됨: %s", out)
				}
			}
			if got := h.beats.recorded(); len(got) != 0 {
				t.Fatalf("잘못된 HEARTBEAT가 last_seen을 갱신함: %+v", got)
			}
			waitFor(t, "registry release", func() bool {
				_, ok := h.registry.Current(h.principal.ConnectorID)
				return !ok
			})
		})
	}
}

// renameKey는 map의 key 하나를 다른 이름으로 바꾼다.
func renameKey(m map[string]any, from, to string) {
	m[to] = m[from]
	delete(m, from)
}

// validHeartbeat는 Schema의 HeartbeatMessage 조건과 같은 판정을 한다(정확한 property 이름, known optional 제약, Trace·future field 무시).
func TestValidHeartbeat(t *testing.T) {
	const at = `"2026-09-30T00:00:00Z"`
	build := func(envelope, payload string) string {
		members := []string{`"type":"HEARTBEAT"`, `"messageId":"m1"`, `"sentAt":` + at}
		if envelope != "" {
			members = append(members, envelope)
		}
		if payload != "-" {
			members = append(members, `"payload":{`+payload+`}`)
		}
		return "{" + strings.Join(members, ",") + "}"
	}
	observed := `"observedAt":` + at

	tests := []struct {
		name string
		data string
		want bool
	}{
		{"정상", build("", observed), true},
		{"알 수 없는 field", build(`"futureField":{"a":1}`, observed+`,"future":1`), true},
		{"잘못된 Trace", build(`"traceparent":123,"tracestate":[]`, observed), true},
		{"유효한 known optional", build(`"requestId":"r","operationId":"o","labInstanceId":"l","replyToMessageId":"m","generation":1`, observed), true},
		{"대소문자만 다른 key는 무시", build(`"RequestId":42,"GENERATION":0`, observed+`,"ObservedAt":"x"`), true},
		{"observedAt 다음 ObservedAt", build("", observed+`,"ObservedAt":"garbage"`), true},
		{"ObservedAt 다음 observedAt", build("", `"ObservedAt":"garbage",`+observed), true},

		{"payload 없음", build("", "-"), false},
		{"빈 payload", build("", ""), false},
		{"observedAt null", build("", `"observedAt":null`), false},
		{"observedAt number", build("", `"observedAt":1`), false},
		{"observedAt 형식 오류", build("", `"observedAt":"yesterday"`), false},
		{"zero observedAt", build("", `"observedAt":"0001-01-01T00:00:00Z"`), false},
		{"ObservedAt만 있음", build("", `"ObservedAt":`+at), false},
		{"잘못된 observedAt과 유효한 ObservedAt", build("", `"observedAt":"x","ObservedAt":`+at), false},
		{"유효한 ObservedAt과 잘못된 observedAt", build("", `"ObservedAt":`+at+`,"observedAt":"x"`), false},
		{"requestId number", build(`"requestId":42`, observed), false},
		{"빈 operationId", build(`"operationId":""`, observed), false},
		{"generation 0", build(`"generation":0`, observed), false},
		{"payload가 배열", `{"type":"HEARTBEAT","messageId":"m1","sentAt":` + at + `,"payload":[]}`, false},
		{"payload null", `{"type":"HEARTBEAT","messageId":"m1","sentAt":` + at + `,"payload":null}`, false},
		{"messageId 없음", `{"type":"HEARTBEAT","sentAt":` + at + `,"payload":{` + observed + `}}`, false},
		{"빈 messageId", `{"type":"HEARTBEAT","messageId":"","sentAt":` + at + `,"payload":{` + observed + `}}`, false},
		{"sentAt 없음", `{"type":"HEARTBEAT","messageId":"m1","payload":{` + observed + `}}`, false},
		{"MessageID만 있음", `{"type":"HEARTBEAT","MessageID":"m1","sentAt":` + at + `,"payload":{` + observed + `}}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envelope, ok := jsonObject([]byte(tt.data))
			if !ok {
				t.Fatalf("test 입력이 JSON object가 아님: %s", tt.data)
			}
			if got := validHeartbeat(envelope); got != tt.want {
				t.Fatalf("validHeartbeat(%s) = %v, want %v", tt.data, got, tt.want)
			}
		})
	}
}

// heartbeatOfSize는 JSON으로 직렬화했을 때 정확히 size byte인 유효한 HEARTBEAT다.
func heartbeatOfSize(t *testing.T, size int) []byte {
	t.Helper()
	hb := validHeartbeatMessage()
	payloadOf(hb)["padding"] = ""
	base, err := json.Marshal(hb)
	if err != nil {
		t.Fatal(err)
	}
	if len(base) > size {
		t.Fatalf("기본 HEARTBEAT(%d byte)가 size %d보다 큼", len(base), size)
	}
	payloadOf(hb)["padding"] = strings.Repeat("x", size-len(base))
	data, err := json.Marshal(hb)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != size {
		t.Fatalf("HEARTBEAT 크기 = %d, want %d", len(data), size)
	}
	return data
}

// JSON Text 1 MiB guard는 HEARTBEAT에도 적용된다. 정확히 1 MiB는 정상 기록되고 1 MiB+1은 1009로 끝나며 기록되지 않는다.
func TestHeartbeatMessageSizeLimit(t *testing.T) {
	t.Run("정확히 1 MiB인 HEARTBEAT는 기록", func(t *testing.T) {
		h := newHarness(t)
		p := h.establish()
		if err := p.conn.WriteMessage(websocket.TextMessage, heartbeatOfSize(t, int(protocol.MaxJSONMessageSize))); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "heartbeat 기록", func() bool { return len(h.beats.recorded()) == 1 })
		if p.closedNow() {
			t.Fatal("1 MiB HEARTBEAT 뒤에 connection이 닫힘")
		}
	})
	t.Run("1 MiB 초과 HEARTBEAT는 1009", func(t *testing.T) {
		h := newHarness(t)
		p := h.establish()
		if err := p.conn.WriteMessage(websocket.TextMessage, heartbeatOfSize(t, int(protocol.MaxJSONMessageSize)+1)); err != nil {
			t.Fatal(err)
		}
		closeErr := p.waitClosed(5 * time.Second)
		if closeErr.Code != protocol.CloseMessageTooBig {
			t.Fatalf("close code = %d, want %d", closeErr.Code, protocol.CloseMessageTooBig)
		}
		if got := h.beats.recorded(); len(got) != 0 {
			t.Fatalf("크기 초과 HEARTBEAT가 기록됨: %+v", got)
		}
		waitFor(t, "registry release", func() bool {
			_, ok := h.registry.Current(h.principal.ConnectorID)
			return !ok
		})
	})
}
