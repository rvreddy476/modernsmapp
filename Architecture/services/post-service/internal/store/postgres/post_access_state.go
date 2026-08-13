package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PostAccessState is the small canonical state that may revoke public access
// while a cached post body still exists. It is deliberately read from
// PostgreSQL on every cache hit: cache invalidation is best-effort and cannot
// be the safety boundary for moderation or deletion.
type PostAccessState struct {
	ReviewStatus string
	Deleted      bool
}

func (s *Store) GetPostAccessState(ctx context.Context, postID uuid.UUID) (*PostAccessState, error) {
	var state PostAccessState
	err := s.db.QueryRow(ctx, `
		SELECT review_status, deleted_at IS NOT NULL
		FROM posts
		WHERE id=$1
	`, postID).Scan(&state.ReviewStatus, &state.Deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &state, nil
}
