package operation

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/server/maintenance"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

func TestMaintenanceHoldsPreclaimedExecutionAndStopsNewClaims(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mode")
	write := func(mode string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(mode), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("open")
	gate := maintenance.New(path)
	claimed := make(chan struct{})
	resume := make(chan struct{})
	done := make(chan error, 1)
	store := workerStoreFake{
		claim: func(context.Context, uuid.UUID, time.Duration) (repository.OperationItemClaim, error) {
			close(claimed)
			<-resume
			return repository.OperationItemClaim{}, nil
		},
		renew: func(context.Context, repository.OperationItemLease, time.Duration) (time.Time, error) {
			return time.Now().Add(time.Minute), nil
		},
	}
	executed := false
	w, err := NewWorker(WorkerOptions{Store: store, LeaseDuration: time.Minute, Admission: gate.Enter, Execute: func(context.Context, repository.OperationItemClaim) error { executed = true; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, err := w.RunOne(t.Context()); done <- err }()
	<-claimed
	write("closed")
	if closed, active := gate.Snapshot(); !closed || active["worker"] != 1 {
		t.Fatalf("preclaim missing: %v", active)
	}
	if got, err := w.RunOne(t.Context()); got || err != nil {
		t.Fatalf("new claim: %v %v", got, err)
	}
	close(resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !executed {
		t.Fatal("preclaim did not drain")
	}
	if _, active := gate.Snapshot(); active["worker"] != 0 {
		t.Fatal(active)
	}
}
