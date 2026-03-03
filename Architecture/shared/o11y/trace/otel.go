package trace

import "log/slog"

// TracerProvider is a no-op tracer provider stub.
// Replace with full OpenTelemetry implementation when otel deps are added.
type TracerProvider struct{ ServiceName string }

// InitTracer returns a stub tracer provider for services that don't yet have OTel.
// To enable full distributed tracing, add go.opentelemetry.io/otel to go.mod and
// replace this file with the full OTLP HTTP exporter implementation.
func InitTracer(serviceName, otlpEndpoint string) (*TracerProvider, error) {
	slog.Info("tracing: stub provider initialized (OTel not configured)",
		"service", serviceName,
		"otlp_endpoint", otlpEndpoint)
	return &TracerProvider{ServiceName: serviceName}, nil
}

// Shutdown is a no-op for the stub provider. It accepts any context-like value
// so callers can pattern-match the real OTel API (tp.Shutdown(ctx)).
func (tp *TracerProvider) Shutdown(_ interface{}) error { return nil }
