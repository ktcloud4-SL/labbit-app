package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/ktcloud4-SL/labbit-app/internal/server/maintenance"
	wc "github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

const mutationGuardPath = reconciliationPath + "/mutation-guard"

type mutationGuardResult struct {
	Before          wc.ProcessReadback `json:"before"`
	After           wc.ProcessReadback `json:"after"`
	ClaimBlocked    bool               `json:"claim_blocked"`
	DispatchBlocked bool               `json:"dispatch_blocked"`
}

func withMutationGuardReadback(next http.Handler, c *wc.Control, gate *maintenance.Gate, standby bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != mutationGuardPath {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if !isReconciliationClient(r) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodGet || standby {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		report, ok := observeMutationGuard(r.Context(), func(ctx context.Context) (wc.ProcessReadback, bool) {
			return durableStatus(ctx, c, gate)
		}, c.AcquireMutationFor)
		if !ok {
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(report)
	})
}

func observeMutationGuard(ctx context.Context, observe func(context.Context) (wc.ProcessReadback, bool),
	acquire func(context.Context, wc.Phase) (func(), error)) (mutationGuardResult, bool) {
	first, ok := observe(ctx)
	if !ok || (first.Binding.Mode != wc.PauseNew && first.Binding.Mode != wc.ReconcileOnly) {
		return mutationGuardResult{}, false
	}
	// Both paths exercise the exact Worker mutation admission. A valid paused
	// process cannot acquire either ticket. No downstream operation is connected.
	blocked := func(phase wc.Phase) (bool, bool) {
		release, err := acquire(ctx, phase)
		if release != nil {
			release()
		}
		return release == nil && errors.Is(err, wc.ErrPaused), release != nil || errors.Is(err, wc.ErrPaused)
	}
	claim, claimVerified := blocked(wc.MutationClaim)
	dispatch, dispatchVerified := blocked(wc.MutationDispatch)
	if !claimVerified || !dispatchVerified {
		return mutationGuardResult{}, false
	}
	last, ok := observe(ctx)
	if !ok || last.Binding != first.Binding || last.ObservedAt.Before(first.ObservedAt) {
		return mutationGuardResult{}, false
	}
	return mutationGuardResult{Before: first, After: last, ClaimBlocked: claim, DispatchBlocked: dispatch}, true
}
