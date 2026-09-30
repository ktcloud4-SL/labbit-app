package connector

import (
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

// Session은 인증과 WebSocket Upgrade를 마친 Control connection 하나를 식별한다.
// 같은 Connector의 connection이 다시 붙어도 Session.ID는 서로 다르다.
// Session은 connection의 소유자일 뿐 protocol-ready가 아니다. HELLO_ACK가 끝나기 전의 connection도 포함한다.
type Session struct {
	ID           uuid.UUID
	ConnectorID  uuid.UUID
	CredentialID uuid.UUID
}

// Registry는 인증과 WebSocket Upgrade를 마친 Control connection을 Connector별 current Session 하나로 소유한다.
// 여러 connection goroutine이 동시에 사용해도 안전하다.
//
//   - 같은 Connector의 새 Session이 등록되면 새 Session이 current가 되고 이전 Session의 종료를 요청한다.
//     이 교체는 새 connection이 HELLO를 보내기 전에 일어난다(contracts/connector/README.md §4).
//   - current는 소유자이지 protocol-ready(HELLO_ACK 완료)가 아니다. Registry는 message를 보내는 경로를 노출하지 않으며,
//     command routing은 HELLO_ACK 이후에만 Session의 connection을 사용해야 한다.
//   - Session 종료와 release는 그 Session만 다룬다. 이미 교체된 Session의 늦은 release는 새 Session을 지우지 않는다.
//   - 종료 요청(closeFn)은 항상 Registry lock 밖에서 호출한다. lock을 잡은 채 network I/O를 하지 않는다.
//
// Registry는 프로세스 안의 ephemeral connection 소유 상태다. 영속 관측값(connectors.last_seen_at)과 분리한다.
type Registry struct {
	mu      sync.RWMutex
	current map[uuid.UUID]*entry
}

// entry는 등록된 Session과 그 종료 방법이다.
type entry struct {
	session Session
	closeFn func(CloseReason)

	// mu는 retired와, IfCurrent가 실행 중인 fn을 보호한다. 교체·revoke는 진행 중인 fn이 끝난 뒤에 retired를 표시하므로
	// 그 이후에는 이 Session이 fn을 시작하지 못한다.
	mu      sync.Mutex
	retired bool
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

// RevokeCredential은 credentialID로 인증된 current Session을 registry에서 제거하고 CloseRevoked로 종료를 요청한다.
// 종료를 요청한 Session 수를 반환한다. 이미 교체된 Session은 current가 아니므로 대상이 아니다.
// Credential revoke use case가 저장소 상태를 바꾼 뒤 호출하는 lifecycle hook이며 raw Credential이 필요 없다.
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
	return len(revoked)
}

// RevokeConnector는 connectorID의 current Session을 registry에서 제거하고 CloseRevoked로 종료를 요청한다.
// 종료를 요청했으면 true다. Connector revoke use case가 호출하는 lifecycle hook이다.
func (r *Registry) RevokeConnector(connectorID uuid.UUID) bool {
	r.mu.Lock()
	e, ok := r.current[connectorID]
	delete(r.current, connectorID)
	r.mu.Unlock()

	if !ok {
		return false
	}
	e.retire(CloseRevoked)
	return true
}

// retire는 진행 중인 IfCurrent가 끝나기를 기다린 뒤 이 Session을 더 이상 current가 아닌 것으로 표시하고 종료를 요청한다.
// 종료 요청은 entry lock 밖에서 한다.
func (e *entry) retire(reason CloseReason) {
	e.mu.Lock()
	e.retired = true
	e.mu.Unlock()

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
