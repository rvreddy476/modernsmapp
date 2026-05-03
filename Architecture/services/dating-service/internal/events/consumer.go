package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// Layer2Processor is the dating-service handler for the async LLM scan.
// We accept this as an interface so the consumer doesn't depend on the
// service package directly (avoids an import cycle).
type Layer2Processor interface {
	ProcessLayer2(ctx context.Context, messageID, conversationID uuid.UUID, snippet string) error
}

// Consumer listens on platform-events for cross-service lifecycle signals
// + the dating-events topic for the moderation layer-2 work queue.
//
// Sprint 1 wired user.deletion_requested (DPDP §15.8 — soft-delete).
// Sprint 4 adds dating.moderation.layer2.requested → ProcessLayer2.
type Consumer struct {
	platformReader *kafka.Reader
	datingReader   *kafka.Reader
	store          *store.Store
	layer2         Layer2Processor
}

// NewConsumer constructs a Consumer with the default dialer.
func NewConsumer(brokers []string, groupID string, s *store.Store) *Consumer {
	return NewConsumerWithDialer(brokers, groupID, s, nil)
}

// NewConsumerWithDialer wires both Kafka readers (platform-events + the
// dating-events topic for moderation layer 2).
func NewConsumerWithDialer(brokers []string, groupID string, s *store.Store, dialer *kafka.Dialer) *Consumer {
	platform := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  groupID,
		Topic:    "platform-events",
		MinBytes: 10e3,
		MaxBytes: 10e6,
		Dialer:   dialer,
	})
	dating := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  groupID + "-dating",
		Topic:    "dating-events",
		MinBytes: 10e3,
		MaxBytes: 10e6,
		Dialer:   dialer,
	})
	return &Consumer{platformReader: platform, datingReader: dating, store: s}
}

// SetLayer2Processor injects the moderation layer-2 handler.
func (c *Consumer) SetLayer2Processor(p Layer2Processor) {
	c.layer2 = p
}

// Start blocks until ctx is cancelled. Two readers run in parallel.
func (c *Consumer) Start(ctx context.Context) {
	slog.Info("dating-service consumer listening on platform-events + dating-events")
	go c.runPlatform(ctx)
	c.runDating(ctx)
}

func (c *Consumer) runPlatform(ctx context.Context) {
	for {
		m, err := c.platformReader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				slog.Info("platform consumer shutting down")
				return
			}
			slog.Error("platform consumer read error", "error", err)
			time.Sleep(2 * time.Second)
			continue
		}
		if err := c.processPlatformMessage(ctx, m); err != nil {
			slog.Warn("platform consumer process failed", "error", err)
		}
	}
}


func (c *Consumer) runDating(ctx context.Context) {
	for {
		m, err := c.datingReader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				slog.Info("dating consumer shutting down")
				return
			}
			slog.Error("dating consumer read error", "error", err)
			time.Sleep(2 * time.Second)
			continue
		}
		if err := c.processDatingMessage(ctx, m); err != nil {
			slog.Warn("dating consumer process failed", "error", err)
		}
	}
}

func (c *Consumer) processPlatformMessage(ctx context.Context, m kafka.Message) error {
	var envelope events.EventEnvelope
	if err := json.Unmarshal(m.Value, &envelope); err != nil {
		return err
	}
	switch envelope.EventType {
	case events.EventUserDeletionRequested:
		return c.handleUserDeletionRequested(ctx, envelope)
	}
	return nil
}

func (c *Consumer) processDatingMessage(ctx context.Context, m kafka.Message) error {
	var envelope events.EventEnvelope
	if err := json.Unmarshal(m.Value, &envelope); err != nil {
		return err
	}
	switch envelope.EventType {
	case events.EventDatingModerationLayer2Requested:
		return c.handleLayer2Requested(ctx, envelope)
	}
	return nil
}

func (c *Consumer) handleUserDeletionRequested(ctx context.Context, envelope events.EventEnvelope) error {
	var payload events.UserDeletionRequestedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return err
	}
	userID, err := uuid.Parse(payload.UserID)
	if err != nil {
		return err
	}
	slog.Info("dating-service: processing DPDP deletion", "user_id", userID)
	if err := c.store.SoftDeleteProfile(ctx, userID); err != nil {
		slog.Warn("dating-service: failed to soft-delete profile", "user_id", userID, "error", err)
		return err
	}
	return nil
}

// handleLayer2Requested dispatches a moderation.layer2.requested event to
// the layer-2 processor. Idempotent on (message_id, layer) at the store
// level — re-deliveries are safe.
func (c *Consumer) handleLayer2Requested(ctx context.Context, envelope events.EventEnvelope) error {
	if c.layer2 == nil {
		slog.Warn("layer2 processor not wired; dropping event", "event_id", envelope.EventID)
		return nil
	}
	var payload struct {
		MessageID      string `json:"message_id"`
		ConversationID string `json:"conversation_id"`
		Snippet        string `json:"snippet"`
	}
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return err
	}
	msgID, err := uuid.Parse(payload.MessageID)
	if err != nil {
		return err
	}
	convID, err := uuid.Parse(payload.ConversationID)
	if err != nil {
		return err
	}
	return c.layer2.ProcessLayer2(ctx, msgID, convID, payload.Snippet)
}

// Close releases the underlying readers.
func (c *Consumer) Close() error {
	if c == nil {
		return nil
	}
	if c.platformReader != nil {
		_ = c.platformReader.Close()
	}
	if c.datingReader != nil {
		_ = c.datingReader.Close()
	}
	return nil
}
