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
	w := &kafka.Writer{
		Addr:     kafka.TCP(brokers...),
		Topic:    topic,
		Balancer: &kafka.LeastBytes{},
	}
	return &Producer{writer: w}
}

func (p *Producer) PublishChannelCreated(ctx context.Context, channelID, ownerID uuid.UUID, name, channelType string) error {
	payload := ChannelCreatedPayload{
		ChannelID:   channelID.String(),
		OwnerID:     ownerID.String(),
		Name:        name,
		ChannelType: channelType,
		CreatedAt:   time.Now(),
	}
	return p.publish(ctx, EventChannelCreated, &ownerID, payload)
}

func (p *Producer) PublishChannelUpdated(ctx context.Context, channelID, actorID uuid.UUID) error {
	payload := ChannelUpdatedPayload{
		ChannelID: channelID.String(),
		ActorID:   actorID.String(),
		UpdatedAt: time.Now(),
	}
	return p.publish(ctx, EventChannelUpdated, &actorID, payload)
}

func (p *Producer) PublishChannelDeleted(ctx context.Context, channelID, actorID uuid.UUID) error {
	payload := ChannelDeletedPayload{
		ChannelID: channelID.String(),
		ActorID:   actorID.String(),
		DeletedAt: time.Now(),
	}
	return p.publish(ctx, EventChannelDeleted, &actorID, payload)
}

func (p *Producer) PublishChannelSubscribed(ctx context.Context, channelID, userID uuid.UUID) error {
	payload := ChannelSubscribedPayload{
		ChannelID:    channelID.String(),
		UserID:       userID.String(),
		SubscribedAt: time.Now(),
	}
	return p.publish(ctx, EventChannelSubscribed, &userID, payload)
}

func (p *Producer) PublishChannelUnsubscribed(ctx context.Context, channelID, userID uuid.UUID) error {
	payload := ChannelUnsubscribedPayload{
		ChannelID:      channelID.String(),
		UserID:         userID.String(),
		UnsubscribedAt: time.Now(),
	}
	return p.publish(ctx, EventChannelUnsubscribed, &userID, payload)
}

func (p *Producer) PublishChannelUpdatePublished(ctx context.Context, channelID, updateID, authorID uuid.UUID) error {
	payload := ChannelUpdatePublishedPayload{
		ChannelID:   channelID.String(),
		UpdateID:    updateID.String(),
		AuthorID:    authorID.String(),
		PublishedAt: time.Now(),
	}
	return p.publish(ctx, EventChannelUpdatePublished, &authorID, payload)
}

func (p *Producer) PublishChannelUpdateDeleted(ctx context.Context, channelID, updateID, actorID uuid.UUID) error {
	payload := ChannelUpdateDeletedPayload{
		ChannelID: channelID.String(),
		UpdateID:  updateID.String(),
		ActorID:   actorID.String(),
		DeletedAt: time.Now(),
	}
	return p.publish(ctx, EventChannelUpdateDeleted, &actorID, payload)
}

func (p *Producer) PublishChannelMemberBanned(ctx context.Context, channelID, userID, bannedBy uuid.UUID) error {
	payload := ChannelMemberBannedPayload{
		ChannelID: channelID.String(),
		UserID:    userID.String(),
		BannedBy:  bannedBy.String(),
		BannedAt:  time.Now(),
	}
	return p.publish(ctx, EventChannelMemberBanned, &bannedBy, payload)
}

func (p *Producer) publish(ctx context.Context, eventType string, actorID *uuid.UUID, payload any) error {
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

// --- Event type constants ---

const (
	EventChannelCreated         = "channel.created"
	EventChannelUpdated         = "channel.updated"
	EventChannelDeleted         = "channel.deleted"
	EventChannelSubscribed      = "channel.subscribed"
	EventChannelUnsubscribed    = "channel.unsubscribed"
	EventChannelUpdatePublished = "channel.update.published"
	EventChannelUpdateDeleted   = "channel.update.deleted"
	EventChannelMemberBanned    = "channel.member.banned"
)

// --- Payload types ---

type ChannelCreatedPayload struct {
	ChannelID   string    `json:"channel_id"`
	OwnerID     string    `json:"owner_id"`
	Name        string    `json:"name"`
	ChannelType string    `json:"channel_type"`
	CreatedAt   time.Time `json:"created_at"`
}

type ChannelUpdatedPayload struct {
	ChannelID string    `json:"channel_id"`
	ActorID   string    `json:"actor_id"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ChannelDeletedPayload struct {
	ChannelID string    `json:"channel_id"`
	ActorID   string    `json:"actor_id"`
	DeletedAt time.Time `json:"deleted_at"`
}

type ChannelSubscribedPayload struct {
	ChannelID    string    `json:"channel_id"`
	UserID       string    `json:"user_id"`
	SubscribedAt time.Time `json:"subscribed_at"`
}

type ChannelUnsubscribedPayload struct {
	ChannelID      string    `json:"channel_id"`
	UserID         string    `json:"user_id"`
	UnsubscribedAt time.Time `json:"unsubscribed_at"`
}

type ChannelUpdatePublishedPayload struct {
	ChannelID   string    `json:"channel_id"`
	UpdateID    string    `json:"update_id"`
	AuthorID    string    `json:"author_id"`
	PublishedAt time.Time `json:"published_at"`
}

type ChannelUpdateDeletedPayload struct {
	ChannelID string    `json:"channel_id"`
	UpdateID  string    `json:"update_id"`
	ActorID   string    `json:"actor_id"`
	DeletedAt time.Time `json:"deleted_at"`
}

type ChannelMemberBannedPayload struct {
	ChannelID string    `json:"channel_id"`
	UserID    string    `json:"user_id"`
	BannedBy  string    `json:"banned_by"`
	BannedAt  time.Time `json:"banned_at"`
}
