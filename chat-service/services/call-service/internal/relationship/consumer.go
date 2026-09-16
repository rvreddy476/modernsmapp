// Package relationship ends live 1:1 calls when the relationship that
// permitted them is revoked.
//
// The defect this closes: call-service checked "may A call B" exactly once, in
// CreateCall, and consumed no relationship events at all. Blocking somebody
// mid-call, or unmatching them, left the call running indefinitely — the block
// closed chat and the graph, but not the voice/video channel already open. For
// Dating that is the sharp edge, because unmatching IS the way a user cuts
// contact with someone they have just met.
//
// Two revocations are consumed, both as the platform EventEnvelope
// ({"event_type","payload"}) that every producer in the repo writes:
//
//   - "UserBlocked" on social.events.v1 (graph-service; payload
//     {"blocker_id","blocked_id"}) — the same event message-service already
//     rides to sever a shared direct conversation.
//   - "dating.match.closed" on dating-events (dating-service; payload
//     {"match_id","closed_by","user_a","user_b"}) — an unmatch, an expiry
//     close, or a safety close.
//
// Both mean the same thing here: this pair may no longer be connected, so any
// live 1:1 call between them ends with EndedReasonPermissionRevoked and both
// sides are told through the ordinary call lifecycle (CallEnded on the
// lifecycle + notification topics, signaling authorization cleared, SFU room
// closed) — there is no separate teardown notification path to keep in sync.
//
// Delivery is at-least-once and the teardown is idempotent (ending an
// already-ended call is a no-op), so a redelivery is safe. An event that
// cannot be applied holds its offset rather than being dropped: a missed
// teardown is a safety failure, not a lost metric.
package relationship

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

// Event types, mirrored from the shared contract (Architecture/shared/events)
// rather than imported: chat-service is its own Go module and deliberately
// does not depend on the Architecture workspace. message-service's social
// consumer mirrors "UserBlocked" the same way.
const (
	EventUserBlocked       = "UserBlocked"
	EventDatingMatchClosed = "dating.match.closed"

	// DefaultSocialTopic carries graph-service's connection/block events.
	DefaultSocialTopic = "social.events.v1"
	// DefaultDatingTopic carries dating-service's events.
	DefaultDatingTopic = "dating-events"
)

// Terminator ends live 1:1 calls between a pair. Implemented by
// *service.Service (EndDirectCallsBetween). Must be idempotent.
type Terminator interface {
	EndDirectCallsBetween(ctx context.Context, userA, userB uuid.UUID, reason string) (int, error)
}

// ErrPermanent marks an event that can never be applied (malformed JSON, a
// payload with no usable pair). Such a message is logged and skipped instead
// of blocking its partition forever.
var ErrPermanent = errors.New("permanent: event can never be processed")

// userBlockedPayload is graph-service's UserBlocked payload.
type userBlockedPayload struct {
	BlockerID string `json:"blocker_id"`
	BlockedID string `json:"blocked_id"`
}

// matchClosedPayload is dating-service's dating.match.closed payload. Only the
// pair is needed; match_id and closed_by are carried for logging.
type matchClosedPayload struct {
	MatchID  string `json:"match_id"`
	ClosedBy string `json:"closed_by"`
	UserA    string `json:"user_a"`
	UserB    string `json:"user_b"`
}

// Handler applies one revocation event.
type Handler struct {
	calls  Terminator
	reason string
	log    *slog.Logger
}

// NewHandler builds the handler. reason is the ended_reason recorded on every
// call it terminates (domain.EndedReasonPermissionRevoked).
func NewHandler(calls Terminator, reason string, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{calls: calls, reason: reason, log: log}
}

// Handles reports whether eventType is one this package acts on.
func Handles(eventType string) bool {
	return eventType == EventUserBlocked || eventType == EventDatingMatchClosed
}

