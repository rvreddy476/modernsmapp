package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/atpost/media-service/internal/purge"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/segmentio/kafka-go"
)

// Consumer subscribes to the identity event bus and reacts to account
// lifecycle events:
//
//   - user.deletion_requested (legacy, no longer emitted): ignored. Its old
//     handler set processing_status='deleted', which the status CHECK
//     refuses, and would otherwise have erased media at request time with
//     no recovery window. Erasure is user.purge_requested's job.
//   - user.purge_requested and the deactivate/reactivate family
//     (internal/purge): erased in one transaction, acked as "media", retried
//     in place until durable — the offset never advances past an unacked
//     purge.
type Consumer struct {
	reader    *kafka.Reader
	store     *postgres.MediaAssetStore
	lifecycle *purge.Handler
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
		MinBytes: 1,
		MaxBytes: 10e6,
		MaxWait:  time.Second,
		Dialer:   dialer,
	})
	return &Consumer{reader: reader, store: store}
}

// WithLifecycleHandler wires the account-control (purge) handler.
func (c *Consumer) WithLifecycleHandler(h *purge.Handler) *Consumer {
	c.lifecycle = h
	return c
}

// Start blocks and processes messages until ctx is cancelled. Fetch →
// handle → commit-after.
func (c *Consumer) Start(ctx context.Context) {
	slog.Info("media identity consumer started", "topic", c.reader.Config().Topic, "group", c.reader.Config().GroupID)
	for {
		m, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				slog.Info("media identity consumer stopped")
				return
			}
			slog.Error("media consumer error", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		if !c.handle(ctx, m) {
			return // shutting down; leave the offset for redelivery
		}
		commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		if err := c.reader.CommitMessages(commitCtx, m); err != nil {
			slog.Warn("media consumer: offset commit failed, will redeliver", "error", err)
		}
		cancel()
	}
}

// handle processes one message. Reports false only on shutdown.
func (c *Consumer) handle(ctx context.Context, m kafka.Message) bool {
	var envelope events.EventEnvelope
	if err := json.Unmarshal(m.Value, &envelope); err != nil {
		slog.Warn("media consumer: undecodable envelope skipped", "offset", m.Offset, "error", err)
		return true
	}
	switch envelope.EventType {
	case events.EventUserDeletionRequested:
		// auth-service emits user.deletion_scheduled at request time and
		// user.purge_requested after the 30-day window (shared/events); this
		// legacy event would bypass both. A stray or replayed one is dropped.
		slog.Warn("media consumer: legacy user.deletion_requested ignored; media is erased on user.purge_requested",
			"offset", m.Offset)
	default:
		if c.lifecycle != nil && purge.Handles(envelope.EventType) {
			return c.lifecycle.HandleUntilDurable(ctx, envelope.EventType, envelope.Payload)
		}
	}
	return true
}

// Close shuts down the underlying Kafka reader.
func (c *Consumer) Close() error {
	return c.reader.Close()
}
