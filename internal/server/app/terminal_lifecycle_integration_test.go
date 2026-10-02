//go:build integration

package app

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal"
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal/terminaltest"
)

// attach token은 CSPRNG 32 bytes의 padding 없는 Base64URL이다. DB에는 SHA-256 digest만 저장하고 원문은 어디에도 없다.
// 절대 만료는 발급 후 8시간이며 그 경계에서 정확히 만료된다. 회전이나 갱신은 없다.
func TestAttachTokenSemantics(t *testing.T) {
	e := newTerminalEnv(t)
	f := e.fixture

	s1 := e.createSession(e.ownerCookie, f.LabInstanceID)
	s2 := e.createSession(e.peerCookie, f.PeerLabInstanceID)
	if !base64URLToken.MatchString(s1.Token) || s1.Token == s2.Token {
		t.Fatalf("token 형식/유일성: %q %q", s1.Token, s2.Token)
	}

	// DB에는 digest만 있다.
	row := e.row(s1.ID)
	if string(row.TokenHash) != string(tokenDigest(t, s1.Token)) {
		t.Fatal("attach_token_hash가 SHA-256(raw token)이 아님")
	}
	if want := row.CreatedAt.Add(8 * time.Hour); !row.TokenExpiresAt.Equal(want) {
		t.Fatalf("token_expires_at = %v, want created_at + 8h = %v", row.TokenExpiresAt, want)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(s1.Token)
	for name, needle := range map[string]string{
		"base64url token": s1.Token, "std base64": base64.StdEncoding.EncodeToString(raw), "hex": hex.EncodeToString(raw),
	} {
		if table, found := e.databaseContains(needle); found {
			t.Fatalf("raw attach token(%s)이 DB table %s에 있음", name, table)
		}
	}
	// 대조군: 검사기가 실제로 저장된 값을 찾는다(digest의 hex는 bytea 출력에 나타난다).
	if _, found := e.databaseContains(hex.EncodeToString(row.TokenHash)); !found {
		t.Fatal("DB 검사기가 저장된 digest를 찾지 못함: 검사기가 동작하지 않을 수 있음")
	}

	// 8시간 경계: 만료 1ns 전까지는 attach할 수 있고 정확히 만료 시각에는 사용할 수 없다.
	b1, _ := e.attach(s1)
	e.clock.Advance(8*time.Hour - time.Nanosecond)
	b2, attached := e.attach(s1)
	if attached["type"] != "TERMINAL_ATTACHED" {
		t.Fatal("만료 1ns 전 attach 실패")
	}
	if code, _ := b1.closeCode(); code != 4004 {
		t.Fatalf("교체된 Browser close code = %d, want 4004", code)
	}
	e.clock.Advance(time.Nanosecond)
	if code, closeCode := e.attachRejected(e.ownerCookie, s1.ID, s1.Token); code != "INVALID_SESSION_TOKEN" || closeCode != 4001 {
		t.Fatalf("만료 시각의 attach = %s %d, want INVALID_SESSION_TOKEN 4001", code, closeCode)
	}
	// 이미 attach한 연결은 token 만료로 끊기지 않는다. token은 attach할 때만 확인한다.
	b2.write(websocket.BinaryMessage, []byte("still-attached"))
	eventually(t, "만료 뒤에도 INPUT 전달", 5*time.Second, func() bool { return string(e.connector.Inputs(s1.ID)) == "still-attached" })
}

// attach/re-attach마다 현재 로그인 세션, 현재 사용자, 현재 Class 권한, LabInstance 소유, TerminalSession lifecycle, token digest를 다시 확인한다.
func TestAttachReauthorizesEveryTime(t *testing.T) {
	type testCase struct {
		name     string
		setup    func(e *terminalEnv, own, peer created)
		cookie   func(e *terminalEnv) string
		session  func(own, peer created) string
		token    func(own, peer created) string
		wantCode string
		wantWS   int // Upgrade 전에 거절되는 경우의 HTTP status
		wantBye  int
	}
	ownID := func(own, peer created) string { return own.ID }
	ownToken := func(own, peer created) string { return own.Token }
	owner := func(e *terminalEnv) string { return e.ownerCookie }
	tests := []testCase{
		{name: "another user's login with the right token", cookie: func(e *terminalEnv) string { return e.peerCookie }, session: ownID, token: ownToken, wantCode: "FORBIDDEN", wantBye: 4002},
		{name: "user of another organization", cookie: func(e *terminalEnv) string { return e.foreignCookie }, session: ownID, token: ownToken, wantCode: "FORBIDDEN", wantBye: 4002},
		{name: "another session's token", cookie: owner, session: ownID, token: func(own, peer created) string { return peer.Token }, wantCode: "INVALID_SESSION_TOKEN", wantBye: 4001},
		{name: "another user's session with its own token", cookie: owner, session: func(own, peer created) string { return peer.ID }, token: func(own, peer created) string { return peer.Token }, wantCode: "FORBIDDEN", wantBye: 4002},
		{name: "token with trailing newline is not canonical", cookie: owner, session: ownID, token: func(own, peer created) string { return own.Token + "\n" }, wantCode: "INVALID_SESSION_TOKEN", wantBye: 4001},
		{name: "token with padding", cookie: owner, session: ownID, token: func(own, peer created) string { return own.Token + "=" }, wantCode: "INVALID_SESSION_TOKEN", wantBye: 4001},
		{name: "token of the wrong length", cookie: owner, session: ownID, token: func(own, peer created) string { return own.Token[:40] }, wantCode: "INVALID_SESSION_TOKEN", wantBye: 4001},
		{name: "unknown session", cookie: owner, session: func(own, peer created) string { return uuid.NewString() }, token: ownToken, wantCode: "SESSION_NOT_FOUND", wantBye: 4003},
		{name: "malformed session id", cookie: owner, session: func(own, peer created) string { return "not-a-uuid" }, token: ownToken, wantCode: "SESSION_NOT_FOUND", wantBye: 4003},
		{name: "non-canonical session id", cookie: owner, session: func(own, peer created) string { return strings.ToUpper(own.ID) }, token: ownToken, wantCode: "SESSION_NOT_FOUND", wantBye: 4003},
		{
			name: "class membership removed after creation", cookie: owner, session: ownID, token: ownToken, wantCode: "FORBIDDEN", wantBye: 4002,
			setup: func(e *terminalEnv, own, peer created) {
				terminaltest.Exec(t, e.conn, `DELETE FROM class_memberships WHERE class_id = $1 AND user_id = $2`, e.fixture.ClassID, e.fixture.OwnerID)
			},
		},
		{
			name: "lab instance reassigned to another user", cookie: owner, session: ownID, token: ownToken, wantCode: "FORBIDDEN", wantBye: 4002,
			setup: func(e *terminalEnv, own, peer created) {
				terminaltest.Exec(t, e.conn, `UPDATE lab_instances SET user_id = $2 WHERE id = $1`, e.fixture.LabInstanceID, e.fixture.InstructorID)
			},
		},
		{
			name: "login session revoked", cookie: owner, session: ownID, token: ownToken, wantWS: http.StatusUnauthorized,
			setup: func(e *terminalEnv, own, peer created) {
				terminaltest.Exec(t, e.conn, `UPDATE auth_sessions SET revoked_at = now() WHERE user_id = $1`, e.fixture.OwnerID)
			},
		},
		{
			name: "login session expired", cookie: owner, session: ownID, token: ownToken, wantWS: http.StatusUnauthorized,
			setup: func(e *terminalEnv, own, peer created) {
				terminaltest.Exec(t, e.conn, `UPDATE auth_sessions SET created_at = now() - interval '2 hours', expires_at = now() - interval '1 hour' WHERE user_id = $1`, e.fixture.OwnerID)
			},
		},
		{
			name: "user disabled", cookie: owner, session: ownID, token: ownToken, wantWS: http.StatusUnauthorized,
			setup: func(e *terminalEnv, own, peer created) {
				terminaltest.Exec(t, e.conn, `UPDATE users SET disabled_at = now() WHERE id = $1`, e.fixture.OwnerID)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newTerminalEnv(t)
			own := e.createSession(e.ownerCookie, e.fixture.LabInstanceID)
			peer := e.createSession(e.peerCookie, e.fixture.PeerLabInstanceID)
			if tt.setup != nil {
				tt.setup(e, own, peer)
			}

			if tt.wantWS != 0 {
				_, resp, err := e.dialBrowser(tt.cookie(e), terminalTrustedOrigin)
				if err == nil || resp == nil || resp.StatusCode != tt.wantWS {
					t.Fatalf("Upgrade = %v, %v, want HTTP %d", resp, err, tt.wantWS)
				}
			} else {
				code, closeCode := e.attachRejected(tt.cookie(e), tt.session(own, peer), tt.token(own, peer))
				if code != tt.wantCode || closeCode != tt.wantBye {
					t.Fatalf("attach = %s %d, want %s %d", code, closeCode, tt.wantCode, tt.wantBye)
				}
			}
			// 거절된 attach는 session 상태를 바꾸지 않는다.
			if row := e.row(own.ID); row.Status != "DETACHED" || row.AttachedAt != nil {
				t.Fatalf("거절된 attach가 session을 바꿈: %+v", row)
			}
		})
	}
}

// 생성 후 첫 attach를 기다리는 session도 60초 안에 attach하지 않으면 종료한다. Connector에 CLOSE를 요청하고 이후 attach할 수 없다.
func TestSessionNeverAttachedExpires(t *testing.T) {
	e := newTerminalEnv(t)
	s := e.createSession(e.ownerCookie, e.fixture.LabInstanceID)

	e.clock.Advance(59 * time.Second)
	time.Sleep(150 * time.Millisecond)
	if row := e.row(s.ID); row.Status != "DETACHED" {
		t.Fatalf("59초에 종료됨: %+v", row)
	}
	e.clock.Advance(time.Second)

	row := e.waitStatus(s.ID, "ENDED")
	if row.EndReason == nil || *row.EndReason != "SESSION_EXPIRED" {
		t.Fatalf("row = %+v, want SESSION_EXPIRED", row)
	}
	eventually(t, "TERMINAL_CLOSE", 5*time.Second, func() bool { return len(e.connector.Closes()) == 1 })
	if closeMsg := e.connector.Closes()[0]; closeMsg.Reason != "SESSION_EXPIRED" || closeMsg.TerminalSessionID != s.ID {
		t.Fatalf("TERMINAL_CLOSE = %+v", closeMsg)
	}
	eventually(t, "TERMINAL_DATA_CLOSE", 5*time.Second, func() bool { return len(e.connector.DataCloses(s.ID)) == 1 })
	eventually(t, "Relay 정리", 5*time.Second, func() bool { return e.stack.Relay.Sessions() == 0 })
	if code, closeCode := e.attachRejected(e.ownerCookie, s.ID, s.Token); code != "SESSION_EXPIRED" || closeCode != 4003 {
		t.Fatalf("만료된 session attach = %s %d, want SESSION_EXPIRED 4003", code, closeCode)
	}
}

// Browser가 단절된 뒤 grace가 만료되면 종료하고 Connector에 CLOSE를 요청한다. 만료 직전에는 재접속할 수 있다.
func TestGraceExpiryAfterBrowserDisconnect(t *testing.T) {
	e := newTerminalEnv(t)
	s := e.createSession(e.ownerCookie, e.fixture.LabInstanceID)
	b, _ := e.attach(s)
	b.close()
	e.waitStatus(s.ID, "DETACHED")

	e.clock.Advance(60 * time.Second)
	row := e.waitStatus(s.ID, "ENDED")
	if row.EndReason == nil || *row.EndReason != "SESSION_EXPIRED" {
		t.Fatalf("row = %+v, want SESSION_EXPIRED", row)
	}
	eventually(t, "TERMINAL_CLOSE", 5*time.Second, func() bool { return len(e.connector.Closes()) == 1 })
	if code, closeCode := e.attachRejected(e.ownerCookie, s.ID, s.Token); code != "SESSION_EXPIRED" || closeCode != 4003 {
		t.Fatalf("grace 만료 뒤 attach = %s %d, want SESSION_EXPIRED 4003", code, closeCode)
	}
}

// Reset으로 generation이 바뀐 뒤 이전 generation의 TerminalSession은 attach할 수 없다. 그 TerminalSession을 종료하고 CLOSE를 요청한다.
func TestStaleGenerationAttachIsRejectedAndEndsTheSession(t *testing.T) {
	e := newTerminalEnv(t)
	f := e.fixture
	s := e.createSession(e.ownerCookie, f.LabInstanceID)
	f.BumpGeneration(t, e.conn, f.LabInstanceID)

	code, closeCode := e.attachRejected(e.ownerCookie, s.ID, s.Token)
	if code != "LAB_MUTATION" || closeCode != 4006 {
		t.Fatalf("stale attach = %s %d, want LAB_MUTATION 4006", code, closeCode)
	}
	row := e.row(s.ID)
	if row.Status != "ENDED" || row.EndReason == nil || *row.EndReason != "LAB_RESET" {
		t.Fatalf("row = %+v, want ENDED LAB_RESET", row)
	}
	eventually(t, "TERMINAL_CLOSE", 5*time.Second, func() bool { return len(e.connector.Closes()) == 1 })
	if closeMsg := e.connector.Closes()[0]; closeMsg.Reason != "LAB_RESET" || closeMsg.Generation != 1 {
		t.Fatalf("TERMINAL_CLOSE = %+v, want the old generation", closeMsg)
	}
	// 같은 token으로 다시 시도해도 종료된 session이다.
	if code, _ := e.attachRejected(e.ownerCookie, s.ID, s.Token); code != "SESSION_EXPIRED" {
		t.Fatalf("다시 attach = %s, want SESSION_EXPIRED", code)
	}
	// 새 generation의 VM에는 새 TerminalSession을 만들 수 있다.
	fresh := e.createSession(e.ownerCookie, f.LabInstanceID)
	if fresh.Generation != 2 {
		t.Fatalf("새 TerminalSession generation = %d, want 2", fresh.Generation)
	}
}

// Reset/Cleanup이 호출할 경계다. LabInstance의 모든 TerminalSession을 그 이유로 종료하며 grace(reconnect) 대상이 아니다.
func TestCloseForLabMutation(t *testing.T) {
	for _, reason := range []string{"LAB_RESET", "LAB_CLEANUP"} {
		t.Run(reason, func(t *testing.T) {
			e := newTerminalEnv(t)
			f := e.fixture
			attachedSession := e.createSession(e.ownerCookie, f.LabInstanceID)
			b, _ := e.attach(attachedSession)
			detachedSession := e.createSession(e.ownerCookie, f.LabInstanceID)
			peerSession := e.createSession(e.peerCookie, f.PeerLabInstanceID)

			if err := e.stack.Terminals.CloseForLabMutation(context.Background(), terminal.LabMutation{LabInstanceID: f.LabInstanceID, Reason: "SESSION_CLOSED"}); !errors.Is(err, terminal.ErrInvalidReason) {
				t.Fatalf("허용하지 않는 이유 error = %v, want ErrInvalidReason", err)
			}
			if err := e.stack.Terminals.CloseForLabMutation(context.Background(), terminal.LabMutation{LabInstanceID: f.LabInstanceID, Reason: reason, OperationID: "operation-1"}); err != nil {
				t.Fatalf("CloseForLabMutation() error = %v", err)
			}

			for _, id := range []string{attachedSession.ID, detachedSession.ID} {
				row := e.waitStatus(id, "ENDED")
				if row.EndReason == nil || *row.EndReason != reason || row.GraceExpiresAt != nil {
					t.Fatalf("row %s = %+v, want ENDED %s without grace", id, row, reason)
				}
			}
			// 다른 LabInstance의 TerminalSession은 영향이 없다.
			if row := e.row(peerSession.ID); row.Status != "DETACHED" {
				t.Fatalf("다른 LabInstance의 session이 종료됨: %+v", row)
			}
			code, collected := b.closeCode()
			if code != 4006 || len(collected) != 1 || !strings.Contains(string(collected[0].data), `"`+reason+`"`) {
				t.Fatalf("Browser = close %d, %d messages (%s)", code, len(collected), reason)
			}
			eventually(t, "TERMINAL_CLOSE 2건", 5*time.Second, func() bool { return len(e.connector.Closes()) == 2 })
			for _, c := range e.connector.Closes() {
				if c.Reason != reason || c.OperationID != "operation-1" || c.LabInstanceID != f.LabInstanceID.String() {
					t.Fatalf("TERMINAL_CLOSE = %+v", c)
				}
			}
			// 반복해도 이미 종료된 session에 CLOSE를 다시 보내지 않는다(멱등).
			if err := e.stack.Terminals.CloseForLabMutation(context.Background(), terminal.LabMutation{LabInstanceID: f.LabInstanceID, Reason: reason}); err != nil {
				t.Fatal(err)
			}
			time.Sleep(150 * time.Millisecond)
			if got := len(e.connector.Closes()); got != 2 {
				t.Fatalf("반복 호출 뒤 TERMINAL_CLOSE = %d, want 2", got)
			}
			// 종료 이유가 Reset/Cleanup이면 그 TerminalSession은 reconnect 대상이 아니다.
			if code, _ := e.attachRejected(e.ownerCookie, attachedSession.ID, attachedSession.Token); code != "SESSION_EXPIRED" {
				t.Fatalf("종료된 session attach = %s", code)
			}
		})
	}
}

// Connector가 알린 종료(PTY 종료, SSH 끊김)는 DB에 기록하고 Browser에 이유와 알려진 exit code를 전달한다.
// 같은 종료가 Data와 Control 양쪽으로 와도 멱등이고, Connector에 CLOSE를 다시 보내지 않는다.
func TestConnectorReportedEnd(t *testing.T) {
	three := 3
	zero := 0
	tests := []struct {
		name       string
		end        func(e *terminalEnv, s created)
		wantReason string
		wantExit   *int
	}{
		{name: "pty exit via data channel", end: func(e *terminalEnv, s created) { e.connector.EndData(s.ID, "PTY_EXITED", &three) }, wantReason: "PTY_EXITED", wantExit: &three},
		{name: "exit code zero is known", end: func(e *terminalEnv, s created) { e.connector.EndData(s.ID, "PTY_EXITED", &zero) }, wantReason: "PTY_EXITED", wantExit: &zero},
		{name: "ssh disconnect via control without exit code", end: func(e *terminalEnv, s created) {
			e.connector.EndControl(s.ID, e.fixture.LabInstanceID.String(), 1, "SSH_DISCONNECTED", nil)
		}, wantReason: "SSH_DISCONNECTED"},
		{name: "pty exit via control with exit code", end: func(e *terminalEnv, s created) {
			e.connector.EndControl(s.ID, e.fixture.LabInstanceID.String(), 1, "PTY_EXITED", &three)
		}, wantReason: "PTY_EXITED", wantExit: &three},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newTerminalEnv(t)
			s := e.createSession(e.ownerCookie, e.fixture.LabInstanceID)
			b, _ := e.attach(s)

			tt.end(e, s)

			code, collected := b.closeCode()
			if code != 1000 || len(collected) != 1 {
				t.Fatalf("Browser = close %d, %d messages", code, len(collected))
			}
			ended := string(collected[0].data)
			if !strings.Contains(ended, `"TERMINAL_SESSION_ENDED"`) || !strings.Contains(ended, `"reason":"`+tt.wantReason+`"`) {
				t.Fatalf("Browser message = %s", ended)
			}
			switch {
			case tt.wantExit == nil && strings.Contains(ended, "exitCode"):
				t.Fatalf("알 수 없는 exit code가 전달됨: %s", ended)
			case tt.wantExit != nil && !strings.Contains(ended, `"exitCode":`+strconv.Itoa(*tt.wantExit)):
				t.Fatalf("exit code가 전달되지 않음: %s", ended)
			}
			row := e.waitStatus(s.ID, "ENDED")
			if row.EndReason == nil || *row.EndReason != tt.wantReason {
				t.Fatalf("row = %+v, want %s", row, tt.wantReason)
			}
			// exit code는 DB에 저장하지 않는다(알려진 경우에도 wire로만 전달).
			if len(e.connector.Closes()) != 0 {
				t.Fatalf("Connector가 알린 종료에 CLOSE를 다시 보냄: %+v", e.connector.Closes())
			}
			// 같은 종료가 다른 경로로 한 번 더 와도 기록과 상태는 그대로다.
			e.connector.EndControl(s.ID, e.fixture.LabInstanceID.String(), 1, "SESSION_CLOSED", nil)
			time.Sleep(200 * time.Millisecond)
			if again := e.row(s.ID); again.EndReason == nil || *again.EndReason != tt.wantReason || !again.EndedAt.Equal(*row.EndedAt) {
				t.Fatalf("중복 종료가 기록을 바꿈: %+v", again)
			}
			if e.stack.Relay.Sessions() != 0 {
				t.Fatalf("Relay Sessions = %d, want 0", e.stack.Relay.Sessions())
			}
		})
	}
}

