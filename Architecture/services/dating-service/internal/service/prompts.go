package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// promptCatalog is the v1 hard-coded set of prompts shown to users.
// Spec §6.3 / §16.2: a static catalog is fine for v1; dynamic catalog
// management lives in a future admin tool.
var promptCatalog = []store.PromptCatalogItem{
	{ID: 1, Question: "My ideal Sunday is..."},
	{ID: 2, Question: "I get nerdy about..."},
	{ID: 3, Question: "The way to my heart is..."},
	{ID: 4, Question: "A skill I'm working on..."},
	{ID: 5, Question: "My most controversial take..."},
	{ID: 6, Question: "What I'm currently reading or watching..."},
	{ID: 7, Question: "The best meal I ever ate..."},
	{ID: 8, Question: "A place I want to visit next..."},
	{ID: 9, Question: "I'm looking for someone who..."},
	{ID: 10, Question: "Two truths and a lie..."},
	{ID: 11, Question: "Family means... to me"},
	{ID: 12, Question: "What I love about my city..."},
}

// validPromptID gates writes to known catalog ids.
func validPromptID(id int) bool {
	for _, item := range promptCatalog {
		if item.ID == id {
			return true
		}
	}
	return false
}

// PromptCatalog returns the static prompt catalog for v1.
func (s *Service) PromptCatalog(_ context.Context) []store.PromptCatalogItem {
	return promptCatalog
}

// ListPrompts returns the user's answered prompts.
func (s *Service) ListPrompts(ctx context.Context, userID uuid.UUID) ([]store.Prompt, error) {
	return s.store.ListPrompts(ctx, userID)
}

// UpsertPrompt validates the prompt id + answer and writes through.
func (s *Service) UpsertPrompt(ctx context.Context, userID uuid.UUID, promptID int, answer string) (*store.Prompt, error) {
	if !validPromptID(promptID) {
		return nil, fmt.Errorf("invalid: prompt_id is not in the v1 catalog")
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return nil, fmt.Errorf("invalid: answer is required")
	}
	if len(answer) > 280 {
		return nil, fmt.Errorf("invalid: answer must be <= 280 characters")
	}
	return s.store.UpsertPrompt(ctx, userID, promptID, answer)
}

// DeletePrompt removes the user's answer for the given prompt.
func (s *Service) DeletePrompt(ctx context.Context, userID uuid.UUID, promptID int) error {
	return s.store.DeletePrompt(ctx, userID, promptID)
}
