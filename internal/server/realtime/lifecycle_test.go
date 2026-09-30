package realtime_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
)

const grace = realtime.DefaultGrace

// 같은 TerminalSession에 새 Browser가 attach하면 새 연결이 current가 되고 이전 Browser 연결만 4004로 종료된다.
// 새 PTY나 data channel을 만들지 않으며 교체만으로는 detach도 종료도 기록하지 않는다.
func TestSecondBrowserAttachReplacesOnlyTheFirst(t *testing.T) {
	e := newEnv(t)
	s, d := e.liveSession()
	b1 := e.connectBrowser(s)
	d.readJSON()
	b2 := e.connectBrowser(s)
	d.readJSON()

	if code := b1.expectClose(); code != 4004 {
		t.Fatalf("이전 Browser close code = %d, want 4004", code)
	}
	if p := payload(t, b2.attached); p["resumed"] != true || p["historyAvailable"] != false {
		t.Fatalf("두 번째 attach payload = %v, want resumed=true historyAvailable=false", p)
	}

	// 같은 data channel이 새 Browser와 이어진다. 이전 Browser에는 더 이상 OUTPUT이 가지 않는다.
	b2.writeBinary([]byte("to-connector"))
	if got := d.readBinary(); string(got) != "to-connector" {
		t.Fatalf("INPUT = %q", got)
	}
	d.writeBinary([]byte("to-browser"))
	if got := b2.readBinary(); string(got) != "to-browser" {
		t.Fatalf("OUTPUT = %q", got)
	}

	time.Sleep(100 * time.Millisecond) // 교체된 연결의 read loop가 정리될 시간
	attached, detached, closed, ended := e.control.snapshot()
	if len(attached) != 2 || len(detached) != 0 || len(closed) != 0 || len(ended) != 0 {
		t.Fatalf("Control 호출 = attached %v detached %v closed %v ended %v", attached, detached, closed, ended)
	}
	if !e.relay.DataBound(s.ID) || e.relay.Sessions() != 1 || e.clock.Pending() != 0 {
		t.Fatalf("교체가 상태를 바꿈: bound %v sessions %d timers %d", e.relay.DataBound(s.ID), e.relay.Sessions(), e.clock.Pending())
	}
}

// Browser가 비정상 단절되면 TerminalSession은 DETACHED가 되어 60초 grace가 시작되고 PTY와 data channel은 유지된다.
// grace 안의 재접속은 같은 TerminalSession/같은 data channel이며 resumed=true, history 없음이다.
func TestBrowserDisconnectStartsGraceAndReattachResumes(t *testing.T) {
	e := newEnv(t)
	s, d := e.liveSession()
	b1 := e.connectBrowser(s)
	d.readJSON()
	if n := e.clock.Pending(); n != 0 {
		t.Fatalf("attach 중에 grace timer가 있음: %d", n)
	}

	b1.close() // 네트워크 단절처럼 close frame 없이 끊는다.
	eventually(t, "DETACHED 기록", func() bool { _, detached, _, _ := e.control.snapshot(); return len(detached) == 1 })
	_, detached, closed, _ := e.control.snapshot()
	if detached[0].ID != s.ID || !detached[0].At.Equal(startTime) || !detached[0].GraceUntil.Equal(startTime.Add(grace)) {
		t.Fatalf("detach 기록 = %+v, want id %s at %v grace until %v", detached[0], s.ID, startTime, startTime.Add(grace))
	}
	if len(closed) != 0 || !e.relay.DataBound(s.ID) || e.clock.Pending() != 1 {
		t.Fatalf("단절이 PTY/data channel을 끊음: closed %v bound %v timers %d", closed, e.relay.DataBound(s.ID), e.clock.Pending())
	}

	// 단절된 동안의 OUTPUT은 저장하거나 재생하지 않는다.
	d.writeBinary([]byte(outputMarker))
	time.Sleep(300 * time.Millisecond) // Relay가 그 frame을 받아 버릴 시간

	e.clock.Advance(grace - time.Second)
	time.Sleep(100 * time.Millisecond)
	if _, _, closed, _ := e.control.snapshot(); len(closed) != 0 {
		t.Fatalf("grace 안에 종료됨: %v", closed)
	}

	b2 := e.connectBrowser(s)
	if p := payload(t, b2.attached); p["resumed"] != true || p["historyAvailable"] != false {
		t.Fatalf("재접속 payload = %v, want resumed=true historyAvailable=false", p)
	}
	if n := e.clock.Pending(); n != 0 {
		t.Fatalf("재접속 뒤에도 grace timer가 남음: %d", n)
	}
	b2.expectNoMessage(200 * time.Millisecond) // 단절 중의 OUTPUT을 재생하지 않는다.
	d.readJSON()                               // 재접속한 Browser의 크기

	// 같은 data channel로 계속 I/O한다.
	b2.writeBinary([]byte(inputMarker))
	if got := d.readBinary(); string(got) != inputMarker {
		t.Fatalf("INPUT = %q", got)
	}
	d.writeBinary([]byte("after-reattach"))
	if got := b2.readBinary(); string(got) != "after-reattach" {
		t.Fatalf("OUTPUT = %q, want only output after reattach", got)
	}

	// timer가 취소되었으므로 시간이 더 지나도 종료되지 않는다.
	e.clock.Advance(10 * time.Minute)
	time.Sleep(100 * time.Millisecond)
	if _, _, closed, _ := e.control.snapshot(); len(closed) != 0 || e.relay.Sessions() != 1 {
		t.Fatalf("재접속한 TerminalSession이 종료됨: closed %v sessions %d", closed, e.relay.Sessions())
	}
	if attached, _, _, _ := e.control.snapshot(); len(attached) != 2 {
		t.Fatalf("attach 기록 = %v, want 2", attached)
	}
}

