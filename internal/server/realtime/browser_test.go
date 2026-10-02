package realtime_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
)

// 최초 attach는 resumed=false, historyAvailable=false다. 응답은 attach의 messageId를 가리키고 attach를 Control에 기록한다.
// Browser의 현재 terminal 크기는 PTY에 반영하도록 TERMINAL_DATA_RESIZE로 Connector에 전달한다.
func TestBrowserAttachSucceeds(t *testing.T) {
	e := newEnv(t)
	s, d := e.liveSession()
	b := e.connectBrowser(s)

	p := payload(t, b.attached)
	if p["resumed"] != false || p["historyAvailable"] != false {
		t.Fatalf("attach payload = %v, want resumed=false historyAvailable=false", p)
	}
	if b.attached["replyToMessageId"] != "attach-1" || b.attached["terminalSessionId"] != s.ID {
		t.Fatalf("ATTACHED envelope = %v", b.attached)
	}
	attached, _, _, _ := e.control.snapshot()
	if len(attached) != 1 || attached[0] != s.ID {
		t.Fatalf("RecordAttached = %v, want [%s]", attached, s.ID)
	}

	resize := d.readJSON()
	rp := payload(t, resize)
	if resize["type"] != "TERMINAL_DATA_RESIZE" || rp["cols"] != float64(120) || rp["rows"] != float64(40) ||
		resize["terminalSessionId"] != s.ID || resize["labInstanceId"] != s.LabID || resize["generation"] != float64(s.Generation) {
		t.Fatalf("attach 직후 Connector 메시지 = %v", resize)
	}
}

// Control이 attach를 거절하면 그 이유를 안정적인 ERROR code와 close code로 전달한다. 모든 오류는 fatal이고 attach를 가리킨다.
func TestBrowserAttachRejectedByControl(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantCode  string
		wantClose int
	}{
		{name: "login session no longer valid", err: realtime.ErrUnauthenticated, wantCode: "AUTH_REQUIRED", wantClose: 4001},
		{name: "invalid token", err: realtime.ErrInvalidToken, wantCode: "INVALID_SESSION_TOKEN", wantClose: 4001},
		{name: "expired token", err: realtime.ErrTokenExpired, wantCode: "INVALID_SESSION_TOKEN", wantClose: 4001},
		{name: "no permission", err: realtime.ErrForbidden, wantCode: "FORBIDDEN", wantClose: 4002},
		{name: "unknown session", err: realtime.ErrSessionNotFound, wantCode: "SESSION_NOT_FOUND", wantClose: 4003},
		{name: "ended session", err: realtime.ErrSessionEnded, wantCode: "SESSION_EXPIRED", wantClose: 4003},
		{name: "stale generation after reset", err: realtime.ErrLabMutation, wantCode: "LAB_MUTATION", wantClose: 4006},
		{name: "dependency failure", err: fmt.Errorf("%w: database is down", realtime.ErrDependencyUnavailable), wantCode: "INTERNAL_ERROR", wantClose: 1011},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			s, _ := e.liveSession()
			e.control.authorizeErr = tt.err

			p, _, err := e.dialBrowser(s.OwnerCookie, trustedOrigin)
			if err != nil {
				t.Fatalf("dial error = %v", err)
			}
			p.writeText(browserAttachMessage(t, s, nil, nil))

			msg := p.readJSON()
			if msg["type"] != "ERROR" || msg["replyToMessageId"] != "attach-1" {
				t.Fatalf("응답 = %v, want ERROR replying to attach-1", msg)
			}
			ep := payload(t, msg)
			if ep["code"] != tt.wantCode || ep["fatal"] != true {
				t.Fatalf("ERROR payload = %v, want code %s fatal", ep, tt.wantCode)
			}
			// 오류 message에 token, ID, 내부 오류 문구를 싣지 않는다.
			raw := string(marshal(t, msg))
			for _, secret := range []string{s.Token, s.ID, "database is down"} {
				if strings.Contains(raw, secret) {
					t.Fatalf("ERROR message가 %q를 포함함: %s", secret, raw)
				}
			}
			if code := p.expectClose(); code != tt.wantClose {
				t.Fatalf("close code = %d, want %d", code, tt.wantClose)
			}
			attached, _, _, _ := e.control.snapshot()
			if len(attached) != 0 {
				t.Fatalf("거절된 attach가 기록됨: %v", attached)
			}
		})
	}
}

