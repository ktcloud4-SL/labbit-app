package connector

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// routeRecorder는 Route로 쓴 data를 기록하고 이 route가 몇 번 불렸는지 센다.
type routeRecorder struct {
	mu    sync.Mutex
	calls int
}

func (r *routeRecorder) route(_ []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return nil
}

func (r *routeRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// use는 registry의 ready route를 통해 write를 한 번 시도하고 그 결과 오류와 fn이 받은 Session을 반환한다.
func use(registry *Registry, connectorID uuid.UUID) (Session, bool, error) {
	var session Session
	called := false
	err := registry.WithReadyRoute(connectorID, func(s Session, route Route) error {
		called = true
		session = s
		return route([]byte("{}"))
	})
	return session, called, err
}

// current는 소유이고 ready는 별개다. current만으로는 route가 열리지 않고 MarkReady 뒤에야 그 exact Session의 route가 열린다.
func TestReadyRouteRequiresMarkReadyNotJustOwnership(t *testing.T) {
	registry := NewRegistry()
	principal := principalOf(uuid.New())

	if _, called, err := use(registry, principal.ConnectorID); called || !errors.Is(err, ErrNotConnected) || !errors.Is(err, ErrConnectorUnavailable) {
		t.Fatalf("등록 전 = called %v, err %v, want ErrNotConnected", called, err)
	}

	registration := registry.Register(principal, nil)
	if _, ok := registry.Current(principal.ConnectorID); !ok {
		t.Fatal("등록 직후 current가 아님")
	}
	if _, called, err := use(registry, principal.ConnectorID); called || !errors.Is(err, ErrNotReady) || !errors.Is(err, ErrConnectorUnavailable) {
		t.Fatalf("HELLO_ACK 전 = called %v, err %v, want ErrNotReady", called, err)
	}

	recorder := &routeRecorder{}
	if !registration.MarkReady(recorder.route) {
		t.Fatal("current Session의 MarkReady가 거절됨")
	}
	session, called, err := use(registry, principal.ConnectorID)
	if !called || err != nil || session != registration.Session() || recorder.count() != 1 {
		t.Fatalf("ready 뒤 = session %+v, called %v, err %v, writes %d", session, called, err, recorder.count())
	}
}

func TestMarkReadyRejectsNilRoute(t *testing.T) {
	registry := NewRegistry()
	registration := registry.Register(principalOf(uuid.New()), nil)
	if registration.MarkReady(nil) {
		t.Fatal("nil route가 등록됨")
	}
	if _, called, err := use(registry, registration.Session().ConnectorID); called || !errors.Is(err, ErrNotReady) {
		t.Fatalf("called %v, err %v, want ErrNotReady", called, err)
	}
}

// A가 ready인 상태에서 B가 등록되면 A는 즉시 route가 아니고, B는 자신의 MarkReady 전까지 route가 아니다.
func TestReplacementClosesOldRouteImmediatelyAndNewRouteOpensOnlyAfterReady(t *testing.T) {
	registry := NewRegistry()
	principal := principalOf(uuid.New())
	oldRecorder, newRecorder := &routeRecorder{}, &routeRecorder{}

	old := registry.Register(principal, nil)
	old.MarkReady(oldRecorder.route)
	if _, called, err := use(registry, principal.ConnectorID); !called || err != nil {
		t.Fatalf("교체 전 A route = called %v, err %v", called, err)
	}

	fresh := registry.Register(principal, nil)
	if _, called, err := use(registry, principal.ConnectorID); called || !errors.Is(err, ErrNotReady) {
		t.Fatalf("교체 직후 = called %v, err %v, want ErrNotReady(A는 닫히고 B는 아직 HELLO_ACK 전)", called, err)
	}
	if old.MarkReady(oldRecorder.route) {
		t.Fatal("교체된 Session이 다시 ready가 됨")
	}
	if _, called, err := use(registry, principal.ConnectorID); called || !errors.Is(err, ErrNotReady) {
		t.Fatalf("교체된 Session의 MarkReady 뒤 = called %v, err %v", called, err)
	}

	if !fresh.MarkReady(newRecorder.route) {
		t.Fatal("새 Session의 MarkReady가 거절됨")
	}
	session, called, err := use(registry, principal.ConnectorID)
	if !called || err != nil || session != fresh.Session() {
		t.Fatalf("B ready 뒤 = session %+v, called %v, err %v, want %+v", session, called, err, fresh.Session())
	}
	if oldRecorder.count() != 1 || newRecorder.count() != 1 {
		t.Fatalf("writes = old %d, new %d, want old 1(교체 전), new 1", oldRecorder.count(), newRecorder.count())
	}
}

// 교체된 이전 Session의 늦은 release는 더 새로운 ready Session의 route를 지우지 않는다(LBT-70 stale release invariant).
func TestStaleReleaseKeepsNewerReadyRoute(t *testing.T) {
	registry := NewRegistry()
	principal := principalOf(uuid.New())
	old := registry.Register(principal, nil)
	old.MarkReady((&routeRecorder{}).route)
	fresh := registry.Register(principal, nil)
	recorder := &routeRecorder{}
	fresh.MarkReady(recorder.route)

	old.Release()
	old.Release()
	session, called, err := use(registry, principal.ConnectorID)
	if !called || err != nil || session != fresh.Session() || recorder.count() != 1 {
		t.Fatalf("stale release 뒤 = session %+v, called %v, err %v, writes %d", session, called, err, recorder.count())
	}
}

func TestReleaseAndRevokeCloseRoute(t *testing.T) {
	t.Run("release", func(t *testing.T) {
		registry := NewRegistry()
		registration := registry.Register(principalOf(uuid.New()), nil)
		registration.MarkReady((&routeRecorder{}).route)
		registration.Release()
		if _, called, err := use(registry, registration.Session().ConnectorID); called || !errors.Is(err, ErrNotConnected) {
			t.Fatalf("release 뒤 = called %v, err %v", called, err)
		}
	})
	t.Run("revoke credential", func(t *testing.T) {
		registry := NewRegistry()
		principal := principalOf(uuid.New())
		registration := registry.Register(principal, nil)
		recorder := &routeRecorder{}
		registration.MarkReady(recorder.route)
		registry.RevokeCredential(principal.CredentialID)
		if _, called, err := use(registry, principal.ConnectorID); called || !errors.Is(err, ErrNotConnected) {
			t.Fatalf("revoke 뒤 = called %v, err %v", called, err)
		}
		if registration.MarkReady(recorder.route) {
			t.Fatal("revoke된 Session이 ready가 됨")
		}
	})
	t.Run("revoke connector", func(t *testing.T) {
		registry := NewRegistry()
		principal := principalOf(uuid.New())
		registry.Register(principal, nil).MarkReady((&routeRecorder{}).route)
		registry.RevokeConnector(principal.ConnectorID)
		if _, called, err := use(registry, principal.ConnectorID); called || !errors.Is(err, ErrNotConnected) {
			t.Fatalf("revoke 뒤 = called %v, err %v", called, err)
		}
	})
}

func TestReadyRoutesOfDifferentConnectorsAreIndependent(t *testing.T) {
	registry := NewRegistry()
	a, b := principalOf(uuid.New()), principalOf(uuid.New())
	recorderA, recorderB := &routeRecorder{}, &routeRecorder{}
	registry.Register(a, nil).MarkReady(recorderA.route)
	registry.Register(b, nil).MarkReady(recorderB.route)

	if _, _, err := use(registry, a.ConnectorID); err != nil {
		t.Fatal(err)
	}
	if recorderA.count() != 1 || recorderB.count() != 0 {
		t.Fatalf("writes = A %d, B %d, want A만 1", recorderA.count(), recorderB.count())
	}
}

func TestWithReadyRouteReturnsFnError(t *testing.T) {
	registry := NewRegistry()
	principal := principalOf(uuid.New())
	registry.Register(principal, nil).MarkReady((&routeRecorder{}).route)
	want := errors.New("fn failed")
	if got := registry.WithReadyRoute(principal.ConnectorID, func(Session, Route) error { return want }); !errors.Is(got, want) {
		t.Fatalf("WithReadyRoute() = %v, want %v", got, want)
	}
}

// 교체는 진행 중인 route write가 끝나기를 기다린다. 그 write가 끝난 뒤에는 이전 Session에 새 write가 시작되지 않는다.
// 진행 중에 들어온 다른 send는 교체가 시작된 뒤이므로 이전 Session으로 나가지 않는다.
func TestReplacementWaitsForInFlightWriteAndThenNoNewWriteReachesOldSession(t *testing.T) {
	registry := NewRegistry()
	principal := principalOf(uuid.New())
	oldRecorder := &closeRecorder{}
	old := registry.Register(principal, oldRecorder.close)
	writes := &routeRecorder{}
	old.MarkReady(writes.route)

	inWrite, releaseWrite := make(chan struct{}), make(chan struct{})
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- registry.WithReadyRoute(principal.ConnectorID, func(_ Session, route Route) error {
			close(inWrite)
			<-releaseWrite
			return route([]byte("{}"))
		})
	}()
	<-inWrite

	registered := make(chan *Registration, 1)
	go func() { registered <- registry.Register(principal, nil) }()

	select {
	case <-registered:
		t.Fatal("진행 중인 write가 끝나기 전에 교체가 끝남")
	case <-time.After(100 * time.Millisecond):
	}
	if got := oldRecorder.got(); len(got) != 0 {
		t.Fatalf("write 중에 종료가 요청됨: %v", got)
	}

	close(releaseWrite)
	if err := <-writeDone; err != nil {
		t.Fatalf("진행 중이던 write 오류 = %v", err)
	}
	fresh := <-registered
	if writes.count() != 1 {
		t.Fatalf("이전 Session writes = %d, want 1(교체 전에 시작한 write만)", writes.count())
	}
	if _, called, err := use(registry, principal.ConnectorID); called || !errors.Is(err, ErrNotReady) {
		t.Fatalf("교체 뒤 = called %v, err %v, want ErrNotReady", called, err)
	}
	if writes.count() != 1 {
		t.Fatalf("교체 뒤 이전 Session에 write가 시작됨: %d", writes.count())
	}
	if got := oldRecorder.got(); len(got) != 1 || got[0] != CloseReplaced {
		t.Fatalf("종료 요청 = %v, want [CloseReplaced]", got)
	}
	fresh.Release()
}

