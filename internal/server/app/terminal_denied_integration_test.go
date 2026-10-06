//go:build integration

package app

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal/terminaltest"
)

// requireNoSideEffects는 거절된 요청이 어떤 side effect도 만들지 않았음을 확인한다. TerminalSession row, Connector에 전달된 OPEN,
// Relay 등록, pending OPEN이 모두 없어야 한다.
func (e *terminalEnv) requireNoSideEffects() {
	e.t.Helper()
	if n := e.sessionCount(); n != 0 {
		e.t.Fatalf("terminal_sessions row = %d, want 0: 거절된 요청이 session을 만듦", n)
	}
	if opens := e.connector.Opens(); len(opens) != 0 {
		e.t.Fatalf("Connector가 TERMINAL_OPEN을 받음: %+v", opens)
	}
	if n := e.stack.Relay.Sessions(); n != 0 {
		e.t.Fatalf("Relay Sessions = %d, want 0", n)
	}
	if n := e.stack.Router.PendingTerminalOpens(e.fixture.ConnectorID); n != 0 {
		e.t.Fatalf("pending TERMINAL_OPEN = %d, want 0", n)
	}
}

func (e *terminalEnv) problemCode(resp apiResponse) string {
	e.t.Helper()
	code, _ := resp.json(e.t)["code"].(string)
	return code
}

