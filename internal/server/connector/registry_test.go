package connector

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// closeRecorder는 Registry가 요청한 종료를 기록하는 closeFn이다.
type closeRecorder struct {
	mu      sync.Mutex
	reasons []CloseReason
}

func (c *closeRecorder) close(reason CloseReason) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reasons = append(c.reasons, reason)
}

func (c *closeRecorder) got() []CloseReason {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]CloseReason(nil), c.reasons...)
}

func principalOf(connectorID uuid.UUID) Principal {
	return Principal{ConnectorID: connectorID, OrganizationID: uuid.New(), CredentialID: uuid.New()}
}

func TestRegistryTracksSessionUntilReleased(t *testing.T) {
	registry := NewRegistry()
	principal := principalOf(uuid.New())

	if _, ok := registry.Current(principal.ConnectorID); ok {
		t.Fatal("등록 전에 Current가 존재함")
	}

	registration := registry.Register(principal, nil)
	session := registration.Session()
	if session.ConnectorID != principal.ConnectorID || session.CredentialID != principal.CredentialID || session.ID == uuid.Nil {
		t.Fatalf("Session = %+v", session)
	}
	if got, ok := registry.Current(principal.ConnectorID); !ok || got != session {
		t.Fatalf("Current() = %+v, %v, want %+v", got, ok, session)
	}

	registration.Release()
	registration.Release() // 중복 release도 안전하다.
	if _, ok := registry.Current(principal.ConnectorID); ok {
		t.Fatal("release 후에도 Current가 남아 있음")
	}
}

func TestRegistryKeepsConnectorsIndependent(t *testing.T) {
	registry := NewRegistry()
	a, b := principalOf(uuid.New()), principalOf(uuid.New())
	recorderA, recorderB := &closeRecorder{}, &closeRecorder{}

	regA := registry.Register(a, recorderA.close)
	regB := registry.Register(b, recorderB.close)
	regA.Release()

	if _, ok := registry.Current(a.ConnectorID); ok {
		t.Fatal("Connector A의 release가 반영되지 않음")
	}
	if got, ok := registry.Current(b.ConnectorID); !ok || got != regB.Session() {
		t.Fatalf("Connector B Current() = %+v, %v, want %+v", got, ok, regB.Session())
	}
	if regA.Session().ID == regB.Session().ID {
		t.Fatal("서로 다른 connection의 Session ID가 같음")
	}
	if len(recorderA.got())+len(recorderB.got()) != 0 {
		t.Fatal("다른 Connector의 등록과 release가 종료를 요청함")
	}
}

// 같은 Connector의 새 Session이 current가 되고 이전 Session은 교체 사유로 종료가 요청된다.
func TestRegistryReplacementMakesNewSessionCurrentAndClosesOld(t *testing.T) {
	registry := NewRegistry()
	principal := principalOf(uuid.New())
	oldRecorder, newRecorder := &closeRecorder{}, &closeRecorder{}

	old := registry.Register(principal, oldRecorder.close)
	fresh := registry.Register(principal, newRecorder.close)

	if got, ok := registry.Current(principal.ConnectorID); !ok || got != fresh.Session() {
		t.Fatalf("Current() = %+v, %v, want new session %+v", got, ok, fresh.Session())
	}
	if got := oldRecorder.got(); len(got) != 1 || got[0] != CloseReplaced {
		t.Fatalf("이전 Session 종료 요청 = %v, want [CloseReplaced]", got)
	}
	if got := newRecorder.got(); len(got) != 0 {
		t.Fatalf("새 Session이 종료 요청을 받음: %v", got)
	}
	if old.Session().ID == fresh.Session().ID {
		t.Fatal("교체 전후 Session ID가 같음")
	}
}

// 교체된 이전 connection의 늦은 release는 새 Session을 지우거나 종료하면 안 된다.
func TestRegistryStaleReleaseDoesNotRemoveNewerSession(t *testing.T) {
	registry := NewRegistry()
	principal := principalOf(uuid.New())
	newRecorder := &closeRecorder{}

	old := registry.Register(principal, nil)
	fresh := registry.Register(principal, newRecorder.close)
	old.Release()
	old.Release()

	if got, ok := registry.Current(principal.ConnectorID); !ok || got != fresh.Session() {
		t.Fatalf("Current() = %+v, %v, want newer session %+v", got, ok, fresh.Session())
	}
	if got := newRecorder.got(); len(got) != 0 {
		t.Fatalf("이전 Session의 release가 새 Session 종료를 요청함: %v", got)
	}
}

