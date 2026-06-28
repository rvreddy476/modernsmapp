package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// GetReelCandidates returns public reel posts ordered by recency, with cursor pagination.
func (s *Store) GetReelCandidates(ctx context.Context, limit int, cursor string) ([]*Post, string, error) {
	var cursorTime time.Time
	var cursorID uuid.UUID

	if cursor != "" {
		// cursor format: "RFC3339Nano_UUID"
		if t, id, err := parseReelCursor(cursor); err == nil {
			cursorTime = t
			cursorID = id
		}
	}

	var rows pgx.Rows
	var err error

	if cursorTime.IsZero() {
		rows, err = s.db.Query(ctx, `
			SELECT `+postCols+`
			FROM posts
			WHERE content_type = 'reel' AND visibility IN ('public', 'staged')
				AND deleted_at IS NULL AND review_status = 'approved'
			ORDER BY created_at DESC, id DESC
			LIMIT $1
		`, limit)
	} else {
		rows, err = s.db.Query(ctx, `
			SELECT `+postCols+`
			FROM posts
			WHERE content_type = 'reel' AND visibility IN ('public', 'staged')
				AND deleted_at IS NULL AND review_status = 'approved'
				AND (created_at, id) < ($2, $3)
			ORDER BY created_at DESC, id DESC
			LIMIT $1
		`, limit, cursorTime, cursorID)
	}
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	posts, err := scanPostRows(rows)
	if err != nil {
		return nil, "", err
	}

	// Convert to pointer slice
	reels := make([]*Post, len(posts))
	for i := range posts {
		reels[i] = &posts[i]
	}

	nextCursor := ""
	if len(reels) >= limit {
		last := reels[len(reels)-1]
		nextCursor = encodeReelCursor(last.CreatedAt, last.ID)
	}

	return reels, nextCursor, nil
}

func parseReelCursor(cursor string) (time.Time, uuid.UUID, error) {
	parts := splitReelCursor(cursor)
	if len(parts) != 2 {
		return time.Time{}, uuid.Nil, fmt.Errorf("invalid cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	return t, id, nil
}

func splitReelCursor(cursor string) []string {
	// UUID is 36 chars; cursor format is "timestamp_UUID"
	idx := len(cursor) - 37 // 36 UUID chars + 1 underscore
	if idx <= 0 || cursor[idx] != '_' {
		return nil
	}
	return []string{cursor[:idx], cursor[idx+1:]}
}

func encodeReelCursor(t time.Time, id uuid.UUID) string {
	return t.Format(time.RFC3339Nano) + "_" + id.String()
}

// GetFlickCandidates returns public flick posts (content_type IN ('flick', 'reel'))
// ordered by recency with cursor pagination.
func (s *Store) GetFlickCandidates(ctx context.Context, limit int, cursor string) ([]*Post, string, error) {
	var cursorTime time.Time
	var cursorID uuid.UUID

	if cursor != "" {
		if t, id, err := parseReelCursor(cursor); err == nil {
			cursorTime = t
			cursorID = id
		}
	}

	var rows pgx.Rows
	var err error

	if cursorTime.IsZero() {
		rows, err = s.db.Query(ctx, `
			SELECT `+postCols+`
			FROM posts
			WHERE content_type IN ('flick', 'reel') AND visibility IN ('public', 'staged')
				AND deleted_at IS NULL AND review_status = 'approved'
			ORDER BY created_at DESC, id DESC
			LIMIT $1
		`, limit)
	} else {
		rows, err = s.db.Query(ctx, `
			SELECT `+postCols+`
			FROM posts
			WHERE content_type IN ('flick', 'reel') AND visibility IN ('public', 'staged')
				AND deleted_at IS NULL AND review_status = 'approved'
				AND (created_at, id) < ($2, $3)
			ORDER BY created_at DESC, id DESC
			LIMIT $1
		`, limit, cursorTime, cursorID)
	}
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	posts, err := scanPostRows(rows)
	if err != nil {
		return nil, "", err
	}

	result := make([]*Post, len(posts))
	for i := range posts {
		result[i] = &posts[i]
	}

	nextCursor := ""
	if len(result) >= limit {
		last := result[len(result)-1]
		nextCursor = encodeReelCursor(last.CreatedAt, last.ID)
	}

	return result, nextCursor, nil
}

// GetLongVideoCandidates returns public long video posts (content_type IN ('long_video', 'video'))
// ordered by recency with cursor pagination.
func (s *Store) GetLongVideoCandidates(ctx context.Context, limit int, cursor string) ([]*Post, string, error) {
	var cursorTime time.Time
	var cursorID uuid.UUID

	if cursor != "" {
		if t, id, err := parseReelCursor(cursor); err == nil {
			cursorTime = t
			cursorID = id
		}
	}

	var rows pgx.Rows
	var err error

	if cursorTime.IsZero() {
		rows, err = s.db.Query(ctx, `
			SELECT `+postCols+`
			FROM posts
			WHERE content_type IN ('long_video', 'video') AND visibility IN ('public', 'staged')
				AND deleted_at IS NULL AND review_status = 'approved'
			ORDER BY created_at DESC, id DESC
			LIMIT $1
		`, limit)
	} else {
		rows, err = s.db.Query(ctx, `
			SELECT `+postCols+`
			FROM posts
			WHERE content_type IN ('long_video', 'video') AND visibility IN ('public', 'staged')
				AND deleted_at IS NULL AND review_status = 'approved'
				AND (created_at, id) < ($2, $3)
			ORDER BY created_at DESC, id DESC
			LIMIT $1
		`, limit, cursorTime, cursorID)
	}
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	posts, err := scanPostRows(rows)
	if err != nil {
		return nil, "", err
	}

	result := make([]*Post, len(posts))
	for i := range posts {
		result[i] = &posts[i]
	}

	nextCursor := ""
	if len(result) >= limit {
		last := result[len(result)-1]
		nextCursor = encodeReelCursor(last.CreatedAt, last.ID)
	}

	return result, nextCursor, nil
}