// 현재 사용자, LabInstance 소유, 현재 ClassMembership, LabInstance lifecycle, 현재 generation의 대상 VM을 모두 만족하지 않으면
// Connector나 Relay, DB에 어떤 side effect도 만들기 전에 거절한다.
func TestCreateTerminalSessionIsDeniedBeforeAnySideEffect(t *testing.T) {
	type testCase struct {
		name       string
		cookie     func(*terminalEnv) string
		labID      func(*terminalEnv) string
		body       string
		setup      func(*terminalEnv)
		mods       []func(*http.Request)
		wantStatus int
		wantCode   string
	}
	owner := func(e *terminalEnv) string { return e.ownerCookie }
	ownLab := func(e *terminalEnv) string { return e.fixture.LabInstanceID.String() }
	status := func(s string) func(*terminalEnv) {
		return func(e *terminalEnv) {
			terminaltest.Exec(t, e.conn, `UPDATE lab_instances SET status = $2 WHERE id = $1`, e.fixture.LabInstanceID, s)
		}
	}
	target := func(name string) string {
		return `{"targetVmKey":"` + name + `","cols":80,"rows":24}`
	}

	tests := []testCase{
		{name: "no login session", cookie: func(*terminalEnv) string { return "" }, labID: ownLab, wantStatus: 401, wantCode: "unauthenticated"},
		{name: "expired or unknown login session", cookie: func(*terminalEnv) string { return "unknown-session" }, labID: ownLab, wantStatus: 401, wantCode: "unauthenticated"},
		{name: "another student's lab instance", cookie: owner, labID: func(e *terminalEnv) string { return e.fixture.PeerLabInstanceID.String() }, wantStatus: 403, wantCode: "forbidden"},
		{name: "peer opens the owner's lab instance", cookie: func(e *terminalEnv) string { return e.peerCookie }, labID: ownLab, wantStatus: 403, wantCode: "forbidden"},
		{
			// 강사가 학생 LabInstance의 Terminal을 직접 여는 기능은 MVP 범위가 아니다.
			name:   "instructor opens a student's lab instance",
			cookie: func(e *terminalEnv) string { return terminaltest.LoginSession(t, e.conn, e.fixture.InstructorID) },
			labID:  ownLab, wantStatus: 403, wantCode: "forbidden",
		},
		{name: "user of another organization", cookie: func(e *terminalEnv) string { return e.foreignCookie }, labID: ownLab, wantStatus: 403, wantCode: "forbidden"},
		{name: "user without membership in the class", cookie: func(e *terminalEnv) string { return e.outsiderCookie }, labID: ownLab, wantStatus: 403, wantCode: "forbidden"},
		{
			// 소유자여도 현재 ClassMembership이 없으면 거절한다(권한은 생성 시점의 현재 상태다).
			name: "owner whose class membership was removed", cookie: owner, labID: ownLab, wantStatus: 403, wantCode: "forbidden",
			setup: func(e *terminalEnv) {
				terminaltest.Exec(t, e.conn, `DELETE FROM class_memberships WHERE class_id = $1 AND user_id = $2`, e.fixture.ClassID, e.fixture.OwnerID)
			},
		},
		{name: "unknown lab instance", cookie: owner, labID: func(*terminalEnv) string { return uuid.NewString() }, wantStatus: 404, wantCode: "not_found"},
		{name: "malformed lab instance id", cookie: owner, labID: func(*terminalEnv) string { return "not-a-uuid" }, wantStatus: 404, wantCode: "not_found"},
		{name: "non-canonical lab instance id", cookie: owner, labID: func(e *terminalEnv) string { return strings.ToUpper(e.fixture.LabInstanceID.String()) }, wantStatus: 404, wantCode: "not_found"},
		{name: "lab instance PENDING", cookie: owner, labID: ownLab, setup: status("PENDING"), wantStatus: 409, wantCode: "lab_instance_not_ready"},
		{name: "lab instance PROVISIONING", cookie: owner, labID: ownLab, setup: status("PROVISIONING"), wantStatus: 409, wantCode: "lab_instance_not_ready"},
		{name: "lab instance ERROR", cookie: owner, labID: ownLab, setup: status("ERROR"), wantStatus: 409, wantCode: "lab_instance_not_ready"},
		{name: "lab instance DELETING", cookie: owner, labID: ownLab, setup: status("DELETING"), wantStatus: 409, wantCode: "lab_instance_not_ready"},
		{name: "lab instance in an unknown future state", cookie: owner, labID: ownLab, setup: status("SOMETHING_NEW"), wantStatus: 409, wantCode: "lab_instance_not_ready"},
		{name: "unknown target vm", cookie: owner, labID: ownLab, body: target("nope"), wantStatus: 422, wantCode: "invalid_terminal_target"},
		{name: "a network with the same name is not a server", cookie: owner, labID: ownLab, body: target("net"), wantStatus: 422, wantCode: "invalid_terminal_target"},
		{name: "another user's vm key", cookie: owner, labID: ownLab, body: target("peer-only"), wantStatus: 422, wantCode: "invalid_terminal_target"},
		{
			// 현재 generation에 PRESENT SERVER ProviderResource가 있어도 immutable CreationSnapshot의 VM이 아니면 target이 아니다.
			// Browser가 GET terminal-targets에 없는 임의의 logical name을 보낼 수 없다.
			name: "present provider resource that is not in the creation snapshot", cookie: owner, labID: ownLab, body: target("hidden-server"), wantStatus: 422, wantCode: "invalid_terminal_target",
			setup: func(e *terminalEnv) {
				e.fixture.AddResource(t, e.conn, e.fixture.LabInstanceID, 1, "SERVER", "hidden-server", "PRESENT")
			},
		},
		{name: "deleted provider resource", cookie: owner, labID: ownLab, body: target("retired"), wantStatus: 409, wantCode: "terminal_target_unavailable"},
		{name: "missing provider resource", cookie: owner, labID: ownLab, body: target("ghost"), wantStatus: 409, wantCode: "terminal_target_unavailable"},
		{
			// Reset으로 generation이 올랐지만 새 generation에 아직 VM이 없다. 이전 generation의 리소스는 현재 대상이 아니다.
			// workspace는 CreationSnapshot의 VM이므로 잘못된 입력(422)이 아니라 지금 사용할 수 없는 target(409)이다.
			name: "resource only exists in a previous generation", cookie: owner, labID: ownLab, wantStatus: 409, wantCode: "terminal_target_unavailable",
			setup: func(e *terminalEnv) {
				terminaltest.Exec(t, e.conn, `UPDATE lab_instances SET generation = 2 WHERE id = $1`, e.fixture.LabInstanceID)
			},
		},
		{
			// 같은 generation에 같은 논리 이름의 PRESENT VM이 둘이면 어느 쪽인지 알 수 없다. 임의로 고르지 않고 내부 오류로 막는다(fail closed).
			name: "ambiguous target", cookie: owner, labID: ownLab, body: target("db"), wantStatus: 500, wantCode: "internal_error",
			setup: func(e *terminalEnv) {
				e.fixture.AddResource(t, e.conn, e.fixture.LabInstanceID, 1, "SERVER", "db", "PRESENT")
			},
		},
		{name: "wrong origin", cookie: owner, labID: ownLab, mods: []func(*http.Request){func(r *http.Request) { r.Header.Set("Origin", "https://evil.test") }}, wantStatus: 403, wantCode: "csrf_rejected"},
		{name: "missing origin", cookie: owner, labID: ownLab, mods: []func(*http.Request){func(r *http.Request) { r.Header.Del("Origin") }}, wantStatus: 403, wantCode: "csrf_rejected"},
		{name: "request body is not valid", cookie: owner, labID: ownLab, body: `{"targetVmKey":"workspace"}`, wantStatus: 400, wantCode: "invalid_request"},
		{name: "request tries to choose the connector", cookie: owner, labID: ownLab, body: `{"targetVmKey":"workspace","cols":80,"rows":24,"connectorId":"x"}`, wantStatus: 400, wantCode: "invalid_request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newTerminalEnv(t)
			if tt.setup != nil {
				tt.setup(e)
			}
			body := tt.body
			if body == "" {
				body = createBody
			}
			resp := e.request(http.MethodPost, "/api/v1/lab-instances/"+tt.labID(e)+"/terminal-sessions", tt.cookie(e), body, tt.mods...)
			if resp.Status != tt.wantStatus || e.problemCode(resp) != tt.wantCode {
				t.Fatalf("응답 = %d %s, want %d %s", resp.Status, resp.Body, tt.wantStatus, tt.wantCode)
			}
			if resp.Header.Get("Content-Type") != "application/problem+json" {
				t.Fatalf("Content-Type = %q", resp.Header.Get("Content-Type"))
			}
			// 오류 응답에 token이나 내부 오류 문구가 없다.
			if strings.Contains(string(resp.Body), "sessionToken") || strings.Contains(strings.ToLower(string(resp.Body)), "sqlstate") {
				t.Fatalf("오류 응답에 내부 정보가 있음: %s", resp.Body)
			}
			e.requireNoSideEffects()
		})
	}
}

