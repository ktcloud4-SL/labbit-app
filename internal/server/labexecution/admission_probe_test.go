package labexecution

import (
	"context"
	"errors"
	"testing"
)

func TestAdmissionProbeCannotEnterOperationWhenGateReopens(t *testing.T) {
	closed, active := true, 0
	service := NewService(nil, func(kind string) (func(), bool) {
		if kind != "http" {
			t.Fatal("probe used a different production admission gate")
		}
		if closed {
			return nil, false
		}
		active++
		return func() { active-- }, true
	})
	ctx := WithAdmissionProbe(context.Background())
	if release, err := service.enter(ctx); release != nil || !errors.Is(err, ErrMaintenance) {
		t.Fatalf("closed gate did not report ordinary maintenance: %v", err)
	}
	closed = false
	if release, err := service.enter(ctx); release != nil || !errors.Is(err, ErrAdmissionProbeOpen) || active != 0 {
		t.Fatalf("reopened gate admitted a probe or leaked a slot: %v, active %d", err, active)
	}
	release, err := service.enter(context.Background())
	if err != nil || active != 1 {
		t.Fatalf("ordinary request was blocked: %v", err)
	}
	release()
}
