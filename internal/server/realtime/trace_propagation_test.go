package realtime_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/server/tracecontext"
)

// contracts/connector/README.md §9(D-25)와 contracts/realtime/README.md: Terminal JSON control event의 W3C Trace Context는 control event에서
// 사용할 수 있도록 보존·전파하고, PTY Binary frame에는 붙이지 않는다. 유효하지 않은 값은 그 관측 metadata만 폐기하고 업무 message는 그대로 처리한다.
// 이 파일은 실제 WebSocket을 거쳐 그 전파를 확인한다(wire 해석 자체는 wiretrace_test.go).

const (
	tpSampled   = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	tpUnsampled = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-00"
	tsValid     = "vendor=opaque,other=value"
	traceIDHex  = "4bf92f3577b34da6a3ce929d0e0e4736"
)

func browserResizeMessage(t *testing.T, s session, cols, rows int, fields map[string]any) []byte {
	t.Helper()
	msg := map[string]any{
		"type": "TERMINAL_RESIZE", "messageId": "resize-1", "sentAt": time.Now().UTC().Format(time.RFC3339Nano),
		"terminalSessionId": s.ID, "payload": map[string]any{"cols": cols, "rows": rows},
	}
	for k, v := range fields {
		msg[k] = v
	}
	return marshal(t, msg)
}

func dataEndedMessage(t *testing.T, s session, fields map[string]any) []byte {
	t.Helper()
	merged := map[string]any{"type": "TERMINAL_DATA_ENDED", "messageId": "ended-1", "payload": map[string]any{"reason": "PTY_EXITED", "exitCode": 0}}
	for k, v := range fields {
		merged[k] = v
	}
	return dataAttachMessage(t, s, merged)
}

// traceOf는 받은 JSON message의 traceparent/tracestate다. 없으면 빈 문자열이다.
func traceOf(msg map[string]any) (traceparent, tracestate string) {
	traceparent, _ = msg["traceparent"].(string)
	tracestate, _ = msg["tracestate"].(string)
	return traceparent, tracestate
}

func requireTrace(t *testing.T, what string, msg map[string]any, wantParent, wantState string) {
	t.Helper()
	gotParent, gotState := traceOf(msg)
	if gotParent != wantParent || gotState != wantState {
		t.Fatalf("%s의 Trace = {%q %q}, want {%q %q}: %v", what, gotParent, gotState, wantParent, wantState, msg)
	}
	// 없어야 하는 값은 빈 문자열이 아니라 field 자체가 없어야 한다.
	if _, present := msg["traceparent"]; present != (wantParent != "") {
		t.Fatalf("%s의 traceparent field 존재 = %v, want %v", what, present, wantParent != "")
	}
	if _, present := msg["tracestate"]; present != (wantState != "") {
		t.Fatalf("%s의 tracestate field 존재 = %v, want %v", what, present, wantState != "")
	}
}

