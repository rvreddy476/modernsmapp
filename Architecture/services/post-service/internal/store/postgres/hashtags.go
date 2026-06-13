package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ReelHashtag represents a hashtag extracted from a reel caption.
type ReelHashtag struct {
	ReelID    uuid.UUID `json:"reel_id"`
	Hashtag   string    `json:"hashtag"`
	Position  int       `json:"position"`
	CreatedAt time.Time `json:"created_at"`
}

// UpsertReelHashtags inserts hashtags for a reel within a transaction.
func UpsertReelHashtagsTx(ctx context.Context, tx pgx.Tx, reelID uuid.UUID, hashtags []string) error {
	// Delete existing hashtags first
	if _, err := tx.Exec(ctx, `DELETE FROM reel_hashtags WHERE reel_id = $1`, reelID); err != nil {
		return err
	}

	for i, tag := range hashtags {
		if _, err := tx.Exec(ctx, `
			INSERT INTO reel_hashtags (reel_id, hashtag, position, created_at)
			VALUES ($1, $2, $3, NOW())
			ON CONFLICT (reel_id, hashtag) DO UPDATE SET position = EXCLUDED.position
		`, reelID, tag, i); err != nil {
			return err
		}
	}
	return nil
}

// UpsertReelHashtags inserts/updates hashtags for a reel (non-transactional).
func (s *Store) UpsertReelHashtags(ctx context.Context, reelID uuid.UUID, hashtags []string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := UpsertReelHashtagsTx(ctx, tx, reelID, hashtags); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// GetReelHashtags returns all hashtags for a reel.
func (s *Store) GetReelHashtags(ctx context.Context, reelID uuid.UUID) ([]ReelHashtag, error) {
	rows, err := s.db.Query(ctx, `
		SELECT reel_id, hashtag, position, created_at
		FROM reel_hashtags WHERE reel_id = $1
		ORDER BY position
	`, reelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []ReelHashtag
	for rows.Next() {
		var t ReelHashtag
		if err := rows.Scan(&t.ReelID, &t.Hashtag, &t.Position, &t.CreatedAt); err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, nil
}

// GetTrendingHashtags returns the most frequently used hashtags in recent reels.
func (s *Store) GetTrendingHashtags(ctx context.Context, limit int, sinceDays int) ([]TrendingHashtag, error) {
	rows, err := s.db.Query(ctx, `
		SELECT hashtag, COUNT(*) as count
		FROM reel_hashtags
		WHERE created_at > NOW() - ($2 || ' days')::INTERVAL
		GROUP BY hashtag
		ORDER BY count DESC
		LIMIT $1
	`, limit, sinceDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []TrendingHashtag
	for rows.Next() {
		var t TrendingHashtag
		if err := rows.Scan(&t.Hashtag, &t.Count); err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, nil
}

// TrendingHashtag represents a hashtag with its usage count.
type TrendingHashtag struct {
	Hashtag string `json:"hashtag"`
	Count   int    `json:"count"`
}

// SetHashtagUseCount writes an absolute use_count for a hashtag tag.
// Used by the sharded-counter flush worker: every flush interval it
// materializes the sum across Redis shards back into hashtags.use_count.
// The row is upserted because the tag may have been created by an
// earlier post that wrote +1 through Redis without ever touching PG.
//
// The aggregate `hashtags` table (tag PRIMARY KEY, use_count BIGINT) is
// distinct from `reel_hashtags` (per-reel tag membership). It's bootstrapped
// in cmd/server main.go DDL so hashtag-using post-service deploys don't
// need a separate migration step.
func (s *Store) SetHashtagUseCount(ctx context.Context, tag string, total int64) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO hashtags (tag, use_count, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (tag) DO UPDATE SET use_count = EXCLUDED.use_count, updated_at = NOW()
	`, tag, total)
	return err
}

// IncrementHashtagUseCount is the Redis-less fallback path: bumps the
// hashtag's use_count by one. Mirrors IncrementAudioUseCount in shape so
// the counter-sharding wrapper can pick either path uniformly.
func (s *Store) IncrementHashtagUseCount(ctx context.Context, tag string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO hashtags (tag, use_count, updated_at)
		VALUES ($1, 1, NOW())
		ON CONFLICT (tag) DO UPDATE SET use_count = hashtags.use_count + 1, updated_at = NOW()
	`, tag)
	return err
}