// Reset 뒤 현재 generation의 VM이 있으면 생성할 수 있고 Connector는 새 generation과 새 Provider Server ID를 받는다.
func TestCreateTerminalSessionUsesTheCurrentGeneration(t *testing.T) {
	e := newTerminalEnv(t)
	f := e.fixture
	oldServer := f.Servers["workspace"]
	generation, current := f.BumpGeneration(t, e.conn, f.LabInstanceID)

	s := e.createSession(e.ownerCookie, f.LabInstanceID)
	if s.Generation != generation {
		t.Fatalf("generation = %d, want %d", s.Generation, generation)
	}
	open := e.connector.Opens()[0]
	if open.Generation != generation || open.ProviderServerID != current.ProviderID || open.ProviderServerID == oldServer.ProviderID {
		t.Fatalf("TERMINAL_OPEN = %+v, want generation %d and the current provider server", open, generation)
	}
	row := e.row(s.ID)
	if row.Generation != generation {
		t.Fatalf("row.generation = %d, want %d", row.Generation, generation)
	}
	// TerminalSession은 현재 generation의 ProviderResource를 가리킨다(FK로도 보장된다).
	var resourceID uuid.UUID
	if err := e.conn.QueryRow(t.Context(), `SELECT provider_resource_id FROM terminal_sessions WHERE id = $1`, s.ID).Scan(&resourceID); err != nil || resourceID != current.ResourceID {
		t.Fatalf("provider_resource_id = %v, %v, want %v", resourceID, err, current.ResourceID)
	}
}

// 한 사용자는 자기 LabInstance의 서로 다른 VM에 각각 TerminalSession을 열 수 있다. Workspace VM 하나로 강제하지 않는다.
func TestCreateTerminalSessionForEachVM(t *testing.T) {
	e := newTerminalEnv(t)
	f := e.fixture

	workspace := e.createSession(e.ownerCookie, f.LabInstanceID)
	resp := e.request(http.MethodPost, e.createPath(f.LabInstanceID), e.ownerCookie, `{"targetVmKey":"db","cols":80,"rows":24}`)
	if resp.Status != http.StatusCreated {
		t.Fatalf("db VM = %d: %s", resp.Status, resp.Body)
	}
	db := resp.json(t)["id"].(string)
	if db == workspace.ID {
		t.Fatal("서로 다른 TerminalSession이어야 한다")
	}
	opens := e.connector.Opens()
	if len(opens) != 2 || opens[0].ProviderServerID != f.Servers["workspace"].ProviderID || opens[1].ProviderServerID != f.Servers["db"].ProviderID {
		t.Fatalf("TERMINAL_OPEN = %+v", opens)
	}
	if got := e.connector.Attached(workspace.ID); len(got) != 1 {
		t.Fatalf("workspace data channel = %v", got)
	}
	if got := e.connector.Attached(db); len(got) != 1 {
		t.Fatalf("db data channel = %v", got)
	}
}