// grace가 만료되면 Control에 종료(Connector CLOSE와 기록)를 요청하고 data channel에 TERMINAL_DATA_CLOSE를 보낸 뒤 닫는다.
// 만료된 TerminalSession에는 더 이상 attach할 수 없다.
func TestGraceExpiryEndsTheSession(t *testing.T) {
	e := newEnv(t)
	s, d := e.liveSession()
	b1 := e.connectBrowser(s)
	d.readJSON()
	b1.close()
	eventually(t, "DETACHED 기록", func() bool { _, detached, _, _ := e.control.snapshot(); return len(detached) == 1 })

	e.clock.Advance(grace - time.Second)
	time.Sleep(100 * time.Millisecond)
	if _, _, closed, _ := e.control.snapshot(); len(closed) != 0 {
		t.Fatalf("만료 1초 전에 종료됨: %v", closed)
	}
	e.clock.Advance(time.Second)

	eventually(t, "grace 만료 종료", func() bool { _, _, closed, _ := e.control.snapshot(); return len(closed) == 1 })
	_, _, closed, ended := e.control.snapshot()
	if closed[0].ID != s.ID || closed[0].End.Reason != "SESSION_EXPIRED" || len(ended) != 0 {
		t.Fatalf("종료 요청 = %+v ended %v", closed, ended)
	}

	msgs, code := d.collect()
	if code != websocket.CloseNormalClosure || len(msgs) != 1 {
		t.Fatalf("data channel = %d messages, close %d, want DATA_CLOSE then 1000", len(msgs), code)
	}
	cl := jsonOf(t, msgs[0])
	if cl["type"] != "TERMINAL_DATA_CLOSE" || payload(t, cl)["reason"] != "SESSION_EXPIRED" ||
		cl["terminalSessionId"] != s.ID || cl["labInstanceId"] != s.LabID || cl["generation"] != float64(s.Generation) {
		t.Fatalf("Connector 메시지 = %v", cl)
	}
	if e.relay.Sessions() != 0 || e.relay.DataBound(s.ID) {
		t.Fatalf("만료 뒤 상태가 남음: sessions %d bound %v", e.relay.Sessions(), e.relay.DataBound(s.ID))
	}

	// 만료 뒤의 attach는 같은 PTY에 붙을 수 없다.
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
}

