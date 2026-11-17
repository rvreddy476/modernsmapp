package http

import (
	"log/slog"

	"github.com/atpost/identity-shared/health"
	sharedmiddleware "github.com/atpost/identity-shared/middleware"
	"github.com/atpost/identity-shared/o11y/metrics"
	"github.com/gin-gonic/gin"
)

// MetricsServiceName is the chart's service.name
// (deploy/services/identity-auth-service): the chart's alert rules build the
// metric prefix atpost_identity_auth_service_http_* from it.
const MetricsServiceName = "identity-auth-service"

// RouterConfig is what NewRouter needs besides the handler.
type RouterConfig struct {
	Logger         *slog.Logger
	TrustedProxies []string
	// Probes serves /livez and /readyz; nil means no dependency checks.
	Probes *health.Checker
	// Auth guards the consumer routes, AdminAuth the admin console's
	// (admin cookie only, sk=admin only).
	Auth      gin.HandlerFunc
	AdminAuth gin.HandlerFunc
	CSRF      gin.HandlerFunc
}

// NewRouter builds the engine auth-service serves; main.go and the tests use
// the same one.
//
// The probe and scrape routes go on first, with no middleware: the kubelet
// and Prometheus send no credentials, and nothing that gates, limits or
// times a request may sit in front of them.
func NewRouter(h *Handler, cfg RouterConfig) (*gin.Engine, error) {
	r := gin.New()

	probes := cfg.Probes
	if probes == nil {
		probes = health.New("auth-service", cfg.Logger)
	}
	probes.RegisterRoutes(r)
	m := metrics.New(MetricsServiceName)
	health.Bare(r).GET(metrics.Path, m.Handler())

	// Body cap first: the rate limiters and idempotency middleware read the
	// whole body, so the limit must be in place before any of them runs.
	r.Use(BodyLimitMiddleware(MaxRequestBodyBytes))
	// Tracing next so the span context is available to RequestID + Logger
	// downstream. Same ordering as the Architecture-side services.
	r.Use(sharedmiddleware.OtelTracing("auth-service"))
	r.Use(m.Middleware())
	r.Use(RequestIDMiddleware())
	r.Use(LoggerMiddleware(cfg.Logger))
	r.Use(RecoveryMiddleware(cfg.Logger))
	// CORS is handled by the API Gateway — do not add duplicate headers here.

	proxies := cfg.TrustedProxies
	if len(proxies) == 0 {
		proxies = nil
	}
	if err := r.SetTrustedProxies(proxies); err != nil {
		return nil, err
	}

	h.RegisterRoutes(r, cfg.Auth, cfg.CSRF)
	h.RegisterAdminSessionRoutes(r, cfg.AdminAuth)
	h.RegisterWebAuthnRoutes(r, cfg.Auth, cfg.CSRF) // no-op unless built with -tags webauthn
	h.RegisterDocsRoutes(r)
	return r, nil
}