// Browser의 control event는 각자의 Trace Context를 Control 호출과 Connector로 가는 message, 그리고 응답에 이어 준다.
// Context는 connection 전역이 아니다. TERMINAL_ATTACH의 Trace를 이후의 TERMINAL_RESIZE에 재사용하지 않는다.
func TestBrowserControlEventsPropagateTheirOwnTraceContext(t *testing.T) {
	e := newEnv(t)
	s, d := e.liveSession()

	p, _, err := e.dialBrowser(s.OwnerCookie, trustedOrigin)
	if err != nil {
		t.Fatalf("Browser WSS dial error = %v", err)
	}
	p.writeText(browserAttachMessage(t, s, map[string]any{"traceparent": tpSampled, "tracestate": tsValid}, nil))

	// 응답(TERMINAL_ATTACHED)은 요청의 Trace를 그대로 돌려준다.
	attached := p.readJSON()
	if attached["type"] != "TERMINAL_ATTACHED" {
		t.Fatalf("attach 응답 = %v", attached)
	}
	requireTrace(t, "TERMINAL_ATTACHED", attached, tpSampled, tsValid)

	// Control(저장소 경계)은 ctx로 그 Trace를 받는다. 권한 확인과 attach 기록 모두 같은 control event의 일이다.
	authorize, recorded, _ := e.control.traces()
	want := tracecontext.Context{Traceparent: tpSampled, Tracestate: tsValid}
	if len(authorize) != 1 || authorize[0] != want || len(recorded) != 1 || recorded[0] != want {
		t.Fatalf("Control ctx Trace authorize=%v recorded=%v, want 둘 다 [%v]", authorize, recorded, want)
	}

	// attach가 PTY에 반영하는 첫 resize는 그 attach의 Trace를 쓴다.
	first := d.readJSON()
	if first["type"] != "TERMINAL_DATA_RESIZE" {
		t.Fatalf("attach 뒤 Connector가 받은 message = %v, want TERMINAL_DATA_RESIZE", first)
	}
	requireTrace(t, "attach의 TERMINAL_DATA_RESIZE", first, tpSampled, tsValid)

	// 다른 Trace를 가진 RESIZE는 자신의 Trace를 쓴다(미샘플링 그대로, tracestate 없음).
	p.writeText(browserResizeMessage(t, s, 100, 30, map[string]any{"traceparent": tpUnsampled}))
	second := d.readJSON()
	requireTrace(t, "두 번째 TERMINAL_DATA_RESIZE", second, tpUnsampled, "")
	if pl := payload(t, second); pl["cols"] != float64(100) || pl["rows"] != float64(30) {
		t.Fatalf("resize payload = %v, want 100x30", pl)
	}

	// Trace가 없는 RESIZE에는 이전 message의 Trace를 붙이지 않는다.
	p.writeText(browserResizeMessage(t, s, 90, 20, nil))
	requireTrace(t, "Trace 없는 RESIZE의 TERMINAL_DATA_RESIZE", d.readJSON(), "", "")

	// log에는 정상화한 trace_id만 남고 원문 tracestate는 남지 않는다.
	if logs := e.logs.String(); !strings.Contains(logs, `"trace_id":"`+traceIDHex+`"`) {
		t.Fatalf("attach log에 trace_id가 없음:\n%s", logs)
	} else if strings.Contains(logs, tsValid) {
		t.Fatalf("log에 tracestate 원문이 남음:\n%s", logs)
	}
}

// 유효하지 않은 Trace는 업무 message를 실패시키지 않는다. 그 관측 field만 폐기하고 가짜 Trace를 만들지 않으며 원문을 log에 복사하지 않는다.
// traceparent가 유효하고 tracestate만 잘못되면 traceparent는 유지한다.
func TestInvalidTraceContextDoesNotFailControlEventsAndIsNotEchoedOrLogged(t *testing.T) {
	const badParent, badState = "INVALID-TRACE-MARKER-7d20", "INVALID-STATE-MARKER-31fa"
	variants := []struct {
		name                  string
		fields                map[string]any
		wantParent, wantState string
	}{
		{"malformed traceparent", map[string]any{"traceparent": badParent, "tracestate": tsValid}, "", ""},
		{"traceparent wrong type", map[string]any{"traceparent": 12345, "tracestate": tsValid}, "", ""},
		{"traceparent too long", map[string]any{"traceparent": tpSampled + strings.Repeat("0", 600), "tracestate": tsValid}, "", ""},
		{"tracestate without traceparent", map[string]any{"tracestate": tsValid}, "", ""},
		{"invalid tracestate only", map[string]any{"traceparent": tpSampled, "tracestate": badState}, tpSampled, ""},
		{"tracestate wrong type", map[string]any{"traceparent": tpSampled, "tracestate": []string{badState}}, tpSampled, ""},
	}
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			e := newEnv(t)

			// Browser → Connector
			s, d := e.liveSession()
			p, _, err := e.dialBrowser(s.OwnerCookie, trustedOrigin)
			if err != nil {
				t.Fatalf("Browser WSS dial error = %v", err)
			}
			p.writeText(browserAttachMessage(t, s, v.fields, nil))
			attached := p.readJSON()
			if attached["type"] != "TERMINAL_ATTACHED" {
				t.Fatalf("Trace 때문에 attach가 실패함: %v", attached)
			}
			requireTrace(t, "TERMINAL_ATTACHED", attached, v.wantParent, v.wantState)
			requireTrace(t, "attach의 TERMINAL_DATA_RESIZE", d.readJSON(), v.wantParent, v.wantState)

			p.writeText(browserResizeMessage(t, s, 101, 31, v.fields))
			resize := d.readJSON()
			requireTrace(t, "TERMINAL_DATA_RESIZE", resize, v.wantParent, v.wantState)
			if pl := payload(t, resize); pl["cols"] != float64(101) || pl["rows"] != float64(31) {
				t.Fatalf("Trace 때문에 resize 값이 바뀜: %v", pl)
			}

			// Connector → Relay. DATA_ATTACH와 DATA_ENDED도 Trace 때문에 거절되지 않는다.
			s2 := e.newSession()
			dp, _, err := e.dialData(connectorCred)
			if err != nil {
				t.Fatalf("Data WSS dial error = %v", err)
			}
			dp.writeText(dataAttachMessage(t, s2, v.fields))
			dataAttached := dp.readJSON()
			if dataAttached["type"] != "TERMINAL_DATA_ATTACHED" {
				t.Fatalf("Trace 때문에 Data attach가 실패함: %v", dataAttached)
			}
			requireTrace(t, "TERMINAL_DATA_ATTACHED", dataAttached, v.wantParent, v.wantState)

			if err := e.relay.Activate(s2.ID, e.clock.Now().Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			b2 := e.connectBrowser(s2)
			dp.readJSON() // attach 때 Browser의 크기를 PTY에 반영하는 TERMINAL_DATA_RESIZE
			dp.writeText(dataEndedMessage(t, s2, v.fields))
			ended := b2.readJSON()
			if ended["type"] != "TERMINAL_SESSION_ENDED" || payload(t, ended)["reason"] != "PTY_EXITED" {
				t.Fatalf("Trace 때문에 종료 처리가 바뀜: %v", ended)
			}
			requireTrace(t, "TERMINAL_SESSION_ENDED", ended, v.wantParent, v.wantState)

			// 잘못된 원문은 log에 없고 가짜 Trace ID도 만들지 않는다.
			logs := e.logs.String()
			for _, raw := range []string{badParent, badState} {
				if strings.Contains(logs, raw) {
					t.Fatalf("log에 잘못된 Trace 원문이 남음: %q", raw)
				}
			}
			if v.wantParent == "" && strings.Contains(logs, `"trace_id"`) {
				t.Fatalf("유효한 Trace가 없는데 trace_id가 log에 남음:\n%s", logs)
			}
		})
	}
}