// 첫 attach 전의 TerminalSession도 같은 grace 안에서만 attach를 기다린다. PTY를 무기한 남기지 않는다.
func TestSessionNeverAttachedExpiresAfterGrace(t *testing.T) {
	e := newEnv(t)
	s, d := e.liveSession()
	if n := e.clock.Pending(); n != 1 {
		t.Fatalf("Activate 뒤 grace timer = %d, want 1", n)
	}

	e.clock.Advance(grace - time.Second)
	time.Sleep(100 * time.Millisecond)
	if _, _, closed, _ := e.control.snapshot(); len(closed) != 0 {
		t.Fatalf("만료 1초 전에 종료됨: %v", closed)
	}
	e.clock.Advance(time.Second)
	eventually(t, "grace 만료 종료", func() bool { _, _, closed, _ := e.control.snapshot(); return len(closed) == 1 })
	if _, _, closed, _ := e.control.snapshot(); closed[0].ID != s.ID || closed[0].End.Reason != "SESSION_EXPIRED" {
		t.Fatalf("종료 요청 = %+v", closed)
	}
	if _, code := d.collect(); code != websocket.CloseNormalClosure {
		t.Fatalf("data channel close code = %d, want 1000", code)
	}
	if e.relay.Sessions() != 0 {
		t.Fatalf("Sessions = %d, want 0", e.relay.Sessions())
	}
}

// 이미 만료되어 실행을 기다리는 timer callback이 그 사이에 재attach한 TerminalSession을 종료해서는 안 된다.
// attach가 전이 잠금을 쥔 채 Control에서 멈춘 동안 grace timer를 만료시켜 이 경쟁을 결정적으로 만든다.
func TestExpiredTimerCallbackDoesNotEndAReattachedSession(t *testing.T) {
	e := newEnv(t)
	s, d := e.liveSession()
	b1 := e.connectBrowser(s)
	d.readJSON()
	b1.close()
	eventually(t, "DETACHED 기록", func() bool { _, detached, _, _ := e.control.snapshot(); return len(detached) == 1 })

	g := newGate()
	e.control.mu.Lock()
	e.control.attachGate = g
	e.control.mu.Unlock()
	p2, _, err := e.dialBrowser(s.OwnerCookie, trustedOrigin)
	if err != nil {
		t.Fatalf("dial error = %v", err)
	}
	p2.writeText(browserAttachMessage(t, s, nil, nil))
	<-g.entered // attach가 전이 잠금을 쥐고 RecordAttached에서 기다린다.

	e.clock.Advance(grace) // grace timer가 만료되어 callback이 전이 잠금을 기다린다.
	time.Sleep(150 * time.Millisecond)
	close(g.release)

	attached := p2.readJSON()
	if attached["type"] != "TERMINAL_ATTACHED" || payload(t, attached)["resumed"] != true {
		t.Fatalf("재attach 응답 = %v", attached)
	}
	d.readJSON()
	time.Sleep(150 * time.Millisecond)

	if _, _, closed, ended := e.control.snapshot(); len(closed) != 0 || len(ended) != 0 {
		t.Fatalf("만료된 timer callback이 재attach한 TerminalSession을 종료함: closed %v ended %v", closed, ended)
	}
	if e.relay.Sessions() != 1 || !e.relay.DataBound(s.ID) {
		t.Fatalf("상태가 끝남: sessions %d bound %v", e.relay.Sessions(), e.relay.DataBound(s.ID))
	}
	p2.writeBinary([]byte("still-alive"))
	if got := d.readBinary(); string(got) != "still-alive" {
		t.Fatalf("INPUT = %q", got)
	}
}

