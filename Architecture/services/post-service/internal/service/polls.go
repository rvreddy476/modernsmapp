package service

import (
	"context"
	"errors"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Why a poll vote was refused. Each of these maps to one stable HTTP error
// code (see internal/http.writePollVoteError), so no client has to read an
// error message to tell them apart.
//
// Before this existed, /v1/posts/{id}/poll/vote answered "already voted" and
// "poll closed" with the SAME code, VOTE_ERROR, distinguished only by the
// pgx error text embedded in the message — down to the constraint name and
// SQLSTATE. The web client was matching on the literal "23505" to work out
// which one it had hit. That is a client coupled to a database error format
// it cannot see the definition of, and it breaks the day the constraint is
// renamed.
var (
	ErrPollNotFound      = errors.New("poll not found")
	ErrPollEnded         = errors.New("poll has ended")
	ErrPollOptionInvalid = errors.New("option does not belong to this poll")
	ErrPollAlreadyVoted  = errors.New("already voted on this poll")
)

// CastPollVote records one vote and is the single implementation behind BOTH
// vote routes.
//
// There were two, and neither was right. /v1/posts/{id}/vote checked
// allows_multiple and the end date but reported every refusal as
// 500 INTERNAL_ERROR. /v1/posts/{id}/poll/vote had usable status codes but
// skipped the service entirely — it went straight to an INSERT whose only
// guard was a primary key of (post_id, user_id, option_id). That key stops
// the same option twice and nothing else, so on a single-choice poll one
// account could vote Teal and then Orange, both 200, and the poll would
// report three votes from two people. Confirmed live on 2026-09-09.
//
// So the enforcement lived on one route and the error handling on the other.
// Both routes now call this.
func (s *Service) CastPollVote(ctx context.Context, postID, optionID, userID uuid.UUID) error {
	poll, err := s.pgStore.GetPoll(ctx, postID)
	if err != nil {
		return err
	}
	if poll == nil {
		return ErrPollNotFound
	}
	if poll.HasEnded {
		return ErrPollEnded
	}
	if !pollHasOption(poll, optionID) {
		return ErrPollOptionInvalid
	}

	// The store restates allows_multiple in SQL under a per-voter lock, so
	// this is the reason for the refusal rather than the check itself: a
	// false here means the row was rejected by that guard.
	inserted, err := s.pgStore.InsertPollVote(ctx, postID, optionID, userID, poll.AllowsMultiple)
	if err != nil {
		return err
	}
	if !inserted {
		return ErrPollAlreadyVoted
	}
	return nil
}

// CastVote is the name /v1/posts/{id}/vote reaches. It is deliberately a
// straight delegation: the two routes are the same operation, and the shipped
// Android client uses only /poll/vote, so the older spelling is kept rather
// than removed.
func (s *Service) CastVote(ctx context.Context, postID, optionID, userID uuid.UUID) error {
	return s.CastPollVote(ctx, postID, optionID, userID)
}

// pollHasOption answers membership from the options GetPoll already loaded,
// rather than issuing a fourth query for something we are holding.
func pollHasOption(poll *postgres.PollData, optionID uuid.UUID) bool {
	for _, opt := range poll.Options {
		if opt.ID == optionID {
			return true
		}
	}
	return false
}

func (s *Service) GetPollResults(ctx context.Context, postID uuid.UUID) ([]postgres.PollVoteResult, error) {
	return s.pgStore.GetPollResults(ctx, postID)
}
