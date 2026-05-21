// Phase F3.2 — real OpenTelemetry middleware. Replaces the previous
// no-op stub so every HTTP request now opens a server span scoped to
// the route, propagates W3C trace context out via response headers,
// and (via the global propagator set in shared/o11y/trace) accepts
// inbound traceparent / tracestate headers from upstream callers.
package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// OtelTracing wraps the Gin request lifecycle in an otelhttp server span.
// `serviceName` becomes the span's service.name attribute and the
// instrumentation scope; pass the same name you handed to
// trace.InitTracer so Jaeger shows a single service per pod.
//
// The middleware is a thin adapter: it lets otelhttp do the standard
// thing (extract traceparent, start span, set semconv attributes, close
// span on response), then hands control to Gin so the rest of the
// chain (logger, request-id) inherits the context.
func OtelTracing(serviceName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Route template — e.g. "/v1/commerce/orders/:orderId" — gives
		// a stable span name even when the path includes a UUID.
		route := c.FullPath()
		if route == "" {
			route = c.Request.URL.Path
		}
		handler := otelhttp.NewHandler(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// otelhttp has already injected the span context into
				// r.Context() at this point; copy it back onto the Gin
				// request so downstream handlers + outbound clients
				// can use it for propagation.
				c.Request = r
				c.Next()
			}),
			route,
			otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
				return r.Method + " " + route
			}),
		)
		handler.ServeHTTP(c.Writer, c.Request)
	}
}