// Connector가 PTY/SSH 종료를 알리면 Control에 종료를 기록하고 Browser에 TERMINAL_SESSION_ENDED를 보낸다.
// 알 수 없는 exit code를 0으로 만들어 내지 않으며 Connector에 CLOSE를 다시 보내지 않는다.
func TestDataEndedEndsTheSessionAndNotifiesTheBrowser(t *testing.T) {
	zero := int64(0)
	seven := int64(7)
	tests := []struct {
		name       string
		payload    map[string]any
		wantReason string
		wantExit   *int64
	}{
		{name: "pty exited with code", payload: map[string]any{"reason": "PTY_EXITED", "exitCode": 7}, wantReason: "PTY_EXITED", wantExit: &seven},
		{name: "exit code zero is a known code", payload: map[string]any{"reason": "PTY_EXITED", "exitCode": 0}, wantReason: "PTY_EXITED", wantExit: &zero},
		{name: "exit code unknown", payload: map[string]any{"reason": "SSH_DISCONNECTED", "error": map[string]any{"code": "SSH_LOST"}}, wantReason: "SSH_DISCONNECTED"},
		{name: "exit code beyond int64 is unknown", payload: map[string]any{"reason": "PTY_EXITED", "exitCode": 1e30}, wantReason: "PTY_EXITED"},
		{name: "reason that is not a machine constant", payload: map[string]any{"reason": "shell said: bye!", "exitCode": 1}, wantReason: "UNKNOWN", wantExit: func() *int64 { v := int64(1); return &v }()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			s, d := e.liveSession()
			b := e.connectBrowser(s)
			d.readJSON()

			d.writeText(dataAttachMessage(t, s, map[string]any{"type": "TERMINAL_DATA_ENDED", "messageId": "ended-1", "payload": tt.payload}))

			msgs, code := b.collect()
			if code != websocket.CloseNormalClosure || len(msgs) != 1 {
				t.Fatalf("Browser = %d messages, close %d, want TERMINAL_SESSION_ENDED then 1000", len(msgs), code)
			}
			ended := jsonOf(t, msgs[0])
			ep := payload(t, ended)
			if ended["type"] != "TERMINAL_SESSION_ENDED" || ended["terminalSessionId"] != s.ID || ep["reason"] != tt.wantReason {
				t.Fatalf("Browser 메시지 = %v", ended)
			}
			exit, hasExit := ep["exitCode"]
			switch {
			case tt.wantExit == nil && hasExit:
				t.Fatalf("알 수 없는 exit code가 %v로 만들어짐", exit)
			case tt.wantExit != nil && (!hasExit || exit != float64(*tt.wantExit)):
				t.Fatalf("exitCode = %v, want %d", exit, *tt.wantExit)
			}

			_, _, closed, endedCalls := e.control.snapshot()
			if len(closed) != 0 || len(endedCalls) != 1 {
				t.Fatalf("Control = closed %v ended %v, want one SessionEnded and no CloseSession", closed, endedCalls)
			}
			got := endedCalls[0]
			if got.ID != s.ID || got.End.Reason != tt.wantReason || !got.End.FromConnector || (got.End.ExitCode == nil) != (tt.wantExit == nil) {
				t.Fatalf("SessionEnded = %+v", got)
			}

			// Connector는 이미 종료를 알렸으므로 TERMINAL_DATA_CLOSE를 다시 받지 않는다.
			dataMsgs, dataCode := d.collect()
			if len(dataMsgs) != 0 || dataCode != websocket.CloseNormalClosure {
				t.Fatalf("data channel = %d messages, close %d, want none then 1000", len(dataMsgs), dataCode)
			}
			if e.relay.Sessions() != 0 {
				t.Fatalf("Sessions = %d, want 0", e.relay.Sessions())
			}
		})
	}
}

// Browser가 없어도(DETACHED) Connector의 종료 통지는 TerminalSession을 종료하고 이후 attach할 수 없다.
func TestDataEndedWhileDetachedEndsTheSession(t *testing.T) {
	e := newEnv(t)
	s, d := e.liveSession()
	d.writeText(dataAttachMessage(t, s, map[string]any{"type": "TERMINAL_DATA_ENDED", "payload": map[string]any{"reason": "PTY_EXITED", "exitCode": 0}}))
	eventually(t, "SessionEnded 기록", func() bool { _, _, _, ended := e.control.snapshot(); return len(ended) == 1 })
	eventually(t, "Relay 정리", func() bool { return e.relay.Sessions() == 0 })
	if e.clock.Pending() != 0 {
		t.Fatalf("종료 뒤 grace timer가 남음: %d", e.clock.Pending())
	}
}

