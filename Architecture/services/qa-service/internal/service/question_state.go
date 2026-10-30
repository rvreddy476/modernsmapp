package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Close, reopen and the community approval queue (audit H2). Who may act is
// guards.go (closeDecision, reopenDecision); the store re-checks the state
// it moves from in the same statement.

func (s *Service) CloseQuestion(ctx context.Context, questionID, actorID uuid.UUID, reason string) error {
	reason = strings.TrimSpace(reason)
	if err := checkLen("reason", reason, maxReasonLen); err != nil {
		return err
	}
	q, role, err := s.visibleQuestion(ctx, questionID, &actorID)
	if err != nil {
		return err
	}
	if err := closeDecision(q.Status, q.AuthorID, actorID, q.CommunityID != nil, role); err != nil {
		return err
	}
	closed, err := s.store.CloseQuestion(ctx, questionID, actorID, reason)
	if err != nil {
		return err
	}
	if !closed {
		return fmt.Errorf("conflict: only an open question can be closed")
	}
	if s.producer != nil {
		_ = s.producer.PublishQuestionClosed(ctx, questionID, actorID, reason)
	}
	return nil
}

// ReopenQuestion: the author reopens only what they closed themselves; a
// community moderator reopens any closed question of their community. No one
// reopens a platform moderator's lock, a merge or a question awaiting
// approval this way.
func (s *Service) ReopenQuestion(ctx context.Context, questionID, actorID uuid.UUID) error {
	q, role, err := s.visibleQuestion(ctx, questionID, &actorID)
	if err != nil {
		return err
	}
	asModerator, err := reopenDecision(q.Status, q.AuthorID, q.ClosedBy, actorID, q.CommunityID != nil, role)
	if err != nil {
		return err
	}
	reopened, err := s.store.ReopenQuestion(ctx, questionID, actorID, asModerator)
	if err != nil {
		return err
	}
	if !reopened {
		return fmt.Errorf("conflict: this question cannot be reopened")
	}
	return nil
}

// DecidePendingQuestion approves or rejects a question awaiting approval in
// a community whose settings require it. Moderators of that community only.
func (s *Service) DecidePendingQuestion(ctx context.Context, communityID, questionID, actorID uuid.UUID, approve bool, reason string) error {
	reason = strings.TrimSpace(reason)
	if err := checkLen("reason", reason, maxReasonLen); err != nil {
		return err
	}
	_, role, _, err := s.getCommunityAccess(ctx, communityID, &actorID)
	if err != nil {
		return err
	}
	if err := approvalDecision(role); err != nil {
		return err
	}
	done, err := s.store.DecidePendingQuestion(ctx, communityID, questionID, actorID, approve, reason)
	if err != nil {
		return err
	}
	if !done {
		return fmt.Errorf("not_found: no question awaiting approval with that id in this community")
	}
	if !approve {
		s.ReleaseRemovedMedia(ctx, []uuid.UUID{questionID}, nil)
	}
	return nil
}
