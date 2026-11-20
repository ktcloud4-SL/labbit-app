package workercontrol

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestModesAndUninitializedFailClosed(t *testing.T) {
	for _, tt := range []struct {
		raw     string
		mode    Mode
		invalid bool
	}{
		{"", Active, false}, {"ACTIVE", Active, false}, {" PAUSE_NEW ", PauseNew, false},
		{"RECONCILE_ONLY", ReconcileOnly, false}, {"active", 0, true}, {"private-invalid", 0, true},
	} {
		t.Run(tt.raw, func(t *testing.T) {
			m, err := ParseMode(tt.raw)
			if tt.invalid {
				if !errors.Is(err, ErrInvalidMode) || strings.Contains(err.Error(), tt.raw) {
					t.Fatalf("parse=%v", err)
				}
				return
			}
			if err != nil || m != tt.mode {
				t.Fatalf("mode=%v err=%v", m, err)
			}
			c, err := New(m)
			if err != nil {
				t.Fatal(err)
			}
			for _, reconcile := range []bool{false, true} {
				var end func()
				if reconcile {
					end, err = c.AcquireReconciliation(t.Context())
				} else {
					end, err = c.AcquireMutation(t.Context())
				}
				allowed := m == Active || (m == ReconcileOnly && reconcile)
				if allowed {
					if err != nil || c.Snapshot().InFlight != 1 {
						t.Fatalf("permit=%v", err)
					}
					end()
					end() // A duplicate release cannot underflow or open the gate.
				} else if !errors.Is(err, ErrPaused) || end != nil {
					t.Fatalf("blocked=%v", err)
				}
			}
			if s := c.Snapshot(); s.InFlight != 0 || s.Quiesced != (m == PauseNew) {
				t.Fatalf("state=%+v", s)
			}
		})
	}
	if c, err := New(Mode(255)); c != nil || !errors.Is(err, ErrInvalidMode) {
		t.Fatal("invalid mode accepted")
	}
	for _, c := range []*Control{nil, {}} {
		if end, err := c.AcquireMutation(t.Context()); end != nil || !errors.Is(err, ErrPaused) {
			t.Fatal("uninitialized mutation allowed")
		}
		if end, err := c.AcquireReconciliation(t.Context()); end != nil || !errors.Is(err, ErrPaused) {
			t.Fatal("uninitialized reconcile allowed")
		}
		if err := c.Pause(t.Context()); !errors.Is(err, ErrPaused) || c.Snapshot().Quiesced {
			t.Fatal("uninitialized pause claimed verified")
		}
	}
}

func TestPauseWaitsForLocalBoundariesAndDoesNotReopenAfterCancellation(t *testing.T) {
	c, _ := New(Active)
	end, _ := c.AcquireMutation(t.Context())
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- c.Pause(ctx) }()
	deadline := time.After(2 * time.Second)
	for c.Snapshot().Mode != PauseNew {
		select {
		case <-deadline:
			t.Fatal("pause not latched")
		default:
			runtime.Gosched()
		}
	}
	if c.Snapshot().Quiesced {
		t.Fatal("claimed drained with held permit")
	}
	if _, err := c.AcquireMutation(t.Context()); !errors.Is(err, ErrPaused) {
		t.Fatal("pause allowed mutation")
	}
	if _, err := c.AcquireReconciliation(t.Context()); !errors.Is(err, ErrPaused) {
		t.Fatal("pause allowed reconcile")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("pause=%v", err)
	}
	if s := c.Snapshot(); s.Mode != PauseNew || s.Quiesced {
		t.Fatalf("cancellation lost latch: %+v", s)
	}
	end()
	if err := c.Pause(t.Context()); err != nil || !c.Snapshot().Quiesced {
		t.Fatal("repeat pause failed to observe drain")
	}
	if _, err := c.AcquireMutation(t.Context()); !errors.Is(err, ErrPaused) {
		t.Fatal("repeat pause reopened gate")
	}
}

func TestConcurrentPauseDoesNotPermitWorkAfterDrain(t *testing.T) {
	c, _ := New(Active)
	var wg sync.WaitGroup
	var completed atomic.Int32
	start := make(chan struct{})
	for range 96 {
		wg.Go(func() {
			<-start
			end, err := c.AcquireMutation(t.Context())
			if err == nil {
				completed.Add(1)
				end()
			} else if !errors.Is(err, ErrPaused) {
				t.Errorf("acquire=%v", err)
			}
		})
	}
	close(start)
	if err := c.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	before := completed.Load()
	for range 32 {
		if end, err := c.AcquireMutation(t.Context()); end != nil || !errors.Is(err, ErrPaused) {
			t.Fatal("mutation after drain")
		}
	}
	if !c.Snapshot().Quiesced || before != completed.Load() {
		t.Fatal("unstable drain")
	}
}

func TestCanceledRequestDoesNotChangeModeOrAcquire(t *testing.T) {
	c, _ := New(Active)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := c.Pause(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := c.AcquireMutation(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if s := c.Snapshot(); s.Mode != Active || s.InFlight != 0 {
		t.Fatalf("state=%+v", s)
	}
}
