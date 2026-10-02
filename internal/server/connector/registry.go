package connector

import (
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"
)

// CloseReason은 Registry가 Session의 connection에 종료를 요청하는 이유다.
// WebSocket close code 같은 wire 표현은 transport가 정한다(contracts/connector/README.md §15).
type CloseReason int

const (
	// CloseReplaced는 같은 Connector의 새 Control connection이 current가 되어 이전 connection을 종료한다.
	CloseReplaced CloseReason = iota + 1
	// CloseRevoked는 Connector Credential 또는 Connector가 revoke되어 현재 connection을 더 이상 신뢰하지 않는다.
	CloseRevoked
)

// Route는 protocol-ready(HELLO_ACK 완료)인 Session 하나의 control writer다. data는 JSON text message 하나이며
// 직렬화된 write와 close의 순서는 transport가 소유한다. 종료가 시작된 connection이라 아무것도 쓰지 않았다면
// ErrRouteClosed를 반환해야 한다. 그 외의 오류는 일부가 전송되었을 수도 있는 실패다.
type Route func(data []byte) error

// ErrRouteClosed는 Route가 종료 중인 connection에 아무것도 쓰지 않고 거절했음을 나타낸다.
var ErrRouteClosed = errors.New("connector: route closed before write")

// ErrConnectorUnavailable은 지금 Connector에 message를 보낼 수 있는 Control connection이 없음을 나타낸다.
// 호출자는 이 오류를 이유로 같은 command를 다른 connection으로 자동 재전송하지 않는다.
var ErrConnectorUnavailable = errors.New("connector: Control connection을 사용할 수 없음")

var (
	// ErrNotConnected는 Connector의 current Control connection이 없음을 나타낸다.
	ErrNotConnected = fmt.Errorf("%w: current connection 없음", ErrConnectorUnavailable)
	// ErrNotReady는 current connection이 있지만 HELLO_ACK 전이라 protocol-ready가 아님을 나타낸다.
	// 교체된 새 connection이 HELLO_ACK를 마치기 전에도 이전 connection으로 보내지 않으므로 이 오류가 된다.
	ErrNotReady = fmt.Errorf("%w: HELLO_ACK 완료 전", ErrConnectorUnavailable)
	// ErrConnectionClosing은 current connection이 종료 중이라 아무것도 쓰지 못했음을 나타낸다.
	ErrConnectionClosing = fmt.Errorf("%w: connection 종료 중", ErrConnectorUnavailable)
)

// Session은 인증과 WebSocket Upgrade를 마친 Control connection 하나를 식별한다.
// 같은 Connector의 connection이 다시 붙어도 Session.ID는 서로 다르다.
// Session은 connection의 소유자일 뿐 protocol-ready가 아니다. HELLO_ACK가 끝나기 전의 connection도 포함한다.
// protocol-ready 여부는 Registration.MarkReady와 Registry.WithReadyRoute가 별도로 표현한다.
type Session struct {
	ID           uuid.UUID
	ConnectorID  uuid.UUID
	CredentialID uuid.UUID
}

// RevokeObserver는 Registry가 처리한 revoke를 Control 밖의 connection에도 알리는 경계다. Terminal Data WSS가 대표적이다.
// contracts/connector/README.md §2는 Credential이 revoke되면 Control과 Data 연결을 모두 종료하도록 요구하는데, Data WSS에는 자신의
// heartbeat가 없어 저장소 상태를 스스로 관측하지 못한다. 그래서 revoke를 관측하는 경로(Registry.RevokeCredential/RevokeConnector)가
// 이 observer로 통지한다.
//
// 통지는 저장소 상태가 이미 revoke로 바뀐 뒤에만 온다(RevokeCredential의 계약). 그래서 통지 뒤의 새 인증은 저장소에서 거절된다.
// 구현은 Registry lock 밖에서 호출되며 오래 막히면 안 된다. 같은 revoke가 여러 번 통지되어도 안전해야 한다.
// 통지는 Control Session이 없어도 온다(Control은 끊겼지만 Data WSS만 남은 경우).
type RevokeObserver interface {
	CredentialRevoked(credentialID uuid.UUID)
	ConnectorRevoked(connectorID uuid.UUID)
}