// Connector가 PTY를 열지 못하면 TerminalSession은 성공 상태로 남지 않는다. ENDED로 기록하고 Relay 등록과 pending을 지우며
// PTY가 만들어졌을 수 있으면 CLOSE를 요청한다. 실패 뒤 다시 시도하면 새 TerminalSession으로 성공할 수 있다.
func TestConnectorOpenFailuresLeaveNoSession(t *testing.T) {
	type failure struct {
		name string
		// arrange는 Connector를 실패 모드로 만든다.
		arrange func(*terminalEnv)
		// timeout이면 응답이 올 때까지 fake clock으로 OPEN 시간 초과를 일으킨다.
		timeout     bool
		wantStatus  int
		wantCode    string
		wantClose   bool
		wantOpens   int
		wantDataLnk bool
	}
	tests := []failure{
		{
			name: "connector offline", wantStatus: 503, wantCode: "connector_unavailable",
			arrange: func(e *terminalEnv) {
				e.connector.Stop()
				eventually(t, "Connector 연결 해제", 10*time.Second, func() bool {
					return e.stack.Registry.WithReadyRoute(e.fixture.ConnectorID, func(connector.Session, connector.Route) error { return nil }) != nil
				})
			},
		},
		{name: "connector reports FAILED", arrange: func(e *terminalEnv) { e.connector.SetMode(terminaltest.OpenFails) }, wantStatus: 503, wantCode: "terminal_open_failed", wantClose: true, wantOpens: 1},
		{name: "connector never answers", arrange: func(e *terminalEnv) { e.connector.SetMode(terminaltest.OpenIgnored) }, timeout: true, wantStatus: 503, wantCode: "terminal_open_failed", wantClose: true, wantOpens: 1},
		{
			// OPEN_RESULT SUCCEEDED라는 주장만 믿지 않는다. 같은 TerminalSession의 Terminal Data WSS가 bind되어 있어야 한다.
			name: "success claimed without a data channel", arrange: func(e *terminalEnv) { e.connector.SetMode(terminaltest.OpenSucceedsWithoutData) },
			wantStatus: 503, wantCode: "terminal_open_failed", wantClose: true, wantOpens: 1,
		},
		{
			name: "result with the wrong generation is not trusted", arrange: func(e *terminalEnv) { e.connector.SetMode(terminaltest.OpenWrongCorrelation) },
			timeout: true, wantStatus: 503, wantCode: "terminal_open_failed", wantClose: true, wantOpens: 1, wantDataLnk: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newTerminalEnv(t)
			tt.arrange(e)

			respCh := make(chan apiResponse, 1)
			go func() {
				respCh <- e.request(http.MethodPost, e.createPath(e.fixture.LabInstanceID), e.ownerCookie, createBody)
			}()
			if tt.timeout {
				eventually(t, "TERMINAL_OPEN 수신", 10*time.Second, func() bool { return len(e.connector.Opens()) == 1 })
				time.Sleep(100 * time.Millisecond)
				e.clock.Advance(30 * time.Second) // OPEN_RESULT를 기다리는 기본 시간
			}
			var resp apiResponse
			select {
			case resp = <-respCh:
			case <-time.After(20 * time.Second):
				t.Fatal("생성 요청이 끝나지 않음")
			}
			if resp.Status != tt.wantStatus || e.problemCode(resp) != tt.wantCode {
				t.Fatalf("응답 = %d %s, want %d %s", resp.Status, resp.Body, tt.wantStatus, tt.wantCode)
			}
			if strings.Contains(string(resp.Body), "sessionToken") || strings.Contains(string(resp.Body), "SSH_UNREACHABLE") {
				t.Fatalf("오류 응답이 token이나 Connector 오류 문구를 포함함: %s", resp.Body)
			}

			// 성공 상태로 남은 TerminalSession이 없다.
			if n := e.sessionCount(); n != 1 {
				t.Fatalf("terminal_sessions row = %d, want 1(ENDED 기록)", n)
			}
			var live int
			if err := e.conn.QueryRow(t.Context(), `SELECT count(*) FROM terminal_sessions WHERE status <> 'ENDED'`).Scan(&live); err != nil || live != 0 {
				t.Fatalf("ENDED가 아닌 TerminalSession = %d, %v, want 0 (ghost session)", live, err)
			}
			var reason string
			if err := e.conn.QueryRow(t.Context(), `SELECT end_reason FROM terminal_sessions`).Scan(&reason); err != nil || reason != "OPEN_FAILED" {
				t.Fatalf("end_reason = %q, %v, want OPEN_FAILED", reason, err)
			}
			if n := e.stack.Relay.Sessions(); n != 0 {
				t.Fatalf("Relay Sessions = %d, want 0", n)
			}
			if n := e.stack.Router.PendingTerminalOpens(e.fixture.ConnectorID); n != 0 {
				t.Fatalf("pending TERMINAL_OPEN = %d, want 0", n)
			}
			if got := len(e.connector.Opens()); got != tt.wantOpens {
				t.Fatalf("Connector가 받은 TERMINAL_OPEN = %d, want %d", got, tt.wantOpens)
			}
			if tt.name == "connector reports FAILED" {
				open := e.connector.Opens()[0]
				requireLogFields(t, e.logEvent("Connector TERMINAL_OPEN 실패 보고", open.TerminalSessionID), map[string]any{
					"connector_id": e.fixture.ConnectorID.String(), "lab_instance_id": open.LabInstanceID,
					"request_id": open.RequestID, "error_code": "SSH_UNREACHABLE", "trace_id": nil,
				})
			}
			if tt.wantClose {
				eventually(t, "TERMINAL_CLOSE", 10*time.Second, func() bool { return len(e.connector.Closes()) == 1 })
				if closeMsg := e.connector.Closes()[0]; closeMsg.Reason != "SESSION_CLOSED" || closeMsg.Generation != 1 {
					t.Fatalf("TERMINAL_CLOSE = %+v", closeMsg)
				}
			} else if len(e.connector.Closes()) != 0 {
				t.Fatalf("OPEN을 보내지 않았는데 CLOSE를 보냄: %+v", e.connector.Closes())
			}
			// 연결이 끊긴 경우를 제외하면 같은 Connector로 다시 시도해 성공할 수 있다(새 TerminalSession).
			if tt.wantOpens > 0 {
				e.connector.SetMode(terminaltest.OpenSucceeds)
				retry := e.createSession(e.ownerCookie, e.fixture.LabInstanceID)
				if e.row(retry.ID).Status != "DETACHED" {
					t.Fatalf("재시도 session = %+v", e.row(retry.ID))
				}
			}
		})
	}
}

