package connector

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCloseNotificationWaitsAcrossSealedRouteAndLateCallback(t *testing.T) {
	r := NewRegistry()
	p := principalOf(uuid.New())
	entered, proceed := make(chan struct{}), make(chan struct{})
	g := r.Register(p, func(CloseReason) { close(entered); <-proceed })
	retired := make(chan struct{})
	go func() { g.Retire(CloseRevoked); close(retired) }()
	<-entered
	if g.MarkReady(func([]byte) error { return nil }) {
		t.Fatal("retired route reopened")
	}
	g.Release()
	waited := make(chan struct{})
	go func() { g.WaitCloseNotification(); close(waited) }()
	select {
	case <-waited:
		t.Fatal("cleanup skipped an incomplete close callback")
	case <-time.After(50 * time.Millisecond):
	}
	close(proceed)
	<-retired
	<-waited
	g.WaitCloseNotification()
}

func TestRetireCallbackMayReenterRetireWithoutWaitingOnItself(t *testing.T) {
	r := NewRegistry()
	var g *Registration
	calls := 0
	g = r.Register(principalOf(uuid.New()), func(reason CloseReason) {
		calls++
		g.Retire(reason)
	})
	g.Retire(CloseRevoked)
	g.WaitCloseNotification()
	if calls != 1 {
		t.Fatalf("callback calls = %d", calls)
	}
}
