package connector

import (
	"sync"
	"testing"

	"github.com/google/uuid"
)

// revokeRecorder는 Registry가 통지한 revoke를 기록하는 RevokeObserver다.
type revokeRecorder struct {
	mu          sync.Mutex
	credentials []uuid.UUID
	connectors  []uuid.UUID
	// onNotify가 있으면 통지 안에서 호출한다. lock 밖에서 호출되는지(재진입해도 교착하지 않는지) 확인하는 데 쓴다.
	onNotify func()
}

func (r *revokeRecorder) CredentialRevoked(id uuid.UUID) {
	r.mu.Lock()
	r.credentials = append(r.credentials, id)
	fn := r.onNotify
	r.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (r *revokeRecorder) ConnectorRevoked(id uuid.UUID) {
	r.mu.Lock()
	r.connectors = append(r.connectors, id)
	fn := r.onNotify
	r.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (r *revokeRecorder) seen() (credentials, connectors []uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]uuid.UUID(nil), r.credentials...), append([]uuid.UUID(nil), r.connectors...)
}

// RevokeCredential은 Control Session이 있든 없든 observer에 통지한다. Control은 끊겼지만 Data WSS만 남은 경우를 놓치지 않기 위해서다.
func TestRegistryRevokeCredentialNotifiesObserverEvenWithoutControlSession(t *testing.T) {
	registry := NewRegistry()
	recorder := &revokeRecorder{}
	registry.SetRevokeObserver(recorder)

	credentialID := uuid.New()
	if n := registry.RevokeCredential(credentialID); n != 0 {
		t.Fatalf("Control Session이 없을 때 RevokeCredential() = %d, want 0", n)
	}
	if got, _ := recorder.seen(); len(got) != 1 || got[0] != credentialID {
		t.Fatalf("통지 = %v, want [%v](Control Session이 없어도 통지)", got, credentialID)
	}
}

// 통지는 revoke된 Credential에 대해서만 오고, Control Session 종료 동작은 observer가 있어도 그대로다.
func TestRegistryRevokeCredentialNotifiesObserverAndStillClosesOnlyMatchingSession(t *testing.T) {
	registry := NewRegistry()
	recorder := &revokeRecorder{}
	registry.SetRevokeObserver(recorder)

	revoked, other := principalOf(uuid.New()), principalOf(uuid.New())
	revokedClose, otherClose := &closeRecorder{}, &closeRecorder{}
	registry.Register(revoked, revokedClose.close)
	registry.Register(other, otherClose.close)

	if n := registry.RevokeCredential(revoked.CredentialID); n != 1 {
		t.Fatalf("RevokeCredential() = %d, want 1", n)
	}
	if got := revokedClose.got(); len(got) != 1 || got[0] != CloseRevoked {
		t.Fatalf("revoke된 Session 종료 요청 = %v, want [CloseRevoked]", got)
	}
	if got := otherClose.got(); len(got) != 0 {
		t.Fatalf("다른 Session 종료 요청 = %v, want 없음", got)
	}
	credentials, connectors := recorder.seen()
	if len(credentials) != 1 || credentials[0] != revoked.CredentialID || len(connectors) != 0 {
		t.Fatalf("통지 credentials=%v connectors=%v, want 이 Credential 하나뿐", credentials, connectors)
	}
}

// RevokeConnector도 Session이 없어도 통지하고, 반환값은 Control Session을 종료했는지를 그대로 나타낸다.
func TestRegistryRevokeConnectorNotifiesObserverAndKeepsReturnValue(t *testing.T) {
	registry := NewRegistry()
	recorder := &revokeRecorder{}
	registry.SetRevokeObserver(recorder)

	principal := principalOf(uuid.New())
	closer := &closeRecorder{}
	registry.Register(principal, closer.close)

	if !registry.RevokeConnector(principal.ConnectorID) {
		t.Fatal("RevokeConnector(Session 있음) = false, want true")
	}
	if registry.RevokeConnector(principal.ConnectorID) {
		t.Fatal("RevokeConnector(Session 없음) = true, want false")
	}
	if _, got := recorder.seen(); len(got) != 2 || got[0] != principal.ConnectorID || got[1] != principal.ConnectorID {
		t.Fatalf("통지 = %v, want 두 번 모두 %v(Session이 없어도 통지)", got, principal.ConnectorID)
	}
}

// observer가 없으면 기존 동작과 같다. 조립하지 않은 Registry도 revoke를 처리한다.
func TestRegistryRevokeWithoutObserverKeepsWorking(t *testing.T) {
	registry := NewRegistry()
	principal := principalOf(uuid.New())
	closer := &closeRecorder{}
	registry.Register(principal, closer.close)

	if n := registry.RevokeCredential(principal.CredentialID); n != 1 {
		t.Fatalf("RevokeCredential() = %d, want 1", n)
	}
	registry.SetRevokeObserver(nil)
	if registry.RevokeConnector(uuid.New()) {
		t.Fatal("없는 Connector의 RevokeConnector() = true")
	}
}

// observer는 Registry lock 밖에서 호출된다. 통지 안에서 Registry를 다시 써도 교착하지 않는다.
func TestRegistryNotifiesObserverWithoutHoldingItsLock(t *testing.T) {
	registry := NewRegistry()
	principal := principalOf(uuid.New())
	registry.Register(principal, nil)

	reentered := make(chan struct{})
	registry.SetRevokeObserver(&revokeRecorder{onNotify: func() {
		registry.Current(principal.ConnectorID)
		registry.Register(principalOf(uuid.New()), nil)
		close(reentered)
	}})

	done := make(chan struct{})
	go func() {
		registry.RevokeCredential(principal.CredentialID)
		close(done)
	}()
	<-done
	select {
	case <-reentered:
	default:
		t.Fatal("observer가 호출되지 않음")
	}
}

// 동시에 revoke와 조립(SetRevokeObserver), 등록이 일어나도 -race에서 안전하다.
func TestRegistryObserverConcurrentUse(t *testing.T) {
	registry := NewRegistry()
	recorder := &revokeRecorder{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				principal := principalOf(uuid.New())
				registry.Register(principal, nil)
				registry.RevokeCredential(principal.CredentialID)
				registry.RevokeConnector(principal.ConnectorID)
			}
		}()
	}
	registry.SetRevokeObserver(recorder)
	wg.Wait()
}