// 종료 통지는 요청에 대한 응답이 아니므로 권위 있는 상태와 대조한다. terminalSessionId, labInstanceId, generation, 그리고
// 그 TerminalSession을 소유한 Connector가 모두 맞아야 종료한다. 하나라도 다르면 다른 TerminalSession으로 fallback하지 않는다.
func TestConnectorEndedWithWrongClaimsIsIgnored(t *testing.T) {
	e := newTerminalEnv(t)
	f := e.fixture
	a := e.createSession(e.ownerCookie, f.LabInstanceID)
	b := e.createSession(e.peerCookie, f.PeerLabInstanceID)

	// 같은 조직의 두 번째 Connector. 첫 Connector의 TerminalSession을 받지도, 종료하지도 못한다.
	second := "terminaltest-connector-credential-second-91ab"
	secondID := uuid.New()
	digest := connector.CredentialDigest(second)
	terminaltest.Exec(t, e.conn, `INSERT INTO connectors (id, organization_id, name) VALUES ($1, $2, 'Second Connector')`, secondID, f.OrganizationID)
	terminaltest.Exec(t, e.conn, `INSERT INTO connector_credentials (id, connector_id, credential_hash) VALUES ($1, $2, $3)`, uuid.New(), secondID, digest[:])
	other := terminaltest.NewConnector(t, e.server.URL, second)
	other.Start()
	eventually(t, "두 번째 Connector ready", 10*time.Second, func() bool {
		return e.stack.Registry.WithReadyRoute(secondID, func(connector.Session, connector.Route) error { return nil }) == nil
	})
	if got := other.Opens(); len(got) != 0 {
		t.Fatalf("두 번째 Connector가 TERMINAL_OPEN을 받음: %+v", got)
	}

	lab := f.LabInstanceID.String()
	peerLab := f.PeerLabInstanceID.String()
	claims := []struct {
		name      string
		sender    *terminaltest.Connector
		session   string
		labID     string
		generatio int64
	}{
		{"another connector claims my session", other, a.ID, lab, 1},
		{"my session with another lab instance", e.connector, a.ID, peerLab, 1},
		{"my session with another generation", e.connector, a.ID, lab, 2},
		{"another user's session with my lab instance", e.connector, b.ID, lab, 1},
		{"unknown session", e.connector, uuid.NewString(), lab, 1},
		{"non-canonical session id", e.connector, strings.ToUpper(a.ID), lab, 1},
		{"malformed session id", e.connector, "not-a-uuid", lab, 1},
	}
	for _, c := range claims {
		c.sender.EndControl(c.session, c.labID, c.generatio, "PTY_EXITED", nil)
	}
	time.Sleep(400 * time.Millisecond)
	for name, s := range map[string]created{"a": a, "b": b} {
		if row := e.row(s.ID); row.Status != "DETACHED" {
			t.Fatalf("잘못된 종료 통지가 session %s를 종료함: %+v", name, row)
		}
	}
	if got := e.stack.Relay.Sessions(); got != 2 {
		t.Fatalf("Relay Sessions = %d, want 2", got)
	}

	// 두 번째 Connector의 Data WSS attach도 첫 Connector의 TerminalSession에는 bind되지 않는다(FORBIDDEN).
	dialer := websocket.Dialer{Subprotocols: []string{"labbit.connector-terminal.v1"}, HandshakeTimeout: 5 * time.Second}
	ws, _, err := dialer.Dial("ws"+strings.TrimPrefix(e.server.URL, "http")+"/connector/v1/terminal-data", http.Header{"Authorization": []string{"Bearer " + second}})
	if err != nil {
		t.Fatalf("Data WSS 연결 실패: %v", err)
	}
	defer ws.Close()
	_ = ws.WriteJSON(map[string]any{
		"type": "TERMINAL_DATA_ATTACH", "messageId": "m", "sentAt": time.Now().UTC().Format(time.RFC3339Nano),
		"terminalSessionId": a.ID, "labInstanceId": lab, "generation": 1, "payload": map[string]any{"runtimeId": "other-runtime"},
	})
	var reject map[string]any
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := ws.ReadJSON(&reject); err != nil || reject["type"] != "ERROR" || reject["payload"].(map[string]any)["code"] != "FORBIDDEN" {
		t.Fatalf("다른 Connector의 attach 응답 = %v, %v, want ERROR FORBIDDEN", reject, err)
	}
	if !e.stack.Relay.DataBound(a.ID) {
		t.Fatal("다른 Connector의 시도가 기존 data channel을 끊음")
	}

	// 올바른 통지는 그 TerminalSession만 종료한다.
	e.connector.EndControl(a.ID, lab, 1, "PTY_EXITED", nil)
	e.waitStatus(a.ID, "ENDED")
	if row := e.row(b.ID); row.Status != "DETACHED" {
		t.Fatalf("다른 TerminalSession이 종료됨: %+v", row)
	}
}

