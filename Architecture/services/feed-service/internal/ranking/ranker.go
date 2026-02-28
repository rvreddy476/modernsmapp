package ranking

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Ranker orchestrates the full v2.0 ranking pipeline: signal loading,
// score computation, and diversity enforcement. It applies a circuit-breaker
// timeout so that callers can fall back to chronological ordering when ranking
// is too slow.
type Ranker struct {
	signals *SignalLoader
	timeout time.Duration // circuit breaker timeout (typically 20ms)
}

// NewRanker creates a Ranker backed by the given Redis client. The timeout
// parameter configures the circuit-breaker deadline for a single Rank call.
func NewRanker(rdb *redis.Client, timeout time.Duration) *Ranker {
	return &Ranker{
		signals: NewSignalLoader(rdb),
		timeout: timeout,
	}
}

// Rank scores and reorders candidates for the given viewer. It enforces a
// hard timeout; if the deadline is exceeded at any stage the method returns
// an error so the caller can fall back to a chronological feed.
func (r *Ranker) Rank(ctx context.Context, viewerID uuid.UUID, candidates []Candidate, limit int) ([]Candidate, error) {
	// Apply circuit-breaker timeout.
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	// 1. Load signals from Redis.
	sigs, err := r.signals.LoadSignals(ctx, viewerID, candidates)
	if err != nil {
		return nil, fmt.Errorf("ranking: load signals: %w", err)
	}

	// Check deadline after signal loading.
	if ctx.Err() != nil {
		return nil, fmt.Errorf("ranking: deadline exceeded after signal loading: %w", ctx.Err())
	}

	// 2. Score candidates.
	scored := ScoreCandidates(candidates, sigs)

	// Check deadline after scoring.
	if ctx.Err() != nil {
		return nil, fmt.Errorf("ranking: deadline exceeded after scoring: %w", ctx.Err())
	}

	// 3. Apply diversity rules and trim to limit.
	result := ApplyDiversity(scored, limit)

	// Final deadline check.
	if ctx.Err() != nil {
		return nil, fmt.Errorf("ranking: deadline exceeded after diversity pass: %w", ctx.Err())
	}

	return result, nil
}
