package realtime_test

import (
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
)

// contracts/connector/README.md §2: Credential이 revoke되면 현재 Control/Data 연결을 더 이상 신뢰하지 않고 종료하며 이후 같은 Credential
// 인증을 거절한다. 이 파일은 Data WSS 쪽을 검증한다. Control이 revoke를 관측하는 경로(HEARTBEAT)와 이 경계를 잇는 것은
// connector.Registry의 observer이며 그 연결은 connector, connectorwss, app package의 test가 검증한다.

const closeCredentialRevoked = 4001

// liveOn은 connectorID의 Connector가 cred로 Data WSS를 붙이고 Browser도 attach한 TerminalSession이다.
type liveOn struct {
	s session
	d *dataPeer
	b *browserPeer
}

func (e *env) liveWith(connectorID, cred string) liveOn {
	e.t.Helper()
	s := e.newSessionOn(connectorID)
	d := e.connectDataWith(s, cred)
	if err := e.relay.Activate(s.ID, e.clock.Now().Add(realtime.DefaultGrace)); err != nil {
		e.t.Fatalf("Activate() error = %v", err)
	}
	b := e.connectBrowser(s)
	d.readJSON() // attach 때 Browser의 크기를 PTY에 반영하는 TERMINAL_DATA_RESIZE다.
	return liveOn{s: s, d: d, b: b}
}

// relays는 Browser와 Connector 사이에 Binary가 양방향으로 흐르는지 확인한다.
func (l liveOn) relays(t *testing.T, tag string) {
	t.Helper()
	l.b.writeBinary([]byte("input-" + tag))
	if got := l.d.readBinary(); string(got) != "input-"+tag {
		t.Fatalf("INPUT = %q, want %q", got, "input-"+tag)
	}
	l.d.writeBinary([]byte("output-" + tag))
	if got := l.b.readBinary(); string(got) != "output-"+tag {
		t.Fatalf("OUTPUT = %q, want %q", got, "output-"+tag)
	}
}

// Credential이 revoke되면 그 Credential로 인증된 Data WSS만 4001로 종료된다. 같은 Connector의 다른 유효한 Credential과 다른 Connector의
// connection은 그대로이고, revoke된 connection으로는 INPUT도 OUTPUT도 흐르지 않으며 TerminalSession은 끝나지 않는다.
func TestRevokeCredentialTerminatesOnlyDataWSSAuthenticatedByThatCredential(t *testing.T) {
	e := newEnv(t)
	revoked := e.liveWith(connectorID1, connectorCred)
	sameConnector := e.liveWith(connectorID1, connectorCred1b)
	otherConnector := e.liveWith(connectorID2, connectorCred2)
	for name, l := range map[string]liveOn{"revoked": revoked, "same connector": sameConnector, "other connector": otherConnector} {
		l.relays(t, "before-"+name)
	}

	e.connectors.revoke(connectorCred)
	if n := e.relay.RevokeConnectorCredential(credentialID1); n != 1 {
		t.Fatalf("RevokeConnectorCredential() = %d, want 1", n)
	}

	// 종료된 connection에서 온 OUTPUT은 Browser에 전달되지 않는다. 종료가 시작된 뒤에 보낸다.
	_ = revoked.d.conn.WriteMessage(websocket.BinaryMessage, []byte("late-output-from-revoked"))
	// Browser의 INPUT도 종료된 connection으로 가지 않는다. data channel이 없으므로 Browser는 CONNECTOR_UNAVAILABLE을 받는다.
	revoked.b.writeBinary([]byte("late-input-to-revoked"))
	msg := revoked.b.readJSON()
	if msg["type"] != "ERROR" || payload(t, msg)["code"] != "CONNECTOR_UNAVAILABLE" || payload(t, msg)["fatal"] == true {
		t.Fatalf("Browser 응답 = %v, want non-fatal ERROR CONNECTOR_UNAVAILABLE", msg)
	}
	revoked.b.expectNoMessage(200 * time.Millisecond)

	all, code := revoked.d.collect()
	if code != closeCredentialRevoked {
		t.Fatalf("revoke된 Data WSS close code = %d, want %d", code, closeCredentialRevoked)
	}
	for _, m := range all {
		if m.kind == websocket.BinaryMessage {
			t.Fatalf("revoke된 Data WSS가 종료 전에 Binary를 받음: %q", m.data)
		}
	}
	if e.relay.DataBound(revoked.s.ID) {
		t.Fatal("revoke된 connection이 여전히 TerminalSession의 data channel임")
	}

	// TerminalSession 자체는 끝나지 않는다. Data transport의 trust 상실일 뿐 PTY/Browser 세션의 제품 lifecycle 종료가 아니다.
	if got := e.relay.Sessions(); got != 3 {
		t.Fatalf("Sessions() = %d, want 3(revoke가 TerminalSession을 종료하면 안 됨)", got)
	}
	if _, _, closed, ended := e.control.snapshot(); len(closed) != 0 || len(ended) != 0 {
		t.Fatalf("Control 종료 기록 closed=%v ended=%v, want 없음", closed, ended)
	}

	// revoke 대상이 아닌 connection은 영향이 없다.
	sameConnector.relays(t, "after-same-connector")
	otherConnector.relays(t, "after-other-connector")
}

