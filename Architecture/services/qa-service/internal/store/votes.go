package store

import (
	"context"
	"errors"
	"fmt"

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
//
// Audit 2026-10-30 H1/M2: the read of the prior vote is serialized per
// (voter, target) by a transaction-scoped advisory lock, so two concurrent
// first votes cannot both read "no vote" and count twice; the reputation a
// transition pays (or reverses) is written in the same transaction; and the
// caller learns whether anything changed, so a repeated vote neither pays
// nor notifies.

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

// VoteResult is what one vote call did. Previous == Current means nothing
// changed: no counter moved, no reputation was paid, nothing to notify.
type VoteResult struct {
	Previous string
	Current  string
	// AuthorID is the target's author.
	AuthorID uuid.UUID
}

// Changed reports whether the call changed the caller's vote.
func (r VoteResult) Changed() bool { return r.Previous != r.Current }

// voteKind names one votable table family.
type voteKind struct {
	name        string // "question", "answer", "comment": reputation source type
	votes       string // vote rows
	targetCol   string // the vote rows' target column
	target      string // the target table
	upDownCount bool   // the target keeps upvote_count/downvote_count
}

var (
	questionVotes = voteKind{"question", "question_votes", "question_id", "questions", true}
	answerVotes   = voteKind{"answer", "answer_votes", "answer_id", "answers", true}
	commentVotes  = voteKind{"comment", "answer_comment_votes", "comment_id", "answer_comments", false}
)

// ErrOwnContent refuses a vote on the voter's own question, answer or comment.
var ErrOwnContent = refuse(ErrForbidden, "cannot vote on your own content")

// voteAllowed refuses casting a vote on one's own content; taking a vote
// back is always allowed.
func voteAllowed(newVote string, author, voter uuid.UUID) error {
	if newVote != "" && author == voter {
		return ErrOwnContent
	}
	return nil
}

// applyVote moves userID's vote on targetID to newVote ("" removes it).
func (s *Store) applyVote(ctx context.Context, k voteKind, userID, targetID uuid.UUID, newVote string) (VoteResult, error) {
	res := VoteResult{Current: newVote}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// One writer per (voter, target): the prior vote read below is then
	// the committed state, even for a missing row (M2).
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		fmt.Sprintf("qa-vote:%s:%s:%s", k.name, userID, targetID)); err != nil {
		return res, err
	}

	// A new vote needs a live target; a removal also reverses a vote on a
	// target that has since been deleted.
	authorSQL := `SELECT author_id FROM ` + k.target + ` WHERE id = $1`
	if newVote != "" {
		authorSQL += ` AND deleted_at IS NULL`
	}
	err = tx.QueryRow(ctx, authorSQL, targetID).Scan(&res.AuthorID)
	if errors.Is(err, pgx.ErrNoRows) {
		if newVote == "" {
			res.Current = ""
			return res, nil
		}
		return res, refuse(ErrNotFound, k.name+" not found")
	}
	if err != nil {
		return res, err
	}
	if err := voteAllowed(newVote, res.AuthorID, userID); err != nil {
		return res, err
	}

	err = tx.QueryRow(ctx, `SELECT vote_type FROM `+k.votes+` WHERE user_id = $1 AND `+k.targetCol+` = $2`,
		userID, targetID).Scan(&res.Previous)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return res, err
	}
	if !res.Changed() {
		return res, nil
	}

	if newVote == "" {
		_, err = tx.Exec(ctx, `DELETE FROM `+k.votes+` WHERE user_id = $1 AND `+k.targetCol+` = $2`, userID, targetID)
	} else {
		_, err = tx.Exec(ctx,
			`INSERT INTO `+k.votes+` (user_id, `+k.targetCol+`, vote_type) VALUES ($1, $2, $3)
			 ON CONFLICT (user_id, `+k.targetCol+`) DO UPDATE SET vote_type = EXCLUDED.vote_type`,
			userID, targetID, newVote)
	}
	if err != nil {
		return res, err
	}

	ds, du, dd := voteDelta(res.Previous, newVote)
	if k.upDownCount {
		_, err = tx.Exec(ctx,
			`UPDATE `+k.target+` SET vote_score = vote_score + $2, upvote_count = upvote_count + $3, downvote_count = downvote_count + $4 WHERE id = $1`,
			targetID, ds, du, dd)
	} else {
		_, err = tx.Exec(ctx, `UPDATE `+k.target+` SET vote_score = vote_score + $2 WHERE id = $1`, targetID, ds)
	}
	if err != nil {
		return res, err
	}

	src := targetID
	for _, ev := range VoteReputation(k.name, res.Previous, newVote) {
		if err := addReputationTx(ctx, tx, res.AuthorID, ev.EventType, ev.Points, k.name, &src); err != nil {
			return res, err
		}
	}
	return res, tx.Commit(ctx)
}

func (s *Store) VoteQuestion(ctx context.Context, userID, questionID uuid.UUID, voteType string) (VoteResult, error) {
	return s.applyVote(ctx, questionVotes, userID, questionID, voteType)
}

func (s *Store) RemoveQuestionVote(ctx context.Context, userID, questionID uuid.UUID) (VoteResult, error) {
	return s.applyVote(ctx, questionVotes, userID, questionID, "")
}

func (s *Store) GetUserQuestionVote(ctx context.Context, userID, questionID uuid.UUID) (*string, error) {
	var voteType string
	err := s.db.QueryRow(ctx, `SELECT vote_type FROM question_votes WHERE user_id = $1 AND question_id = $2`, userID, questionID).Scan(&voteType)
	if err != nil {
		return nil, nil
	}
	return &voteType, nil
}

func (s *Store) VoteAnswer(ctx context.Context, userID, answerID uuid.UUID, voteType string) (VoteResult, error) {
	return s.applyVote(ctx, answerVotes, userID, answerID, voteType)
}

func (s *Store) RemoveAnswerVote(ctx context.Context, userID, answerID uuid.UUID) (VoteResult, error) {
	return s.applyVote(ctx, answerVotes, userID, answerID, "")
}

func (s *Store) GetUserAnswerVote(ctx context.Context, userID, answerID uuid.UUID) (*string, error) {
	var voteType string
	err := s.db.QueryRow(ctx, `SELECT vote_type FROM answer_votes WHERE user_id = $1 AND answer_id = $2`, userID, answerID).Scan(&voteType)
	if err != nil {
		return nil, nil
	}
	return &voteType, nil
}

// VoteComment refuses a vote on the voter's own comment (audit L4), like
// questions and answers.
func (s *Store) VoteComment(ctx context.Context, userID, commentID uuid.UUID, voteType string) (VoteResult, error) {
	return s.applyVote(ctx, commentVotes, userID, commentID, voteType)
}

func (s *Store) RemoveCommentVote(ctx context.Context, userID, commentID uuid.UUID) (VoteResult, error) {
	return s.applyVote(ctx, commentVotes, userID, commentID, "")
}
