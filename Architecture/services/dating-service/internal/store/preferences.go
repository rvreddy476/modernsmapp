package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// UpsertPreferencesParams is the payload accepted by PUT /v1/dating/preferences.
type UpsertPreferencesParams struct {
	MinAge             *int     `json:"min_age,omitempty"`
	MaxAge             *int     `json:"max_age,omitempty"`
	DistanceKm         *int     `json:"distance_km,omitempty"`
	InterestedInGender *string  `json:"interested_in_gender,omitempty"`
	IntentFilter       []string `json:"intent_filter,omitempty"`
	BlurModePref       *bool    `json:"blur_mode_pref,omitempty"`
	LanguageFilter     []string `json:"language_filter,omitempty"`
}

// GetPreferences returns the user's preferences row, creating a default one
// (with the spec's 25 km default distance) if none exists.
func (s *Store) GetPreferences(ctx context.Context, userID uuid.UUID) (*Preferences, error) {
	if _, err := s.db.Exec(ctx, `
        INSERT INTO dating_preferences (user_id) VALUES ($1)
        ON CONFLICT (user_id) DO NOTHING`, userID); err != nil {
		return nil, fmt.Errorf("ensure preferences: %w", err)
	}

	p := &Preferences{}
	row := s.db.QueryRow(ctx, `
        SELECT user_id, min_age, max_age, distance_km, interested_in_gender,
               intent_filter, blur_mode_pref, language_filter, updated_at
        FROM dating_preferences WHERE user_id = $1`, userID)
	if err := row.Scan(
		&p.UserID, &p.MinAge, &p.MaxAge, &p.DistanceKm, &p.InterestedInGender,
		&p.IntentFilter, &p.BlurModePref, &p.LanguageFilter, &p.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("get preferences: %w", err)
		}
		return nil, fmt.Errorf("scan preferences: %w", err)
	}
	return p, nil
}

// UpsertPreferences sets only the non-nil fields on the preferences row.
func (s *Store) UpsertPreferences(ctx context.Context, userID uuid.UUID, p UpsertPreferencesParams) (*Preferences, error) {
	if _, err := s.db.Exec(ctx, `
        INSERT INTO dating_preferences (user_id) VALUES ($1)
        ON CONFLICT (user_id) DO NOTHING`, userID); err != nil {
		return nil, fmt.Errorf("ensure preferences: %w", err)
	}

	if p.MinAge != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_preferences SET min_age = $2, updated_at = now() WHERE user_id = $1`, userID, *p.MinAge)
	}
	if p.MaxAge != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_preferences SET max_age = $2, updated_at = now() WHERE user_id = $1`, userID, *p.MaxAge)
	}
	if p.DistanceKm != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_preferences SET distance_km = $2, updated_at = now() WHERE user_id = $1`, userID, *p.DistanceKm)
	}
	if p.InterestedInGender != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_preferences SET interested_in_gender = $2, updated_at = now() WHERE user_id = $1`, userID, *p.InterestedInGender)
	}
	if p.IntentFilter != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_preferences SET intent_filter = $2, updated_at = now() WHERE user_id = $1`, userID, p.IntentFilter)
	}
	if p.BlurModePref != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_preferences SET blur_mode_pref = $2, updated_at = now() WHERE user_id = $1`, userID, *p.BlurModePref)
	}
	if p.LanguageFilter != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_preferences SET language_filter = $2, updated_at = now() WHERE user_id = $1`, userID, p.LanguageFilter)
	}

	return s.GetPreferences(ctx, userID)
}
