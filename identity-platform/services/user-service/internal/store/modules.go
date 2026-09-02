package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ModulePreferences is a user's server-driven module selection (Module 3).
//
// No row in usr.module_preferences means "defaults" — the service layer
// materialises that shape rather than answering 404, so a fresh account and
// an account that explicitly chose everything are indistinguishable to the
// client (privacy-first: the choice itself is not observable).
//
// UserID is deliberately not serialised: the routes are /v1/users/me/*, so
// echoing the id adds nothing and the response contract (modules,
// home_module, onboarding_completed_at, updated_at) stays minimal.
type ModulePreferences struct {
	UserID                uuid.UUID  `json:"-"`
	Modules               []string   `json:"modules"`
	HomeModule            string     `json:"home_module"`
	OnboardingCompletedAt *time.Time `json:"onboarding_completed_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

// GetModulePreferences returns the stored row, or (nil, nil) when the user
// has never written one — the caller supplies the defaults.
func (s *Store) GetModulePreferences(ctx context.Context, userID uuid.UUID) (*ModulePreferences, error) {
	p := ModulePreferences{UserID: userID}
	err := s.db.QueryRow(ctx, `
		SELECT modules, home_module, onboarding_completed_at, updated_at
		FROM usr.module_preferences
		WHERE user_id = $1
	`, userID).Scan(&p.Modules, &p.HomeModule, &p.OnboardingCompletedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// UpsertModulePreferences writes the full selection.
//
// onboarding_completed_at is write-once: it is set to NOW() only when
// completeOnboarding is true AND the column is still NULL. A later update
// with the flag (or without it) never moves or clears the original
// completion time.
func (s *Store) UpsertModulePreferences(ctx context.Context, userID uuid.UUID, modules []string, homeModule string, completeOnboarding bool) (*ModulePreferences, error) {
	// pgx maps a nil []string to NULL, which violates NOT NULL; an explicit
	// empty selection must round-trip as an empty array.
	if modules == nil {
		modules = []string{}
	}
	p := ModulePreferences{UserID: userID}
	err := s.db.QueryRow(ctx, `
		INSERT INTO usr.module_preferences
			(user_id, modules, home_module, onboarding_completed_at, updated_at)
		VALUES ($1, $2, $3, CASE WHEN $4 THEN NOW() END, NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			modules     = EXCLUDED.modules,
			home_module = EXCLUDED.home_module,
			onboarding_completed_at = COALESCE(
				usr.module_preferences.onboarding_completed_at,
				CASE WHEN $4 THEN NOW() END),
			updated_at  = NOW()
		RETURNING modules, home_module, onboarding_completed_at, updated_at
	`, userID, modules, homeModule, completeOnboarding).
		Scan(&p.Modules, &p.HomeModule, &p.OnboardingCompletedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// SetRegion stores the user's ISO-3166-1 alpha-2 region on usr.users. The
// caller (service layer) has already validated and uppercased the code.
// Returns pgx.ErrNoRows via the scan when the user does not exist.
func (s *Store) SetRegion(ctx context.Context, userID uuid.UUID, countryCode string) (string, error) {
	var region string
	err := s.db.QueryRow(ctx, `
		UPDATE usr.users SET region = $2, updated_at = NOW()
		WHERE id = $1
		RETURNING COALESCE(region, '')
	`, userID, countryCode).Scan(&region)
	if err != nil {
		return "", err
	}
	return region, nil
}

// getRegion reads the region column for embedding into settings reads. An
// unset region reads as "".
func (s *Store) getRegion(ctx context.Context, userID uuid.UUID) (string, error) {
	var region string
	err := s.db.QueryRow(ctx,
		`SELECT COALESCE(region, '') FROM usr.users WHERE id = $1`, userID).
		Scan(&region)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return region, nil
}
