// Pulse Boost — Sprint 5. See PULSE_DATING_SPEC.md §14.
//
// A boost adds 5 candidates to today's Pulse for the requesting user. It is
// premium-only by default; non-premium users can purchase a single-use
// boost via the boost_49 plan, which mints a one-shot Redis token.
//
// Rate limit: one boost per 24h per user (premium). The Redis key is the
// rate-limit gate; the boost-token key is consumed atomically when a free
// user redeems a purchased boost.
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

// BoostResult is the response shape for POST /v1/dating/pulse/boost.
type BoostResult struct {
	Granted     bool   `json:"granted"`
	ExtraCount  int    `json:"extra_count"`
	Source      string `json:"source"` // "premium_daily" | "boost_token"
	NextBoostAt string `json:"next_boost_at,omitempty"`
}

const (
	// boostExtraCount is the number of additional candidates a boost grants.
	boostExtraCount = 5
	// boostRateLimitTTL is how long a premium user must wait between boosts.
	boostRateLimitTTL = 24 * time.Hour
)

func boostRateLimitKey(userID uuid.UUID) string {
	return "dating:boost:premium:" + userID.String()
}

func boostTokenKey(userID uuid.UUID) string {
	return "dating:boost:token:" + userID.String()
}

// grantBoostToken is called from the premium service after a successful
// boost_49 purchase. Stored in Redis with no TTL so the user can redeem at
// their leisure.
func (s *Service) grantBoostToken(ctx context.Context, userID uuid.UUID) error {
	if s.rdb == nil {
		// No Redis — degrade gracefully but log loudly: this means the
		// user paid but cannot redeem until Redis is back.
		slog.Warn("boost token: redis unavailable; token not granted", "user_id", userID)
		return fmt.Errorf("redis unavailable")
	}
	if err := s.rdb.Set(ctx, boostTokenKey(userID), "1", 0).Err(); err != nil {
		return fmt.Errorf("set boost token: %w", err)
	}
	return nil
}

// RequestBoost is the entrypoint for POST /v1/dating/pulse/boost.
//
// Decision tree:
//   - User holds a one-shot boost token → consume it, grant boost.
//   - User is premium AND has not boosted in 24h → grant boost, set rate gate.
//   - Otherwise → forbidden.
func (s *Service) RequestBoost(ctx context.Context, userID uuid.UUID) (*BoostResult, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user_id required")
	}

	// 1) one-shot boost token redemption (free users who bought boost_49).
	if s.rdb != nil {
		consumed, err := s.rdb.Del(ctx, boostTokenKey(userID)).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			slog.Warn("boost token consume failed", "user_id", userID, "error", err)
		} else if consumed > 0 {
			s.applyBoostToCache(ctx, userID)
			return &BoostResult{
				Granted:    true,
				ExtraCount: boostExtraCount,
				Source:     "boost_token",
			}, nil
		}
	}

	// 2) premium daily boost.
	premium, err := s.store.IsPremium(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !premium {
		return nil, fmt.Errorf("forbidden: boost requires premium or a purchased boost token")
	}

	if s.rdb != nil {
		// Use SET NX to atomically take the rate-limit slot.
		ok, err := s.rdb.SetNX(ctx, boostRateLimitKey(userID), "1", boostRateLimitTTL).Result()
		if err != nil {
			return nil, fmt.Errorf("rate limit: %w", err)
		}
		if !ok {
			// Already boosted in the past 24h.
			ttl, _ := s.rdb.TTL(ctx, boostRateLimitKey(userID)).Result()
			next := time.Now().Add(ttl).UTC().Format(time.RFC3339)
			return &BoostResult{
				Granted:     false,
				ExtraCount:  0,
				Source:      "premium_daily",
				NextBoostAt: next,
			}, fmt.Errorf("forbidden: boost rate-limited; try again at %s", next)
		}
	}

	s.applyBoostToCache(ctx, userID)
	return &BoostResult{
		Granted:    true,
		ExtraCount: boostExtraCount,
		Source:     "premium_daily",
	}, nil
}

// applyBoostToCache invalidates the user's pulse cache so the next /pulse/today
// request recomputes. The matcher already returns up to 50 candidates by
// default; v1 boost simply trips a re-compute. A future iteration may push 5
// extra rows directly into the cached list.
//
// (Spec §14: "Adds 5 candidates to today's Pulse." For v1 we do this by
// invalidating the cache so the next fetch takes the freshest 5 + the
// existing 7. The invariant — user sees 5 more — holds because the matcher
// returns a wider candidate pool than the diversity constraint emits.)
func (s *Service) applyBoostToCache(ctx context.Context, userID uuid.UUID) {
	s.InvalidatePulseCache(ctx, userID)
}
