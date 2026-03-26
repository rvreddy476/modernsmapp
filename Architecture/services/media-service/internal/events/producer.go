package events

import (
	"context"
	"encoding/json"
	"fmt"

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

func (p *Producer) PublishTranscodeRequested(ctx context.Context, mediaAssetID uuid.UUID, uploaderID uuid.UUID, storageKey, mimeType string) error {
	payload := events.MediaTranscodeRequestedPayload{
		MediaAssetID: mediaAssetID.String(),
		UploaderID:   uploaderID.String(),
		StorageKey:   storageKey,
		MimeType:     mimeType,
	}
	return p.publish(ctx, events.MediaTranscodeRequested, &uploaderID, payload)
}

func (p *Producer) PublishTranscodeCompleted(ctx context.Context, mediaAssetID uuid.UUID, processingStatus string) error {
	payload := events.MediaTranscodeCompletedPayload{
		MediaAssetID:     mediaAssetID.String(),
		ProcessingStatus: processingStatus,
	}
	return p.publish(ctx, events.MediaTranscodeCompleted, nil, payload)
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