// Control이 종료를 결정한 TerminalSession(명시적 종료, Reset/Cleanup, 서비스 종료)은 Browser에 이유를 알리고
// 이유에 맞는 close code로 닫는다. 단순 Browser disconnect와 달리 reconnect 대상이 아니다.
func TestTerminateNotifiesBrowserAndConnector(t *testing.T) {
	ptr := func(v int64) *int64 { return &v }
	tests := []struct {
		name          string
		end           realtime.End
		wantClose     int
		wantDataClose bool
		wantExit      *int64
	}{
		{name: "explicit close", end: realtime.End{Reason: realtime.EndReasonSessionClosed}, wantClose: 1000, wantDataClose: true},
		{name: "lab reset", end: realtime.End{Reason: realtime.EndReasonLabReset}, wantClose: 4006, wantDataClose: true},
		{name: "lab cleanup", end: realtime.End{Reason: realtime.EndReasonLabCleanup}, wantClose: 4006, wantDataClose: true},
		{name: "service restarting", end: realtime.End{Reason: realtime.EndReasonServiceRestarting}, wantClose: 1012, wantDataClose: true},
		{name: "reported by connector", end: realtime.End{Reason: realtime.EndReasonPTYExited, ExitCode: ptr(0), FromConnector: true}, wantClose: 1000, wantExit: ptr(0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			s, d := e.liveSession()
			b := e.connectBrowser(s)
			d.readJSON()

			e.relay.Terminate(s.ID, tt.end)

			msgs, code := b.collect()
			if code != tt.wantClose || len(msgs) != 1 {
				t.Fatalf("Browser = %d messages, close %d, want 1 message, close %d", len(msgs), code, tt.wantClose)
			}
			ended := jsonOf(t, msgs[0])
			ep := payload(t, ended)
			if ended["type"] != "TERMINAL_SESSION_ENDED" || ep["reason"] != tt.end.Reason {
				t.Fatalf("Browser 메시지 = %v", ended)
			}
			if tt.wantExit != nil && ep["exitCode"] != float64(*tt.wantExit) {
				t.Fatalf("exitCode = %v, want %d", ep["exitCode"], *tt.wantExit)
			}
			if tt.wantExit == nil {
				if _, ok := ep["exitCode"]; ok {
					t.Fatalf("알 수 없는 exit code가 전달됨: %v", ep)
				}
			}

			dataMsgs, dataCode := d.collect()
			if dataCode != websocket.CloseNormalClosure {
				t.Fatalf("data channel close code = %d, want 1000", dataCode)
			}
			if tt.wantDataClose {
				if len(dataMsgs) != 1 || jsonOf(t, dataMsgs[0])["type"] != "TERMINAL_DATA_CLOSE" || payload(t, jsonOf(t, dataMsgs[0]))["reason"] != tt.end.Reason {
					t.Fatalf("Connector 메시지 = %v, want TERMINAL_DATA_CLOSE(%s)", dataMsgs, tt.end.Reason)
				}
			} else if len(dataMsgs) != 0 {
				t.Fatalf("Connector가 알린 종료에 TERMINAL_DATA_CLOSE를 다시 보냄: %v", dataMsgs)
			}
			if e.relay.Sessions() != 0 || e.clock.Pending() != 0 {
				t.Fatalf("정리되지 않음: sessions %d timers %d", e.relay.Sessions(), e.clock.Pending())
			}
		})
	}
}

// Terminate는 없는 TerminalSession에도, 이미 종료된 TerminalSession에 반복해도 안전하다(멱등).
func TestTerminateIsIdempotent(t *testing.T) {
	e := newEnv(t)
	s, d := e.liveSession()
	b := e.connectBrowser(s)
	d.readJSON()

	e.relay.Terminate("unknown-session", realtime.End{Reason: realtime.EndReasonSessionClosed})
	e.relay.Terminate(s.ID, realtime.End{Reason: realtime.EndReasonSessionClosed})
	e.relay.Terminate(s.ID, realtime.End{Reason: realtime.EndReasonLabReset})

	msgs, code := b.collect()
	if code != 1000 || len(msgs) != 1 {
		t.Fatalf("Browser = %d messages, close %d, want exactly one TERMINAL_SESSION_ENDED and 1000", len(msgs), code)
	}
	if reason := payload(t, jsonOf(t, msgs[0]))["reason"]; reason != "SESSION_CLOSED" {
		t.Fatalf("reason = %v, want the first one (SESSION_CLOSED)", reason)
	}
}

// Forget은 생성에 실패한 TerminalSession의 등록과 bind된 data channel을 조용히 정리한다.
func TestForgetClosesDataChannelQuietly(t *testing.T) {
	e := newEnv(t)
	s := e.newSession()
	d := e.connectData(s)

	e.relay.Forget(s.ID)

	msgs, code := d.collect()
	if code != websocket.CloseNormalClosure || len(msgs) != 0 {
		t.Fatalf("data channel = %d messages, close %d, want none then 1000", len(msgs), code)
	}
	if e.relay.Sessions() != 0 || e.relay.DataBound(s.ID) {
		t.Fatalf("Forget 뒤 상태가 남음: sessions %d bound %v", e.relay.Sessions(), e.relay.DataBound(s.ID))
	}
	// 같은 ID를 다시 기대할 수 있다.
	if err := e.relay.Expect(realtime.Expected{TerminalSessionID: s.ID, ConnectorID: s.ConnectorID, LabInstanceID: s.LabID, Generation: s.Generation}); err != nil {
		t.Fatalf("Expect() after Forget error = %v", err)
	}
}

