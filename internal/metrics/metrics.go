// Package metrics exposes Prometheus metrics: per-route HTTP RED metrics
// (rate, errors, duration), Go runtime and process metrics, and build info.
//
// A dedicated registry is used instead of the global default so that
// dependencies are explicit and tests stay isolated.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NewRegistry returns a registry with Go runtime, process, and build-info
// collectors.
func NewRegistry(version string) *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name:        "todo_api_build_info",
			Help:        "Build information; the value is always 1.",
			ConstLabels: prometheus.Labels{"version": version},
		}, func() float64 { return 1 }),
	)
	return reg
}

// Handler serves the registry in the Prometheus exposition format.
func Handler(reg *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg})
}

// HTTP holds the request metrics.
type HTTP struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	inFlight prometheus.Gauge
}

// NewHTTP creates and registers the HTTP request metrics.
func NewHTTP(reg prometheus.Registerer) *HTTP {
	m := &HTTP{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "HTTP requests processed, by method, route pattern, and status code.",
		}, []string{"method", "route", "code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "http_request_duration_seconds",
			Help: "HTTP request latency, by method and route pattern.",
			// From 1 ms up to the 5 s request timeout.
			Buckets: []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
		}, []string{"method", "route"}),
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "http_requests_in_flight",
			Help: "HTTP requests currently being processed.",
		}),
	}
	reg.MustRegister(m.requests, m.duration, m.inFlight)
	return m
}

// Instrument wraps next, recording metrics under the given route. route must
// be a route pattern such as "/todos/{id}", never the raw URL path: one time
// series per todo ID would explode Prometheus cardinality.
func (m *HTTP) Instrument(route string, next http.Handler) http.Handler {
	labels := prometheus.Labels{"route": route}
	return promhttp.InstrumentHandlerInFlight(m.inFlight,
		promhttp.InstrumentHandlerDuration(m.duration.MustCurryWith(labels),
			promhttp.InstrumentHandlerCounter(m.requests.MustCurryWith(labels), next),
		),
	)
}
