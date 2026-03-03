package middleware

import (
	"strconv"
	"time"

	"github.com/atpost/shared/o11y/metrics"
	"github.com/gin-gonic/gin"
)

// Metrics returns a Gin middleware that records Prometheus HTTP metrics.
// It uses c.FullPath() for route patterns to prevent cardinality explosion.
func Metrics(m *metrics.HTTPMetrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path == "/metrics" {
			c.Next()
			return
		}

		m.InFlight.Inc()
		start := time.Now()

		c.Next()

		m.InFlight.Dec()
		duration := time.Since(start).Seconds()
		status := strconv.Itoa(c.Writer.Status())

		path := c.FullPath()
		if path == "" {
			path = "unmatched"
		}

		method := c.Request.Method
		m.RequestsTotal.WithLabelValues(method, path, status).Inc()
		m.RequestDuration.WithLabelValues(method, path, status).Observe(duration)
		m.ResponseSize.WithLabelValues(method, path, status).Observe(float64(c.Writer.Size()))
	}
}