// send와 replacement가 계속 경쟁해도 Register가 반환한 뒤에는 이전 Session의 route가 호출되지 않는다.
func TestNoWriteReachesReplacedSessionAfterRegisterReturns(t *testing.T) {
	const rounds = 200
	for round := 0; round < rounds; round++ {
		registry := NewRegistry()
		principal := principalOf(uuid.New())
		old := registry.Register(principal, nil)

		var replaced atomic.Bool
		var violations atomic.Int32
		old.MarkReady(func([]byte) error {
			if replaced.Load() {
				violations.Add(1)
			}
			return nil
		})

		stop := make(chan struct{})
		var senders sync.WaitGroup
		for i := 0; i < 4; i++ {
			senders.Add(1)
			go func() {
				defer senders.Done()
				for {
					select {
					case <-stop:
						return
					default:
						_, _, _ = use(registry, principal.ConnectorID)
					}
				}
			}()
		}

		fresh := registry.Register(principal, nil)
		replaced.Store(true) // Register가 반환한 뒤부터 이전 Session에 새 write가 있으면 안 된다.
		fresh.MarkReady((&routeRecorder{}).route)
		close(stop)
		senders.Wait()

		if got := violations.Load(); got != 0 {
			t.Fatalf("round %d: 교체가 끝난 뒤 이전 Session route가 %d번 호출됨", round, got)
		}
	}
}