// 현재 권한 판정은 Control이 한다. 다른 사용자, 잘못된 token, 없는 session은 attach할 수 없다.
func TestBrowserAttachAuthorizationFlows(t *testing.T) {
	tests := []struct {
		name      string
		cookie    string
		fields    map[string]any
		payload   map[string]any
		mutate    func(*fakeControl, session)
		wantCode  string
		wantClose int
	}{
		{name: "another user's login session", cookie: otherCookie, wantCode: "FORBIDDEN", wantClose: 4002},
		{name: "token of a different session", payload: map[string]any{"sessionToken": "test-attach-token-999"}, wantCode: "INVALID_SESSION_TOKEN", wantClose: 4001},
		{name: "empty-looking token is still not the token", payload: map[string]any{"sessionToken": " "}, wantCode: "INVALID_SESSION_TOKEN", wantClose: 4001},
		{name: "unknown terminal session", fields: map[string]any{"terminalSessionId": "00000000-0000-4000-8000-00000000ffff"}, wantCode: "SESSION_NOT_FOUND", wantClose: 4003},
		{
			// Control이 허용한 TerminalSession이 Relay가 등록한 correlation과 다르면 사용하지 않는다.
			name: "grant does not match the expected session", wantCode: "SESSION_NOT_FOUND", wantClose: 4003,
			mutate: func(c *fakeControl, s session) {
				c.mu.Lock()
				defer c.mu.Unlock()
				changed := c.sessions[s.ID]
				changed.Generation = 2
				c.sessions[s.ID] = changed
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			s, _ := e.liveSession()
			if tt.mutate != nil {
				tt.mutate(e.control, s)
			}
			cookie := ownerCookie
			if tt.cookie != "" {
				cookie = tt.cookie
			}
			p, _, err := e.dialBrowser(cookie, trustedOrigin)
			if err != nil {
				t.Fatalf("dial error = %v", err)
			}
			p.writeText(browserAttachMessage(t, s, tt.fields, tt.payload))

			msg := p.readJSON()
			if msg["type"] != "ERROR" || payload(t, msg)["code"] != tt.wantCode {
				t.Fatalf("응답 = %v, want ERROR %s", msg, tt.wantCode)
			}
			if code := p.expectClose(); code != tt.wantClose {
				t.Fatalf("close code = %d, want %d", code, tt.wantClose)
			}
			attached, _, _, _ := e.control.snapshot()
			if len(attached) != 0 {
				t.Fatalf("거절된 attach가 기록됨: %v", attached)
			}
		})
	}
}

