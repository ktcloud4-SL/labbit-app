package maintenance

import (
	"github.com/prometheus/client_golang/prometheus"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestBarrierAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mode")
	write := func(mode string) {
		t.Helper()
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, []byte(mode), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, path); err != nil {
			t.Fatal(err)
		}
	}
	g := New(path)
	if _, ok := g.Enter("http"); ok {
		t.Fatal("missing configured file admitted")
	}
	write("open")
	release, ok := g.Enter("http")
	if !ok {
		t.Fatal("open rejected")
	}
	write("closed")
	for range 2 {
		closed, counts := g.Snapshot()
		if !closed || counts["http"] != 1 {
			t.Fatalf("barrier lost accepted work: %v %v", closed, counts)
		}
		if _, ok := g.Enter("worker"); ok {
			t.Fatal("closed admitted claim")
		}
		if _, ok := New(path).Enter("browser"); ok {
			t.Fatal("restart bypass")
		}
	}
	release()
	release()
	closed, counts := g.Snapshot()
	if !closed || counts["http"] != 0 {
		t.Fatal("release not idempotent")
	}
	for _, mode := range []string{"", "typo", "open garbage", "open                 ", "open" + string(make([]byte, 100))} {
		write(mode)
		if _, ok := g.Enter("preview"); ok {
			t.Fatalf("invalid input admitted")
		}
	}
	write("open")
	for range 2 {
		release, ok := g.Enter("http")
		if !ok {
			t.Fatal("restore failed")
		}
		release()
	}
}

func TestReconciliationTelemetryDoesNotOpenAdmissionAndBlocksLocalResume(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mode")
	if err := os.WriteFile(path, []byte("closed"), 0600); err != nil {
		t.Fatal(err)
	}
	g := New(path)
	done := g.ObserveReconciliation()
	if _, ok := g.Enter("http"); ok {
		t.Fatal("telemetry opened API")
	}
	if g.LocalResumeReady() {
		t.Fatal("active query counted as drained")
	}
	r := prometheus.NewRegistry()
	r.MustRegister(g)
	families, err := r.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range families {
		if f.GetName() != "labbit_maintenance_active" {
			continue
		}
		for _, m := range f.Metric {
			for _, label := range m.Label {
				if label.GetName() == "kind" && label.GetValue() == "reconciliation" && m.GetGauge().GetValue() == 1 {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("query lifetime missing from metrics")
	}
	done()
	done()
	if !g.LocalResumeReady() {
		t.Fatal("idempotent release failed")
	}
}

func TestConcurrentBarrier(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mode")
	if err := os.WriteFile(path, []byte("open"), 0600); err != nil {
		t.Fatal(err)
	}
	g := New(path)
	var wg sync.WaitGroup
	for range 64 {
		wg.Go(func() {
			for range 50 {
				if release, ok := g.Enter("worker"); ok {
					release()
				}
				g.Snapshot()
			}
		})
	}
	// Atomic replacement is the required platform writer boundary.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte("closed"), 0600); err != nil {
		t.Fatal(err)
	}
	// Windows denies replacement while another process-local reader has an
	// open handle. Retry ONLY this fixture's external writer; application gates
	// never retry or mutate the file. Linux keeps its atomic rename semantics.
	deadline := time.Now().Add(3 * time.Second)
	for {
		err := os.Rename(tmp, path)
		if err == nil {
			break
		}
		if runtime.GOOS != "windows" || !os.IsPermission(err) || time.Now().After(deadline) {
			t.Fatal(err)
		}
		runtime.Gosched()
	}
	if closed, _ := g.Snapshot(); !closed {
		t.Fatal("not closed")
	}
	wg.Wait()
	_, active := g.Snapshot()
	if active["worker"] != 0 {
		t.Fatal(active)
	}
	for range 100 {
		if _, ok := g.Enter("worker"); ok {
			t.Fatal("post-barrier admission")
		}
	}
}

func TestDefaultCompatible(t *testing.T) {
	release, ok := New("").Enter("http")
	if !ok {
		t.Fatal("default closed")
	}
	release()
}

func TestIndependentReplicasObserveConfiguredState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mode")
	replicas := []*Gate{New(path), New(path)}
	for _, g := range replicas {
		if _, ok := g.Enter("worker"); ok {
			t.Fatal("missing mode admitted")
		}
	}
	if err := os.WriteFile(path, []byte("open"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, g := range replicas {
		release, ok := g.Enter("http")
		if !ok {
			t.Fatal("open rejected")
		}
		release()
	}
	if err := os.WriteFile(path, []byte("closed"), 0600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, g := range replicas {
		wg.Go(func() {
			for range 100 {
				if _, ok := g.Enter("http"); ok {
					t.Error("closed replica admitted")
				}
			}
		})
	}
	wg.Wait()
}