// closeFn은 Registry lock 밖에서 호출한다. closeFn 안에서 Registry를 다시 사용해도 멈추지 않는다.
func TestRegistryCallsCloseFnWithoutHoldingItsLock(t *testing.T) {
	registry := NewRegistry()
	principal := principalOf(uuid.New())
	other := principalOf(uuid.New())

	reentered := make(chan struct{})
	registry.Register(principal, func(CloseReason) {
		// lock을 잡은 채 호출되었다면 아래 호출이 교착한다.
		registry.Current(principal.ConnectorID)
		registry.Register(other, nil)
		close(reentered)
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		registry.Register(principal, nil)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("closeFn이 Registry lock을 잡은 채 호출되어 교착함")
	}
	select {
	case <-reentered:
	default:
		t.Fatal("closeFn이 호출되지 않음")
	}
}

// 교체 후 이전 Session의 IfCurrent는 fn을 시작하지 못한다(stale heartbeat가 last_seen을 갱신하지 못한다).
func TestRegistryStaleSessionCannotRunHeartbeatWork(t *testing.T) {
	registry := NewRegistry()
	principal := principalOf(uuid.New())

	old := registry.Register(principal, nil)
	fresh := registry.Register(principal, nil)

	ran := false
	current, err := old.IfCurrent(func() error { ran = true; return nil })
	if current || err != nil || ran {
		t.Fatalf("stale IfCurrent = (%v, %v), ran = %v, want (false, nil) without running", current, err, ran)
	}

	current, err = fresh.IfCurrent(func() error { ran = true; return nil })
	if !current || err != nil || !ran {
		t.Fatalf("current IfCurrent = (%v, %v), ran = %v, want (true, nil) and ran", current, err, ran)
	}
}

func TestRegistryIfCurrentReturnsWorkError(t *testing.T) {
	registry := NewRegistry()
	registration := registry.Register(principalOf(uuid.New()), nil)
	want := errors.New("record failed")

	current, err := registration.IfCurrent(func() error { return want })
	if !current || !errors.Is(err, want) {
		t.Fatalf("IfCurrent() = (%v, %v), want (true, %v)", current, err, want)
	}
}

// 진행 중인 heartbeat 기록이 있으면 교체는 그 완료를 기다리고, Register가 반환한 뒤에는 이전 Session이 시작하는 기록이 없다.
func TestRegistryReplacementWaitsForInFlightHeartbeatWork(t *testing.T) {
	registry := NewRegistry()
	principal := principalOf(uuid.New())
	oldRecorder := &closeRecorder{}
	old := registry.Register(principal, oldRecorder.close)

	inWork, releaseWork := make(chan struct{}), make(chan struct{})
	workDone := make(chan struct{})
	go func() {
		defer close(workDone)
		_, _ = old.IfCurrent(func() error {
			close(inWork)
			<-releaseWork
			return nil
		})
	}()
	<-inWork

	registered := make(chan *Registration, 1)
	go func() { registered <- registry.Register(principal, nil) }()

	// 기록이 끝나기 전에는 교체가 끝나지 않고 이전 Session에 종료도 요청되지 않는다.
	select {
	case <-registered:
		t.Fatal("진행 중인 heartbeat 기록이 끝나기 전에 교체가 끝남")
	case <-time.After(100 * time.Millisecond):
	}
	if got := oldRecorder.got(); len(got) != 0 {
		t.Fatalf("기록 중에 종료가 요청됨: %v", got)
	}

	close(releaseWork)
	<-workDone
	fresh := <-registered
	if got := oldRecorder.got(); len(got) != 1 || got[0] != CloseReplaced {
		t.Fatalf("이전 Session 종료 요청 = %v, want [CloseReplaced]", got)
	}
	ran := false
	if current, _ := old.IfCurrent(func() error { ran = true; return nil }); current || ran {
		t.Fatal("교체 뒤 이전 Session이 heartbeat 기록을 시작함")
	}
	if got, ok := registry.Current(principal.ConnectorID); !ok || got != fresh.Session() {
		t.Fatalf("Current() = %+v, %v, want %+v", got, ok, fresh.Session())
	}
}

// 같은 Connector에 동시에 등록해도 current는 정확히 하나이고, 나머지 Session은 모두 정확히 한 번 교체 종료가 요청된다.
func TestRegistryConcurrentReplacementLeavesExactlyOneCurrent(t *testing.T) {
	registry := NewRegistry()
	principal := principalOf(uuid.New())
	const sessions = 32

	recorders := make([]*closeRecorder, sessions)
	registrations := make([]*Registration, sessions)
	var wg sync.WaitGroup
	for i := range sessions {
		recorders[i] = &closeRecorder{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			registrations[i] = registry.Register(principal, recorders[i].close)
		}()
	}
	wg.Wait()

	current, ok := registry.Current(principal.ConnectorID)
	if !ok {
		t.Fatal("동시 등록 뒤 current가 없음")
	}
	closed, alive := 0, 0
	for i, registration := range registrations {
		got := recorders[i].got()
		switch {
		case registration.Session() == current:
			alive++
			if len(got) != 0 {
				t.Fatalf("current Session이 종료를 요청받음: %v", got)
			}
		case len(got) == 1 && got[0] == CloseReplaced:
			closed++
		default:
			t.Fatalf("교체된 Session %d의 종료 요청 = %v, want [CloseReplaced]", i, got)
		}
	}
	if alive != 1 || closed != sessions-1 {
		t.Fatalf("alive = %d, replaced = %d, want 1 and %d", alive, closed, sessions-1)
	}
}

// Credential revoke hook은 그 Credential로 인증된 current Session만 registry에서 제거하고 종료를 요청한다.
func TestRegistryRevokeCredentialClosesOnlyMatchingCurrentSession(t *testing.T) {
	registry := NewRegistry()
	revoked, other := principalOf(uuid.New()), principalOf(uuid.New())
	revokedRecorder, otherRecorder := &closeRecorder{}, &closeRecorder{}
	revokedReg := registry.Register(revoked, revokedRecorder.close)
	otherReg := registry.Register(other, otherRecorder.close)

	if n := registry.RevokeCredential(revoked.CredentialID); n != 1 {
		t.Fatalf("RevokeCredential() = %d, want 1", n)
	}
	if got := revokedRecorder.got(); len(got) != 1 || got[0] != CloseRevoked {
		t.Fatalf("revoke된 Session 종료 요청 = %v, want [CloseRevoked]", got)
	}
	if _, ok := registry.Current(revoked.ConnectorID); ok {
		t.Fatal("revoke된 Session이 registry에 남음")
	}
	if got, ok := registry.Current(other.ConnectorID); !ok || got != otherReg.Session() || len(otherRecorder.got()) != 0 {
		t.Fatal("다른 Credential의 Session이 영향을 받음")
	}

	// revoke 뒤의 늦은 release와 heartbeat 기록은 아무 영향이 없다.
	revokedReg.Release()
	ran := false
	if current, _ := revokedReg.IfCurrent(func() error { ran = true; return nil }); current || ran {
		t.Fatal("revoke된 Session이 heartbeat 기록을 시작함")
	}
	if n := registry.RevokeCredential(revoked.CredentialID); n != 0 {
		t.Fatalf("이미 제거된 Credential의 RevokeCredential() = %d, want 0", n)
	}
}

func TestRegistryRevokeConnectorClosesCurrentSession(t *testing.T) {
	registry := NewRegistry()
	principal := principalOf(uuid.New())
	recorder := &closeRecorder{}
	registry.Register(principal, recorder.close)

	if !registry.RevokeConnector(principal.ConnectorID) {
		t.Fatal("RevokeConnector() = false, want true")
	}
	if got := recorder.got(); len(got) != 1 || got[0] != CloseRevoked {
		t.Fatalf("종료 요청 = %v, want [CloseRevoked]", got)
	}
	if _, ok := registry.Current(principal.ConnectorID); ok {
		t.Fatal("revoke 뒤에도 Session이 남음")
	}
	if registry.RevokeConnector(principal.ConnectorID) {
		t.Fatal("Session이 없는 Connector의 RevokeConnector() = true")
	}
}

// 이미 다른 Credential/Session으로 교체되었다면 이전 Credential의 revoke는 새 current Session을 닫으면 안 된다.
func TestRegistryRevokeOfReplacedCredentialDoesNotCloseNewSession(t *testing.T) {
	registry := NewRegistry()
	connectorID := uuid.New()
	oldPrincipal, newPrincipal := principalOf(connectorID), principalOf(connectorID)
	newRecorder := &closeRecorder{}

	registry.Register(oldPrincipal, nil)
	fresh := registry.Register(newPrincipal, newRecorder.close)

	if n := registry.RevokeCredential(oldPrincipal.CredentialID); n != 0 {
		t.Fatalf("RevokeCredential(교체된 Credential) = %d, want 0", n)
	}
	if got, ok := registry.Current(connectorID); !ok || got != fresh.Session() || len(newRecorder.got()) != 0 {
		t.Fatal("교체된 Credential의 revoke가 새 current Session에 영향을 줌")
	}
}

// 교체, revoke hook, release, heartbeat 기록이 동시에 일어나도 각 Session에는 종료 요청이 최대 한 번이고
// 모든 작업이 끝나면 current는 없거나 하나이며 그 Session은 종료 요청을 받지 않았다. -race로 실행한다.
func TestRegistryReplaceRevokeReleaseRace(t *testing.T) {
	registry := NewRegistry()
	principal := principalOf(uuid.New())

	var mu sync.Mutex
	var registrations []*Registration
	var recorders []*closeRecorder
	var wg sync.WaitGroup
	for i := range 48 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			recorder := &closeRecorder{}
			registration := registry.Register(principal, recorder.close)
			mu.Lock()
			registrations = append(registrations, registration)
			recorders = append(recorders, recorder)
			mu.Unlock()
			switch i % 4 {
			case 0:
				registration.Release()
			case 1:
				registry.RevokeCredential(principal.CredentialID)
			case 2:
				registry.RevokeConnector(principal.ConnectorID)
			default:
				_, _ = registration.IfCurrent(func() error { return nil })
			}
		}()
	}
	wg.Wait()

	current, hasCurrent := registry.Current(principal.ConnectorID)
	for i, registration := range registrations {
		got := recorders[i].got()
		if len(got) > 1 {
			t.Fatalf("Session %d가 종료 요청을 %d번 받음: %v", i, len(got), got)
		}
		if hasCurrent && registration.Session() == current && len(got) != 0 {
			t.Fatalf("current Session이 종료 요청을 받음: %v", got)
		}
	}
}