// Connector의 control event(TERMINAL_DATA_ATTACH, TERMINAL_DATA_ENDED)도 Trace를 이어 준다.
// 응답/오류는 요청의 Trace를 돌려주고, 종료는 Control 호출 ctx와 Browser의 TERMINAL_SESSION_ENDED에 이어진다.
func TestConnectorControlEventsPropagateTraceContext(t *testing.T) {
	e := newEnv(t)
	s := e.newSession()
	dp, _, err := e.dialData(connectorCred)
	if err != nil {
		t.Fatalf("Data WSS dial error = %v", err)
	}
	dp.writeText(dataAttachMessage(t, s, map[string]any{"traceparent": tpSampled, "tracestate": tsValid}))
	attached := dp.readJSON()
	if attached["type"] != "TERMINAL_DATA_ATTACHED" || attached["replyToMessageId"] != "data-attach-1" {
		t.Fatalf("attach 응답 = %v", attached)
	}
	requireTrace(t, "TERMINAL_DATA_ATTACHED", attached, tpSampled, tsValid)

	// 같은 connection의 두 번째 attach는 fatal ERROR이며 그 요청의 Trace를 돌려준다.
	dp.writeText(dataAttachMessage(t, s, map[string]any{"messageId": "data-attach-2", "traceparent": tpUnsampled}))
	rejected := dp.readJSON()
	if rejected["type"] != "ERROR" || rejected["replyToMessageId"] != "data-attach-2" {
		t.Fatalf("중복 attach 응답 = %v, want ERROR replyTo data-attach-2", rejected)
	}
	requireTrace(t, "중복 attach의 ERROR", rejected, tpUnsampled, "")

	// 종료 통지: Control 호출의 ctx, Control이 받는 End, Browser의 종료 통지에 같은 Trace가 이어진다.
	live, liveData := e.liveSession()
	b := e.connectBrowser(live)
	liveData.readJSON() // attach 때 Browser의 크기를 PTY에 반영하는 TERMINAL_DATA_RESIZE
	liveData.writeText(dataEndedMessage(t, live, map[string]any{"traceparent": tpUnsampled, "tracestate": tsValid}))

	ended := b.readJSON()
	if ended["type"] != "TERMINAL_SESSION_ENDED" {
		t.Fatalf("종료 통지 = %v", ended)
	}
	requireTrace(t, "TERMINAL_SESSION_ENDED", ended, tpUnsampled, tsValid)
	want := tracecontext.Context{Traceparent: tpUnsampled, Tracestate: tsValid}
	_, _, endedCtx := e.control.traces()
	_, _, closed, endedCalls := e.control.snapshot()
	if len(endedCalls) != 1 || endedCalls[0].End.Trace != want {
		t.Fatalf("Control이 받은 End = %+v, want Trace %v", endedCalls, want)
	}
	if len(endedCtx) != 1 || endedCtx[0] != want {
		t.Fatalf("Control ctx Trace = %v, want [%v]", endedCtx, want)
	}
	if len(closed) != 0 {
		t.Fatalf("Connector가 알린 종료에서 CloseSession이 호출됨: %v", closed)
	}
}

