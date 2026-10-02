//go:build integration

package app

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal/terminaltest"
)

// contracts/connector/README.md §2: Credential이 revoke되면 현재 Control/Data 연결을 더 이상 신뢰하지 않고 종료하며 이후 같은 Credential
// 인증을 거절한다. 이 파일은 실제 PostgreSQL, 실제 Control WSS(HEARTBEAT로 revoke 관측), 실제 Terminal Data WSS와 Browser relay로
// Data WSS가 Control과 함께 끝나는지 확인한다. Credential revoke use case가 아직 없으므로 DB row를 직접 revoke한다.

const (
	closeCredentialRevoked = 4001
	// 같은 Connector의 두 번째 유효한 Credential과 다른 Connector의 Credential이다. 실제 Secret이 아니다.
	secondCredential         = "terminaltest-second-credential-0b5e"
	otherConnectorCredential = "terminaltest-other-connector-credential-9a13"
)

// nextWithin은 시간 안에 message가 오지 않으면 ok=false를 반환한다. 오지 않아야 하는 message를 확인할 때 쓴다.
func (b *browser) nextWithin(d time.Duration) (browserMessage, bool) {
	b.start()
	select {
	case m, ok := <-b.msgs:
		if !ok {
			return browserMessage{err: io.EOF}, true
		}
		return m, true
	case <-time.After(d):
		return browserMessage{}, false
	}
}

// addCredential은 connectorID의 Connector에 유효한 Credential을 하나 더 만들고 그 ID를 반환한다.
func (e *terminalEnv) addCredential(connectorID uuid.UUID, credential string) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	digest := connector.CredentialDigest(credential)
	terminaltest.Exec(e.t, e.conn, `INSERT INTO connector_credentials (id, connector_id, credential_hash) VALUES ($1, $2, $3)`, id, connectorID, digest[:])
	return id
}

// addOtherConnector는 같은 Organization의 다른 Connector와 그 Credential을 만든다.
func (e *terminalEnv) addOtherConnector(credential string) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	terminaltest.Exec(e.t, e.conn, `INSERT INTO connectors (id, organization_id, name) VALUES ($1, $2, 'Other Connector')`, id, e.fixture.OrganizationID)
	e.addCredential(id, credential)
	return id
}

// revokeCredentialInDB는 저장소에서 Credential 하나를 revoke한다. raw Credential 대신 digest로 그 row만 고른다.
func (e *terminalEnv) revokeCredentialInDB(credential string) {
	e.t.Helper()
	digest := connector.CredentialDigest(credential)
	terminaltest.Exec(e.t, e.conn, `UPDATE connector_credentials SET revoked_at = now() WHERE credential_hash = $1 AND revoked_at IS NULL`, digest[:])
}

func (e *terminalEnv) dialData(credential string) (*websocket.Conn, *http.Response, error) {
	dialer := websocket.Dialer{Subprotocols: []string{realtime.DataSubprotocol}, HandshakeTimeout: 5 * time.Second}
	return dialer.Dial("ws"+strings.TrimPrefix(e.server.URL, "http")+realtime.DataPath,
		http.Header{"Authorization": []string{"Bearer " + credential}})
}

