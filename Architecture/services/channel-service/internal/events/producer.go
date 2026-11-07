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
	// fanoutWriter has no default topic: it carries the rich
	// UpdatePublishedEvent to FanoutTopic (the worker that notifies
	// subscribers) — a different contract from the channel-events envelope.
	fanoutWriter *kafka.Writer
	rdb          *redis.Client
}

// FanoutTopic is consumed by workers.FanoutWorker.
const FanoutTopic = "atpost.channel.updates"

func NewProducer(brokers []string, topic string, rdb *redis.Client) *Producer {
	return NewProducerWithDialer(brokers, topic, rdb, nil)
}

func NewProducerWithDialer(brokers []string, topic string, rdb *redis.Client, dialer *kafka.Dialer) *Producer {
	w := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  brokers,
		Topic:    topic,
		// Hash, not LeastBytes: LeastBytes ignores the message key, so keying
		// lifecycle events by channel id would order nothing (a stale edit could
		// land after a suspension and re-list the channel in search).
		Balancer: &kafka.Hash{},
		Dialer:   dialer,
	})
	fw := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  brokers,
		Balancer: &kafka.LeastBytes{},
		Dialer:   dialer,
	})
	return &Producer{writer: w, fanoutWriter: fw, rdb: rdb}
}

// FanoutUpdatePayload mirrors workers.UpdatePublishedPayload (kept local so
// events does not import workers).
type FanoutUpdatePayload struct {
	UpdateID    string `json:"update_id"`
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	AuthorID    string `json:"author_id"`
	UpdateType  string `json:"update_type"`
	Title       string `json:"title"`
	BodyPreview string `json:"body_preview"`
	DeepLink    string `json:"deep_link"`
	PublishedAt string `json:"published_at"`
}

// PublishFanoutUpdate emits the subscriber fan-out event for an update that
// was published immediately (chat-app pass). Synchronous: the caller has
// already committed the row, and a lost event means nobody is told.
func (p *Producer) PublishFanoutUpdate(ctx context.Context, channelID uuid.UUID, channelName string, updateID, authorID uuid.UUID, updateType string, title *string, body string) error {
	if p == nil || p.fanoutWriter == nil {
		return nil
	}
	t := ""
	if title != nil {
		t = *title
	}
	preview := body
	if len(preview) > 200 {
		preview = preview[:200]
	}
	event := map[string]any{
		"event_type": EventChannelUpdatePublished,
		"event_id":   uuid.New().String(),
		"timestamp":  time.Now().UTC(),
		"payload": FanoutUpdatePayload{
			UpdateID:    updateID.String(),
			ChannelID:   channelID.String(),
			ChannelName: channelName,
			AuthorID:    authorID.String(),
			UpdateType:  updateType,
			Title:       t,
			BodyPreview: preview,
			DeepLink:    fmt.Sprintf("/channels/%s/updates/%s", channelID, updateID),
			PublishedAt: time.Now().UTC().Format(time.RFC3339),
		},
	}
	b, err := json.Marshal(event)
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return p.fanoutWriter.WriteMessages(writeCtx, kafka.Message{
		Topic: FanoutTopic,
		Key:   []byte(channelID.String()),
		Value: b,
	})
}

// PublishChannelCreated announces a new channel. The top-level fields are the
// original contract; Channel carries the whole searchable state so a consumer
// can index the handle and description too.
func (p *Producer) PublishChannelCreated(ctx context.Context, snap ChannelSnapshot) error {
	ownerID, err := uuid.Parse(snap.OwnerID)
	if err != nil {
		return fmt.Errorf("channel.created: owner id: %w", err)
	}
	return p.publishKeyed(ctx, EventChannelCreated, &ownerID, snap.ChannelID,
		NewChannelCreatedPayload(snap, time.Now()))
}

// PublishChannelUpdated announces a channel's state after any change a
// consumer may care about: an edit, a suspension or its lifting, an archive.
// actorID is nil for a system change (the internal moderation key, the
// account-deletion consumer).
func (p *Producer) PublishChannelUpdated(ctx context.Context, snap ChannelSnapshot, actorID *uuid.UUID) error {
	return p.publishKeyed(ctx, EventChannelUpdated, actorID, snap.ChannelID,
		NewChannelUpdatedPayload(snap, actorID, time.Now()))
}

