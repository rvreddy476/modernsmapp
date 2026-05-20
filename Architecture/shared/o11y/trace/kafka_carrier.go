// Phase F3.3 — TextMapCarrier adapter over a kafka-go message header slice.
// Lets the global W3C propagator inject traceparent / tracestate / baggage
// into outbound messages and extract them again on the consumer side,
// without dragging the otelkafka dependency into every publisher.
package trace

import (
	"context"

	kafka "github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// KafkaHeaderCarrier implements propagation.TextMapCarrier over a
// *[]kafka.Header. Mutates the slice in place; safe to use as the
// carrier passed to otel.GetTextMapPropagator().Inject(...).
type KafkaHeaderCarrier struct {
	Headers *[]kafka.Header
}

// Compile-time check that we satisfy the carrier interface.
var _ propagation.TextMapCarrier = (*KafkaHeaderCarrier)(nil)

// Get returns the first header value matching `key`.
func (c *KafkaHeaderCarrier) Get(key string) string {
	if c == nil || c.Headers == nil {
		return ""
	}
	for _, h := range *c.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

// Set replaces (or appends) a header. Replace-semantics matches the
// HeaderCarrier behaviour the OTel SDK expects.
func (c *KafkaHeaderCarrier) Set(key, value string) {
	if c == nil || c.Headers == nil {
		return
	}
	for i, h := range *c.Headers {
		if h.Key == key {
			(*c.Headers)[i].Value = []byte(value)
			return
		}
	}
	*c.Headers = append(*c.Headers, kafka.Header{Key: key, Value: []byte(value)})
}

// Keys returns every header name. Required by the TextMapCarrier
// interface for downstream propagators that iterate.
func (c *KafkaHeaderCarrier) Keys() []string {
	if c == nil || c.Headers == nil {
		return nil
	}
	out := make([]string, 0, len(*c.Headers))
	for _, h := range *c.Headers {
		out = append(out, h.Key)
	}
	return out
}

// InjectKafkaHeaders is the convenience wrapper most publishers want:
// pass the ctx + a pointer to the slice and the global propagator
// writes traceparent / tracestate / baggage into it. Idempotent — calling
// twice replaces (not duplicates) the header values.
func InjectKafkaHeaders(ctx context.Context, headers *[]kafka.Header) {
	if headers == nil {
		return
	}
	otel.GetTextMapPropagator().Inject(ctx, &KafkaHeaderCarrier{Headers: headers})
}

// ExtractKafkaHeaders is the consumer-side companion: pulls trace
// context out of an incoming message's headers and returns a context
// with that span context attached. Pass the result down into the
// handler so its child spans link back to the producer.
func ExtractKafkaHeaders(ctx context.Context, headers []kafka.Header) context.Context {
	return otel.GetTextMapPropagator().Extract(ctx, &KafkaHeaderCarrier{Headers: &headers})
}