// 같은 TerminalSession에 새 Browser가 attach하면 이전 Browser만 4004로 끊기고 같은 PTY/data channel을 계속 쓴다.
func TestSecondBrowserReplacesFirstOnly(t *testing.T) {
	e := newTerminalEnv(t)
	s := e.createSession(e.ownerCookie, e.fixture.LabInstanceID)
	b1, _ := e.attach(s)
	b2, attached := e.attach(s)

	if code, _ := b1.closeCode(); code != 4004 {
		t.Fatalf("이전 Browser close code = %d, want 4004", code)
	}
	if payload := attached["payload"].(map[string]any); payload["resumed"] != true || payload["historyAvailable"] != false {
		t.Fatalf("payload = %v", payload)
	}
	if row := e.waitStatus(s.ID, "ACTIVE"); row.GraceExpiresAt != nil {
		t.Fatalf("row = %+v", row)
	}
	e.connector.Output(s.ID, []byte("for-the-current-browser"))
	if got := b2.binary(); string(got) != "for-the-current-browser" {
		t.Fatalf("OUTPUT = %q", got)
	}
	if e.connector.DataDials() != 1 || len(e.connector.Opens()) != 1 {
		t.Fatalf("새 attach가 PTY나 data channel을 다시 만듦: dials %d opens %d", e.connector.DataDials(), len(e.connector.Opens()))
	}
	time.Sleep(200 * time.Millisecond)
	if row := e.row(s.ID); row.Status != "ACTIVE" || len(e.connector.Closes()) != 0 {
		t.Fatalf("교체가 session 상태를 바꿈: %+v closes %v", row, e.connector.Closes())
	}
}

