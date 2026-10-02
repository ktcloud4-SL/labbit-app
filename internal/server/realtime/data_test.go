package realtime_test

import (
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
)

// 첫 Data attach는 resumed=false, 같은 살아 있는 TerminalSession의 transport reconnect는 resumed=true다.
// 어느 쪽도 history를 제공하지 않고(historyAvailable=false), 새 channel이 current가 되며 이전 channel은 종료된다.
func TestDataAttachThenReconnectIsResumedWithoutHistory(t *testing.T) {
	e := newEnv(t)
	s := e.newSession()

	d1 := e.connectData(s)
	p := payload(t, d1.attached)
	if p["resumed"] != false || p["historyAvailable"] != false {
		t.Fatalf("첫 attach payload = %v, want resumed=false historyAvailable=false", p)
	}
	// 응답은 attach의 messageId를 가리키고 같은 TerminalSession/LabInstance/generation을 싣는다.
	if d1.attached["replyToMessageId"] != "data-attach-1" || d1.attached["terminalSessionId"] != s.ID ||
		d1.attached["labInstanceId"] != s.LabID || d1.attached["generation"] != float64(s.Generation) {
		t.Fatalf("ATTACHED envelope = %v", d1.attached)
	}
	if !e.relay.DataBound(s.ID) {
		t.Fatal("attach 뒤 DataBound = false")
	}

	d2 := e.connectData(s)
	p = payload(t, d2.attached)
	if p["resumed"] != true || p["historyAvailable"] != false {
		t.Fatalf("reconnect payload = %v, want resumed=true historyAvailable=false", p)
	}
	// 이전 channel은 current가 아니므로 종료된다. TerminalSession은 그대로다.
	if code := d1.expectClose(); code != websocket.CloseNormalClosure {
		t.Fatalf("교체된 data channel close code = %d, want 1000", code)
	}
	if !e.relay.DataBound(s.ID) {
		t.Fatal("교체 뒤 DataBound = false")
	}
	if e.relay.Sessions() != 1 {
		t.Fatalf("Sessions = %d, want 1", e.relay.Sessions())
	}
	_, _, closed, ended := e.control.snapshot()
	if len(closed)+len(ended) != 0 {
		t.Fatalf("data channel 교체가 TerminalSession을 종료함: closed %v ended %v", closed, ended)
	}
}