// 서비스 종료 시 active TerminalSession은 SERVICE_RESTARTING으로 종료한다. Connector에 CLOSE를 요청하고
// Browser에 이유를 알린 뒤 1012로 닫는다.
func TestShutdownEndsActiveSessionsWithServiceRestarting(t *testing.T) {
	e := newEnv(t)
	s, d := e.liveSession()
	b := e.connectBrowser(s)
	d.readJSON()
	s2 := e.newSession() // 아직 Activate되지 않은(생성 중인) TerminalSession도 정리한다.

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.relay.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	msgs, code := b.collect()
	if code != websocket.CloseServiceRestart || len(msgs) != 1 {
		t.Fatalf("Browser = %d messages, close %d, want TERMINAL_SESSION_ENDED then 1012", len(msgs), code)
	}
	if ended := jsonOf(t, msgs[0]); payload(t, ended)["reason"] != "SERVICE_RESTARTING" {
		t.Fatalf("Browser 메시지 = %v", ended)
	}
	dataMsgs, _ := d.collect()
	if len(dataMsgs) != 1 || payload(t, jsonOf(t, dataMsgs[0]))["reason"] != "SERVICE_RESTARTING" {
		t.Fatalf("Connector 메시지 = %v, want TERMINAL_DATA_CLOSE(SERVICE_RESTARTING)", dataMsgs)
	}
	_, _, closed, _ := e.control.snapshot()
	if len(closed) != 2 {
		t.Fatalf("CloseSession = %v, want both sessions", closed)
	}
	for _, c := range closed {
		if c.End.Reason != "SERVICE_RESTARTING" {
			t.Fatalf("종료 사유 = %q", c.End.Reason)
		}
	}
	if e.relay.Sessions() != 0 || s2.ID == "" {
		t.Fatalf("Sessions = %d, want 0", e.relay.Sessions())
	}
}

// data channel의 transport 단절은 PTY 종료가 아니다. TerminalSession은 유지되고 Browser INPUT은 버려지며 그 사실을 한 번 알린다.
// 같은 TerminalSession에 다시 attach하면 resumed=true이고 history 없이 계속된다.
func TestDataTransportLossDoesNotEndTheSession(t *testing.T) {
	e := newEnv(t)
	s, d1 := e.liveSession()
	b := e.connectBrowser(s)
	d1.readJSON()

	d1.close()
	eventually(t, "data channel 해제", func() bool { return !e.relay.DataBound(s.ID) })
	if _, _, closed, ended := e.control.snapshot(); len(closed)+len(ended) != 0 || e.relay.Sessions() != 1 {
		t.Fatalf("data 단절이 TerminalSession을 끝냄: closed %v ended %v", closed, ended)
	}

	b.writeBinary([]byte(inputMarker))
	msg := b.readJSON()
	if msg["type"] != "ERROR" || payload(t, msg)["code"] != "CONNECTOR_UNAVAILABLE" || payload(t, msg)["fatal"] == true {
		t.Fatalf("응답 = %v, want non-fatal ERROR CONNECTOR_UNAVAILABLE", msg)
	}
	b.writeBinary([]byte("again"))
	b.expectNoMessage(150 * time.Millisecond) // 같은 단절에서는 한 번만 알린다.

	d2 := e.connectData(s)
	if p := payload(t, d2.attached); p["resumed"] != true || p["historyAvailable"] != false {
		t.Fatalf("data reconnect payload = %v, want resumed=true historyAvailable=false", p)
	}
	// 재연결 뒤에는 새 INPUT만 전달된다. 단절 중에 버린 INPUT은 재생하지 않는다.
	d2.expectNoMessage(150 * time.Millisecond)
	b.writeBinary([]byte("after-reconnect"))
	if got := d2.readBinary(); string(got) != "after-reconnect" {
		t.Fatalf("재연결 뒤 INPUT = %q, want only the new input", got)
	}
}