// revoke된 Data WSS 대신 같은 Connector가 유효한 Credential로 다시 attach하면 같은 TerminalSession의 새 data channel이 된다.
// Browser는 그대로 붙어 있고, 같은 Credential의 새 Upgrade는 저장소 인증에서 401로 거절된다.
func TestRevokedDataWSSCanBeReplacedByValidCredentialButNotByTheRevokedOne(t *testing.T) {
	e := newEnv(t)
	l := e.liveWith(connectorID1, connectorCred)
	l.relays(t, "before")

	e.connectors.revoke(connectorCred)
	e.relay.RevokeConnectorCredential(credentialID1)
	if code := l.d.expectClose(); code != closeCredentialRevoked {
		t.Fatalf("close code = %d, want %d", code, closeCredentialRevoked)
	}

	if _, resp, err := e.dialData(connectorCred); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoke된 Credential의 새 Upgrade = %v, %v, want 401", resp, err)
	}

	d2 := e.connectDataWith(l.s, connectorCred1b)
	attached := payload(t, d2.attached)
	if attached["resumed"] != true || attached["historyAvailable"] != false {
		t.Fatalf("재접속 TERMINAL_DATA_ATTACHED payload = %v, want resumed=true historyAvailable=false", attached)
	}
	l.d = d2
	l.relays(t, "after-reconnect")
}

// TERMINAL_DATA_ATTACH를 기다리는 동안(Upgrade 뒤, attach 전)에도 Credential이 revoke되면 종료된다.
func TestRevokeClosesDataWSSThatIsStillWaitingForAttach(t *testing.T) {
	e := newEnv(t)
	s := e.newSession()
	p, _, err := e.dialData(connectorCred)
	if err != nil {
		t.Fatalf("Data WSS dial error = %v", err)
	}

	e.connectors.revoke(connectorCred)
	if n := e.relay.RevokeConnectorCredential(credentialID1); n != 1 {
		t.Fatalf("RevokeConnectorCredential() = %d, want 1(attach 전 connection도 대상)", n)
	}
	if code := p.expectClose(); code != closeCredentialRevoked {
		t.Fatalf("close code = %d, want %d", code, closeCredentialRevoked)
	}
	if e.relay.DataBound(s.ID) {
		t.Fatal("attach하지 않은 connection이 bind됨")
	}
}

