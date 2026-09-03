// Package purge implements this service's side of the auth-service account
// lifecycle (Architecture/shared/events/events.go, "Account control").
//
//   - user.deactivated / user.deletion_scheduled  → hide (reversible)
//   - user.reactivated / user.deletion_cancelled  → unhide
//   - user.purge_requested                        → erase every row keyed by
//     the user in one transaction, then ack onto platform.purge-acks.v1 with
//     {"user_id","service","purged_at"}.
//
// The handler is idempotent: auth re-emits user.purge_requested every 24h
// until it sees the ack, so a redelivery must find nothing to erase and still
// ack. The ack is published only AFTER the erase committed; the caller commits
// the Kafka offset only after Handle returns nil, so a crash anywhere in
// between replays the (idempotent) erase on restart.
package purge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// Event types, mirrored from the shared contract so this package stays free
// of the shared events module (chat/identity repos have their own).
const (
	EventUserDeactivated       = "user.deactivated"
	EventUserReactivated       = "user.reactivated"
	EventUserDeletionScheduled = "user.deletion_scheduled"
	EventUserDeletionCancelled = "user.deletion_cancelled"
	EventUserPurgeRequested    = "user.purge_requested"

	// EventUserPurgeAcked is the outbox event type used by services that
	// route the ack through their transactional outbox.
	EventUserPurgeAcked = "user.purge_acked"

	// DefaultAcksTopic is where auth-service's AcksConsumer listens.
	DefaultAcksTopic = "platform.purge-acks.v1"
)

// Ack is the wire message auth-service expects (bare JSON, or wrapped in an
// EventEnvelope payload — its parser accepts both).
type Ack struct {
	UserID   string    `json:"user_id"`
	Service  string    `json:"service"`
	PurgedAt time.Time `json:"purged_at"`
}

// Eraser erases every row keyed by the user in ONE transaction. Must be
// idempotent: a second call finds nothing and returns nil.
type Eraser interface {
	PurgeUser(ctx context.Context, userID uuid.UUID) error
}

// Hider hides/unhides the user without erasing anything. Optional.
type Hider interface {
	SetUserHidden(ctx context.Context, userID uuid.UUID, hidden bool, reason string) error
}

// AckPublisher delivers the ack durably. It is called only after the erase
// committed. An error is retried by the consumer (the erase re-runs as a
// no-op first).
type AckPublisher interface {
	PublishPurgeAck(ctx context.Context, ack Ack) error
}

// ErrPermanent marks a payload that can never be processed (malformed JSON,
// bad uuid). The consumer logs and skips such messages rather than blocking
// the partition forever.
var ErrPermanent = errors.New("permanent: event can never be processed")

// Handler dispatches the lifecycle events for one service.
type Handler struct {
	service string
	eraser  Eraser
	acks    AckPublisher
	hider   Hider
	now     func() time.Time
	log     *slog.Logger
}

// NewHandler builds a handler. hider may be nil (hide is then a no-op).
func NewHandler(service string, eraser Eraser, acks AckPublisher, hider Hider, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{service: service, eraser: eraser, acks: acks, hider: hider, now: func() time.Time { return time.Now().UTC() }, log: log}
}

// WithClock overrides the purged_at source (tests).
func (h *Handler) WithClock(now func() time.Time) *Handler { h.now = now; return h }

// Service returns the ack service name.
func (h *Handler) Service() string { return h.service }

// Handles reports whether eventType is one of the lifecycle events.
func Handles(eventType string) bool {
	switch eventType {
	case EventUserDeactivated, EventUserReactivated, EventUserDeletionScheduled,
		EventUserDeletionCancelled, EventUserPurgeRequested:
		return true
	}
	return false
}

// Handle processes one lifecycle event. Returns nil when the event is not a
// lifecycle event. Wraps ErrPermanent for undecodable payloads.
func (h *Handler) Handle(ctx context.Context, eventType string, payload json.RawMessage) error {
	if !Handles(eventType) {
		return nil
	}
	var p struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("%w: decode %s payload: %v", ErrPermanent, eventType, err)
	}
	userID, err := uuid.Parse(p.UserID)
	if err != nil || userID == uuid.Nil {
		return fmt.Errorf("%w: %s user_id %q", ErrPermanent, eventType, p.UserID)
	}

	switch eventType {
	case EventUserDeactivated, EventUserDeletionScheduled:
		return h.hide(ctx, userID, true, eventType)
	case EventUserReactivated, EventUserDeletionCancelled:
		return h.hide(ctx, userID, false, eventType)
	case EventUserPurgeRequested:
		return h.purge(ctx, userID)
	}
	return nil
}

func (h *Handler) hide(ctx context.Context, userID uuid.UUID, hidden bool, reason string) error {
	if h.hider == nil {
		return nil
	}
	if err := h.hider.SetUserHidden(ctx, userID, hidden, reason); err != nil {
		return fmt.Errorf("%s: set hidden=%v for %s: %w", h.service, hidden, userID, err)
	}
	h.log.Info("account visibility updated", "event", "user_hidden_set", "service", h.service,
		"user_id", userID, "hidden", hidden, "reason", reason)
	return nil
}

