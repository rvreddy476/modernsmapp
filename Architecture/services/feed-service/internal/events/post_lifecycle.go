package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/atpost/feed-service/internal/service"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// Soft delete → restore (product decision 2026-09-04).
//
// PostDeleted must make the post vanish from everyone's home and reels feeds
// AT ONCE. Three things hide a post from a viewer here, and all three are
// handled:
//
//  1. the Scylla timeline rows — every follower's home copy and the author's
//     own copy — are deleted through the HF4 reverse index;
//  2. the per-viewer hydration cache rows (feed:hydrate:<viewer>:<post>) are
//     dropped for every owner the index named, and a tombstone
//     (post:deleted:<post>) is written that HydratePosts consults so a
//     cached row for a viewer the index did NOT name (trending, discovery,
//     celeb pull) is discarded too;
//  3. post-service's batch endpoint drops a deleted post outright, so the
//     fresh path fails closed even if 1 and 2 were somehow both missed.
//
// PostRestored removes the tombstone and fans the post back out exactly the
// way PostCreated does (author's own timelines plus followers / circle), so
// the restored post reappears where it was.

// deletedTombstoneTTL bounds the tombstone. It only has to outlive the
// hydration cache (5 min); the batch endpoint is the durable gate. 24h
// matches the existing UploadDeleted / CrosspostRemoved markers.
const deletedTombstoneTTL = 24 * time.Hour

func (c *Consumer) handlePostDeleted(ctx context.Context, envelope events.EventEnvelope) error {
	var event events.PostDeletedPayload
	payloadBytes, _ := json.Marshal(envelope.Payload)
	if err := json.Unmarshal(payloadBytes, &event); err != nil {
		return err
	}
	postID, err := uuid.Parse(event.PostID)
	if err != nil {
		return fmt.Errorf("PostDeleted: bad post_id %q: %w", event.PostID, err)
	}

	// Tombstone first: from this moment no cached row for the post is
	// served to anyone, whatever the timeline cleanup below manages.
	if c.rdb != nil {
		if err := c.rdb.Set(ctx, service.DeletedTombstoneKey(postID), "1", deletedTombstoneTTL).Err(); err != nil {
			return fmt.Errorf("PostDeleted: tombstone %s: %w", postID, err)
		}
		c.rdb.Del(ctx, fmt.Sprintf("post:counters:%s:likes", event.PostID))
		c.rdb.Del(ctx, fmt.Sprintf("post:counters:%s:comments", event.PostID))
		c.rdb.Del(ctx, fmt.Sprintf("post:likers:%s", event.PostID))
	}

	removed, err := c.removePostFromTimelines(ctx, postID, event.AuthorID)
	if err != nil {
		return fmt.Errorf("PostDeleted: %w", err)
	}

	log.Printf("Processing PostDeleted: post=%s author=%s type=%s timeline_rows_removed=%d purge_at=%v",
		event.PostID, event.AuthorID, event.ContentType, removed, event.PurgeAt)
	return nil
}

// handlePostVisibilityChanged re-derives a post's timeline rows after its
// owner edited the visibility. post.visibility_changed was defined but never
// produced or consumed, so an edit left every row written for the OLD
// visibility in place: a post made unlisted stayed in every follower's feed,
// and per-viewer hydration caches kept serving the old value for minutes.
//
// Everything goes first — every timeline row and the cached copies of every
// owner (removePostFromTimelines) — then FanoutPost writes exactly what the
// new visibility calls for: the author's rows only for unlisted / private,
// the full audience for public / followers. Both steps are idempotent, so a
// retry is safe.
//
// Stale marker (audit H1, 2026-10-30): post-service also publishes this
// event for changes that are not an owner's visibility edit, named by
// `reason` (events.VisibilityChange*): "moderation" (a takedown),
// "copyright" (a block) and "membership_gate" (a members-tier gate), each
// with old == new == the post's current visibility. Every one of them
// means "this post must be re-checked for every viewer", and the
// per-viewer hydration cache cannot know who those viewers are (it also
// holds rows for discovery, cold-start and celeb-pull viewers the reverse
// index never names). So the first thing written is the post's stale
// marker, which makes every cached row a miss until it expires. A failure
// to write it fails the event, so it is retried.
//
// Only an audience change re-derives the timeline rows (rederiveAudience):
// reason "audience", or no reason (an older producer) with old != new. For
// any other reason the rows are already right for the audience;
// post-service's batch answer decides what each viewer sees, and the
// marker makes sure it is asked.
func (c *Consumer) handlePostVisibilityChanged(ctx context.Context, envelope events.EventEnvelope) error {
	var event events.PostVisibilityChangedPayload
	payloadBytes, _ := json.Marshal(envelope.Payload)
	if err := json.Unmarshal(payloadBytes, &event); err != nil {
		return err
	}
	postID, err := uuid.Parse(event.PostID)
	if err != nil {
		return fmt.Errorf("PostVisibilityChanged: bad post_id %q: %w", event.PostID, err)
	}
	if err := c.markStale(ctx, postID); err != nil {
		return fmt.Errorf("PostVisibilityChanged: %w", err)
	}
	if !rederiveAudience(event.Reason, event.OldVisibility, event.NewVisibility) {
		log.Printf("Processing PostVisibilityChanged: post=%s re-check only (reason=%q, visibility %q -> %q)",
			event.PostID, event.Reason, event.OldVisibility, event.NewVisibility)
		return nil
	}
	if c.rederive != nil { // tests observe the audience branch here
		return c.rederive(ctx, event)
	}
	return c.rederiveVisibility(ctx, postID, event)
}

