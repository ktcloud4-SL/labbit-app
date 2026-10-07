package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/observability"
)

func withMetrics(m *observability.HTTPMetrics, mux *http.ServeMux, routes map[string]string, next http.Handler) http.Handler {
	if m == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only registered patterns are labels, including when Origin rejects before routing.
		// ServeMux may return a redirect pattern derived from the request; never use it directly.
		route := routeTemplate(mux, routes, r)
		method := metricMethod(r.Method)
		start := time.Now()
		response := &metricResponse{ResponseWriter: w}
		m.Inflight.Inc()
		completed := false
		defer func() {
			m.Inflight.Dec()
			status := response.status
			if status == 0 {
				status = http.StatusOK
				if !completed {
					status = http.StatusInternalServerError
				}
			}
			m.Requests.WithLabelValues(method, route, strconv.Itoa(status/100)+"xx").Inc()
			m.Duration.WithLabelValues(method, route).Observe(time.Since(start).Seconds())
		}()
		next.ServeHTTP(response, r)
		completed = true
	})
}

func metricMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return method
	default:
		return "unknown"
	}
}

type metricResponse struct {
	http.ResponseWriter
	status int
}

func (w *metricResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *metricResponse) WriteHeader(status int) {
	if w.status == 0 && status >= 200 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *metricResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}
