package push

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type retryError struct {
	err   error
	delay time.Duration
}

func (e *retryError) Error() string { return e.err.Error() }
func (e *retryError) Unwrap() error { return e.err }

// RetryDelay preserves an upstream Retry-After without persisting provider
// bodies, credentials or tokens. The worker combines it with local backoff.
func RetryDelay(err error) time.Duration {
	var e *retryError
	if errors.As(err, &e) {
		return e.delay
	}
	return 0
}

func withRetryAfter(err error, value string, now time.Time) error {
	value = strings.TrimSpace(value)
	var delay time.Duration
	if seconds, e := strconv.ParseInt(value, 10, 64); e == nil {
		// Bound conversion, including overflow, to the longest sensible retry
		// horizon. A malicious/invalid header must not wrap into an early retry.
		if seconds > 7*24*60*60 {
			seconds = 7 * 24 * 60 * 60
		}
		if seconds > 0 {
			delay = time.Duration(seconds) * time.Second
		}
	} else if when, e := http.ParseTime(value); e == nil && when.After(now) {
		delay = min(when.Sub(now), 7*24*time.Hour)
	}
	if delay <= 0 {
		return err
	}
	return &retryError{err: err, delay: delay}
}
