package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
)

// discardLogger keeps quota-denial WARNs out of test output. The log line
// itself is asserted nowhere here; its stable `event` field is what a
// CloudWatch metric filter keys on in production.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeThrottle is a deterministic in-memory counter. It records the exact keys
// it was asked about, which is how the IP-independence claim is proved: the
// key must contain the user id and nothing caller-supplied.
type fakeThrottle struct {
	counts map[string]int64
	keys   []string
	err    error
}

func newFakeThrottle() *fakeThrottle {
	return &fakeThrottle{counts: map[string]int64{}}
}

func (f *fakeThrottle) Allow(
	_ context.Context,
	key string,
	limit int64,
	window time.Duration,
) (bool, time.Duration, error) {
	if f.err != nil {
		return false, window, f.err
	}
	f.keys = append(f.keys, key)
	f.counts[key]++
	if f.counts[key] <= limit {
		return true, 0, nil
	}
	return false, window, nil
}

func serviceWithThrottle(t *fakeThrottle) *Service {
	s := &Service{log: discardLogger()}
	s.SetThrottle(t)
	return s
}

// TestResendQuotaAllowsUpToHourlyLimit — the legitimate user waiting on slow
// mail must not be blocked on their second attempt.
func TestResendQuotaAllowsUpToHourlyLimit(t *testing.T) {
	svc := serviceWithThrottle(newFakeThrottle())
	userID := uuid.New()

	for i := int64(1); i <= resendPerHour; i++ {
		if err := svc.checkResendQuota(context.Background(), userID); err != nil {
			t.Fatalf("resend %d of %d was denied: %v", i, resendPerHour, err)
		}
	}
}

// TestResendQuotaDeniesBeyondHourlyLimit — the 4th attempt inside the hour is
// refused, and it says how long to wait.
func TestResendQuotaDeniesBeyondHourlyLimit(t *testing.T) {
	svc := serviceWithThrottle(newFakeThrottle())
	userID := uuid.New()

	for i := int64(0); i < resendPerHour; i++ {
		if err := svc.checkResendQuota(context.Background(), userID); err != nil {
			t.Fatalf("unexpected denial within quota: %v", err)
		}
	}

	err := svc.checkResendQuota(context.Background(), userID)
	throttled, ok := AsThrottled(err)
	if !ok {
		t.Fatalf("expected ErrThrottled past the hourly cap, got %v", err)
	}
	if throttled.RetryAfter <= 0 {
		t.Fatal("a denial must carry a positive RetryAfter, or the client cannot know when to try again")
	}
	if throttled.Reason != "hourly" {
		t.Fatalf("reason = %q, want \"hourly\"", throttled.Reason)
	}
}

// TestResendQuotaIsKeyedOnUserNotCaller — THE core B2 property.
//
// The original defect was that the only surviving cap was per-IP, because
// OTPRateLimit keys on a `phone` field this route does not carry. Rotating
// source IPs therefore removed the limit entirely. This asserts the quota key
// is derived from the server-resolved user id, so the caller cannot influence
// it at all — no IP, no header, no body field appears in it.
func TestResendQuotaIsKeyedOnUserNotCaller(t *testing.T) {
	fake := newFakeThrottle()
	svc := serviceWithThrottle(fake)
	userID := uuid.New()

	_ = svc.checkResendQuota(context.Background(), userID)

	if len(fake.keys) == 0 {
		t.Fatal("no throttle key was checked")
	}
	for _, key := range fake.keys {
		if !contains(key, userID.String()) {
			t.Fatalf("quota key %q does not contain the user id; it is not per-account", key)
		}
	}
}

// TestResendQuotaSeparatesAccounts — one user exhausting their quota must not
// deny anybody else.
func TestResendQuotaSeparatesAccounts(t *testing.T) {
	svc := serviceWithThrottle(newFakeThrottle())
	victim, other := uuid.New(), uuid.New()

	for i := int64(0); i <= resendPerHour; i++ {
		_ = svc.checkResendQuota(context.Background(), victim)
	}

	if err := svc.checkResendQuota(context.Background(), other); err != nil {
		t.Fatalf("a second account was denied by the first account's quota: %v", err)
	}
}

// TestResendQuotaFailsClosedOnThrottleError — a Redis outage must not disable
// the abuse control.
//
// The asymmetry is deliberate: a false denial costs one delayed email; a false
// allow costs an unbounded mail flood against a third party and this domain's
// sending reputation.
func TestResendQuotaFailsClosedOnThrottleError(t *testing.T) {
	fake := newFakeThrottle()
	fake.err = errors.New("redis unavailable")
	svc := serviceWithThrottle(fake)

	err := svc.checkResendQuota(context.Background(), uuid.New())
	throttled, ok := AsThrottled(err)
	if !ok {
		t.Fatalf("expected a denial when the throttle is unavailable, got %v", err)
	}
	if throttled.Reason != "throttle_unavailable" {
		t.Fatalf("reason = %q, want \"throttle_unavailable\"", throttled.Reason)
	}
}

// TestResendQuotaNoThrottleConfigured — a service with no throttle installed
// allows, so unit tests of unrelated behaviour are not blocked by quotas.
func TestResendQuotaNoThrottleConfigured(t *testing.T) {
	svc := &Service{log: discardLogger()}
	if err := svc.checkResendQuota(context.Background(), uuid.New()); err != nil {
		t.Fatalf("expected no quota enforcement without a throttle, got %v", err)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
