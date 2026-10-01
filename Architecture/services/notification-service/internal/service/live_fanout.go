package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/atpost/notification-service/internal/graph"
	"github.com/atpost/notification-service/internal/livestream"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
)

// "Creator is live" notifications (Live in PostTube and Reels, 2 Oct 2026).
//
// Who is told when a stream goes live:
//   - everyone who set a reminder on the stream (paged from
//     live-service-v2's internal route), then
//   - the creator's channel subscribers with the bell on (the same
//     subscriber source the upload fan-out uses), then
//   - everyone who follows the creator (paged from graph-service's keyset
//     follower listing; founder decision, 2 Oct 2026).
//
// Never the creator, and one notification per person per stream however
// many of the three groups they are in.
//
// This is NOT a second pipeline. A live job is a row in
// subscriber_fanout_jobs keyed on the stream id, claimed, retried and
// cursor-resumed by the upload worker, delivered through deliverPage, and
// de-duplicated by subscriber_fanout_delivered. The only additions are a
// first phase that pages reminder holders, a last phase that pages
// followers, and copy that names the stream.

const (
	// LiveNotifType is the notification type; preference category `live`
	// (push_live / inapp_live, migration 005).
	LiveNotifType = "creator_went_live"
	// LiveContentType is what a live job stores in content_type.
	LiveContentType = "live"
	// liveEntityType is the inbox row's entity type; entity_id is the
	// stream id.
	liveEntityType = "live_stream"
	// liveNameFallback stands in for the creator's name when neither a
	// channel name nor a profile display name could be resolved.
	liveNameFallback = "A creator you follow"
	// There is deliberately no time limit on a live job (founder, 2 Oct
	// 2026: "always reach every follower, remove the 30 minute cutoff").
	// A job that is late, reclaimed after a crash, or walking a very long
	// follower list still tells everyone, even if the stream has ended by
	// then; the watch page says so when they arrive.
	// followerPageSize is the follower walk's page size: the most
	// graph-service's cursor listing serves in one page.
	followerPageSize = graph.FollowerPageMax
)

// LiveVisibilityNotifies reports whether a stream of this visibility is
// announced at all. An allowlist, so paid, private, unlisted and anything
// this service has not heard of stay silent.
func LiveVisibilityNotifies(visibility string) bool {
	return visibility == "public" || visibility == "followers"
}

// LiveDeepLink is where a tap on the notification lands: the wide PostTube
// room for a landscape stream, the vertical Reels room for a portrait one.
// Unknown or absent orientation is landscape, live-service-v2's default.
// The web zones and the Android deep-link table key on these exact paths.
func LiveDeepLink(orientation, streamID string) string {
	if orientation == "portrait" {
		return "/reels/live/" + streamID
	}
	return "/posttube/live/" + streamID
}

// reminderSource pages the users who set a reminder on a stream.
type reminderSource interface {
	ReminderUserIDs(ctx context.Context, streamID uuid.UUID, after string, limit int) (*livestream.Page, error)
}

// creatorNamer resolves a creator's display name for push copy when they
// have no channel to take a name from. The Service implements it over the
// profile batch read the inbox already uses.
type creatorNamer interface {
	CreatorDisplayName(ctx context.Context, userID uuid.UUID) string
}

// SetReminderSource attaches live-service-v2's reminder route. A nil
// client stays "not attached" rather than a non-nil interface around nil.
func (f *SubscriberFanout) SetReminderSource(c *livestream.Client) {
	if f != nil && c != nil {
		f.reminders = c
	}
}

// HasReminders reports whether at least one person set a reminder on the
// stream. An error means "could not tell"; the caller must then enqueue,
// because the durable job retries and a skipped job does not.
func (f *SubscriberFanout) HasReminders(ctx context.Context, streamID uuid.UUID) (bool, error) {
	if f == nil || f.reminders == nil {
		return false, nil
	}
	page, err := f.reminders.ReminderUserIDs(ctx, streamID, "", 1)
	if err != nil {
		return false, err
	}
	return len(page.IDs) > 0, nil
}

