//go:build !backend_router_harness

package app

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultBuildIgnoresHarnessGates(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LABBIT_C1_HARNESS", "WAIT_FOR_OPERATOR")
	t.Setenv("LABBIT_C1_DIRECTORY", dir)
	if err := os.WriteFile(filepath.Join(dir, "approval.json"), []byte("invalid operator file"), 0600); err != nil {
		t.Fatal(err)
	}
	h := newRouterHarness()
	if h.sink() != nil {
		t.Fatal("default build installed Operation Sink")
	}
	h.start(context.Background(), nil, slog.New(slog.DiscardHandler))
	if _, err := os.Stat(filepath.Join(dir, "attempt.jsonl")); !os.IsNotExist(err) {
		t.Fatal("default build accessed harness directory")
	}
}
