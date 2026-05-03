package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// UpsertTuneParams is the payload accepted by PUT /v1/dating/tune.
type UpsertTuneParams struct {
	LifestyleRhythm   *int    `json:"lifestyle_rhythm,omitempty"`
	ConversationStyle *string `json:"conversation_style,omitempty"`
	FaithWeight       *int    `json:"faith_weight,omitempty"`
	FamilyWeight      *int    `json:"family_weight,omitempty"`
	RegionWeight      *int    `json:"region_weight,omitempty"`
	FamilyPlansAxis   *int    `json:"family_plans_axis,omitempty"`
	EducationAxis     *int    `json:"education_axis,omitempty"`
}

// ErrTuneNotFound is returned when no row exists for the requested user.
var ErrTuneNotFound = errors.New("not_found: tune not found")

// GetTune returns the user's Tune. If none exists yet, ErrTuneNotFound is
// returned — service layer typically responds with an empty Tune for that
// case.
func (s *Store) GetTune(ctx context.Context, userID uuid.UUID) (*Tune, error) {
	t := &Tune{}
	row := s.db.QueryRow(ctx, `
        SELECT user_id, lifestyle_rhythm, conversation_style,
               faith_weight, family_weight, region_weight,
               family_plans_axis, education_axis, updated_at
        FROM dating_tunes WHERE user_id = $1`, userID)
	err := row.Scan(
		&t.UserID, &t.LifestyleRhythm, &t.ConversationStyle,
		&t.FaithWeight, &t.FamilyWeight, &t.RegionWeight,
		&t.FamilyPlansAxis, &t.EducationAxis, &t.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTuneNotFound
		}
		return nil, fmt.Errorf("scan tune: %w", err)
	}
	return t, nil
}

// UpsertTune inserts or updates a Tune row. Only non-nil fields are written.
func (s *Store) UpsertTune(ctx context.Context, userID uuid.UUID, p UpsertTuneParams) (*Tune, error) {
	if _, err := s.db.Exec(ctx, `
        INSERT INTO dating_tunes (user_id) VALUES ($1)
        ON CONFLICT (user_id) DO NOTHING`, userID); err != nil {
		return nil, fmt.Errorf("ensure tune: %w", err)
	}

	if p.LifestyleRhythm != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_tunes SET lifestyle_rhythm = $2, updated_at = now() WHERE user_id = $1`, userID, *p.LifestyleRhythm)
	}
	if p.ConversationStyle != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_tunes SET conversation_style = $2, updated_at = now() WHERE user_id = $1`, userID, *p.ConversationStyle)
	}
	if p.FaithWeight != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_tunes SET faith_weight = $2, updated_at = now() WHERE user_id = $1`, userID, *p.FaithWeight)
	}
	if p.FamilyWeight != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_tunes SET family_weight = $2, updated_at = now() WHERE user_id = $1`, userID, *p.FamilyWeight)
	}
	if p.RegionWeight != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_tunes SET region_weight = $2, updated_at = now() WHERE user_id = $1`, userID, *p.RegionWeight)
	}
	if p.FamilyPlansAxis != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_tunes SET family_plans_axis = $2, updated_at = now() WHERE user_id = $1`, userID, *p.FamilyPlansAxis)
	}
	if p.EducationAxis != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_tunes SET education_axis = $2, updated_at = now() WHERE user_id = $1`, userID, *p.EducationAxis)
	}

	return s.GetTune(ctx, userID)
}
