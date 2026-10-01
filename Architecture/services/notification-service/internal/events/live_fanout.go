package events

import (
	"context"
	"log/slog"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/notification-service/internal/store/postgres"
	sharedevents "github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// Enqueue side of "creator is live" (Live in PostTube and Reels, 2 Oct
// 2026). live.stream.started used to push every follower from an untracked
// goroutine capped at 5,000; it now records one durable fan-out job, the
// same job an upload records, and the worker tells the stream's reminder
// holders and the channel's subscribers (service/live_fanout.go).

// liveFanout is the extra surface a live job needs from the pipeline: two
// cheap "is anyone there?" probes so a stream nobody is waiting for does
// not leave a job row behind.
type liveFanout interface {
	HasReminders(ctx context.Context, streamID uuid.UUID) (bool, error)
	HasSubscribers(ctx context.Context, channelID uuid.UUID) (bool, error)
}

// enqueueLiveFanout persists the fan-out job for a stream that just went
// live. Synchronous, like enqueueSubscriberFanout, so a committed offset
// implies durable work.
//
// Gates applied here:
//   - visibility: only public and followers streams are announced. Paid,
//     private and anything unrecognised stay silent.
//   - audience: no reminder holders AND no channel subscribers ⇒ no job.
//     A probe that fails counts as "maybe": the job is recorded and the
//     worker, which retries, finds out. Never the follower list.
//
// The creator is excluded and a person in both groups is told once at
// delivery (the per-(stream, user) delivered marker), not here.
func (c *Consumer) enqueueLiveFanout(ctx context.Context, e sharedevents.LiveStreamStartedPayload) error {
	if c.fanout == nil {
		return nil
	}
	if !service.LiveVisibilityNotifies(e.Visibility) {
		return nil
	}

	streamID, err := uuid.Parse(e.StreamID)
	if err != nil {
		return nil // malformed payload — don't retry forever
	}
	// creator_user_id is the newer name for the same value; events written
	// before it existed carry creator_id only.
	rawCreator := e.CreatorUserID
	if rawCreator == "" {
		rawCreator = e.CreatorID
	}
	creatorID, err := uuid.Parse(rawCreator)
	if err != nil {
		return nil
	}

	// uuid.Nil when the creator has no channel: reminder holders only.
	channelID := c.fanout.ResolveChannel(ctx, creatorID)

	if probe, ok := c.fanout.(liveFanout); ok {
		hasReminders, rerr := probe.HasReminders(ctx, streamID)
		if rerr != nil {
			slog.Warn("live fanout: reminder probe failed; enqueueing so the worker retries",
				"stream_id", streamID, "error", rerr)
		}
		hasSubscribers, serr := probe.HasSubscribers(ctx, channelID)
		if serr != nil {
			slog.Warn("live fanout: subscriber probe failed; enqueueing so the worker retries",
				"stream_id", streamID, "error", serr)
		}
		if rerr == nil && serr == nil && !hasReminders && !hasSubscribers {
			slog.Debug("live fanout: nobody to notify", "stream_id", streamID, "creator_id", creatorID)
			return nil
		}
	}

	startedAt := e.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}

	return c.fanout.Enqueue(ctx, service.EnqueueParams{
		PostID:      streamID, // the job is keyed on the stream
		AuthorID:    creatorID,
		ChannelID:   channelID,
		ContentType: service.LiveContentType,
		Visibility:  e.Visibility,
		DeepLink:    service.LiveDeepLink(e.Orientation, e.StreamID),
		NotifType:   service.LiveNotifType,
		CreatedAt:   startedAt,
		Title:       e.Title,
		Phase:       postgres.FanoutPhaseReminders,
	})
}