// HasSubscribers reports whether the channel has at least one subscriber
// with the bell on. Same error contract as HasReminders.
func (f *SubscriberFanout) HasSubscribers(ctx context.Context, channelID uuid.UUID) (bool, error) {
	if f == nil || f.subs == nil || channelID == uuid.Nil {
		return false, nil
	}
	page, err := f.subs.SubscriberIDs(ctx, channelID, uuid.Nil, 1)
	if err != nil {
		return false, err
	}
	return len(page.IDs) > 0, nil
}

// followerSource pages the users who follow a creator. `cursor` is the
// source's own token, "" for the first page.
type followerSource interface {
	FollowerIDs(ctx context.Context, userID uuid.UUID, cursor string, limit int) (*graph.FollowerPage, error)
}

// SetFollowerSource attaches graph-service's follower listing. A nil
// client stays "not attached" rather than a non-nil interface around nil.
func (f *SubscriberFanout) SetFollowerSource(c *graph.Client) {
	if f != nil && c != nil {
		f.followers = c
	}
}

// HasFollowers reports whether at least one person follows the creator.
// Same error contract as HasReminders.
func (f *SubscriberFanout) HasFollowers(ctx context.Context, creatorID uuid.UUID) (bool, error) {
	if f == nil || f.followers == nil || creatorID == uuid.Nil {
		return false, nil
	}
	page, err := f.followers.FollowerIDs(ctx, creatorID, "", 1)
	if err != nil {
		return false, err
	}
	return len(page.IDs) > 0, nil
}

// drainFollowers walks a live job's last group, the creator's followers,
// from the stored token to exhaustion. Every follower is paged: there is
// no recipient cap. It obeys the same rule as the other two walks: the
// token is persisted after every page and never moves past a page in
// which any recipient failed.
//
// People already told as reminder holders or subscribers are skipped by
// the per-(stream, user) delivered marker, the creator by deliverPage, and
// blocks, followers-only access and the `live` preference are applied per
// recipient exactly as for the other groups.
func (f *SubscriberFanout) drainFollowers(ctx context.Context, job *postgres.FanoutJob) error {
	if f.followers == nil {
		slog.Warn("fanout: no follower source wired; followers skipped", "stream_id", job.PostID)
		return nil
	}

	cursor := job.FollowerCursor
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// No time limit on the walk (founder, 2 Oct 2026: "always reach
		// every follower"): however long the list, everyone is told.

		page, err := f.followers.FollowerIDs(ctx, job.AuthorID, cursor, followerPageSize)
		if err != nil {
			return fmt.Errorf("follower page: %w", err)
		}

		var delivered, failed int64
		if len(page.IDs) > 0 {
			delivered, failed = f.deliverPage(ctx, job, page.IDs)
		}
		if failed > 0 {
			if err := f.pg.AdvanceFanoutFollowers(ctx, job.PostID, cursor, delivered); err != nil {
				slog.Warn("fanout: follower cursor persist failed", "stream_id", job.PostID, "error", err)
			}
			return fmt.Errorf("%d follower recipient(s) failed in page; will retry from cursor", failed)
		}

		// A next page exists only if the source hands back a token that
		// moves. One that does not move would re-fetch this page for ever.
		more := page.NextCursor != "" && page.NextCursor != cursor
		if more {
			cursor = page.NextCursor
		}
		if err := f.pg.AdvanceFanoutFollowers(ctx, job.PostID, cursor, delivered); err != nil {
			// A retry re-walks the page; the delivered markers make that a
			// no-op.
			return fmt.Errorf("advance follower cursor: %w", err)
		}
		job.FollowerCursor = cursor
		if !more {
			return nil
		}
	}
}

