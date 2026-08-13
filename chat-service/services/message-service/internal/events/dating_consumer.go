package events

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

const (
	datingMatchClosed  = "dating.match.closed"
	datingMatchExpired = "dating.match.expired"
	datingUserBlocked  = "dating.user.blocked"
)

type datingEnvelope struct {
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
}

type matchClosedPayload struct {
	MatchID string `json:"match_id"`
}

type datingUserBlockedPayload struct {
	BlockerID string `json:"blocker_id"`
	BlockedID string `json:"blocked_id"`
}

type DatingReconciler interface {
	MarkConversationClosedByMatch(ctx context.Context, matchID uuid.UUID) error
	MarkConversationsClosedByPair(ctx context.Context, userA, userB uuid.UUID) error
}

type DatingConsumer struct {
	reader *kafka.Reader
	store  DatingReconciler
	log    *slog.Logger
}

func NewDatingConsumer(brokers []string, topic, groupID string, store DatingReconciler, logger *slog.Logger) *DatingConsumer {
	return NewDatingConsumerWithDialer(brokers, topic, groupID, nil, store, logger)
}

func NewDatingConsumerWithDialer(brokers []string, topic, groupID string, dialer *kafka.Dialer, store DatingReconciler, logger *slog.Logger) *DatingConsumer {
	if logger == nil {
		logger = slog.Default()
	}
	reader := kafka.NewReader(kafka.ReaderConfig{Brokers: brokers, Topic: topic, GroupID: groupID, Dialer: dialer})
	return &DatingConsumer{reader: reader, store: store, log: logger}
}

// Start does not fetch a later offset until the current close/block effect is
// durable. Malformed poison messages are the only messages committed without
// a store mutation.
func (c *DatingConsumer) Start(ctx context.Context) {
	c.log.Info("starting dating event consumer")
	defer func() {
		if err := c.reader.Close(); err != nil {
			c.log.Warn("dating consumer close", "err", err)
		}
	}()
	for {
		message, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}
			c.log.Warn("dating consumer fetch", "err", err)
			time.Sleep(2 * time.Second)
			continue
		}
		if err := c.handleUntilDurable(ctx, message); err != nil {
			return
		}
		if err := c.reader.CommitMessages(ctx, message); err != nil {
			c.log.Warn("dating consumer commit", "err", err)
		}
	}
}

func (c *DatingConsumer) handleUntilDurable(ctx context.Context, message kafka.Message) error {
	for {
		permanent, err := c.processMessage(ctx, message)
		if err == nil || permanent {
			if permanent && err != nil {
				c.log.Warn("discarding permanently invalid dating event", "err", err)
			}
			return nil
		}
		c.log.Error("dating event not durable; partition remains blocked", "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (c *DatingConsumer) processMessage(ctx context.Context, message kafka.Message) (bool, error) {
	var envelope datingEnvelope
	if err := json.Unmarshal(message.Value, &envelope); err != nil {
		return true, err
	}
	switch envelope.EventType {
	case datingMatchClosed, datingMatchExpired:
		var payload matchClosedPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return true, err
		}
		matchID, err := uuid.Parse(payload.MatchID)
		if err != nil {
			return true, err
		}
		if err := c.store.MarkConversationClosedByMatch(ctx, matchID); err != nil {
			return false, err
		}
		c.log.Info("closed dating conversation for match", "match_id", matchID, "event_type", envelope.EventType)
	case datingUserBlocked:
		var payload datingUserBlockedPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return true, err
		}
		blocker, err := uuid.Parse(payload.BlockerID)
		if err != nil {
			return true, err
		}
		blocked, err := uuid.Parse(payload.BlockedID)
		if err != nil {
			return true, err
		}
		if err := c.store.MarkConversationsClosedByPair(ctx, blocker, blocked); err != nil {
			return false, err
		}
		c.log.Info("closed dating conversations by pair", "blocker_id", blocker, "blocked_id", blocked)
	default:
		return true, nil
	}
	return false, nil
}