// Handle processes one event. Returns nil for event types it does not act on,
// so both topics can be read with the same handler.
func (h *Handler) Handle(ctx context.Context, eventType string, payload json.RawMessage) error {
	switch eventType {
	case EventUserBlocked:
		var p userBlockedPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return fmt.Errorf("%w: decode %s payload: %v", ErrPermanent, eventType, err)
		}
		blocker, blocked, err := parsePair(p.BlockerID, p.BlockedID)
		if err != nil {
			return fmt.Errorf("%w: %s: %v", ErrPermanent, eventType, err)
		}
		return h.end(ctx, blocker, blocked, eventType, slog.String("blocker_id", p.BlockerID))

	case EventDatingMatchClosed:
		var p matchClosedPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return fmt.Errorf("%w: decode %s payload: %v", ErrPermanent, eventType, err)
		}
		userA, userB, err := parsePair(p.UserA, p.UserB)
		if err != nil {
			return fmt.Errorf("%w: %s: %v", ErrPermanent, eventType, err)
		}
		return h.end(ctx, userA, userB, eventType,
			slog.String("match_id", p.MatchID), slog.String("closed_by", p.ClosedBy))

	default:
		return nil
	}
}

func (h *Handler) end(ctx context.Context, userA, userB uuid.UUID, eventType string, attrs ...any) error {
	ended, err := h.calls.EndDirectCallsBetween(ctx, userA, userB, h.reason)
	if err != nil {
		return fmt.Errorf("end direct calls for %s: %w", eventType, err)
	}
	if ended > 0 {
		args := []any{"event", "call_terminated_on_revocation", "event_type", eventType,
			"user_a", userA, "user_b", userB, "calls_ended", ended}
		for _, a := range attrs {
			args = append(args, a)
		}
		h.log.Info("live call ended because the relationship was revoked", args...)
	}
	return nil
}

// parsePair validates both ids and rejects a self-pair, which can never
// identify a call between two people.
func parsePair(a, b string) (uuid.UUID, uuid.UUID, error) {
	idA, err := uuid.Parse(a)
	if err != nil || idA == uuid.Nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("invalid user id %q", a)
	}
	idB, err := uuid.Parse(b)
	if err != nil || idB == uuid.Nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("invalid user id %q", b)
	}
	if idA == idB {
		return uuid.Nil, uuid.Nil, fmt.Errorf("pair references one user twice: %s", a)
	}
	return idA, idB, nil
}

// HandleUntilDurable retries Handle with backoff until it succeeds, the error
// is permanent, or ctx is cancelled. Reports false only on cancellation, so the
// caller leaves the offset uncommitted for redelivery.
func (h *Handler) HandleUntilDurable(ctx context.Context, eventType string, payload json.RawMessage) bool {
	stall := 2 * time.Second
	const maxStall = 60 * time.Second
	for {
		err := h.Handle(ctx, eventType, payload)
		if err == nil {
			return true
		}
		if errors.Is(err, ErrPermanent) {
			h.log.Warn("relationship event skipped (permanent)", "event_type", eventType, "err", err)
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		h.log.Error("call teardown not durable; holding offset",
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

// Consumer reads one topic and applies revocations. One is started per topic
// (social + dating); they share a Handler.
type Consumer struct {
	reader  *kafka.Reader
	handler *Handler
	log     *slog.Logger
}

// NewConsumer builds the reader. groupID must be unique per service per topic.
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
	c.log.Info("relationship teardown consumer started",
		"topic", c.reader.Config().Topic, "group", c.reader.Config().GroupID)
	for {
		m, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				c.log.Info("relationship teardown consumer stopped", "topic", c.reader.Config().Topic)
				return
			}
			c.log.Warn("relationship consumer fetch failed", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		var env Envelope
		if err := json.Unmarshal(m.Value, &env); err != nil {
			c.log.Warn("relationship consumer: undecodable envelope skipped", "offset", m.Offset, "err", err)
		} else if !c.handler.HandleUntilDurable(ctx, env.EventType, env.Payload) {
			return // shutting down; offset stays for redelivery
		}
		commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		if err := c.reader.CommitMessages(commitCtx, m); err != nil {
			c.log.Warn("relationship consumer: offset commit failed, will redeliver", "err", err)
		}
		cancel()
	}
}

// Close releases the reader.
func (c *Consumer) Close() error { return c.reader.Close() }
