package service

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/qa-service/internal/store"
	"github.com/google/uuid"
)

// Votes (audit H1, M2, L4). The store applies the vote, its counters and the
// reputation the transition pays or reverses in one transaction, serialized
// per (voter, target). A vote that changes nothing pays nothing and
// notifies nobody. Self-votes are refused for questions, answers and
// comments alike.

// ErrCannotVoteOwn is returned when a user votes on their own content.
var ErrCannotVoteOwn = store.ErrOwnContent

// voteNoticeTTL bounds how often one voter's vote on one target can reach
// its author: removing and re-adding a vote is a real change, but must not
// become a push loop.
const voteNoticeTTL = 7 * 24 * time.Hour

// shouldNotifyVote reports whether a vote result is worth an event: only a
// real change to a cast vote, and only the first time in voteNoticeTTL this
// voter casts that vote on that target. Without Redis every change notifies.
func (s *Service) shouldNotifyVote(ctx context.Context, kind string, voter, target uuid.UUID, res store.VoteResult) bool {
	if !res.Changed() || res.Current == "" {
		return false
	}
	if s.rdb == nil {
		return true
	}
	key := fmt.Sprintf("qa:vote-notice:%s:%s:%s:%s", kind, voter, target, res.Current)
	ok, err := s.rdb.SetNX(ctx, key, 1, voteNoticeTTL).Result()
	return err != nil || ok
}

func (s *Service) VoteQuestion(ctx context.Context, userID, questionID uuid.UUID, voteType string) error {
	if err := checkVoteType(voteType); err != nil {
		return err
	}
	if _, _, err := s.visibleQuestion(ctx, questionID, &userID); err != nil {
		return err
	}
	res, err := s.store.VoteQuestion(ctx, userID, questionID, voteType)
	if err != nil {
		return err
	}
	if s.producer != nil && s.shouldNotifyVote(ctx, "question", userID, questionID, res) {
		_ = s.producer.PublishQuestionVoted(ctx, questionID, userID, voteType)
	}
	return nil
}

// RemoveQuestionVote needs no visibility: taking back one's own vote reveals
// nothing and reverses what it paid.
func (s *Service) RemoveQuestionVote(ctx context.Context, userID, questionID uuid.UUID) error {
	_, err := s.store.RemoveQuestionVote(ctx, userID, questionID)
	return err
}

func (s *Service) VoteAnswer(ctx context.Context, userID, answerID uuid.UUID, voteType string) error {
	if err := checkVoteType(voteType); err != nil {
		return err
	}
	if _, _, err := s.visibleAnswer(ctx, answerID, &userID); err != nil {
		return err
	}
	res, err := s.store.VoteAnswer(ctx, userID, answerID, voteType)
	if err != nil {
		return err
	}
	if s.producer != nil && s.shouldNotifyVote(ctx, "answer", userID, answerID, res) {
		_ = s.producer.PublishAnswerVoted(ctx, answerID, userID, voteType)
	}
	return nil
}

func (s *Service) RemoveAnswerVote(ctx context.Context, userID, answerID uuid.UUID) error {
	_, err := s.store.RemoveAnswerVote(ctx, userID, answerID)
	return err
}

func (s *Service) VoteComment(ctx context.Context, userID, commentID uuid.UUID, voteType string) error {
	if err := checkVoteType(voteType); err != nil {
		return err
	}
	if _, err := s.visibleComment(ctx, commentID, &userID); err != nil {
		return err
	}
	_, err := s.store.VoteComment(ctx, userID, commentID, voteType)
	return err
}

func (s *Service) RemoveCommentVote(ctx context.Context, userID, commentID uuid.UUID) error {
	_, err := s.store.RemoveCommentVote(ctx, userID, commentID)
	return err
}
