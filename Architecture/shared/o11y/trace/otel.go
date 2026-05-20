// Package trace owns OpenTelemetry initialisation + W3C trace-context
// propagation. Phase F3 — replaces the previous no-op stub with a real
// SDK + OTLP/gRPC exporter pointed at the collector configured via
// OTEL_EXPORTER_OTLP_ENDPOINT (defaults to http://jaeger:4317 in our
// docker-compose stack).
package trace

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	apitrace "go.opentelemetry.io/otel/trace"
)

// TracerProvider wraps the SDK provider so callers keep the same API
// signature the stub had (InitTracer returns *TracerProvider; caller
// does defer tp.Shutdown(ctx)). The exported SDK reference is exposed
// for tests / advanced wiring.
type TracerProvider struct {
	ServiceName string
	SDK         *sdktrace.TracerProvider
}

// InitTracer wires the global OpenTelemetry SDK and propagator for the
// calling service. Safe to call multiple times — the global setup is
// idempotent and any failure falls back to a no-op provider so the
// service keeps running uninstrumented rather than crashing on boot.
//
// otlpEndpoint may be empty: we read OTEL_EXPORTER_OTLP_ENDPOINT, then
// fall back to the supplied default. Sampling honours
// OTEL_TRACES_SAMPLER_ARG (0.0–1.0); empty / invalid means always-on.
//
// The W3C TraceContext + Baggage propagator pair is registered as the
// global so HTTP middleware and Kafka header carriers can call
// otel.GetTextMapPropagator() without per-call wiring.
func InitTracer(serviceName, defaultEndpoint string) (*TracerProvider, error) {
	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	// otlptracegrpc takes a bare host:port — strip the http:// the
	// docker-compose convention uses for consistency with HTTP exporters.
	grpcEndpoint := strings.TrimPrefix(endpoint, "http://")
	grpcEndpoint = strings.TrimPrefix(grpcEndpoint, "https://")
	// 4318 is the OTLP/HTTP port the existing env vars target; the gRPC
	// receiver typically listens on 4317. Map automatically so we don't
	// require an env-var migration in lockstep with this change.
	grpcEndpoint = strings.ReplaceAll(grpcEndpoint, ":4318", ":4317")

	tp := &TracerProvider{ServiceName: serviceName}

	// Always install the W3C propagator pair so cross-service header
	// extraction works even if the exporter is mis-configured. This is
	// what makes traceparent forwarding survive an offline collector.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	exporter, err := otlptrace.New(ctx,
		otlptracegrpc.NewClient(
			otlptracegrpc.WithEndpoint(grpcEndpoint),
			otlptracegrpc.WithInsecure(),
			otlptracegrpc.WithTimeout(2*time.Second),
		),
	)
	if err != nil {
		// Fall back to a no-op provider so the service still runs —
		// uninstrumented traces are better than a startup crash.
		slog.Warn("tracing: OTLP exporter init failed; running uninstrumented",
			"service", serviceName, "endpoint", grpcEndpoint, "error", err)
		noop := sdktrace.NewTracerProvider()
		otel.SetTracerProvider(noop)
		tp.SDK = noop
		return tp, nil
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(envOr("SERVICE_VERSION", "dev")),
			semconv.DeploymentEnvironment(envOr("DEPLOY_ENV", "local")),
		),
	)
	if err != nil {
		res = resource.Default()
	}

	sampler := parseSampler(os.Getenv("OTEL_TRACES_SAMPLER_ARG"))

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			sdktrace.WithBatchTimeout(5*time.Second),
			sdktrace.WithMaxExportBatchSize(512),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)
	otel.SetTracerProvider(provider)
	tp.SDK = provider

	slog.Info("tracing: OTLP exporter initialised",
		"service", serviceName, "endpoint", grpcEndpoint, "sampler", samplerName(sampler))
	return tp, nil
}

// Shutdown flushes the exporter. Accepts any context-like value so the
// pre-OTel callers (which passed a bare context.Context or nil) keep
// working without a signature change.
func (tp *TracerProvider) Shutdown(ctx interface{}) error {
	if tp == nil || tp.SDK == nil {
		return nil
	}
	c, ok := ctx.(context.Context)
	if !ok {
		var cancel context.CancelFunc
		c, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
	}
	return tp.SDK.Shutdown(c)
}

// Tracer returns a named tracer from the global provider. Convenience
// wrapper so service code can do `trace.Tracer("commerce.checkout")`
// without pulling in the OTel API import.
func Tracer(name string) apitrace.Tracer {
	return otel.Tracer(name)
}

// SpanFromContext returns the active span (or a no-op one if none). Used
// by middleware/logger to read trace_id + span_id off the request.
func SpanFromContext(ctx context.Context) apitrace.Span {
	return apitrace.SpanFromContext(ctx)
}

// parseSampler honours OTEL_TRACES_SAMPLER_ARG as a 0.0–1.0 ratio.
// Defaults to AlwaysSample for safety in dev; production should set a
// lower ratio (0.1) via env to keep collector cost bounded.
func parseSampler(arg string) sdktrace.Sampler {
	if arg == "" {
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	}
	r, err := strconv.ParseFloat(arg, 64)
	if err != nil || r <= 0 {
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	}
	if r >= 1.0 {
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	}
	return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(r))
}

func samplerName(s sdktrace.Sampler) string {
	return fmt.Sprintf("%T", s)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
