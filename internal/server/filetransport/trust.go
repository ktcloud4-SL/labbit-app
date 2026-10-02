package filetransport

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// dataConn은 인증과 Upgrade를 마친 File Data WSS connection 하나다.
type dataConn struct {
	ws           *websocket.Conn
	credentialID uuid.UUID
	connectorID  uuid.UUID

	// revoked는 인증한 Credential이나 Connector가 revoke되어 이 connection을 더 이상 신뢰하지 않음이다.
	revoked atomic.Bool
	// req는 이 connection이 bind된 요청이다. attach 전에는 nil이다.
	req atomic.Pointer[request]
}

// closeNow는 close frame을 보내고 connection을 닫는다. 어느 goroutine에서 호출해도 안전하다(gorilla는 Close/WriteControl을
// 다른 read/write와 동시에 호출하는 것을 허용한다). 여러 번 호출해도 안전하다.
func (d *dataConn) closeNow(code int, text string) {
	_ = d.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, text), time.Now().Add(controlWriteTimeout))
	_ = d.ws.Close()
}

// dataTrust는 File Data WSS를 인증한 Connector trust(CredentialID, ConnectorID)별로 열린 connection을 추적한다.
//
// contracts/connector/README.md §2는 Credential이 revoke되면 현재 Control/Data 연결을 더 이상 신뢰하지 않고 종료하도록 요구한다.
// Data WSS에는 heartbeat가 없고 frame마다 저장소를 다시 조회하지도 않으므로 revoke를 관측한 쪽(Connector Control의 Registry)이
// 이 추적기에 알린다.
//
// Upgrade 중인 요청과의 경쟁을 닫는 것이 핵심이다. 요청이 Credential을 인증한 뒤 connection으로 등록되기 전에 revoke 통지가
// 지나가면 추적 목록에는 그 connection이 없어 revoke를 놓친다. 그래서 인증을 시작할 때(begin) revoke 순번을 기억하고 등록할 때(admit)
// 그 사이에 같은 trust의 revoke가 있었는지 확인한다. 표식은 인증 중인 요청이 있는 동안에만 필요하므로 없으면 모두 버린다.
type dataTrust struct {
	mu                 sync.Mutex
	seq                uint64
	inflight           int
	conns              map[*dataConn]struct{}
	revokedCredentials map[uuid.UUID]uint64
	revokedConnectors  map[uuid.UUID]uint64
}

func newDataTrust() *dataTrust {
	return &dataTrust{
		conns:              make(map[*dataConn]struct{}),
		revokedCredentials: make(map[uuid.UUID]uint64),
		revokedConnectors:  make(map[uuid.UUID]uint64),
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

	if seq, ok := t.revokedCredentials[d.credentialID]; ok && seq > k.since {
		d.revoked.Store(true)
		return false
	}
	if seq, ok := t.revokedConnectors[d.connectorID]; ok && seq > k.since {
		d.revoked.Store(true)
		return false
	}
	t.conns[d] = struct{}{}
	return true
}

// forget은 종료된 connection을 목록에서 뺀다. 여러 번 호출해도 안전하다.
func (t *dataTrust) forget(d *dataConn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.conns, d)
}

// revokeCredential은 credentialID로 인증된 connection을 revoked로 표시하고 돌려준다. 호출자가 각각 종료한다.
func (t *dataTrust) revokeCredential(credentialID uuid.UUID) []*dataConn {
	return t.revoke(func(d *dataConn) bool { return d.credentialID == credentialID }, func() {
		if t.inflight > 0 {
			t.revokedCredentials[credentialID] = t.seq
		}
	})
}

// revokeConnector는 connectorID의 모든 connection을 revoked로 표시하고 돌려준다. 그 Connector의 어떤 Credential로 인증했든 같다.
func (t *dataTrust) revokeConnector(connectorID uuid.UUID) []*dataConn {
	return t.revoke(func(d *dataConn) bool { return d.connectorID == connectorID }, func() {
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
