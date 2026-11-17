package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/atpost/chat-shared/health"
	"github.com/atpost/chat-shared/metrics"
	"github.com/gin-gonic/gin"
)

// RouterDeps is everything NewRouter needs besides the handler.
type RouterDeps struct {
	Logger *slog.Logger
	// Keys feeds the engine-wide auth middleware.
	Keys           JWTKeySet
	TrustedProxies []string
	// Ready holds the /readyz dependency checks. Nil means none.
	Ready *health.Checker
	// Metrics records every request and serves /metrics. Nil builds one.
	Metrics *metrics.HTTPMetrics
}

// NewRouter is the whole call-service HTTP surface, used by cmd/server and by
// the tests.
//
// The Kubernetes probes (GET /livez, /readyz) and the Prometheus scrape
// (GET /metrics) are answered by two outer handlers wrapped AROUND the gin
// engine, so no engine middleware (auth, the internal key, the calls-enabled
// gate, request logging) can refuse them, whatever order it is added in. The
// kubelet and the scraper send no credentials.
func NewRouter(h *Handler, deps RouterDeps) (http.Handler, error) {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Ready == nil {
		deps.Ready = health.New(ServiceName, deps.Logger)
	}
	if deps.Metrics == nil {
		deps.Metrics = metrics.NewHTTP(ServiceName)
	}

	r := gin.New()
	// First, so a request the auth middleware refuses is still counted.
	r.Use(MetricsMiddleware(deps.Metrics))
	r.Use(RequestIDMiddleware())
	r.Use(LoggerMiddleware(deps.Logger))
	r.Use(RecoveryMiddleware(deps.Logger))
	r.Use(CORSMiddleware())
	r.Use(AuthMiddlewareWithKeys(deps.Keys, deps.Logger))

	proxies := deps.TrustedProxies
	if len(proxies) == 0 {
		proxies = nil
	}
	if err := r.SetTrustedProxies(proxies); err != nil {
		return nil, err
	}

	h.RegisterRoutes(r)

	return deps.Ready.Wrap(deps.Metrics.Wrap(r)), nil
}

// ServiceName is the chart's service.name (deploy/services/chat-call-service).
// The chart's alert rules derive the metric prefix from it.
const ServiceName = "chat-call-service"

// MetricsMiddleware records each request under its route pattern, never the
// raw URL.
func MetricsMiddleware(m *metrics.HTTPMetrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		m.Observe(c.Request.Method, c.FullPath(), c.Writer.Status(), time.Since(start))
	}
}
