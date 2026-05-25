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

// Dating event types — defined locally to avoid a cross-module
// dependency on Architecture/shared/events. Mirrors the producer-side
// constants in Architecture/services/dating-service/internal/events.
const (
	datingMatchClosed  = "dating.match.closed"
	datingMatchExpired = "dating.match.expired"
)

// datingEnvelope mirrors the CloudEvents-style envelope dating-service
// writes to the dating-events topic. Only the fields we consume are
// decoded.
type datingEnvelope struct {
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
}

// matchClosedPayload mirrors the dating-service MatchClosedPayload /
// MatchExpiredPayload shapes — we only need the match_id, so accept
// either with a permissive struct.
type matchClosedPayload struct {
	MatchID string `json:"match_id"`
}

// DatingReconciler is the chat-side surface the dating consumer needs.
// Implemented by *postgres.ConversationStore.
type DatingReconciler interface {
	MarkConversationClosedByMatch(ctx context.Context, matchID uuid.UUID) error
}

// DatingConsumer consumes dating-service events from Kafka and reconciles
// chat state: closing a conversation when its underlying match closes
// or expires so the send-path gate can refuse new messages. P0-3 +
// P0-9 in dating/PRODUCTION_GAP_ANALYSIS.md.
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
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers,
		Topic:   topic,
		GroupID: groupID,
		Dialer:  dialer,
	})
	return &DatingConsumer{reader: r, store: store, log: logger}
}

func (c *DatingConsumer) Start(ctx context.Context) {
	c.log.Info("starting dating event consumer")
	defer func() {
		if err := c.reader.Close(); err != nil {
			c.log.Warn("dating consumer close", "err", err)
		}
	}()
	for {
		m, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}
			c.log.Warn("dating consumer fetch", "err", err)
			time.Sleep(2 * time.Second)
			continue
		}
		if err := c.handle(ctx, m); err != nil {
			// Log + commit anyway. Idempotent close — re-running on the
			// next backfill would just re-set the same closed_at value.
			// We don't DLQ closes because the cost of failing to close
			// (matched users still able to chat after an unmatch) is
			// worse than re-processing a duplicate.
			c.log.Warn("dating consumer handle", "err", err, "key", string(m.Key))
		}
		if err := c.reader.CommitMessages(ctx, m); err != nil {
			c.log.Warn("dating consumer commit", "err", err)
		}
	}
}

func (c *DatingConsumer) handle(ctx context.Context, m kafka.Message) error {
	var env datingEnvelope
	if err := json.Unmarshal(m.Value, &env); err != nil {
		return err
	}
	switch env.EventType {
	case datingMatchClosed, datingMatchExpired:
		var p matchClosedPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return err
		}
		matchID, err := uuid.Parse(p.MatchID)
		if err != nil {
			c.log.Warn("dating consumer: bad match_id", "raw", p.MatchID)
			return nil
		}
		if err := c.store.MarkConversationClosedByMatch(ctx, matchID); err != nil {
			return err
		}
		c.log.Info("dating consumer: closed conversation for match", "match_id", matchID, "event_type", env.EventType)
	}
	return nil
}
