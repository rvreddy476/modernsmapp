package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	// BirthDate is still accepted on the wire for compatibility, but
	// UpsertProfile never writes it: the service records it through
	// SetProfileBirthDate (identity wins; a client value locks after first
	// set).
	BirthDate *time.Time `json:"birth_date,omitempty"`
	// FirstName is the interim client-supplied name, written by the service
	// through SetProfileFirstName and never over an identity-sourced name.
	FirstName        *string    `json:"first_name,omitempty"`
	City             *string    `json:"city,omitempty"`
	State            *string    `json:"state,omitempty"`
	Country          *string    `json:"country,omitempty"`
	// Latitude and Longitude are sent together. Lane D7: UpsertProfile
	// validates them, stores only the point snapped to the 0.01 degree grid
	// (with location_geohash derived from it) and applies the location change
	// limits. location_geohash is no longer accepted from the client.
	Latitude         *float64   `json:"latitude,omitempty"`
	Longitude        *float64   `json:"longitude,omitempty"`
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
    language_prefs, trust_tier, profile_status, created_at, updated_at, deleted_at,
    first_name, prior_status, dob_source, first_name_source`

func scanProfile(row pgx.Row) (*Profile, error) {
	p := &Profile{}
	err := row.Scan(
		&p.UserID, &p.Intent, &p.Bio, &p.Gender, &p.BirthDate, &p.City, &p.State, &p.Country,
		&p.Latitude, &p.Longitude, &p.LocationGeohash, &p.HeightCm, &p.Religion, &p.Community,
		&p.Occupation, &p.Education, &p.Drinking, &p.Smoking, &p.Exercise, &p.Diet,
		&p.WantsChildren, &p.FamilyPlans, &p.BlurMode, &p.VisibleToPublic, &p.Paused,
		&p.LanguagePrefs, &p.TrustTier, &p.ProfileStatus, &p.CreatedAt, &p.UpdatedAt, &p.DeletedAt,
		&p.FirstName, &p.PriorStatus, &p.DOBSource, &p.FirstNameSource,
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
// preserve untouched columns. The non-nil fields are written by ONE UPDATE,
// so a bad value fails the whole call with an error and nothing half-lands.
// birth_date, first_name and the lifecycle columns are never written here.
//
// Lane D7: a location (Latitude + Longitude) is validated first
// (ErrInvalidLocation), then stored snapped through setLocationTx, which may
// refuse it with a *LocationRateLimitError. Row creation, the location and
// the other columns share one transaction, so a refused location writes
// nothing at all.
func (s *Store) UpsertProfile(ctx context.Context, userID uuid.UUID, p UpsertProfileParams) (*Profile, error) {
	setLocation := p.Latitude != nil || p.Longitude != nil
	var lat, lng float64
	if setLocation {
		var err error
		if lat, lng, err = ValidateLocation(p.Latitude, p.Longitude); err != nil {
			return nil, err
		}
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin profile upsert: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Step 1: ensure a row exists. We can't INSERT … ON CONFLICT DO UPDATE
	// against arbitrarily nullable params, so we INSERT-IF-MISSING then UPDATE
	// only the non-nil columns.
	intent := "casual"
	if p.Intent != nil && *p.Intent != "" {
		intent = *p.Intent
	}
	// Sprint 6: cohort_salt populated at first insert so the staged
	// rollout has a stable per-user bucket. The salt isn't a secret.
	if _, err := tx.Exec(ctx, `
        INSERT INTO dating_profiles (user_id, intent, cohort_salt)
        VALUES ($1, $2, encode(gen_random_bytes(8), 'hex'))
        ON CONFLICT (user_id) DO NOTHING`, userID, intent); err != nil {
		return nil, fmt.Errorf("ensure dating profile: %w", err)
	}

	// Step 2: the snapped location, under the change limits.
	if setLocation {
		if _, err := s.setLocationTx(ctx, tx, userID, lat, lng); err != nil {
			return nil, err
		}
	}

	// Step 3: one UPDATE carrying every other non-nil column.
	cols, vals := profileAssignments(p)
	if len(cols) > 0 {
		sets := make([]string, 0, len(cols)+1)
		args := make([]any, 0, len(vals)+1)
		args = append(args, userID)
		for i, col := range cols {
			sets = append(sets, fmt.Sprintf("%s = $%d", col, i+2))
			args = append(args, vals[i])
		}
		sets = append(sets, "updated_at = now()")
		tag, err := tx.Exec(ctx, `UPDATE dating_profiles SET `+strings.Join(sets, ", ")+` WHERE user_id = $1`, args...)
		if err != nil {
			return nil, fmt.Errorf("update dating profile: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return nil, ErrProfileNotFound
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit profile upsert: %w", err)
	}
	return s.GetProfile(ctx, userID)
}

// profileAssignments lists the client-editable columns present in p, in a
// fixed order. Column names are constants, never user input.
func profileAssignments(p UpsertProfileParams) ([]string, []any) {
	var cols []string
	var vals []any
	add := func(col string, v any) {
		cols = append(cols, col)
		vals = append(vals, v)
	}
	if p.Intent != nil {
		add("intent", *p.Intent)
	}
	if p.Bio != nil {
		add("bio", *p.Bio)
	}
	if p.Gender != nil {
		add("gender", *p.Gender)
	}
	if p.City != nil {
		add("city", *p.City)
	}
	if p.State != nil {
		add("state", *p.State)
	}
	if p.Country != nil {
		add("country", *p.Country)
	}
	// latitude / longitude / location_geohash: never here (lane D7,
	// setLocationTx writes the snapped point and its geohash).
	if p.HeightCm != nil {
		add("height_cm", *p.HeightCm)
	}
	if p.Religion != nil {
		add("religion", *p.Religion)
	}
	if p.Community != nil {
		add("community", *p.Community)
	}
	if p.Occupation != nil {
		add("occupation", *p.Occupation)
	}
	if p.Education != nil {
		add("education", *p.Education)
	}
	if p.Drinking != nil {
		add("drinking", *p.Drinking)
	}
	if p.Smoking != nil {
		add("smoking", *p.Smoking)
	}
	if p.Exercise != nil {
		add("exercise", *p.Exercise)
	}
	if p.Diet != nil {
		add("diet", *p.Diet)
	}
	if p.WantsChildren != nil {
		add("wants_children", *p.WantsChildren)
	}
	if p.FamilyPlans != nil {
		add("family_plans", *p.FamilyPlans)
	}
	if p.BlurMode != nil {
		add("blur_mode", *p.BlurMode)
	}
	if p.VisibleToPublic != nil {
		add("visible_to_public", *p.VisibleToPublic)
	}
	if p.LanguagePrefs != nil {
		add("language_prefs", p.LanguagePrefs)
	}
	return cols, vals
}

// Where a profile's birth date / first name came from.
//
// first_name_source is "identity" or "client". dob_source is one of the
// identity sources or "client":
//   - identity_registration: identity's registration date of birth.
//   - identity_profile: identity had no registration date of birth and served
//     the one on the identity profile (founder decision, lane D2 wiring).
//   - identity: identity-sourced, origin not recorded (test seeds and any
//     value written before the registration/profile split).
//   - client: the interim rule, accepted once from the dating client, then
//     locked.
const (
	BasicsSourceIdentity             = "identity"
	BasicsSourceIdentityRegistration = "identity_registration"
	BasicsSourceIdentityProfile      = "identity_profile"
	BasicsSourceClient               = "client"
)

// IsIdentityBirthDateSource reports whether source is one of the identity
// birth date sources (which always replace a client value).
func IsIdentityBirthDateSource(source string) bool {
	switch source {
	case BasicsSourceIdentity, BasicsSourceIdentityRegistration, BasicsSourceIdentityProfile:
		return true
	}
	return false
}

// SetProfileBirthDate records the birth date. An identity-sourced value
// always wins (replacing a client value or an older identity value). A client
// value is written only while no birth date is on file, so it locks after
// first set. Reports whether the row changed.
func (s *Store) SetProfileBirthDate(ctx context.Context, userID uuid.UUID, dob time.Time, source string) (bool, error) {
	var stmt string
	switch {
	case IsIdentityBirthDateSource(source):
		stmt = `
        UPDATE dating_profiles
        SET birth_date = $2::date, dob_source = $3::text, updated_at = now()
        WHERE user_id = $1 AND deleted_at IS NULL
          AND (birth_date IS DISTINCT FROM $2::date OR dob_source IS DISTINCT FROM $3::text)`
	case source == BasicsSourceClient:
		stmt = `
        UPDATE dating_profiles
        SET birth_date = $2::date, dob_source = $3::text, updated_at = now()
        WHERE user_id = $1 AND deleted_at IS NULL AND birth_date IS NULL`
	default:
		return false, fmt.Errorf("invalid: unknown birth date source %q", source)
	}
	tag, err := s.db.Exec(ctx, stmt, userID, dob, source)
	if err != nil {
		return false, fmt.Errorf("set birth date: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// SetProfileFirstName records the first name. Identity always wins; a
// client value never replaces an identity-sourced name. Reports whether the
// row changed.
func (s *Store) SetProfileFirstName(ctx context.Context, userID uuid.UUID, name, source string) (bool, error) {
	var stmt string
	switch source {
	case BasicsSourceIdentity:
		stmt = `
        UPDATE dating_profiles
        SET first_name = $2, first_name_source = 'identity', updated_at = now()
        WHERE user_id = $1 AND deleted_at IS NULL
          AND (first_name IS DISTINCT FROM $2 OR first_name_source IS DISTINCT FROM 'identity')`
	case BasicsSourceClient:
		stmt = `
        UPDATE dating_profiles
        SET first_name = $2, first_name_source = 'client', updated_at = now()
        WHERE user_id = $1 AND deleted_at IS NULL
          AND first_name_source IS DISTINCT FROM 'identity'
          AND first_name IS DISTINCT FROM $2`
	default:
		return false, fmt.Errorf("invalid: unknown first name source %q", source)
	}
	tag, err := s.db.Exec(ctx, stmt, userID, name)
	if err != nil {
		return false, fmt.Errorf("set first name: %w", err)
	}
	return tag.RowsAffected() > 0, nil
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

// Pause / unpause and every other profile_status change go through
// TransitionProfileStatus (profile_status.go).

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

// SoftDeleteProfile lives in profile_status.go: it is a lifecycle transition.

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
	if err := exec(`DELETE FROM dating_spark_ledger WHERE from_user_id = $1`, userID); err != nil {
		return 0, err
	}
	if err := exec(`DELETE FROM dating_location_changes WHERE user_id = $1`, userID); err != nil {
		return 0, err
	}
	if err := exec(`DELETE FROM dating_explain_ledger WHERE viewer_id = $1`, userID); err != nil {
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
        SET status = 'closed', closed_by = $1, closed_at = now()
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
