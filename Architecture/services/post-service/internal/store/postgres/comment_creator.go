package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

/*
	Creator comment tools (MTube, 2026-09-27; migration 051 part E).

	  comments.hearted_at  — the post author's heart on a comment
	  comments.pinned_at   — the post author's one pinned comment per post
	                          (partial unique index uq_comments_pinned_per_post)

	and the creator inbox: every comment on the caller's own posts, newest
	first, optionally only the ones the author has not replied to.
*/

// ErrCannotPinReply: only a top-level comment can be pinned.
var ErrCannotPinReply = errors.New("only a top-level comment can be pinned")

// CommentOwnerRef is what the author-only comment writes need to decide
// ownership: the comment's post and that post's author.
type CommentOwnerRef struct {
	CommentID  uuid.UUID
	PostID     uuid.UUID
	PostAuthor uuid.UUID
	ParentID   *uuid.UUID
}

// GetCommentOwnerRef loads a visible comment with its post's author. A
// deleted / hidden comment, or one on a deleted post, is COMMENT_NOT_FOUND.
func (s *Store) GetCommentOwnerRef(ctx context.Context, commentID uuid.UUID) (*CommentOwnerRef, error) {
	ref := &CommentOwnerRef{CommentID: commentID}
	err := s.db.QueryRow(ctx, `
		SELECT c.post_id, p.author_id, c.parent_id
		FROM comments c JOIN posts p ON p.id = c.post_id
		WHERE c.id = $1 AND c.`+visibleCommentPredicate+` AND p.deleted_at IS NULL`,
		commentID).Scan(&ref.PostID, &ref.PostAuthor, &ref.ParentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("COMMENT_NOT_FOUND")
		}
		return nil, err
	}
	return ref, nil
}

// SetCommentHeart sets or clears hearted_at. Idempotent: hearting twice is
// one heart, unhearting an unhearted comment is a no-op.
func (s *Store) SetCommentHeart(ctx context.Context, commentID uuid.UUID, on bool) error {
	var err error
	if on {
		_, err = s.db.Exec(ctx,
			`UPDATE comments SET hearted_at = COALESCE(hearted_at, now()) WHERE id = $1`, commentID)
	} else {
		_, err = s.db.Exec(ctx,
			`UPDATE comments SET hearted_at = NULL WHERE id = $1`, commentID)
	}
	if err != nil {
		return fmt.Errorf("set comment heart: %w", err)
	}
	return nil
}

// PinComment makes commentID the post's one pinned comment: any other
// pinned comment on the post is unpinned in the same transaction, so the
// partial unique index is never tripped by a re-pin. A reply is refused.
func (s *Store) PinComment(ctx context.Context, postID, commentID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var parentID *uuid.UUID
	if err := tx.QueryRow(ctx,
		`SELECT parent_id FROM comments WHERE id = $1 AND post_id = $2 AND `+visibleCommentPredicate+` FOR UPDATE`,
		commentID, postID).Scan(&parentID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("COMMENT_NOT_FOUND")
		}
		return err
	}
	if parentID != nil {
		return ErrCannotPinReply
	}
	if _, err := tx.Exec(ctx,
		`UPDATE comments SET pinned_at = NULL WHERE post_id = $1 AND pinned_at IS NOT NULL AND id <> $2`,
		postID, commentID); err != nil {
		return fmt.Errorf("unpin previous: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE comments SET pinned_at = COALESCE(pinned_at, now()) WHERE id = $1`, commentID); err != nil {
		return fmt.Errorf("pin comment: %w", err)
	}
	return tx.Commit(ctx)
}

// UnpinComment clears pinned_at. Idempotent.
func (s *Store) UnpinComment(ctx context.Context, commentID uuid.UUID) error {
	if _, err := s.db.Exec(ctx, `UPDATE comments SET pinned_at = NULL WHERE id = $1`, commentID); err != nil {
		return fmt.Errorf("unpin comment: %w", err)
	}
	return nil
}

// ── Creator inbox ───────────────────────────────────────────────────────────

// Inbox filters, mirrored from the query string by the handler.
const (
	InboxStatusUnanswered = "unanswered"
	InboxStatusAll        = "all"

	InboxSortNewest   = "newest"
	InboxSortRelevant = "relevant"
)

// InboxPostRef is the compact post the inbox row points at.
type InboxPostRef struct {
	ID           uuid.UUID  `json:"id"`
	Title        string     `json:"title"`
	ContentType  string     `json:"content_type"`
	CoverMediaID *uuid.UUID `json:"cover_media_id"`
}

// InboxRow is one creator-inbox entry: the comment, its post, and whether
// the post author has replied to it.
type InboxRow struct {
	Comment       Comment      `json:"comment"`
	Post          InboxPostRef `json:"post"`
	AuthorReplied bool         `json:"author_replied"`
}

