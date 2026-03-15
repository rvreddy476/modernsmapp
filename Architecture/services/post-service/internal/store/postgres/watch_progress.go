package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// WatchProgress tracks how far a user has watched a video post.
type WatchProgress struct {
	UserID         uuid.UUID `json:"user_id"`
	PostID         uuid.UUID `json:"post_id"`
	PositionMs     int       `json:"position_ms"`
	DurationMs     int       `json:"duration_ms"`
	PercentWatched float32   `json:"percent_watched"`
	Completed      bool      `json:"completed"`
	LastWatchedAt  time.Time `json:"last_watched_at"`
}

// UpsertWatchProgress inserts or updates a watch_progress row.
func (s *Store) UpsertWatchProgress(ctx context.Context, wp *WatchProgress) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO watch_progress (user_id, post_id, position_ms, duration_ms, percent_watched, completed, last_watched_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
		ON CONFLICT (user_id, post_id) DO UPDATE SET
			position_ms     = EXCLUDED.position_ms,
			duration_ms     = EXCLUDED.duration_ms,
			percent_watched = EXCLUDED.percent_watched,
			completed       = EXCLUDED.completed,
			last_watched_at = NOW()`,
		wp.UserID, wp.PostID, wp.PositionMs, wp.DurationMs, wp.PercentWatched, wp.Completed)
	return err
}

// GetWatchProgress retrieves a single watch_progress row. Returns nil, nil if not found.
func (s *Store) GetWatchProgress(ctx context.Context, userID, postID uuid.UUID) (*WatchProgress, error) {
	wp := &WatchProgress{}
	err := s.db.QueryRow(ctx, `
		SELECT user_id, post_id, position_ms, duration_ms, percent_watched, completed, last_watched_at
		FROM watch_progress WHERE user_id = $1 AND post_id = $2`,
		userID, postID,
	).Scan(&wp.UserID, &wp.PostID, &wp.PositionMs, &wp.DurationMs, &wp.PercentWatched, &wp.Completed, &wp.LastWatchedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return wp, err
}

// DeleteWatchProgress removes a watch_progress row.
func (s *Store) DeleteWatchProgress(ctx context.Context, userID, postID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM watch_progress WHERE user_id = $1 AND post_id = $2`, userID, postID)
	return err
}

// GetContinueWatching returns incomplete watch_progress rows for a user ordered by last_watched_at DESC.
func (s *Store) GetContinueWatching(ctx context.Context, userID uuid.UUID, limit int) ([]WatchProgress, error) {
	rows, err := s.db.Query(ctx, `
		SELECT user_id, post_id, position_ms, duration_ms, percent_watched, completed, last_watched_at
		FROM watch_progress
		WHERE user_id = $1 AND completed = FALSE
		ORDER BY last_watched_at DESC
		LIMIT $2`,
		userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []WatchProgress
	for rows.Next() {
		var wp WatchProgress
		if err := rows.Scan(&wp.UserID, &wp.PostID, &wp.PositionMs, &wp.DurationMs, &wp.PercentWatched, &wp.Completed, &wp.LastWatchedAt); err != nil {
			return nil, err
		}
		result = append(result, wp)
	}
	return result, rows.Err()
}