// rederiveVisibility removes every timeline row of the post and fans it out
// again for its new visibility — the audience-change branch of
// handlePostVisibilityChanged. Both steps are idempotent.
func (c *Consumer) rederiveVisibility(ctx context.Context, postID uuid.UUID, event events.PostVisibilityChangedPayload) error {
	authorID, err := uuid.Parse(event.AuthorID)
	if err != nil {
		return fmt.Errorf("PostVisibilityChanged: bad author_id %q: %w", event.AuthorID, err)
	}

	removed, err := c.removePostFromTimelines(ctx, postID, event.AuthorID)
	if err != nil {
		return fmt.Errorf("PostVisibilityChanged: %w", err)
	}
	if c.service == nil {
		return nil
	}
	contentType := event.ContentType
	if contentType == "" {
		contentType = "post"
	}
	createdAt := event.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	channelID := uuid.Nil
	if event.ChannelID != "" {
		if parsed, err := uuid.Parse(event.ChannelID); err == nil {
			channelID = parsed
		}
	}
	if err := c.service.FanoutPost(ctx, postID, authorID, createdAt, contentType, event.NewVisibility, channelID); err != nil {
		return fmt.Errorf("PostVisibilityChanged: re-fan-out %s: %w", postID, err)
	}
	log.Printf("Processing PostVisibilityChanged: post=%s %s -> %s reason=%q timeline_rows_removed=%d",
		event.PostID, event.OldVisibility, event.NewVisibility, event.Reason, removed)
	return nil
}

// rederiveAudience reports whether a visibility_changed event calls for
// removing and re-fanning-out the post's timeline rows: only an audience
// change does. reason "audience" with a new visibility to fan out to, or
// (an older producer) no reason and a visibility that really changed.
// Moderation, copyright, membership-gate and any reason this consumer does
// not know are re-check only.
func rederiveAudience(reason, oldVis, newVis string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case events.VisibilityChangeAudience:
		return strings.TrimSpace(newVis) != ""
	case "":
		return !visibilityUnchanged(oldVis, newVis)
	default:
		return false
	}
}

// visibilityUnchanged reports whether a visibility_changed event leaves the
// post's audience where it was, so only the re-check (stale marker) is due.
// An empty new visibility is a producer that is announcing a moderation or
// gate change, not an edit: re-deriving rows from "" would fan out nothing.
func visibilityUnchanged(oldVis, newVis string) bool {
	n := strings.ToLower(strings.TrimSpace(newVis))
	return n == "" || n == strings.ToLower(strings.TrimSpace(oldVis))
}

// markStale writes the post's stale marker (service.StaleMarkerKey): every
// viewer's cached hydration row for it is a miss until the marker expires.
func (c *Consumer) markStale(ctx context.Context, postID uuid.UUID) error {
	if c.rdb == nil {
		return nil
	}
	if err := c.rdb.Set(ctx, service.StaleMarkerKey(postID), "1", service.StaleMarkerTTL).Err(); err != nil {
		return fmt.Errorf("stale marker %s: %w", postID, err)
	}
	return nil
}

