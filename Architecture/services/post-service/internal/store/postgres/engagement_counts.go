package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// validEngagementColumns guards the dynamically-built column name used
// in IncrementEngagementCount / SetEngagementCount against SQL
// injection. Only these five columns are allowed.
var validEngagementColumns = map[string]bool{
	"like_count":     true,
	"comment_count":  true,
	"share_count":    true,
	"bookmark_count": true,
	"repost_count":   true,
}

// IncrementEngagementCount is the legacy per-event UPDATE path used as
// a fallback when the sharded Redis counter is unavailable (Redis nil
// or transient failure). Same semantics as the old per-column
// IncrementRepostCount/CreateComment-inline UPDATE — INSERT … ON
// CONFLICT DO UPDATE so the row exists before the increment lands.
func (s *Store) IncrementEngagementCount(ctx context.Context, postID uuid.UUID, column string, delta int64) error {
	if !validEngagementColumns[column] {
		return fmt.Errorf("invalid engagement column: %s", column)
	}
	query := fmt.Sprintf(`
		INSERT INTO post_engagement_counts (post_id, %s)
		VALUES ($1, GREATEST($2, 0))
		ON CONFLICT (post_id) DO UPDATE SET
			%s = GREATEST(post_engagement_counts.%s + $2, 0),
			updated_at = now()`, column, column, column)
	_, err := s.db.Exec(ctx, query, postID, delta)
	return err
}

// SetEngagementCount overwrites the column to the absolute value. Used
// by the sharded-counter flush worker — Redis is the realtime buffer,
// this UPDATE periodically materializes the shard sum back to PG.
// Idempotent INSERT path so rows for posts that haven't been flushed
// yet still pick up their counter.
func (s *Store) SetEngagementCount(ctx context.Context, postID uuid.UUID, column string, total int64) error {
	if !validEngagementColumns[column] {
		return fmt.Errorf("invalid engagement column: %s", column)
	}
	query := fmt.Sprintf(`
		INSERT INTO post_engagement_counts (post_id, %s)
		VALUES ($1, GREATEST($2, 0))
		ON CONFLICT (post_id) DO UPDATE SET
			%s = GREATEST($2, 0),
			updated_at = now()`, column, column)
	_, err := s.db.Exec(ctx, query, postID, total)
	return err
}
