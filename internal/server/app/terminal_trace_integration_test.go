//go:build integration

package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal"
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal/terminaltest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/tracecontext"
)

// contracts/connector/README.md §9(D-25)와 contracts/realtime/README.md: Terminal JSON control event의 유효한 W3C Trace Context는 보존·전파하고
// 유효하지 않은 값은 그 관측 metadata만 버린다. 실제 PostgreSQL, Control WSS, Terminal Data WSS, Browser WSS를 모두 거쳐 확인한다.
// PTY Binary frame에는 Trace를 붙이지 않는다(realtime package의 test가 byte 단위로 확인한다).

const (
	traceSampled   = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	traceUnsampled = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-00"
	traceStateOK   = "vendor=opaque,other=value"
	traceIDSampled = "4bf92f3577b34da6a3ce929d0e0e4736"

	invalidTraceMarker = "INVALID-TRACE-MARKER-c41e"
)

// traceAttachFrame은 TERMINAL_ATTACH이며 message 최상위에 trace 원문을 그대로 싣는다.
func traceAttachFrame(t *testing.T, sessionID, token string, trace map[string]any) []byte {
	t.Helper()
	msg := map[string]any{
		"type": "TERMINAL_ATTACH", "messageId": "attach-" + uuid.NewString(), "sentAt": time.Now().UTC().Format(time.RFC3339Nano),
		"terminalSessionId": sessionID, "payload": map[string]any{"sessionToken": token, "cols": 132, "rows": 43},
	}
	for k, v := range trace {
		msg[k] = v
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func traceResizeFrame(t *testing.T, sessionID string, cols, rows int, trace map[string]any) []byte {
	t.Helper()
	msg := map[string]any{
		"type": "TERMINAL_RESIZE", "messageId": "resize-" + uuid.NewString(), "sentAt": time.Now().UTC().Format(time.RFC3339Nano),
		"terminalSessionId": sessionID, "payload": map[string]any{"cols": cols, "rows": rows},
	}
	for k, v := range trace {
		msg[k] = v
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func requireMessageTrace(t *testing.T, what string, msg map[string]any, wantParent, wantState string) {
	t.Helper()
	gotParent, _ := msg["traceparent"].(string)
	gotState, _ := msg["tracestate"].(string)
	if gotParent != wantParent || gotState != wantState {
		t.Fatalf("%s의 Trace = {%q %q}, want {%q %q}: %v", what, gotParent, gotState, wantParent, wantState, msg)
	}
	if _, present := msg["traceparent"]; present != (wantParent != "") {
		t.Fatalf("%s의 traceparent field 존재 = %v, want %v", what, present, wantParent != "")
	}
}

func (e *terminalEnv) waitResizes(sessionID string, n int) []terminaltest.Resize {
	e.t.Helper()
	var got []terminaltest.Resize
	eventually(e.t, "TERMINAL_DATA_RESIZE 수신", 5*time.Second, func() bool {
		got = e.connector.Resizes(sessionID)
		return len(got) >= n
	})
	return got
}

// 유효한 Trace Context는 Browser → Relay → Connector, 그리고 Connector의 Control 종료 통지 → Relay → Browser로 이어진다.
// Context는 control event별이다. attach의 Trace를 이후 resize에 재사용하지 않는다. DB에는 Trace가 저장되지 않는다.
func TestTerminalControlEventsCarryValidTraceContextThroughTheRealStack(t *testing.T) {
	e := newTerminalEnv(t)
	f := e.fixture
	s := e.createSession(e.ownerCookie, f.LabInstanceID)

	// SaaS는 받은 적 없는 Trace를 만들어 내지 않는다. 생성 요청에는 Trace가 없었으므로 TERMINAL_OPEN에도 없다.
	if raw := e.connector.Opens()[0].Raw; strings.Contains(raw, "traceparent") || strings.Contains(raw, "tracestate") {
		t.Fatalf("Trace가 없는 요청인데 TERMINAL_OPEN에 Trace가 있음: %s", raw)
	}

	b, _, err := e.dialBrowser(s.cookie, terminalTrustedOrigin)
	if err != nil {
		t.Fatalf("Browser WSS 연결 실패: %v", err)
	}
	b.write(websocket.TextMessage, traceAttachFrame(t, s.ID, s.Token, map[string]any{"traceparent": traceSampled, "tracestate": traceStateOK}))
	attached := b.json()
	if attached["type"] != "TERMINAL_ATTACHED" {
		t.Fatalf("attach 응답 = %v", attached)
	}
	requireMessageTrace(t, "TERMINAL_ATTACHED", attached, traceSampled, traceStateOK)

	// attach가 PTY에 반영하는 크기, 그리고 이후 resize는 각각 자신의 Trace를 Connector에 전달한다.
	resizes := e.waitResizes(s.ID, 1)
	if want := (terminaltest.Resize{Cols: "132", Rows: "43", Traceparent: traceSampled, Tracestate: traceStateOK}); resizes[0] != want {
		t.Fatalf("attach의 TERMINAL_DATA_RESIZE = %+v, want %+v", resizes[0], want)
	}
	b.write(websocket.TextMessage, traceResizeFrame(t, s.ID, 200, 50, map[string]any{"traceparent": traceUnsampled}))
	b.write(websocket.TextMessage, traceResizeFrame(t, s.ID, 210, 51, nil)) // Trace 없음. attach의 Trace를 재사용하면 안 된다.
	resizes = e.waitResizes(s.ID, 3)
	if want := (terminaltest.Resize{Cols: "200", Rows: "50", Traceparent: traceUnsampled}); resizes[1] != want {
		t.Fatalf("두 번째 TERMINAL_DATA_RESIZE = %+v, want %+v(미샘플링 그대로)", resizes[1], want)
	}
	if want := (terminaltest.Resize{Cols: "210", Rows: "51"}); resizes[2] != want {
		t.Fatalf("Trace 없는 resize의 TERMINAL_DATA_RESIZE = %+v, want %+v(attach의 Trace를 재사용하면 안 됨)", resizes[2], want)
	}

	// Connector가 Control WSS로 알린 종료의 Trace는 Browser의 종료 통지까지 이어진다.
	e.connector.EndControlWithTrace(s.ID, f.LabInstanceID.String(), 1, "PTY_EXITED", map[string]any{"traceparent": traceUnsampled, "tracestate": traceStateOK})
	ended := b.json()
	if ended["type"] != "TERMINAL_SESSION_ENDED" || ended["payload"].(map[string]any)["reason"] != "PTY_EXITED" {
		t.Fatalf("종료 통지 = %v", ended)
	}
	requireMessageTrace(t, "TERMINAL_SESSION_ENDED", ended, traceUnsampled, traceStateOK)
	e.waitStatus(s.ID, "ENDED")
	requireLogFields(t, e.logEvent("Browser Terminal attach", s.ID), map[string]any{"trace_id": traceIDSampled})
	for _, message := range []string{"TerminalSession 종료 수신", "TerminalSession Relay 정리"} {
		requireLogFields(t, e.logEvent(message, s.ID), map[string]any{
			"trace_id": "0af7651916cd43dd8448eb211c80319c", "terminal_session_id": s.ID,
			"lab_instance_id": f.LabInstanceID.String(), "connector_id": f.ConnectorID.String(),
		})
	}
	for _, raw := range []string{traceSampled, traceUnsampled, traceStateOK} {
		if strings.Contains(e.logs.String(), raw) {
			t.Fatal("log contains raw Trace metadata")
		}
	}

	// Trace는 관측용 전파 metadata다. DB에 저장하지 않는다.
	for _, needle := range []string{traceIDSampled, "0af7651916cd43dd8448eb211c80319c", traceStateOK, "tracestate"} {
		if table, found := e.databaseContains(needle); found {
			t.Fatalf("DB(%s)에 Trace 값 %q가 저장됨", table, needle)
		}
	}
}

// 유효하지 않은 Trace는 control event를 실패시키지 않는다. 그 field만 버리고 가짜 Trace를 만들지 않으며 원문을 log에 복사하지 않는다.
func TestTerminalControlEventsWithInvalidTraceContextStillSucceedThroughTheRealStack(t *testing.T) {
	e := newTerminalEnv(t)
	f := e.fixture
	s := e.createSession(e.ownerCookie, f.LabInstanceID)
	invalid := map[string]any{"traceparent": invalidTraceMarker, "tracestate": traceStateOK}

	b, _, err := e.dialBrowser(s.cookie, terminalTrustedOrigin)
	if err != nil {
		t.Fatalf("Browser WSS 연결 실패: %v", err)
	}
	b.write(websocket.TextMessage, traceAttachFrame(t, s.ID, s.Token, invalid))
	attached := b.json()
	if attached["type"] != "TERMINAL_ATTACHED" {
		t.Fatalf("잘못된 Trace 때문에 attach가 실패함: %v", attached)
	}
	requireMessageTrace(t, "TERMINAL_ATTACHED", attached, "", "")
	e.waitStatus(s.ID, "ACTIVE")

	b.write(websocket.TextMessage, traceResizeFrame(t, s.ID, 120, 30, map[string]any{"traceparent": 12345}))
	resizes := e.waitResizes(s.ID, 2)
	for i, r := range resizes {
		if r.Traceparent != "" || r.Tracestate != "" {
			t.Fatalf("TERMINAL_DATA_RESIZE[%d]가 잘못된 Trace를 전달함: %+v", i, r)
		}
	}
	if want := (terminaltest.Resize{Cols: "120", Rows: "30"}); resizes[1] != want {
		t.Fatalf("resize 값 = %+v, want %+v(Trace 때문에 바뀌면 안 됨)", resizes[1], want)
	}

	e.connector.EndControlWithTrace(s.ID, f.LabInstanceID.String(), 1, "PTY_EXITED", invalid)
	ended := b.json()
	if ended["type"] != "TERMINAL_SESSION_ENDED" {
		t.Fatalf("종료 통지 = %v", ended)
	}
	requireMessageTrace(t, "TERMINAL_SESSION_ENDED", ended, "", "")
	row := e.waitStatus(s.ID, "ENDED")
	if row.EndReason == nil || *row.EndReason != "PTY_EXITED" {
		t.Fatalf("종료 사유 = %v, want PTY_EXITED(Trace 때문에 바뀌면 안 됨)", row.EndReason)
	}

	// 잘못된 원문은 log에 없고, 유효한 Trace가 없으므로 trace_id도 없다.
	logs := e.logs.String()
	if strings.Contains(logs, invalidTraceMarker) {
		t.Fatal("log에 잘못된 Trace 원문이 남음")
	}
	if strings.Contains(logs, `"trace_id"`) {
		t.Fatalf("유효한 Trace가 없는데 trace_id가 log에 남음:\n%s", logs)
	}
	for _, event := range applicationLogEvents(t, logs) {
		requireLogFields(t, event, map[string]any{"trace_id": nil})
	}
}

// Trace 때문이 아닌 attach 거절은 그대로 거절된다. Trace가 유효하다고 권한 판정이 달라지지 않는다.
func TestValidTraceContextDoesNotChangeAttachAuthorization(t *testing.T) {
	e := newTerminalEnv(t)
	s := e.createSession(e.ownerCookie, e.fixture.LabInstanceID)

	for name, cookie := range map[string]string{"다른 사용자": e.peerCookie, "다른 Class 사용자": e.outsiderCookie} {
		b, _, err := e.dialBrowser(cookie, terminalTrustedOrigin)
		if err != nil {
			t.Fatalf("%s: Browser WSS 연결 실패: %v", name, err)
		}
		b.write(websocket.TextMessage, traceAttachFrame(t, s.ID, s.Token, map[string]any{"traceparent": traceSampled}))
		msg := b.json()
		if msg["type"] != "ERROR" {
			t.Fatalf("%s: 응답 = %v, want ERROR(유효한 Trace가 권한을 대신하지 않는다)", name, msg)
		}
		// 거절 응답은 요청의 Trace를 돌려준다.
		requireMessageTrace(t, name+"의 ERROR", msg, traceSampled, "")
		if code, _ := b.closeCode(); code == 0 {
			t.Fatalf("%s: close code 없음", name)
		}
	}
	if row := e.row(s.ID); row.Status == "ACTIVE" || row.AttachedAt != nil {
		t.Fatalf("거절된 attach가 세션을 ACTIVE로 만듦: %+v", row)
	}
}

// Labbit이 종료를 일으킬 때(사용자의 명시적 종료, Reset/Cleanup)는 호출 ctx의 유효한 Trace를 Browser의 종료 통지에 이어 준다.
// ctx에 Trace가 없으면 가짜 Trace를 만들지 않는다.
func TestLabbitInitiatedEndCarriesTheCallersTraceContextToTheBrowser(t *testing.T) {
	e := newTerminalEnv(t)
	f := e.fixture
	owner := repository.User{ID: f.OwnerID, OrganizationID: f.OrganizationID}
	withTrace := tracecontext.NewContext(context.Background(), tracecontext.Context{Traceparent: traceUnsampled, Tracestate: traceStateOK})

	// 사용자의 명시적 종료
	closed := e.createSession(e.ownerCookie, f.LabInstanceID)
	b, _ := e.attach(closed)
	if err := e.stack.Terminals.Close(withTrace, owner, closed.ID); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	ended := b.json()
	if ended["type"] != "TERMINAL_SESSION_ENDED" || ended["payload"].(map[string]any)["reason"] != "SESSION_CLOSED" {
		t.Fatalf("종료 통지 = %v", ended)
	}
	requireMessageTrace(t, "명시적 종료의 TERMINAL_SESSION_ENDED", ended, traceUnsampled, traceStateOK)
	for _, message := range []string{"TerminalSession 종료", "Connector TERMINAL_CLOSE 전송", "TerminalSession Relay 정리"} {
		requireLogFields(t, e.logEvent(message, closed.ID), map[string]any{
			"trace_id": "0af7651916cd43dd8448eb211c80319c", "request_id": nil,
		})
	}

	// Reset/Cleanup 같은 Lab mutation
	mutated := e.createSession(e.ownerCookie, f.LabInstanceID)
	b2, _ := e.attach(mutated)
	if err := e.stack.Terminals.CloseForLabMutation(withTrace, terminal.LabMutation{LabInstanceID: f.LabInstanceID, Reason: "LAB_RESET"}); err != nil {
		t.Fatalf("CloseForLabMutation() error = %v", err)
	}
	ended = b2.json()
	if ended["type"] != "TERMINAL_SESSION_ENDED" || ended["payload"].(map[string]any)["reason"] != "LAB_RESET" {
		t.Fatalf("종료 통지 = %v", ended)
	}
	requireMessageTrace(t, "Lab mutation의 TERMINAL_SESSION_ENDED", ended, traceUnsampled, traceStateOK)

	// ctx에 Trace가 없으면 Trace가 없다.
	plain := e.createSession(e.ownerCookie, f.LabInstanceID)
	b3, _ := e.attach(plain)
	if err := e.stack.Terminals.Close(context.Background(), owner, plain.ID); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	requireMessageTrace(t, "Trace 없는 종료의 TERMINAL_SESSION_ENDED", b3.json(), "", "")
	for _, message := range []string{"TerminalSession 종료", "Connector TERMINAL_CLOSE 전송", "TerminalSession Relay 정리"} {
		requireLogFields(t, e.logEvent(message, plain.ID), map[string]any{"trace_id": nil, "request_id": nil})
	}
}
