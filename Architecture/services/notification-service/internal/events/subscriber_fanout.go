package events

import (
	"context"
	"log/slog"
	"strings"
	"time"

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
	// LookupChannel is ResolveChannel with the failure kept apart from
	// "no channel" (uuid.Nil, nil), for a caller that must not drop work
	// on a lookup error.
	LookupChannel(ctx context.Context, authorID uuid.UUID) (uuid.UUID, error)
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
	outcome, err := c.enqueueSubscriberFanoutOutcome(ctx, e)
	countFanoutTrigger(fanoutTriggerPostCreated, outcome)
	return err
}

func (c *Consumer) enqueueSubscriberFanoutOutcome(ctx context.Context, e sharedevents.PostCreatedPayload) (string, error) {
	if c.fanout == nil {
		return triggerDroppedNoPipeline, nil
	}

	// Routing lives in the service package so enqueue and delivery can
	// never disagree about where a tap lands.
	notifType := service.UploadNotifType(e.ContentType)
	if notifType == "" {
		// Not an upload — nothing to notify subscribers about.
		return triggerIgnoredNotUpload, nil
	}
	deepLink := service.UploadDeepLink(e.ContentType, e.PostID)

	if e.Visibility == "private" || e.Visibility == "unlisted" {
		return triggerIgnoredAudience, nil
	}

	// Creator choice (P0-1 policy). Legacy events omit the field: nil
	// means "no policy", which keeps the pre-existing notify behavior.
	if e.NotifySubscribers != nil && !*e.NotifySubscribers {
		slog.Debug("fanout: suppressed by creator notify_subscribers=false", "post_id", e.PostID)
		return triggerIgnoredOptedOut, nil
	}

	postID, err := uuid.Parse(e.PostID)
	if err != nil {
		return triggerDroppedMalformed, nil // malformed payload — don't retry forever
	}
	authorID, err := uuid.Parse(e.AuthorID)
	if err != nil {
		return triggerDroppedMalformed, nil
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
		return triggerIgnoredNoChannel, nil
	}

	return enqueueOutcome(c.fanout.Enqueue(ctx, service.EnqueueParams{
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
	}))
}

// visibleTriggerWindow bounds which posts the "post became visible" trigger
// may announce: only uploads created this recently. It stays inside the
// 7-day retention of completed fan-out rows (CleanupFanoutRecords), so a
// post announced earlier always still has the row that makes a second
// trigger a no-op, and a months-old video made public is not pushed to
// every subscriber as if it were new.
//
// Known limit, kept on purpose (product decision, 18 Nov): an upload whose
// FIRST public moment comes more than 6 days after it was created (kept
// private or unlisted for a week, or a scheduled post published private
// and opened up later) is never announced. Lifting it safely needs more
// than a longer window: the row that stops a second announcement is
// deleted 7 days after completion, so a post announced, cleaned up and then
// made private and public again would be pushed to every subscriber a
// second time. The fix, if wanted, is a permanent post_id tombstone written
// on completion and checked here before the window, plus the decision that
// an old video's first publication is news to its subscribers.
const visibleTriggerWindow = 6 * 24 * time.Hour

// fanoutNow is the trigger's clock; tests pin it.
var fanoutNow = time.Now

// enqueueOnVisible is the second trigger for the upload fan-out (lv-1,
// lv-17, reels-30). post-service writes PostSearchEligibilityChanged in the
// same transaction as every review-status and visibility change, including
// the review gate's flip when a video finishes processing. When the event
// says the post is now approved, published and in an audience that
// notifies, the upload job is enqueued, or the waiting job for it is woken.
//
// Exactly once: the job table holds one row per post, so PostCreated and
// this trigger (and every repeat of either) share it; a completed job is
// never run again (store EnqueueFanoutJob). The event does not carry the
// creator's notify_subscribers choice; the worker reads it from the post
// before anyone is told.
func (c *Consumer) enqueueOnVisible(ctx context.Context, e sharedevents.PostSearchEligibilityChangedPayload) error {
	outcome, err := c.enqueueOnVisibleOutcome(ctx, e)
	countFanoutTrigger(fanoutTriggerPostVisible, outcome)
	return err
}

func (c *Consumer) enqueueOnVisibleOutcome(ctx context.Context, e sharedevents.PostSearchEligibilityChangedPayload) (string, error) {
	if c.fanout == nil {
		return triggerDroppedNoPipeline, nil
	}
	notifType := service.UploadNotifType(e.ContentType)
	if notifType == "" {
		return triggerIgnoredNotUpload, nil
	}
	if e.Deleted || e.Scheduled || e.Restricted {
		return triggerIgnoredState, nil
	}
	if strings.ToLower(e.ReviewStatus) != "approved" {
		return triggerIgnoredState, nil
	}
	switch strings.ToLower(e.Visibility) {
	case "", "public", "followers", "friends":
	default:
		// private, unlisted, staged, retired audiences: nothing to announce.
		return triggerIgnoredAudience, nil
	}
	if e.CreatedAt.IsZero() || fanoutNow().Sub(e.CreatedAt) > visibleTriggerWindow {
		return triggerIgnoredTooOld, nil
	}

	postID, err := uuid.Parse(e.PostID)
	if err != nil {
		return triggerDroppedMalformed, nil // malformed payload — don't retry forever
	}
	authorID, err := uuid.Parse(e.AuthorID)
	if err != nil {
		return triggerDroppedMalformed, nil
	}
	// A lookup that FAILS must not lose the trigger: for a post first
	// published private it is the only one there will be. Returning the
	// error would not help (this event type is committed when it is
	// queued, and holding the read loop on post-service would stall every
	// notification), so the job is enqueued without a channel and the
	// worker resolves it, retrying with backoff (SubscriberFanout.
	// processJob). Only a clean "no channel" answer skips the job.
	channelID, err := c.fanout.LookupChannel(ctx, authorID)
	if err != nil {
		slog.Warn("fanout: channel lookup failed on the visible trigger; the worker resolves it",
			"author_id", authorID, "post_id", postID, "error", err)
		channelID = uuid.Nil
	} else if channelID == uuid.Nil {
		return triggerIgnoredNoChannel, nil // no channel ⇒ no subscribers; never followers
	}

	return enqueueOutcome(c.fanout.Enqueue(ctx, service.EnqueueParams{
		PostID:      postID,
		AuthorID:    authorID,
		ChannelID:   channelID,
		ContentType: e.ContentType,
		Visibility:  e.Visibility,
		DeepLink:    service.UploadDeepLink(e.ContentType, e.PostID),
		NotifType:   notifType,
		CreatedAt:   e.CreatedAt,
		// Empty for a gated or non-public post (post-service keeps the body
		// off the bus); the worker fills it from the post and resolves the
		// channel name once per job.
		Title: e.Title,
	}))
}
