package app

import (
	"encoding/json"
	"github.com/ktcloud4-SL/labbit-app/internal/server/maintenance"
	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
	"net/http"
	"time"
)

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
		b, ok := c.ProcessBinding()
		if !ok || g == nil {
			http.NotFound(w, r)
			return
		}
		state, active := g.Readback()
		closed := state == "closed"
		for _, n := range active {
			closed = closed && n == 0
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(wc.ProcessReadback{Binding: b, Control: c.Snapshot(), MaintenanceState: state, AdmissionClosed: closed, Active: active, ObservedAt: time.Now().UTC().Truncate(time.Microsecond)})
	})
}
