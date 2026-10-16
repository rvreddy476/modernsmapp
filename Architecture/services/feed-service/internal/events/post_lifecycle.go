package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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
	log.Printf("Processing PostVisibilityChanged: post=%s %s -> %s timeline_rows_removed=%d",
		event.PostID, event.OldVisibility, event.NewVisibility, removed)
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