// 명시적 종료는 소유자만 할 수 있다. 다른 사용자, 다른 조직, 없는 ID, 인증/출처 실패는 session을 종료하지 않는다.
func TestExplicitCloseAuthorization(t *testing.T) {
	e := newTerminalEnv(t)
	s := e.createSession(e.ownerCookie, e.fixture.LabInstanceID)
	path := "/api/v1/terminal-sessions/" + s.ID

	tests := []struct {
		name       string
		path       string
		cookie     string
		mods       []func(*http.Request)
		wantStatus int
		wantCode   string
	}{
		{"no login", path, "", nil, 401, "unauthenticated"},
		{"another user", path, e.peerCookie, nil, 403, "forbidden"},
		{"another organization", path, e.foreignCookie, nil, 403, "forbidden"},
		{"unknown session", "/api/v1/terminal-sessions/" + uuid.NewString(), e.ownerCookie, nil, 404, "not_found"},
		{"malformed id", "/api/v1/terminal-sessions/not-a-uuid", e.ownerCookie, nil, 404, "not_found"},
		{"non-canonical id", "/api/v1/terminal-sessions/" + strings.ToUpper(s.ID), e.ownerCookie, nil, 404, "not_found"},
		{"foreign origin", path, e.ownerCookie, []func(*http.Request){func(r *http.Request) { r.Header.Set("Origin", "https://evil.test") }}, 403, "csrf_rejected"},
		{"missing origin", path, e.ownerCookie, []func(*http.Request){func(r *http.Request) { r.Header.Del("Origin") }}, 403, "csrf_rejected"},
	}
	for _, tt := range tests {
		resp := e.request(http.MethodDelete, tt.path, tt.cookie, "", tt.mods...)
		if resp.Status != tt.wantStatus || e.problemCode(resp) != tt.wantCode {
			t.Fatalf("%s: %d %s, want %d %s", tt.name, resp.Status, resp.Body, tt.wantStatus, tt.wantCode)
		}
	}
	if row := e.row(s.ID); row.Status != "DETACHED" || len(e.connector.Closes()) != 0 {
		t.Fatalf("거절된 종료 요청이 session을 바꿈: %+v closes %v", row, e.connector.Closes())
	}
}

