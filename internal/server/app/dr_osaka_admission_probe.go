package app

import (
	"encoding/json"
	"net/http"
	"slices"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/server/labexecution"
)

func isReconciliationClient(r *http.Request) bool {
	return r.TLS != nil && len(r.TLS.VerifiedChains) > 0 && len(r.TLS.VerifiedChains[0]) > 0 &&
		slices.Contains(r.TLS.VerifiedChains[0][0].DNSNames, reconciliationClient)
}

// Only mount this mux on the private Osaka reconciliation TLS listener.
// The API authenticates the caller and its service prevents probe mutations
// even if maintenance reopens during the request.
func withOsakaAdmissionProbe(next, api http.Handler, mode func() string, standby bool) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", next)
	check := func(use func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if !isReconciliationClient(r) {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			if api == nil || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" {
				http.NotFound(w, r)
				return
			}
			use(w, r)
		}
	}
	mux.HandleFunc("GET /api/v1/me", check(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || mode() != "closed" {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		api.ServeHTTP(w, r)
	}))
	if standby {
		return mux
	}
	probe := check(func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("target"))
		if err != nil || id == uuid.Nil || id.String() != r.PathValue("target") ||
			r.ContentLength < 0 || r.ContentLength > 4096 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch mode() {
		case "open":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": "dr_probe_gate_open"})
		case "closed":
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			api.ServeHTTP(w, r.WithContext(labexecution.WithAdmissionProbe(r.Context())))
		default:
			w.WriteHeader(http.StatusPreconditionFailed)
		}
	})
	mux.HandleFunc("POST /api/v1/classes/{target}/lab-executions", probe)
	mux.HandleFunc("POST /api/v1/lab-instances/{target}/reset", probe)
	mux.HandleFunc("POST /api/v1/lab-executions/{target}/cleanup", probe)
	return mux
}
