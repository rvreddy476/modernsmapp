package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Product-tag counter dedup. Anti-fraud for the impression and click
// endpoints, which are public (the player calls them unauthenticated
// from any viewer).
//
// Without dedup, a 30-second video lets one viewer fire ~10 impressions
// (the overlay re-mounts on every replay) and a refresh fires the
// click counter at will. Bots can hit /click in a loop and pump
// numbers a creator might pitch to a brand sponsor.
//
// Strategy: per-(tag, IP) Redis SETNX with a 1-hour window. The first
// SETNX wins → counter increments. Subsequent ones in the window are
// silently skipped (we still return 204 to the player; the counter
// just doesn't move).
//
// IP is hashed (not stored raw) so a long-lived dedup record doesn't
// hold a PII trail. Hash key is the api-gateway's INTERNAL_SERVICE_KEY
// — also keeps the bucket per-environment.

const (
	// Bucket length per (tag, IP). 1h matches the typical attention
	// span on a single video without leaving the door open for a 24h
	// floodbot to credit one IP for thousands of impressions.
	productTagImpressionDedupTTL = 1 * time.Hour
	// Clicks are rarer than impressions; the dedup window can be
	// shorter (15 min) so a viewer who genuinely clicks twice in a
	// session is counted once but a viewer who comes back next day
	// is fresh.
	productTagClickDedupTTL = 15 * time.Minute
)

// AcceptProductTagImpression returns true when the (tagID, ipHash)
// pair has NOT been seen inside the impression window — the caller
// then increments the counter. Returns false (skip) on repeat.
//
// Fail-open: if Redis is down, every event is accepted (worse to
// silently drop legitimate impressions than to over-count during a
// transient blip). The gateway's H5 fleet rate limit is the upstream
// flood protection.
func (s *Service) AcceptProductTagImpression(
	ctx context.Context,
	tagID uuid.UUID,
	ipHash string,
) bool {
	return s.acceptProductTagEvent(ctx, "imp", tagID, ipHash, productTagImpressionDedupTTL)
}

// AcceptProductTagClick — sibling to AcceptProductTagImpression.
func (s *Service) AcceptProductTagClick(
	ctx context.Context,
	tagID uuid.UUID,
	ipHash string,
) bool {
	return s.acceptProductTagEvent(ctx, "click", tagID, ipHash, productTagClickDedupTTL)
}

func (s *Service) acceptProductTagEvent(
	ctx context.Context,
	kind string,
	tagID uuid.UUID,
	ipHash string,
	ttl time.Duration,
) bool {
	if s.rdb == nil || ipHash == "" {
		// No Redis or no IP → can't dedup. Better to accept than to
		// drop; gateway rate-limit is the upstream guard.
		return true
	}
	key := fmt.Sprintf("ptg_dedup:%s:%s:%s", kind, tagID, ipHash)
	ok, err := s.rdb.SetNX(ctx, key, "1", ttl).Result()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			slog.Debug("product tag dedup setnx error",
				"kind", kind, "tag_id", tagID, "err", err)
		}
		// Fail-open on Redis error.
		return true
	}
	return ok
}
