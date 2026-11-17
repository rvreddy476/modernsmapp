package http

import (
	"log/slog"

	"github.com/atpost/identity-shared/health"
	sharedmiddleware "github.com/atpost/identity-shared/middleware"
	"github.com/atpost/identity-shared/o11y/metrics"
	"github.com/gin-gonic/gin"
)

// MetricsServiceName is the chart's service.name
// (deploy/services/identity-profile-service): the chart's alert rules build the
// metric prefix atpost_identity_profile_service_http_* from it.
const MetricsServiceName = "identity-profile-service"

// RouterConfig is what NewRouter needs besides the handler.
type RouterConfig struct {
	Logger         *slog.Logger
	TrustedProxies []string
	// Probes serves /livez and /readyz; nil means no dependency checks.
	Probes *health.Checker
	Auth   gin.HandlerFunc
	CSRF   gin.HandlerFunc
}

// NewRouter builds the engine profile-service serves; main.go and the tests use
// the same one.
//
// The probe and scrape routes go on first, with no middleware: the kubelet
// and Prometheus send no credentials, and RegisterRoutes installs the
// internal-key gate engine-wide.
func NewRouter(h *Handler, cfg RouterConfig) (*gin.Engine, error) {
	r := gin.New()

	probes := cfg.Probes
	if probes == nil {
		probes = health.New("profile-service", cfg.Logger)
	}
	probes.RegisterRoutes(r)
	m := metrics.New(MetricsServiceName)
	health.Bare(r).GET(metrics.Path, m.Handler())

	// Tracing first so spans wrap the rest of the chain and log enrichment
	// picks up trace_id + span_id.
	r.Use(sharedmiddleware.OtelTracing("profile-service"))
	r.Use(m.Middleware())
	r.Use(RequestIDMiddleware())
	r.Use(LoggerMiddleware(cfg.Logger))
	r.Use(RecoveryMiddleware(cfg.Logger))

	proxies := cfg.TrustedProxies
	if len(proxies) == 0 {
		proxies = nil
	}
	if err := r.SetTrustedProxies(proxies); err != nil {
		return nil, err
	}

	h.RegisterRoutes(r, cfg.Auth, cfg.CSRF)
	return r, nil
}
