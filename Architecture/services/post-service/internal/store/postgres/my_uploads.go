package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/shared/events"
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
	// Ordered + normalized in one place; see post_media.go.
	if err := s.attachPostMedia(ctx, posts); err != nil {
		return nil, "", err
	}

	return posts, nextCursor, nil
}

// DeleteUploadCascade SOFT-deletes a post and all its crosspost links + target
// embed posts, and emits — in the same transaction — the two events every
// downstream surface needs:
//
//   - PostSearchEligibilityChanged (deleted=true) for search, via the
//     canonical choke point, and
//   - PostDeleted for feed / notification consumers, carrying the SAME
//     search revision so no consumer raises a barrier past the canonical
//     one (a restore is rev+1 and must not be dropped as stale).
//
// Nothing is erased here: rows and media stay so the author can restore
// from "Recently deleted" for purgeAfter; the purge worker hard-deletes
// after that (internal/postpurge). Returns the number of cascade-deleted
// embed posts.
func (s *Store) DeleteUploadCascade(ctx context.Context, postID, authorID uuid.UUID, purgeAfter time.Duration) (int, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	// Verify ownership and soft-delete the source post. One NOW() for the
	// whole transaction: cascaded targets share the exact deleted_at, which
	// is how RestorePost finds them again.
	var (
		deletedID   uuid.UUID
		deletedAt   time.Time
		contentType string
		createdAt   time.Time
	)
	err = tx.QueryRow(ctx, `
		UPDATE posts SET deleted_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND author_id = $2 AND deleted_at IS NULL
		RETURNING id, deleted_at, content_type, created_at
	`, postID, authorID).Scan(&deletedID, &deletedAt, &contentType, &createdAt)
	if err != nil {
		return 0, fmt.Errorf("post not found or not owned by user")
	}
	purgeAt := deletedAt.Add(purgeAfter)

	// M2-P0-2: deletion removes the post from public search. Emitted in
	// this same transaction so the removal cannot be lost.
	rev, err := BumpSearchRevAndEmitTxRev(ctx, tx, postID)
	if err != nil {
		return 0, fmt.Errorf("emit search eligibility on delete: %w", err)
	}
	if err := InsertOutboxEventTx(ctx, tx, events.PostDeleted, "post", postID, events.PostDeletedPayload{
		PostID: postID.String(), AuthorID: authorID.String(), DeletedAt: deletedAt,
		ContentType: contentType, CreatedAt: createdAt, SearchRev: rev, PurgeAt: &purgeAt,
	}); err != nil {
		return 0, fmt.Errorf("emit PostDeleted: %w", err)
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
				UPDATE crosspost_links SET deleted_at = $2
				WHERE source_post_id = $1 AND deleted_at IS NULL
			`, postID, deletedAt)
		}
		if len(targetPostIDs) > 0 {
			trows, err := tx.Query(ctx, `
				UPDATE posts SET deleted_at = $2, updated_at = NOW()
				WHERE id = ANY($1) AND deleted_at IS NULL
				RETURNING id, author_id, content_type, created_at
			`, targetPostIDs, deletedAt)
			if err == nil {
				type target struct {
					id, author  uuid.UUID
					contentType string
					createdAt   time.Time
				}
				var targets []target
				for trows.Next() {
					var t target
					if err := trows.Scan(&t.id, &t.author, &t.contentType, &t.createdAt); err == nil {
						targets = append(targets, t)
					}
				}
				trows.Close()
				cascadeCount = len(targets)
				// M2-P0-2: each cascaded delete must leave public search,
				// and every feed that carries the embed must drop it.
				for _, t := range targets {
					trev, emitErr := BumpSearchRevAndEmitTxRev(ctx, tx, t.id)
					if emitErr != nil {
						return 0, fmt.Errorf("emit search eligibility on cascade delete: %w", emitErr)
					}
					if emitErr := InsertOutboxEventTx(ctx, tx, events.PostDeleted, "post", t.id, events.PostDeletedPayload{
						PostID: t.id.String(), AuthorID: t.author.String(), DeletedAt: deletedAt,
						ContentType: t.contentType, CreatedAt: t.createdAt, SearchRev: trev, PurgeAt: &purgeAt,
					}); emitErr != nil {
						return 0, fmt.Errorf("emit PostDeleted on cascade delete: %w", emitErr)
					}
				}
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
