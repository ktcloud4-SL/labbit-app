package labexecution

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ktcloud4-SL/labbit-app/internal/server/maintenance"
)

func TestMaintenanceAdmissionPreservesAuthorizationAndReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mode")
	set := func(s string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	set("open")
	t.Run("provision", func(t *testing.T) {
		w := newWorld(t)
		w.svc.admission = maintenance.New(path).Enter
		in := w.input("existing", w.studentA)
		created, err := w.svc.Provision(t.Context(), w.instructor, in)
		if err != nil {
			t.Fatal(err)
		}
		before := w.store.committed.rowCounts()
		set("closed")
		if replay, err := w.svc.Provision(t.Context(), w.instructor, in); err != nil || !replay.Replayed || replay.OperationID != created.OperationID {
			t.Fatalf("replay: %+v %v", replay, err)
		}
		if _, err := w.svc.Provision(t.Context(), w.studentA, in); !errors.Is(err, ErrForbidden) {
			t.Fatalf("authorization: %v", err)
		}
		if _, err := w.svc.Provision(t.Context(), w.instructor, w.input("new", w.studentA)); !errors.Is(err, ErrMaintenance) {
			t.Fatalf("new: %v", err)
		}
		if got := w.store.committed.rowCounts(); got != before {
			t.Fatalf("rows changed: %v -> %v", before, got)
		}
	})
	for _, kind := range []string{"reset", "cleanup"} {
		t.Run(kind, func(t *testing.T) {
			set("open")
			w := newMutationWorld(t)
			w.svc.admission = maintenance.New(path).Enter
			invoke := func(key string) (MutationAccepted, error) {
				if kind == "reset" {
					return w.reset(w.instructor, w.aLI, key)
				}
				return w.cleanup(w.instructor, key)
			}
			created, err := invoke("existing")
			if err != nil {
				t.Fatal(err)
			}
			before := w.store.committed.rowCounts()
			set("closed")
			replay, err := invoke("existing")
			if err != nil || !replay.Replayed || replay.OperationID != created.OperationID {
				t.Fatalf("replay: %+v %v", replay, err)
			}
			if _, err := invoke("new"); !errors.Is(err, ErrMaintenance) {
				t.Fatalf("new: %v", err)
			}
			if got := w.store.committed.rowCounts(); got != before {
				t.Fatalf("rows changed: %v -> %v", before, got)
			}
		})
	}
}
