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
// changed. Payload carries the NEW privacy_version and, as of Module 3, the
// NEW account_visibility — the one value graph-service must act on directly
// (auto-accepting pending follow requests on a private→public flip) rather
// than merely invalidate a cache for. Everything else stays an invalidation
// signal: consumers that need other values fetch the authoritative snapshot
// (which also closes the lost-event race: a fetch always returns the current
// row).
//
// Consumers (production chat pass, directive §5.1):
//   - graph-service drops its privacy:<user_id> Redis cache entry so the next
//     permission check re-reads the canonical snapshot instead of waiting out
//     the 3-second TTL, and reads account_visibility for the private→public
//     auto-accept;
//   - chat message-service refreshes its local chat.user_policy projection so
//     the hot send/typing/receipt paths see chat pause and receipt changes
//     without an HTTP call per message.
const EventUserSettingsChanged = "user.settings_changed"

// EventUserModulesChanged announces that a user changed their module
// selection or home surface (Module 3). Registered in
// Architecture/shared/events/events.go as UserModulesChanged.
const EventUserModulesChanged = "user.modules_changed"

// UserSettingsChangedPayload is the wire payload for EventUserSettingsChanged.
type UserSettingsChangedPayload struct {
	UserID         string    `json:"user_id"`
	PrivacyVersion int       `json:"privacy_version"`
	// AccountVisibility is the NEW value after the committed write. Additive
	// field: old consumers that ignore it are unaffected.
	AccountVisibility string    `json:"account_visibility,omitempty"`
	OccurredAt        time.Time `json:"occurred_at"`
}

// UserModulesChangedPayload is the wire payload for EventUserModulesChanged.
type UserModulesChangedPayload struct {
	UserID     string    `json:"user_id"`
	Modules    []string  `json:"modules"`
	HomeModule string    `json:"home_module"`
	OccurredAt time.Time `json:"occurred_at"`
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
// events for one user stay ordered on one partition). accountVisibility is
// the committed NEW value.
func (p *Producer) PublishSettingsChanged(ctx context.Context, userID uuid.UUID, privacyVersion int, accountVisibility string) {
	if p == nil {
		return
	}
	payload, err := json.Marshal(UserSettingsChangedPayload{
		UserID:            userID.String(),
		PrivacyVersion:    privacyVersion,
		AccountVisibility: accountVisibility,
		OccurredAt:        time.Now().UTC(),
	})
	if err != nil {
		p.log.Warn("marshal settings-changed payload failed", "err", err, "user_id", userID)
		return
	}
	p.publish(ctx, userID, EventUserSettingsChanged, payload)
}

// PublishModulesChanged emits user.modules_changed keyed by user id.
func (p *Producer) PublishModulesChanged(ctx context.Context, userID uuid.UUID, modules []string, homeModule string) {
	if p == nil {
		return
	}
	payload, err := json.Marshal(UserModulesChangedPayload{
		UserID:     userID.String(),
		Modules:    modules,
		HomeModule: homeModule,
		OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		p.log.Warn("marshal modules-changed payload failed", "err", err, "user_id", userID)
		return
	}
	p.publish(ctx, userID, EventUserModulesChanged, payload)
}

// publish wraps a payload in the identity envelope and writes it, best-effort.
func (p *Producer) publish(ctx context.Context, userID uuid.UUID, eventType string, payload json.RawMessage) {
	value, err := json.Marshal(producerEnvelope{EventType: eventType, Payload: payload})
	if err != nil {
		p.log.Warn("marshal event envelope failed", "err", err, "event_type", eventType, "user_id", userID)
		return
	}
	writeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := p.writer.WriteMessages(writeCtx, kafka.Message{
		Key:   []byte(userID.String()),
		Value: value,
	}); err != nil {
		p.log.Warn("publish event failed — consumers fall back to TTL",
			"err", err, "event_type", eventType, "user_id", userID)
	}
}

// Close flushes and closes the underlying writer.
func (p *Producer) Close() error {
	if p == nil {
		return nil
	}
	return p.writer.Close()
}