// attach가 거절되어도 오류 응답은 요청의 Trace를 돌려준다. 그 Trace는 Control 호출 ctx에도 전달되었다.
func TestRejectedAttachEchoesTheRequestTraceContext(t *testing.T) {
	e := newEnv(t)
	s := e.newSession()
	e.connectData(s)

	p, _, err := e.dialBrowser(s.OwnerCookie, trustedOrigin)
	if err != nil {
		t.Fatalf("Browser WSS dial error = %v", err)
	}
	wrong := s
	wrong.Token = "not-the-attach-token"
	p.writeText(browserAttachMessage(t, wrong, map[string]any{"traceparent": tpSampled, "tracestate": tsValid}, nil))

	rejected := p.readJSON()
	if rejected["type"] != "ERROR" || payload(t, rejected)["code"] != "INVALID_SESSION_TOKEN" || rejected["replyToMessageId"] != "attach-1" {
		t.Fatalf("attach 거절 응답 = %v", rejected)
	}
	requireTrace(t, "ERROR", rejected, tpSampled, tsValid)
	authorize, _, _ := e.control.traces()
	if len(authorize) != 1 || authorize[0] != (tracecontext.Context{Traceparent: tpSampled, Tracestate: tsValid}) {
		t.Fatalf("Control ctx Trace = %v", authorize)
	}
	if strings.Contains(string(marshal(t, rejected)), wrong.Token) {
		t.Fatal("오류 응답에 attach token이 담김")
	}
}

// PTY Binary frame에는 Trace Context나 어떤 envelope도 붙이지 않는다. byte stream은 양방향 모두 그대로 전달된다.
func TestBinaryFramesNeverCarryTraceContext(t *testing.T) {
	e := newEnv(t)
	s, d := e.liveSession()
	p, _, err := e.dialBrowser(s.OwnerCookie, trustedOrigin)
	if err != nil {
		t.Fatalf("Browser WSS dial error = %v", err)
	}
	p.writeText(browserAttachMessage(t, s, map[string]any{"traceparent": tpSampled, "tracestate": tsValid}, nil))
	p.readJSON() // TERMINAL_ATTACHED
	d.readJSON() // attach의 TERMINAL_DATA_RESIZE

	// Trace처럼 보이는 byte도 본문일 뿐이다. 그대로 오가야 하고 본문에 덧붙는 것이 없어야 한다.
	input := []byte(inputMarker + "\x1b[A\xff\x00traceparent:" + tpSampled)
	p.writeBinary(input)
	if got := d.readBinary(); string(got) != string(input) {
		t.Fatalf("INPUT = %q, want 변형 없이 %q", got, input)
	}
	output := []byte(outputMarker + "\x00\xfe" + tpSampled)
	d.writeBinary(output)
	if got := p.readBinary(); string(got) != string(output) {
		t.Fatalf("OUTPUT = %q, want 변형 없이 %q", got, output)
	}

	// Binary 본문은 log에도 남지 않는다.
	logs := e.logs.String()
	for _, marker := range []string{inputMarker, outputMarker} {
		if strings.Contains(logs, marker) {
			t.Fatalf("log에 PTY 본문이 남음: %q", marker)
		}
	}
}
