package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type Producer struct {
	writer *kafka.Writer
}

func NewProducer(brokers []string, topic string) *Producer {
	return NewProducerWithDialer(brokers, topic, nil)
}

func NewProducerWithDialer(brokers []string, topic string, dialer *kafka.Dialer) *Producer {
	w := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  brokers,
		Topic:    topic,
		Balancer: &kafka.LeastBytes{},
		Dialer:   dialer,
	})
	return &Producer{writer: w}
}

func (p *Producer) PublishConnectionRequested(ctx context.Context, senderID, receiverID uuid.UUID) error {
	payload := events.ConnectionRequestedPayload{
		SenderID:   senderID.String(),
		ReceiverID: receiverID.String(),
		CreatedAt:  time.Now(),
	}
	return p.publish(ctx, events.ConnectionRequested, &senderID, payload)
}

func (p *Producer) PublishConnectionAccepted(ctx context.Context, senderID, receiverID uuid.UUID) error {
	payload := events.ConnectionAcceptedPayload{
		SenderID:   senderID.String(),
		ReceiverID: receiverID.String(),
		AcceptedAt: time.Now(),
	}
	return p.publish(ctx, events.ConnectionAccepted, &receiverID, payload)
}

func (p *Producer) PublishConnectionDeclined(ctx context.Context, senderID, receiverID uuid.UUID) error {
	payload := events.ConnectionDeclinedPayload{
		SenderID:   senderID.String(),
		ReceiverID: receiverID.String(),
		DeclinedAt: time.Now(),
	}
	return p.publish(ctx, events.ConnectionDeclined, &receiverID, payload)
}

func (p *Producer) PublishConnectionRequestCancelled(ctx context.Context, senderID, receiverID uuid.UUID) error {
	payload := events.ConnectionRequestCancelledPayload{
		SenderID:    senderID.String(),
		ReceiverID:  receiverID.String(),
		CancelledAt: time.Now(),
	}
	return p.publish(ctx, events.ConnectionRequestCancelled, &senderID, payload)
}

func (p *Producer) PublishConnectionRemoved(ctx context.Context, userA, userB, removedBy uuid.UUID) error {
	payload := events.ConnectionRemovedPayload{
		UserA:     userA.String(),
		UserB:     userB.String(),
		RemovedBy: removedBy.String(),
		RemovedAt: time.Now(),
	}
	return p.publish(ctx, events.ConnectionRemoved, &removedBy, payload)
}

func (p *Producer) PublishUserBlocked(ctx context.Context, blockerID, blockedID uuid.UUID) error {
	payload := events.UserBlockedPayload{
		BlockerID: blockerID.String(),
		BlockedID: blockedID.String(),
		BlockedAt: time.Now(),
	}
	return p.publish(ctx, events.UserBlocked, &blockerID, payload)
}

func (p *Producer) PublishUserUnblocked(ctx context.Context, blockerID, blockedID uuid.UUID) error {
	payload := events.UserUnblockedPayload{
		BlockerID:   blockerID.String(),
		BlockedID:   blockedID.String(),
		UnblockedAt: time.Now(),
	}
	return p.publish(ctx, events.UserUnblocked, &blockerID, payload)
}

func (p *Producer) PublishCloseFriendAdded(ctx context.Context, ownerID, memberID uuid.UUID) error {
	payload := events.CloseFriendChangedPayload{
		OwnerID:    ownerID.String(),
		MemberID:   memberID.String(),
		OccurredAt: time.Now(),
	}
	return p.publish(ctx, events.CloseFriendAdded, &ownerID, payload)
}

func (p *Producer) PublishCloseFriendRemoved(ctx context.Context, ownerID, memberID uuid.UUID) error {
	payload := events.CloseFriendChangedPayload{
		OwnerID:    ownerID.String(),
		MemberID:   memberID.String(),
		OccurredAt: time.Now(),
	}
	return p.publish(ctx, events.CloseFriendRemoved, &ownerID, payload)
}

func (p *Producer) PublishUserFollowed(ctx context.Context, followerID, followeeID uuid.UUID) error {
	payload := events.UserFollowedPayload{
		FollowerID: followerID.String(),
		FolloweeID: followeeID.String(),
		CreatedAt:  time.Now(),
	}
	return p.publish(ctx, events.UserFollowed, &followerID, payload)
}

func (p *Producer) PublishUserUnfollowed(ctx context.Context, followerID, followeeID uuid.UUID) error {
	payload := events.UserUnfollowedPayload{
		FollowerID: followerID.String(),
		FolloweeID: followeeID.String(),
		OccurredAt: time.Now(),
	}
	return p.publish(ctx, events.UserUnfollowed, &followerID, payload)
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