// attachDataConn은 TERMINAL_DATA_ATTACH를 보내 TERMINAL_DATA_ATTACHED payload를 받는다. test goroutine 밖에서 써도 되도록 오류를 반환한다.
func attachDataConn(conn *websocket.Conn, sessionID, labInstanceID string, generation int64) (map[string]any, error) {
	if err := conn.WriteJSON(map[string]any{
		"type": "TERMINAL_DATA_ATTACH", "messageId": uuid.NewString(), "sentAt": time.Now().UTC().Format(time.RFC3339Nano),
		"terminalSessionId": sessionID, "labInstanceId": labInstanceID, "generation": generation,
		"payload": map[string]any{"runtimeId": "runtime-revoke-test"},
	}); err != nil {
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer conn.SetReadDeadline(time.Time{})
	var msg struct {
		Type    string         `json:"type"`
		Payload map[string]any `json:"payload"`
	}
	if err := conn.ReadJSON(&msg); err != nil {
		return nil, err
	}
	if msg.Type != "TERMINAL_DATA_ATTACHED" {
		return nil, errors.New("TERMINAL_DATA_ATTACHED가 아님: " + msg.Type)
	}
	return msg.Payload, nil
}

// standalone은 Terminal Data WSS만 붙어 있는 (DB에 TerminalSession이 없는) Relay 세션이다. revoke가 다른 connection을 건드리지 않음을 확인하는 데 쓴다.
type standalone struct {
	id   string
	conn *websocket.Conn
}

func (e *terminalEnv) standaloneData(connectorID uuid.UUID, credential string) standalone {
	e.t.Helper()
	id, labID := uuid.NewString(), uuid.NewString()
	if err := e.stack.Relay.Expect(realtime.Expected{TerminalSessionID: id, ConnectorID: connectorID.String(), LabInstanceID: labID, Generation: 1}); err != nil {
		e.t.Fatalf("Expect() error = %v", err)
	}
	conn, _, err := e.dialData(credential)
	if err != nil {
		e.t.Fatalf("Data WSS 연결 실패: %v", err)
	}
	e.t.Cleanup(func() { _ = conn.Close() })
	if _, err := attachDataConn(conn, id, labID, 1); err != nil {
		e.t.Fatalf("Data attach 실패: %v", err)
	}
	if !e.stack.Relay.DataBound(id) {
		e.t.Fatal("Data WSS가 bind되지 않음")
	}
	return standalone{id: id, conn: conn}
}

// staysOpen은 잠시 동안 close frame이 오지 않고 Relay가 아직 bind하고 있음을 확인한다.
func (e *terminalEnv) staysOpen(s standalone, what string) {
	e.t.Helper()
	_ = s.conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	_, _, err := s.conn.ReadMessage()
	var netErr interface{ Timeout() bool }
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		e.t.Fatalf("%s: connection이 종료됨: %v", what, err)
	}
	if !e.stack.Relay.DataBound(s.id) {
		e.t.Fatalf("%s: 더 이상 bind되어 있지 않음", what)
	}
}

