package connector

import (
	"sync"

	"github.com/google/uuid"
)

// Session은 HELLO까지 완료한 Control connection 하나를 식별한다.
// 같은 Connector의 connection이 다시 붙어도 Session.ID는 서로 다르다.
type Session struct {
	ID          uuid.UUID
	ConnectorID uuid.UUID
}

// Registry는 HELLO까지 완료한 Control connection을 Connector별로 기록하는 최소 골격이다.
// 여러 connection goroutine이 동시에 사용해도 안전하다.
//
// 중복 connection 교체(이전 connection 종료), heartbeat 기반 lifecycle, stale 정리는 이 골격의 범위가 아니다.
// 같은 Connector가 다시 등록되면 새 Session이 current가 되지만 이전 connection을 닫지는 않는다.
type Registry struct {
	mu      sync.RWMutex
	current map[uuid.UUID]Session
}

func NewRegistry() *Registry {
	return &Registry{current: make(map[uuid.UUID]Session)}
}

// Register는 connectorID의 current Session을 새로 만들고, 그 Session만 제거하는 release를 반환한다.
// release는 여러 번 호출해도 안전하며, 이미 다른 Session이 current가 되었다면 그것을 지우지 않는다.
func (r *Registry) Register(connectorID uuid.UUID) (Session, func()) {
	session := Session{ID: uuid.New(), ConnectorID: connectorID}

	r.mu.Lock()
	r.current[connectorID] = session
	r.mu.Unlock()

	var once sync.Once
	release := func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.current[connectorID].ID == session.ID {
				delete(r.current, connectorID)
			}
		})
	}
	return session, release
}

// Current는 connectorID의 현재 Session을 반환한다. 없으면 false다.
func (r *Registry) Current(connectorID uuid.UUID) (Session, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	session, ok := r.current[connectorID]
	return session, ok
}
