package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

type Producer struct {
	writer *kafka.Writer
	rdb    *redis.Client
}

func NewProducer(brokers []string, topic string, rdb *redis.Client) *Producer {
	w := &kafka.Writer{
		Addr:     kafka.TCP(brokers...),
		Topic:    topic,
		Balancer: &kafka.LeastBytes{},
	}
	return &Producer{writer: w, rdb: rdb}
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

func (p *Producer) PublishChannelUpdateEchoed(ctx context.Context, channelID, updateID, userID uuid.UUID, echoType string) error {
	payload := ChannelUpdateEchoedPayload{
		ChannelID: channelID.String(),
		UpdateID:  updateID.String(),
		UserID:    userID.String(),
		EchoType:  echoType,
		EchoedAt:  time.Now(),
	}
	return p.publish(ctx, EventChannelUpdateEchoed, &userID, payload)
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

// --- Comment event publishers (dual: Kafka + Redis realtime) ---

func (p *Producer) PublishCommentCreated(ctx context.Context, channelID, updateID, commentID, authorID uuid.UUID, body string, parentID string) error {
	now := time.Now()
	payload := CommentCreatedPayload{
		CommentID: commentID.String(),
		UpdateID:  updateID.String(),
		ChannelID: channelID.String(),
		AuthorID:  authorID.String(),
		Body:      body,
		ParentID:  parentID,
		CreatedAt: now,
	}

	// Kafka (durable, cross-service)
	if err := p.publish(ctx, EventChannelCommentCreated, &authorID, payload); err != nil {
		slog.Warn("kafka publish comment.created failed", "error", err)
	}

	// Redis (realtime fanout to ws-gateway)
	p.publishRealtime(ctx, updateID.String(), "comment_created", map[string]any{
		"event_id":   uuid.New().String(),
		"update_id":  updateID.String(),
		"channel_id": channelID.String(),
		"comment_id": commentID.String(),
		"author_id":  authorID.String(),
		"body":       body,
		"parent_id":  parentID,
		"created_at": now.Format(time.RFC3339Nano),
	})
	return nil
}

func (p *Producer) PublishCommentDeleted(ctx context.Context, channelID, updateID, commentID, actorID uuid.UUID) error {
	now := time.Now()
	payload := CommentDeletedPayload{
		CommentID: commentID.String(),
		UpdateID:  updateID.String(),
		ChannelID: channelID.String(),
		ActorID:   actorID.String(),
		DeletedAt: now,
	}

	if err := p.publish(ctx, EventChannelCommentDeleted, &actorID, payload); err != nil {
		slog.Warn("kafka publish comment.deleted failed", "error", err)
	}

	p.publishRealtime(ctx, updateID.String(), "comment_deleted", map[string]any{
		"event_id":   uuid.New().String(),
		"update_id":  updateID.String(),
		"channel_id": channelID.String(),
		"comment_id": commentID.String(),
		"actor_id":   actorID.String(),
	})
	return nil
}

func (p *Producer) PublishCommentUpdated(ctx context.Context, channelID, updateID, commentID, actorID uuid.UUID, body string) error {
	now := time.Now()
	payload := CommentUpdatedPayload{
		CommentID: commentID.String(),
		UpdateID:  updateID.String(),
		ChannelID: channelID.String(),
		ActorID:   actorID.String(),
		Body:      body,
		UpdatedAt: now,
	}

	if err := p.publish(ctx, EventChannelCommentUpdated, &actorID, payload); err != nil {
		slog.Warn("kafka publish comment.updated failed", "error", err)
	}

	p.publishRealtime(ctx, updateID.String(), "comment_updated", map[string]any{
		"event_id":   uuid.New().String(),
		"update_id":  updateID.String(),
		"channel_id": channelID.String(),
		"comment_id": commentID.String(),
		"actor_id":   actorID.String(),
		"body":       body,
		"updated_at": now.Format(time.RFC3339Nano),
	})
	return nil
}

// publishRealtime sends a compact JSON event to Redis for ws-gateway fanout.
func (p *Producer) publishRealtime(ctx context.Context, updateID, updateType string, payload map[string]any) {
	if p.rdb == nil {
		return
	}
	payload["update_type"] = updateType
	msg := map[string]any{
		"type":    "comment_update",
		"payload": payload,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		slog.Warn("failed to marshal realtime comment event", "error", err)
		return
	}
	channel := fmt.Sprintf("update:%s", updateID)
	if err := p.rdb.Publish(ctx, channel, string(data)).Err(); err != nil {
		slog.Warn("redis publish comment event failed", "error", err, "channel", channel)
	}
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
	EventChannelUpdateEchoed    = "channel.update.echoed"
	EventChannelCommentCreated  = "channel.comment.created"
	EventChannelCommentDeleted  = "channel.comment.deleted"
	EventChannelCommentUpdated  = "channel.comment.updated"
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

type CommentCreatedPayload struct {
	CommentID string    `json:"comment_id"`
	UpdateID  string    `json:"update_id"`
	ChannelID string    `json:"channel_id"`
	AuthorID  string    `json:"author_id"`
	Body      string    `json:"body"`
	ParentID  string    `json:"parent_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type CommentDeletedPayload struct {
	CommentID string    `json:"comment_id"`
	UpdateID  string    `json:"update_id"`
	ChannelID string    `json:"channel_id"`
	ActorID   string    `json:"actor_id"`
	DeletedAt time.Time `json:"deleted_at"`
}

type ChannelUpdateEchoedPayload struct {
	ChannelID string    `json:"channel_id"`
	UpdateID  string    `json:"update_id"`
	UserID    string    `json:"user_id"`
	EchoType  string    `json:"echo_type"`
	EchoedAt  time.Time `json:"echoed_at"`
}

type CommentUpdatedPayload struct {
	CommentID string    `json:"comment_id"`
	UpdateID  string    `json:"update_id"`
	ChannelID string    `json:"channel_id"`
	ActorID   string    `json:"actor_id"`
	Body      string    `json:"body"`
	UpdatedAt time.Time `json:"updated_at"`
}
