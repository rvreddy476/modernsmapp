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
				// A moderation/deletion transaction may commit while Redis is
				// unavailable, leaving an approved body in cache. Revalidate the
				// minimal revocable state against the canonical database before
				// returning any cache hit. Failure is fail-closed, never stale-read.
				state, stateErr := s.pgStore.GetPostAccessState(ctx, id)
				if stateErr != nil {
					return nil, stateErr
				}
				if state == nil || state.Deleted {
					_ = s.rdb.Del(ctx, key).Err()
					return nil, nil
				}
				applyPostAccessState(&p, state)
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

// applyPostAccessState overwrites the revocable fields of a cached body with
// the canonical row's. Every field the read gates consult for "may this be
// seen at all" must be here: review status, visibility, the 18+ flag and
// the schedule (hiddenWhileScheduled reads PublishAt; IsScheduled is the
// wire-only mirror deriveScheduled sets at scan time), and the active
// restriction count that turns the effective status "restricted"
// (migration 056; the cached JSON never carries it).
func applyPostAccessState(p *postgres.Post, state *postgres.PostAccessState) {
	p.ReviewStatus = state.ReviewStatus
	p.ActiveRestrictionCount = state.ActiveRestrictionCount
	p.Visibility = state.Visibility
	p.AgeRestricted = state.AgeRestricted
	p.PublishAt = state.PublishAt
	p.IsScheduled = state.PublishAt != nil
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
