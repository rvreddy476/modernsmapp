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

	envelope := events.EventEnvelope{
		EventID:     uuid.New().String(),
		EventType:   eventType,
		OccurredAt:  time.Now(),
		ActorUserID: actorStr,
		Payload:     payloadBytes,
	}

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
