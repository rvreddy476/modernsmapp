package service

import (
	"context"

	"github.com/google/uuid"
)

// Accepted answers (audit H1, M9). The store decides and pays in one
// transaction (store/best_answer.go): the question's author only, an answer
// of that question only, one accepted answer, no pay for accepting one's own
// answer, and every unaccept (explicit, by accepting another, by deleting
// or hiding the answer) reverses the award.

func (s *Service) SelectBestAnswer(ctx context.Context, questionID, answerID, selectorID uuid.UUID) error {
	if _, _, err := s.visibleQuestion(ctx, questionID, &selectorID); err != nil {
		return err
	}
	res, err := s.store.SelectBestAnswer(ctx, questionID, answerID, selectorID)
	if err != nil {
		return err
	}
	if res.Changed && s.producer != nil {
		_ = s.producer.PublishBestAnswerSelected(ctx, questionID, answerID, selectorID, res.AnswerAuthorID)
	}
	return nil
}

func (s *Service) UnselectBestAnswer(ctx context.Context, questionID, actorID uuid.UUID) error {
	_, err := s.store.UnselectBestAnswer(ctx, questionID, actorID)
	return err
}

// DeleteAnswer deletes the actor's own answer. A repeat is a no-op.
func (s *Service) DeleteAnswer(ctx context.Context, answerID, actorID uuid.UUID) error {
	deleted, err := s.store.DeleteAnswer(ctx, answerID, actorID)
	if err != nil {
		return err
	}
	if deleted {
		s.ReleaseRemovedMedia(ctx, nil, []uuid.UUID{answerID})
	}
	return nil
}
