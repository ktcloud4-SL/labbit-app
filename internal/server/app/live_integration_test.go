//go:build integration

package app

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal/terminaltest"
)

func TestLiveSessionEndToEnd(t *testing.T) {
	e := newTerminalEnv(t)
	f := e.fixture

	// 강사용 LabInstance 및 서버 리소스 준비
	instructorLabID := uuid.New()
	_, err := e.conn.Exec(t.Context(),
		`INSERT INTO lab_instances (id, organization_id, lab_execution_id, user_id, participant_role, status, generation)
		 VALUES ($1, $2, $3, $4, 'INSTRUCTOR', 'READY', 1)`,
		instructorLabID, f.OrganizationID, f.LabExecutionID, f.InstructorID)
	if err != nil {
		t.Fatalf("insert instructor lab instance: %v", err)
	}
	f.AddResource(t, e.conn, instructorLabID, 1, "SERVER", "workspace", "PRESENT")

	instructorCookie := terminaltest.LoginSession(t, e.conn, f.InstructorID)

	// 1. 강사가 본인의 LabInstance에서 TerminalSession 생성
	resp := e.request(http.MethodPost, e.createPath(instructorLabID), instructorCookie, createBody)
	if resp.Status != http.StatusCreated {
		t.Fatalf("terminal create status = %d, want 201: %s", resp.Status, resp.Body)
	}
	termBody := resp.json(t)
	termSessionID := termBody["id"].(string)
	termToken := termBody["sessionToken"].(string)

	// 강사 Browser attach
	instructorBrowser, attachedMsg := e.attach(created{
		ID: termSessionID, Token: termToken, Generation: 1, cookie: instructorCookie,
	})
	defer instructorBrowser.close()
	if attachedMsg["terminalSessionId"] != termSessionID {
		t.Fatalf("instructor attachedMsg = %v", attachedMsg)
	}

	// 2. 강사가 LiveSession 생성 (POST /api/v1/terminal-sessions/{id}/live-sessions)
	liveCreateResp := e.request(http.MethodPost, "/api/v1/terminal-sessions/"+termSessionID+"/live-sessions", instructorCookie, "")
	if liveCreateResp.Status != http.StatusCreated {
		t.Fatalf("live create status = %d, want 201: %s", liveCreateResp.Status, liveCreateResp.Body)
	}
	liveBody := liveCreateResp.json(t)
	liveID := liveBody["id"].(string)
	if liveBody["classId"] != f.ClassID.String() || liveBody["sourceTerminalSessionId"] != termSessionID {
		t.Fatalf("liveBody mismatch: %+v", liveBody)
	}

	// 3. 학생이 active LiveSession 조회 (GET /api/v1/classes/{classId}/live-session)
	activeResp := e.request(http.MethodGet, "/api/v1/classes/"+f.ClassID.String()+"/live-session", e.ownerCookie, "")
	if activeResp.Status != http.StatusOK {
		t.Fatalf("active live status = %d, want 200: %s", activeResp.Status, activeResp.Body)
	}
	activeBody := activeResp.json(t)
	if activeBody["id"] != liveID {
		t.Fatalf("activeBody id = %v, want %v", activeBody["id"], liveID)
	}

	// 4. 학생 1 & 학생 2가 /realtime/v1/live 에 WebSocket 연결 및 LIVE_SUBSCRIBE 전송
	connectLiveClient := func(cookie string) *browser {
		dialer := websocket.Dialer{Subprotocols: []string{realtime.LiveSubprotocol}}
		header := http.Header{}
		header.Set("Origin", terminalTrustedOrigin)
		header.Set("Cookie", realtime.SessionCookieName+"="+cookie)
		url := "ws" + strings.TrimPrefix(e.server.URL, "http") + realtime.LivePath
		ws, res, err := dialer.Dial(url, header)
		if err != nil {
			t.Fatalf("live dial error = %v, res = %v", err, res)
		}
		b := &browser{t: t, conn: ws}
		b.start()
		return b
	}

	student1 := connectLiveClient(e.ownerCookie)
	defer student1.close()

	// 학생 1 SUBSCRIBE 전송
	student1.write(websocket.TextMessage, []byte(`{"type":"LIVE_SUBSCRIBE","messageId":"s1","sentAt":"2026-10-01T09:00:00Z","liveSessionId":"`+liveID+`","payload":{}}`))
	sub1Msg := student1.json()
	if sub1Msg["type"] != "LIVE_SUBSCRIBED" || sub1Msg["liveSessionId"] != liveID {
		t.Fatalf("student1 subscribedMsg = %v", sub1Msg)
	}
	if sub1Payload, ok := sub1Msg["payload"].(map[string]any); !ok || sub1Payload["historyAvailable"] != false {
		t.Fatalf("student1 historyAvailable want false, got %v", sub1Msg)
	}

	student2 := connectLiveClient(e.peerCookie)
	defer student2.close()

	student2.write(websocket.TextMessage, []byte(`{"type":"LIVE_SUBSCRIBE","messageId":"s2","sentAt":"2026-10-01T09:00:00Z","liveSessionId":"`+liveID+`","payload":{}}`))
	sub2Msg := student2.json()
	if sub2Msg["type"] != "LIVE_SUBSCRIBED" {
		t.Fatalf("student2 subscribedMsg = %v", sub2Msg)
	}

	// 5. Connector가 source terminal에 PTY output 송신 -> 두 학생 모두에게 fan-out
	ptyOutput := []byte("hello from instructor pty\r\n")
	e.connector.Output(termSessionID, ptyOutput)

	if got1 := student1.binary(); string(got1) != string(ptyOutput) {
		t.Fatalf("student1 output = %q, want %q", got1, ptyOutput)
	}
	if got2 := student2.binary(); string(got2) != string(ptyOutput) {
		t.Fatalf("student2 output = %q, want %q", got2, ptyOutput)
	}

	// 6. Read-only 검증: 학생이 data 또는 임의 control 메시지를 보내면 즉시 1008 policy violation으로 종료
	student2.write(websocket.TextMessage, []byte(`{"type":"MALICIOUS"}`))
	code, _ := student2.closeCode()
	if code != websocket.ClosePolicyViolation {
		t.Fatalf("expected close policy violation (1008), got %d", code)
	}

	// 7. 강사 Browser가 끊겨도 (60초 grace) LiveSession은 유지되어 출력 전달
	instructorBrowser.close()
	e.waitStatus(termSessionID, "DETACHED")

	detachedOutput := []byte("output while instructor browser detached\r\n")
	e.connector.Output(termSessionID, detachedOutput)

	if got1 := student1.binary(); string(got1) != string(detachedOutput) {
		t.Fatalf("student1 output while detached = %q, want %q", got1, detachedOutput)
	}

	// 8. 강사가 LiveSession 명시적 종료 (DELETE /api/v1/live-sessions/{liveSessionId})
	delResp := e.request(http.MethodDelete, "/api/v1/live-sessions/"+liveID, instructorCookie, "")
	if delResp.Status != http.StatusNoContent {
		t.Fatalf("live delete status = %d, want 204: %s", delResp.Status, delResp.Body)
	}

	// 학생 1은 LIVE_ENDED 수신
	endedMsg := student1.json()
	if endedMsg["type"] != "LIVE_ENDED" {
		t.Fatalf("expected LIVE_ENDED, got %v", endedMsg)
	}

	// 9. 원본 TerminalSession은 종료되지 않고 유지됨
	termRow := e.row(termSessionID)
	if termRow.Status == "ENDED" {
		t.Fatalf("source terminal session unexpectedly ended")
	}

	// 10. 활성 LiveSession 조회 시 404
	afterResp := e.request(http.MethodGet, "/api/v1/classes/"+f.ClassID.String()+"/live-session", e.ownerCookie, "")
	if afterResp.Status != http.StatusNotFound {
		t.Fatalf("active live status after end = %d, want 404", afterResp.Status)
	}
}
