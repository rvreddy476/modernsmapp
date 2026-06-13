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

// Social (graph) event types — defined locally to avoid a cross-module
// dependency on Architecture/shared/events (chat-service is its own workspace).
const (
	socialConnectionAccepted = "ConnectionAccepted"
	socialUserBlocked        = "UserBlocked"
)

// socialEnvelope mirrors the CloudEvents-style envelope graph-service writes
// to the social.events.v1 topic. Only the fields we consume are decoded.
type socialEnvelope struct {
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
}

// connectionAcceptedPayload is graph-service's ConnectionAccepted payload.
type connectionAcceptedPayload struct {
	SenderID   string `json:"sender_id"`
	ReceiverID string `json:"receiver_id"`
}

// userBlockedPayload is graph-service's UserBlocked payload.
type userBlockedPayload struct {
	BlockerID string `json:"blocker_id"`
	BlockedID string `json:"blocked_id"`
}

// SocialReconciler is the store surface the social consumer needs to apply
// auto-promote (§16.6) and block-sever (§16.1) effects on chat state.
type SocialReconciler interface {
	// PromoteRequestConversationByPair promotes a pending message-request
	// conversation between the pair to a normal conversation.
	PromoteRequestConversationByPair(ctx context.Context, userA, userB uuid.UUID) (bool, error)
	// SeverDirectConversation severs the blocker from the direct conversation
	// it shares with the blocked user.
	SeverDirectConversation(ctx context.Context, blockerID, blockedID uuid.UUID) (bool, error)
}

// SocialConsumer consumes graph-service events from Kafka and reconciles chat
// state: auto-promoting message requests on ConnectionAccepted and severing
// shared direct conversations on UserBlocked.
type SocialConsumer struct {
	reader *kafka.Reader
	store  SocialReconciler
	log    *slog.Logger
}

func NewSocialConsumer(brokers []string, topic, groupID string, store SocialReconciler, logger *slog.Logger) *SocialConsumer {
	return NewSocialConsumerWithDialer(brokers, topic, groupID, nil, store, logger)
}

func NewSocialConsumerWithDialer(brokers []string, topic, groupID string, dialer *kafka.Dialer, store SocialReconciler, logger *slog.Logger) *SocialConsumer {
	if logger == nil {
		logger = slog.Default()
	}
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers,
		Topic:   topic,
		GroupID: groupID,
		Dialer:  dialer,
	})
	return &SocialConsumer{reader: r, store: store, log: logger}
}

func (c *SocialConsumer) Start(ctx context.Context) {
	c.log.Info("starting social event consumer")
	defer func() {
		if err := c.reader.Close(); err != nil {
			c.log.Warn("failed to close social kafka reader", "err", err)
		}
	}()
	for {
		m, err := c.reader.ReadMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				c.log.Info("social consumer context closed")
				return
			}
			c.log.Error("error reading social message", "err", err)
			time.Sleep(2 * time.Second)
			continue
		}

		var envelope socialEnvelope
		if err := json.Unmarshal(m.Value, &envelope); err != nil {
			c.log.Warn("error unmarshalling social event", "err", err)
			continue
		}

		switch envelope.EventType {
		case socialConnectionAccepted:
			c.handleConnectionAccepted(ctx, envelope.Payload)
		case socialUserBlocked:
			c.handleUserBlocked(ctx, envelope.Payload)
		}
	}
}

// handleConnectionAccepted auto-promotes the pair's pending message-request
// conversation once they become connections (spec §16.6).
func (c *SocialConsumer) handleConnectionAccepted(ctx context.Context, payload json.RawMessage) {
	var p connectionAcceptedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		c.log.Warn("error unmarshalling connection accepted payload", "err", err)
		return
	}

	senderID, err := uuid.Parse(p.SenderID)
	if err != nil {
		c.log.Warn("invalid sender id in connection accepted event", "sender_id", p.SenderID)
		return
	}
	receiverID, err := uuid.Parse(p.ReceiverID)
	if err != nil {
		c.log.Warn("invalid receiver id in connection accepted event", "receiver_id", p.ReceiverID)
		return
	}

	promoted, err := c.store.PromoteRequestConversationByPair(ctx, senderID, receiverID)
	if err != nil {
		c.log.Error("failed to auto-promote request conversation", "err", err, "sender_id", senderID, "receiver_id", receiverID)
		return
	}
	if promoted {
		c.log.Info("auto-promoted message request to conversation on connection", "sender_id", senderID, "receiver_id", receiverID)
	}
}

// handleUserBlocked severs the blocker from the direct conversation they share
// with the blocked user (spec §16.1).
func (c *SocialConsumer) handleUserBlocked(ctx context.Context, payload json.RawMessage) {
	var p userBlockedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		c.log.Warn("error unmarshalling user blocked payload", "err", err)
		return
	}

	blockerID, err := uuid.Parse(p.BlockerID)
	if err != nil {
		c.log.Warn("invalid blocker id in user blocked event", "blocker_id", p.BlockerID)
		return
	}
	blockedID, err := uuid.Parse(p.BlockedID)
	if err != nil {
		c.log.Warn("invalid blocked id in user blocked event", "blocked_id", p.BlockedID)
		return
	}

	severed, err := c.store.SeverDirectConversation(ctx, blockerID, blockedID)
	if err != nil {
		c.log.Error("failed to sever direct conversation on block", "err", err, "blocker_id", blockerID, "blocked_id", blockedID)
		return
	}
	if severed {
		c.log.Info("severed direct conversation on block", "blocker_id", blockerID, "blocked_id", blockedID)
	}
}