// 요청이 취소되어도(Browser가 기다리다 떠났다) 생성 중이던 TerminalSession은 정리된다. ghost session이 남지 않는다.
func TestCanceledCreateIsCleanedUp(t *testing.T) {
	e := newTerminalEnv(t)
	e.connector.SetMode(terminaltest.OpenIgnored)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan apiResponse, 1)
	go func() {
		done <- e.requestCtx(ctx, http.MethodPost, e.createPath(e.fixture.LabInstanceID), e.ownerCookie, createBody)
	}()
	eventually(t, "TERMINAL_OPEN 수신", 10*time.Second, func() bool { return len(e.connector.Opens()) == 1 })
	cancel()
	<-done

	eventually(t, "생성 중이던 session 정리", 10*time.Second, func() bool {
		var live int
		_ = e.conn.QueryRow(t.Context(), `SELECT count(*) FROM terminal_sessions WHERE status <> 'ENDED'`).Scan(&live)
		return e.sessionCount() == 1 && live == 0
	})
	eventually(t, "TERMINAL_CLOSE", 10*time.Second, func() bool { return len(e.connector.Closes()) == 1 })
	if e.stack.Relay.Sessions() != 0 || e.stack.Router.PendingTerminalOpens(e.fixture.ConnectorID) != 0 {
		t.Fatalf("Relay %d pending %d, want 0 0", e.stack.Relay.Sessions(), e.stack.Router.PendingTerminalOpens(e.fixture.ConnectorID))
	}
}