func (h *Handler) purge(ctx context.Context, userID uuid.UUID) error {
	if err := h.eraser.PurgeUser(ctx, userID); err != nil {
		return fmt.Errorf("%s: purge %s: %w", h.service, userID, err)
	}
	ack := Ack{UserID: userID.String(), Service: h.service, PurgedAt: h.now()}
	if err := h.acks.PublishPurgeAck(ctx, ack); err != nil {
		return fmt.Errorf("%s: publish purge ack for %s: %w", h.service, userID, err)
	}
	h.log.Info("user purged and acked", "event", "user_purged", "service", h.service, "user_id", userID)
	return nil
}

// HandleUntilDurable retries Handle in place with exponential backoff until it
// succeeds, the error is permanent, or ctx is cancelled. Reports false only
// on cancellation, so the caller leaves the offset uncommitted for redelivery.
func (h *Handler) HandleUntilDurable(ctx context.Context, eventType string, payload json.RawMessage) bool {
	stall := 2 * time.Second
	const maxStall = 60 * time.Second
	for {
		err := h.Handle(ctx, eventType, payload)
		if err == nil {
			return true
		}
		if errors.Is(err, ErrPermanent) {
			h.log.Warn("lifecycle event skipped (permanent)", "service", h.service, "event_type", eventType, "err", err)
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		h.log.Error("lifecycle event not durable; holding offset", "service", h.service,
			"event_type", eventType, "retry_in", stall, "err", err)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(stall):
		}
		if stall < maxStall {
			stall *= 2
			if stall > maxStall {
				stall = maxStall
			}
		}
	}
}

// ── Direct Kafka ack publisher ──────────────────────────────────────────────

// KafkaAckPublisher writes bare acks straight to the acks topic with
// RequiredAcks=all. Used by services without a transactional outbox (or whose
// outbox is bound to a single topic).
type KafkaAckPublisher struct {
	writer *kafka.Writer
}

// NewKafkaAckPublisher builds the writer. dialer may be nil.
func NewKafkaAckPublisher(brokers []string, topic string, dialer *kafka.Dialer) *KafkaAckPublisher {
	if topic == "" {
		topic = DefaultAcksTopic
	}
	return &KafkaAckPublisher{writer: kafka.NewWriter(kafka.WriterConfig{
		Brokers:      brokers,
		Topic:        topic,
		Balancer:     &kafka.Hash{},
		RequiredAcks: int(kafka.RequireAll),
		WriteTimeout: 10 * time.Second,
		Dialer:       dialer,
	})}
}

// PublishPurgeAck writes one ack keyed by user_id.
func (p *KafkaAckPublisher) PublishPurgeAck(ctx context.Context, ack Ack) error {
	b, err := json.Marshal(ack)
	if err != nil {
		return err
	}
	return p.writer.WriteMessages(ctx, kafka.Message{Key: []byte(ack.UserID), Value: b})
}

// Close releases the writer.
func (p *KafkaAckPublisher) Close() error { return p.writer.Close() }

// ── Standalone durable consumer ─────────────────────────────────────────────

// Consumer is a self-contained identity-topic reader for services that have
// no identity consumer to extend. FetchMessage → HandleUntilDurable →
// CommitMessages; the offset never advances past an unresolved purge.
type Consumer struct {
	reader  *kafka.Reader
	handler *Handler
	log     *slog.Logger
}

// NewConsumer builds the reader. groupID must be unique per service.
func NewConsumer(brokers []string, topic, groupID string, dialer *kafka.Dialer, h *Handler, log *slog.Logger) *Consumer {
	if log == nil {
		log = slog.Default()
	}
	return &Consumer{
		reader: kafka.NewReader(kafka.ReaderConfig{
			Brokers: brokers, Topic: topic, GroupID: groupID, Dialer: dialer,
			MinBytes: 1, MaxBytes: 10e6, MaxWait: time.Second,
		}),
		handler: h, log: log,
	}
}

// Envelope is the subset of the platform EventEnvelope this consumer needs.
type Envelope struct {
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
}

// Start consumes until ctx is cancelled.
func (c *Consumer) Start(ctx context.Context) {
	c.log.Info("account lifecycle consumer started", "service", c.handler.service,
		"topic", c.reader.Config().Topic, "group", c.reader.Config().GroupID)
	for {
		m, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				c.log.Info("account lifecycle consumer stopped", "service", c.handler.service)
				return
			}
			c.log.Warn("account lifecycle consumer fetch failed", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		var env Envelope
		if err := json.Unmarshal(m.Value, &env); err != nil {
			c.log.Warn("account lifecycle consumer: undecodable envelope skipped", "offset", m.Offset, "err", err)
		} else if !c.handler.HandleUntilDurable(ctx, env.EventType, env.Payload) {
			return // shutting down; offset stays for redelivery
		}
		commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		if err := c.reader.CommitMessages(commitCtx, m); err != nil {
			c.log.Warn("account lifecycle consumer: offset commit failed, will redeliver", "err", err)
		}
		cancel()
	}
}

// Close releases the reader.
func (c *Consumer) Close() error { return c.reader.Close() }
