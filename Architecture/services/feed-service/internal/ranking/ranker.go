package ranking

import (
	"context"
	"fmt"
	"time"

	"github.com/gocql/gocql"
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

// NewRanker creates a Ranker backed by Redis and an optional ScyllaDB session
// for durable interaction verification. The timeout parameter configures the
// circuit-breaker deadline for a single Rank call.
func NewRanker(rdb *redis.Client, scyllaSession *gocql.Session, timeout time.Duration) *Ranker {
	return &Ranker{
		signals: NewSignalLoader(rdb, scyllaSession),
		timeout: timeout,
	}
}

// Rank scores and reorders candidates for the given viewer. It enforces a
// hard timeout; if the deadline is exceeded at any stage the method returns
// an error so the caller can fall back to a chronological feed.
func (r *Ranker) Rank(ctx context.Context, viewerID uuid.UUID, candidates []Candidate, limit int) ([]Candidate, error) {
	return r.rank(ctx, viewerID, nil, candidates, limit)
}

// RankRelated is the up-next / related-videos ordering: the same signals,
// the same formula and the same diversity pass as Rank, plus a seed — the
// video the viewer is watching — that turns on the relatedness term in
// ScoreCandidates.
//
// It is a parameter rather than a second ranker on purpose. A separate
// related ranker would be a second place for the block, mute, feedback and
// diversity rules to be got wrong, and the first thing that would happen
// is that one of them would be forgotten there. Here the only difference
// between "your feed" and "what plays next" is one extra scoring term and
// two exclusions.
//
// Anything the viewer has already watched to completion is dropped
// outright rather than penalised: on the ordinary feeds a finished post
// still deserves a place at the back of the page, but offering someone the
// video they just finished as the thing to play next is simply wrong. The
// seed itself is dropped for the same reason, and defensively — a caller
// that forgets to exclude it must not be able to produce an endless loop
// of one video.
func (r *Ranker) RankRelated(ctx context.Context, viewerID uuid.UUID, seed *Seed, candidates []Candidate, limit int) ([]Candidate, error) {
	return r.rank(ctx, viewerID, seed, candidates, limit)
}

func (r *Ranker) rank(ctx context.Context, viewerID uuid.UUID, seed *Seed, candidates []Candidate, limit int) ([]Candidate, error) {
	// Apply circuit-breaker timeout.
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	// 1. Load signals from Redis.
	sigs, err := r.signals.LoadSignals(ctx, viewerID, candidates)
	if err != nil {
		return nil, fmt.Errorf("ranking: load signals: %w", err)
	}
	if seed != nil {
		sigs.Seed = seed
		candidates = ExcludeSeen(candidates, seed, sigs)
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

// ExcludeSeen drops the seed video itself and everything the viewer has
// already watched to the end. Pure, so the guarantee can be tested without
// a Redis.
//
// It is the last of the related surface's exclusions, not the only one:
// the viewer's own posts, blocked and muted authors, "don't recommend this
// account" authors and posts the viewer marked "not interested" are all
// removed earlier, at candidate collection and again at the hydration
// tail. This one is here because it is the only exclusion that depends on
// a ranking signal.
func ExcludeSeen(candidates []Candidate, seed *Seed, signals *ViewerSignals) []Candidate {
	if len(candidates) == 0 {
		return candidates
	}
	out := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
		if seed != nil && c.PostID == seed.PostID {
			continue
		}
		if signals != nil && signals.Completions[c.PostID.String()] {
			continue
		}
		out = append(out, c)
	}
	return out
}
