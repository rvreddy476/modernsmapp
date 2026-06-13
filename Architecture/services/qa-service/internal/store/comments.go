package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

func (s *Store) CreateComment(ctx context.Context, answerID, authorID uuid.UUID, body string) (*AnswerComment, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	c := &AnswerComment{}
	err = tx.QueryRow(ctx, `
		INSERT INTO answer_comments (answer_id, author_id, body)
		VALUES ($1, $2, $3)
		RETURNING id, answer_id, author_id, body, vote_score, created_at, updated_at`,
		answerID, authorID, body,
	).Scan(&c.ID, &c.AnswerID, &c.AuthorID, &c.Body, &c.VoteScore, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert comment: %w", err)
	}

	_, _ = tx.Exec(ctx, `UPDATE answers SET comment_count = comment_count + 1 WHERE id = $1`, answerID)
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

// GetComment returns the comment row used by the handler for the
// author-ownership check (audit CQ5).
func (s *Store) GetComment(ctx context.Context, commentID uuid.UUID) (*AnswerComment, error) {
	c := &AnswerComment{}
	err := s.db.QueryRow(ctx, `
		SELECT id, answer_id, author_id, body, vote_score, created_at, updated_at
		FROM answer_comments WHERE id = $1 AND deleted_at IS NULL`,
		commentID,
	).Scan(&c.ID, &c.AnswerID, &c.AuthorID, &c.Body, &c.VoteScore, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// UpdateComment now requires the actor be the comment author. The
// service layer pre-validates ownership; this gate is also encoded in
// the SQL so that any future caller that forgets the service check
// still fails closed.
func (s *Store) UpdateComment(ctx context.Context, commentID, authorID uuid.UUID, body string) (*AnswerComment, error) {
	c := &AnswerComment{}
	err := s.db.QueryRow(ctx, `
		UPDATE answer_comments SET body = $3, updated_at = now()
		WHERE id = $1 AND author_id = $2 AND deleted_at IS NULL
		RETURNING id, answer_id, author_id, body, vote_score, created_at, updated_at`,
		commentID, authorID, body,
	).Scan(&c.ID, &c.AnswerID, &c.AuthorID, &c.Body, &c.VoteScore, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("update comment: %w", err)
	}
	return c, nil
}

// DeleteComment now requires the actor be the comment author.
func (s *Store) DeleteComment(ctx context.Context, commentID, authorID uuid.UUID) error {
	var answerID uuid.UUID
	err := s.db.QueryRow(ctx,
		`SELECT answer_id FROM answer_comments WHERE id = $1 AND author_id = $2 AND deleted_at IS NULL`,
		commentID, authorID).Scan(&answerID)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, _ = tx.Exec(ctx, `UPDATE answer_comments SET deleted_at = now() WHERE id = $1 AND author_id = $2`, commentID, authorID)
	_, _ = tx.Exec(ctx, `UPDATE answers SET comment_count = GREATEST(comment_count - 1, 0) WHERE id = $1`, answerID)
	return tx.Commit(ctx)
}

func (s *Store) ListCommentsByAnswer(ctx context.Context, answerID uuid.UUID, limit, offset int) ([]AnswerComment, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, answer_id, author_id, body, vote_score, created_at, updated_at
		FROM answer_comments WHERE answer_id = $1 AND deleted_at IS NULL
		ORDER BY created_at ASC LIMIT $2 OFFSET $3`, answerID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []AnswerComment
	for rows.Next() {
		var c AnswerComment
		if err := rows.Scan(&c.ID, &c.AnswerID, &c.AuthorID, &c.Body, &c.VoteScore, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		results = append(results, c)
	}
	return results, nil
}