// 첫 message가 valid TERMINAL_ATTACH가 아니면 protocol/policy 위반(1008)이다. 그 전에는 어떤 Binary도 처리하지 않는다.
func TestBrowserFirstMessageMustBeValidAttach(t *testing.T) {
	tests := []struct {
		name  string
		write func(t *testing.T, s session, p *peer)
	}{
		{name: "binary before attach", write: func(t *testing.T, s session, p *peer) { p.writeBinary([]byte(inputMarker)) }},
		{name: "not json", write: func(t *testing.T, s session, p *peer) { p.writeText([]byte("attach please")) }},
		{name: "json array", write: func(t *testing.T, s session, p *peer) { p.writeText([]byte("[]")) }},
		{name: "resize instead of attach", write: func(t *testing.T, s session, p *peer) {
			p.writeText(marshal(t, map[string]any{
				"type": "TERMINAL_RESIZE", "messageId": "m", "sentAt": time.Now().UTC().Format(time.RFC3339Nano),
				"terminalSessionId": s.ID, "payload": map[string]any{"cols": 80, "rows": 24},
			}))
		}},
		{name: "live subscribe instead of attach", write: func(t *testing.T, s session, p *peer) {
			p.writeText([]byte(`{"type":"LIVE_SUBSCRIBE","messageId":"m","sentAt":"2026-10-01T09:00:00Z","liveSessionId":"l","payload":{}}`))
		}},
		{name: "type with different case", write: func(t *testing.T, s session, p *peer) {
			p.writeText(browserAttachMessage(t, s, map[string]any{"type": "terminal_attach"}, nil))
		}},
		{name: "property name with different case", write: func(t *testing.T, s session, p *peer) {
			p.writeText([]byte(fmt.Sprintf(`{"type":"TERMINAL_ATTACH","messageId":"m","sentAt":"2026-10-01T09:00:00Z","TerminalSessionId":%q,"payload":{"sessionToken":%q,"cols":1,"rows":1}}`, s.ID, s.Token)))
		}},
		{name: "messageId missing", write: func(t *testing.T, s session, p *peer) {
			p.writeText(browserAttachMessage(t, s, map[string]any{"messageId": nil}, nil))
		}},
		{name: "sentAt not a date-time", write: func(t *testing.T, s session, p *peer) {
			p.writeText(browserAttachMessage(t, s, map[string]any{"sentAt": "yesterday"}, nil))
		}},
		{name: "terminalSessionId missing", write: func(t *testing.T, s session, p *peer) {
			p.writeText(browserAttachMessage(t, s, map[string]any{"terminalSessionId": nil}, nil))
		}},
		{name: "terminalSessionId empty", write: func(t *testing.T, s session, p *peer) {
			p.writeText(browserAttachMessage(t, s, map[string]any{"terminalSessionId": ""}, nil))
		}},
		{name: "payload not an object", write: func(t *testing.T, s session, p *peer) {
			p.writeText(browserAttachMessage(t, s, map[string]any{"payload": "x"}, nil))
		}},
		{name: "sessionToken missing", write: func(t *testing.T, s session, p *peer) {
			p.writeText(browserAttachMessage(t, s, nil, map[string]any{"sessionToken": nil}))
		}},
		{name: "sessionToken empty", write: func(t *testing.T, s session, p *peer) {
			p.writeText(browserAttachMessage(t, s, nil, map[string]any{"sessionToken": ""}))
		}},
		{name: "sessionToken not a string", write: func(t *testing.T, s session, p *peer) {
			p.writeText(browserAttachMessage(t, s, nil, map[string]any{"sessionToken": 123}))
		}},
		{name: "cols missing", write: func(t *testing.T, s session, p *peer) {
			p.writeText(browserAttachMessage(t, s, nil, map[string]any{"cols": nil}))
		}},
		{name: "cols zero", write: func(t *testing.T, s session, p *peer) {
			p.writeText(browserAttachMessage(t, s, nil, map[string]any{"cols": 0}))
		}},
		{name: "rows negative", write: func(t *testing.T, s session, p *peer) {
			p.writeText(browserAttachMessage(t, s, nil, map[string]any{"rows": -1}))
		}},
		{name: "cols fraction", write: func(t *testing.T, s session, p *peer) {
			p.writeText(browserAttachMessage(t, s, nil, map[string]any{"cols": 1.5}))
		}},
		{name: "rows as string", write: func(t *testing.T, s session, p *peer) {
			p.writeText(browserAttachMessage(t, s, nil, map[string]any{"rows": "40"}))
		}},
		{name: "cols null", write: func(t *testing.T, s session, p *peer) {
			p.writeText([]byte(fmt.Sprintf(`{"type":"TERMINAL_ATTACH","messageId":"m","sentAt":"2026-10-01T09:00:00Z","terminalSessionId":%q,"payload":{"sessionToken":%q,"cols":null,"rows":1}}`, s.ID, s.Token)))
		}},
		{name: "optional replyToMessageId empty", write: func(t *testing.T, s session, p *peer) {
			p.writeText(browserAttachMessage(t, s, map[string]any{"replyToMessageId": ""}, nil))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			s, _ := e.liveSession()
			p, _, err := e.dialBrowser(s.OwnerCookie, trustedOrigin)
			if err != nil {
				t.Fatalf("dial error = %v", err)
			}
			tt.write(t, s, p)

			msg := p.readJSON()
			if msg["type"] != "ERROR" || payload(t, msg)["code"] != "PROTOCOL_ERROR" || payload(t, msg)["fatal"] != true {
				t.Fatalf("응답 = %v, want fatal ERROR PROTOCOL_ERROR", msg)
			}
			if code := p.expectClose(); code != websocket.ClosePolicyViolation {
				t.Fatalf("close code = %d, want 1008", code)
			}
			attached, _, _, _ := e.control.snapshot()
			if len(attached) != 0 {
				t.Fatalf("유효하지 않은 attach가 기록됨: %v", attached)
			}
		})
	}
}