// 요구한 sequence 전체다.
//
//	valid Control WSS → valid TerminalSession → Terminal Data WSS → Browser attach → Binary relay
//	→ Credential을 DB에서 revoke → 유효한 HEARTBEAT → Control 4001 → 같은 Credential로 인증한 active Data WSS도 종료
//	→ 이후 기존 Data WSS로 PTY bytes relay 불가 → 같은 Credential의 새 Data Upgrade 401
//
// 다른 Connector, 같은 Connector의 다른 유효한 Credential의 Data WSS는 유지되고, TerminalSession은 끝나지 않으며 유효한 Credential로
// 다시 attach하면 같은 TerminalSession을 이어 간다.
func TestCredentialRevokeDetectedOnHeartbeatTerminatesControlAndActiveDataWSS(t *testing.T) {
	e := newTerminalEnv(t)
	f := e.fixture
	e.addCredential(f.ConnectorID, secondCredential)
	otherConnectorID := e.addOtherConnector(otherConnectorCredential)

	s := e.createSession(e.ownerCookie, f.LabInstanceID)
	b, _ := e.attach(s)
	e.waitStatus(s.ID, "ACTIVE")

	// revoke 대상이 아닌 Data WSS 둘. 다른 Connector와, 같은 Connector의 다른 유효한 Credential이다.
	unrelatedConnector := e.standaloneData(otherConnectorID, otherConnectorCredential)
	sameConnectorOtherCredential := e.standaloneData(f.ConnectorID, secondCredential)

	// revoke 전에는 양방향 relay가 정상이다.
	input := []byte(terminalInputMarker + "-before")
	b.write(websocket.BinaryMessage, input)
	eventually(t, "revoke 전 PTY INPUT", 5*time.Second, func() bool { return string(e.connector.Inputs(s.ID)) == string(input) })
	e.connector.Output(s.ID, []byte(terminalOutputMarker+"-before"))
	if got := b.binary(); string(got) != terminalOutputMarker+"-before" {
		t.Fatalf("revoke 전 OUTPUT = %q", got)
	}

	// 저장소에서 이 Connector의 (Control과 Data에 쓰던) Credential을 revoke한다. Backend는 다음 유효한 HEARTBEAT에서 관측한다.
	e.revokeCredentialInDB(terminaltest.Credential)
	e.connector.Heartbeat()

	if code, ok := e.connector.ControlClosed(10 * time.Second); !ok || code != closeCredentialRevoked {
		t.Fatalf("Control WSS 종료 = code %d, closed %v, want 4001", code, ok)
	}
	if code, ok := e.connector.DataClosed(s.ID, 10*time.Second); !ok || code != closeCredentialRevoked {
		t.Fatalf("같은 Credential로 인증한 Data WSS 종료 = code %d, closed %v, want 4001", code, ok)
	}
	if e.stack.Relay.DataBound(s.ID) {
		t.Fatal("revoke된 Data WSS가 여전히 TerminalSession의 data channel임")
	}

	// 종료된 connection으로는 PTY bytes가 오가지 않는다.
	_ = e.connector.TryOutput(s.ID, []byte(terminalOutputMarker+"-after-revoke")) // 서버가 종료한 connection이라 전송 자체가 실패할 수도 있다.
	b.write(websocket.BinaryMessage, []byte(terminalInputMarker+"-after-revoke"))
	msg := b.json()
	if msg["type"] != "ERROR" || msg["payload"].(map[string]any)["code"] != "CONNECTOR_UNAVAILABLE" {
		t.Fatalf("revoke 뒤 Browser INPUT 응답 = %v, want ERROR CONNECTOR_UNAVAILABLE(OUTPUT을 받으면 안 됨)", msg)
	}
	if m, got := b.nextWithin(300 * time.Millisecond); got {
		t.Fatalf("revoke된 connection의 OUTPUT이 Browser에 전달됨: %+v", m)
	}
	if got := string(e.connector.Inputs(s.ID)); got != string(input) {
		t.Fatalf("revoke 뒤 INPUT이 Connector에 전달됨: %q, want %q", got, input)
	}

	// 같은 Credential의 새 Upgrade는 Control이든 Data든 저장소 인증에서 401이다.
	if _, resp, err := e.dialData(terminaltest.Credential); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoke된 Credential의 Data Upgrade = %v, %v, want 401", resp, err)
	}

	// revoke 대상이 아닌 connection은 유지된다.
	e.staysOpen(unrelatedConnector, "다른 Connector의 Data WSS")
	e.staysOpen(sameConnectorOtherCredential, "같은 Connector의 다른 Credential Data WSS")

	// Data transport의 trust 상실이 TerminalSession의 제품 lifecycle 종료는 아니다. PTY와 Browser attachment는 그대로다.
	if row := e.row(s.ID); row.Status != "ACTIVE" || row.EndedAt != nil {
		t.Fatalf("revoke 뒤 row = %+v, want ACTIVE(TerminalSession은 끝나지 않음)", row)
	}

	// 같은 Connector가 유효한 다른 Credential로 같은 TerminalSession에 다시 붙으면 이어 간다(resumed=true, history 없음).
	conn, _, err := e.dialData(secondCredential)
	if err != nil {
		t.Fatalf("유효한 Credential의 Data WSS 연결 실패: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	attached, err := attachDataConn(conn, s.ID, f.LabInstanceID.String(), 1)
	if err != nil {
		t.Fatalf("유효한 Credential로 재attach 실패: %v", err)
	}
	if attached["resumed"] != true || attached["historyAvailable"] != false {
		t.Fatalf("재attach payload = %v, want resumed=true historyAvailable=false", attached)
	}
	b.write(websocket.BinaryMessage, []byte("input-after-reconnect"))
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if kind, data, err := conn.ReadMessage(); err != nil || kind != websocket.BinaryMessage || string(data) != "input-after-reconnect" {
		t.Fatalf("재attach한 Data WSS가 받은 INPUT = kind %d %q, %v", kind, data, err)
	}
	_ = conn.SetReadDeadline(time.Time{})
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("output-after-reconnect")); err != nil {
		t.Fatal(err)
	}
	if got := b.binary(); string(got) != "output-after-reconnect" {
		t.Fatalf("재attach 뒤 OUTPUT = %q", got)
	}

	// Credential 원문은 log에 남지 않는다.
	for _, secret := range []string{terminaltest.Credential, secondCredential, otherConnectorCredential} {
		if strings.Contains(e.logs.String(), secret) {
			t.Fatalf("log에 Connector Credential이 남음: %q", secret)
		}
	}
}

