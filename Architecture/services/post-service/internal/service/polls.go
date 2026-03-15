package service

import (
	"context"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

func (s *Service) CastPollVote(ctx context.Context, postID, optionID, userID uuid.UUID) error {
	return s.pgStore.CastPollVote(ctx, postID, optionID, userID)
}

func (s *Service) GetPollResults(ctx context.Context, postID uuid.UUID) ([]postgres.PollVoteResult, error) {
	return s.pgStore.GetPollResults(ctx, postID)
}