func TestBrowserAttachOversizedJSONIsClosedWith1009(t *testing.T) {
	for name, mod := range map[string]func(*realtime.Options){
		"default limits":              func(*realtime.Options) {},
		"binary limit above json cap": func(o *realtime.Options) { o.MaxBinaryMessage = 4 << 20 },
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, mod)
			s, _ := e.liveSession()
			p, _, err := e.dialBrowser(s.OwnerCookie, trustedOrigin)
			if err != nil {
				t.Fatalf("dial error = %v", err)
			}
			p.writeText(browserAttachMessage(t, s, nil, map[string]any{"padding": strings.Repeat("x", 1<<20)}))
			if code := p.expectClose(); code != websocket.CloseMessageTooBig {
				t.Fatalf("close code = %d, want 1009", code)
			}
			attached, _, _, _ := e.control.snapshot()
			if len(attached) != 0 {
				t.Fatalf("초과한 message가 attach됨: %v", attached)
			}
		})
	}
}

func TestBrowserAttachTimesOut(t *testing.T) {
	e := newEnv(t, func(o *realtime.Options) { o.AttachTimeout = 150 * time.Millisecond })
	p, _, err := e.dialBrowser(ownerCookie, trustedOrigin)
	if err != nil {
		t.Fatalf("dial error = %v", err)
	}
	if code := p.expectClose(); code != websocket.ClosePolicyViolation {
		t.Fatalf("close code = %d, want 1008", code)
	}
}

// Schema는 integer에 maximum을 두지 않고 1.0, 1e2 같은 표기도 integer로 본다. Schema-valid 값은 거절하지 않고 값을 바꾸지 않은
// 채 Connector에 그대로 전달한다(Relay가 임의의 상한을 만들지 않는다).
func TestBrowserAttachAcceptsEverySchemaValidPositiveInteger(t *testing.T) {
	for _, tt := range []struct{ cols, rows string }{
		{"1", "1"},
		{"1.0", "1e2"},
		{"9007199254740993", "100E-2"},
		{"1e999999999", "123456789012345678901234567890"},
	} {
		t.Run(tt.cols+"x"+tt.rows, func(t *testing.T) {
			e := newEnv(t)
			s, d := e.liveSession()
			p, _, err := e.dialBrowser(s.OwnerCookie, trustedOrigin)
			if err != nil {
				t.Fatalf("dial error = %v", err)
			}
			p.writeText([]byte(fmt.Sprintf(
				`{"type":"TERMINAL_ATTACH","messageId":"attach-1","sentAt":"2026-10-01T09:00:00Z","terminalSessionId":%q,"payload":{"sessionToken":%q,"cols":%s,"rows":%s}}`,
				s.ID, s.Token, tt.cols, tt.rows)))
			if msg := p.readJSON(); msg["type"] != "TERMINAL_ATTACHED" {
				t.Fatalf("응답 = %v, want TERMINAL_ATTACHED", msg)
			}
			text := d.readText()
			if !strings.Contains(text, `"cols":`+tt.cols+`,`) || !strings.Contains(text, `"rows":`+tt.rows+`}`) {
				t.Fatalf("Connector 메시지가 cols/rows 원문을 바꿈: %s", text)
			}
		})
	}
}

