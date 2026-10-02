package realtime

import "net/http"

// rejected records only failed admission, not errors on an already bound connection.
func (r *Relay) rejected(status int) {
	if r.metrics == nil {
		return
	}
	switch status {
	case http.StatusUnauthorized:
		r.metrics.Unauthorized.Inc()
	case http.StatusForbidden:
		r.metrics.Forbidden.Inc()
	case http.StatusInternalServerError, http.StatusServiceUnavailable:
		r.metrics.Unavailable.Inc()
	default:
		r.metrics.InvalidRequest.Inc()
	}
}

func (r *Relay) upgradeRejected(w http.ResponseWriter, _ *http.Request, status int, _ error) {
	r.rejected(status)
	// Preserve gorilla's default Upgrade error response.
	w.Header().Set("Sec-Websocket-Version", "13")
	http.Error(w, http.StatusText(status), status)
}
