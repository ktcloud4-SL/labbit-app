package connector

import (
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestRegistryTracksSessionUntilReleased(t *testing.T) {
	registry := NewRegistry()
	connectorID := uuid.New()

	if _, ok := registry.Current(connectorID); ok {
		t.Fatal("등록 전에 Current가 존재함")
	}

	session, release := registry.Register(connectorID)
	if session.ConnectorID != connectorID || session.ID == uuid.Nil {
		t.Fatalf("Session = %+v", session)
	}
	if got, ok := registry.Current(connectorID); !ok || got != session {
		t.Fatalf("Current() = %+v, %v, want %+v", got, ok, session)
	}

	release()
	release() // 중복 release도 안전하다.
	if _, ok := registry.Current(connectorID); ok {
		t.Fatal("release 후에도 Current가 남아 있음")
	}
}

func TestRegistryKeepsConnectorsIndependent(t *testing.T) {
	registry := NewRegistry()
	a, b := uuid.New(), uuid.New()

	sessionA, releaseA := registry.Register(a)
	sessionB, _ := registry.Register(b)
	releaseA()

	if _, ok := registry.Current(a); ok {
		t.Fatal("Connector A의 release가 반영되지 않음")
	}
	if got, ok := registry.Current(b); !ok || got != sessionB {
		t.Fatalf("Connector B Current() = %+v, %v, want %+v", got, ok, sessionB)
	}
	if sessionA.ID == sessionB.ID {
		t.Fatal("서로 다른 connection의 Session ID가 같음")
	}
}

// 같은 Connector가 다시 등록되면 새 Session이 current다. 이전 connection이 늦게 release해도
// 새 Session을 지우면 안 된다. (이전 connection을 닫는 교체 정책은 이 골격의 범위가 아니다.)
func TestRegistryStaleReleaseDoesNotRemoveNewerSession(t *testing.T) {
	registry := NewRegistry()
	connectorID := uuid.New()

	_, releaseOld := registry.Register(connectorID)
	newer, _ := registry.Register(connectorID)
	releaseOld()

	if got, ok := registry.Current(connectorID); !ok || got != newer {
		t.Fatalf("Current() = %+v, %v, want newer session %+v", got, ok, newer)
	}
}

func TestRegistryConcurrentUse(t *testing.T) {
	registry := NewRegistry()
	connectorIDs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(id uuid.UUID) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, release := registry.Register(id)
				registry.Current(id)
				release()
			}
		}(connectorIDs[i%len(connectorIDs)])
	}
	wg.Wait()

	// 경쟁 중 교체된 Session의 늦은 release가 있어도 모든 goroutine이 끝나면 남는 Session이 없다.
	for _, id := range connectorIDs {
		if _, ok := registry.Current(id); ok {
			t.Fatalf("모든 release 후에도 Connector %s Session이 남음", id)
		}
	}
}
