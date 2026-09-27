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

// Comment represents a comment or reply in the comments table.
type Comment struct {
	ID           uuid.UUID  `json:"id"`
	PostID       uuid.UUID  `json:"post_id"`
	AuthorID     uuid.UUID  `json:"author_id"`
	ParentID     *uuid.UUID `json:"parent_id,omitempty"`
	Body         string     `json:"body"`
	LikeCount    int        `json:"like_count"`
	DislikeCount int        `json:"dislike_count"`
	ReplyCount   int        `json:"reply_count"`
	IsReply      bool       `json:"is_reply"`
	IsDeleted    bool       `json:"-"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	// Emoji reactions (comment_reactions, migration 050). Reactions is the
	// top CommentReactionTop emojis by count; ReactionCount counts every
	// reaction; ViewerReaction is the viewer's own emoji, nil when none or
	// anonymous. LikeCount == ReactionCount, computed at read time.
	Reactions      []CommentReaction `json:"reactions"`
	ReactionCount  int               `json:"reaction_count"`
	ViewerReaction *string           `json:"viewer_reaction"`
	// Creator tools (migration 051). Pinned: the post author pinned this
	// comment (at most one per post; it leads every list). HeartedByAuthor:
	// the post author hearted it. Never omitempty: a client must not
	// confuse "false" with "unknown".
	Pinned          bool `json:"pinned"`
	HeartedByAuthor bool `json:"hearted_by_author"`
	// First reply (oldest visible), kept for clients that predate
	// GET /v1/comments/:id/replies. ReplyCount is the full number.
	Reply *Comment `json:"reply,omitempty"`
	// Author presentation, hydrated at read time from identity-profile —
	// never stored here. Nil when hydration was skipped or failed; clients
	// must render a fallback identity from AuthorID.
	Author *CommentAuthor `json:"author,omitempty"`
}

// CommentAuthor is the deliberately small public identity a comment row
// renders with — the comment-list twin of the feed's embedded author.
type CommentAuthor struct {
	ID            uuid.UUID  `json:"id"`
	Username      string     `json:"username"`
	DisplayName   string     `json:"display_name"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id,omitempty"`
}

// GetCommentByID returns the comment's author_id and post_id for a given comment ID.
func (s *Store) GetCommentByID(ctx context.Context, commentID uuid.UUID) (*Comment, error) {
	row := s.db.QueryRow(ctx,
		`SELECT id, post_id, author_id FROM comments WHERE id = $1 AND is_deleted = false`,
		commentID,
	)
	c := &Comment{}
	if err := row.Scan(&c.ID, &c.PostID, &c.AuthorID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("COMMENT_NOT_FOUND")
		}
		return nil, err
	}
	return c, nil
}

// GetVisibleCommentByID is GetCommentByID restricted to the public thread:
// is_deleted = FALSE AND moderation_status = 'visible'. Reactions and
// replies target comments through this, so a held or hidden comment answers
// COMMENT_NOT_FOUND to everyone (the post-level gate is the service's).
func (s *Store) GetVisibleCommentByID(ctx context.Context, commentID uuid.UUID) (*Comment, error) {
	row := s.db.QueryRow(ctx,
		`SELECT id, post_id, author_id, parent_id, is_reply FROM comments
		 WHERE id = $1 AND `+visibleCommentPredicate,
		commentID,
	)
	c := &Comment{}
	if err := row.Scan(&c.ID, &c.PostID, &c.AuthorID, &c.ParentID, &c.IsReply); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("COMMENT_NOT_FOUND")
		}
		return nil, err
	}
	return c, nil
}

// IncrementCommentLikeCount atomically increments or decrements a comment's
// like_count column. Reactions no longer write it (like_count on the wire
// is reaction_count, computed at read time); only the dislike toggle's
// legacy mutual-exclusion path still calls this.
func (s *Store) IncrementCommentLikeCount(ctx context.Context, commentID uuid.UUID, delta int) error {
	_, err := s.db.Exec(ctx,
		`UPDATE comments SET like_count = GREATEST(0, like_count + $1) WHERE id = $2`,
		delta, commentID,
	)
	return err
}

// GetCommentDislikeCount reads the dislike_count column (still maintained
// by the dislike toggle) for the legacy like response.
func (s *Store) GetCommentDislikeCount(ctx context.Context, commentID uuid.UUID) (int64, error) {
	var n int64
	err := s.db.QueryRow(ctx, `SELECT dislike_count FROM comments WHERE id = $1`, commentID).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return n, err
}

