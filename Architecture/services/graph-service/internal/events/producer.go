package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/facebook-like/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type Producer struct {
	writer *kafka.Writer
}

func NewProducer(brokers []string, topic string) *Producer {
	w := &kafka.Writer{
		Addr:     kafka.TCP(brokers...),
		Topic:    topic,
		Balancer: &kafka.LeastBytes{},
	}
	return &Producer{writer: w}
}

func (p *Producer) PublishFriendRequestSent(ctx context.Context, senderID, receiverID uuid.UUID) error {
	payload := events.FriendRequestSentPayload{
		SenderID:   senderID.String(),
		ReceiverID: receiverID.String(),
		CreatedAt:  time.Now(),
	}
	return p.publish(ctx, events.FriendRequestSent, &senderID, payload)
}

func (p *Producer) PublishFriendRequestAccepted(ctx context.Context, senderID, receiverID uuid.UUID) error {
	payload := events.FriendRequestAcceptedPayload{
		SenderID:   senderID.String(),
		ReceiverID: receiverID.String(),
		AcceptedAt: time.Now(),
	}
	return p.publish(ctx, events.FriendRequestAccepted, &receiverID, payload)
}

func (p *Producer) PublishFriendRequestDeclined(ctx context.Context, senderID, receiverID uuid.UUID) error {
	payload := events.FriendRequestDeclinedPayload{
		SenderID:   senderID.String(),
		ReceiverID: receiverID.String(),
		DeclinedAt: time.Now(),
	}
	return p.publish(ctx, events.FriendRequestDeclined, &receiverID, payload)
}

func (p *Producer) PublishFriendRemoved(ctx context.Context, userA, userB, removedBy uuid.UUID) error {
	payload := events.FriendRemovedPayload{
		UserA:     userA.String(),
		UserB:     userB.String(),
		RemovedBy: removedBy.String(),
		RemovedAt: time.Now(),
	}
	return p.publish(ctx, events.FriendRemoved, &removedBy, payload)
}

func (p *Producer) PublishUserBlocked(ctx context.Context, blockerID, blockedID uuid.UUID) error {
	payload := events.UserBlockedPayload{
		BlockerID: blockerID.String(),
		BlockedID: blockedID.String(),
		BlockedAt: time.Now(),
	}
	return p.publish(ctx, events.UserBlocked, &blockerID, payload)
}

func (p *Producer) publish(ctx context.Context, eventType string, actorID *uuid.UUID, payload interface{}) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	var actorStr *string
	if actorID != nil {
		s := actorID.String()
		actorStr = &s
	}

	envelope := events.NewEnvelope(ctx, eventType, actorStr, payloadBytes)

	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("failed to marshal envelope: %w", err)
	}

	return p.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(envelope.EventID),
		Value: envelopeBytes,
	})
}

func (p *Producer) Close() error {
	return p.writer.Close()
}
