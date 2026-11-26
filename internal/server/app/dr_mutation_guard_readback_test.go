package app

import (
	"context"
	"errors"
	"testing"
	"time"

	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

func TestObserveMutationGuardExercisesWorkerBoundariesWithoutSendingWork(t *testing.T) {
	for _, mode := range []wc.Mode{wc.PauseNew, wc.ReconcileOnly} {
		c, err := wc.New(mode)
		if err != nil {
			t.Fatal(err)
		}
		state := wc.ProcessReadback{Binding: wc.Binding{Mode: mode}, ObservedAt: time.Now()}
		observe := func(context.Context) (wc.ProcessReadback, bool) { return state, true }
		report, ok := observeMutationGuard(context.Background(), observe, c.AcquireMutationFor)
		if !ok || !report.ClaimBlocked || !report.DispatchBlocked || c.Snapshot().InFlight != 0 {
			t.Fatalf("paused %v gate=%+v present=%v", mode, report, ok)
		}
	}
}

func TestObserveMutationGuardDisallowsActiveDriftAndIncompleteObservation(t *testing.T) {
	state := wc.ProcessReadback{Binding: wc.Binding{Mode: wc.Active}, ObservedAt: time.Now()}
	observe := func(context.Context) (wc.ProcessReadback, bool) { return state, true }
	_, ok := observeMutationGuard(context.Background(), observe, func(context.Context, wc.Phase) (func(), error) {
		t.Fatal("attempted a boundary on an already Active worker")
		return nil, nil
	})
	if ok {
		t.Fatal("Active could not be used as a paused witness")
	}
	state.Binding.Mode = wc.PauseNew
	if _, ok := observeMutationGuard(context.Background(), observe, func(context.Context, wc.Phase) (func(), error) {
		return nil, errors.New("incomplete")
	}); ok {
		t.Fatal("transport failure was treated as observed denial")
	}
	if _, ok := observeMutationGuard(context.Background(), observe, func(context.Context, wc.Phase) (func(), error) {
		state.Binding.Epoch++
		return nil, wc.ErrPaused
	}); ok {
		t.Fatal("epoch drift was treated as observed denial")
	}
}