// handlePostCopyrightReview applies post.copyright_review.changed. Every
// state changes who may watch the post — pending and blocked hide it,
// cleared shows it again — so each one writes the stale marker and nothing
// else: post-service's batch answer is the gate, and the marker makes
// every viewer's cached row go back and ask it. A malformed post id fails
// the event (retried, then the DLQ) rather than leaving a blocked video in
// caches.
func (c *Consumer) handlePostCopyrightReview(ctx context.Context, envelope events.EventEnvelope) error {
	var event events.PostCopyrightReviewPayload
	payloadBytes, _ := json.Marshal(envelope.Payload)
	if err := json.Unmarshal(payloadBytes, &event); err != nil {
		return err
	}
	postID, err := uuid.Parse(event.PostID)
	if err != nil {
		return fmt.Errorf("PostCopyrightReview: bad post_id %q: %w", event.PostID, err)
	}
	if markErr := c.markStale(ctx, postID); markErr != nil {
		return fmt.Errorf("PostCopyrightReview: %w", markErr)
	}
	log.Printf("Processing PostCopyrightReview: post=%s state=%s reason=%q", event.PostID, event.State, event.Reason)
	return nil
}

// removePostFromTimelines deletes every timeline row that holds the post —
// each follower's home copy and the author's own — through the HF4 reverse
// index, then drops the per-viewer hydration cache rows of every owner it
// found (and the author's). Shared by PostDeleted and UploadDeleted.
// Idempotent. Returns how many timeline rows it removed.
func (c *Consumer) removePostFromTimelines(ctx context.Context, postID uuid.UUID, authorIDStr string) (int, error) {
	if c.timelineStore == nil {
		return 0, nil
	}
	owners, err := c.timelineStore.DeletePostFromTimelines(ctx, postID)
	if err != nil {
		return 0, fmt.Errorf("remove timeline rows for %s: %w", postID, err)
	}
	if c.rdb != nil && len(owners) > 0 {
		keys := make([]string, 0, len(owners)+1)
		for _, o := range owners {
			keys = append(keys, service.HydrationCacheKey(o.OwnerID, postID))
		}
		if authorID, err := uuid.Parse(authorIDStr); err == nil {
			keys = append(keys, service.HydrationCacheKey(authorID, postID))
		}
		if err := c.rdb.Del(ctx, keys...).Err(); err != nil {
			log.Printf("hydration cache invalidation failed for %s: %v", postID, err)
		}
	}
	return len(owners), nil
}

func (c *Consumer) handlePostRestored(ctx context.Context, envelope events.EventEnvelope) error {
	var event events.PostRestoredPayload
	payloadBytes, _ := json.Marshal(envelope.Payload)
	if err := json.Unmarshal(payloadBytes, &event); err != nil {
		return err
	}
	postID, err := uuid.Parse(event.PostID)
	if err != nil {
		return fmt.Errorf("PostRestored: bad post_id %q: %w", event.PostID, err)
	}
	authorID, err := uuid.Parse(event.AuthorID)
	if err != nil {
		return fmt.Errorf("PostRestored: bad author_id %q: %w", event.AuthorID, err)
	}
	if c.rdb != nil {
		if err := c.rdb.Del(ctx, service.DeletedTombstoneKey(postID)).Err(); err != nil {
			return fmt.Errorf("PostRestored: clear tombstone %s: %w", postID, err)
		}
	}
	contentType := event.ContentType
	if contentType == "" {
		contentType = "post"
	}
	createdAt := event.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	if c.service == nil {
		return nil
	}
	// Same fan-out as creation: author's own timelines, then followers /
	// circle (or pull model for celebs). AddTo* upserts on the primary key
	// (scylla.TimelineTS), so a row that somehow survived the delete is not
	// duplicated.
	//
	// The channel id (post-service stamps it on PostRestored as on
	// PostCreated) brings a restored long video back to the channel's
	// subscribers too. Older producers leave it empty and a malformed one
	// is ignored: the subscriber leg is skipped on uuid.Nil rather than
	// guessed at, and the followers' rows still land. See FanoutPost.
	channelID := uuid.Nil
	if event.ChannelID != "" {
		if parsed, err := uuid.Parse(event.ChannelID); err == nil {
			channelID = parsed
		} else {
			log.Printf("PostRestored %s: ignoring malformed channel_id %q: %v", event.PostID, event.ChannelID, err)
		}
	}
	if err := c.service.FanoutPost(ctx, postID, authorID, createdAt, contentType, event.Visibility, channelID); err != nil {
		return fmt.Errorf("PostRestored: fan-out %s: %w", postID, err)
	}
	log.Printf("Processing PostRestored: post=%s author=%s type=%s", event.PostID, event.AuthorID, contentType)
	return nil
}
