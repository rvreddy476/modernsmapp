package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
)

// Soft-deleted posts and the hydration cache (2026-09-04).
//
// HydratePosts fronts post-service with a per-(viewer, post) Redis cache
// that lives for five minutes. post-service's batch endpoint drops a
// deleted post outright, so the FRESH path fails closed by construction —
// but a cached row does not go back to post-service, and a delete must
// vanish everywhere at once, not "within five minutes".
//
// The PostDeleted consumer therefore writes a tombstone (post:deleted:<id>)
// and HydratePosts discards every cached row whose post carries one. The
// same key the UploadDeleted / CrosspostRemoved / Q&A-removed handlers
// already wrote — they set it and nothing ever read it.
//
// Fail closed: when the tombstone lookup itself fails, the cache is not
// trusted at all and the whole page is re-fetched from post-service, whose
// answer is authoritative. A Redis blip costs one round trip, never a
// deleted post on screen.

// DeletedTombstoneKey is the Redis key marking a soft-deleted post.
func DeletedTombstoneKey(postID uuid.UUID) string {
	return fmt.Sprintf("post:deleted:%s", postID)
}

// StaleMarkerKey is the Redis key marking a post whose readability changed
// for some viewers without the post being deleted: a moderator takedown, a
// copyright block (or hold), a members-tier gate added by an edit, or a
// visibility change. A cached hydration row for a stale-marked post is a
// cache MISS for every viewer, so post-service — which knows the new
// state — is asked again. It sits next to the post:deleted tombstone and
// is read in the same MGET (filterDeletedFromCache).
func StaleMarkerKey(postID uuid.UUID) string {
	return fmt.Sprintf("post:stale:%s", postID)
}

// StaleMarkerTTL is how long a stale marker lives: twice the hydration
// cache TTL. Every row cached before the marker was written has expired by
// then, and a row cached from a batch request that was already in flight
// when the event landed (stored up to a second after the marker) has too.
// A row cached after the marker came from post-service's new answer.
const StaleMarkerTTL = 2 * hydrationCacheTTL

// HydrationCacheKey exposes the per-(viewer, post) cache key so the
// PostDeleted consumer can invalidate rows for the viewers it knows about.
func HydrationCacheKey(viewerID, postID uuid.UUID) string {
	return hydrationCacheKey(viewerID, postID)
}

// dropTombstoned returns cached without any post present in tombstoned.
// Pure; the Redis read that feeds it is filterDeletedFromCache.
func dropTombstoned(cached map[uuid.UUID]HydratedPost, tombstoned map[uuid.UUID]bool) map[uuid.UUID]HydratedPost {
	if len(cached) == 0 || len(tombstoned) == 0 {
		return cached
	}
	out := make(map[uuid.UUID]HydratedPost, len(cached))
	for id, hp := range cached {
		if tombstoned[id] {
			continue
		}
		out[id] = hp
	}
	return out
}

// filterDeletedFromCache consults the tombstone and the stale marker of
// every cached post and drops the rows that carry either. On a lookup error it returns an EMPTY map so the
// caller falls through to post-service for the whole page.
func (s *Service) filterDeletedFromCache(ctx context.Context, cached map[uuid.UUID]HydratedPost) map[uuid.UUID]HydratedPost {
	if len(cached) == 0 {
		return cached
	}
	if s.rdb == nil {
		// No Redis means no cache either (fetchHydratedCache returns empty),
		// so this branch is unreachable in practice; keep the invariant.
		return map[uuid.UUID]HydratedPost{}
	}
	// One MGET for both markers: keys[2i] is post i's tombstone and
	// keys[2i+1] its stale marker. Either one makes the cached row a miss.
	ids := make([]uuid.UUID, 0, len(cached))
	keys := make([]string, 0, 2*len(cached))
	for id := range cached {
		ids = append(ids, id)
		keys = append(keys, DeletedTombstoneKey(id), StaleMarkerKey(id))
	}
	mgetCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	vals, err := s.rdb.MGet(mgetCtx, keys...).Result()
	if err != nil || len(vals) != len(keys) {
		log.Printf("[feed-hydrate] tombstone MGET failed, discarding cached page (fail closed): %v", err)
		return map[uuid.UUID]HydratedPost{}
	}
	tombstoned := make(map[uuid.UUID]bool)
	for i, v := range vals {
		if v != nil {
			tombstoned[ids[i/2]] = true
		}
	}
	return dropTombstoned(cached, tombstoned)
}

// MarkPostStale writes the post's stale marker (StaleMarkerKey). The
// consumer calls it for every event that changes who may read a post
// without deleting it. An error is returned, not logged: the event must be
// retried, or cached rows keep serving the old answer for five minutes.
func (s *Service) MarkPostStale(ctx context.Context, postID uuid.UUID) error {
	if s.rdb == nil {
		return nil
	}
	return s.rdb.Set(ctx, StaleMarkerKey(postID), "1", StaleMarkerTTL).Err()
}
