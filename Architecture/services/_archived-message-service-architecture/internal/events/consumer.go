package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/atpost/message-service/internal/store/scylla"
	"github.com/atpost/shared/events"
	"github.com/segmentio/kafka-go"
)

// Consumer subscribes to the platform event bus and reacts to events relevant
// to the message service (e.g. GDPR user deletion requests).
type Consumer struct {
	reader *kafka.Reader
	store  *scylla.MessageStore
}

// NewConsumer creates a Consumer that connects to the given Kafka brokers,
// joining the specified consumer group and reading from topic.
func NewConsumer(brokers []string, groupID, topic string, store *scylla.MessageStore) *Consumer {
	return NewConsumerWithDialer(brokers, groupID, topic, store, nil)
}

func NewConsumerWithDialer(brokers []string, groupID, topic string, store *scylla.MessageStore, dialer *kafka.Dialer) *Consumer {
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
			if ctx.Err() != nil {
				slog.Info("message consumer shutting down")
				return
			}
			slog.Error("message consumer error", "error", err)
			time.Sleep(2 * time.Second)
			continue
		}
		if err := c.processMessage(ctx, m); err != nil {
			slog.Error("message: failed to process message", "topic", m.Topic, "error", err)
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
		return c.store.AnonymizeMessagesFromUser(ctx, p.UserID)
	}
	return nil
}

// Close shuts down the underlying Kafka reader.
func (c *Consumer) Close() error {
	return c.reader.Close()
}
