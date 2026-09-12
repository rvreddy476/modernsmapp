package postgres

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
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
	// LastWatchedAt is the row's last_watched_at; on the wire it is
	// `updated_at` (the Tube contract, 2026-09-05).
	LastWatchedAt time.Time `json:"updated_at"`
}

// UpsertWatchProgress inserts or updates a watch_progress row and reads the
// stored row back into wp, so the response carries the kept duration and the
// real updated_at rather than what the client happened to send.
func (s *Store) UpsertWatchProgress(ctx context.Context, wp *WatchProgress) error {
	return s.db.QueryRow(ctx, `
		INSERT INTO watch_progress (user_id, post_id, position_ms, duration_ms, percent_watched, completed, last_watched_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
		ON CONFLICT (user_id, post_id) DO UPDATE SET
			position_ms     = EXCLUDED.position_ms,
			duration_ms     = COALESCE(NULLIF(EXCLUDED.duration_ms, 0), watch_progress.duration_ms),
			percent_watched = EXCLUDED.percent_watched,
			completed       = EXCLUDED.completed,
			last_watched_at = NOW()
		RETURNING duration_ms, last_watched_at`,
		wp.UserID, wp.PostID, wp.PositionMs, wp.DurationMs, wp.PercentWatched, wp.Completed,
	).Scan(&wp.DurationMs, &wp.LastWatchedAt)
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

// Watch history (Tube "You" page, 2026-09-12). continue-watching above is
// the shelf: unfinished rows only, one limit, no way past the first page.
// History is the full record, completed rows included, and it pages: a
// viewer with a year of watching cannot be served in one response.

const (
	watchHistoryDefaultLimit = 20
	watchHistoryMaxLimit     = 100
)

// ErrInvalidWatchHistoryCursor: the cursor is not one this store issued.
var ErrInvalidWatchHistoryCursor = errors.New("invalid watch history cursor")

// encodeWatchHistoryCursor / decodeWatchHistoryCursor carry the keyset
// position opaquely: base64url of "<RFC3339Nano>|<post uuid>", the same
// shape as the channel-subscriptions cursor. Keyset rather than offset
// because the list moves under the reader: every playback bumps a row to
// the top, and an offset page would then repeat or skip rows.
func encodeWatchHistoryCursor(at time.Time, postID uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + postID.String()))
}

// decodeWatchHistoryCursor returns ok=false for an empty cursor (first
// page) and ErrInvalidWatchHistoryCursor for anything it cannot read, so a
// tampered cursor becomes a 400 and never a silent restart from the top.
func decodeWatchHistoryCursor(raw string) (at time.Time, postID uuid.UUID, ok bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, uuid.Nil, false, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return time.Time{}, uuid.Nil, false, ErrInvalidWatchHistoryCursor
	}
	parts := strings.SplitN(string(b), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, uuid.Nil, false, ErrInvalidWatchHistoryCursor
	}
	at, err = time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, uuid.Nil, false, ErrInvalidWatchHistoryCursor
	}
	postID, err = uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, false, ErrInvalidWatchHistoryCursor
	}
	return at, postID, true, nil
}

// GetWatchHistory returns one page of the viewer's watch_progress rows,
// completed or not, most recently watched first with post_id breaking
// ties, and the cursor for the next page ("" on the last one). Rows whose
// post is gone are still returned here: the service hydrates and drops
// them, and the cursor has to advance past them or a run of deleted posts
// would pin the reader to the same page.
func (s *Store) GetWatchHistory(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]WatchProgress, string, error) {
	if limit <= 0 {
		limit = watchHistoryDefaultLimit
	}
	if limit > watchHistoryMaxLimit {
		limit = watchHistoryMaxLimit
	}
	at, afterPost, hasCursor, err := decodeWatchHistoryCursor(cursor)
	if err != nil {
		return nil, "", err
	}

	args := []interface{}{userID, limit + 1}
	query := `
		SELECT user_id, post_id, position_ms, duration_ms, percent_watched, completed, last_watched_at
		FROM watch_progress
		WHERE user_id = $1`
	if hasCursor {
		// Strictly after the cursor row in (last_watched_at DESC, post_id ASC)
		// order. Written out rather than as a row comparison because the two
		// columns sort in opposite directions.
		query += ` AND (last_watched_at < $3 OR (last_watched_at = $3 AND post_id > $4))`
		args = append(args, at, afterPost)
	}
	query += ` ORDER BY last_watched_at DESC, post_id ASC LIMIT $2`

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var result []WatchProgress
	for rows.Next() {
		var wp WatchProgress
		if err := rows.Scan(&wp.UserID, &wp.PostID, &wp.PositionMs, &wp.DurationMs, &wp.PercentWatched, &wp.Completed, &wp.LastWatchedAt); err != nil {
			return nil, "", err
		}
		result = append(result, wp)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var nextCursor string
	if len(result) > limit {
		result = result[:limit]
		last := result[limit-1]
		nextCursor = encodeWatchHistoryCursor(last.LastWatchedAt, last.PostID)
	}
	return result, nextCursor, nil
}

// DeleteAllWatchProgress clears every watch_progress row of one user and
// returns the post ids it removed, so the caller can drop the matching
// Redis mirror keys (watch_progress:<user>:<post>) as well.
func (s *Store) DeleteAllWatchProgress(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `DELETE FROM watch_progress WHERE user_id = $1 RETURNING post_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
