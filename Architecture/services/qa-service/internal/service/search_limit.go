package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Anonymous search limit (audit M6). /search and /questions/similar are open
// to signed-out callers; each call is a database query, so a signed-out
// client address gets anonymousSearchLimit calls per window (the gateway's
// global per-IP limit still applies on top). Signed-in callers are bounded
// by the gateway's per-user limit. Same limiter and fail-open policy as the
// posting limits (ratelimit.go).
const (
	anonymousSearchLimit  = 30
	anonymousSearchWindow = time.Minute
)

// AllowAnonymousSearch counts one signed-out search from clientKey (the
// client address) and refuses it with "rate_limited: ..." past the limit.
func (s *Service) AllowAnonymousSearch(ctx context.Context, clientKey string) error {
	if s.limiter == nil {
		return nil
	}
	if clientKey == "" {
		clientKey = "unknown" // no address: one shared bucket, never unlimited
	}
	key := fmt.Sprintf("qa:rl:anon_search:%d:%s", int64(anonymousSearchWindow/time.Second), clientKey)
	ok, err := s.limiter.Allow(ctx, key, anonymousSearchLimit, anonymousSearchWindow)
	if err != nil {
		slog.WarnContext(ctx, "qa: search rate limiter unavailable; allowing", "error", err)
		return nil
	}
	if !ok {
		return fmt.Errorf("rate_limited: too many searches; sign in or try again later")
	}
	return nil
}
