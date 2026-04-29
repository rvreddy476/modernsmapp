package service

import (
	"context"

	"github.com/atpost/qa-service/internal/store"
	"github.com/google/uuid"
)

// --- Question drafts ---

func (s *Service) UpsertQuestionDraft(ctx context.Context, p store.UpsertQuestionDraftParams) (*store.QuestionDraft, error) {
	return s.store.UpsertQuestionDraft(ctx, p)
}

func (s *Service) ListQuestionDrafts(ctx context.Context, authorID uuid.UUID, limit, offset int) ([]store.QuestionDraft, error) {
	return s.store.ListQuestionDrafts(ctx, authorID, limit, offset)
}

func (s *Service) DeleteQuestionDraft(ctx context.Context, draftID, authorID uuid.UUID) error {
	return s.store.DeleteQuestionDraft(ctx, draftID, authorID)
}

// --- Answer drafts ---

func (s *Service) UpsertAnswerDraft(ctx context.Context, p store.UpsertAnswerDraftParams) (*store.AnswerDraft, error) {
	return s.store.UpsertAnswerDraft(ctx, p)
}

func (s *Service) ListAnswerDrafts(ctx context.Context, authorID uuid.UUID, limit, offset int) ([]store.AnswerDraft, error) {
	return s.store.ListAnswerDrafts(ctx, authorID, limit, offset)
}

func (s *Service) DeleteAnswerDraft(ctx context.Context, draftID, authorID uuid.UUID) error {
	return s.store.DeleteAnswerDraft(ctx, draftID, authorID)
}
