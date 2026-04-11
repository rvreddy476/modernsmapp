package store

import (
	"context"

	"github.com/google/uuid"
)

func (s *Store) SaveQuestion(ctx context.Context, userID, questionID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `INSERT INTO question_saves (user_id, question_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, userID, questionID)
	return err
}

func (s *Store) UnsaveQuestion(ctx context.Context, userID, questionID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM question_saves WHERE user_id = $1 AND question_id = $2`, userID, questionID)
	return err
}

func (s *Store) IsSavedQuestion(ctx context.Context, userID, questionID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM question_saves WHERE user_id = $1 AND question_id = $2)`, userID, questionID).Scan(&exists)
	return exists, err
}

func (s *Store) GetSavedQuestions(ctx context.Context, userID uuid.UUID, limit, offset int) ([]QuestionSummary, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT q.id, q.author_id, q.title, q.slug, q.status, q.vote_score, q.answer_count, q.view_count, q.is_answered, q.created_at
		FROM questions q JOIN question_saves qs ON q.id = qs.question_id
		WHERE qs.user_id = $1 AND q.deleted_at IS NULL
		ORDER BY qs.created_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanQuestionSummaries(rows)
}

func (s *Store) SaveAnswer(ctx context.Context, userID, answerID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `INSERT INTO answer_saves (user_id, answer_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, userID, answerID)
	return err
}

func (s *Store) UnsaveAnswer(ctx context.Context, userID, answerID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM answer_saves WHERE user_id = $1 AND answer_id = $2`, userID, answerID)
	return err
}

func (s *Store) GetSavedAnswers(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Answer, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT a.id, a.question_id, a.author_id, a.body, a.body_html, a.vote_score, a.upvote_count, a.downvote_count,
		       a.is_best, a.is_accepted, a.comment_count, a.reference_count, a.created_at, a.updated_at
		FROM answers a JOIN answer_saves sa ON a.id = sa.answer_id
		WHERE sa.user_id = $1 AND a.deleted_at IS NULL
		ORDER BY sa.created_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAnswers(rows)
}