// IncrementCommentDislikeCount atomically increments or decrements a comment's dislike_count.
func (s *Store) IncrementCommentDislikeCount(ctx context.Context, commentID uuid.UUID, delta int) error {
	_, err := s.db.Exec(ctx,
		`UPDATE comments SET dislike_count = GREATEST(0, dislike_count + $1) WHERE id = $2`,
		delta, commentID,
	)
	return err
}

// CreateComment inserts a top-level comment and increments the post's comment count.
func (s *Store) CreateComment(ctx context.Context, postID, authorID uuid.UUID, body string) (*Comment, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	comment := &Comment{
		ID:        uuid.New(),
		PostID:    postID,
		AuthorID:  authorID,
		Body:      body,
		IsReply:   false,
		Reactions: []CommentReaction{},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO comments (id, post_id, author_id, body, is_reply, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		comment.ID, comment.PostID, comment.AuthorID, comment.Body,
		comment.IsReply, comment.CreatedAt, comment.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert comment: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	// post_engagement_counts.comment_count is bumped at the service
	// layer via the sharded Redis counter (see
	// (*Service).CreateCommentPG → adjustEngagementCount). The legacy
	// in-tx INSERT … ON CONFLICT DO UPDATE was the original hot-row
	// path — a celebrity comment thread pinned this single row and
	// every comment serialized on it.

	return comment, nil
}

// CreateReply creates a reply under a comment (2026-09-27: open to every
// viewer who may comment on the post — the post-owner rule and the
// one-reply cap are gone; the service applies the same gates AddComment
// does before calling this).
//
// Threading stays one level deep: replying to a REPLY attaches the new row
// to that reply's parent (the client prefixes @username), so parent_id is
// always a top-level comment and reply_count on that parent increments.
//
// The target must be in the public thread (visible, not deleted); anything
// else is COMMENT_NOT_FOUND. Returns the author of the comment the caller
// actually replied to (the reply's author when replying to a reply), so the
// service can notify them.
func (s *Store) CreateReply(ctx context.Context, commentID, userID uuid.UUID, body string) (*Comment, uuid.UUID, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, uuid.Nil, err
	}
	defer tx.Rollback(ctx)

	// Load the target: the comment the caller tapped "reply" on.
	var targetPostID, targetAuthorID uuid.UUID
	var targetParentID *uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT post_id, author_id, parent_id
		FROM comments WHERE id = $1 AND `+visibleCommentPredicate,
		commentID,
	).Scan(&targetPostID, &targetAuthorID, &targetParentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, uuid.Nil, fmt.Errorf("COMMENT_NOT_FOUND")
		}
		return nil, uuid.Nil, err
	}

	// One level of threading: a reply to a reply hangs off the reply's parent.
	parentID := commentID
	if targetParentID != nil {
		parentID = *targetParentID
		var parentPostID uuid.UUID
		if err := tx.QueryRow(ctx,
			`SELECT post_id FROM comments WHERE id = $1 AND is_deleted = FALSE`, parentID,
		).Scan(&parentPostID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, uuid.Nil, fmt.Errorf("COMMENT_NOT_FOUND")
			}
			return nil, uuid.Nil, err
		}
		targetPostID = parentPostID
	}

	reply := &Comment{
		ID:        uuid.New(),
		PostID:    targetPostID,
		AuthorID:  userID,
		ParentID:  &parentID,
		Body:      body,
		IsReply:   true,
		Reactions: []CommentReaction{},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO comments (id, post_id, author_id, parent_id, body, is_reply, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		reply.ID, reply.PostID, reply.AuthorID, reply.ParentID,
		reply.Body, reply.IsReply, reply.CreatedAt, reply.UpdatedAt,
	)
	if err != nil {
		return nil, uuid.Nil, fmt.Errorf("insert reply: %w", err)
	}

	// Update the (top-level) parent's reply_count
	_, err = tx.Exec(ctx, `
		UPDATE comments SET reply_count = reply_count + 1, updated_at = now()
		WHERE id = $1`, parentID)
	if err != nil {
		return nil, uuid.Nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, uuid.Nil, err
	}

	// The author of the comment actually replied to, for the notification.
	return reply, targetAuthorID, nil
}

// GetReplies pages a comment's replies oldest-first for
// GET /v1/comments/:commentId/replies. Same moderation rule as the inline
// reply in ListComments: 'visible' to everyone, 'review' only to its own
// author, hidden / removed / deleted never. cursor is the created_at of
// the last row seen (RFC3339Nano); limit defaults to 20, max 100.
//
// Reactions are hydrated here (two queries for the page); author
// enrichment is the service's (HydrateCommentAuthors).
func (s *Store) GetReplies(ctx context.Context, parentID uuid.UUID, viewerID *uuid.UUID, cursor string, limit int) ([]Comment, string, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	args := []interface{}{parentID, limit + 1}
	visibilityClause := `AND moderation_status = 'visible'`
	if viewerID != nil {
		args = append(args, *viewerID)
		visibilityClause = `AND (moderation_status = 'visible' OR (moderation_status = 'review' AND author_id = $3))`
	}
	query := `SELECT id, post_id, author_id, parent_id, body, like_count, dislike_count, reply_count,
		is_reply, is_deleted, created_at, updated_at, pinned_at IS NOT NULL, hearted_at IS NOT NULL
		FROM comments
		WHERE parent_id = $1 AND is_deleted = FALSE ` + visibilityClause
	if cursor != "" {
		if cursorTime, err := time.Parse(time.RFC3339Nano, cursor); err == nil {
			query += ` AND created_at > $` + strconv.Itoa(len(args)+1)
			args = append(args, cursorTime)
		}
	}
	query += ` ORDER BY created_at ASC, id ASC LIMIT $2`

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	replies := []Comment{}
	for rows.Next() {
		var r Comment
		if err := rows.Scan(
			&r.ID, &r.PostID, &r.AuthorID, &r.ParentID, &r.Body,
			&r.LikeCount, &r.DislikeCount, &r.ReplyCount, &r.IsReply, &r.IsDeleted,
			&r.CreatedAt, &r.UpdatedAt, &r.Pinned, &r.HeartedByAuthor,
		); err != nil {
			return nil, "", err
		}
		replies = append(replies, r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var nextCursor string
	if len(replies) > limit {
		replies = replies[:limit]
		nextCursor = replies[limit-1].CreatedAt.Format(time.RFC3339Nano)
	}
	if err := s.hydrateCommentReactions(ctx, replies, viewerID); err != nil {
		return nil, "", err
	}
	return replies, nextCursor, nil
}

// ListComments returns paginated top-level comments with their inline replies.
//
// viewerID is used to enforce moderation visibility:
//   - 'visible' comments are shown to everyone.
//   - 'review' comments (auto-flagged, awaiting moderator) are only
//     shown to the comment's author.
//   - 'hidden' / 'removed' are filtered out entirely (the moderation
//     queue endpoint at /v1/admin/comments/moderation surfaces them
//     for moderators).
//
// Pass viewerID == nil for anonymous viewers (visible-only).
// Comment sort orders for GET /v1/posts/:postId/comments?sort= (2026-09-27).
//
//   - "" / "newest": created_at desc, the behaviour every client has had.
//     The cursor is the created_at of the last row (RFC3339Nano).
//   - "top": reaction_count desc, reply_count desc, created_at desc. A
//     page in this order has no natural keyset, so the cursor is an offset
//     ("o:<n>"); the page size is capped at 50 and threads are short-lived
//     enough that offset paging is honest here.
//
// In BOTH orders the post author's pinned comment (comments.pinned_at) is
// the first row of the first page and is never repeated on a later page.
const (
	CommentSortNewest = "newest"
	CommentSortTop    = "top"
)

// commentTopOrder is the "top" ORDER BY. reaction_count is computed from
// comment_reactions (migration 050) — comments.like_count is not maintained
// for reactions any more, so it cannot be the sort key.
const commentTopOrder = `(SELECT COUNT(*) FROM comment_reactions cr WHERE cr.comment_id = comments.id) DESC,
		reply_count DESC, created_at DESC, id DESC`

// ListComments returns paginated top-level comments with their inline
// replies in the default (newest) order. See ListCommentsSorted.
func (s *Store) ListComments(ctx context.Context, postID uuid.UUID, viewerID *uuid.UUID, cursor string, limit int) ([]Comment, string, error) {
	return s.ListCommentsSorted(ctx, postID, viewerID, cursor, limit, CommentSortNewest)
}

// ListCommentsSorted returns paginated top-level comments with their inline
// replies, in the requested order (CommentSortNewest / CommentSortTop; an
// unknown value is newest).
//
// viewerID is used to enforce moderation visibility:
//   - 'visible' comments are shown to everyone.
//   - 'review' comments (auto-flagged, awaiting moderator) are only
//     shown to the comment's author.
//   - 'hidden' / 'removed' are filtered out entirely (the moderation
//     queue endpoint at /v1/admin/comments/moderation surfaces them
//     for moderators).
//
// Pass viewerID == nil for anonymous viewers (visible-only).
func (s *Store) ListCommentsSorted(ctx context.Context, postID uuid.UUID, viewerID *uuid.UUID, cursor string, limit int, sort string) ([]Comment, string, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	args := []interface{}{postID, limit + 1}
	visibilityClause := `AND moderation_status = 'visible'`
	if viewerID != nil {
		args = append(args, *viewerID)
		visibilityClause = `AND (moderation_status = 'visible' OR (moderation_status = 'review' AND author_id = $3))`
	}

	query := `SELECT id, post_id, author_id, parent_id, body, like_count, dislike_count, reply_count,
		is_reply, is_deleted, created_at, updated_at, pinned_at IS NOT NULL, hearted_at IS NOT NULL
		FROM comments
		WHERE post_id = $1 AND parent_id IS NULL AND is_deleted = FALSE ` + visibilityClause

	top := sort == CommentSortTop
	if top {
		offset := parseOffsetCursor(cursor)
		if offset > 0 {
			// A continuation page never repeats the pinned row.
			query += ` AND pinned_at IS NULL`
		}
		query += ` ORDER BY (pinned_at IS NOT NULL) DESC, ` + commentTopOrder +
			` LIMIT $2 OFFSET $` + strconv.Itoa(len(args)+1)
		args = append(args, offset)
	} else {
		if cursor != "" {
			cursorTime, err := time.Parse(time.RFC3339Nano, cursor)
			if err == nil {
				query += ` AND created_at < $` + strconv.Itoa(len(args)+1) + ` AND pinned_at IS NULL`
				args = append(args, cursorTime)
			}
		}
		query += ` ORDER BY (pinned_at IS NOT NULL) DESC, created_at DESC LIMIT $2`
	}

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var comments []Comment
	var commentIDs []uuid.UUID
	for rows.Next() {
		var c Comment
		if err := rows.Scan(
			&c.ID, &c.PostID, &c.AuthorID, &c.ParentID, &c.Body,
			&c.LikeCount, &c.DislikeCount, &c.ReplyCount, &c.IsReply, &c.IsDeleted,
			&c.CreatedAt, &c.UpdatedAt, &c.Pinned, &c.HeartedByAuthor,
		); err != nil {
			return nil, "", err
		}
		comments = append(comments, c)
		commentIDs = append(commentIDs, c.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var nextCursor string
	if len(comments) > limit {
		comments = comments[:limit]
		commentIDs = commentIDs[:limit]
		nextCursor = nextCommentCursor(comments, top, parseOffsetCursor(cursor)+limit)
	}

	// Load the first inline reply for these comments. Same moderation
	// rules apply — a hidden reply doesn't show under its parent.
	if len(commentIDs) > 0 {
		s.loadInlineReplies(ctx, comments, commentIDs, viewerID)
	}
	if err := s.hydrateCommentReactions(ctx, comments, viewerID); err != nil {
		return nil, "", err
	}

	return comments, nextCursor, nil
}

// nextCommentCursor is the continuation cursor for a full page. In top
// order it is the next offset. In newest order it is the created_at of the
// last row that is NOT the pinned one (the pinned row leads the page
// regardless of age, so its timestamp would skip everything newer); when the
// page held nothing but the pinned row the cursor is "now", which the
// continuation reads as "every unpinned row".
func nextCommentCursor(page []Comment, top bool, nextOffset int) string {
	if top {
		return "o:" + strconv.Itoa(nextOffset)
	}
	for i := len(page) - 1; i >= 0; i-- {
		if !page[i].Pinned {
			return page[i].CreatedAt.Format(time.RFC3339Nano)
		}
	}
	return time.Now().Format(time.RFC3339Nano)
}

// parseOffsetCursor reads an "o:<n>" cursor; anything else is offset 0.
func parseOffsetCursor(cursor string) int {
	if len(cursor) < 3 || cursor[:2] != "o:" {
		return 0
	}
	n, err := strconv.Atoi(cursor[2:])
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// GetCommentsAround returns top-level comments surrounding a target comment.
// It fetches half the limit before and half after the target, centering the result.
//
// viewerID drives moderation visibility (same rules as ListComments):
//   - 'visible' shown to everyone.
//   - 'review' shown only to the comment's author.
//   - 'hidden' / 'removed' filtered out entirely.
//
// Pass viewerID == nil for anonymous viewers (visible-only).
func (s *Store) GetCommentsAround(ctx context.Context, postID, commentID uuid.UUID, viewerID *uuid.UUID, limit int) ([]Comment, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	half := limit / 2

	// 1. Get the target comment's created_at
	var targetCreatedAt time.Time
	err := s.db.QueryRow(ctx, `
		SELECT created_at FROM comments WHERE id = $1 AND post_id = $2 AND is_deleted = FALSE`,
		commentID, postID,
	).Scan(&targetCreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("COMMENT_NOT_FOUND")
		}
		return nil, err
	}

	// 2. Fetch comments: half newer + target + half older (using a UNION approach)
	// "Newer" means created_at >= target (ordered ASC, take half) then flip
	// "Older" means created_at < target (ordered DESC, take half)
	args := []interface{}{postID, targetCreatedAt, half + 1, half}
	visibilityClause := `AND moderation_status = 'visible'`
	if viewerID != nil {
		args = append(args, *viewerID)
		visibilityClause = `AND (moderation_status = 'visible' OR (moderation_status = 'review' AND author_id = $5))`
	}
	query := `
		(SELECT id, post_id, author_id, parent_id, body, like_count, dislike_count, reply_count,
			is_reply, is_deleted, created_at, updated_at, pinned_at IS NOT NULL, hearted_at IS NOT NULL
		FROM comments
		WHERE post_id = $1 AND parent_id IS NULL AND is_deleted = FALSE AND created_at >= $2 ` + visibilityClause + `
		ORDER BY created_at ASC
		LIMIT $3)
		UNION ALL
		(SELECT id, post_id, author_id, parent_id, body, like_count, dislike_count, reply_count,
			is_reply, is_deleted, created_at, updated_at, pinned_at IS NOT NULL, hearted_at IS NOT NULL
		FROM comments
		WHERE post_id = $1 AND parent_id IS NULL AND is_deleted = FALSE AND created_at < $2 ` + visibilityClause + `
		ORDER BY created_at DESC
		LIMIT $4)
		ORDER BY created_at DESC
	`

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var comments []Comment
	var commentIDs []uuid.UUID
	for rows.Next() {
		var c Comment
		if err := rows.Scan(
			&c.ID, &c.PostID, &c.AuthorID, &c.ParentID, &c.Body,
			&c.LikeCount, &c.DislikeCount, &c.ReplyCount, &c.IsReply, &c.IsDeleted,
			&c.CreatedAt, &c.UpdatedAt, &c.Pinned, &c.HeartedByAuthor,
		); err != nil {
			return nil, err
		}
		comments = append(comments, c)
		commentIDs = append(commentIDs, c.ID)
	}

	// 3. Load inline replies (same moderation filter applied)
	if len(commentIDs) > 0 {
		s.loadInlineReplies(ctx, comments, commentIDs, viewerID)
	}
	if err := s.hydrateCommentReactions(ctx, comments, viewerID); err != nil {
		return nil, err
	}

	return comments, nil
}

// loadInlineReplies fetches and attaches the FIRST (oldest visible) reply
// per top-level comment, for clients that predate GET /replies. Applies the
// same moderation filter as the parent query: a hidden reply doesn't show
// under its parent.
func (s *Store) loadInlineReplies(ctx context.Context, comments []Comment, commentIDs []uuid.UUID, viewerID *uuid.UUID) {
	args := []interface{}{commentIDs}
	visibilityClause := `AND moderation_status = 'visible'`
	if viewerID != nil {
		args = append(args, *viewerID)
		visibilityClause = `AND (moderation_status = 'visible' OR (moderation_status = 'review' AND author_id = $2))`
	}
	replyRows, err := s.db.Query(ctx, `
		SELECT id, post_id, author_id, parent_id, body, like_count, dislike_count, reply_count,
			is_reply, is_deleted, created_at, updated_at, pinned_at IS NOT NULL, hearted_at IS NOT NULL
		FROM comments
		WHERE parent_id = ANY($1) AND is_deleted = FALSE `+visibilityClause+`
		ORDER BY created_at ASC`,
		args...,
	)
	if err != nil {
		return
	}
	defer replyRows.Close()
	replyMap := make(map[uuid.UUID]*Comment)
	for replyRows.Next() {
		var r Comment
		if err := replyRows.Scan(
			&r.ID, &r.PostID, &r.AuthorID, &r.ParentID, &r.Body,
			&r.LikeCount, &r.DislikeCount, &r.ReplyCount, &r.IsReply, &r.IsDeleted,
			&r.CreatedAt, &r.UpdatedAt, &r.Pinned, &r.HeartedByAuthor,
		); err == nil && r.ParentID != nil {
			// ORDER BY created_at ASC: the first row per parent is the oldest.
			if _, seen := replyMap[*r.ParentID]; !seen {
				replyMap[*r.ParentID] = &r
			}
		}
	}
	for i := range comments {
		if reply, ok := replyMap[comments[i].ID]; ok {
			comments[i].Reply = reply
		}
	}
}

// SoftDeleteComment marks a comment as deleted and decrements the post's comment count.
// Returns the post_id for event publishing.
// SoftDeleteComment marks a comment deleted. counted reports whether the
// comment was in the visible set (and so must leave comment_count); a
// second delete of the same comment answers COMMENT_NOT_FOUND, so a retry
// never decrements twice.
func (s *Store) SoftDeleteComment(ctx context.Context, commentID, userID uuid.UUID) (postID uuid.UUID, counted bool, err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return uuid.Nil, false, err
	}
	defer tx.Rollback(ctx)

	var authorID uuid.UUID
	var status string
	err = tx.QueryRow(ctx, `
		SELECT post_id, author_id, moderation_status FROM comments WHERE id = $1 AND is_deleted = FALSE FOR UPDATE`,
		commentID,
	).Scan(&postID, &authorID, &status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, fmt.Errorf("COMMENT_NOT_FOUND")
		}
		return uuid.Nil, false, err
	}

	if authorID != userID {
		return uuid.Nil, false, fmt.Errorf("NOT_COMMENT_AUTHOR")
	}

	_, err = tx.Exec(ctx, `
		UPDATE comments SET is_deleted = TRUE, updated_at = now() WHERE id = $1`,
		commentID,
	)
	if err != nil {
		return uuid.Nil, false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, false, err
	}

	return postID, status == "visible", nil
}

// EditComment edits a comment's body. Must be within 15 minutes of creation and by the author.
func (s *Store) EditComment(ctx context.Context, commentID, userID uuid.UUID, body string) (uuid.UUID, error) {
	var authorID, postID uuid.UUID
	var createdAt time.Time
	err := s.db.QueryRow(ctx, `
		SELECT author_id, created_at, post_id FROM comments WHERE id = $1 AND is_deleted = FALSE`,
		commentID,
	).Scan(&authorID, &createdAt, &postID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, fmt.Errorf("COMMENT_NOT_FOUND")
		}
		return uuid.Nil, err
	}

	if authorID != userID {
		return uuid.Nil, fmt.Errorf("NOT_COMMENT_AUTHOR")
	}

	if time.Since(createdAt) > 15*time.Minute {
		return uuid.Nil, fmt.Errorf("EDIT_WINDOW_EXPIRED")
	}

	_, err = s.db.Exec(ctx, `
		UPDATE comments SET body = $2, updated_at = now() WHERE id = $1`,
		commentID, body,
	)
	return postID, err
}

// ---------------------------------------------------------------------------
// Tier 2b: comment moderation
// ---------------------------------------------------------------------------

// FlaggedComment is the payload returned by the moderation queue
// endpoint — a comment that's either been reported (flagged_count > 0)
// or already moved out of the public thread.
type FlaggedComment struct {
	ID               uuid.UUID `json:"id"`
	PostID           uuid.UUID `json:"post_id"`
	AuthorID         uuid.UUID `json:"author_id"`
	Body             string    `json:"body"`
	ModerationStatus string    `json:"moderation_status"`
	FlaggedCount     int       `json:"flagged_count"`
	CreatedAt        time.Time `json:"created_at"`
}

// IncrementCommentFlaggedCount bumps the report counter and, if the
// counter crosses the auto-review threshold, transitions the comment
// to 'review' status (visible to author + moderator, hidden from
// everyone else). Idempotent — multiple reports stack the count, the
// status flip is one-shot via the WHERE clause.
const commentAutoReviewThreshold = 3

// IncrementCommentFlaggedCount bumps the report counter and, at the
// auto-review threshold, moves a visible comment to 'review'. It returns
// the post and whether that flip happened, so the caller can take the
// comment out of comment_count exactly once.
func (s *Store) IncrementCommentFlaggedCount(ctx context.Context, commentID uuid.UUID) (uuid.UUID, bool, error) {
	var postID uuid.UUID
	var before, after string
	err := s.db.QueryRow(ctx, `
		UPDATE comments c
		SET flagged_count = c.flagged_count + 1,
		    moderation_status = CASE
		        WHEN c.moderation_status = 'visible' AND c.flagged_count + 1 >= $2
		        THEN 'review'
		        ELSE c.moderation_status
		    END
		FROM (SELECT id, moderation_status AS before FROM comments WHERE id = $1 FOR UPDATE) prev
		WHERE c.id = prev.id
		RETURNING c.post_id, prev.before, c.moderation_status
	`, commentID, commentAutoReviewThreshold).Scan(&postID, &before, &after)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, nil
		}
		return uuid.Nil, false, err
	}
	return postID, before == "visible" && after == "review", nil
}

// SetCommentModerationStatus is the admin's override. Status must be
// one of visible / hidden / removed / review. Returns
// COMMENT_NOT_FOUND if the row doesn't exist.
//
// actor is the acting human (required). The change and one post_admin_audit
// row (comment.moderate, previous → new status) commit together.
func (s *Store) SetCommentModerationStatus(ctx context.Context, actor, commentID uuid.UUID, status string) (postID uuid.UUID, previous string, err error) {
	switch status {
	case "visible", "hidden", "removed", "review":
	default:
		return uuid.Nil, "", fmt.Errorf("INVALID_MODERATION_STATUS: %q", status)
	}
	if actor == uuid.Nil {
		return uuid.Nil, "", ErrAdminAuditActor
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return uuid.Nil, "", err
	}
	defer tx.Rollback(ctx)
	if err := tx.QueryRow(ctx, `SELECT moderation_status, post_id FROM comments WHERE id = $1 FOR UPDATE`, commentID).Scan(&previous, &postID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, "", fmt.Errorf("COMMENT_NOT_FOUND")
		}
		return uuid.Nil, "", err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE comments SET moderation_status = $2, updated_at = NOW()
		WHERE id = $1
	`, commentID, status); err != nil {
		return uuid.Nil, "", err
	}
	if err := insertAdminAudit(ctx, tx, actor, "comment.moderate", "comment", commentID, previous, status, ""); err != nil {
		return uuid.Nil, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, "", err
	}
	return postID, previous, nil
}

