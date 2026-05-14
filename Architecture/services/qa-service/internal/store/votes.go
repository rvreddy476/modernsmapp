package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Audit HQ2: previously every vote/unvote ran 3 COUNT(*) subqueries
// over the full vote table to recompute vote_score / upvote_count /
// downvote_count for the target. On a hot question with 10k votes,
// every new vote cost O(10k) DB work. Now we read the prior vote (if
// any), compute the score/up/down deltas in-app, and apply them with
// a single arithmetic UPDATE — O(1) per vote regardless of total
// votes on the target.

// voteDelta returns (score_delta, up_delta, down_delta) for a transition
// from oldVote -> newVote. Either side may be empty ("") meaning no
// vote on that side. up_delta / down_delta are -1/0/+1; score_delta is
// -2/-1/0/+1/+2 to cover flips.
func voteDelta(oldVote, newVote string) (int, int, int) {
	scoreOf := map[string]int{"up": 1, "down": -1, "": 0}
	upOf := map[string]int{"up": 1, "down": 0, "": 0}
	downOf := map[string]int{"up": 0, "down": 1, "": 0}
	return scoreOf[newVote] - scoreOf[oldVote],
		upOf[newVote] - upOf[oldVote],
		downOf[newVote] - downOf[oldVote]
}

// readAndUpsertVote reads any existing vote, then UPSERTs the new one,
// returning the previous vote_type ("" if no prior vote). Caller uses
// the result to compute and apply a counter delta.
func readAndUpsertVote(ctx context.Context, tx pgx.Tx, table, userCol, targetCol string, userID, targetID uuid.UUID, voteType string) (string, error) {
	var existing string
	err := tx.QueryRow(ctx,
		`SELECT vote_type FROM `+table+` WHERE `+userCol+` = $1 AND `+targetCol+` = $2`,
		userID, targetID).Scan(&existing)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO `+table+` (`+userCol+`, `+targetCol+`, vote_type) VALUES ($1, $2, $3)
		 ON CONFLICT (`+userCol+`, `+targetCol+`) DO UPDATE SET vote_type = EXCLUDED.vote_type`,
		userID, targetID, voteType)
	return existing, err
}

func (s *Store) VoteQuestion(ctx context.Context, userID, questionID uuid.UUID, voteType string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	old, err := readAndUpsertVote(ctx, tx, "question_votes", "user_id", "question_id", userID, questionID, voteType)
	if err != nil {
		return err
	}
	ds, du, dd := voteDelta(old, voteType)
	if ds != 0 || du != 0 || dd != 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE questions SET vote_score = vote_score + $2, upvote_count = upvote_count + $3, downvote_count = downvote_count + $4 WHERE id = $1`,
			questionID, ds, du, dd); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) RemoveQuestionVote(ctx context.Context, userID, questionID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var existing string
	err = tx.QueryRow(ctx, `DELETE FROM question_votes WHERE user_id = $1 AND question_id = $2 RETURNING vote_type`, userID, questionID).Scan(&existing)
	if errors.Is(err, pgx.ErrNoRows) {
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	ds, du, dd := voteDelta(existing, "")
	if _, err := tx.Exec(ctx,
		`UPDATE questions SET vote_score = vote_score + $2, upvote_count = upvote_count + $3, downvote_count = downvote_count + $4 WHERE id = $1`,
		questionID, ds, du, dd); err != nil {
		return err
	}
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
	old, err := readAndUpsertVote(ctx, tx, "answer_votes", "user_id", "answer_id", userID, answerID, voteType)
	if err != nil {
		return err
	}
	ds, du, dd := voteDelta(old, voteType)
	if ds != 0 || du != 0 || dd != 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE answers SET vote_score = vote_score + $2, upvote_count = upvote_count + $3, downvote_count = downvote_count + $4 WHERE id = $1`,
			answerID, ds, du, dd); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) RemoveAnswerVote(ctx context.Context, userID, answerID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var existing string
	err = tx.QueryRow(ctx, `DELETE FROM answer_votes WHERE user_id = $1 AND answer_id = $2 RETURNING vote_type`, userID, answerID).Scan(&existing)
	if errors.Is(err, pgx.ErrNoRows) {
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	ds, du, dd := voteDelta(existing, "")
	if _, err := tx.Exec(ctx,
		`UPDATE answers SET vote_score = vote_score + $2, upvote_count = upvote_count + $3, downvote_count = downvote_count + $4 WHERE id = $1`,
		answerID, ds, du, dd); err != nil {
		return err
	}
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
	old, err := readAndUpsertVote(ctx, tx, "answer_comment_votes", "user_id", "comment_id", userID, commentID, voteType)
	if err != nil {
		return err
	}
	ds, _, _ := voteDelta(old, voteType)
	if ds != 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE answer_comments SET vote_score = vote_score + $2 WHERE id = $1`, commentID, ds); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) RemoveCommentVote(ctx context.Context, userID, commentID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var existing string
	err = tx.QueryRow(ctx, `DELETE FROM answer_comment_votes WHERE user_id = $1 AND comment_id = $2 RETURNING vote_type`, userID, commentID).Scan(&existing)
	if errors.Is(err, pgx.ErrNoRows) {
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	ds, _, _ := voteDelta(existing, "")
	if _, err := tx.Exec(ctx,
		`UPDATE answer_comments SET vote_score = vote_score + $2 WHERE id = $1`, commentID, ds); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
