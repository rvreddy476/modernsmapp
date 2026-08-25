package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// EventUserSettingsChanged announces that a user's privacy settings snapshot
// changed. Payload carries the NEW privacy_version, never the values — the
// event is an invalidation signal, and consumers that need the values fetch
// the authoritative snapshot (which also closes the lost-event race: a fetch
// always returns the current row).
//
// Consumers (production chat pass, directive §5.1):
//   - graph-service drops its privacy:<user_id> Redis cache entry so the next
//     permission check re-reads the canonical snapshot instead of waiting out
//     the 3-second TTL;
//   - chat message-service refreshes its local chat.user_policy projection so
//     the hot send/typing/receipt paths see chat pause and receipt changes
//     without an HTTP call per message.
const EventUserSettingsChanged = "user.settings_changed"

// UserSettingsChangedPayload is the wire payload for EventUserSettingsChanged.
type UserSettingsChangedPayload struct {
	UserID         string    `json:"user_id"`
	PrivacyVersion int       `json:"privacy_version"`
	OccurredAt     time.Time `json:"occurred_at"`
}

// envelope matches the identity topic's {event_type, payload} shape consumed
// by the existing identity consumers (chat, graph, this service's own).
type producerEnvelope struct {
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
}

// Producer publishes identity events. Writes are BEST-EFFORT from the
// caller's point of view: a settings update must not fail because Kafka is
// briefly unavailable — the 3-second graph TTL and chat's policy fetch
// fallback bound the staleness window when an event is lost.
type Producer struct {
	writer *kafka.Writer
	log    *slog.Logger
}

// NewProducer builds a producer for the identity topic. Returns nil when no
// brokers are configured (local test runs without Kafka), which every method
// treats as a no-op.
func NewProducer(brokers []string, topic string, logger *slog.Logger) *Producer {
	if len(brokers) == 0 || topic == "" {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Producer{
		writer: &kafka.Writer{
			Addr:         kafka.TCP(brokers...),
			Topic:        topic,
			Balancer:     &kafka.Hash{},
			RequiredAcks: kafka.RequireOne,
			BatchTimeout: 50 * time.Millisecond,
		},
		log: logger,
	}
}

// PublishSettingsChanged emits user.settings_changed keyed by user id (so all
// events for one user stay ordered on one partition).
func (p *Producer) PublishSettingsChanged(ctx context.Context, userID uuid.UUID, privacyVersion int) {
	if p == nil {
		return
	}
	payload, err := json.Marshal(UserSettingsChangedPayload{
		UserID:         userID.String(),
		PrivacyVersion: privacyVersion,
		OccurredAt:     time.Now().UTC(),
	})
	if err != nil {
		p.log.Warn("marshal settings-changed payload failed", "err", err, "user_id", userID)
		return
	}
	value, err := json.Marshal(producerEnvelope{EventType: EventUserSettingsChanged, Payload: payload})
	if err != nil {
		p.log.Warn("marshal settings-changed envelope failed", "err", err, "user_id", userID)
		return
	}
	writeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := p.writer.WriteMessages(writeCtx, kafka.Message{
		Key:   []byte(userID.String()),
		Value: value,
	}); err != nil {
		p.log.Warn("publish settings-changed failed — consumers fall back to TTL",
			"err", err, "user_id", userID)
	}
}

// Close flushes and closes the underlying writer.
func (p *Producer) Close() error {
	if p == nil {
		return nil
	}
	return p.writer.Close()
}
