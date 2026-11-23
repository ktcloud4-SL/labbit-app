// Package maintenance implements a process-local admission barrier backed by a
// platform-owned, read-only file. It never changes configuration itself.
package maintenance

import (
	"io"
	"os"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

type Gate struct {
	mu     sync.Mutex
	path   string
	active map[string]int
}

func New(path string) *Gate { return &Gate{path: path, active: make(map[string]int)} }

func (g *Gate) Configured() bool { return g != nil && g.path != "" }

// ObserveReconciliation tracks a whole query execution, including result writes.
// This is telemetry, NOT permission: callers must use the persistent, exact
// RECONCILE_ONLY binding at claim, prepare and Router send boundaries.
func (g *Gate) ObserveReconciliation() func() {
	g.mu.Lock()
	g.active["reconciliation"]++
	g.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { g.mu.Lock(); defer g.mu.Unlock(); g.active["reconciliation"]-- }) }
}

// LocalResumeReady is an additional local check, never all-Pod/Writer proof.
// Missing/unreadable/invalid files block admissions but are NOT verified closed.
// Application code never writes the platform-owned file.
func (g *Gate) LocalResumeReady() bool {
	if !g.Configured() {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	f, err := os.Open(g.path)
	if err != nil {
		return false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 17))
	if err != nil || len(b) > 16 || strings.TrimSpace(string(b)) != "closed" {
		return false
	}
	for _, n := range g.active {
		if n != 0 {
			return false
		}
	}
	return true
}

// closedLocked reads on every admission, including the first after restart.
// Missing, unreadable, oversized and unknown configured inputs fail closed.
func (g *Gate) closedLocked() bool {
	if g.path == "" {
		return false
	}
	f, err := os.Open(g.path)
	if err != nil {
		return true
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 17))
	return err != nil || len(b) > 16 || strings.TrimSpace(string(b)) != "open"
}

// Enter and Snapshot serialize file observation with registration. Once a
// closed snapshot is observed, every earlier admitted boundary is counted.
// The owner must hold release through the entire handler/claim execution.
func (g *Gate) Enter(kind string) (release func(), ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closedLocked() {
		return nil, false
	}
	g.active[kind]++
	var once sync.Once
	return func() { once.Do(func() { g.mu.Lock(); defer g.mu.Unlock(); g.active[kind]-- }) }, true
}

// EnterControlledWorker checks file integrity and tracks Worker lifetime but
// permits a verified closed API barrier. It is telemetry/file validation, NOT
// Worker authorization: production assembly only selects it with an explicit
// APPROVED_CONTROL policy plus a persistent immutable binding. Every claim,
// prepare and Router dispatch must still pass the authoritative control Store.
// Default Enter behavior, including PR194 closed Worker blocking, is unchanged.
func (g *Gate) EnterControlledWorker(kind string) (func(), bool) {
	if !g.Configured() || kind != "worker" {
		return nil, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	f, err := os.Open(g.path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 17))
	mode := strings.TrimSpace(string(b))
	if err != nil || len(b) > 16 || (mode != "open" && mode != "closed") {
		return nil, false
	}
	g.active["worker"]++
	var once sync.Once
	return func() { once.Do(func() { g.mu.Lock(); defer g.mu.Unlock(); g.active["worker"]-- }) }, true
}

func (g *Gate) Snapshot() (bool, map[string]int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	counts := make(map[string]int, len(g.active))
	for k, n := range g.active {
		counts[k] = n
	}
	return g.closedLocked(), counts
}

// Readback observes exact configuration and counts under one lock. A file that
// fails closed is not evidence of a verified closed configuration.
func (g *Gate) Readback() (string, map[string]int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	counts := map[string]int{"http": 0, "worker": 0, "reconciliation": 0}
	for k, n := range g.active {
		counts[k] = n
	}
	if g.path == "" {
		return "NOT_VERIFIED", counts
	}
	f, err := os.Open(g.path)
	if err != nil {
		return "NOT_VERIFIED", counts
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 17))
	state := strings.TrimSpace(string(b))
	if err != nil || len(b) > 16 || (state != "open" && state != "closed") {
		return "NOT_VERIFIED", counts
	}
	return state, counts
}

var closedDesc = prometheus.NewDesc("labbit_maintenance_closed", "One when this process refuses maintenance admissions.", nil, nil)
var activeDesc = prometheus.NewDesc("labbit_maintenance_active", "Admitted operation requests and worker boundaries still executing.", []string{"kind"}, nil)

func (g *Gate) Describe(ch chan<- *prometheus.Desc) { ch <- closedDesc; ch <- activeDesc }
func (g *Gate) Collect(ch chan<- prometheus.Metric) {
	closed, active := g.Snapshot()
	v := 0.0
	if closed {
		v = 1
	}
	ch <- prometheus.MustNewConstMetric(closedDesc, prometheus.GaugeValue, v)
	for _, kind := range []string{"http", "worker", "reconciliation"} {
		ch <- prometheus.MustNewConstMetric(activeDesc, prometheus.GaugeValue, float64(active[kind]), kind)
	}
}
