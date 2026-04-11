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

func (s *Store) UpdateComment(ctx context.Context, commentID uuid.UUID, body string) (*AnswerComment, error) {
	c := &AnswerComment{}
	err := s.db.QueryRow(ctx, `
		UPDATE answer_comments SET body = $2, updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING id, answer_id, author_id, body, vote_score, created_at, updated_at`,
		commentID, body,
	).Scan(&c.ID, &c.AnswerID, &c.AuthorID, &c.Body, &c.VoteScore, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("update comment: %w", err)
	}
	return c, nil
}

func (s *Store) DeleteComment(ctx context.Context, commentID uuid.UUID) error {
	var answerID uuid.UUID
	err := s.db.QueryRow(ctx, `SELECT answer_id FROM answer_comments WHERE id = $1`, commentID).Scan(&answerID)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, _ = tx.Exec(ctx, `UPDATE answer_comments SET deleted_at = now() WHERE id = $1`, commentID)
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