// 서로 다른 Connector 여러 개를 동시에 등록·release해도 안전하다.
func TestRegistryConcurrentUse(t *testing.T) {
	registry := NewRegistry()
	principals := []Principal{principalOf(uuid.New()), principalOf(uuid.New()), principalOf(uuid.New())}

	var wg sync.WaitGroup
	var heartbeats atomic.Int64
	for i := range 64 {
		wg.Add(1)
		go func(principal Principal) {
			defer wg.Done()
			for range 50 {
				registration := registry.Register(principal, nil)
				registry.Current(principal.ConnectorID)
				if current, _ := registration.IfCurrent(func() error { return nil }); current {
					heartbeats.Add(1)
				}
				registration.Release()
			}
		}(principals[i%len(principals)])
	}
	wg.Wait()

	// 경쟁 중 교체된 Session의 늦은 release가 있어도 모든 goroutine이 끝나면 남는 Session이 없다.
	for _, principal := range principals {
		if _, ok := registry.Current(principal.ConnectorID); ok {
			t.Fatalf("모든 release 후에도 Connector %s Session이 남음", principal.ConnectorID)
		}
	}
	if heartbeats.Load() == 0 {
		t.Fatal("어느 Session도 current로 heartbeat 기록을 수행하지 못함")
	}
}