// Connector를 사용할 수 없어 CLOSE를 전달하지 못해도 Labbit의 TerminalSession은 ENDED이며 이후 attach할 수 없다.
func TestExplicitCloseWithoutConnectorStillEndsTheSession(t *testing.T) {
	e := newTerminalEnv(t)
	s := e.createSession(e.ownerCookie, e.fixture.LabInstanceID)
	e.connector.Stop()
	eventually(t, "Connector 연결 해제", 10*time.Second, func() bool {
		return e.stack.Registry.WithReadyRoute(e.fixture.ConnectorID, func(connector.Session, connector.Route) error { return nil }) != nil
	})

	if resp := e.request(http.MethodDelete, "/api/v1/terminal-sessions/"+s.ID, e.ownerCookie, ""); resp.Status != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s, want 204", resp.Status, resp.Body)
	}
	row := e.waitStatus(s.ID, "ENDED")
	if row.EndReason == nil || *row.EndReason != "SESSION_CLOSED" {
		t.Fatalf("row = %+v", row)
	}
	if code, _ := e.attachRejected(e.ownerCookie, s.ID, s.Token); code != "SESSION_EXPIRED" {
		t.Fatalf("종료 뒤 attach = %s", code)
	}
}

// 서비스 종료(graceful shutdown) 시 active TerminalSession은 SERVICE_RESTARTING으로 종료하고 Connector에 CLOSE를 요청한다.
func TestShutdownEndsActiveSessionsWithServiceRestarting(t *testing.T) {
	e := newTerminalEnv(t)
	s := e.createSession(e.ownerCookie, e.fixture.LabInstanceID)
	b, _ := e.attach(s)

	// Run이 하는 순서 그대로다. Shutdown이 시작되면 close를 호출하고, 그 뒤 shutdown이 session을 종료한 다음 Control connection을 닫는다.
	e.stack.close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.stack.shutdown(ctx); err != nil {
		t.Fatalf("shutdown() error = %v", err)
	}

	code, collected := b.closeCode()
	if code != 1012 || len(collected) != 1 || !strings.Contains(string(collected[0].data), `"SERVICE_RESTARTING"`) {
		t.Fatalf("Browser = close %d, %d messages", code, len(collected))
	}
	row := e.waitStatus(s.ID, "ENDED")
	if row.EndReason == nil || *row.EndReason != "SERVICE_RESTARTING" {
		t.Fatalf("row = %+v", row)
	}
	eventually(t, "TERMINAL_CLOSE", 5*time.Second, func() bool { return len(e.connector.Closes()) == 1 })
	if e.connector.Closes()[0].Reason != "SERVICE_RESTARTING" {
		t.Fatalf("TERMINAL_CLOSE = %+v", e.connector.Closes()[0])
	}
}