// Data attach는 기대한 TerminalSession과 인증된 Connector identity, terminalSessionId, labInstanceId, generation이
// 모두 정확히 일치해야 bind된다. 하나라도 다르면 bind하지 않고 close한다. message가 주장한 correlation을 안전하게 되돌려 줄 수 있으면
// (terminal-data.schema.json의 Envelope가 요구하는 세 값을 모두 읽을 수 있을 때) ERROR를 먼저 보낸다.
func TestDataAttachRejectsMismatches(t *testing.T) {
	tests := []struct {
		name       string
		credential string
		fields     map[string]any
		// wantCode는 ERROR로 돌려줄 수 있을 때의 code다. 비어 있으면 ERROR 없이 close만 한다.
		wantCode string
	}{
		{name: "unknown terminal session", fields: map[string]any{"terminalSessionId": "00000000-0000-4000-8000-00000000ffff"}, wantCode: "INVALID_SESSION"},
		{name: "wrong lab instance", fields: map[string]any{"labInstanceId": "10000000-0000-4000-8000-00000000ffff"}, wantCode: "INVALID_SESSION"},
		{name: "wrong generation", fields: map[string]any{"generation": 2}, wantCode: "STALE_GENERATION"},
		{name: "another connector with a valid credential", credential: connectorCred2, wantCode: "FORBIDDEN"},
		{name: "runtimeId missing", fields: map[string]any{"payload": map[string]any{}}, wantCode: "PROTOCOL_ERROR"},
		{name: "runtimeId empty", fields: map[string]any{"payload": map[string]any{"runtimeId": ""}}, wantCode: "PROTOCOL_ERROR"},
		{name: "messageId missing", fields: map[string]any{"messageId": nil}, wantCode: "PROTOCOL_ERROR"},
		{name: "first message is not attach", fields: map[string]any{"type": "TERMINAL_DATA_ENDED", "payload": map[string]any{"reason": "PTY_EXITED"}}, wantCode: "PROTOCOL_ERROR"},
		// 읽을 수 없거나 int64로 표현할 수 없는 generation은 correlation을 되돌려 줄 수 없다.
		{name: "generation beyond int64", fields: map[string]any{"generation": 1e30}},
		{name: "generation zero", fields: map[string]any{"generation": 0}},
		{name: "generation as string", fields: map[string]any{"generation": "1"}},
		{name: "terminalSessionId as number", fields: map[string]any{"terminalSessionId": 7}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			s := e.newSession()
			cred := connectorCred
			if tt.credential != "" {
				cred = tt.credential
			}
			p, _, err := e.dialData(cred)
			if err != nil {
				t.Fatalf("dial error = %v", err)
			}
			p.writeText(dataAttachMessage(t, s, tt.fields))

			if tt.wantCode != "" {
				msg := p.readJSON()
				if msg["type"] != "ERROR" {
					t.Fatalf("응답 = %v, want ERROR", msg)
				}
				errPayload := payload(t, msg)
				if errPayload["code"] != tt.wantCode || errPayload["fatal"] != true {
					t.Fatalf("ERROR payload = %v, want code %s fatal", errPayload, tt.wantCode)
				}
				// Schema-valid ERROR는 correlation을 싣는다.
				for _, key := range []string{"terminalSessionId", "labInstanceId", "generation", "messageId", "sentAt"} {
					if _, ok := msg[key]; !ok {
						t.Fatalf("ERROR envelope에 %s가 없음: %v", key, msg)
					}
				}
			}
			if code := p.expectClose(); code != websocket.ClosePolicyViolation {
				t.Fatalf("close code = %d, want 1008", code)
			}
			if e.relay.DataBound(s.ID) {
				t.Fatal("거절된 attach가 data channel을 bind함")
			}
			if e.relay.Sessions() != 1 {
				t.Fatalf("Sessions = %d, want 1: 거절이 기대한 TerminalSession을 지움", e.relay.Sessions())
			}
		})
	}
}

// correlation을 안전하게 되돌려 줄 수 없는 잘못된 첫 message는 ERROR 없이 close한다(terminal-data.schema.json의 Envelope가
// terminalSessionId, labInstanceId, generation을 요구하므로 Schema-valid ERROR를 만들 수 없다).
func TestDataAttachUnparsableFirstMessageIsClosed(t *testing.T) {
	tests := []struct {
		name  string
		write func(p *peer)
	}{
		{name: "not json", write: func(p *peer) { p.writeText([]byte("hello")) }},
		{name: "json array", write: func(p *peer) { p.writeText([]byte("[]")) }},
		{name: "binary before attach", write: func(p *peer) { p.writeBinary([]byte("raw bytes")) }},
		{name: "attach without session id", write: func(p *peer) {
			p.writeText([]byte(`{"type":"TERMINAL_DATA_ATTACH","messageId":"m","sentAt":"2026-10-01T09:00:00Z","payload":{"runtimeId":"r"}}`))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			s := e.newSession()
			p, _, err := e.dialData(connectorCred)
			if err != nil {
				t.Fatalf("dial error = %v", err)
			}
			tt.write(p)
			if code := p.expectClose(); code != websocket.ClosePolicyViolation {
				t.Fatalf("close code = %d, want 1008", code)
			}
			if e.relay.DataBound(s.ID) {
				t.Fatal("거절된 attach가 data channel을 bind함")
			}
		})
	}
}

