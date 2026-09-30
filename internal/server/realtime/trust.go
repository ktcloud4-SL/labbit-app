package realtime

import (
	"sync"
	"sync/atomic"
)

// dataTrust는 Terminal Data WSS를 인증한 Connector trust(CredentialID, ConnectorID)별로 열린 connection을 추적한다.
//
// contracts/connector/README.md §2는 Credential이 revoke되면 현재 Control/Data 연결을 더 이상 신뢰하지 않고 종료하도록 요구한다.
// Control WSS는 자신의 heartbeat로 revoke를 관측하지만 Data WSS에는 heartbeat가 없고, Binary frame마다 저장소를 다시 조회할 수도
// 없다(realtime은 저장소를 모른다). 그래서 Credential 저장소 상태가 바뀐 것을 관측한 쪽(Connector Control)이 이 추적기에 알린다.
//
// Upgrade 중인 요청과의 경쟁을 닫는 것이 이 type의 핵심이다. 요청이 Credential을 인증한 뒤 connection으로 등록되기 전에
// revoke 통지가 지나가면 추적 목록에는 그 connection이 없어 revoke를 놓치고, 오래된 trust가 새 connection으로 살아난다.
// 그래서 인증을 시작할 때(begin) revoke 순번을 기억하고 등록할 때(admit) 그 사이에 같은 trust의 revoke가 있었는지 확인한다.
//
//   - 인증 시작 뒤에 통지된 revoke는 그 요청에게 "이미 지나간 사건"이다. 요청의 인증이 통지 전에 끝났을 수 있으므로 거절한다.
//   - 통지 뒤에 시작된 요청은 거절하지 않는다. Credential 저장소는 통지 전에 이미 revoke되어 있으므로 그 요청의 인증이 실패한다.
//     (통지는 저장소 상태를 바꾼 뒤에만 온다: Registry.RevokeCredential의 계약.)
//
// 표식(tombstone)은 인증 중인 요청이 있는 동안에만 필요하므로 그런 요청이 없으면 모두 버린다. 저장하는 것은 식별자와 순번뿐이다.
type dataTrust struct {
	mu sync.Mutex
	// seq는 지금까지 통지된 revoke의 수다. 인증을 시작한 요청이 자신이 본 시점을 기억하는 데 쓴다.
	seq uint64
	// inflight는 begin했지만 아직 admit(또는 release)하지 않은 요청 수다.
	inflight int
	// conns는 등록된 Data WSS connection이다. 한 process의 TerminalSession 수만큼이라 순회해도 작다.
	conns map[*dataConn]struct{}
	// revokedCredentials와 revokedConnectors는 inflight가 있는 동안의 revoke 표식이다. 값은 그 revoke의 순번이다.
	revokedCredentials map[string]uint64
	revokedConnectors  map[string]uint64
}

func newDataTrust() *dataTrust {
	return &dataTrust{
		conns:              make(map[*dataConn]struct{}),
		revokedCredentials: make(map[string]uint64),
		revokedConnectors:  make(map[string]uint64),
	}
}

// trustTicket은 Data WSS Upgrade 요청 하나가 인증을 시작한 시점이다. release는 여러 번 호출해도 안전하다.
type trustTicket struct {
	t        *dataTrust
	since    uint64
	released atomic.Bool
}

// begin은 Connector credential 인증을 시작하기 직전에 호출한다. 호출자는 admit이나 release를 반드시 호출해야 한다.
func (t *dataTrust) begin() *trustTicket {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.inflight++
	return &trustTicket{t: t, since: t.seq}
}

// release는 등록 없이 요청을 끝낸다(인증 실패, Upgrade 실패 등). admit 뒤에 호출해도 아무것도 하지 않는다.
func (k *trustTicket) release() {
	if !k.released.CompareAndSwap(false, true) {
		return
	}
	k.t.mu.Lock()
	defer k.t.mu.Unlock()
	k.t.doneLocked()
}

// doneLocked는 요청 하나가 끝났음을 기록하고, 인증 중인 요청이 더 없으면 revoke 표식을 모두 버린다. t.mu를 잡고 호출한다.
func (t *dataTrust) doneLocked() {
	t.inflight--
	if t.inflight == 0 {
		clear(t.revokedCredentials)
		clear(t.revokedConnectors)
	}
}

// admit은 d를 추적 목록에 넣고 ticket을 끝낸다. 인증을 시작한 뒤 d의 Credential이나 Connector가 revoke되었다면 등록하지 않고
// false를 반환한다. 그 경우 호출자가 connection을 종료해야 한다. ticket당 한 번만 호출한다.
func (t *dataTrust) admit(k *trustTicket, d *dataConn) bool {
	if !k.released.CompareAndSwap(false, true) {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	defer t.doneLocked()

	if t.revokedSinceLocked(k.since, d) {
		d.revoked.Store(true)
		return false
	}
	t.conns[d] = struct{}{}
	return true
}

func (t *dataTrust) revokedSinceLocked(since uint64, d *dataConn) bool {
	if seq, ok := t.revokedCredentials[d.credentialID]; ok && d.credentialID != "" && seq > since {
		return true
	}
	if seq, ok := t.revokedConnectors[d.connectorID]; ok && d.connectorID != "" && seq > since {
		return true
	}
	return false
}

// forget은 종료된 connection을 목록에서 뺀다. 여러 번 호출해도 안전하다.
func (t *dataTrust) forget(d *dataConn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.conns, d)
}

// revokeCredential은 credentialID로 인증된 connection을 revoked로 표시하고 돌려준다. 호출자가 각각 종료한다.
func (t *dataTrust) revokeCredential(credentialID string) []*dataConn {
	return t.revoke(func(d *dataConn) bool { return d.credentialID == credentialID },
		func() {
			if t.inflight > 0 {
				t.revokedCredentials[credentialID] = t.seq
			}
		})
}

// revokeConnector는 connectorID의 모든 connection을 revoked로 표시하고 돌려준다. 그 Connector의 어떤 Credential로 인증했든 같다.
func (t *dataTrust) revokeConnector(connectorID string) []*dataConn {
	return t.revoke(func(d *dataConn) bool { return d.connectorID == connectorID },
		func() {
			if t.inflight > 0 {
				t.revokedConnectors[connectorID] = t.seq
			}
		})
}

func (t *dataTrust) revoke(match func(*dataConn) bool, mark func()) []*dataConn {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seq++
	mark()
	var out []*dataConn
	for d := range t.conns {
		if match(d) {
			d.revoked.Store(true)
			out = append(out, d)
			delete(t.conns, d)
		}
	}
	return out
}

// size는 추적 중인 connection 수다(test용).
func (t *dataTrust) size() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.conns)
}
