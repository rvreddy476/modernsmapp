package service

import (
	"context"
	"fmt"

	"github.com/atpost/qa-service/internal/store"
	"github.com/google/uuid"
)

// One visibility rule for every read and every action on a question, its
// answers and their comments (audit H5): questionVisibility (guards.go) by
// id, store.questionVisibleSQL for lists. Votes, follows, saves, comments
// and answer requests act only on what the actor can see; a deleted answer
// or comment is not found.

// questionAccess applies the visibility rule for viewer and returns the
// viewer's role in the question's community ("" outside one).
func (s *Service) questionAccess(ctx context.Context, q *store.Question, viewer *uuid.UUID) (string, error) {
	if q == nil {
		return "", fmt.Errorf("not_found: question not found")
	}
	var role string
	restricted := false
	if q.CommunityID != nil {
		community, err := s.store.GetCommunity(ctx, *q.CommunityID)
		if err != nil || community == nil {
			return "", fmt.Errorf("not_found: question not found")
		}
		restricted = isRestrictedCommunity(community.CommunityType)
		if viewer != nil {
			role, err = s.store.GetCommunityMemberRole(ctx, *q.CommunityID, *viewer)
			if err != nil {
				return "", err
			}
		}
	}
	return role, questionVisibility(q.Status, q.AuthorID, viewer, q.CommunityID != nil, restricted, role)
}

// visibleQuestion loads a live question viewer may see.
func (s *Service) visibleQuestion(ctx context.Context, questionID uuid.UUID, viewer *uuid.UUID) (*store.Question, string, error) {
	q, err := s.store.GetQuestion(ctx, questionID)
	if err != nil {
		return nil, "", fmt.Errorf("not_found: question not found")
	}
	role, err := s.questionAccess(ctx, q, viewer)
	if err != nil {
		return nil, "", err
	}
	return q, role, nil
}

// visibleAnswer loads a live answer on a question viewer may see.
func (s *Service) visibleAnswer(ctx context.Context, answerID uuid.UUID, viewer *uuid.UUID) (*store.Answer, *store.Question, error) {
	a, err := s.store.GetAnswer(ctx, answerID)
	if err != nil {
		return nil, nil, fmt.Errorf("not_found: answer not found")
	}
	q, _, err := s.visibleQuestion(ctx, a.QuestionID, viewer)
	if err != nil {
		return nil, nil, err
	}
	return a, q, nil
}

// visibleComment loads a live comment on an answer viewer may see.
func (s *Service) visibleComment(ctx context.Context, commentID uuid.UUID, viewer *uuid.UUID) (*store.AnswerComment, error) {
	c, err := s.store.GetComment(ctx, commentID)
	if err != nil || c == nil {
		return nil, fmt.Errorf("not_found: comment not found")
	}
	if _, _, err := s.visibleAnswer(ctx, c.AnswerID, viewer); err != nil {
		return nil, err
	}
	return c, nil
}
