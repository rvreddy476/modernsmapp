package events

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/atpost/shared/events"
	"github.com/segmentio/kafka-go"
)

type Producer struct {
	writer *kafka.Writer
}

func NewProducer(brokers []string, topic string) *Producer {
	return NewProducerWithDialer(brokers, topic, nil)
}

func NewProducerWithDialer(brokers []string, topic string, dialer *kafka.Dialer) *Producer {
	w := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  brokers,
		Topic:    topic,
		Balancer: &kafka.LeastBytes{},
		Dialer:   dialer,
	})
	return &Producer{writer: w}
}

// PublishRawEvent publishes an already-marshalled payload under an
// arbitrary event type. Used by the transcript request path (P0-9).
func (p *Producer) PublishRawEvent(ctx context.Context, eventType, key string, payload []byte) error {
	envelope := events.NewEnvelope(ctx, eventType, nil, payload)
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("failed to marshal envelope: %w", err)
	}
	if key == "" {
		key = envelope.EventID
	}
	return p.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(key),
		Value: envelopeBytes,
	})
}

// PublishEnvelope publishes an outbox-owned envelope without regenerating its
// identity. Keeping event_id stable across retries is what lets consumers
// collapse the unavoidable publish-before-mark duplicate window.
func (p *Producer) PublishEnvelope(ctx context.Context, envelope events.EventEnvelope) error {
	if p == nil || p.writer == nil {
		return fmt.Errorf("media event producer is not configured")
	}
	if envelope.EventID == "" || envelope.EventType == "" {
		return fmt.Errorf("media event envelope is missing identity")
	}
	b, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal media event envelope: %w", err)
	}
	return p.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(envelope.EventID),
		Value: b,
	})
}

func (p *Producer) Close() error {
	return p.writer.Close()
}