// InboxQuery is the page request.
type InboxQuery struct {
	AuthorID uuid.UUID
	// Status: InboxStatusUnanswered or InboxStatusAll.
	Status string
	// ContentTypes: nil means every kind.
	ContentTypes []string
	// Sort: InboxSortNewest (created_at desc, keyset cursor) or
	// InboxSortRelevant (reaction_count desc, created_at desc, offset cursor).
	Sort   string
	Cursor string
	Limit  int
}

// ListCreatorInbox pages the visible comments (top-level and replies alike)
// on the caller's own live posts. "Unanswered" means no visible reply on
// the thread by the post author: for a top-level comment, no author reply
// under it; for a reply, no author reply under its parent that is newer
// than it. Author-written comments are excluded — an author does not need
// to answer themselves.
func (s *Store) ListCreatorInbox(ctx context.Context, q InboxQuery) ([]InboxRow, string, error) {
	limit := q.Limit
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	args := []interface{}{q.AuthorID, limit + 1}
	query := `
		SELECT c.id, c.post_id, c.author_id, c.parent_id, c.body, c.like_count, c.dislike_count, c.reply_count,
		       c.is_reply, c.is_deleted, c.created_at, c.updated_at, c.pinned_at IS NOT NULL, c.hearted_at IS NOT NULL,
		       p.title, p.content_type, p.cover_media_id,
		       EXISTS (
		           SELECT 1 FROM comments r
		           WHERE r.parent_id = COALESCE(c.parent_id, c.id)
		             AND r.author_id = p.author_id
		             AND r.is_deleted = FALSE
		             AND r.created_at > c.created_at
		       ) AS author_replied
		FROM comments c
		JOIN posts p ON p.id = c.post_id
		WHERE p.author_id = $1
		  AND p.deleted_at IS NULL
		  AND c.author_id <> $1
		  AND c.` + visibleCommentPredicate
	if len(q.ContentTypes) > 0 {
		args = append(args, q.ContentTypes)
		query += ` AND p.content_type = ANY($` + strconv.Itoa(len(args)) + `)`
	}
	if q.Status == InboxStatusUnanswered {
		query += ` AND NOT EXISTS (
		           SELECT 1 FROM comments r
		           WHERE r.parent_id = COALESCE(c.parent_id, c.id)
		             AND r.author_id = p.author_id
		             AND r.is_deleted = FALSE
		             AND r.created_at > c.created_at)`
	}
	relevant := q.Sort == InboxSortRelevant
	offset := 0
	if relevant {
		offset = parseOffsetCursor(q.Cursor)
		args = append(args, offset)
		query += ` ORDER BY (SELECT COUNT(*) FROM comment_reactions cr WHERE cr.comment_id = c.id) DESC,
		           c.created_at DESC, c.id DESC LIMIT $2 OFFSET $` + strconv.Itoa(len(args))
	} else {
		if q.Cursor != "" {
			if t, err := time.Parse(time.RFC3339Nano, q.Cursor); err == nil {
				args = append(args, t)
				query += ` AND c.created_at < $` + strconv.Itoa(len(args))
			}
		}
		query += ` ORDER BY c.created_at DESC, c.id DESC LIMIT $2`
	}

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("creator inbox: %w", err)
	}
	defer rows.Close()

	var out []InboxRow
	var comments []Comment
	for rows.Next() {
		var r InboxRow
		c := &r.Comment
		if err := rows.Scan(
			&c.ID, &c.PostID, &c.AuthorID, &c.ParentID, &c.Body,
			&c.LikeCount, &c.DislikeCount, &c.ReplyCount, &c.IsReply, &c.IsDeleted,
			&c.CreatedAt, &c.UpdatedAt, &c.Pinned, &c.HeartedByAuthor,
			&r.Post.Title, &r.Post.ContentType, &r.Post.CoverMediaID,
			&r.AuthorReplied,
		); err != nil {
			return nil, "", err
		}
		r.Post.ID = c.PostID
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var next string
	if len(out) > limit {
		out = out[:limit]
		if relevant {
			next = "o:" + strconv.Itoa(offset+limit)
		} else {
			next = out[limit-1].Comment.CreatedAt.Format(time.RFC3339Nano)
		}
	}
	// Reactions in two queries for the page, then copied back (the
	// hydrator works on a []Comment).
	comments = make([]Comment, len(out))
	for i := range out {
		comments[i] = out[i].Comment
	}
	if err := s.hydrateCommentReactions(ctx, comments, &q.AuthorID); err != nil {
		return nil, "", err
	}
	for i := range out {
		out[i].Comment = comments[i]
	}
	return out, next, nil
}
