package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type GroupPostComment struct {
	ID         uuid.UUID  `json:"id"`
	PostID     uuid.UUID  `json:"post_id"`
	UserID     string     `json:"user_id"`
	Body       string     `json:"body"`
	ParentID   *uuid.UUID `json:"parent_id,omitempty"`
	IsPinned   bool       `json:"is_pinned"`
	SparkCount int        `json:"spark_count"`
	CreatedAt  time.Time  `json:"created_at"`
	// Set by the server when the commenter is the author of an anonymous
	// post. Never a client's choice — see anonymous.go.
	IsAnonymous bool       `json:"is_anonymous"`
	AnonAlias   *uuid.UUID `json:"-"`
}

func (s *Store) ListGroupPostComments(ctx context.Context, postID uuid.UUID, limit, offset int) ([]GroupPostComment, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx,
		// An anonymous author's own comments carry the POST's alias on the
		// wire (GroupPostComment.MarshalJSON); the alias is read from the post
		// so a comment never stores a second pseudonym that could drift.
		`SELECT c.id, c.post_id, c.user_id, c.body, c.parent_id, c.is_pinned, c.spark_count, c.created_at,
		        c.is_anonymous, CASE WHEN c.is_anonymous THEN p.anon_alias END
		FROM group_post_comments c
		JOIN group_posts p ON p.id = c.post_id
		WHERE c.post_id = $1
		ORDER BY c.is_pinned DESC, c.created_at ASC
		LIMIT $2 OFFSET $3`,
		postID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var comments []GroupPostComment
	for rows.Next() {
		var c GroupPostComment
		if err := rows.Scan(&c.ID, &c.PostID, &c.UserID, &c.Body, &c.ParentID,
			&c.IsPinned, &c.SparkCount, &c.CreatedAt, &c.IsAnonymous, &c.AnonAlias); err != nil {
			return nil, err
		}
		comments = append(comments, c)
	}
	return comments, rows.Err()
}

// AddGroupPostComment writes a comment. isAnonymous is set by the service
// when the commenter is the anonymous author of the post (never by a
// client); alias is then the post's alias, carried on the returned wire.
func (s *Store) AddGroupPostComment(ctx context.Context, postID uuid.UUID, userID, body string, parentID *uuid.UUID, isAnonymous bool, alias *uuid.UUID) (*GroupPostComment, error) {
	c := &GroupPostComment{
		ID:          uuid.New(),
		PostID:      postID,
		UserID:      userID,
		Body:        body,
		ParentID:    parentID,
		IsAnonymous: isAnonymous,
		AnonAlias:   alias,
	}
	query := `INSERT INTO group_post_comments (id, post_id, user_id, body, parent_id, is_pinned, spark_count, is_anonymous, created_at)
		VALUES ($1, $2, $3, $4, $5, false, 0, $6, NOW()) RETURNING created_at`
	if err := s.db.QueryRow(ctx, query, c.ID, postID, userID, body, parentID, isAnonymous).Scan(&c.CreatedAt); err != nil {
		return nil, err
	}
	// Increment comment_count asynchronously — caller doesn't need to wait for this
	go func() {
		_, _ = s.db.Exec(context.Background(),
			`UPDATE group_posts SET comment_count = comment_count + 1, updated_at = NOW() WHERE id = $1`, postID)
	}()
	return c, nil
}

// DeleteGroupPostComment deletes commentID only if it is a comment of postID
// and postID is a post of groupID. The caller decides privilege on the PATH
// group, so the delete must be scoped to that group too: matching on the
// comment id alone let a moderator of one group delete any group's comment
// (G-M3).
func (s *Store) DeleteGroupPostComment(ctx context.Context, groupID, postID, commentID uuid.UUID, userID string, isPrivileged bool) error {
	query := `DELETE FROM group_post_comments c
		USING group_posts p
		WHERE c.id = $1 AND c.post_id = $2 AND p.id = c.post_id AND p.group_id = $3
		  AND ($4::boolean OR c.user_id = $5)
		RETURNING c.post_id`
	args := []any{commentID, postID, groupID, isPrivileged, userID}
	var deletedFrom uuid.UUID
	err := s.db.QueryRow(ctx, query, args...).Scan(&deletedFrom)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("not found: comment not found or not authorized")
	}
	if err != nil {
		return err
	}
	_, _ = s.db.Exec(ctx,
		`UPDATE group_posts SET comment_count = GREATEST(comment_count - 1, 0), updated_at = NOW() WHERE id = $1`, deletedFrom)
	return nil
}
