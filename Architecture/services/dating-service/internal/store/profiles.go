package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// UpsertProfileParams is the payload accepted by POST /v1/dating/profile.
// Pointer fields are optional — when nil they are not written.
type UpsertProfileParams struct {
	Intent           *string    `json:"intent,omitempty"`
	Bio              *string    `json:"bio,omitempty"`
	Gender           *string    `json:"gender,omitempty"`
	BirthDate        *time.Time `json:"birth_date,omitempty"`
	City             *string    `json:"city,omitempty"`
	State            *string    `json:"state,omitempty"`
	Country          *string    `json:"country,omitempty"`
	Latitude         *float64   `json:"latitude,omitempty"`
	Longitude        *float64   `json:"longitude,omitempty"`
	LocationGeohash  *string    `json:"location_geohash,omitempty"`
	HeightCm         *int       `json:"height_cm,omitempty"`
	Religion         *string    `json:"religion,omitempty"`
	Community        *string    `json:"community,omitempty"`
	Occupation       *string    `json:"occupation,omitempty"`
	Education        *string    `json:"education,omitempty"`
	Drinking         *string    `json:"drinking,omitempty"`
	Smoking          *string    `json:"smoking,omitempty"`
	Exercise         *string    `json:"exercise,omitempty"`
	Diet             *string    `json:"diet,omitempty"`
	WantsChildren    *string    `json:"wants_children,omitempty"`
	FamilyPlans      *string    `json:"family_plans,omitempty"`
	BlurMode         *bool      `json:"blur_mode,omitempty"`
	VisibleToPublic  *bool      `json:"visible_to_public,omitempty"`
	LanguagePrefs    []string   `json:"language_prefs,omitempty"`
}

// ErrProfileNotFound is returned when no row matches the requested user.
var ErrProfileNotFound = errors.New("not_found: dating profile not found")

const profileSelectCols = `
    user_id, intent, bio, gender, birth_date, city, state, country,
    latitude, longitude, location_geohash, height_cm, religion, community,
    occupation, education, drinking, smoking, exercise, diet,
    wants_children, family_plans, blur_mode, visible_to_public, paused,
    language_prefs, trust_tier, created_at, updated_at, deleted_at`

func scanProfile(row pgx.Row) (*Profile, error) {
	p := &Profile{}
	err := row.Scan(
		&p.UserID, &p.Intent, &p.Bio, &p.Gender, &p.BirthDate, &p.City, &p.State, &p.Country,
		&p.Latitude, &p.Longitude, &p.LocationGeohash, &p.HeightCm, &p.Religion, &p.Community,
		&p.Occupation, &p.Education, &p.Drinking, &p.Smoking, &p.Exercise, &p.Diet,
		&p.WantsChildren, &p.FamilyPlans, &p.BlurMode, &p.VisibleToPublic, &p.Paused,
		&p.LanguagePrefs, &p.TrustTier, &p.CreatedAt, &p.UpdatedAt, &p.DeletedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProfileNotFound
		}
		return nil, fmt.Errorf("scan dating profile: %w", err)
	}
	return p, nil
}

// GetProfile returns the (non-deleted) profile for a user.
func (s *Store) GetProfile(ctx context.Context, userID uuid.UUID) (*Profile, error) {
	row := s.db.QueryRow(ctx, `
        SELECT `+profileSelectCols+`
        FROM dating_profiles
        WHERE user_id = $1 AND deleted_at IS NULL`, userID)
	return scanProfile(row)
}

