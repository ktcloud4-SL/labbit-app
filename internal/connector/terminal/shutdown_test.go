package terminal_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/terminal"
)

func TestTerminalShutdownRejectsDelayedOpensAndEndsOnce(t *testing.T) {
	var ended atomic.Int64
	var wrongReason atomic.Bool
	manager := terminal.NewSessionManager(time.Minute, func(_ *terminal.Session, reason string, _ *int, _ error) {
		ended.Add(1)
		if reason != protocol.TerminalReasonServiceRestart {
			wrongReason.Store(true)
		}
	})
	payload := protocol.TerminalOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server-1", Cols: 80, Rows: 24}
	envelope := protocol.BaseEnvelope{TerminalSessionID: "session-1", LabInstanceID: "lab-1", Generation: 1}
	started, release := make(chan struct{}), make(chan struct{})
	opened := make(chan error, 1)
	go func() {
		_, _, err := manager.GetOrCreateSession(payload, envelope, func() (terminal.PTYChannel, error) {
			close(started)
			<-release
			return terminal.NewMockEchoPTY(80, 24), nil
		})
		opened <- err
	}()
	<-started
	shutdownDone := make(chan struct{})
	go func() { manager.Shutdown(protocol.TerminalReasonServiceRestart); close(shutdownDone) }()
	close(release)
	if err := <-opened; err != nil {
		t.Fatal(err)
	}
	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked")
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() { manager.Shutdown(protocol.TerminalReasonServiceRestart) })
	}
	wg.Wait()
	var factoryCalls atomic.Int64
	for _, id := range []string{"session-1", "session-2"} {
		envelope.TerminalSessionID = id
		_, _, err := manager.GetOrCreateSession(payload, envelope, func() (terminal.PTYChannel, error) { factoryCalls.Add(1); return terminal.NewMockEchoPTY(80, 24), nil })
		if !errors.Is(err, terminal.ErrSessionClosed) {
			t.Fatalf("delayed open=%v", err)
		}
	}
	if manager.ActiveCount() != 0 || factoryCalls.Load() != 0 || ended.Load() != 1 || wrongReason.Load() {
		t.Fatalf("shutdown leaked/reopened sessions or changed ENDED: active=%d allocations=%d ended=%d", manager.ActiveCount(), factoryCalls.Load(), ended.Load())
	}
}
