package connectorwss

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// revokeObserverRecorder는 Registry가 Control 밖의 connection(Terminal Data WSS)에 전하는 revoke 통지를 기록한다.
type revokeObserverRecorder struct {
	mu          sync.Mutex
	credentials []uuid.UUID
	connectors  []uuid.UUID
}

func (r *revokeObserverRecorder) CredentialRevoked(id uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.credentials = append(r.credentials, id)
}

func (r *revokeObserverRecorder) ConnectorRevoked(id uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.connectors = append(r.connectors, id)
}

func (r *revokeObserverRecorder) seen() (credentials, connectors []uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]uuid.UUID(nil), r.credentials...), append([]uuid.UUID(nil), r.connectors...)
}

// contracts/connector/README.md §2: Credential revoke는 Control과 Data 연결을 모두 끝낸다. HEARTBEAT가 revoke를 관측하면 Control을
// 4001로 끝내는 것과 함께 Registry의 revoke primitive를 거쳐 observer(Terminal Data WSS 종료)에 그 Credential의 ID를 통지한다.
func TestRevokeDetectedOnHeartbeatNotifiesObserverForThatCredential(t *testing.T) {
	h := newHarness(t)
	observer := &revokeObserverRecorder{}
	h.registry.SetRevokeObserver(observer)
	p := h.establish()

	p.heartbeat()
	waitFor(t, "revoke 전 heartbeat 기록", func() bool { return len(h.beats.recorded()) == 1 })
	if credentials, connectors := observer.seen(); len(credentials)+len(connectors) != 0 {
		t.Fatalf("revoke 전에 통지됨: credentials=%v connectors=%v", credentials, connectors)
	}

	h.beats.revoke(h.principal.CredentialID)
	p.heartbeat()
	if closeErr := p.waitClosed(5 * time.Second); closeErr.Code != closeCredentialRevoked {
		t.Fatalf("close code = %d, want %d", closeErr.Code, closeCredentialRevoked)
	}

	// 통지는 Control close와 함께 온다. close를 본 뒤에는 이미 호출되어 있어야 하지만 비동기 종료 경로와 겹치므로 잠시 기다린다.
	waitFor(t, "revoke 통지", func() bool {
		credentials, _ := observer.seen()
		return len(credentials) > 0
	})
	credentials, connectors := observer.seen()
	if len(credentials) != 1 || credentials[0] != h.principal.CredentialID || len(connectors) != 0 {
		t.Fatalf("통지 credentials=%v connectors=%v, want 이 Credential 하나뿐", credentials, connectors)
	}
}

// 저장소 장애는 revoke가 아니다. HEARTBEAT 기록이 실패해도 Control은 유지되고 Data WSS를 끝내는 통지도 하지 않는다.
func TestHeartbeatPersistenceFailureDoesNotNotifyRevoke(t *testing.T) {
	h := newHarness(t)
	observer := &revokeObserverRecorder{}
	h.registry.SetRevokeObserver(observer)
	h.beats.failWith(errors.New("database unavailable"))
	var attempts atomic.Int32
	h.beats.mu.Lock()
	h.beats.beforeRecord = func() { attempts.Add(1) }
	h.beats.mu.Unlock()
	p := h.establish()

	p.heartbeat()
	p.heartbeat()
	// 저장소 장애에서도 연결은 유지된다. 같은 연결의 HEARTBEAT가 두 번 모두 처리(기록 시도)된 것으로 확인한다.
	waitFor(t, "heartbeat 기록 시도", func() bool { return attempts.Load() >= 2 })

	if credentials, connectors := observer.seen(); len(credentials)+len(connectors) != 0 {
		t.Fatalf("저장소 장애인데 revoke가 통지됨: credentials=%v connectors=%v", credentials, connectors)
	}
	if _, ok := h.registry.Current(h.principal.ConnectorID); !ok {
		t.Fatal("저장소 장애로 Control Session이 registry에서 제거됨")
	}
}

// Credential revoke hook(저장소 상태를 바꾼 뒤 직접 호출)도 같은 primitive이므로 HEARTBEAT 경로와 같은 통지를 만든다.
func TestRevokeCredentialHookNotifiesObserverLikeHeartbeatDetection(t *testing.T) {
	h := newHarness(t)
	observer := &revokeObserverRecorder{}
	h.registry.SetRevokeObserver(observer)
	p := h.establish()

	if n := h.registry.RevokeCredential(h.principal.CredentialID); n != 1 {
		t.Fatalf("RevokeCredential() = %d, want 1", n)
	}
	if closeErr := p.waitClosed(5 * time.Second); closeErr.Code != closeCredentialRevoked {
		t.Fatalf("close code = %d, want %d", closeErr.Code, closeCredentialRevoked)
	}
	if credentials, _ := observer.seen(); len(credentials) != 1 || credentials[0] != h.principal.CredentialID {
		t.Fatalf("통지 = %v, want [%v]", credentials, h.principal.CredentialID)
	}
}