// ListFlaggedComments returns the moderation queue: comments with
// flagged_count > 0 OR moderation_status in (hidden, removed, review),
// newest first. Bounded so a backlog can't take down the admin UI.
//
// status="all" (default) returns everything; status can also be
// specifically "review", "hidden", "removed", or "flagged" (only
// visible-but-flagged-count-positive). Cursor is the created_at of
// the last seen row.
func (s *Store) ListFlaggedComments(ctx context.Context, status string, cursor time.Time, limit int) ([]FlaggedComment, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if cursor.IsZero() {
		cursor = time.Now()
	}
	var query string
	switch status {
	case "review":
		query = `WHERE moderation_status = 'review'`
	case "hidden":
		query = `WHERE moderation_status = 'hidden'`
	case "removed":
		query = `WHERE moderation_status = 'removed'`
	case "flagged":
		query = `WHERE moderation_status = 'visible' AND flagged_count > 0`
	default:
		// Parens are load-bearing: AND binds tighter than OR, so without
		// them the cursor predicate would only apply to the second OR
		// branch.
		query = `WHERE (moderation_status IN ('hidden','removed','review') OR flagged_count > 0)`
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, post_id, author_id, body, moderation_status, flagged_count, created_at
		FROM comments `+query+`
		AND created_at < $1
		AND is_deleted = FALSE
		ORDER BY created_at DESC
		LIMIT $2
	`, cursor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FlaggedComment
	for rows.Next() {
		var c FlaggedComment
		if err := rows.Scan(&c.ID, &c.PostID, &c.AuthorID, &c.Body,
			&c.ModerationStatus, &c.FlaggedCount, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ErrIdempotencyKeyReused reports the same (actor, post, client key) arriving
// with a different payload. Replaying the stored comment would show the caller
// a result for content they did not send.
var ErrIdempotencyKeyReused = errors.New("idempotency key reused with a different payload")

// CreateCommentIdempotent inserts a comment and its idempotency record in ONE
// transaction, making exactly-once a property of PostgreSQL rather than of a
// cache.
//
// # WHY THIS EXISTS
//
// The Redis middleware claims a key, runs the handler, and only then records
// the result. The comment is COMMITTED in the middle of that sequence, so a
// process death — or a failed Redis write, which the middleware deliberately
// resolves by deleting the record — leaves the insert done and no evidence it
// happened. The client's retry with the same key then inserts a duplicate,
// which is the exact failure idempotency exists to prevent.
//
// Here the evidence and the insert commit together or not at all. Redis
// remains valuable as a fast concurrency gate; it is no longer the authority.
//
// Returns replayed=true when the intent had already been recorded, in which
// case the previously created comment is returned unchanged.
func (s *Store) CreateCommentIdempotent(
	ctx context.Context,
	postID, authorID uuid.UUID,
	body, clientKey, fingerprint string,
) (comment *Comment, replayed bool, err error) {
	// No key means no promise; behave exactly as before.
	if clientKey == "" {
		c, err := s.CreateComment(ctx, postID, authorID, body)
		return c, false, err
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)

	created := &Comment{
		ID:        uuid.New(),
		PostID:    postID,
		AuthorID:  authorID,
		Body:      body,
		IsReply:   false,
		Reactions: []CommentReaction{},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Claim the intent first. ON CONFLICT DO NOTHING makes the winner the row
	// that inserts; everyone else takes the replay path below.
	tag, err := tx.Exec(ctx, `
		INSERT INTO comment_idempotency (actor_id, post_id, client_key, fingerprint, comment_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (actor_id, post_id, client_key) DO NOTHING`,
		authorID, postID, clientKey, fingerprint, created.ID,
	)
	if err != nil {
		return nil, false, fmt.Errorf("claim comment idempotency: %w", err)
	}

	if tag.RowsAffected() == 0 {
		// Someone already owns this intent. Roll back and answer from the
		// record rather than inserting a second comment.
		_ = tx.Rollback(ctx)
		return s.replayComment(ctx, postID, authorID, clientKey, fingerprint)
	}

	if _, err = tx.Exec(ctx, `
		INSERT INTO comments (id, post_id, author_id, body, is_reply, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		created.ID, created.PostID, created.AuthorID, created.Body,
		created.IsReply, created.CreatedAt, created.UpdatedAt,
	); err != nil {
		return nil, false, fmt.Errorf("insert comment: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return created, false, nil
}

// replayComment answers a repeated intent from the committed record.
func (s *Store) replayComment(
	ctx context.Context,
	postID, authorID uuid.UUID,
	clientKey, fingerprint string,
) (*Comment, bool, error) {
	var storedFingerprint string
	var commentID uuid.UUID
	if err := s.db.QueryRow(ctx, `
		SELECT fingerprint, comment_id FROM comment_idempotency
		WHERE actor_id = $1 AND post_id = $2 AND client_key = $3`,
		authorID, postID, clientKey,
	).Scan(&storedFingerprint, &commentID); err != nil {
		return nil, false, fmt.Errorf("read comment idempotency: %w", err)
	}

	if storedFingerprint != fingerprint {
		return nil, false, ErrIdempotencyKeyReused
	}

	c := &Comment{}
	if err := s.db.QueryRow(ctx, `
		SELECT id, post_id, author_id, body, is_reply, like_count, dislike_count,
		       reply_count, created_at, updated_at
		FROM comments WHERE id = $1`, commentID,
	).Scan(&c.ID, &c.PostID, &c.AuthorID, &c.Body, &c.IsReply, &c.LikeCount,
		&c.DislikeCount, &c.ReplyCount, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, false, fmt.Errorf("load replayed comment: %w", err)
	}
	page := []Comment{*c}
	if err := s.hydrateCommentReactions(ctx, page, &authorID); err != nil {
		return nil, false, err
	}
	return &page[0], true, nil
}
