package push

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestRetryAfterPreservedWithoutLosingErrorIdentity(t *testing.T) {
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	base := errors.New("provider unavailable")
	for _, tc := range []struct {
		header string
		want   time.Duration
	}{
		{"120", 2 * time.Minute}, {now.Add(2 * time.Hour).Format(http.TimeFormat), 2 * time.Hour},
		{"-1", 0}, {"", 0}, {"not a date", 0}, {"9223372036854775807", 7 * 24 * time.Hour},
		{now.Add(-time.Hour).Format(http.TimeFormat), 0},
	} {
		err := fmt.Errorf("wrapped: %w", withRetryAfter(base, tc.header, now))
		if !errors.Is(err, base) || RetryDelay(err) != tc.want {
			t.Fatalf("header %q: %v", tc.header, RetryDelay(err))
		}
	}
}

func TestFCMQuotaWithoutRetryAfterHasMinimumDelay(t *testing.T) {
	pusher, _ := fixtureFCM(t, http.StatusTooManyRequests, `{"error":{"status":"RESOURCE_EXHAUSTED"}}`)
	err := pusher.Send(context.Background(), "synthetic-token", "android", "test", "test", nil)
	if err == nil || RetryDelay(err) < time.Minute {
		t.Fatalf("quota retry minimum lost: %v %v", err, RetryDelay(err))
	}
}
