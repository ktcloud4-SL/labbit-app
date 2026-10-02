//go:build integration

package app

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal/terminaltest"
)

var base64URLToken = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// 이 PR이 검증하는 전체 sequence다(contract peer 기준).
//
//	POST TerminalSession → TERMINAL_OPEN → Terminal Data WSS Upgrade → TERMINAL_DATA_ATTACH → TERMINAL_DATA_ATTACHED
//	→ TERMINAL_OPEN_RESULT SUCCEEDED → HTTP 201 → Browser WSS → TERMINAL_ATTACH → TERMINAL_ATTACHED → binary I/O / resize
//	→ Browser disconnect → DETACHED / 60s grace → reconnect(같은 session, 같은 data channel, resumed=true, history 없음)
//	→ explicit DELETE → TERMINAL_CLOSE → TERMINAL_ENDED
func TestTerminalSessionEndToEnd(t *testing.T) {
	e := newTerminalEnv(t)
	f := e.fixture

	// 1. HTTP로 TerminalSession을 만든다. Connector가 TERMINAL_OPEN을 받고 Data WSS를 붙인 뒤에야 201이 돌아온다.
	resp := e.request(http.MethodPost, e.createPath(f.LabInstanceID), e.ownerCookie, createBody)
	if resp.Status != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", resp.Status, resp.Body)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	body := resp.json(t)
	s := created{
		ID: body["id"].(string), Token: body["sessionToken"].(string), TokenExpiresAt: body["tokenExpiresAt"].(string),
		Generation: int64(body["generation"].(float64)), cookie: e.ownerCookie,
	}
	if len(body) != 4 || s.Generation != 1 || !base64URLToken.MatchString(s.Token) {
		t.Fatalf("응답 = %s, want exactly id/generation/sessionToken/tokenExpiresAt", resp.Body)
	}
	if want := terminalStart.Add(8 * time.Hour).Format(time.RFC3339); s.TokenExpiresAt != want {
		t.Fatalf("tokenExpiresAt = %s, want %s (발급 + 8시간)", s.TokenExpiresAt, want)
	}

	// 2. Connector가 받은 TERMINAL_OPEN은 서버가 현재 DB 상태에서 결정한 resolved target이다.
	opens := e.connector.Opens()
	if len(opens) != 1 {
		t.Fatalf("TERMINAL_OPEN = %d, want 1", len(opens))
	}
	open := opens[0]
	workspace := f.Servers["workspace"]
	if open.TerminalSessionID != s.ID || open.LabInstanceID != f.LabInstanceID.String() || open.Generation != 1 ||
		open.TargetVMKey != "workspace" || open.ProviderServerID != workspace.ProviderID || open.Cols != "120" || open.Rows != "40" {
		t.Fatalf("TERMINAL_OPEN = %+v", open)
	}
	// Browser Cookie, Password, attach token은 Connector로 가지 않는다.
	for _, secret := range []string{s.Token, e.ownerCookie, "password", "Cookie"} {
		if strings.Contains(open.Raw, secret) {
			t.Fatalf("TERMINAL_OPEN이 %q를 포함함: %s", secret, open.Raw)
		}
	}
	// Data WSS attach는 1번이고 OPEN_RESULT 전에 이미 bind되었다. 생성 직후 세션은 attach를 기다리는 DETACHED + 60초 grace다.
	attached := e.connector.Attached(s.ID)
	if len(attached) != 1 || attached[0]["resumed"] != false || attached[0]["historyAvailable"] != false {
		t.Fatalf("TERMINAL_DATA_ATTACHED = %v, want one resumed=false historyAvailable=false", attached)
	}
	row := e.row(s.ID)
	if row.Status != "DETACHED" || row.GraceExpiresAt == nil || !row.GraceExpiresAt.Equal(terminalStart.Add(60*time.Second)) || row.AttachedAt != nil || row.Generation != 1 {
		t.Fatalf("생성 직후 row = %+v", row)
	}

	// 3. Browser attach. Cookie + Origin + attach token을 모두 다시 확인한다.
	b, attachedMsg := e.attach(s)
	payload := attachedMsg["payload"].(map[string]any)
	if payload["resumed"] != false || payload["historyAvailable"] != false || attachedMsg["terminalSessionId"] != s.ID {
		t.Fatalf("TERMINAL_ATTACHED = %v", attachedMsg)
	}
	row = e.waitStatus(s.ID, "ACTIVE")
	if row.GraceExpiresAt != nil || row.DetachedAt != nil || row.AttachedAt == nil {
		t.Fatalf("attach 뒤 row = %+v", row)
	}
	// Browser의 현재 terminal 크기가 PTY에 반영된다.
	eventually(t, "attach 시 resize", 5*time.Second, func() bool {
		r := e.connector.Resizes(s.ID)
		return len(r) == 1 && r[0] == terminaltest.Resize{Cols: "132", Rows: "43"}
	})

	// 4. Binary INPUT/OUTPUT은 raw bytes로 오간다. resize는 TERMINAL_DATA_RESIZE로 전달된다.
	input := []byte(terminalInputMarker + "\x1b[A\xff\x00")
	b.write(websocket.BinaryMessage, input)
	eventually(t, "PTY INPUT", 5*time.Second, func() bool { return string(e.connector.Inputs(s.ID)) == string(input) })
	output := []byte(terminalOutputMarker + "\x1b[31m\xfe")
	e.connector.Output(s.ID, output)
	if got := b.binary(); string(got) != string(output) {
		t.Fatalf("OUTPUT = %q, want %q", got, output)
	}
	b.write(websocket.TextMessage, []byte(`{"type":"TERMINAL_RESIZE","messageId":"r1","sentAt":"2026-10-01T09:00:00Z","terminalSessionId":"`+s.ID+`","payload":{"cols":200,"rows":50}}`))
	eventually(t, "TERMINAL_DATA_RESIZE", 5*time.Second, func() bool {
		r := e.connector.Resizes(s.ID)
		return len(r) == 2 && r[1] == terminaltest.Resize{Cols: "200", Rows: "50"}
	})

	// 5. Browser가 비정상 단절되면 DETACHED가 되고 60초 grace가 시작된다. PTY와 data channel은 유지한다.
	b.close()
	row = e.waitStatus(s.ID, "DETACHED")
	if row.GraceExpiresAt == nil || !row.GraceExpiresAt.Equal(terminalStart.Add(60*time.Second)) || row.DetachedAt == nil || row.EndedAt != nil {
		t.Fatalf("단절 뒤 row = %+v", row)
	}
	e.connector.Output(s.ID, []byte("while-detached"))
	time.Sleep(300 * time.Millisecond) // Relay가 그 frame을 받아 버릴 시간. 재접속 전에 처리되어야 "재생하지 않음"을 확인할 수 있다.
	e.clock.Advance(59 * time.Second)  // 실제 60초를 기다리지 않는다.

	// 6. 같은 session, 같은 data channel로 재접속한다. 단절 중의 OUTPUT은 재생하지 않는다.
	b2, reattached := e.attach(s)
	payload = reattached["payload"].(map[string]any)
	if payload["resumed"] != true || payload["historyAvailable"] != false {
		t.Fatalf("재접속 payload = %v, want resumed=true historyAvailable=false", payload)
	}
	if got := e.connector.DataDials(); got != 1 {
		t.Fatalf("Terminal Data WSS를 연 횟수 = %d, want 1 (재접속이 새 data channel을 만들면 안 됨)", got)
	}
	if opens := e.connector.Opens(); len(opens) != 1 {
		t.Fatalf("재접속이 TERMINAL_OPEN을 다시 보냄: %d", len(opens))
	}
	e.waitStatus(s.ID, "ACTIVE")
	e.connector.Output(s.ID, []byte("after-reattach"))
	if got := b2.binary(); string(got) != "after-reattach" {
		t.Fatalf("재접속 뒤 첫 OUTPUT = %q, want only output after reattach", got)
	}

	// 7. 명시적 DELETE: 즉시 ENDED. Connector에 TERMINAL_CLOSE(Control)와 TERMINAL_DATA_CLOSE(Data)를 보내고 Browser에 종료를 알린다.
	del := e.request(http.MethodDelete, "/api/v1/terminal-sessions/"+s.ID, e.ownerCookie, "")
	if del.Status != http.StatusNoContent || len(del.Body) != 0 {
		t.Fatalf("DELETE = %d %q, want 204", del.Status, del.Body)
	}
	row = e.waitStatus(s.ID, "ENDED")
	if row.EndReason == nil || *row.EndReason != "SESSION_CLOSED" || row.EndedAt == nil || row.GraceExpiresAt != nil {
		t.Fatalf("종료 뒤 row = %+v", row)
	}
	code, collected := b2.closeCode()
	if code != 1000 || len(collected) != 1 {
		t.Fatalf("Browser 종료 = close %d, %d messages, want TERMINAL_SESSION_ENDED then 1000", code, len(collected))
	}
	if !strings.Contains(string(collected[0].data), `"TERMINAL_SESSION_ENDED"`) || !strings.Contains(string(collected[0].data), `"SESSION_CLOSED"`) {
		t.Fatalf("Browser message = %s", collected[0].data)
	}
	eventually(t, "TERMINAL_CLOSE", 5*time.Second, func() bool { return len(e.connector.Closes()) == 1 })
	closeMsg := e.connector.Closes()[0]
	if closeMsg.TerminalSessionID != s.ID || closeMsg.LabInstanceID != f.LabInstanceID.String() || closeMsg.Generation != 1 || closeMsg.Reason != "SESSION_CLOSED" {
		t.Fatalf("TERMINAL_CLOSE = %+v", closeMsg)
	}
	eventually(t, "TERMINAL_DATA_CLOSE", 5*time.Second, func() bool { return len(e.connector.DataCloses(s.ID)) == 1 })

	// Connector의 늦은 TERMINAL_ENDED(CLOSE에 대한 응답)는 이미 종료된 session에 아무 영향이 없다(멱등).
	time.Sleep(200 * time.Millisecond)
	if row := e.row(s.ID); row.Status != "ENDED" || *row.EndReason != "SESSION_CLOSED" {
		t.Fatalf("늦은 TERMINAL_ENDED가 종료 기록을 바꿈: %+v", row)
	}
	if e.stack.Relay.Sessions() != 0 {
		t.Fatalf("Relay Sessions = %d, want 0", e.stack.Relay.Sessions())
	}
	// 같은 DELETE를 다시 보내도 204다(멱등).
	if again := e.request(http.MethodDelete, "/api/v1/terminal-sessions/"+s.ID, e.ownerCookie, ""); again.Status != http.StatusNoContent {
		t.Fatalf("반복 DELETE = %d, want 204", again.Status)
	}
	// 종료된 session의 token은 만료 전이어도 즉시 사용할 수 없다.
	code2, close2 := e.attachRejected(e.ownerCookie, s.ID, s.Token)
	if code2 != "SESSION_EXPIRED" || close2 != 4003 {
		t.Fatalf("종료된 session attach = %s %d, want SESSION_EXPIRED 4003", code2, close2)
	}

	// Production JSON logger를 쓰는 실제 HTTP/WSS 조립에서 각 event의 correlation을 확인한다.
	requireLogFields(t, e.logEvent("Connector Control connection 수립", ""), map[string]any{
		"connector_id": f.ConnectorID.String(), "trace_id": nil,
	})
	for _, message := range []string{"TerminalSession 생성", "Connector TERMINAL_OPEN 전송", "Connector Terminal Data attach", "Browser Terminal attach", "TerminalSession 종료", "Connector TERMINAL_CLOSE 전송"} {
		event := e.logEvent(message, s.ID)
		requireLogFields(t, event, map[string]any{
			"terminal_session_id": s.ID, "lab_instance_id": f.LabInstanceID.String(),
			"connector_id": f.ConnectorID.String(), "trace_id": nil, "operation_id": nil,
		})
	}
	for _, message := range []string{"TerminalSession 생성", "Connector TERMINAL_OPEN 전송"} {
		requireLogFields(t, e.logEvent(message, s.ID), map[string]any{"request_id": open.RequestID})
	}
	closeEvent := e.logEvent("TerminalSession 종료", s.ID)
	closeRequestID, _ := closeEvent["request_id"].(string)
	if closeRequestID == "" || closeRequestID == open.RequestID {
		t.Fatal("DELETE log must carry its own request_id")
	}
	requireLogFields(t, e.logEvent("Connector TERMINAL_CLOSE 전송", s.ID), map[string]any{"request_id": closeRequestID})
}