// JSON Text application message는 1 MiB를 넘으면 해석하지 않고 1009로 종료한다. Binary 한도가 JSON 한도보다 큰 구성에서도
// Text는 1 MiB를 넘는 만큼을 메모리에 적재하지 않고 같은 결과여야 한다(두 경로를 모두 확인한다).
func TestDataAttachOversizedJSONIsClosedWith1009(t *testing.T) {
	for name, mod := range map[string]func(*realtime.Options){
		"default limits":              func(*realtime.Options) {},
		"binary limit above json cap": func(o *realtime.Options) { o.MaxBinaryMessage = 4 << 20 },
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, mod)
			s := e.newSession()
			p, _, err := e.dialData(connectorCred)
			if err != nil {
				t.Fatalf("dial error = %v", err)
			}
			oversized := dataAttachMessage(t, s, map[string]any{"payload": map[string]any{"runtimeId": "r", "padding": strings.Repeat("x", 1<<20)}})
			p.writeText(oversized)
			if code := p.expectClose(); code != websocket.CloseMessageTooBig {
				t.Fatalf("close code = %d, want 1009", code)
			}
			if e.relay.DataBound(s.ID) {
				t.Fatal("초과한 message가 data channel을 bind함")
			}
		})
	}
}

// 1 MiB 이하의 message는 처리한다. 한도를 경계 값에서 확인한다.
func TestDataAttachAcceptsJSONAtTheLimit(t *testing.T) {
	e := newEnv(t)
	s := e.newSession()
	p, _, err := e.dialData(connectorCred)
	if err != nil {
		t.Fatalf("dial error = %v", err)
	}
	// sentAt을 고정해 message 길이가 호출마다 달라지지 않게 한다(RFC3339Nano는 끝의 0을 생략한다).
	const sentAt = "2026-10-01T09:00:00Z"
	base := len(dataAttachMessage(t, s, map[string]any{"sentAt": sentAt, "payload": map[string]any{"runtimeId": "r", "padding": ""}}))
	atLimit := dataAttachMessage(t, s, map[string]any{"sentAt": sentAt, "payload": map[string]any{"runtimeId": "r", "padding": strings.Repeat("x", (1<<20)-base)}})
	if len(atLimit) != 1<<20 {
		t.Fatalf("message length = %d, want %d", len(atLimit), 1<<20)
	}
	p.writeText(atLimit)
	if msg := p.readJSON(); msg["type"] != "TERMINAL_DATA_ATTACHED" {
		t.Fatalf("한도 크기의 attach 응답 = %v", msg)
	}
}

func TestDataAttachTimesOut(t *testing.T) {
	e := newEnv(t, func(o *realtime.Options) { o.AttachTimeout = 150 * time.Millisecond })
	p, _, err := e.dialData(connectorCred)
	if err != nil {
		t.Fatalf("dial error = %v", err)
	}
	if code := p.expectClose(); code != websocket.ClosePolicyViolation {
		t.Fatalf("close code = %d, want 1008", code)
	}
}

// attach 뒤 protocol을 어기면 그 data channel만 끊는다. 단절은 PTY 종료가 아니므로 TerminalSession은 유지한다.
func TestDataProtocolViolationDropsOnlyTheChannel(t *testing.T) {
	tests := []struct {
		name  string
		write func(t *testing.T, s session, p *peer)
	}{
		{name: "second attach", write: func(t *testing.T, s session, p *peer) { p.writeText(dataAttachMessage(t, s, nil)) }},
		{name: "malformed ended", write: func(t *testing.T, s session, p *peer) {
			p.writeText(dataAttachMessage(t, s, map[string]any{"type": "TERMINAL_DATA_ENDED", "payload": map[string]any{}}))
		}},
		{name: "ended for another terminal session", write: func(t *testing.T, s session, p *peer) {
			p.writeText(dataAttachMessage(t, s, map[string]any{"type": "TERMINAL_DATA_ENDED", "terminalSessionId": "00000000-0000-4000-8000-00000000ffff", "payload": map[string]any{"reason": "PTY_EXITED"}}))
		}},
		{name: "ended for another lab instance", write: func(t *testing.T, s session, p *peer) {
			p.writeText(dataAttachMessage(t, s, map[string]any{"type": "TERMINAL_DATA_ENDED", "labInstanceId": "10000000-0000-4000-8000-00000000ffff", "payload": map[string]any{"reason": "PTY_EXITED"}}))
		}},
		{name: "ended for another generation", write: func(t *testing.T, s session, p *peer) {
			p.writeText(dataAttachMessage(t, s, map[string]any{"type": "TERMINAL_DATA_ENDED", "generation": 2, "payload": map[string]any{"reason": "PTY_EXITED"}}))
		}},
		{name: "not json", write: func(t *testing.T, s session, p *peer) { p.writeText([]byte("{")) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			s, d := e.liveSession()
			b := e.connectBrowser(s)
			d.readJSON() // attach 시 Browser의 크기를 PTY에 반영하는 TERMINAL_DATA_RESIZE

			tt.write(t, s, d.peer)
			msg := d.readJSON()
			if msg["type"] != "ERROR" {
				t.Fatalf("응답 = %v, want ERROR", msg)
			}
			if code := d.expectClose(); code != websocket.ClosePolicyViolation {
				t.Fatalf("close code = %d, want 1008", code)
			}

			// 잘못된 message가 어떤 TerminalSession도 종료시키지 않는다.
			eventually(t, "data channel 해제", func() bool { return !e.relay.DataBound(s.ID) })
			if e.relay.Sessions() != 1 {
				t.Fatalf("Sessions = %d, want 1", e.relay.Sessions())
			}
			_, _, closed, ended := e.control.snapshot()
			if len(closed)+len(ended) != 0 {
				t.Fatalf("protocol 위반이 TerminalSession을 종료함: closed %v ended %v", closed, ended)
			}
			b.expectNoMessage(150 * time.Millisecond) // Browser에 TERMINAL_SESSION_ENDED가 가지 않는다.
		})
	}
}

