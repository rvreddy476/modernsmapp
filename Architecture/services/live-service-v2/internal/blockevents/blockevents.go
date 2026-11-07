// Package blockevents feeds graph-service's UserBlocked events to the live
// service, so a block between a viewer and a live host removes the viewer
// from the room at once (service.OnUserBlocked; audit LV-M1, 7 Nov 2026).
//
// graph-service publishes {event_type:"UserBlocked", payload:{blocker_id,
// blocked_id, blocked_at}} on social.events.v1 after the block committed
// (shared/events.UserBlockedPayload). Every other event on the topic is
// skipped.
//
// Delivery: a new consumer group starts at the newest offset (an old block
// has no live room left to act on). A message is retried a few times in
// place and then given up, logged: one block that cannot be applied must not
// hold back every later one, and the join gate (service.applyPresence) still
// refuses the viewer on their next reconnect.
package blockevents

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

// EventUserBlocked mirrors shared/events.UserBlocked.
const EventUserBlocked = "UserBlocked"

// DefaultTopic is where graph-service publishes blocks.
const DefaultTopic = "social.events.v1"

// Handler applies one block.
type Handler interface {
	OnUserBlocked(ctx context.Context, blockerID, blockedID uuid.UUID) error
}

// Block is one decoded UserBlocked event.
type Block struct {
	BlockerID uuid.UUID
	BlockedID uuid.UUID
}

// ErrMalformed is a UserBlocked event that can never be applied.
var ErrMalformed = errors.New("malformed UserBlocked event")

type envelope struct {
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
}

type blockedPayload struct {
	BlockerID string `json:"blocker_id"`
	BlockedID string `json:"blocked_id"`
}

// Decode reads a topic message. ok is false for any other event (and for a
// message that is not an envelope at all); err is ErrMalformed for a
// UserBlocked event without two valid, different user ids.
func Decode(value []byte) (b Block, ok bool, err error) {
	var env envelope
	if json.Unmarshal(value, &env) != nil || env.EventType != EventUserBlocked {
		return Block{}, false, nil
	}
	var p blockedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return Block{}, true, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	blocker, err1 := uuid.Parse(p.BlockerID)
	blocked, err2 := uuid.Parse(p.BlockedID)
	if err1 != nil || err2 != nil || blocker == uuid.Nil || blocked == uuid.Nil || blocker == blocked {
		return Block{}, true, ErrMalformed
	}
	return Block{BlockerID: blocker, BlockedID: blocked}, true, nil
}

// Attempts is how many times one block is tried before it is given up.
const Attempts = 3

// Apply decodes one message and hands a block to h, retrying a failure up to
// Attempts times, waiting backoff before the second and doubling it. It
// reports whether the block was applied; false for anything that is not a
// block, a malformed one, a block given up, or a cancelled ctx.
func Apply(ctx context.Context, h Handler, value []byte, backoff time.Duration, log *slog.Logger) bool {
	b, ok, err := Decode(value)
	if !ok {
		return false
	}
	if err != nil {
		log.Warn("live-v2 blocks: malformed UserBlocked skipped", "err", err)
		return false
	}
	wait := backoff
	for attempt := 1; ; attempt++ {
		err := h.OnUserBlocked(ctx, b.BlockerID, b.BlockedID)
		if err == nil {
			return true
		}
		if attempt >= Attempts || ctx.Err() != nil {
			log.Error("live-v2 blocks: block not applied to live rooms; the join gate still refuses a reconnect",
				"attempts", attempt, "err", err)
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(wait):
		}
		wait *= 2
	}
}

// Consumer reads the topic and applies every block.
type Consumer struct {
	reader *kafka.Reader
	h      Handler
	log    *slog.Logger
}

// NewConsumer builds the reader. groupID must be unique to this service.
func NewConsumer(brokers []string, topic, groupID string, dialer *kafka.Dialer, h Handler, log *slog.Logger) *Consumer {
	if topic == "" {
		topic = DefaultTopic
	}
	if log == nil {
		log = slog.Default()
	}
	return &Consumer{
		reader: kafka.NewReader(kafka.ReaderConfig{
			Brokers: brokers, Topic: topic, GroupID: groupID, Dialer: dialer,
			StartOffset: kafka.LastOffset,
			MinBytes:    1, MaxBytes: 10e6, MaxWait: time.Second,
		}),
		h: h, log: log,
	}
}

// Start consumes until ctx is cancelled.
func (c *Consumer) Start(ctx context.Context) {
	c.log.Info("live-v2 block consumer started", "topic", c.reader.Config().Topic, "group", c.reader.Config().GroupID)
	for {
		m, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			c.log.Warn("live-v2 block consumer fetch failed", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		Apply(ctx, c.h, m.Value, time.Second, c.log)
		if ctx.Err() != nil {
			return // shutting down: the offset stays for redelivery
		}
		commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		if err := c.reader.CommitMessages(commitCtx, m); err != nil {
			c.log.Warn("live-v2 block consumer: offset commit failed, will redeliver", "err", err)
		}
		cancel()
	}
}

// Close releases the reader.
func (c *Consumer) Close() error { return c.reader.Close() }
