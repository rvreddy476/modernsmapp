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
// auto-promote (§16.6) effects on chat state.
type SocialReconciler interface {
	// PromoteRequestConversationByPair promotes a pending message-request
	// conversation between the pair to a normal conversation.
	PromoteRequestConversationByPair(ctx context.Context, userA, userB uuid.UUID) (bool, error)
}

// DirectSeverRevoker is the SERVICE surface the block-sever (§16.1) rides:
// the sever must arm the room-revocation marker before the event is
// acknowledged (final-verification P0-4), and only the service holds the
// Redis client and entitlement configuration to do that.
type DirectSeverRevoker interface {
	SeverDirectConversationOnBlock(ctx context.Context, blockerID, blockedID uuid.UUID) (bool, error)
}

// SocialConsumer consumes graph-service events from Kafka and reconciles chat
// state: auto-promoting message requests on ConnectionAccepted and severing
// shared direct conversations on UserBlocked.
type SocialConsumer struct {
	reader  *kafka.Reader
	store   SocialReconciler
	severer DirectSeverRevoker
	log     *slog.Logger
}

func NewSocialConsumer(brokers []string, topic, groupID string, store SocialReconciler, severer DirectSeverRevoker, logger *slog.Logger) *SocialConsumer {
	return NewSocialConsumerWithDialer(brokers, topic, groupID, nil, store, severer, logger)
}

func NewSocialConsumerWithDialer(brokers []string, topic, groupID string, dialer *kafka.Dialer, store SocialReconciler, severer DirectSeverRevoker, logger *slog.Logger) *SocialConsumer {
	if logger == nil {
		logger = slog.Default()
	}
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers,
		Topic:   topic,
		GroupID: groupID,
		Dialer:  dialer,
	})
	return &SocialConsumer{reader: r, store: store, severer: severer, log: logger}
}

func (c *SocialConsumer) Start(ctx context.Context) {
	c.log.Info("starting social event consumer")
	defer func() {
		if err := c.reader.Close(); err != nil {
			c.log.Warn("failed to close social kafka reader", "err", err)
		}
	}()
	for {
		m, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				c.log.Info("social consumer context closed")
				return
			}
			c.log.Error("error reading social message", "err", err)
			time.Sleep(2 * time.Second)
			continue
		}

		if err := c.handleUntilDurable(ctx, m); err != nil {
			return
		}
		if err := c.reader.CommitMessages(ctx, m); err != nil {
			c.log.Error("failed to commit durable social event", "err", err)
		}
	}
}

func (c *SocialConsumer) handleUntilDurable(ctx context.Context, message kafka.Message) error {
	for {
		err := c.processMessage(ctx, message)
		if err == nil {
			return nil
		}
		var poison permanentEventError
		if errors.As(err, &poison) {
			c.log.Warn("discarding permanently invalid social event", "err", err)
			return nil
		}
		c.log.Error("social event not durable; partition remains blocked", "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (c *SocialConsumer) processMessage(ctx context.Context, message kafka.Message) error {
	var envelope socialEnvelope
	if err := json.Unmarshal(message.Value, &envelope); err != nil {
		return permanentEventError{err}
	}
	switch envelope.EventType {
	case socialConnectionAccepted:
		return c.handleConnectionAccepted(ctx, envelope.Payload)
	case socialUserBlocked:
		return c.handleUserBlocked(ctx, envelope.Payload)
	default:
		return nil
	}
}

type permanentEventError struct{ error }

// handleConnectionAccepted auto-promotes the pair's pending message-request
// conversation once they become connections (spec §16.6).
func (c *SocialConsumer) handleConnectionAccepted(ctx context.Context, payload json.RawMessage) error {
	var p connectionAcceptedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		c.log.Warn("error unmarshalling connection accepted payload", "err", err)
		return permanentEventError{err}
	}

	senderID, err := uuid.Parse(p.SenderID)
	if err != nil {
		c.log.Warn("invalid sender id in connection accepted event", "sender_id", p.SenderID)
		return permanentEventError{err}
	}
	receiverID, err := uuid.Parse(p.ReceiverID)
	if err != nil {
		c.log.Warn("invalid receiver id in connection accepted event", "receiver_id", p.ReceiverID)
		return permanentEventError{err}
	}

	promoted, err := c.store.PromoteRequestConversationByPair(ctx, senderID, receiverID)
	if err != nil {
		c.log.Error("failed to auto-promote request conversation", "err", err, "sender_id", senderID, "receiver_id", receiverID)
		return err
	}
	if promoted {
		c.log.Info("auto-promoted message request to conversation on connection", "sender_id", senderID, "receiver_id", receiverID)
	}
	return nil
}

// handleUserBlocked severs the blocker from the direct conversation they share
// with the blocked user (spec §16.1).
func (c *SocialConsumer) handleUserBlocked(ctx context.Context, payload json.RawMessage) error {
	var p userBlockedPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		c.log.Warn("error unmarshalling user blocked payload", "err", err)
		return permanentEventError{err}
	}

	blockerID, err := uuid.Parse(p.BlockerID)
	if err != nil {
		c.log.Warn("invalid blocker id in user blocked event", "blocker_id", p.BlockerID)
		return permanentEventError{err}
	}
	blockedID, err := uuid.Parse(p.BlockedID)
	if err != nil {
		c.log.Warn("invalid blocked id in user blocked event", "blocked_id", p.BlockedID)
		return permanentEventError{err}
	}

	// The sever rides the revocation protocol (final-verification P0-4): an
	// error — including a failed marker write — is returned WITHOUT ack, so
	// the at-least-once redelivery retries until revocation is durable.
	severed, err := c.severer.SeverDirectConversationOnBlock(ctx, blockerID, blockedID)
	if err != nil {
		c.log.Error("failed to sever direct conversation on block", "err", err, "blocker_id", blockerID, "blocked_id", blockedID)
		return err
	}
	if severed {
		c.log.Info("severed direct conversation on block", "blocker_id", blockerID, "blocked_id", blockedID)
	}
	return nil
}
