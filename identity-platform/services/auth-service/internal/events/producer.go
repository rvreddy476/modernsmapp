package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/atpost/identity-shared/events"
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
	return &Producer{writer: kafka.NewWriter(kafka.WriterConfig{
		Brokers:  brokers,
		Topic:    topic,
		Balancer: &kafka.LeastBytes{},
		Dialer:   dialer,
	})}
}

func (p *Producer) PublishUserRegistered(ctx context.Context, userID uuid.UUID, phone string, email *string, firstName, lastName, dob, gender string) error {
	payload := events.UserRegisteredPayload{
		UserID:    userID.String(),
		Phone:     phone,
		Email:     email,
		FirstName: firstName,
		LastName:  lastName,
		DOB:       dob,
		Gender:    gender,
		CreatedAt: time.Now(),
	}

	return p.publish(ctx, events.UserRegistered, &userID, payload)
}

func (p *Producer) PublishUserLoggedIn(ctx context.Context, userID, sessionID uuid.UUID, deviceID, platform, ip string) error {
	payload := events.UserLoggedInPayload{
		UserID:    userID.String(),
		SessionID: sessionID.String(),
		DeviceID:  deviceID,
		Platform:  platform,
		IP:        ip,
		Timestamp: time.Now(),
	}

	return p.publish(ctx, events.UserLoggedIn, &userID, payload)
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

// PublishRaw publishes a pre-built payload to Kafka, used by the outbox relay.
//
// CRITICAL BUG FIX: previously this wrote the raw `payloadBytes` as the
// Kafka message Value with no envelope wrapper. Every downstream consumer
// (search-service, notification-service, etc.) expects an EventEnvelope
// with `event_type` to dispatch on — without it, the switch falls
// through to `default` and the event is silently dropped. End-user
// symptom: a user registered through auth-service never showed up in
// search results / notification settings / any cross-service projection,
// because the outbox events all hit downstream consumers as unparseable
// blobs. The non-outbox `publish` method (above) already wraps in
// EventEnvelope; PublishRaw now does the same.
func (p *Producer) PublishRaw(ctx context.Context, eventType string, partitionKey string, payloadBytes json.RawMessage) error {
	envelope := events.EventEnvelope{
		EventID:    uuid.New().String(),
		EventType:  eventType,
		OccurredAt: time.Now(),
		Payload:    payloadBytes,
	}
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("failed to marshal envelope: %w", err)
	}
	key := partitionKey
	if key == "" {
		key = envelope.EventID
	}
	return p.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(key),
		Value: envelopeBytes,
	})
}

func (p *Producer) Close() error {
	return p.writer.Close()
}