// Connector가 보낼 일이 없는 message type은 계약 확장에 대비해 무시한다. 연결과 TerminalSession에는 영향이 없다.
func TestDataUnknownMessageTypesAreIgnored(t *testing.T) {
	e := newEnv(t)
	s, d := e.liveSession()
	d.writeText(dataAttachMessage(t, s, map[string]any{"type": "TERMINAL_DATA_RESIZE", "payload": map[string]any{"cols": 1, "rows": 1}}))
	d.writeText([]byte(`{"type":"SOMETHING_NEW","messageId":"m","sentAt":"2026-10-01T09:00:00Z","payload":{}}`))
	d.expectNoMessage(150 * time.Millisecond)
	if !e.relay.DataBound(s.ID) {
		t.Fatal("무시해야 하는 message가 data channel을 끊음")
	}
}

// Activate는 valid Terminal Data WSS가 실제로 bind된 TerminalSession에서만 성공한다.
func TestActivateRequiresBoundData(t *testing.T) {
	e := newEnv(t)
	s := e.newSession()
	deadline := e.clock.Now().Add(realtime.DefaultGrace)

	if err := e.relay.Activate(s.ID, deadline); err != realtime.ErrDataNotBound {
		t.Fatalf("Activate() without data = %v, want ErrDataNotBound", err)
	}
	if err := e.relay.Activate("unknown", deadline); err != realtime.ErrSessionNotFound {
		t.Fatalf("Activate(unknown) = %v, want ErrSessionNotFound", err)
	}

	e.connectData(s)
	if err := e.relay.Activate(s.ID, deadline); err != nil {
		t.Fatalf("Activate() with data error = %v", err)
	}
}

// Activate 전의 TerminalSession에는 Browser가 attach할 수 없다.
func TestBrowserCannotAttachBeforeActivate(t *testing.T) {
	e := newEnv(t)
	s := e.newSession()
	e.connectData(s)

	p, _, err := e.dialBrowser(s.OwnerCookie, trustedOrigin)
	if err != nil {
		t.Fatalf("dial error = %v", err)
	}
	p.writeText(browserAttachMessage(t, s, nil, nil))
	msg := p.readJSON()
	if msg["type"] != "ERROR" || payload(t, msg)["code"] != "SESSION_NOT_FOUND" {
		t.Fatalf("응답 = %v, want ERROR SESSION_NOT_FOUND", msg)
	}
	if code := p.expectClose(); code != 4003 {
		t.Fatalf("close code = %d, want 4003", code)
	}
	attached, _, _, _ := e.control.snapshot()
	if len(attached) != 0 {
		t.Fatalf("Activate 전 attach가 기록됨: %v", attached)
	}
}
