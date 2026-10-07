package observability

import "github.com/prometheus/client_golang/prometheus"

// HTTPMetrics is registered only by the api role. Route labels come from HTTP route registration.
type HTTPMetrics struct {
	Requests *prometheus.CounterVec
	Duration *prometheus.HistogramVec
	Inflight prometheus.Gauge
}

func NewHTTPMetrics(reg *prometheus.Registry) *HTTPMetrics {
	m := &HTTPMetrics{
		Requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "labbit_http_requests_total", Help: "Completed application HTTP API requests.",
		}, []string{"method", "route", "status_class"}),
		Duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "labbit_http_request_duration_seconds", Help: "Application HTTP API request duration in seconds.",
		}, []string{"method", "route"}),
		Inflight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "labbit_http_inflight_requests", Help: "Application HTTP API requests currently in progress.",
		}),
	}
	reg.MustRegister(m.Requests, m.Duration, m.Inflight)
	return m
}

// RealtimeMetrics exposes only the implemented Terminal/Live channels and fixed rejection reasons.
type RealtimeMetrics struct {
	BrowserConnections     prometheus.Gauge
	BrowserLiveConnections prometheus.Gauge
	ConnectorConnections   prometheus.Gauge
	BrowserReconnects      prometheus.Counter
	ConnectorReconnects    prometheus.Counter
	Unauthorized           prometheus.Counter
	Forbidden              prometheus.Counter
	InvalidRequest         prometheus.Counter
	DrainingRejected       prometheus.Counter
	Unavailable            prometheus.Counter
	Draining               prometheus.Gauge
}

func NewRealtimeMetrics(reg *prometheus.Registry) *RealtimeMetrics {
	connections := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "labbit_realtime_connections", Help: "Bound Terminal WebSocket connections currently open.",
	}, []string{"channel"})
	reconnects := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "labbit_realtime_reconnects_total", Help: "Successful Terminal attachments identified as resumed.",
	}, []string{"channel"})
	rejected := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "labbit_realtime_rejected_connections_total", Help: "Rejected Terminal WebSocket upgrades or attachments.",
	}, []string{"reason"})
	m := &RealtimeMetrics{
		BrowserConnections:     connections.WithLabelValues("browser_terminal"),
		BrowserLiveConnections: connections.WithLabelValues("browser_live"),
		ConnectorConnections:   connections.WithLabelValues("connector_terminal"),
		BrowserReconnects:      reconnects.WithLabelValues("browser_terminal"),
		ConnectorReconnects:    reconnects.WithLabelValues("connector_terminal"),
		Unauthorized:           rejected.WithLabelValues("unauthorized"),
		Forbidden:              rejected.WithLabelValues("forbidden"),
		InvalidRequest:         rejected.WithLabelValues("invalid_request"),
		DrainingRejected:       rejected.WithLabelValues("draining"),
		Unavailable:            rejected.WithLabelValues("unavailable"),
		Draining: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "labbit_realtime_draining", Help: "Whether Realtime shutdown has started (0 or 1).",
		}),
	}
	reg.MustRegister(connections, reconnects, rejected, m.Draining)
	return m
}
