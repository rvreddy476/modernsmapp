// Their preferences — what a candidate is looking for, read in one batch so
// a deck or a day's picks can be checked from the candidate's side too:
// mutual picks (M7) and dealbreakers (mechanic M12, DATING_DEALBREAKERS_ENABLED).
//
// dating_preferences.dealbreakers lists the preferences the user marked as
// dealbreakers: someone who does not fit one never sees them, and they never
// see that person.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TheirPreferences is one candidate's own preferences. Zero bounds mean
// "not set".
type TheirPreferences struct {
	MinAge       int
	MaxAge       int
	DistanceKm   int
	IntentFilter []string
	// PrivacyVerifiedOnly is the privacy toggle on the profile.
	PrivacyVerifiedOnly bool
	// Pass is the pass filters (M6); they count only while HoldsPass.
	Pass         PassFilters
	HoldsPass    bool
	Dealbreakers []string
}

// TheirPreferencesOf returns each listed user's own preferences. A user with
// no preferences row gets zero bounds.
func (s *Store) TheirPreferencesOf(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]TheirPreferences, error) {
	out := make(map[uuid.UUID]TheirPreferences, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
        SELECT p.user_id, COALESCE(pr.min_age, 0), COALESCE(pr.max_age, 0), COALESCE(pr.distance_km, 0),
               COALESCE(pr.intent_filter, '{}'), p.verified_only_filter,
               COALESCE(pr.verified_only, false), pr.min_height_cm, pr.max_height_cm,
               COALESCE(pr.language_filter, '{}'), COALESCE(pr.drinking_filter, '{}'), COALESCE(pr.smoking_filter, '{}'),
               COALESCE(pr.exercise_filter, '{}'), COALESCE(pr.diet_filter, '{}'),
               COALESCE(pr.dealbreakers, '{}'),
               EXISTS (SELECT 1 FROM dating_premium_subscriptions ps
                   WHERE ps.user_id = p.user_id AND ps.expires_at > now())
        FROM dating_profiles p
        LEFT JOIN dating_preferences pr ON pr.user_id = p.user_id
        WHERE p.user_id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, fmt.Errorf("their preferences: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var t TheirPreferences
		if err := rows.Scan(&id, &t.MinAge, &t.MaxAge, &t.DistanceKm, &t.IntentFilter, &t.PrivacyVerifiedOnly,
			&t.Pass.VerifiedOnly, &t.Pass.MinHeightCm, &t.Pass.MaxHeightCm,
			&t.Pass.Languages, &t.Pass.Drinking, &t.Pass.Smoking, &t.Pass.Exercise, &t.Pass.Diet,
			&t.Dealbreakers, &t.HoldsPass); err != nil {
			return nil, err
		}
		out[id] = t
	}
	return out, rows.Err()
}

// GetDealbreakers reads the user's dealbreakers (none without a row).
func (s *Store) GetDealbreakers(ctx context.Context, userID uuid.UUID) ([]string, error) {
	var out []string
	err := s.db.QueryRow(ctx, `SELECT COALESCE(dealbreakers, '{}') FROM dating_preferences WHERE user_id = $1`, userID).Scan(&out)
	if errors.Is(err, pgx.ErrNoRows) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dealbreakers: %w", err)
	}
	return out, nil
}

// SetDealbreakers replaces the user's dealbreakers.
func (s *Store) SetDealbreakers(ctx context.Context, userID uuid.UUID, codes []string) error {
	if codes == nil {
		codes = []string{}
	}
	if _, err := s.db.Exec(ctx, `
        INSERT INTO dating_preferences (user_id, dealbreakers) VALUES ($1, $2)
        ON CONFLICT (user_id) DO UPDATE SET dealbreakers = EXCLUDED.dealbreakers, updated_at = now()`, userID, codes); err != nil {
		return fmt.Errorf("set dealbreakers: %w", err)
	}
	return nil
}
