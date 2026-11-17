// Package metrics exports the Prometheus metrics the Helm chart scrapes and
// alerts on (charts/atpost-service: ServiceMonitor GET /metrics on the http
// port, no credentials; PrometheusRule group "<service>.default").
//
// The metric names, labels and buckets match Architecture/shared/o11y/metrics
// and Architecture/shared/middleware/metrics.go, which chat-service (its own
// Go workspace) cannot import:
//
//	atpost_<service with - as _>_http_requests_total{method,path,status,service}
//	atpost_<service with - as _>_http_request_duration_seconds{...}  (histogram)
//
// plus the Go runtime and process collectors. Each HTTPMetrics has its own
// registry, so nothing is registered globally and tests can build several.
package metrics

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Path is the chart's scrape path (serviceMonitor.path).
const Path = "/metrics"

// UnmatchedPath labels a request no route matched, as the shared middleware
// does, so scanners cannot create one series per URL.
const UnmatchedPath = "unmatched"

var labelNames = []string{"method", "path", "status", "service"}

// HTTPMetrics records HTTP requests for one service.
type HTTPMetrics struct {
	service  string
	registry *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

// NewHTTP builds the metrics for a service. serviceName must be the chart's
// service.name (for example "chat-message-service"): the alert rules derive
// the metric prefix from it.
func NewHTTP(serviceName string) *HTTPMetrics {
	sub := strings.ReplaceAll(serviceName, "-", "_")
	m := &HTTPMetrics{
		service:  serviceName,
		registry: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "atpost",
			Subsystem: sub,
			Name:      "http_requests_total",
			Help:      "Total number of HTTP requests.",
		}, labelNames),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "atpost",
			Subsystem: sub,
			Name:      "http_request_duration_seconds",
			Help:      "HTTP request duration in seconds.",
			Buckets:   []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}, labelNames),
	}
	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.requests,
		m.duration,
	)
	return m
}

// Observe records one finished request. routePattern is the matched route
// pattern (gin's c.FullPath()), never the raw URL; empty means no route
// matched.
func (m *HTTPMetrics) Observe(method, routePattern string, status int, took time.Duration) {
	if routePattern == "" {
		routePattern = UnmatchedPath
	}
	code := strconv.Itoa(status)
	m.requests.WithLabelValues(method, routePattern, code, m.service).Inc()
	m.duration.WithLabelValues(method, routePattern, code, m.service).Observe(took.Seconds())
}

// Wrap serves GET /metrics itself and hands every other request to next
// untouched. Like health.Wrap it sits outside the service's router, so the
// router's auth middleware cannot refuse the scraper, which sends no
// credentials.
func (m *HTTPMetrics) Wrap(next http.Handler) http.Handler {
	scrape := promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == Path && r.Method == http.MethodGet {
			scrape.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
