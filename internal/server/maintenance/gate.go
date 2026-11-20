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

func (g *Gate) Snapshot() (bool, map[string]int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	counts := make(map[string]int, len(g.active))
	for k, n := range g.active {
		counts[k] = n
	}
	return g.closedLocked(), counts
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
	for _, kind := range []string{"http", "worker"} {
		ch <- prometheus.MustNewConstMetric(activeDesc, prometheus.GaugeValue, float64(active[kind]), kind)
	}
}
