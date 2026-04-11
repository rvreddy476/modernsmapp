package store

import (
	"context"

	"github.com/google/uuid"
)

func (s *Store) VoteQuestion(ctx context.Context, userID, questionID uuid.UUID, voteType string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, _ = tx.Exec(ctx, `DELETE FROM question_votes WHERE user_id = $1 AND question_id = $2`, userID, questionID)
	_, err = tx.Exec(ctx, `INSERT INTO question_votes (user_id, question_id, vote_type) VALUES ($1, $2, $3)`, userID, questionID, voteType)
	if err != nil {
		return err
	}
	_, _ = tx.Exec(ctx, `
		UPDATE questions SET
			vote_score = (SELECT COALESCE(SUM(CASE WHEN vote_type='up' THEN 1 ELSE -1 END), 0) FROM question_votes WHERE question_id = $1),
			upvote_count = (SELECT count(*) FROM question_votes WHERE question_id = $1 AND vote_type = 'up'),
			downvote_count = (SELECT count(*) FROM question_votes WHERE question_id = $1 AND vote_type = 'down')
		WHERE id = $1`, questionID)
	return tx.Commit(ctx)
}

func (s *Store) RemoveQuestionVote(ctx context.Context, userID, questionID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, _ = tx.Exec(ctx, `DELETE FROM question_votes WHERE user_id = $1 AND question_id = $2`, userID, questionID)
	_, _ = tx.Exec(ctx, `
		UPDATE questions SET
			vote_score = (SELECT COALESCE(SUM(CASE WHEN vote_type='up' THEN 1 ELSE -1 END), 0) FROM question_votes WHERE question_id = $1),
			upvote_count = (SELECT count(*) FROM question_votes WHERE question_id = $1 AND vote_type = 'up'),
			downvote_count = (SELECT count(*) FROM question_votes WHERE question_id = $1 AND vote_type = 'down')
		WHERE id = $1`, questionID)
	return tx.Commit(ctx)
}

func (s *Store) GetUserQuestionVote(ctx context.Context, userID, questionID uuid.UUID) (*string, error) {
	var voteType string
	err := s.db.QueryRow(ctx, `SELECT vote_type FROM question_votes WHERE user_id = $1 AND question_id = $2`, userID, questionID).Scan(&voteType)
	if err != nil {
		return nil, nil
	}
	return &voteType, nil
}

func (s *Store) VoteAnswer(ctx context.Context, userID, answerID uuid.UUID, voteType string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, _ = tx.Exec(ctx, `DELETE FROM answer_votes WHERE user_id = $1 AND answer_id = $2`, userID, answerID)
	_, err = tx.Exec(ctx, `INSERT INTO answer_votes (user_id, answer_id, vote_type) VALUES ($1, $2, $3)`, userID, answerID, voteType)
	if err != nil {
		return err
	}
	_, _ = tx.Exec(ctx, `
		UPDATE answers SET
			vote_score = (SELECT COALESCE(SUM(CASE WHEN vote_type='up' THEN 1 ELSE -1 END), 0) FROM answer_votes WHERE answer_id = $1),
			upvote_count = (SELECT count(*) FROM answer_votes WHERE answer_id = $1 AND vote_type = 'up'),
			downvote_count = (SELECT count(*) FROM answer_votes WHERE answer_id = $1 AND vote_type = 'down')
		WHERE id = $1`, answerID)
	return tx.Commit(ctx)
}

func (s *Store) RemoveAnswerVote(ctx context.Context, userID, answerID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, _ = tx.Exec(ctx, `DELETE FROM answer_votes WHERE user_id = $1 AND answer_id = $2`, userID, answerID)
	_, _ = tx.Exec(ctx, `
		UPDATE answers SET
			vote_score = (SELECT COALESCE(SUM(CASE WHEN vote_type='up' THEN 1 ELSE -1 END), 0) FROM answer_votes WHERE answer_id = $1),
			upvote_count = (SELECT count(*) FROM answer_votes WHERE answer_id = $1 AND vote_type = 'up'),
			downvote_count = (SELECT count(*) FROM answer_votes WHERE answer_id = $1 AND vote_type = 'down')
		WHERE id = $1`, answerID)
	return tx.Commit(ctx)
}

func (s *Store) GetUserAnswerVote(ctx context.Context, userID, answerID uuid.UUID) (*string, error) {
	var voteType string
	err := s.db.QueryRow(ctx, `SELECT vote_type FROM answer_votes WHERE user_id = $1 AND answer_id = $2`, userID, answerID).Scan(&voteType)
	if err != nil {
		return nil, nil
	}
	return &voteType, nil
}

func (s *Store) VoteComment(ctx context.Context, userID, commentID uuid.UUID, voteType string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, _ = tx.Exec(ctx, `DELETE FROM answer_comment_votes WHERE user_id = $1 AND comment_id = $2`, userID, commentID)
	_, err = tx.Exec(ctx, `INSERT INTO answer_comment_votes (user_id, comment_id, vote_type) VALUES ($1, $2, $3)`, userID, commentID, voteType)
	if err != nil {
		return err
	}
	_, _ = tx.Exec(ctx, `
		UPDATE answer_comments SET
			vote_score = (SELECT COALESCE(SUM(CASE WHEN vote_type='up' THEN 1 ELSE -1 END), 0) FROM answer_comment_votes WHERE comment_id = $1)
		WHERE id = $1`, commentID)
	return tx.Commit(ctx)
}

func (s *Store) RemoveCommentVote(ctx context.Context, userID, commentID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, _ = tx.Exec(ctx, `DELETE FROM answer_comment_votes WHERE user_id = $1 AND comment_id = $2`, userID, commentID)
	_, _ = tx.Exec(ctx, `
		UPDATE answer_comments SET
			vote_score = (SELECT COALESCE(SUM(CASE WHEN vote_type='up' THEN 1 ELSE -1 END), 0) FROM answer_comment_votes WHERE comment_id = $1)
		WHERE id = $1`, commentID)
	return tx.Commit(ctx)
}