// Terminal INPUT/OUTPUT, attach token, 로그인 Session, Connector Credential은 DB와 log 어디에도 남지 않는다.
// "코드상 그렇게 보인다"가 아니라 실제 DB 내용과 log를 관찰한다.
func TestTerminalContentAndSecretsAreNeverPersisted(t *testing.T) {
	e := newTerminalEnv(t)
	s := e.createSession(e.ownerCookie, e.fixture.LabInstanceID)
	b, _ := e.attach(s)

	// INPUT/OUTPUT을 여러 번 주고받고 재접속, 종료까지 모든 경로를 지난다.
	for range 3 {
		b.write(websocket.BinaryMessage, []byte(terminalInputMarker))
		e.connector.Output(s.ID, []byte(terminalOutputMarker))
		if got := b.binary(); string(got) != terminalOutputMarker {
			t.Fatalf("OUTPUT = %q", got)
		}
	}
	eventually(t, "INPUT 도착", 5*time.Second, func() bool {
		return strings.Count(string(e.connector.Inputs(s.ID)), terminalInputMarker) == 3
	})
	b.close()
	e.waitStatus(s.ID, "DETACHED")
	e.connector.Output(s.ID, []byte(terminalOutputMarker+"-while-detached"))
	b2, _ := e.attach(s)
	b2.write(websocket.BinaryMessage, []byte(terminalInputMarker+"-after-reattach"))
	if resp := e.request(http.MethodDelete, "/api/v1/terminal-sessions/"+s.ID, e.ownerCookie, ""); resp.Status != http.StatusNoContent {
		t.Fatalf("DELETE = %d", resp.Status)
	}
	e.waitStatus(s.ID, "ENDED")
	time.Sleep(300 * time.Millisecond)

	secrets := map[string]string{
		"terminal INPUT":      terminalInputMarker,
		"terminal OUTPUT":     terminalOutputMarker,
		"attach token":        s.Token,
		"login session":       e.ownerCookie,
		"connector credental": terminaltest.Credential,
	}
	logs := e.logs.String()
	for name, secret := range secrets {
		if table, found := e.databaseContains(secret); found {
			t.Errorf("%s가 DB table %s에 저장됨", name, table)
		}
		if strings.Contains(logs, secret) {
			t.Errorf("%s가 log에 남음", name)
		}
	}
	// 대조군: 검사기가 동작한다. 저장되어야 하는 lifecycle metadata는 찾고, log에는 correlation ID가 있다.
	if _, found := e.databaseContains("SESSION_CLOSED"); !found {
		t.Error("DB 검사기가 저장된 종료 이유를 찾지 못함")
	}
	if !strings.Contains(logs, s.ID) || !strings.Contains(logs, e.fixture.LabInstanceID.String()) {
		t.Error("log에 terminal_session_id/lab_instance_id가 없음: 관측 가능한 correlation이 사라짐")
	}
	// Authorization header도 기록하지 않는다.
	if strings.Contains(strings.ToLower(logs), "bearer ") {
		t.Error("log에 Authorization header가 남음")
	}
	// 그리고 기대했던 연결 내용은 실제로 전달되었다(검사가 의미 있으려면 흐름이 실제로 일어나야 한다).
	if got := string(e.connector.Inputs(s.ID)); !strings.Contains(got, terminalInputMarker+"-after-reattach") {
		t.Errorf("재접속 뒤 INPUT이 전달되지 않음: %q", got)
	}
}

