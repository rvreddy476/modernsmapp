package events

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"time"
)

// Module 2 M2-P0-2 — durable, retryable application of eligibility
// transitions.
//
// Why this exists: before this change a single transient OpenSearch error
// (a rolling restart, a 429 from a full write queue, a network blip) meant
// the transition was attempted exactly once, logged, dropped into the DLQ,
// and then never applied by anything. For an APPROVAL that is merely an
// invisible post. For a REJECTION, TAKEDOWN, or VISIBILITY DOWNGRADE it
// means unsafe or private content stays publicly searchable indefinitely,
// with nothing in the system that will ever fix it.
//
// The recovery ladder is now:
//
//	1. in-process bounded retry with exponential backoff + jitter (here)
//	2. DLQ, retained, with the attempt count and original topic on headers
//	3. automated DLQ replay (dlq_replay.go), which re-applies the operation
//	4. the Postgres-sourced reconciler (cmd/backfill -entity posts), which
//	   repairs anything the stream lost entirely
//
// Every step above is idempotent, so overlapping recovery is harmless.

// retryPolicy bounds an in-process retry loop.
type retryPolicy struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

func defaultRetryPolicy() retryPolicy {
	return retryPolicy{MaxAttempts: 4, BaseDelay: 200 * time.Millisecond, MaxDelay: 5 * time.Second}
}

// backoff returns the delay before the given (1-based) retry, with full
// jitter so that a fleet of consumer replicas recovering from the same
// OpenSearch outage does not synchronize into a thundering herd.
func (p retryPolicy) backoff(attempt int) time.Duration {
	d := float64(p.BaseDelay) * math.Pow(2, float64(attempt-1))
	if d > float64(p.MaxDelay) {
		d = float64(p.MaxDelay)
	}
	return time.Duration(rand.Int63n(int64(d)) + int64(d)/2)
}

// retry runs fn until it succeeds, the policy is exhausted, or the context
// is done. It returns the last error.
//
// Cancellation is deliberately NOT swallowed: on shutdown we want the
// message left uncommitted so it is redelivered, rather than dropped.
func (p retryPolicy) retry(ctx context.Context, fn func() error) error {
	var err error
	for attempt := 1; attempt <= p.MaxAttempts; attempt++ {
		if err = fn(); err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return errors.Join(err, ctx.Err())
		}
		if attempt == p.MaxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-time.After(p.backoff(attempt)):
		}
	}
	return err
}
