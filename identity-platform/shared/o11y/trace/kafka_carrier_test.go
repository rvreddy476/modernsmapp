package trace

import (
	"context"
	"strings"
	"testing"

	kafka "github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// TestKafkaHeaderCarrier_RoundTrip ensures that injecting trace context
// into a Kafka header slice and then extracting it on the consumer side
// preserves the trace ID — the basic invariant of F3.3.
func TestKafkaHeaderCarrier_RoundTrip(t *testing.T) {
	// Install the W3C propagator the way InitTracer does in production.
	otel.SetTextMapPropagator(propagation.TraceContext{})

	tp := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(tp)
	defer func() { _ = tp.Shutdown(context.Background()) }()

	ctx, span := tp.Tracer("test").Start(context.Background(), "producer")
	defer span.End()
	wantTraceID := span.SpanContext().TraceID().String()

	var headers []kafka.Header
	InjectKafkaHeaders(ctx, &headers)

	// The propagator must have written a traceparent header.
	var traceparent string
	for _, h := range headers {
		if h.Key == "traceparent" {
			traceparent = string(h.Value)
		}
	}
	if traceparent == "" {
		t.Fatalf("InjectKafkaHeaders did not write traceparent; headers=%+v", headers)
	}
	if !strings.Contains(traceparent, wantTraceID) {
		t.Errorf("traceparent %q does not embed trace id %q", traceparent, wantTraceID)
	}

	// Extracted context on the consumer side must carry the same trace ID.
	consumerCtx := ExtractKafkaHeaders(context.Background(), headers)
	got := SpanFromContext(consumerCtx).SpanContext().TraceID().String()
	if got != wantTraceID {
		t.Errorf("ExtractKafkaHeaders trace id = %q, want %q", got, wantTraceID)
	}
}

func TestKafkaHeaderCarrier_Set_ReplacesExisting(t *testing.T) {
	headers := []kafka.Header{{Key: "traceparent", Value: []byte("old")}}
	c := &KafkaHeaderCarrier{Headers: &headers}
	c.Set("traceparent", "new")
	if got := c.Get("traceparent"); got != "new" {
		t.Errorf("Set did not replace existing: got %q want %q", got, "new")
	}
	if len(headers) != 1 {
		t.Errorf("Set duplicated header; got %d entries", len(headers))
	}
}
