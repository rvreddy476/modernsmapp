package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Per-user posting limits (audit M4). A fixed window per (action, user) in
// Redis: the first hit in a window creates the counter with the window's
// TTL, every hit increments it, and a hit past the limit is refused with
// "rate_limited: ..." (429). The refused hit still counts, so hammering a
// closed window does not reopen it early.
//
// Fail open: Redis down (it is health-checked and required at boot) lets the
// write through with a warning rather than taking posting down with it. The
// per-question answer cap and the answer-request uniqueness still hold.

// RateLimiter counts one hit against key and reports whether it is within
// limit for the window.
type RateLimiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error)
}

type redisLimiter struct{ rdb *redis.Client }

func (l redisLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	var incr *redis.IntCmd
	_, err := l.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.SetNX(ctx, key, 0, window)
		incr = p.Incr(ctx, key)
		return nil
	})
	if err != nil {
		return false, err
	}
	return incr.Val() <= int64(limit), nil
}

// WithRateLimiter replaces the limiter (tests); nil disables limiting.
func (s *Service) WithRateLimiter(l RateLimiter) *Service {
	s.limiter = l
	return s
}

// rateRule is one window of one action's limit.
type rateRule struct {
	action string
	limit  int
	window time.Duration
}

var (
	questionRates = []rateRule{{"question", 10, time.Hour}, {"question", 30, 24 * time.Hour}}
	answerRates   = []rateRule{{"answer", 30, time.Hour}, {"answer", 150, 24 * time.Hour}}
	commentRates  = []rateRule{{"comment", 60, time.Hour}, {"comment", 300, 24 * time.Hour}}
	// answerRequestRates is the per-requester daily cap on asking people to
	// answer (each request is a push to its target).
	answerRequestRates = []rateRule{{"answer_request", 20, 24 * time.Hour}}
	reportRates        = []rateRule{{"report", 30, 24 * time.Hour}}
)

// checkRate counts one hit of userID against every rule and refuses the
// first that is exceeded.
func (s *Service) checkRate(ctx context.Context, userID uuid.UUID, rules []rateRule) error {
	if s.limiter == nil {
		return nil
	}
	for _, r := range rules {
		key := fmt.Sprintf("qa:rl:%s:%d:%s", r.action, int64(r.window/time.Second), userID)
		ok, err := s.limiter.Allow(ctx, key, r.limit, r.window)
		if err != nil {
			slog.WarnContext(ctx, "qa: rate limiter unavailable; allowing", "action", r.action, "error", err)
			continue
		}
		if !ok {
			return fmt.Errorf("rate_limited: too many %s posts; try again later", r.action)
		}
	}
	return nil
}
