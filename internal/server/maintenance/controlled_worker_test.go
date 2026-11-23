package maintenance

import (
	"os"
	"path/filepath"
	"testing"
)

func TestControlledWorkerStillRequiresValidFileAndCountsDrain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mode")
	g := New(path)
	for _, value := range []string{"", "unknown", "open extra", "closed                 "} {
		if os.WriteFile(path, []byte(value), 0600) != nil {
			t.Fatal("fixture")
		}
		if _, ok := g.EnterControlledWorker("worker"); ok {
			t.Fatal("invalid mode admitted")
		}
	}
	for _, value := range []string{"closed", "open"} {
		if os.WriteFile(path, []byte(value), 0600) != nil {
			t.Fatal("fixture")
		}
		if _, ok := g.EnterControlledWorker("http"); ok {
			t.Fatal("HTTP bypass")
		}
		release, ok := g.EnterControlledWorker("worker")
		if !ok {
			t.Fatal("valid mode rejected")
		}
		if g.LocalResumeReady() {
			t.Fatal("active Worker lost from drain")
		}
		_, counts := g.Snapshot()
		if counts["worker"] != 1 {
			t.Fatal("missing worker lifetime")
		}
		release()
		release()
		_, counts = g.Snapshot()
		if counts["worker"] != 0 {
			t.Fatal("lifetime leak")
		}
		if value == "closed" {
			if _, ok := g.Enter("http"); ok {
				t.Fatal("API opened")
			}
			if _, ok := g.Enter("worker"); ok {
				t.Fatal("default PR194 changed")
			}
		}
	}
	if os.Remove(path) != nil {
		t.Fatal("fixture cleanup")
	}
	if _, ok := g.EnterControlledWorker("worker"); ok {
		t.Fatal("missing file bypass")
	}
	if _, ok := New("").EnterControlledWorker("worker"); ok {
		t.Fatal("unconfigured bypass")
	}
}
