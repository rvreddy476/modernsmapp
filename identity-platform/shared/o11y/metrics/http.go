// Package metrics exports Prometheus metrics for the identity services: the
// Go and process collectors and the HTTP request metrics the Helm chart's
// default alert rules read (charts/atpost-service/templates/prometheusrule.yaml).
//
// The metric names and labels match Architecture/shared/o11y/metrics and
// Architecture/shared/middleware/metrics.go, which this Go workspace cannot
// import:
//
//	atpost_<service with - as _>_http_requests_total{method,path,status,service}
//	atpost_<service with - as _>_http_request_duration_seconds{method,path,status,service}
//	atpost_<service with - as _>_http_response_size_bytes{method,path,status,service}
//	atpost_<service with - as _>_http_requests_in_flight
//
// <service> must be the chart's service.name (for example
// "identity-auth-service"), since the rules build the metric prefix from it.
package metrics

import (
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Path is where the chart's ServiceMonitor scrapes, with no credentials.
const Path = "/metrics"

// HTTP holds one service's registry and HTTP request metrics. Each instance
// has its own registry, so building two routers in one process (tests) does
// not collide on the global one.
type HTTP struct {
	service  string
	registry *prometheus.Registry

	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	size     *prometheus.HistogramVec
	inFlight prometheus.Gauge
}

// New builds the metrics for a service named as in the chart's service.name.
func New(service string) *HTTP {
	sub := strings.ReplaceAll(service, "-", "_")
	labels := []string{"method", "path", "status", "service"}
	m := &HTTP{
		service:  service,
		registry: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "atpost", Subsystem: sub, Name: "http_requests_total",
			Help: "Total number of HTTP requests.",
		}, labels),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "atpost", Subsystem: sub, Name: "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}, labels),
		size: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "atpost", Subsystem: sub, Name: "http_response_size_bytes",
			Help:    "HTTP response size in bytes.",
			Buckets: prometheus.ExponentialBuckets(100, 10, 7),
		}, labels),
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "atpost", Subsystem: sub, Name: "http_requests_in_flight",
			Help: "Current number of HTTP requests being served.",
		}),
	}
	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.requests, m.duration, m.size, m.inFlight,
	)
	return m
}

// Handler serves the registry in the Prometheus text format.
func (m *HTTP) Handler() gin.HandlerFunc {
	h := promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
	return func(c *gin.Context) { h.ServeHTTP(c.Writer, c.Request) }
}

// Middleware records one observation per request. The path label is the
// route pattern (c.FullPath()), never the raw URL, so an id in the path does
// not become a new series; a request that matches no route is "unmatched".
func (m *HTTP) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		m.inFlight.Inc()
		start := time.Now()

		c.Next()

		m.inFlight.Dec()
		path := c.FullPath()
		if path == "" {
			path = "unmatched"
		}
		method := c.Request.Method
		status := strconv.Itoa(c.Writer.Status())
		m.requests.WithLabelValues(method, path, status, m.service).Inc()
		m.duration.WithLabelValues(method, path, status, m.service).Observe(time.Since(start).Seconds())
		m.size.WithLabelValues(method, path, status, m.service).Observe(float64(c.Writer.Size()))
	}
}
