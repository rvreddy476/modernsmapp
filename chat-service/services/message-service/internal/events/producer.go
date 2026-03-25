package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	sharedEvents "github.com/atpost/chat-shared/events"
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
	return &Producer{
		writer: kafka.NewWriter(kafka.WriterConfig{
			Brokers:      brokers,
			Topic:        topic,
			Balancer:     &kafka.LeastBytes{},
			WriteTimeout: 10 * time.Second,
			Dialer:       dialer,
		}),
	}
}

func (p *Producer) PublishConversationCreated(ctx context.Context, payload sharedEvents.ConversationCreatedPayload) error {
	return p.publish(ctx, sharedEvents.ConversationCreated, &payload.CreatedBy, payload.ConversationID, payload)
}

func (p *Producer) PublishMessageCreated(ctx context.Context, payload sharedEvents.MessageCreatedPayload) error {
	return p.publish(ctx, sharedEvents.MessageCreated, &payload.SenderID, payload.ConversationID, payload)
}

func (p *Producer) PublishMessageDeleted(ctx context.Context, payload sharedEvents.MessageDeletedPayload) error {
	return p.publish(ctx, sharedEvents.MessageDeleted, &payload.DeletedBy, payload.ConversationID, payload)
}

func (p *Producer) PublishRaw(ctx context.Context, eventType string, partitionKey string, payloadBytes json.RawMessage) error {
	actorStr := ""
	envelope := sharedEvents.EventEnvelope{
		EventID:     uuid.New().String(),
		EventType:   eventType,
		OccurredAt:  time.Now(),
		ActorUserID: &actorStr,
		Payload:     payloadBytes,
	}
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	return p.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(partitionKey),
		Value: envelopeBytes,
	})
}

func (p *Producer) publish(ctx context.Context, eventType string, actorID *string, partitionKey string, payload interface{}) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	envelope := sharedEvents.EventEnvelope{
		EventID:     uuid.New().String(),
		EventType:   eventType,
		OccurredAt:  time.Now(),
		ActorUserID: actorID,
		Payload:     payloadBytes,
	}

	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}

	return p.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(partitionKey),
		Value: envelopeBytes,
	})
}

func (p *Producer) Close() error {
	return p.writer.Close()
}
