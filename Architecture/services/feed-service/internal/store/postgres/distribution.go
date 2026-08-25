package postgres

import (
	"context"

	"github.com/google/uuid"
)

// Module 1 P0-1 (feed side): normalized per-post main-feed distribution
// state, and P0-4: the viewer's long-video frequency preference.

// UpsertDistribution records a post's main-feed eligibility. The write is
// rev-guarded: an incoming (replayed / out-of-order) event with a rev that
// is not newer than the stored one is a no-op, which makes event replay
// idempotent and protects against A→B→A flapping from reordered delivery.
func (s *MetaStore) UpsertDistribution(ctx context.Context, postID uuid.UUID, mainFeed bool, rev int64) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO feed_distribution (post_id, main_feed, rev, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (post_id) DO UPDATE
		SET main_feed = EXCLUDED.main_feed, rev = EXCLUDED.rev, updated_at = NOW()
		WHERE feed_distribution.rev < EXCLUDED.rev`,
		postID, mainFeed, rev)
	return err
}

// ExcludedFromMainFeed returns the subset of postIDs whose distribution
// policy excludes them from the social home surface. Posts without a row
// (legacy posts) are never excluded.
func (s *MetaStore) ExcludedFromMainFeed(ctx context.Context, postIDs []uuid.UUID) (map[uuid.UUID]struct{}, error) {
	out := make(map[uuid.UUID]struct{})
	if len(postIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT post_id FROM feed_distribution
		WHERE post_id = ANY($1) AND main_feed = FALSE`, postIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}

// PolicyProvenance is the tri-state, ROW-LEVEL answer to "does this post
// carry a distribution policy?" (fixes-v2 / Codex P1-2). It replaces the
// deployment-timestamp heuristic as the primary signal.
type PolicyProvenance int

const (
	// ProvenanceUnknown — we could not determine it (lookup failed).
	ProvenanceUnknown PolicyProvenance = iota
	// ProvenanceNoPolicy — a legacy post: no policy row was ever written.
	ProvenanceNoPolicy
	// ProvenanceEligible — an explicit policy that allows social home.
	ProvenanceEligible
	// ProvenanceExcluded — an explicit policy that opts out.
	ProvenanceExcluded
)

// MainFeedProvenance returns the row-level provenance for each post.
// Posts with no row are ProvenanceNoPolicy — they predate policies (or
// their producer sent none), so they cannot be a creator opt-out.
func (s *MetaStore) MainFeedProvenance(ctx context.Context, postIDs []uuid.UUID) (map[uuid.UUID]PolicyProvenance, error) {
	out := make(map[uuid.UUID]PolicyProvenance, len(postIDs))
	if len(postIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT post_id, main_feed FROM feed_distribution
		WHERE post_id = ANY($1)`, postIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id       uuid.UUID
			mainFeed bool
		)
		if err := rows.Scan(&id, &mainFeed); err != nil {
			return nil, err
		}
		if mainFeed {
			out[id] = ProvenanceEligible
		} else {
			out[id] = ProvenanceExcluded
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Anything not returned has no policy row at all.
	for _, id := range postIDs {
		if _, ok := out[id]; !ok {
			out[id] = ProvenanceNoPolicy
		}
	}
	return out, nil
}

// GetLongVideoFrequency returns the viewer's long-video frequency
// preference. Missing row / any error path defaults to "balanced".
func (s *MetaStore) GetLongVideoFrequency(ctx context.Context, userID uuid.UUID) (string, error) {
	var freq string
	err := s.db.QueryRow(ctx,
		`SELECT long_video_frequency FROM user_preferences WHERE user_id = $1`, userID).Scan(&freq)
	if err != nil {
		return "balanced", err
	}
	if freq == "" {
		freq = "balanced"
	}
	return freq, nil
}

// SetLongVideoFrequency persists the preference. Value validation happens
// at the HTTP layer; the DB CHECK constraint is the backstop.
func (s *MetaStore) SetLongVideoFrequency(ctx context.Context, userID uuid.UUID, freq string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO user_preferences (user_id, long_video_frequency, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (user_id) DO UPDATE
		SET long_video_frequency = EXCLUDED.long_video_frequency, updated_at = NOW()`,
		userID, freq)
	return err
}