// Connector의 Terminal Data WSS transport가 끊겨도 PTY 종료가 아니다. TerminalSession은 ACTIVE로 남고 Browser INPUT은 버려지며 그 사실을
// 한 번 알린다. 같은 TerminalSession에 다시 attach하면 resumed=true, historyAvailable=false이고 끊긴 동안의 INPUT은 재생하지 않는다.
func TestDataChannelReconnectOverFullStack(t *testing.T) {
	e := newTerminalEnv(t)
	s := e.createSession(e.ownerCookie, e.fixture.LabInstanceID)
	b, _ := e.attach(s)
	e.waitStatus(s.ID, "ACTIVE")

	e.connector.DropData(s.ID)
	eventually(t, "data channel 해제", 5*time.Second, func() bool { return !e.stack.Relay.DataBound(s.ID) })
	if row := e.row(s.ID); row.Status != "ACTIVE" || len(e.connector.Closes()) != 0 {
		t.Fatalf("data 단절이 session을 바꿈: %+v closes %v", row, e.connector.Closes())
	}

	b.write(websocket.BinaryMessage, []byte(terminalInputMarker+"-while-down"))
	msg := b.json()
	payload, _ := msg["payload"].(map[string]any)
	if msg["type"] != "ERROR" || payload["code"] != "CONNECTOR_UNAVAILABLE" || payload["fatal"] == true {
		t.Fatalf("응답 = %v, want non-fatal ERROR CONNECTOR_UNAVAILABLE", msg)
	}

	if !e.connector.ReattachData(s.ID) {
		t.Fatal("Terminal Data WSS 재연결 실패")
	}
	attaches := e.connector.Attached(s.ID)
	if len(attaches) != 2 || attaches[1]["resumed"] != true || attaches[1]["historyAvailable"] != false {
		t.Fatalf("재연결 TERMINAL_DATA_ATTACHED = %v, want second resumed=true historyAvailable=false", attaches)
	}
	if !e.stack.Relay.DataBound(s.ID) {
		t.Fatal("재연결 뒤 data channel이 bind되지 않음")
	}

	// 같은 session/Browser로 I/O가 이어진다. 단절 중에 버린 INPUT은 재생하지 않는다.
	b.write(websocket.BinaryMessage, []byte("after-reconnect"))
	eventually(t, "재연결 뒤 INPUT", 5*time.Second, func() bool { return string(e.connector.Inputs(s.ID)) == "after-reconnect" })
	e.connector.Output(s.ID, []byte("fresh-output"))
	if got := b.binary(); string(got) != "fresh-output" {
		t.Fatalf("OUTPUT = %q", got)
	}
	if len(e.connector.Opens()) != 1 {
		t.Fatalf("재연결이 TERMINAL_OPEN을 다시 보냄: %d", len(e.connector.Opens()))
	}
	if row := e.row(s.ID); row.Status != "ACTIVE" {
		t.Fatalf("row = %+v", row)
	}
}