// Registry는 인증과 WebSocket Upgrade를 마친 Control connection을 Connector별 current Session 하나로 소유한다.
// 여러 connection goroutine이 동시에 사용해도 안전하다.
//
//   - 같은 Connector의 새 Session이 등록되면 새 Session이 current가 되고 이전 Session의 종료를 요청한다.
//     이 교체는 새 connection이 HELLO를 보내기 전에 일어난다(contracts/connector/README.md §4).
//   - current는 소유자이지 protocol-ready(HELLO_ACK 완료)가 아니다. Current는 message를 보내는 경로를 노출하지 않는다.
//     command routing은 WithReadyRoute만 사용한다. 이 경로는 그 Session이 MarkReady를 마친 뒤에만 열리고,
//     교체·revoke가 시작되면(Register/Revoke가 반환하기 전에) 닫히며, 새 Session은 자신의 MarkReady 전까지 열리지 않는다.
//     따라서 교체된 이전 Session과 HELLO_ACK 전의 새 Session 어느 쪽으로도 command가 나가지 않는다.
//   - Session 종료와 release는 그 Session만 다룬다. 이미 교체된 Session의 늦은 release는 새 Session을 지우지 않는다.
//   - 종료 요청(closeFn)은 항상 Registry lock 밖에서 호출한다. lock을 잡은 채 network I/O를 하지 않는다.
//
// Registry는 프로세스 안의 ephemeral connection 소유 상태다. 영속 관측값(connectors.last_seen_at)과 분리한다.
type Registry struct {
	mu       sync.RWMutex
	current  map[uuid.UUID]*entry
	observer RevokeObserver
}

// entry는 등록된 Session과 그 종료 방법이다.
type entry struct {
	session Session
	closeFn func(CloseReason)

	// mu는 retired와, IfCurrent가 실행 중인 fn을 보호한다. 교체·revoke는 진행 중인 fn이 끝난 뒤에 retired를 표시하므로
	// 그 이후에는 이 Session이 fn을 시작하지 못한다.
	mu      sync.Mutex
	retired bool

	// routeMu는 route와 sealed를 보호한다. WithReadyRoute는 RLock을 잡은 채 fn(pending 등록과 write 포함)을 실행하고,
	// retire는 Lock으로 진행 중인 fn이 끝나기를 기다린 뒤 route를 제거한다. 그래서 retire가 끝난 뒤에는 이 Session의
	// connection에 새 write가 시작되지 않는다. IfCurrent의 mu와 분리해 heartbeat 기록이 command 전송을 막지 않고,
	// IfCurrent 안(event sink)에서 전송을 시작해도 self-deadlock이 없다.
	routeMu sync.RWMutex
	route   Route
	sealed  bool
}

func NewRegistry() *Registry {
	return &Registry{current: make(map[uuid.UUID]*entry)}
}

// Registration은 Register가 돌려준 Session 하나에 대한 handle이다.
type Registration struct {
	registry *Registry
	entry    *entry
}

// Register는 principal의 Connector에 새 current Session을 만들고 그 handle을 반환한다.
// 같은 Connector의 이전 Session이 있으면 이 호출이 끝나기 전에 그 Session을 retire하고 CloseReplaced로 종료를 요청한다.
// closeFn은 이 Session의 connection에 종료를 요청한다. 교체나 revoke 때 Registry가 호출하며, 이미 종료 중이어도 호출될 수
// 있으므로 멱등이어야 하고, 호출자를 오래 막지 않는 것이 좋다. nil이면 종료를 요청하지 않는다.
func (r *Registry) Register(principal Principal, closeFn func(CloseReason)) *Registration {
	e := &entry{
		session: Session{ID: uuid.New(), ConnectorID: principal.ConnectorID, CredentialID: principal.CredentialID},
		closeFn: closeFn,
	}

	r.mu.Lock()
	previous := r.current[principal.ConnectorID]
	r.current[principal.ConnectorID] = e
	r.mu.Unlock()

	if previous != nil {
		previous.retire(CloseReplaced)
	}
	return &Registration{registry: r, entry: e}
}

// SetRevokeObserver는 revoke를 Control 밖의 connection에도 알릴 observer를 정한다. nil이면 통지하지 않는다.
// 조립 시점에 서비스를 시작하기 전에 한 번 호출한다.
func (r *Registry) SetRevokeObserver(o RevokeObserver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observer = o
}

func (r *Registry) revokeObserver() RevokeObserver {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.observer
}

// Current는 connectorID의 현재 Session을 반환한다. 없으면 false다.
func (r *Registry) Current(connectorID uuid.UUID) (Session, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.current[connectorID]
	if !ok {
		return Session{}, false
	}
	return e.session, true
}

// RevokeCredential은 credentialID로 인증된 current Session을 registry에서 제거하고 CloseRevoked로 종료를 요청한 뒤,
// 같은 Credential로 인증된 Control 밖의 connection(Terminal Data WSS)도 observer로 종료하게 한다.
// 종료를 요청한 Control Session 수를 반환한다. 이미 교체된 Session은 current가 아니므로 대상이 아니다.
//
// Credential revoke를 관측하는 모든 경로의 공통 primitive다. 저장소 상태가 revoke로 바뀐 것을 확인한 뒤에만 호출한다.
//   - Control의 HEARTBEAT 기록이 ErrUnauthenticated를 돌려준 경우(현재 구현이 revoke를 관측하는 경로)
//   - Credential revoke use case가 저장소 상태를 바꾼 뒤 호출하는 lifecycle hook(raw Credential이 필요 없다)
func (r *Registry) RevokeCredential(credentialID uuid.UUID) int {
	r.mu.Lock()
	var revoked []*entry
	for connectorID, e := range r.current {
		if e.session.CredentialID == credentialID {
			revoked = append(revoked, e)
			delete(r.current, connectorID)
		}
	}
	r.mu.Unlock()

	for _, e := range revoked {
		e.retire(CloseRevoked)
	}
	// Control Session이 없어도 통지한다. Data WSS만 남아 있을 수 있다.
	if o := r.revokeObserver(); o != nil {
		o.CredentialRevoked(credentialID)
	}
	return len(revoked)
}