// 인증은 성공했지만 connection으로 등록되기 전에 revoke 통지가 지나가면 추적 목록에는 그 connection이 없어 revoke를 놓친다.
// 오래된 trust가 새 connection으로 살아나지 않도록 그 connection은 등록 시점에 거절되고 4001로 종료되어야 한다.
func TestRevokeDuringUpgradeDoesNotLetStaleTrustOpenAConnection(t *testing.T) {
	e := newEnv(t)
	s := e.newSession()

	stalled := e.connectors.stallNextAuth()
	type dialed struct {
		p    *peer
		resp *http.Response
		err  error
	}
	done := make(chan dialed, 1)
	go func() {
		p, resp, err := e.dialData(connectorCred)
		done <- dialed{p, resp, err}
	}()
	<-stalled.entered // 인증은 성공했고 아직 Upgrade와 등록 전이다.

	e.connectors.revoke(connectorCred)
	if n := e.relay.RevokeConnectorCredential(credentialID1); n != 0 {
		t.Fatalf("RevokeConnectorCredential() = %d, want 0(아직 등록된 connection이 없다)", n)
	}
	close(stalled.release)

	d := <-done
	if d.err != nil {
		t.Fatalf("Upgrade는 인증이 끝난 요청이므로 성공한다: %v", d.err)
	}
	if code := d.p.expectClose(); code != closeCredentialRevoked {
		t.Fatalf("close code = %d, want %d(오래된 trust로 열린 connection)", code, closeCredentialRevoked)
	}
	if e.relay.DataBound(s.ID) {
		t.Fatal("오래된 trust의 connection이 TerminalSession에 bind됨")
	}
}

// revoke 통지가 진행 중인 다른 Credential의 Upgrade를 거절하면 안 된다. 같은 Connector의 다른 Credential도 마찬가지다.
func TestRevokeDuringUpgradeDoesNotRejectOtherCredentials(t *testing.T) {
	e := newEnv(t)
	for _, tt := range []struct {
		name, credential, connectorID string
	}{
		{"same connector other credential", connectorCred1b, connectorID1},
		{"other connector", connectorCred2, connectorID2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := e.newSessionOn(tt.connectorID)
			stalled := e.connectors.stallNextAuth()
			type dialed struct {
				p   *peer
				err error
			}
			done := make(chan dialed, 1)
			go func() {
				p, _, err := e.dialData(tt.credential)
				done <- dialed{p, err}
			}()
			<-stalled.entered

			e.relay.RevokeConnectorCredential(credentialID1) // 이 요청의 Credential이 아니다.
			close(stalled.release)

			d := <-done
			if d.err != nil {
				t.Fatalf("다른 Credential의 Upgrade가 revoke 때문에 실패함: %v", d.err)
			}
			d.p.writeText(dataAttachMessage(t, s, nil))
			if attached := d.p.readJSON(); attached["type"] != "TERMINAL_DATA_ATTACHED" {
				t.Fatalf("attach 응답 = %v, want TERMINAL_DATA_ATTACHED", attached)
			}
			if !e.relay.DataBound(s.ID) {
				t.Fatal("다른 Credential의 connection이 bind되지 않음")
			}
		})
	}
}

// Connector 자체가 revoke되면 어떤 Credential로 인증했든 그 Connector의 Data WSS가 모두 종료된다. 다른 Connector는 영향이 없다.
func TestRevokeConnectorTerminatesEveryCredentialOfThatConnector(t *testing.T) {
	e := newEnv(t)
	first := e.liveWith(connectorID1, connectorCred)
	second := e.liveWith(connectorID1, connectorCred1b)
	other := e.liveWith(connectorID2, connectorCred2)

	if n := e.relay.RevokeConnector(connectorID1); n != 2 {
		t.Fatalf("RevokeConnector() = %d, want 2", n)
	}
	for name, l := range map[string]liveOn{"first": first, "second": second} {
		if code := l.d.expectClose(); code != closeCredentialRevoked {
			t.Fatalf("%s close code = %d, want %d", name, code, closeCredentialRevoked)
		}
	}
	other.relays(t, "unrelated-connector")
}

