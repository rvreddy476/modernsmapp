// Pass filters store — mechanic M6. The filters a pass unlocks live on the
// preferences row beside the free ones. They are kept when a pass runs out
// (and simply not applied), so they come back with the next pass.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PassFilters are the filters a pass unlocks. Languages is the existing
// language_filter column.
type PassFilters struct {
	VerifiedOnly bool
	MinHeightCm  *int
	MaxHeightCm  *int
	Languages    []string
	Drinking     []string
	Smoking      []string
	Exercise     []string
	Diet         []string
}

// Empty reports whether no pass filter is set.
func (f PassFilters) Empty() bool {
	return !f.VerifiedOnly && f.MinHeightCm == nil && f.MaxHeightCm == nil &&
		len(f.Languages) == 0 && len(f.Drinking) == 0 && len(f.Smoking) == 0 &&
		len(f.Exercise) == 0 && len(f.Diet) == 0
}

// GetPassFilters reads the user's pass filters (zero value without a row).
func (s *Store) GetPassFilters(ctx context.Context, userID uuid.UUID) (PassFilters, error) {
	var f PassFilters
	err := s.db.QueryRow(ctx, `
        SELECT COALESCE(verified_only, false), min_height_cm, max_height_cm,
               language_filter, drinking_filter, smoking_filter, exercise_filter, diet_filter
        FROM dating_preferences WHERE user_id = $1`, userID).Scan(
		&f.VerifiedOnly, &f.MinHeightCm, &f.MaxHeightCm,
		&f.Languages, &f.Drinking, &f.Smoking, &f.Exercise, &f.Diet)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PassFilters{}, nil
		}
		return PassFilters{}, fmt.Errorf("pass filters: %w", err)
	}
	return f, nil
}

// SetPassFilters replaces the user's pass filters.
func (s *Store) SetPassFilters(ctx context.Context, userID uuid.UUID, f PassFilters) error {
	nz := func(v []string) []string {
		if v == nil {
			return []string{}
		}
		return v
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO dating_preferences (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, userID); err != nil {
		return fmt.Errorf("ensure preferences: %w", err)
	}
	_, err := s.db.Exec(ctx, `
        UPDATE dating_preferences
        SET verified_only = $2, min_height_cm = $3, max_height_cm = $4,
            language_filter = $5, drinking_filter = $6, smoking_filter = $7,
            exercise_filter = $8, diet_filter = $9, updated_at = now()
        WHERE user_id = $1`,
		userID, f.VerifiedOnly, f.MinHeightCm, f.MaxHeightCm,
		nz(f.Languages), nz(f.Drinking), nz(f.Smoking), nz(f.Exercise), nz(f.Diet))
	if err != nil {
		return fmt.Errorf("set pass filters: %w", err)
	}
	return nil
}
