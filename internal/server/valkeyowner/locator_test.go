package valkeyowner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLocator_Lifecycle(t *testing.T) {
	locA, err := NewLocator(Config{
		ValkeyAddr: "127.0.0.1:6379",
		PodID:      "pod-a",
		PodAddr:    "127.0.0.1:8081",
	})
	if err != nil {
		t.Skipf("Valkey not available at 127.0.0.1:6379: %v", err)
	}
	defer locA.Close()

	locB, err := NewLocator(Config{
		ValkeyAddr: "127.0.0.1:6379",
		PodID:      "pod-b",
		PodAddr:    "127.0.0.1:8082",
	})
	if err != nil {
		t.Fatalf("failed to init locB: %v", err)
	}
	defer locB.Close()

	ctx := t.Context()
	connectorID := uuid.New()

	// 1. Initial Lookup should fail with ErrNoOwner
	_, err = locB.LookupConnector(ctx, connectorID)
	if !errors.Is(err, ErrNoOwner) {
		t.Fatalf("LookupConnector on unowned connector err = %v, want ErrNoOwner", err)
	}

	// 2. Pod A registers connector with epoch 2
	if _, err := locA.RegisterConnector(ctx, connectorID, 2, 3*time.Second); err != nil {
		t.Fatalf("RegisterConnector err = %v", err)
	}

	// 3. Pod B looks up connector: should point to Pod A
	owner, err := locB.LookupConnector(ctx, connectorID)
	if err != nil {
		t.Fatalf("LookupConnector err = %v", err)
	}
	if owner.PodID != "pod-a" || owner.PodAddr != "127.0.0.1:8081" || owner.Epoch != 2 {
		t.Fatalf("unexpected owner record: %+v", owner)
	}

	// 4. Stale registration: Pod B tries to register with lower epoch 1 -> should fail with ErrStaleEpoch
	_, err = locB.RegisterConnector(ctx, connectorID, 1, 3*time.Second)
	if !errors.Is(err, ErrStaleEpoch) {
		t.Fatalf("RegisterConnector with lower epoch err = %v, want ErrStaleEpoch", err)
	}

	// 4b. Equal epoch registration: Pod B tries to register with equal epoch 2 -> should also fail with ErrStaleEpoch
	_, err = locB.RegisterConnector(ctx, connectorID, 2, 3*time.Second)
	if !errors.Is(err, ErrStaleEpoch) {
		t.Fatalf("RegisterConnector with equal epoch err = %v, want ErrStaleEpoch", err)
	}

	// 5. Reconnect: Pod B registers with higher epoch 3 -> takes ownership
	if _, err := locB.RegisterConnector(ctx, connectorID, 3, 3*time.Second); err != nil {
		t.Fatalf("RegisterConnector with higher epoch err = %v", err)
	}

	owner2, err := locA.LookupConnector(ctx, connectorID)
	if err != nil {
		t.Fatalf("LookupConnector after epoch 3 err = %v", err)
	}
	if owner2.PodID != "pod-b" || owner2.Epoch != 3 {
		t.Fatalf("expected pod-b to own connector with epoch 3, got: %+v", owner2)
	}

	// 6. Old Pod A tries to refresh epoch 2 -> should fail with ErrStaleEpoch
	err = locA.RefreshConnector(ctx, connectorID, 2, 3*time.Second)
	if !errors.Is(err, ErrStaleEpoch) {
		t.Fatalf("stale refresh err = %v, want ErrStaleEpoch", err)
	}

	// 7. Pod B releases connector
	if err := locB.ReleaseConnector(ctx, connectorID, 3); err != nil {
		t.Fatalf("ReleaseConnector err = %v", err)
	}

	// 8. Lookup after release should return ErrNoOwner
	_, err = locA.LookupConnector(ctx, connectorID)
	if !errors.Is(err, ErrNoOwner) {
		t.Fatalf("Lookup after release err = %v, want ErrNoOwner", err)
	}
}

func TestLocator_AtomicGenerationAndOwnerLossCallback(t *testing.T) {
	locA, err := NewLocator(Config{
		ValkeyAddr: "127.0.0.1:6379",
		PodID:      "pod-a",
		PodAddr:    "127.0.0.1:8081",
	})
	if err != nil {
		t.Skipf("Valkey unavailable: %v", err)
	}
	defer locA.Close()

	locB, err := NewLocator(Config{
		ValkeyAddr: "127.0.0.1:6379",
		PodID:      "pod-b",
		PodAddr:    "127.0.0.1:8082",
	})
	if err != nil {
		t.Skipf("Valkey unavailable: %v", err)
	}
	defer locB.Close()

	ctx := context.Background()
	connectorID := uuid.New()

	lostA := make(chan struct{})
	onLostA := func() {
		select {
		case <-lostA:
		default:
			close(lostA)
		}
	}

	// 1. Pod A registers with epoch=0 -> atomic monotonic generation allocated via Valkey INCR
	epochA, err := locA.RegisterConnector(ctx, connectorID, 0, 2*time.Second, onLostA)
	if err != nil {
		t.Fatalf("RegisterConnector A err = %v", err)
	}
	if epochA <= 0 {
		t.Fatalf("expected epochA > 0, got %d", epochA)
	}

	// 2. Pod B registers with epoch=0 -> atomic monotonic generation must be strictly greater than epochA
	epochB, err := locB.RegisterConnector(ctx, connectorID, 0, 2*time.Second)
	if err != nil {
		t.Fatalf("RegisterConnector B err = %v", err)
	}
	if epochB <= epochA {
		t.Fatalf("expected epochB (%d) > epochA (%d)", epochB, epochA)
	}

	// 3. Pod A's lease renewal should detect STALE_EPOCH and invoke onLost callback
	select {
	case <-lostA:
		// Succeeded: onLost was invoked
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for onLost callback on Pod A after Pod B takeover")
	}

	// Clean up Pod B
	_ = locB.ReleaseConnector(ctx, connectorID, epochB)
}

func TestLocator_TerminalOwner(t *testing.T) {
	loc, err := NewLocator(Config{
		ValkeyAddr: "127.0.0.1:6379",
		PodID:      "pod-a",
		PodAddr:    "127.0.0.1:8081",
	})
	if err != nil {
		t.Skipf("Valkey not available: %v", err)
	}
	defer loc.Close()

	ctx := t.Context()
	sessionID := "ts-" + uuid.NewString()
	connectorID := uuid.New()

	// Initial lookup should return ErrNoOwner
	_, err = loc.LookupTerminal(ctx, sessionID)
	if !errors.Is(err, ErrNoOwner) {
		t.Fatalf("LookupTerminal before register err = %v, want ErrNoOwner", err)
	}

	// Register terminal
	if err := loc.RegisterTerminal(ctx, sessionID, connectorID, 1, 10*time.Minute); err != nil {
		t.Fatalf("RegisterTerminal err = %v", err)
	}

	rec, err := loc.LookupTerminal(ctx, sessionID)
	if err != nil {
		t.Fatalf("LookupTerminal err = %v", err)
	}
	if rec.TerminalSessionID != sessionID || rec.PodID != "pod-a" || rec.PodAddr != "127.0.0.1:8081" {
		t.Fatalf("unexpected terminal owner record: %+v", rec)
	}

	// Release terminal
	if err := loc.ReleaseTerminal(ctx, sessionID); err != nil {
		t.Fatalf("ReleaseTerminal err = %v", err)
	}

	_, err = loc.LookupTerminal(ctx, sessionID)
	if !errors.Is(err, ErrNoOwner) {
		t.Fatalf("LookupTerminal after release err = %v, want ErrNoOwner", err)
	}
}