// UpsertProfile inserts a new profile or updates an existing one in place.
// Pointer-typed fields in p are only written when non-nil so partial updates
// preserve untouched columns.
func (s *Store) UpsertProfile(ctx context.Context, userID uuid.UUID, p UpsertProfileParams) (*Profile, error) {
	// Step 1: ensure a row exists. We can't INSERT … ON CONFLICT DO UPDATE
	// against arbitrarily nullable params, so we INSERT-IF-MISSING then UPDATE
	// only the non-nil columns.
	intent := "casual"
	if p.Intent != nil && *p.Intent != "" {
		intent = *p.Intent
	}
	// Sprint 6: cohort_salt populated at first insert so the staged
	// rollout has a stable per-user bucket. The salt isn't a secret.
	if _, err := s.db.Exec(ctx, `
        INSERT INTO dating_profiles (user_id, intent, cohort_salt)
        VALUES ($1, $2, encode(gen_random_bytes(8), 'hex'))
        ON CONFLICT (user_id) DO NOTHING`, userID, intent); err != nil {
		return nil, fmt.Errorf("ensure dating profile: %w", err)
	}

	// Step 2: per-column updates. Skip nil pointers.
	if p.Intent != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_profiles SET intent = $2, updated_at = now() WHERE user_id = $1`, userID, *p.Intent)
	}
	if p.Bio != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_profiles SET bio = $2, updated_at = now() WHERE user_id = $1`, userID, *p.Bio)
	}
	if p.Gender != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_profiles SET gender = $2, updated_at = now() WHERE user_id = $1`, userID, *p.Gender)
	}
	if p.BirthDate != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_profiles SET birth_date = $2, updated_at = now() WHERE user_id = $1`, userID, *p.BirthDate)
	}
	if p.City != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_profiles SET city = $2, updated_at = now() WHERE user_id = $1`, userID, *p.City)
	}
	if p.State != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_profiles SET state = $2, updated_at = now() WHERE user_id = $1`, userID, *p.State)
	}
	if p.Country != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_profiles SET country = $2, updated_at = now() WHERE user_id = $1`, userID, *p.Country)
	}
	if p.Latitude != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_profiles SET latitude = $2, updated_at = now() WHERE user_id = $1`, userID, *p.Latitude)
	}
	if p.Longitude != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_profiles SET longitude = $2, updated_at = now() WHERE user_id = $1`, userID, *p.Longitude)
	}
	if p.LocationGeohash != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_profiles SET location_geohash = $2, updated_at = now() WHERE user_id = $1`, userID, *p.LocationGeohash)
	}
	if p.HeightCm != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_profiles SET height_cm = $2, updated_at = now() WHERE user_id = $1`, userID, *p.HeightCm)
	}
	if p.Religion != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_profiles SET religion = $2, updated_at = now() WHERE user_id = $1`, userID, *p.Religion)
	}
	if p.Community != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_profiles SET community = $2, updated_at = now() WHERE user_id = $1`, userID, *p.Community)
	}
	if p.Occupation != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_profiles SET occupation = $2, updated_at = now() WHERE user_id = $1`, userID, *p.Occupation)
	}
	if p.Education != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_profiles SET education = $2, updated_at = now() WHERE user_id = $1`, userID, *p.Education)
	}
	if p.Drinking != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_profiles SET drinking = $2, updated_at = now() WHERE user_id = $1`, userID, *p.Drinking)
	}
	if p.Smoking != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_profiles SET smoking = $2, updated_at = now() WHERE user_id = $1`, userID, *p.Smoking)
	}
	if p.Exercise != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_profiles SET exercise = $2, updated_at = now() WHERE user_id = $1`, userID, *p.Exercise)
	}
	if p.Diet != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_profiles SET diet = $2, updated_at = now() WHERE user_id = $1`, userID, *p.Diet)
	}
	if p.WantsChildren != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_profiles SET wants_children = $2, updated_at = now() WHERE user_id = $1`, userID, *p.WantsChildren)
	}
	if p.FamilyPlans != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_profiles SET family_plans = $2, updated_at = now() WHERE user_id = $1`, userID, *p.FamilyPlans)
	}
	if p.BlurMode != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_profiles SET blur_mode = $2, updated_at = now() WHERE user_id = $1`, userID, *p.BlurMode)
	}
	if p.VisibleToPublic != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_profiles SET visible_to_public = $2, updated_at = now() WHERE user_id = $1`, userID, *p.VisibleToPublic)
	}
	if p.LanguagePrefs != nil {
		_, _ = s.db.Exec(ctx, `UPDATE dating_profiles SET language_prefs = $2, updated_at = now() WHERE user_id = $1`, userID, p.LanguagePrefs)
	}

	return s.GetProfile(ctx, userID)
}

// SetIntent updates only the intent field. Validated upstream against the
// CHECK constraint (`casual|serious|marriage`).
func (s *Store) SetIntent(ctx context.Context, userID uuid.UUID, intent string) (*Profile, error) {
	if _, err := s.db.Exec(ctx, `
        UPDATE dating_profiles
        SET intent = $2, updated_at = now()
        WHERE user_id = $1 AND deleted_at IS NULL`, userID, intent); err != nil {
		return nil, fmt.Errorf("set intent: %w", err)
	}
	return s.GetProfile(ctx, userID)
}

// SetPaused toggles the user's paused flag.
func (s *Store) SetPaused(ctx context.Context, userID uuid.UUID, paused bool) (*Profile, error) {
	if _, err := s.db.Exec(ctx, `
        UPDATE dating_profiles
        SET paused = $2, updated_at = now()
        WHERE user_id = $1 AND deleted_at IS NULL`, userID, paused); err != nil {
		return nil, fmt.Errorf("set paused: %w", err)
	}
	return s.GetProfile(ctx, userID)
}

// GetCohortSalt returns the per-user cohort_salt or "" if unset. Used by
// the soft-launch cohort gate (Sprint 6). The salt is set at profile
// creation time and never rotated.
func (s *Store) GetCohortSalt(ctx context.Context, userID uuid.UUID) (string, error) {
	var salt *string
	err := s.db.QueryRow(ctx, `
        SELECT cohort_salt FROM dating_profiles
        WHERE user_id = $1 AND deleted_at IS NULL`, userID).Scan(&salt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrProfileNotFound
		}
		return "", fmt.Errorf("get cohort salt: %w", err)
	}
	if salt == nil {
		return "", nil
	}
	return *salt, nil
}

// SetCohortSalt writes the per-user cohort_salt. Used to backfill profiles
// that predate the column (the migration adds the column but does not
// generate values for existing rows).
func (s *Store) SetCohortSalt(ctx context.Context, userID uuid.UUID, salt string) error {
	if userID == uuid.Nil {
		return fmt.Errorf("invalid: user_id required")
	}
	_, err := s.db.Exec(ctx, `
        UPDATE dating_profiles
        SET cohort_salt = $2, updated_at = now()
        WHERE user_id = $1 AND deleted_at IS NULL`, userID, salt)
	if err != nil {
		return fmt.Errorf("set cohort salt: %w", err)
	}
	return nil
}

// LookupFirstName returns the dating_profiles.first_name column for a user
// or an empty string. Used by cross-service preview lookups.
func (s *Store) LookupFirstName(ctx context.Context, userID uuid.UUID) (string, error) {
	var name *string
	err := s.db.QueryRow(ctx, `
        SELECT first_name FROM dating_profiles
        WHERE user_id = $1 AND deleted_at IS NULL`, userID).Scan(&name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrProfileNotFound
		}
		return "", fmt.Errorf("lookup first name: %w", err)
	}
	if name == nil {
		return "", nil
	}
	return *name, nil
}

// SoftDeleteProfile stamps deleted_at = now(). The 30-day grace begins at
// this moment; cmd/data-purger sweeps rows where deleted_at < now() - 30d.
//
// DPDP §15.8 — soft-delete is the user-visible "delete account" action; the
// real purge runs after the grace window so accidental deletes can be
// reversed.
func (s *Store) SoftDeleteProfile(ctx context.Context, userID uuid.UUID) error {
	if userID == uuid.Nil {
		return fmt.Errorf("invalid: user_id required")
	}
	tag, err := s.db.Exec(ctx, `
        UPDATE dating_profiles
        SET deleted_at = COALESCE(deleted_at, now()), paused = true, updated_at = now()
        WHERE user_id = $1`, userID)
	if err != nil {
		return fmt.Errorf("soft delete profile: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrProfileNotFound
	}
	return nil
}

// ListExpiredSoftDeletes returns user_ids where deleted_at is older than the
// grace window. Used by cmd/data-purger.
func (s *Store) ListExpiredSoftDeletes(ctx context.Context, graceDays int, limit int) ([]uuid.UUID, error) {
	if graceDays <= 0 {
		graceDays = 30
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, fmt.Sprintf(`
        SELECT user_id
        FROM dating_profiles
        WHERE deleted_at IS NOT NULL
          AND deleted_at < now() - INTERVAL '%d days'
        ORDER BY deleted_at ASC
        LIMIT $1`, graceDays), limit)
	if err != nil {
		return nil, fmt.Errorf("list expired soft deletes: %w", err)
	}
	defer rows.Close()
	out := make([]uuid.UUID, 0, 8)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// PurgeUserData hard-deletes every row owned by userID across the dating
// schema. Conversation history is preserved (the message-service owns it),
// but vouches are revoked, sparks/stashes/passes/photos/prompts/tune/
// preferences/safety_events are deleted, matches are anonymised, and the
// profile row itself is dropped.
//
// DPDP §15.8 — payment_intents are deleted; payment_events are kept for
// audit but their user link is removed via FK cascade since intent goes
// away.
//
// Returns the count of high-level rows affected for logging.
func (s *Store) PurgeUserData(ctx context.Context, userID uuid.UUID) (int64, error) {
	if userID == uuid.Nil {
		return 0, fmt.Errorf("invalid: user_id required")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin purge tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var rowsAffected int64
	exec := func(stmt string, args ...any) error {
		tag, err := tx.Exec(ctx, stmt, args...)
		if err != nil {
			return fmt.Errorf("purge step: %w", err)
		}
		rowsAffected += tag.RowsAffected()
		return nil
	}

	// 1) Photos, prompts, tune, preferences (cascade-OK siblings).
	if err := exec(`DELETE FROM dating_photos       WHERE user_id = $1`, userID); err != nil {
		return 0, err
	}
	if err := exec(`DELETE FROM dating_prompts      WHERE user_id = $1`, userID); err != nil {
		return 0, err
	}
	if err := exec(`DELETE FROM dating_tunes        WHERE user_id = $1`, userID); err != nil {
		return 0, err
	}
	if err := exec(`DELETE FROM dating_preferences  WHERE user_id = $1`, userID); err != nil {
		return 0, err
	}
	if err := exec(`DELETE FROM dating_echo_cache   WHERE user_id = $1`, userID); err != nil {
		return 0, err
	}

	// 2) Sparks (sent + received). Stashes and passes (this user's only).
	if err := exec(`DELETE FROM dating_sparks   WHERE from_user_id = $1 OR to_user_id = $1`, userID); err != nil {
		return 0, err
	}
	if err := exec(`DELETE FROM dating_stashes  WHERE user_id = $1`, userID); err != nil {
		return 0, err
	}
	if err := exec(`DELETE FROM dating_passes   WHERE user_id = $1`, userID); err != nil {
		return 0, err
	}
	if err := exec(`DELETE FROM dating_blocks   WHERE user_id = $1 OR blocked_id = $1`, userID); err != nil {
		return 0, err
	}

	// 3) Matches: we DO NOT delete the rows because the other party may
	//    still see the match in their inbox. Anonymise: the deleted user
	//    becomes a sentinel uuid with first_name="Deleted user" surfaced
	//    by the profile preview lookup. The match record itself stays.
	//    We DO close any active matches so the other side stops being
	//    surfaced as conversational.
	if err := exec(`
        UPDATE dating_matches
        SET status = 'closed', closed_by = $1
        WHERE (user_a = $1 OR user_b = $1) AND status NOT IN ('closed','expired')`, userID); err != nil {
		return 0, err
	}

	// 4) Vouches: outstanding entries (sent or received) are revoked.
	if err := exec(`
        UPDATE dating_vouches
        SET status = 'revoked', decided_at = COALESCE(decided_at, now())
        WHERE (voucher_id = $1 OR vouchee_id = $1) AND status NOT IN ('revoked','declined')`, userID); err != nil {
		return 0, err
	}

	// 5) Safety events, meets, reports.
	if err := exec(`DELETE FROM dating_safety_events WHERE user_id = $1`, userID); err != nil {
		return 0, err
	}
	if err := exec(`DELETE FROM dating_meets         WHERE user_id = $1 OR with_user_id = $1`, userID); err != nil {
		return 0, err
	}
	if err := exec(`DELETE FROM dating_reports       WHERE reporter_id = $1 OR target_id = $1`, userID); err != nil {
		return 0, err
	}

	// 6) Verifications. The Aadhaar number was never stored; we drop the
	//    row so digilocker_ref + selfie_score are gone.
	if err := exec(`DELETE FROM dating_verifications WHERE user_id = $1`, userID); err != nil {
		return 0, err
	}

	// 7) Premium: delete the subscription row. Payment events are kept for
	//    audit but their FK to dating_payment_intents goes away when the
	//    intents do; we explicitly NULL the link via SET NULL update.
	if err := exec(`UPDATE dating_payment_events SET payment_intent_id = NULL
                    WHERE payment_intent_id IN (
                        SELECT id FROM dating_payment_intents WHERE user_id = $1
                    )`, userID); err != nil {
		return 0, err
	}
	if err := exec(`DELETE FROM dating_payment_intents WHERE user_id = $1`, userID); err != nil {
		return 0, err
	}
	if err := exec(`DELETE FROM dating_premium_subscriptions WHERE user_id = $1`, userID); err != nil {
		return 0, err
	}

	// 8) Consent log + completed exports — keep audit row count for the
	//    DPDP regulator but anonymise. We retain the consent log because
	//    it's the proof we collected consent at all; we delete exports
	//    because the blob has expired.
	if err := exec(`DELETE FROM dating_data_exports WHERE user_id = $1`, userID); err != nil {
		return 0, err
	}

	// 9) Hard-delete the profile row.
	if err := exec(`DELETE FROM dating_profiles WHERE user_id = $1`, userID); err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit purge: %w", err)
	}
	return rowsAffected, nil
}
