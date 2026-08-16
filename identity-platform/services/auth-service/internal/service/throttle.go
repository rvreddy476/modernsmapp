package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Throttle is the consumer-owned boundary for abuse limits inside the service.
//
// It exists so the resend cap can be keyed on an identity the SERVER resolved,
// not on a field the caller supplied. The HTTP middleware cannot do this: it
// runs before the verification token has been exchanged for a user id, so the
// only keys available to it are the caller's IP and whatever it chooses to put
// in the body.
//
// Allow reports whether the action is permitted and, when it is not, how long
// until the window resets so the caller can emit Retry-After.
type Throttle interface {
	Allow(ctx context.Context, key string, limit int64, window time.Duration) (allowed bool, retryAfter time.Duration, err error)
}

// ErrThrottled is returned when an abuse limit denies an action.
//
// It carries RetryAfter so the HTTP layer can answer with a header instead of
// an opaque 429 the client has to guess at.
type ErrThrottled struct {
	RetryAfter time.Duration
	Reason     string
}

func (e *ErrThrottled) Error() string {
	return fmt.Sprintf("throttled (%s), retry after %s", e.Reason, e.RetryAfter)
}

// AsThrottled extracts an *ErrThrottled from an error chain.
func AsThrottled(err error) (*ErrThrottled, bool) {
	var t *ErrThrottled
	if errors.As(err, &t) {
		return t, true
	}
	return nil, false
}

// redisThrottle is the production implementation.
//
// Fails CLOSED on Redis error, matching the HTTP rate limiters: a Redis outage
// must not silently disable an abuse control. The cost of a false denial here
// is one delayed email; the cost of a false allow is an unbounded mail flood
// against a third party.
type redisThrottle struct {
	rdb *redis.Client
}

// NewRedisThrottle builds a Throttle over rdb. A nil client yields a throttle
// that denies nothing — used only where no Redis is configured, and never in a
// deployment that faces the internet.
func NewRedisThrottle(rdb *redis.Client) Throttle {
	return &redisThrottle{rdb: rdb}
}

func (t *redisThrottle) Allow(
	ctx context.Context,
	key string,
	limit int64,
	window time.Duration,
) (bool, time.Duration, error) {
	if t.rdb == nil {
		return true, 0, nil
	}

	pipe := t.rdb.Pipeline()
	incr := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, window)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, window, err
	}

	count, err := incr.Result()
	if err != nil {
		return false, window, err
	}
	if count <= limit {
		return true, 0, nil
	}

	// Report the ACTUAL remaining window rather than the nominal one, so a
	// client denied late in a window is not told to wait the full period.
	ttl, err := t.rdb.TTL(ctx, key).Result()
	if err != nil || ttl <= 0 {
		ttl = window
	}
	return false, ttl, nil
}

// Resend limits. Deliberately tight: a verification resend sends mail to a
// third party's inbox, so the blast radius of a permissive limit is someone
// else's mailbox and this domain's sending reputation.
const (
	resendPerHour    int64 = 3
	resendPerDay     int64 = 10
	resendHourWindow       = time.Hour
	resendDayWindow        = 24 * time.Hour
)

// checkResendQuota enforces the per-ACCOUNT resend caps.
//
// Keyed on the user id the server resolved from the verification token, which
// is what makes it independent of both the caller's IP and of how many
// verification tokens exist for the account. A per-token key would be
// bypassable: signing in with an unverified account mints a FRESH token, so an
// attacker holding the password could loop login → resend and reset the bucket
// each time.
func (s *Service) checkResendQuota(ctx context.Context, userID uuid.UUID) error {
	if s.throttle == nil {
		return nil
	}

	windows := []struct {
		key    string
		limit  int64
		window time.Duration
		reason string
	}{
		{fmt.Sprintf("resend_rl:hour:%s", userID), resendPerHour, resendHourWindow, "hourly"},
		{fmt.Sprintf("resend_rl:day:%s", userID), resendPerDay, resendDayWindow, "daily"},
	}

	for _, w := range windows {
		allowed, retryAfter, err := s.throttle.Allow(ctx, w.key, w.limit, w.window)
		if err != nil {
			// Fail closed. See redisThrottle.
			s.log.Warn("resend throttle unavailable — denying",
				"event", "auth_resend_denied",
				"reason", "throttle_error",
				"user_id", userID,
				"err", err)
			return &ErrThrottled{RetryAfter: w.window, Reason: "throttle_unavailable"}
		}
		if !allowed {
			// Structured, stable event name so a CloudWatch metric filter can
			// derive auth_resend_denied_total{reason} until real OTel metrics
			// land (audit B6). user_id is a LOG field only — it must never
			// become a metric dimension.
			s.log.Warn("verification resend denied by quota",
				"event", "auth_resend_denied",
				"reason", w.reason,
				"user_id", userID,
				"retry_after_seconds", int(retryAfter.Seconds()))
			return &ErrThrottled{RetryAfter: retryAfter, Reason: w.reason}
		}
	}
	return nil
}
