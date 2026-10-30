package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
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
	// postCacheRedisTimeout bounds every Redis call this cache makes
	// (2026-10-30 audit B10). The cache is best-effort: an unreachable Redis
	// that drops packets must cost a read this long, then Postgres — not the
	// client's dial / read timeouts times its retries.
	postCacheRedisTimeout = 100 * time.Millisecond
)

// postCacheCtx is ctx bounded by postCacheRedisTimeout.
func postCacheCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, postCacheRedisTimeout)
}

// postCacheRedis is the client the body cache talks to: the service's Redis
// (same address, auth and TLS), in its own small pool with every wait bounded
// by postCacheRedisTimeout and no retries. The shared client keeps go-redis'
// defaults (5 s dial, 3 s read, 3 retries, a 30 s pool wait) and ignores
// context deadlines, which suits the engagement paths but would let a
// best-effort cache hold every direct open for seconds. nil without Redis.
func (s *Service) postCacheRedis() *redis.Client {
	if s.rdb == nil {
		return nil
	}
	s.postCacheOnce.Do(func() {
		opt := *s.rdb.Options()
		opt.ContextTimeoutEnabled = true
		opt.MaxRetries = -1 // -1 disables retries (0 means the default 3)
		opt.DialTimeout = postCacheRedisTimeout
		opt.ReadTimeout = postCacheRedisTimeout
		opt.WriteTimeout = postCacheRedisTimeout
		opt.PoolTimeout = postCacheRedisTimeout
		opt.PoolSize = 0 // the default for the host, not the shared pool's
		s.postCacheRDB = redis.NewClient(&opt)
	})
	return s.postCacheRDB
}

// getCachedPostBody returns the immutable post body for `id`,
// preferring Redis. Cache misses fall through to the DB and are
// re-cached on success. nil-Redis is supported (returns DB result
// directly) so unit tests don't need to mock anything.
func (s *Service) getCachedPostBody(ctx context.Context, id uuid.UUID) (*postgres.Post, error) {
	store := s.listReads()
	if store == nil {
		return nil, ErrAuthoringStoreUnavailable
	}
	if rdb := s.postCacheRedis(); rdb != nil {
		key := postCacheKey(id)
		rctx, cancel := postCacheCtx(ctx)
		raw, err := rdb.Get(rctx, key).Bytes()
		cancel()
		if err == nil {
			var p postgres.Post
			if jsonErr := json.Unmarshal(raw, &p); jsonErr == nil {
				// A moderation/deletion transaction may commit while Redis is
				// unavailable, leaving an approved body in cache. Revalidate the
				// minimal revocable state against the canonical database before
				// returning any cache hit. Failure is fail-closed, never stale-read.
				state, stateErr := store.GetPostAccessState(ctx, id)
				if stateErr != nil {
					return nil, stateErr
				}
				if state == nil || state.Deleted {
					s.dropPostCacheKey(ctx, key)
					return nil, nil
				}
				applyPostAccessState(&p, state)
				return &p, nil
			}
			// Corrupt entry: drop it so the next read repopulates.
			s.dropPostCacheKey(ctx, key)
		} else if !errors.Is(err, redis.Nil) {
			// Redis transport error or timeout: fall through to Postgres,
			// don't fail the read. The cache is best-effort.
			slog.DebugContext(ctx, "post body cache read failed; reading Postgres", "post_id", id, "err", err)
		}
	}

	p, err := store.GetPost(ctx, id)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, nil
	}
	if rdb := s.postCacheRedis(); rdb != nil {
		if data, jsonErr := json.Marshal(p); jsonErr == nil {
			wctx, cancel := postCacheCtx(ctx)
			_ = rdb.Set(wctx, postCacheKey(id), data, postCacheTTL).Err()
			cancel()
		}
	}
	return p, nil
}

// dropPostCacheKey deletes one cache key, bounded like every cache call.
func (s *Service) dropPostCacheKey(ctx context.Context, key string) {
	rdb := s.postCacheRedis()
	if rdb == nil {
		return
	}
	dctx, cancel := postCacheCtx(ctx)
	defer cancel()
	_ = rdb.Del(dctx, key).Err()
}

// applyPostAccessState overwrites the revocable fields of a cached body with
// the canonical row's. Every field the read gates consult for "may this be
// seen at all" must be here: review status, visibility, the 18+ flag and
// the schedule (hiddenWhileScheduled reads PublishAt; IsScheduled is the
// wire-only mirror deriveScheduled sets at scan time), and the active
// restriction count that turns the effective status "restricted"
// (migration 056; the cached JSON never carries it), and the members-only
// gate the redaction reads (tier_required_id, 2026-10-30 audit B4).
func applyPostAccessState(p *postgres.Post, state *postgres.PostAccessState) {
	p.ReviewStatus = state.ReviewStatus
	p.ActiveRestrictionCount = state.ActiveRestrictionCount
	p.Visibility = state.Visibility
	p.AgeRestricted = state.AgeRestricted
	p.PublishAt = state.PublishAt
	p.IsScheduled = state.PublishAt != nil
	p.TierRequiredID = state.TierRequiredID
}

// InvalidatePostBodyCache drops the cached body for one post. Called
// from any service method that mutates the columns we cache. nil-safe.
func (s *Service) InvalidatePostBodyCache(ctx context.Context, id uuid.UUID) {
	if s.rdb == nil {
		return
	}
	s.dropPostCacheKey(ctx, postCacheKey(id))
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
