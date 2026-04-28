package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Tier 1b — Hot-post body cache.
//
// GetPost is the hottest read path on a viral post: every share, every
// link click, every "open thread" hits it. The body itself is
// effectively immutable after creation (text, media, tier_required_id
// only change on author UpdatePost / SetPostMembershipGate /
// DeletePost), so we cache the raw *postgres.Post in Redis and let
// the per-request enrichment (counts, viewer reaction, bookmark,
// poll votes) run unchanged.
//
// Caching only the immutable body — not the assembled PostDetail —
// keeps the cache cheap (one entry per post regardless of viewer)
// and means stats/likes don't go stale.
//
// Invalidation:
//   - SetPostMembershipGate explicitly drops the key (tier_required_id
//     is in the cached payload).
//   - DeletePost drops the key.
//   - UpdatePost-style edits (currently only category + cover) drop
//     the key.
//   - TTL of 5 minutes acts as a backstop for any path that mutates
//     the post without going through these helpers.

const (
	postCacheKeyPrefix = "post:body:"
	postCacheTTL       = 5 * time.Minute
)

// getCachedPostBody returns the immutable post body for `id`,
// preferring Redis. Cache misses fall through to the DB and are
// re-cached on success. nil-Redis is supported (returns DB result
// directly) so unit tests don't need to mock anything.
func (s *Service) getCachedPostBody(ctx context.Context, id uuid.UUID) (*postgres.Post, error) {
	if s.rdb != nil {
		key := postCacheKey(id)
		if raw, err := s.rdb.Get(ctx, key).Bytes(); err == nil {
			var p postgres.Post
			if jsonErr := json.Unmarshal(raw, &p); jsonErr == nil {
				return &p, nil
			}
			// Corrupt entry: drop it so the next read repopulates.
			_ = s.rdb.Del(ctx, key).Err()
		} else if !errors.Is(err, redis.Nil) {
			// Redis transport error — log via fall-through, don't fail
			// the read. The cache is best-effort.
			_ = err
		}
	}

	p, err := s.pgStore.GetPost(ctx, id)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, nil
	}
	if s.rdb != nil {
		if data, jsonErr := json.Marshal(p); jsonErr == nil {
			_ = s.rdb.Set(ctx, postCacheKey(id), data, postCacheTTL).Err()
		}
	}
	return p, nil
}

// InvalidatePostBodyCache drops the cached body for one post. Called
// from any service method that mutates the columns we cache. nil-safe.
func (s *Service) InvalidatePostBodyCache(ctx context.Context, id uuid.UUID) {
	if s.rdb == nil {
		return
	}
	_ = s.rdb.Del(ctx, postCacheKey(id)).Err()
}

// postCacheKey returns the Redis key for one post's body cache.
// Exported only for tests via the BuildPostBodyCacheKey wrapper.
func postCacheKey(id uuid.UUID) string {
	return postCacheKeyPrefix + id.String()
}

// BuildPostBodyCacheKey is the test-facing twin of postCacheKey.
// Asserts the key namespacing in unit tests so a future rename
// doesn't silently break the SCAN-based invalidator (if any).
func BuildPostBodyCacheKey(id uuid.UUID) string {
	return postCacheKey(id)
}