// PTY INPUT/OUTPUT은 JSON이나 base64로 감싸지 않은 raw byte stream이다. 유효한 UTF-8이 아니어도, ANSI escape sequence가
// frame 사이에서 끊겨도 byte를 해석하거나 바꾸지 않고 순서대로 전달한다.
func TestBinaryFramesAreRelayedAsRawBytes(t *testing.T) {
	e := newEnv(t)
	s, d := e.liveSession()
	b := e.connectBrowser(s)
	d.readJSON() // attach 시의 TERMINAL_DATA_RESIZE

	chunks := [][]byte{
		[]byte(inputMarker),
		{0xff, 0xfe, 0x00, 0x1b, '[', '3'}, // 유효하지 않은 UTF-8과 끊긴 ANSI escape sequence
		{'1', 'm', 0xe2, 0x82},             // 이어지는 sequence와 끊긴 UTF-8 문자의 앞부분
		{0xac, '\r', '\n'},                 // UTF-8 문자의 뒷부분
		{},                                 // 빈 frame도 그대로
		bytes.Repeat([]byte{0x7f}, 100000),
	}
	for _, chunk := range chunks {
		b.writeBinary(chunk)
	}
	for i, want := range chunks {
		if got := d.readBinary(); !bytes.Equal(got, want) {
			t.Fatalf("INPUT chunk %d = %x, want %x", i, got[:min(len(got), 16)], want[:min(len(want), 16)])
		}
	}

	outputs := [][]byte{[]byte(outputMarker), {0x1b, '[', '?', '2', '5', 'l', 0x80, 0x81}, bytes.Repeat([]byte("ab"), 50000)}
	for _, chunk := range outputs {
		d.writeBinary(chunk)
	}
	for i, want := range outputs {
		if got := b.readBinary(); !bytes.Equal(got, want) {
			t.Fatalf("OUTPUT chunk %d 길이 = %d, want %d", i, len(got), len(want))
		}
	}
}

// Binary는 JSON Text의 1 MiB 한도 대상이 아니다. bounded read 한도(MaxBinaryMessage) 안에서는 더 큰 frame도 전달한다.
func TestBinaryFramesAreNotSubjectToJSONTextLimit(t *testing.T) {
	e := newEnv(t, func(o *realtime.Options) { o.MaxBinaryMessage = 4 << 20 })
	s, d := e.liveSession()
	b := e.connectBrowser(s)
	d.readJSON()

	big := bytes.Repeat([]byte{'z'}, (3<<20)/2) // 1.5 MiB
	b.writeBinary(big)
	if got := d.readBinary(); !bytes.Equal(got, big) {
		t.Fatalf("Binary INPUT 길이 = %d, want %d", len(got), len(big))
	}
	d.writeBinary(big)
	if got := b.readBinary(); !bytes.Equal(got, big) {
		t.Fatalf("Binary OUTPUT 길이 = %d, want %d", len(got), len(big))
	}
}

