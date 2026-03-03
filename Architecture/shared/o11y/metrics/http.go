package metrics

import (
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// HTTPMetrics holds Prometheus metrics for HTTP request instrumentation.
type HTTPMetrics struct {
	ServiceName     string
	RequestsTotal   *prometheus.CounterVec
	RequestDuration *prometheus.HistogramVec
	ResponseSize    *prometheus.HistogramVec
	InFlight        prometheus.Gauge
}

// NewHTTPMetrics creates and registers HTTP metrics with standard labels.
// The serviceName is used both as the Prometheus subsystem (for metric naming)
// and as the value of the "service" label (for alert rule filtering).
// Pass an empty string or omit to use "unknown" as the service name.
func NewHTTPMetrics(opts ...string) *HTTPMetrics {
	serviceName := "unknown"
	if len(opts) > 0 && opts[0] != "" {
		serviceName = opts[0]
	}
	sub := sanitize(serviceName)
	return &HTTPMetrics{
		ServiceName: serviceName,
		RequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "atpost",
				Subsystem: sub,
				Name:      "http_requests_total",
				Help:      "Total number of HTTP requests.",
			},
			[]string{"method", "path", "status", "service"},
		),
		RequestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "atpost",
				Subsystem: sub,
				Name:      "http_request_duration_seconds",
				Help:      "HTTP request duration in seconds.",
				Buckets:   []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
			},
			[]string{"method", "path", "status", "service"},
		),
		ResponseSize: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "atpost",
				Subsystem: sub,
				Name:      "http_response_size_bytes",
				Help:      "HTTP response size in bytes.",
				Buckets:   prometheus.ExponentialBuckets(100, 10, 7),
			},
			[]string{"method", "path", "status", "service"},
		),
		InFlight: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "atpost",
				Subsystem: sub,
				Name:      "http_requests_in_flight",
				Help:      "Current number of HTTP requests being served.",
			},
		),
	}
}

// Handler returns a gin.HandlerFunc that serves the Prometheus /metrics endpoint.
func Handler() gin.HandlerFunc {
	h := promhttp.Handler()
	return func(c *gin.Context) {
		h.ServeHTTP(c.Writer, c.Request)
	}
}

// sanitize replaces hyphens with underscores for Prometheus metric naming.
func sanitize(s string) string {
	out := make([]byte, len(s))
	for i := range s {
		if s[i] == '-' {
			out[i] = '_'
		} else {
			out[i] = s[i]
		}
	}
	return string(out)
}