func (p *Producer) PublishChannelDeleted(ctx context.Context, channelID, actorID uuid.UUID) error {
	payload := ChannelDeletedPayload{
		ChannelID: channelID.String(),
		ActorID:   actorID.String(),
		DeletedAt: time.Now(),
	}
	return p.publishKeyed(ctx, EventChannelDeleted, &actorID, channelID.String(), payload)
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

// PublishChannelReportFiled emits a trust & safety intake event so a
// consumer can pick reports up without polling channel_reports. Added with
// the invite-only pilot (2026-09-12): a reporter used to get 202 and the row
// was never read by anything but a rate-limit COUNT.
func (p *Producer) PublishChannelReportFiled(ctx context.Context, reportID, channelID uuid.UUID, updateID *uuid.UUID, reporterID uuid.UUID, reason string) error {
	payload := ChannelReportFiledPayload{
		ReportID:   reportID.String(),
		ChannelID:  channelID.String(),
		ReporterID: reporterID.String(),
		Reason:     reason,
		FiledAt:    time.Now(),
	}
	if updateID != nil {
		payload.UpdateID = updateID.String()
	}
	return p.publish(ctx, EventChannelReportFiled, &reporterID, payload)
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

// publish serializes the envelope synchronously and writes to Kafka
// asynchronously. Audit HCh3: every Publish* method previously blocked
// the request on broker ACK (50-200 ms). Downstream consumers tolerate
// at-least-once + duplicates, so a transient broker blip becomes a
// logged warning rather than a user-facing failure.
func (p *Producer) publish(ctx context.Context, eventType string, actorID *uuid.UUID, payload any) error {
	return p.publishKeyed(ctx, eventType, actorID, "", payload)
}

// publishKeyed is publish with an explicit partition key. The channel
// lifecycle events (created, updated, deleted) are keyed by channel id so
// they stay in order on one partition: a consumer that applies "suspended"
// and then a stale "active" edit would put a suspended channel back in
// search. An empty key falls back to the event id, which spreads the
// high-volume events (subscribes, comments) across partitions.
func (p *Producer) publishKeyed(ctx context.Context, eventType string, actorID *uuid.UUID, key string, payload any) error {
	if p == nil || p.writer == nil {
		return nil
	}
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

	if key == "" {
		key = envelope.EventID
	}
	msg := kafka.Message{
		Key:   []byte(key),
		Value: envelopeBytes,
	}
	go func() {
		writeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := p.writer.WriteMessages(writeCtx, msg); err != nil {
			slog.Warn("channel-events: async Kafka publish failed",
				"event_type", eventType, "event_id", envelope.EventID, "err", err)
		}
	}()
	return nil
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
	if p.fanoutWriter != nil {
		_ = p.fanoutWriter.Close()
	}
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
	EventChannelReportFiled     = "channel.report.filed"
	EventChannelUpdateEchoed    = "channel.update.echoed"
	EventChannelCommentCreated  = "channel.comment.created"
	EventChannelCommentDeleted  = "channel.comment.deleted"
	EventChannelCommentUpdated  = "channel.comment.updated"
)

// --- Payload types ---

// ChannelSnapshot is a channel's consumer-facing state as committed, carried
// whole on channel.created and channel.updated (2026-11-07). search-service
// indexes from it: it used to get ids only on channel.updated and could not
// apply an edit, a suspension or a change of type at all.
//
// It is a SNAPSHOT, not a diff: every field is the row's current value, so
// an empty description means the description is empty. A consumer tells a
// snapshot event from an older id-only one by the presence of the "channel"
// object. Contract fixture: testdata/contracts/channel_updated.json.
type ChannelSnapshot struct {
	ChannelID   string `json:"channel_id"`
	OwnerID     string `json:"owner_id"`
	Handle      string `json:"handle"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// ChannelType is the authority for who may find the channel;
	// Visibility (public|private) is the wire field derived from it.
	ChannelType     string `json:"channel_type"`
	Visibility      string `json:"visibility"`
	Category        string `json:"category"`
	IsVerified      bool   `json:"is_verified"`
	SubscriberCount int64  `json:"subscriber_count"`
	// Status: active | suspended | archived | deleted. Only active is
	// servable.
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ChannelCreatedPayload struct {
	ChannelID   string    `json:"channel_id"`
	OwnerID     string    `json:"owner_id"`
	Name        string    `json:"name"`
	ChannelType string    `json:"channel_type"`
	CreatedAt   time.Time `json:"created_at"`
	// Channel is the full state (added 2026-11-07; absent on older events).
	Channel *ChannelSnapshot `json:"channel,omitempty"`
}

// NewChannelCreatedPayload fills the original top-level fields from the
// snapshot, so a consumer of the old contract reads exactly what it did.
func NewChannelCreatedPayload(snap ChannelSnapshot, now time.Time) ChannelCreatedPayload {
	created := snap.CreatedAt
	if created.IsZero() {
		created = now
	}
	return ChannelCreatedPayload{
		ChannelID:   snap.ChannelID,
		OwnerID:     snap.OwnerID,
		Name:        snap.Name,
		ChannelType: snap.ChannelType,
		CreatedAt:   created,
		Channel:     &snap,
	}
}

type ChannelUpdatedPayload struct {
	ChannelID string `json:"channel_id"`
	// ActorID is "" for a system change (no person acted).
	ActorID   string    `json:"actor_id"`
	UpdatedAt time.Time `json:"updated_at"`
	// Channel is the full state after the change (added 2026-11-07; absent
	// on older events, which carried the three fields above only).
	Channel *ChannelSnapshot `json:"channel,omitempty"`
}

// NewChannelUpdatedPayload builds channel.updated from the snapshot.
func NewChannelUpdatedPayload(snap ChannelSnapshot, actorID *uuid.UUID, now time.Time) ChannelUpdatedPayload {
	out := ChannelUpdatedPayload{
		ChannelID: snap.ChannelID,
		UpdatedAt: now,
		Channel:   &snap,
	}
	if actorID != nil {
		out.ActorID = actorID.String()
	}
	if !snap.UpdatedAt.IsZero() {
		out.UpdatedAt = snap.UpdatedAt
	}
	return out
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

type ChannelReportFiledPayload struct {
	ReportID   string    `json:"report_id"`
	ChannelID  string    `json:"channel_id"`
	UpdateID   string    `json:"update_id,omitempty"`
	ReporterID string    `json:"reporter_id"`
	Reason     string    `json:"reason"`
	FiledAt    time.Time `json:"filed_at"`
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
