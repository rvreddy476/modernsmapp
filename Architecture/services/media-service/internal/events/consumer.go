package events

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/segmentio/kafka-go"
)

// Consumer subscribes to the platform event bus and reacts to events relevant
// to the media service (e.g. GDPR user deletion requests).
type Consumer struct {
	reader *kafka.Reader
	store  *postgres.MediaAssetStore
}

// NewConsumer creates a Consumer that connects to the given Kafka brokers,
// joining the specified consumer group and reading from topic.
func NewConsumer(brokers []string, groupID, topic string, store *postgres.MediaAssetStore) *Consumer {
	return NewConsumerWithDialer(brokers, groupID, topic, store, nil)
}

// NewConsumerWithDialer creates a Consumer with an explicit Kafka dialer.
func NewConsumerWithDialer(brokers []string, groupID, topic string, store *postgres.MediaAssetStore, dialer *kafka.Dialer) *Consumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  groupID,
		Topic:    topic,
		MinBytes: 10e3,
		MaxBytes: 10e6,
		Dialer:   dialer,
	})
	return &Consumer{reader: reader, store: store}
}

// Start blocks and processes messages until ctx is cancelled.
func (c *Consumer) Start(ctx context.Context) {
	for {
		m, err := c.reader.ReadMessage(ctx)
		if err != nil {
			slog.Error("media consumer error", "error", err)
			break
		}
		if err := c.processMessage(ctx, m); err != nil {
			slog.Error("media: failed to process message", "topic", m.Topic, "error", err)
		}
	}
}

func (c *Consumer) processMessage(ctx context.Context, m kafka.Message) error {
	var envelope events.EventEnvelope
	if err := json.Unmarshal(m.Value, &envelope); err != nil {
		return err
	}

	switch envelope.EventType {
	case events.EventUserDeletionRequested:
		var p events.UserDeletionRequestedPayload
		b, _ := json.Marshal(envelope.Payload)
		if err := json.Unmarshal(b, &p); err != nil {
			return err
		}
		return c.store.SoftDeleteMediaByUploader(ctx, p.UserID)
	}
	return nil
}

// Close shuts down the underlying Kafka reader.
func (c *Consumer) Close() error {
	return c.reader.Close()
}