// Binary frame도 bounded read 한도를 넘으면 끊는다. 무제한으로 메모리에 읽지 않는다.
func TestBinaryFrameAboveReadLimitClosesTheConnection(t *testing.T) {
	e := newEnv(t, func(o *realtime.Options) { o.MaxBinaryMessage = 2 << 20 })
	s, d := e.liveSession()
	b := e.connectBrowser(s)
	d.readJSON()

	// 서버는 한도를 넘는 frame의 header를 읽자마자 1009로 닫으므로 client의 write가 도중에 실패해도 정상이다.
	_ = b.conn.WriteMessage(websocket.BinaryMessage, bytes.Repeat([]byte{'z'}, 3<<20))
	if code := b.expectClose(); code != websocket.CloseMessageTooBig {
		t.Fatalf("close code = %d, want 1009", code)
	}
	// Connector의 PTY와 data channel은 그대로다. Browser attachment만 끝나고 grace가 시작된다.
	eventually(t, "DETACHED 기록", func() bool { _, detached, _, _ := e.control.snapshot(); return len(detached) == 1 })
	if !e.relay.DataBound(s.ID) || e.relay.Sessions() != 1 {
		t.Fatal("Browser의 과도한 Binary frame이 TerminalSession이나 data channel을 끊음")
	}
}

// Browser의 TERMINAL_RESIZE는 같은 TerminalSession correlation을 가진 TERMINAL_DATA_RESIZE로 Connector에 전달한다.
func TestResizeIsForwardedToConnector(t *testing.T) {
	e := newEnv(t)
	s, d := e.liveSession()
	b := e.connectBrowser(s)
	d.readJSON()

	b.writeText(marshal(t, map[string]any{
		"type": "TERMINAL_RESIZE", "messageId": "resize-1", "sentAt": time.Now().UTC().Format(time.RFC3339Nano),
		"terminalSessionId": s.ID, "payload": map[string]any{"cols": 200, "rows": 50},
	}))
	msg := d.readJSON()
	p := payload(t, msg)
	if msg["type"] != "TERMINAL_DATA_RESIZE" || p["cols"] != float64(200) || p["rows"] != float64(50) ||
		msg["terminalSessionId"] != s.ID || msg["labInstanceId"] != s.LabID || msg["generation"] != float64(s.Generation) {
		t.Fatalf("Connector 메시지 = %v", msg)
	}
	if _, ok := msg["messageId"].(string); !ok || msg["messageId"] == "resize-1" {
		t.Fatalf("Relay가 새 messageId를 만들어야 함: %v", msg["messageId"])
	}
}

