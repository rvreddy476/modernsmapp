package events

import (
	"context"
	"log/slog"

	"github.com/atpost/notification-service/internal/service"
	sharedevents "github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// Module 1 P0-3 — enqueue side of subscriber upload notifications.

// subscriberFanout is what the consumer needs from the durable pipeline.
// An interface so the gating below is testable with a recorder instead
// of a Postgres-backed SubscriberFanout.
type subscriberFanout interface {
	Enqueue(ctx context.Context, p service.EnqueueParams) error
	ResolveChannel(ctx context.Context, authorID uuid.UUID) uuid.UUID
}

// WithSubscriberFanout attaches the durable fan-out pipeline. Without it
// the consumer skips upload notifications entirely (it never falls back
// to follower fan-out). A nil pointer stays "not attached" rather than
// becoming a non-nil interface around nil.
func (c *Consumer) WithSubscriberFanout(f *service.SubscriberFanout) *Consumer {
	if f != nil {
		c.fanout = f
	}
	return c
}

// enqueueSubscriberFanout persists an upload fan-out job. Runs
// synchronously inside the consumer so a committed Kafka offset always
// implies durable work; the actual delivery is done by the worker.
//
// Gates applied here (cheap, per-event):
//   - content type: only video/flick uploads notify. Text/photo/poll
//     posts flow through the feed and never push-spam subscribers.
//     (The previous implementation's default branch fanned these out
//     despite a comment claiming otherwise — Codex finding #2.)
//   - visibility: private/unlisted never notify.
//   - creator choice: distribution.notify_subscribers=false suppresses.
//   - channel: no channel ⇒ no notification. No follower fallback.
func (c *Consumer) enqueueSubscriberFanout(ctx context.Context, e sharedevents.PostCreatedPayload) error {
	if c.fanout == nil {
		return nil
	}

	// Routing lives in the service package so enqueue and delivery can
	// never disagree about where a tap lands.
	notifType := service.UploadNotifType(e.ContentType)
	if notifType == "" {
		// Not an upload — nothing to notify subscribers about.
		return nil
	}
	deepLink := service.UploadDeepLink(e.ContentType, e.PostID)

	if e.Visibility == "private" || e.Visibility == "unlisted" {
		return nil
	}

	// Creator choice (P0-1 policy). Legacy events omit the field: nil
	// means "no policy", which keeps the pre-existing notify behavior.
	if e.NotifySubscribers != nil && !*e.NotifySubscribers {
		slog.Debug("fanout: suppressed by creator notify_subscribers=false", "post_id", e.PostID)
		return nil
	}

	postID, err := uuid.Parse(e.PostID)
	if err != nil {
		return nil // malformed payload — don't retry forever
	}
	authorID, err := uuid.Parse(e.AuthorID)
	if err != nil {
		return nil
	}

	// Subscriber fan-out is keyed on the author's channel. The producer
	// stamps channel_id; fall back to an internal lookup for events from
	// older producers. Still no channel ⇒ no notification (never
	// followers).
	channelID := uuid.Nil
	if e.ChannelID != "" {
		channelID, _ = uuid.Parse(e.ChannelID)
	}
	if channelID == uuid.Nil {
		channelID = c.fanout.ResolveChannel(ctx, authorID)
	}
	if channelID == uuid.Nil {
		slog.Debug("fanout: author has no channel; no subscriber notifications",
			"author_id", authorID, "post_id", postID)
		return nil
	}

	return c.fanout.Enqueue(ctx, service.EnqueueParams{
		PostID:      postID,
		AuthorID:    authorID,
		ChannelID:   channelID,
		ContentType: e.ContentType,
		Visibility:  e.Visibility,
		DeepLink:    deepLink,
		NotifType:   notifType,
		CreatedAt:   e.CreatedAt,
		// Render inputs ride the job so delivery needs no lookup. Older
		// producers leave them empty; the worker resolves the channel
		// name once per job in that case.
		Title:       e.Title,
		ChannelName: e.ChannelName,
	})
}