// 식별자가 비었거나 아무 connection과도 맞지 않으면 아무것도 종료하지 않는다. 비어 있는 CredentialID를 가진 connection과도 맞지 않는다.
func TestRevokeWithUnknownOrEmptyIDsTerminatesNothing(t *testing.T) {
	e := newEnv(t)
	l := e.liveWith(connectorID1, connectorCred)
	for _, id := range []string{"", "c9999999-9999-4999-8999-999999999999", connectorID1} {
		if n := e.relay.RevokeConnectorCredential(id); n != 0 {
			t.Fatalf("RevokeConnectorCredential(%q) = %d, want 0", id, n)
		}
	}
	for _, id := range []string{"", "99999999-9999-4999-8999-999999999999", credentialID1} {
		if n := e.relay.RevokeConnector(id); n != 0 {
			t.Fatalf("RevokeConnector(%q) = %d, want 0", id, n)
		}
	}
	l.relays(t, "unaffected")
}

// drainUntilClose는 close frame이나 오류까지 읽고 close code를 반환한다. test goroutine 밖에서 써도 되도록 Fatal을 호출하지 않는다.
func drainUntilClose(p *peer) (code int, err error) {
	for {
		_, err := p.next(readTimeout)
		if err == nil {
			continue
		}
		var closeErr *websocket.CloseError
		if errors.As(err, &closeErr) {
			return closeErr.Code, nil
		}
		return 0, err
	}
}

// revoke와 Data WSS 연결이 동시에 일어나도 revoke 이후에 살아 있는 connection이 없다. 저장소 revoke 뒤의 Upgrade는 401로 거절되고,
// 그 전에 인증한 connection은 등록 전이든 등록 뒤든 4001로 종료된다. -race에서 data race가 없어야 한다.
func TestConcurrentRevokeAndConnectNeverLeavesAnOldTrustAlive(t *testing.T) {
	const clients = 8
	for round := 0; round < 10; round++ {
		e := newEnv(t)
		// revoke 전에 이미 붙어 있는 connection이다. revoke가 먼저 실행되어 경쟁하는 client가 모두 401이 되어도 "등록된 뒤 종료" 경로를 검증한다.
		established := []*dataPeer{e.connectData(e.newSession()), e.connectData(e.newSession())}
		sessions := make([]session, clients)
		attachMessages := make([][]byte, clients)
		for i := range sessions {
			sessions[i] = e.newSession()
			attachMessages[i] = dataAttachMessage(t, sessions[i], nil)
		}

		type outcome struct {
			rejected bool
			code     int
			err      error
		}
		outcomes := make([]outcome, clients)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := range sessions {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				p, resp, err := e.dialData(connectorCred)
				if err != nil {
					outcomes[i] = outcome{rejected: resp != nil && resp.StatusCode == http.StatusUnauthorized, err: err}
					return
				}
				// 닫혔을 수 있으므로 쓰기 오류는 무시한다. 어느 쪽이든 종료되어야 한다.
				_ = p.conn.WriteMessage(websocket.TextMessage, attachMessages[i])
				code, err := drainUntilClose(p)
				outcomes[i] = outcome{code: code, err: err}
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			// 운영과 같은 순서다. 저장소 상태를 먼저 바꾸고 그 뒤에 통지한다.
			e.connectors.revoke(connectorCred)
			e.relay.RevokeConnectorCredential(credentialID1)
		}()
		close(start)
		wg.Wait()

		for i, o := range outcomes {
			switch {
			case o.rejected: // 저장소 revoke 뒤에 인증을 시도해 401
			case o.err == nil && o.code == closeCredentialRevoked: // 인증은 통과했고 4001로 종료
			default:
				t.Fatalf("round %d client %d: rejected=%v code=%d err=%v, want 401 또는 close 4001(살아 있는 오래된 trust)", round, i, o.rejected, o.code, o.err)
			}
		}
		for i, d := range established {
			if code, err := drainUntilClose(d.peer); err != nil || code != closeCredentialRevoked {
				t.Fatalf("round %d: 미리 붙어 있던 connection %d: code=%d err=%v, want close 4001", round, i, code, err)
			}
		}
		for i, s := range sessions {
			if e.relay.DataBound(s.ID) {
				t.Fatalf("round %d client %d: revoke된 Credential의 connection이 bind된 채 남음", round, i)
			}
		}
	}
}