// liveCreatorName resolves the name for a live job whose creator has no
// channel name: the profile display name, once per job attempt. Copy, not
// a safety decision, so a failed lookup yields "" and renderLive falls
// back to neutral wording.
func (f *SubscriberFanout) liveCreatorName(ctx context.Context, job *postgres.FanoutJob) string {
	if f.names == nil {
		return ""
	}
	return f.names.CreatorDisplayName(ctx, job.AuthorID)
}

// drainReminders walks a live job's reminder holders from the stored
// token to exhaustion, then moves the job to the subscriber phase (from
// which processJob moves it on to the followers). It
// obeys the same rule as the subscriber walk: the cursor never moves past
// a page in which any recipient failed.
func (f *SubscriberFanout) drainReminders(ctx context.Context, job *postgres.FanoutJob) error {
	if f.reminders == nil {
		slog.Warn("fanout: no reminder source wired; reminder holders skipped", "stream_id", job.PostID)
		if err := f.pg.AdvanceFanoutReminders(ctx, job.PostID, job.ReminderCursor, 0, true); err != nil {
			return fmt.Errorf("advance reminder phase: %w", err)
		}
		job.Phase = postgres.FanoutPhaseSubscribers
		return nil
	}

	after := job.ReminderCursor
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		page, err := f.reminders.ReminderUserIDs(ctx, job.PostID, after, fanoutPageSize)
		if err != nil {
			return fmt.Errorf("reminder page: %w", err)
		}

		var delivered, failed int64
		if len(page.IDs) > 0 {
			delivered, failed = f.deliverPage(ctx, job, page.IDs)
		}
		if failed > 0 {
			if err := f.pg.AdvanceFanoutReminders(ctx, job.PostID, after, delivered, false); err != nil {
				slog.Warn("fanout: reminder cursor persist failed", "stream_id", job.PostID, "error", err)
			}
			return fmt.Errorf("%d reminder recipient(s) failed in page; will retry from cursor", failed)
		}

		// A next page exists only if the route says so AND hands back a
		// token that moves. A token that does not move would re-fetch this
		// page for ever.
		more := page.HasMore && len(page.IDs) > 0 && page.NextAfter != "" && page.NextAfter != after
		if more {
			after = page.NextAfter
		}
		if err := f.pg.AdvanceFanoutReminders(ctx, job.PostID, after, delivered, !more); err != nil {
			return fmt.Errorf("advance reminder cursor: %w", err)
		}
		if !more {
			job.Phase = postgres.FanoutPhaseSubscribers
			return nil
		}
	}
}

// renderLive is the push copy and collapse key for one "is live" notice:
// "{creator} is live: {title}". n.ChannelName carries the creator's name
// (their channel name, else their profile display name).
func renderLive(n UploadNotification) RenderOverride {
	name := n.ChannelName
	if name == "" {
		name = liveNameFallback
	}
	title := name + " is live"
	if n.Title != "" {
		title = RenderTitle(GetTemplate(LiveNotifType).TitleTemplate,
			map[string]string{"creator": name, "title": n.Title})
	}
	return RenderOverride{
		Title: title,
		Body:  GetTemplate(LiveNotifType).BodyTemplate,
		// Per STREAM: a redelivery replaces itself on the device.
		CollapseKey: GetCollapseKey(LiveNotifType, n.PostID.String(), n.RecipientID.String()),
	}
}

// CreatorDisplayName is the creator's profile display name (username as a
// second choice), or "" when it cannot be resolved.
func (s *Service) CreatorDisplayName(ctx context.Context, userID uuid.UUID) string {
	if s.profileServiceURL == "" || userID == uuid.Nil {
		return ""
	}
	profiles, err := s.fetchActorProfiles(ctx, []string{userID.String()})
	if err != nil {
		slog.Warn("fanout: creator name lookup failed; using neutral copy", "creator_id", userID, "error", err)
		return ""
	}
	p := profiles[userID]
	if p.DisplayName != "" {
		return p.DisplayName
	}
	return p.Username
}