// RevokeConnector는 connectorID의 current Session을 registry에서 제거하고 CloseRevoked로 종료를 요청한 뒤,
// 그 Connector의 Control 밖의 connection(Terminal Data WSS)도 observer로 종료하게 한다.
// Control Session의 종료를 요청했으면 true다. Connector revoke use case가 호출하는 lifecycle hook이다.
func (r *Registry) RevokeConnector(connectorID uuid.UUID) bool {
	r.mu.Lock()
	e, ok := r.current[connectorID]
	delete(r.current, connectorID)
	r.mu.Unlock()

	if ok {
		e.retire(CloseRevoked)
	}
	if o := r.revokeObserver(); o != nil {
		o.ConnectorRevoked(connectorID)
	}
	return ok
}

// retire는 진행 중인 IfCurrent가 끝나기를 기다린 뒤 이 Session을 더 이상 current가 아닌 것으로 표시하고 종료를 요청한다.
// 종료 요청은 entry lock 밖에서 한다.
func (e *entry) retire(reason CloseReason) {
	e.mu.Lock()
	e.retired = true
	e.mu.Unlock()

	// 진행 중인 command write가 끝난 뒤에 route를 닫는다. 이 뒤로는 이 Session에 새 write가 시작되지 않는다.
	e.routeMu.Lock()
	e.route = nil
	e.sealed = true
	e.routeMu.Unlock()

	if e.closeFn != nil {
		e.closeFn(reason)
	}
}

// Session은 이 handle의 Session이다.
func (g *Registration) Session() Session { return g.entry.session }

// IfCurrent는 이 Session이 아직 current이면 fn을 실행하고 true를 반환한다.
// 이미 교체되었거나 revoke되었다면 fn을 호출하지 않고 false를 반환한다.
// fn이 실행되는 동안 교체와 revoke는 fn의 완료를 기다린다. 따라서 Register가 반환한 뒤에는 이전 Session의 fn이 시작되지 않는다.
// fn은 다른 Session의 Register/Revoke를 기다리면 안 된다(예: DB 기록 한 번).
func (g *Registration) IfCurrent(fn func() error) (bool, error) {
	g.entry.mu.Lock()
	defer g.entry.mu.Unlock()
	if g.entry.retired {
		return false, nil
	}
	return true, fn()
}

// MarkReady는 이 Session이 protocol-ready(HELLO_ACK 전송 완료)가 되었음을 알리고 이 Session의 control writer를 등록한다.
// 이미 교체·revoke된 Session이면 등록하지 않고 false를 반환한다. 성공하면 WithReadyRoute가 이 Session의 route를 사용할 수 있다.
func (g *Registration) MarkReady(route Route) bool {
	if route == nil {
		return false
	}
	e := g.entry
	e.routeMu.Lock()
	defer e.routeMu.Unlock()
	if e.sealed {
		return false
	}
	e.route = route
	return true
}

// Release는 이 Session이 아직 current일 때만 registry에서 제거한다. 여러 번 호출해도 안전하다.
// 이미 다른 Session이 current가 되었다면 그것을 지우지 않는다.
func (g *Registration) Release() {
	r := g.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current[g.entry.session.ConnectorID] == g.entry {
		delete(r.current, g.entry.session.ConnectorID)
	}
}

// WithReadyRoute는 connectorID의 current Session이 protocol-ready일 때만 fn을 실행한다.
// current가 없으면 ErrNotConnected, current가 HELLO_ACK 전이거나 이미 교체·revoke되었다면 ErrNotReady를 반환하고
// fn을 호출하지 않는다. 그 밖에는 fn의 결과를 반환한다.
//
// fn에는 그 exact Session과 route를 준다. fn이 실행되는 동안 그 Session의 교체와 revoke는 fn의 완료를 기다린다.
// 그래서 Register/Revoke가 반환한 뒤에는 그 Session의 route로 새 write가 시작되지 않는다. fn은 pending 등록과 write 한 번처럼
// 짧아야 하며(write는 transport의 write timeout이 상한이다), 다른 Session의 Register/Revoke를 기다리면 안 된다.
func (r *Registry) WithReadyRoute(connectorID uuid.UUID, fn func(Session, Route) error) error {
	r.mu.RLock()
	e := r.current[connectorID]
	r.mu.RUnlock()
	if e == nil {
		return ErrNotConnected
	}

	e.routeMu.RLock()
	defer e.routeMu.RUnlock()
	if e.route == nil {
		return ErrNotReady
	}
	return fn(e.session, e.route)
}
