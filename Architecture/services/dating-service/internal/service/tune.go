package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

func validConversationStyle(s string) bool {
	switch s {
	case "witty", "deep", "playful", "direct", "reflective":
		return true
	}
	return false
}

func inSliderRange(v *int) bool {
	if v == nil {
		return true
	}
	return *v >= 1 && *v <= 5
}

func (s *Service) GetTune(ctx context.Context, userID uuid.UUID) (*store.Tune, error) {
	t, err := s.store.GetTune(ctx, userID)
	if err != nil {
		if errors.Is(err, store.ErrTuneNotFound) {
			return &store.Tune{UserID: userID}, nil
		}
		return nil, err
	}
	return t, nil
}

func (s *Service) UpsertTune(ctx context.Context, userID uuid.UUID, p store.UpsertTuneParams) (*store.Tune, error) {
	if p.ConversationStyle != nil && !validConversationStyle(*p.ConversationStyle) {
		return nil, fmt.Errorf("invalid: conversation_style must be one of witty|deep|playful|direct|reflective")
	}
	if !inSliderRange(p.LifestyleRhythm) ||
		!inSliderRange(p.FaithWeight) ||
		!inSliderRange(p.FamilyWeight) ||
		!inSliderRange(p.RegionWeight) ||
		!inSliderRange(p.FamilyPlansAxis) ||
		!inSliderRange(p.EducationAxis) {
		return nil, fmt.Errorf("invalid: tune slider values must be between 1 and 5")
	}
	out, err := s.store.UpsertTune(ctx, userID, p)
	if err != nil {
		return nil, err
	}
	s.InvalidatePulseCache(ctx, userID)
	return out, nil
}
