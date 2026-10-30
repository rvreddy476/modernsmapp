package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Accepted answers (audit H1, M9). Every change runs in one transaction that
// locks the question row first:
//
//   - only the question's author accepts, and only a live answer of THAT
//     question;
//   - a question has at most one accepted answer; accepting the one already
//     accepted changes nothing and pays nothing;
//   - accepting pays the answer's author +25 and the asker +2, and counts a
//     best answer, unless the asker accepted their own answer (then nothing
//     is paid or counted);
//   - unaccepting, accepting another answer, deleting the accepted answer
//     or hiding it reverses exactly what the accept paid.

// BestAnswerResult is what SelectBestAnswer did.
type BestAnswerResult struct {
	// Changed is false when the answer was already the accepted one.
	Changed        bool
	AnswerAuthorID uuid.UUID
}

// acceptPays reports whether accepting an answer pays reputation: never for
// the asker's own answer.
func acceptPays(questionAuthor, answerAuthor uuid.UUID) bool {
	return questionAuthor != answerAuthor
}

// acceptDecision: only the question's author accepts, and only on an open or
// closed question (not one merged or awaiting approval).
func acceptDecision(questionAuthor, selector uuid.UUID, status string) error {
	if questionAuthor != selector {
		return refuse(ErrForbidden, "only the question author can select the best answer")
	}
	if status != "open" && status != "closed" {
		return refuse(ErrConflict, "this question cannot take a best answer")
	}
	return nil
}

// lockQuestionForAnswer reads and locks the question row an accept change
// works on.
func lockQuestionForAnswer(ctx context.Context, tx pgx.Tx, questionID uuid.UUID) (author uuid.UUID, best *uuid.UUID, status string, err error) {
	err = tx.QueryRow(ctx, `
		SELECT author_id, best_answer_id, status FROM questions
		WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, questionID).Scan(&author, &best, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		err = refuse(ErrNotFound, "question not found")
	}
	return
}

func (s *Store) SelectBestAnswer(ctx context.Context, questionID, answerID, selectorID uuid.UUID) (BestAnswerResult, error) {
	var res BestAnswerResult
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qAuthor, best, status, err := lockQuestionForAnswer(ctx, tx, questionID)
	if err != nil {
		return res, err
	}
	if err := acceptDecision(qAuthor, selectorID, status); err != nil {
		return res, err
	}
	err = tx.QueryRow(ctx, `
		SELECT author_id FROM answers
		WHERE id = $1 AND question_id = $2 AND deleted_at IS NULL FOR UPDATE`,
		answerID, questionID).Scan(&res.AnswerAuthorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return res, refuse(ErrInvalid, "answer_id is not an answer to this question")
	}
	if err != nil {
		return res, err
	}
	if best != nil && *best == answerID {
		return res, tx.Commit(ctx) // already accepted: nothing changes, nothing is paid
	}
	if best != nil {
		if err := unacceptTx(ctx, tx, questionID, *best, qAuthor); err != nil {
			return res, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE answers SET is_best = true WHERE id = $1`, answerID); err != nil {
		return res, err
	}
	if _, err := tx.Exec(ctx, `UPDATE questions SET best_answer_id = $2, is_answered = true, updated_at = now() WHERE id = $1`,
		questionID, answerID); err != nil {
		return res, err
	}
	if acceptPays(qAuthor, res.AnswerAuthorID) {
		a, q := answerID, questionID
		if err := addReputationTx(ctx, tx, res.AnswerAuthorID, "best_answer_selected", RepBestAnswerSelected, "answer", &a); err != nil {
			return res, err
		}
		if err := addReputationTx(ctx, tx, qAuthor, "best_answer_selector", RepBestAnswerSelectorBonus, "question", &q); err != nil {
			return res, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO qa_profiles (user_id, best_answer_count) VALUES ($1, 1)
			ON CONFLICT (user_id) DO UPDATE SET best_answer_count = qa_profiles.best_answer_count + 1, updated_at = now()`,
			res.AnswerAuthorID); err != nil {
			return res, err
		}
	}
	res.Changed = true
	return res, tx.Commit(ctx)
}

// UnselectBestAnswer clears the question's accepted answer and reverses what
// accepting it paid. No accepted answer is a no-op (false).
func (s *Store) UnselectBestAnswer(ctx context.Context, questionID, actorID uuid.UUID) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qAuthor, best, _, err := lockQuestionForAnswer(ctx, tx, questionID)
	if err != nil {
		return false, err
	}
	if qAuthor != actorID {
		return false, refuse(ErrForbidden, "only the question author can unselect a best answer")
	}
	if best == nil {
		return false, tx.Commit(ctx)
	}
	if err := unacceptTx(ctx, tx, questionID, *best, qAuthor); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// releaseBestAnswerTx clears answerID as questionID's accepted answer if it
// is one, reversing what the accept paid (a deleted or hidden accepted
// answer, M9). The question row must not be locked by a statement that
// expects it unchanged; this takes its lock.
func releaseBestAnswerTx(ctx context.Context, tx pgx.Tx, questionID, answerID uuid.UUID) error {
	var qAuthor uuid.UUID
	var best *uuid.UUID
	err := tx.QueryRow(ctx, `SELECT author_id, best_answer_id FROM questions WHERE id = $1 FOR UPDATE`, questionID).Scan(&qAuthor, &best)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if best == nil || *best != answerID {
		return nil
	}
	return unacceptTx(ctx, tx, questionID, answerID, qAuthor)
}

// unacceptTx takes answerID off questionID (whose author is qAuthor and whose
// row the caller has locked) and reverses the accept.
func unacceptTx(ctx context.Context, tx pgx.Tx, questionID, answerID, qAuthor uuid.UUID) error {
	var aAuthor uuid.UUID
	err := tx.QueryRow(ctx, `UPDATE answers SET is_best = false WHERE id = $1 RETURNING author_id`, answerID).Scan(&aAuthor)
	answerGone := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !answerGone {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE questions SET best_answer_id = NULL, is_answered = false, updated_at = now()
		WHERE id = $1 AND best_answer_id = $2`, questionID, answerID); err != nil {
		return err
	}
	if answerGone || !acceptPays(qAuthor, aAuthor) {
		return nil
	}
	a, q := answerID, questionID
	if err := addReputationTx(ctx, tx, aAuthor, "best_answer_unselected", -RepBestAnswerSelected, "answer", &a); err != nil {
		return err
	}
	if err := addReputationTx(ctx, tx, qAuthor, "best_answer_selector_reversed", -RepBestAnswerSelectorBonus, "question", &q); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE qa_profiles SET best_answer_count = GREATEST(best_answer_count - 1, 0), updated_at = now()
		WHERE user_id = $1`, aAuthor)
	return err
}
