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

	removed := 0
	if c.timelineStore != nil {
		owners, err := c.timelineStore.DeletePostFromTimelines(ctx, postID)
		if err != nil {
			return fmt.Errorf("PostDeleted: remove timeline rows for %s: %w", postID, err)
		}
		removed = len(owners)
		if c.rdb != nil && len(owners) > 0 {
			keys := make([]string, 0, len(owners)+1)
			for _, o := range owners {
				keys = append(keys, service.HydrationCacheKey(o.OwnerID, postID))
			}
			if authorID, err := uuid.Parse(event.AuthorID); err == nil {
				keys = append(keys, service.HydrationCacheKey(authorID, postID))
			}
			if err := c.rdb.Del(ctx, keys...).Err(); err != nil {
				log.Printf("PostDeleted: hydration cache invalidation failed for %s: %v", postID, err)
			}
		}
	}

	log.Printf("Processing PostDeleted: post=%s author=%s type=%s timeline_rows_removed=%d purge_at=%v",
		event.PostID, event.AuthorID, event.ContentType, removed, event.PurgeAt)
	return nil
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
	// circle (or pull model for celebs). AddTo* upserts on the primary key,
	// so a row that somehow survived the delete is not duplicated.
	if err := c.service.FanoutPost(ctx, postID, authorID, createdAt, contentType, event.Visibility); err != nil {
		return fmt.Errorf("PostRestored: fan-out %s: %w", postID, err)
	}
	log.Printf("Processing PostRestored: post=%s author=%s type=%s", event.PostID, event.AuthorID, contentType)
	return nil
}