// revoke를 관측하는 다른 경로(Credential revoke use case가 저장소를 바꾼 뒤 호출하는 lifecycle hook)도 같은 primitive이므로
// HEARTBEAT 없이도 Control과 Data가 함께 끝난다.
func TestCredentialRevokeHookTerminatesControlAndActiveDataWSS(t *testing.T) {
	e := newTerminalEnv(t)
	f := e.fixture
	s := e.createSession(e.ownerCookie, f.LabInstanceID)
	e.attach(s)
	e.waitStatus(s.ID, "ACTIVE")

	var credentialID uuid.UUID
	digest := connector.CredentialDigest(terminaltest.Credential)
	if err := e.conn.QueryRow(t.Context(), `SELECT id FROM connector_credentials WHERE credential_hash = $1`, digest[:]).Scan(&credentialID); err != nil {
		t.Fatal(err)
	}
	e.revokeCredentialInDB(terminaltest.Credential)
	if n := e.stack.Registry.RevokeCredential(credentialID); n != 1 {
		t.Fatalf("RevokeCredential() = %d, want 1", n)
	}

	if code, ok := e.connector.ControlClosed(10 * time.Second); !ok || code != closeCredentialRevoked {
		t.Fatalf("Control WSS 종료 = code %d, closed %v, want 4001", code, ok)
	}
	if code, ok := e.connector.DataClosed(s.ID, 10*time.Second); !ok || code != closeCredentialRevoked {
		t.Fatalf("Data WSS 종료 = code %d, closed %v, want 4001", code, ok)
	}
	if e.stack.Relay.DataBound(s.ID) {
		t.Fatal("revoke된 Data WSS가 여전히 bind됨")
	}
}

// revoke와 Data WSS 재연결이 경쟁해도 오래된 trust가 살아나지 않는다. Credential이 revoke되기 전에 인증한 요청이 revoke 통지 뒤에
// connection이 되더라도 종료되어야 하고, 통지 뒤의 요청은 401이다. -race에서 안전해야 한다.
func TestRevokeRacingWithDataReconnectNeverLeavesTheRevokedCredentialBound(t *testing.T) {
	e := newTerminalEnv(t)
	f := e.fixture
	s := e.createSession(e.ownerCookie, f.LabInstanceID)
	e.attach(s)
	e.waitStatus(s.ID, "ACTIVE")

	var (
		mu       sync.Mutex
		conns    []*websocket.Conn
		rejected int
		stop     = make(chan struct{})
		wg       sync.WaitGroup
	)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				conn, resp, err := e.dialData(terminaltest.Credential)
				if err != nil {
					if resp != nil && resp.StatusCode == http.StatusUnauthorized {
						mu.Lock()
						rejected++
						mu.Unlock()
					}
					time.Sleep(2 * time.Millisecond)
					continue
				}
				mu.Lock()
				conns = append(conns, conn)
				mu.Unlock()
				// 붙거나 종료되는 것 모두 허용한다. 최종 상태만 본다.
				_, _ = attachDataConn(conn, s.ID, f.LabInstanceID.String(), 1)
			}
		}()
	}

	time.Sleep(100 * time.Millisecond) // 재연결이 한동안 오가게 둔다.
	e.revokeCredentialInDB(terminaltest.Credential)
	e.connector.Heartbeat()
	if code, ok := e.connector.ControlClosed(10 * time.Second); !ok || code != closeCredentialRevoked {
		t.Fatalf("Control WSS 종료 = code %d, closed %v, want 4001", code, ok)
	}
	// Control 종료 뒤에도 잠시 더 경쟁시킨다. 이 사이의 Upgrade는 모두 401이어야 한다.
	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(conns) == 0 {
		t.Fatal("revoke 전에 재연결한 Data WSS가 없음(경쟁 조건이 만들어지지 않음)")
	}
	if rejected == 0 {
		t.Fatal("revoke 뒤의 Upgrade가 401로 거절되지 않음")
	}
	// 모든 connection은 끝났다. 새 connection에 교체된 것은 1000, revoke된 것은 4001이고, 끝나지 않은 connection은 없다.
	for i, conn := range conns {
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		var code int
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				var closeErr *websocket.CloseError
				if !errors.As(err, &closeErr) {
					t.Fatalf("Data WSS %d가 종료되지 않음(살아 있는 오래된 trust): %v", i, err)
				}
				code = closeErr.Code
				break
			}
		}
		if code != closeCredentialRevoked && code != websocket.CloseNormalClosure {
			t.Fatalf("Data WSS %d close code = %d, want 4001(revoke) 또는 1000(새 connection으로 교체)", i, code)
		}
		_ = conn.Close()
	}
	if e.stack.Relay.DataBound(s.ID) {
		t.Fatal("revoke된 Credential의 Data WSS가 bind된 채 남음")
	}
	time.Sleep(300 * time.Millisecond)
	if e.stack.Relay.DataBound(s.ID) {
		t.Fatal("revoke 뒤 늦게 revoke된 Credential의 Data WSS가 bind됨")
	}
}