// attach 뒤의 protocol 위반은 그 Browser connection만 끝낸다. PTY와 TerminalSession은 유지하고 grace가 시작된다.
func TestBrowserProtocolViolationAfterAttachOnlyDetaches(t *testing.T) {
	resize := func(t *testing.T, s session, fields, payload map[string]any) []byte {
		msg := map[string]any{
			"type": "TERMINAL_RESIZE", "messageId": "m", "sentAt": time.Now().UTC().Format(time.RFC3339Nano),
			"terminalSessionId": s.ID, "payload": map[string]any{"cols": 80, "rows": 24},
		}
		for k, v := range fields {
			if v == nil {
				delete(msg, k)
			} else {
				msg[k] = v
			}
		}
		for k, v := range payload {
			msg["payload"].(map[string]any)[k] = v
		}
		return marshal(t, msg)
	}
	tests := []struct {
		name  string
		write func(t *testing.T, s session, p *peer)
	}{
		{name: "second attach", write: func(t *testing.T, s session, p *peer) { p.writeText(browserAttachMessage(t, s, nil, nil)) }},
		{name: "unknown type", write: func(t *testing.T, s session, p *peer) {
			p.writeText([]byte(`{"type":"TERMINAL_FLUSH","messageId":"m","sentAt":"2026-10-01T09:00:00Z","terminalSessionId":"x","payload":{}}`))
		}},
		{name: "server-to-browser type", write: func(t *testing.T, s session, p *peer) {
			p.writeText([]byte(`{"type":"TERMINAL_SESSION_ENDED","messageId":"m","sentAt":"2026-10-01T09:00:00Z","terminalSessionId":"x","payload":{"reason":"PTY_EXITED"}}`))
		}},
		{name: "resize for another session", write: func(t *testing.T, s session, p *peer) {
			p.writeText(resize(t, s, map[string]any{"terminalSessionId": "00000000-0000-4000-8000-00000000ffff"}, nil))
		}},
		{name: "resize without session id", write: func(t *testing.T, s session, p *peer) {
			p.writeText(resize(t, s, map[string]any{"terminalSessionId": nil}, nil))
		}},
		{name: "resize with unknown payload property", write: func(t *testing.T, s session, p *peer) {
			p.writeText(resize(t, s, nil, map[string]any{"extra": true}))
		}},
		{name: "resize cols zero", write: func(t *testing.T, s session, p *peer) { p.writeText(resize(t, s, nil, map[string]any{"cols": 0})) }},
		{name: "resize rows as string", write: func(t *testing.T, s session, p *peer) { p.writeText(resize(t, s, nil, map[string]any{"rows": "24"})) }},
		{name: "not json", write: func(t *testing.T, s session, p *peer) { p.writeText([]byte("{")) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			s, d := e.liveSession()
			b := e.connectBrowser(s)
			d.readJSON()

			tt.write(t, s, b.peer)
			msg := b.readJSON()
			if msg["type"] != "ERROR" || payload(t, msg)["code"] != "PROTOCOL_ERROR" || payload(t, msg)["fatal"] != true {
				t.Fatalf("응답 = %v, want fatal ERROR PROTOCOL_ERROR", msg)
			}
			if code := b.expectClose(); code != websocket.ClosePolicyViolation {
				t.Fatalf("close code = %d, want 1008", code)
			}

			eventually(t, "DETACHED 기록", func() bool { _, detached, _, _ := e.control.snapshot(); return len(detached) == 1 })
			_, _, closed, ended := e.control.snapshot()
			if len(closed)+len(ended) != 0 || !e.relay.DataBound(s.ID) || e.relay.Sessions() != 1 {
				t.Fatalf("protocol 위반이 TerminalSession을 끝냄: closed %v ended %v", closed, ended)
			}
			d.expectNoMessage(100 * time.Millisecond) // 잘못된 resize가 Connector로 새지 않는다.
		})
	}
}

func TestBrowserOversizedJSONAfterAttachOnlyDetaches(t *testing.T) {
	e := newEnv(t, func(o *realtime.Options) { o.MaxBinaryMessage = 4 << 20 })
	s, d := e.liveSession()
	b := e.connectBrowser(s)
	d.readJSON()

	b.writeText(marshal(t, map[string]any{
		"type": "TERMINAL_RESIZE", "messageId": "m", "sentAt": time.Now().UTC().Format(time.RFC3339Nano),
		"terminalSessionId": s.ID, "payload": map[string]any{"cols": 1, "rows": 1, "padding": strings.Repeat("x", 1<<20)},
	}))
	if code := b.expectClose(); code != websocket.CloseMessageTooBig {
		t.Fatalf("close code = %d, want 1009", code)
	}
	eventually(t, "DETACHED 기록", func() bool { _, detached, _, _ := e.control.snapshot(); return len(detached) == 1 })
	if !e.relay.DataBound(s.ID) {
		t.Fatal("초과한 JSON이 data channel을 끊음")
	}
}

// attach가 성공하지 못한 connection은 session의 attachment가 아니므로 끊겨도 grace나 종료 기록을 만들지 않는다.
func TestUnattachedBrowserDisconnectDoesNotDetach(t *testing.T) {
	e := newEnv(t)
	s, _ := e.liveSession()
	e.control.authorizeErr = realtime.ErrForbidden

	p, _, err := e.dialBrowser(s.OwnerCookie, trustedOrigin)
	if err != nil {
		t.Fatalf("dial error = %v", err)
	}
	p.writeText(browserAttachMessage(t, s, nil, nil))
	p.expectClose()
	time.Sleep(50 * time.Millisecond)

	_, detached, closed, _ := e.control.snapshot()
	if len(detached) != 0 || len(closed) != 0 {
		t.Fatalf("거절된 attach가 detach/종료를 만듦: detached %v closed %v", detached, closed)
	}
}