// Browser attachment의 bounded queue가 한도를 넘으면 그 attachment만 4005로 종료한다. Connector output reader를 막지 않고,
// PTY를 종료하지 않으며 byte를 조용히 버려 연결을 유지하지도 않는다. 종료 뒤에는 일반 detach처럼 grace가 시작된다.
func TestSlowBrowserIsDisconnectedWithoutBlockingTheConnector(t *testing.T) {
	e := newEnv(t, func(o *realtime.Options) {
		o.BrowserQueueBytes = 256 << 10
		o.BrowserQueueMessages = 4
	})
	s, d := e.liveSession()
	// attach 응답을 socket에서 직접 읽어 reader goroutine을 시작하지 않는다. 그래야 이후 Browser가 정말 읽지 않는다.
	b, _, err := e.dialBrowser(s.OwnerCookie, trustedOrigin)
	if err != nil {
		t.Fatalf("dial error = %v", err)
	}
	b.writeText(browserAttachMessage(t, s, nil, nil))
	if _, data, err := b.conn.ReadMessage(); err != nil || !strings.Contains(string(data), "TERMINAL_ATTACHED") {
		t.Fatalf("attach 응답 = %q, %v", data, err)
	}
	d.readJSON()

	// Browser는 읽지 않는다. Connector가 kernel buffer를 채우고도 남을 만큼 OUTPUT을 보낸다.
	chunk := bytes.Repeat([]byte{'o'}, 256<<10)
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 200; i++ {
			if err := d.conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
				done <- fmt.Errorf("frame %d: %w", i, err)
				return
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Connector OUTPUT write 실패: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("느린 Browser가 Connector OUTPUT reader를 막음")
	}

	eventually(t, "DETACHED 기록", func() bool { _, detached, _, _ := e.control.snapshot(); return len(detached) == 1 })
	if _, _, closed, ended := e.control.snapshot(); len(closed)+len(ended) != 0 || !e.relay.DataBound(s.ID) || e.relay.Sessions() != 1 {
		t.Fatalf("느린 Browser가 PTY/TerminalSession을 끝냄: closed %v ended %v", closed, ended)
	}
	if e.clock.Pending() != 1 {
		t.Fatalf("grace timer = %d, want 1", e.clock.Pending())
	}

	// Browser가 이제 읽으면 이미 전송된 OUTPUT 뒤에 SLOW_CONSUMER ERROR와 4005를 받는다.
	msgs, code := b.collect()
	if code != 4005 {
		t.Fatalf("close code = %d, want 4005", code)
	}
	var sawError bool
	for _, m := range msgs {
		if m.kind != websocket.TextMessage {
			continue
		}
		if obj := jsonOf(t, m); obj["type"] == "ERROR" && payload(t, obj)["code"] == "SLOW_CONSUMER" && payload(t, obj)["fatal"] == true {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("SLOW_CONSUMER ERROR를 받지 못함")
	}

	// Connector는 계속 같은 data channel로 쓸 수 있고 Browser가 다시 붙으면 새 OUTPUT을 받는다.
	// 마지막 flood frame이 Relay에서 아직 처리 중일 수 있으므로 잠시 기다린 뒤 붙고, 그 전에 전송된 flood chunk는 건너뛴다.
	// 종료된 attachment의 queue에 남았던 것을 새 attachment가 받는 일은 없어야 하므로 건너뛰는 것은 flood chunk뿐이다.
	time.Sleep(300 * time.Millisecond)
	b2 := e.connectBrowser(s)
	d.readJSON()
	d.writeBinary([]byte("fresh"))
	for i := 0; ; i++ {
		got := b2.readBinary()
		if string(got) == "fresh" {
			break
		}
		if !bytes.Equal(got, chunk) || i > 20 {
			t.Fatalf("재접속 뒤 OUTPUT = %d bytes, want fresh", len(got))
		}
	}
}

// Browser/Connector 양방향 Binary와 control message가 동시에 오가도 connection마다 writer가 하나라 경쟁이 없고
// 순서와 byte가 보존된다(go test -race로 실행한다).
func TestConcurrentTrafficKeepsOrderAndBytes(t *testing.T) {
	e := newEnv(t)
	s, d := e.liveSession()
	b := e.connectBrowser(s)
	d.readJSON()

	const frames = 300
	type outcome struct {
		inputs, outputs, resizes int
		err                      error
	}
	result := make(chan outcome, 2)

	// Connector 쪽 reader: INPUT은 순서대로, control은 TERMINAL_DATA_RESIZE여야 한다.
	go func() {
		var o outcome
		for o.inputs < frames || o.resizes < frames/50 {
			m, err := d.next(10 * time.Second)
			if err != nil {
				o.err = err
				break
			}
			if m.kind == websocket.BinaryMessage {
				if want := fmt.Sprintf("in-%04d", o.inputs); string(m.data) != want {
					o.err = fmt.Errorf("INPUT %d = %q, want %q", o.inputs, m.data, want)
					break
				}
				o.inputs++
			} else if strings.Contains(string(m.data), "TERMINAL_DATA_RESIZE") {
				o.resizes++
			} else {
				o.err = fmt.Errorf("예상하지 못한 message: %s", m.data)
				break
			}
		}
		result <- o
	}()
	// Browser 쪽 reader: OUTPUT이 순서대로 온다.
	go func() {
		var o outcome
		for o.outputs < frames {
			m, err := b.next(10 * time.Second)
			if err != nil {
				o.err = err
				break
			}
			if want := fmt.Sprintf("out-%04d", o.outputs); m.kind != websocket.BinaryMessage || string(m.data) != want {
				o.err = fmt.Errorf("OUTPUT %d = %q, want %q", o.outputs, m.data, want)
				break
			}
			o.outputs++
		}
		result <- o
	}()

	// writer는 connection마다 goroutine 하나다.
	go func() {
		for i := 0; i < frames; i++ {
			if err := b.conn.WriteMessage(websocket.BinaryMessage, []byte(fmt.Sprintf("in-%04d", i))); err != nil {
				return
			}
			if i%50 == 49 {
				resize := marshal(t, map[string]any{
					"type": "TERMINAL_RESIZE", "messageId": "m", "sentAt": time.Now().UTC().Format(time.RFC3339Nano),
					"terminalSessionId": s.ID, "payload": map[string]any{"cols": 80 + i, "rows": 24},
				})
				if err := b.conn.WriteMessage(websocket.TextMessage, resize); err != nil {
					return
				}
			}
		}
	}()
	go func() {
		for i := 0; i < frames; i++ {
			if err := d.conn.WriteMessage(websocket.BinaryMessage, []byte(fmt.Sprintf("out-%04d", i))); err != nil {
				return
			}
		}
	}()

	for i := 0; i < 2; i++ {
		o := <-result
		if o.err != nil {
			t.Fatalf("동시 traffic 검증 실패: %v", o.err)
		}
	}
}

// Terminal 본문과 token은 log에 남지 않는다. 같은 흐름에서 correlation ID는 남아야 한다(관측 가능성).
func TestLogsNeverContainTerminalContentOrSecrets(t *testing.T) {
	e := newEnv(t)
	s, d := e.liveSession()
	b := e.connectBrowser(s)
	d.readJSON()

	b.writeBinary([]byte(inputMarker))
	if got := d.readBinary(); string(got) != inputMarker {
		t.Fatalf("INPUT = %q", got)
	}
	d.writeBinary([]byte(outputMarker))
	if got := b.readBinary(); string(got) != outputMarker {
		t.Fatalf("OUTPUT = %q", got)
	}

	// 거절 경로에서도 token과 ID를 log에 복사하지 않는다.
	bad, _, err := e.dialBrowser(s.OwnerCookie, trustedOrigin)
	if err != nil {
		t.Fatalf("dial error = %v", err)
	}
	bad.writeText(browserAttachMessage(t, s, nil, map[string]any{"sessionToken": "wrong-token-" + inputMarker}))
	bad.expectClose()

	b.close()
	eventually(t, "DETACHED 기록", func() bool { _, detached, _, _ := e.control.snapshot(); return len(detached) == 1 })
	e.relay.Terminate(s.ID, realtime.End{Reason: realtime.EndReasonSessionClosed})
	d.collect()

	logs := e.logs.String()
	for name, secret := range map[string]string{
		"terminal INPUT":      inputMarker,
		"terminal OUTPUT":     outputMarker,
		"attach token":        s.Token,
		"login session":       ownerCookie,
		"connector credental": connectorCred,
	} {
		if strings.Contains(logs, secret) {
			t.Errorf("log에 %s가 남음", name)
		}
	}
	if !strings.Contains(logs, s.ID) {
		t.Error("log에 terminal_session_id가 없음: 관측 가능한 correlation이 사라짐")
	}
}
