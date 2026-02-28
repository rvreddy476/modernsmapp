package postgres

import (
	"context"
	"errors"
	"fmt"
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
	// Nested reply (loaded inline for threaded display)
	Reply *Comment `json:"reply,omitempty"`
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

// IncrementCommentLikeCount atomically increments or decrements a comment's like_count.
func (s *Store) IncrementCommentLikeCount(ctx context.Context, commentID uuid.UUID, delta int) error {
	_, err := s.db.Exec(ctx,
		`UPDATE comments SET like_count = GREATEST(0, like_count + $1) WHERE id = $2`,
		delta, commentID,
	)
	return err
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

	// Increment post engagement comment count
	_, err = tx.Exec(ctx, `
		INSERT INTO post_engagement_counts (post_id, comment_count)
		VALUES ($1, 1)
		ON CONFLICT (post_id) DO UPDATE SET comment_count = post_engagement_counts.comment_count + 1, updated_at = now()`,
		postID,
	)
	if err != nil {
		return nil, fmt.Errorf("update comment count: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return comment, nil
}

// CreateReply creates a reply to a comment. Enforces:
// 1. Only the post owner can reply
// 2. Max 1 reply per comment
// 3. Cannot reply to a reply (max depth = 2)
func (s *Store) CreateReply(ctx context.Context, commentID, userID uuid.UUID, body string) (*Comment, uuid.UUID, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, uuid.Nil, err
	}
	defer tx.Rollback(ctx)

	// Load parent comment
	var parentPostID, parentAuthorID uuid.UUID
	var parentIsReply bool
	var parentReplyCount int
	err = tx.QueryRow(ctx, `
		SELECT post_id, author_id, is_reply, reply_count
		FROM comments WHERE id = $1 AND is_deleted = FALSE`,
		commentID,
	).Scan(&parentPostID, &parentAuthorID, &parentIsReply, &parentReplyCount)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, uuid.Nil, fmt.Errorf("COMMENT_NOT_FOUND")
		}
		return nil, uuid.Nil, err
	}

	// Cannot reply to a reply
	if parentIsReply {
		return nil, uuid.Nil, fmt.Errorf("CANNOT_REPLY_TO_REPLY")
	}

	// Max 1 reply per comment
	if parentReplyCount >= 1 {
		return nil, uuid.Nil, fmt.Errorf("REPLY_EXISTS")
	}

	// Only post owner can reply
	var postAuthorID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT author_id FROM posts WHERE id = $1`, parentPostID).Scan(&postAuthorID)
	if err != nil {
		return nil, uuid.Nil, err
	}
	if userID != postAuthorID {
		return nil, uuid.Nil, fmt.Errorf("REPLY_OWNER_ONLY")
	}

	reply := &Comment{
		ID:        uuid.New(),
		PostID:    parentPostID,
		AuthorID:  userID,
		ParentID:  &commentID,
		Body:      body,
		IsReply:   true,
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

	// Update parent's reply_count
	_, err = tx.Exec(ctx, `
		UPDATE comments SET reply_count = reply_count + 1, updated_at = now()
		WHERE id = $1`, commentID)
	if err != nil {
		return nil, uuid.Nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, uuid.Nil, err
	}

	// Return parentAuthorID so the service can notify the comment author about the reply
	return reply, parentAuthorID, nil
}

// ListComments returns paginated top-level comments with their inline replies.
func (s *Store) ListComments(ctx context.Context, postID uuid.UUID, cursor string, limit int) ([]Comment, string, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	var args []interface{}
	args = append(args, postID, limit+1)

	query := `SELECT id, post_id, author_id, parent_id, body, like_count, dislike_count, reply_count,
		is_reply, is_deleted, created_at, updated_at
		FROM comments
		WHERE post_id = $1 AND parent_id IS NULL AND is_deleted = FALSE`

	if cursor != "" {
		cursorTime, err := time.Parse(time.RFC3339Nano, cursor)
		if err == nil {
			query += ` AND created_at < $3`
			args = append(args, cursorTime)
		}
	}

	query += ` ORDER BY created_at DESC LIMIT $2`

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
			&c.CreatedAt, &c.UpdatedAt,
		); err != nil {
			return nil, "", err
		}
		comments = append(comments, c)
		commentIDs = append(commentIDs, c.ID)
	}

	var nextCursor string
	if len(comments) > limit {
		nextCursor = comments[limit-1].CreatedAt.Format(time.RFC3339Nano)
		comments = comments[:limit]
		commentIDs = commentIDs[:limit]
	}

	// Load inline replies for these comments
	if len(commentIDs) > 0 {
		replyRows, err := s.db.Query(ctx, `
			SELECT id, post_id, author_id, parent_id, body, like_count, dislike_count, reply_count,
				is_reply, is_deleted, created_at, updated_at
			FROM comments
			WHERE parent_id = ANY($1) AND is_deleted = FALSE
			ORDER BY created_at ASC`,
			commentIDs,
		)
		if err == nil {
			defer replyRows.Close()
			replyMap := make(map[uuid.UUID]*Comment)
			for replyRows.Next() {
				var r Comment
				if err := replyRows.Scan(
					&r.ID, &r.PostID, &r.AuthorID, &r.ParentID, &r.Body,
					&r.LikeCount, &r.DislikeCount, &r.ReplyCount, &r.IsReply, &r.IsDeleted,
					&r.CreatedAt, &r.UpdatedAt,
				); err == nil && r.ParentID != nil {
					replyMap[*r.ParentID] = &r
				}
			}
			for i := range comments {
				if reply, ok := replyMap[comments[i].ID]; ok {
					comments[i].Reply = reply
				}
			}
		}
	}

	return comments, nextCursor, nil
}

// GetCommentsAround returns top-level comments surrounding a target comment.
// It fetches half the limit before and half after the target, centering the result.
func (s *Store) GetCommentsAround(ctx context.Context, postID, commentID uuid.UUID, limit int) ([]Comment, error) {
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
	query := `
		(SELECT id, post_id, author_id, parent_id, body, like_count, dislike_count, reply_count,
			is_reply, is_deleted, created_at, updated_at
		FROM comments
		WHERE post_id = $1 AND parent_id IS NULL AND is_deleted = FALSE AND created_at >= $2
		ORDER BY created_at ASC
		LIMIT $3)
		UNION ALL
		(SELECT id, post_id, author_id, parent_id, body, like_count, dislike_count, reply_count,
			is_reply, is_deleted, created_at, updated_at
		FROM comments
		WHERE post_id = $1 AND parent_id IS NULL AND is_deleted = FALSE AND created_at < $2
		ORDER BY created_at DESC
		LIMIT $4)
		ORDER BY created_at DESC
	`

	rows, err := s.db.Query(ctx, query, postID, targetCreatedAt, half+1, half)
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
			&c.CreatedAt, &c.UpdatedAt,
		); err != nil {
			return nil, err
		}
		comments = append(comments, c)
		commentIDs = append(commentIDs, c.ID)
	}

	// 3. Load inline replies
	if len(commentIDs) > 0 {
		s.loadInlineReplies(ctx, comments, commentIDs)
	}

	return comments, nil
}

// loadInlineReplies fetches and attaches a single reply per top-level comment.
func (s *Store) loadInlineReplies(ctx context.Context, comments []Comment, commentIDs []uuid.UUID) {
	replyRows, err := s.db.Query(ctx, `
		SELECT id, post_id, author_id, parent_id, body, like_count, dislike_count, reply_count,
			is_reply, is_deleted, created_at, updated_at
		FROM comments
		WHERE parent_id = ANY($1) AND is_deleted = FALSE
		ORDER BY created_at ASC`,
		commentIDs,
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
			&r.CreatedAt, &r.UpdatedAt,
		); err == nil && r.ParentID != nil {
			replyMap[*r.ParentID] = &r
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
func (s *Store) SoftDeleteComment(ctx context.Context, commentID, userID uuid.UUID) (uuid.UUID, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)

	var postID uuid.UUID
	var authorID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT post_id, author_id FROM comments WHERE id = $1 AND is_deleted = FALSE`,
		commentID,
	).Scan(&postID, &authorID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, fmt.Errorf("COMMENT_NOT_FOUND")
		}
		return uuid.Nil, err
	}

	if authorID != userID {
		return uuid.Nil, fmt.Errorf("NOT_COMMENT_AUTHOR")
	}

	_, err = tx.Exec(ctx, `
		UPDATE comments SET is_deleted = TRUE, updated_at = now() WHERE id = $1`,
		commentID,
	)
	if err != nil {
		return uuid.Nil, err
	}

	_, err = tx.Exec(ctx, `
		UPDATE post_engagement_counts
		SET comment_count = GREATEST(comment_count - 1, 0), updated_at = now()
		WHERE post_id = $1`, postID)
	if err != nil {
		return uuid.Nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}

	return postID, nil
}

// EditComment edits a comment's body. Must be within 15 minutes of creation and by the author.
func (s *Store) EditComment(ctx context.Context, commentID, userID uuid.UUID, body string) error {
	var authorID uuid.UUID
	var createdAt time.Time
	err := s.db.QueryRow(ctx, `
		SELECT author_id, created_at FROM comments WHERE id = $1 AND is_deleted = FALSE`,
		commentID,
	).Scan(&authorID, &createdAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("COMMENT_NOT_FOUND")
		}
		return err
	}

	if authorID != userID {
		return fmt.Errorf("NOT_COMMENT_AUTHOR")
	}

	if time.Since(createdAt) > 15*time.Minute {
		return fmt.Errorf("EDIT_WINDOW_EXPIRED")
	}

	_, err = s.db.Exec(ctx, `
		UPDATE comments SET body = $2, updated_at = now() WHERE id = $1`,
		commentID, body,
	)
	return err
}
