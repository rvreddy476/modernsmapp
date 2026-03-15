package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// GetUploadsByContentTypes returns posts by an author filtered by multiple content types,
// with cursor pagination. Uses the partial indexes from migration 009.
func (s *Store) GetUploadsByContentTypes(ctx context.Context, authorID uuid.UUID, contentTypes []string, limit int, cursor string) ([]Post, string, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	args := []interface{}{authorID, contentTypes, limit + 1}
	query := `SELECT ` + postCols + `
		FROM posts
		WHERE author_id = $1 AND content_type = ANY($2) AND deleted_at IS NULL`

	if cursor != "" {
		cursorTime, err := time.Parse(time.RFC3339Nano, cursor)
		if err == nil {
			query += ` AND created_at < $4`
			args = append(args, cursorTime)
		}
	}

	query += ` ORDER BY created_at DESC LIMIT $3`

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	posts, err := scanPostRows(rows)
	if err != nil {
		return nil, "", err
	}

	var nextCursor string
	if len(posts) > limit {
		nextCursor = posts[limit-1].CreatedAt.Format(time.RFC3339Nano)
		posts = posts[:limit]
	}

	// Batch-fetch media
	if len(posts) > 0 {
		postIDs := make([]uuid.UUID, len(posts))
		for i, p := range posts {
			postIDs[i] = p.ID
		}
		mediaRows, err := s.db.Query(ctx, `
			SELECT post_id, media_id, kind FROM post_media WHERE post_id = ANY($1)
		`, postIDs)
		if err == nil {
			defer mediaRows.Close()
			mediaMap := make(map[uuid.UUID][]PostMedia)
			for mediaRows.Next() {
				var postID uuid.UUID
				var m PostMedia
				if err := mediaRows.Scan(&postID, &m.MediaID, &m.Kind); err == nil {
					mediaMap[postID] = append(mediaMap[postID], m)
				}
			}
			for i := range posts {
				posts[i].Media = mediaMap[posts[i].ID]
			}
		}
	}

	return posts, nextCursor, nil
}

// DeleteUploadCascade soft-deletes a post and all its crosspost links + target embed posts.
// Returns the number of cascade-deleted embed posts.
func (s *Store) DeleteUploadCascade(ctx context.Context, postID, authorID uuid.UUID) (int, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	// Verify ownership and soft-delete the source post
	var deletedID uuid.UUID
	err = tx.QueryRow(ctx, `
		UPDATE posts SET deleted_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND author_id = $2 AND deleted_at IS NULL
		RETURNING id
	`, postID, authorID).Scan(&deletedID)
	if err != nil {
		return 0, fmt.Errorf("post not found or not owned by user")
	}

	// Cascade-delete crosspost links (table may not exist yet — use savepoint)
	cascadeCount := 0
	_, _ = tx.Exec(ctx, "SAVEPOINT crosspost_cascade")
	rows, err := tx.Query(ctx, `
		SELECT id, target_post_id FROM crosspost_links
		WHERE source_post_id = $1 AND deleted_at IS NULL
	`, postID)
	if err != nil {
		// Table likely doesn't exist — rollback to savepoint and continue
		_, _ = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT crosspost_cascade")
	} else {
		var linkIDs []uuid.UUID
		var targetPostIDs []uuid.UUID
		for rows.Next() {
			var linkID, targetID uuid.UUID
			if err := rows.Scan(&linkID, &targetID); err == nil {
				linkIDs = append(linkIDs, linkID)
				targetPostIDs = append(targetPostIDs, targetID)
			}
		}
		rows.Close()

		if len(linkIDs) > 0 {
			_, _ = tx.Exec(ctx, `
				UPDATE crosspost_links SET deleted_at = NOW()
				WHERE source_post_id = $1 AND deleted_at IS NULL
			`, postID)
		}
		if len(targetPostIDs) > 0 {
			tag, err := tx.Exec(ctx, `
				UPDATE posts SET deleted_at = NOW(), updated_at = NOW()
				WHERE id = ANY($1) AND deleted_at IS NULL
			`, targetPostIDs)
			if err == nil {
				cascadeCount = int(tag.RowsAffected())
			}
		}
		_, _ = tx.Exec(ctx, "RELEASE SAVEPOINT crosspost_cascade")
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return cascadeCount, nil
}

// CountUploadsByContentTypes returns the count of uploads by content type groups.
func (s *Store) CountUploadsByContentTypes(ctx context.Context, authorID uuid.UUID) (videos, flicks, posts int64, err error) {
	err = s.db.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN content_type IN ('video', 'long_video') THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN content_type IN ('flick', 'reel') THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN content_type IN ('post', 'image') THEN 1 ELSE 0 END), 0)
		FROM posts
		WHERE author_id = $1 AND deleted_at IS NULL
	`, authorID).Scan(&videos, &flicks, &posts)
	return
}
