package app

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/server/maintenance"
	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

func processReadback(c *wc.Control, g *maintenance.Gate) (wc.ProcessReadback, bool) {
	b, ok := c.ProcessBinding()
	if !ok || g == nil {
		return wc.ProcessReadback{}, false
	}
	state, active := g.Readback()
	closed := state == "closed"
	for _, n := range active {
		closed = closed && n == 0
	}
	return wc.ProcessReadback{Binding: b, Control: c.Snapshot(), MaintenanceState: state,
		AdmissionClosed: closed, Active: active, ObservedAt: time.Now().UTC().Truncate(time.Microsecond)}, true
}

// durableStatus ties the process to the current PostgreSQL control row.
func durableStatus(ctx context.Context, c *wc.Control, g *maintenance.Gate) (wc.ProcessReadback, bool) {
	binding, ok := c.ProcessBinding()
	if !ok || binding.Validate() != nil {
		return wc.ProcessReadback{}, false
	}
	row, err := c.DurableReport(ctx)
	if err != nil || !row.State.Matches(binding) || row.PendingResumes != 0 || row.PendingRestrictions != 0 {
		return wc.ProcessReadback{}, false
	}
	snapshot, ok := processReadback(c, g)
	if !ok || snapshot.Binding != binding || !snapshot.AdmissionClosed || snapshot.Control.Unverified || snapshot.Control.Mode != row.State.Mode {
		return wc.ProcessReadback{}, false
	}
	snapshot.ObservedAt = row.ObservedAt.UTC().Truncate(time.Microsecond)
	return snapshot, true
}

type standbyWorkerReadback struct {
	Scope       string         `json:"scope"`
	Durable     bool           `json:"durable"`
	Mode        wc.Mode        `json:"mode"`
	InFlight    uint64         `json:"in_flight"`
	Maintenance string         `json:"maintenance"`
	Active      map[string]int `json:"active"`
	ObservedAt  time.Time      `json:"observed_at_utc"`
}

func writeStandbyStatus(w http.ResponseWriter, c *wc.Control, g *maintenance.Gate) {
	state, active := g.Readback()
	snapshot := c.Snapshot()
	if c.Persistent() || snapshot.Mode != wc.PauseNew || snapshot.Unverified || state != "closed" {
		w.WriteHeader(http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(standbyWorkerReadback{
		Scope: "STANDBY_PROCESS_ONLY", Mode: snapshot.Mode, InFlight: snapshot.InFlight,
		Maintenance: state, Active: active, ObservedAt: time.Now().UTC().Truncate(time.Microsecond),
	})
}

// Admin-only readback. No public route, mutations, secrets, or DB queries.
// Deployment must restrict the admin listener and Kubernetes pods/proxy RBAC.
func withDRReadback(next http.Handler, c *wc.Control, g *maintenance.Gate) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dr-control/status" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		snapshot, ok := processReadback(c, g)
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(snapshot)
	})
}
