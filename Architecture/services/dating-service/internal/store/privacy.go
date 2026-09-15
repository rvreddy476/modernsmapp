// Privacy store — §P1-3 (PRODUCTION_GAP_ANALYSIS.md), lane D7.
//
// The privacy toggles live alongside the rest of the profile row so a
// single read serves both the discovery hard-filter pass and the response
// builder's masking logic. The columns are added via ADD COLUMN IF
// NOT EXISTS in setup.sql; this file is the data plane.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Privacy is the public contract for GET/PATCH /v1/dating/profile/privacy.
type Privacy struct {
	Incognito      bool `json:"incognito"`
	HideLastActive bool `json:"hide_last_active"`
	// ApproximateLocation is always true (lane D7): every location is stored
	// snapped and every distance is a bucket. Kept on the wire for older
	// clients; writing it has no effect.
	ApproximateLocation  bool `json:"approximate_location"`
	VerifiedOnlyFilter   bool `json:"verified_only_filter"`
	BlurPhotosUntilMatch bool `json:"blur_photos_until_match"`
	// EchoesConsent is the explicit opt-in for pulling main-app activity
	// (Echoes) into dating. Off for new profiles (lane D7).
	EchoesConsent bool `json:"echoes_consent"`
}

// PrivacyUpdate is the partial-update payload. nil = "no change".
type PrivacyUpdate struct {
	Incognito      *bool `json:"incognito,omitempty"`
	HideLastActive *bool `json:"hide_last_active,omitempty"`
	// ApproximateLocation is accepted and ignored (always on, lane D7).
	ApproximateLocation  *bool `json:"approximate_location,omitempty"`
	VerifiedOnlyFilter   *bool `json:"verified_only_filter,omitempty"`
	BlurPhotosUntilMatch *bool `json:"blur_photos_until_match,omitempty"`
	EchoesConsent        *bool `json:"echoes_consent,omitempty"`
}

// GetPrivacy returns the caller's current privacy settings. Returns
// ErrProfileNotFound when no profile row exists.
func (s *Store) GetPrivacy(ctx context.Context, userID uuid.UUID) (*Privacy, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user_id required")
	}
	p := &Privacy{ApproximateLocation: true}
	err := s.db.QueryRow(ctx, `
        SELECT incognito, hide_last_active, verified_only_filter,
               blur_photos_until_match, echoes_consent
        FROM dating_profiles
        WHERE user_id = $1 AND deleted_at IS NULL`, userID).Scan(
		&p.Incognito, &p.HideLastActive, &p.VerifiedOnlyFilter,
		&p.BlurPhotosUntilMatch, &p.EchoesConsent,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProfileNotFound
		}
		return nil, fmt.Errorf("get privacy: %w", err)
	}
	return p, nil
}

// UpdatePrivacy applies the partial update and returns the full,
// post-update Privacy row. Nil-valued fields are not written, so the
// PATCH endpoint can advertise partial-update semantics.
func (s *Store) UpdatePrivacy(ctx context.Context, userID uuid.UUID, u PrivacyUpdate) (*Privacy, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user_id required")
	}
	// Per-column UPDATE matches the rest of the profile store. We
	// could collapse into a single statement with COALESCE($n, col)
	// but that requires every column to be set on every call — the
	// per-column shape preserves "nil = untouched" without dragging
	// the existing value into every PATCH round-trip.
	if u.Incognito != nil {
		if _, err := s.db.Exec(ctx, `
            UPDATE dating_profiles
            SET incognito = $2, updated_at = now()
            WHERE user_id = $1 AND deleted_at IS NULL`, userID, *u.Incognito); err != nil {
			return nil, fmt.Errorf("set incognito: %w", err)
		}
	}
	if u.HideLastActive != nil {
		if _, err := s.db.Exec(ctx, `
            UPDATE dating_profiles
            SET hide_last_active = $2, updated_at = now()
            WHERE user_id = $1 AND deleted_at IS NULL`, userID, *u.HideLastActive); err != nil {
			return nil, fmt.Errorf("set hide_last_active: %w", err)
		}
	}
	if u.VerifiedOnlyFilter != nil {
		if _, err := s.db.Exec(ctx, `
            UPDATE dating_profiles
            SET verified_only_filter = $2, updated_at = now()
            WHERE user_id = $1 AND deleted_at IS NULL`, userID, *u.VerifiedOnlyFilter); err != nil {
			return nil, fmt.Errorf("set verified_only_filter: %w", err)
		}
	}
	if u.BlurPhotosUntilMatch != nil {
		if _, err := s.db.Exec(ctx, `
            UPDATE dating_profiles
            SET blur_photos_until_match = $2, updated_at = now()
            WHERE user_id = $1 AND deleted_at IS NULL`, userID, *u.BlurPhotosUntilMatch); err != nil {
			return nil, fmt.Errorf("set blur_photos_until_match: %w", err)
		}
	}
	if u.EchoesConsent != nil {
		if _, err := s.db.Exec(ctx, `
            UPDATE dating_profiles
            SET echoes_consent = $2, updated_at = now()
            WHERE user_id = $1 AND deleted_at IS NULL`, userID, *u.EchoesConsent); err != nil {
			return nil, fmt.Errorf("set echoes_consent: %w", err)
		}
		if !*u.EchoesConsent {
			// Withdrawing consent drops the snapshot already pulled in.
			if _, err := s.db.Exec(ctx, `DELETE FROM dating_echo_cache WHERE user_id = $1`, userID); err != nil {
				return nil, fmt.Errorf("drop echo cache: %w", err)
			}
		}
	}
	return s.GetPrivacy(ctx, userID)
}

// HasViewerSparked reports whether `viewer` has previously sent a spark
// to `target`. Used by the §P1-3 incognito gate — incognito candidates
// remain hidden unless the viewer has shown explicit prior interest.
func (s *Store) HasViewerSparked(ctx context.Context, viewer, target uuid.UUID) (bool, error) {
	if viewer == uuid.Nil || target == uuid.Nil {
		return false, fmt.Errorf("invalid: user ids required")
	}
	var exists bool
	err := s.db.QueryRow(ctx, `
        SELECT EXISTS (
            SELECT 1 FROM dating_sparks
            WHERE from_user_id = $1 AND to_user_id = $2
        )`, viewer, target).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("has viewer sparked: %w", err)
	}
	return exists, nil
}

// ListActiveMatchPartnerIDs returns the set of other-user-ids that share
// an active match with `viewer` (status IN matched|conversing|quiet).
// Used by the §P1-3 blur-photos-until-match gate — matched viewers see
// the original photo URL regardless of the candidate's blur setting.
func (s *Store) ListActiveMatchPartnerIDs(ctx context.Context, viewer uuid.UUID) (map[uuid.UUID]struct{}, error) {
	if viewer == uuid.Nil {
		return nil, fmt.Errorf("invalid: user_id required")
	}
	rows, err := s.db.Query(ctx, `
        SELECT CASE WHEN user_a = $1 THEN user_b ELSE user_a END
        FROM dating_matches
        WHERE (user_a = $1 OR user_b = $1)
          AND status IN ('matched','conversing','quiet')`, viewer)
	if err != nil {
		return nil, fmt.Errorf("list match partners: %w", err)
	}
	defer rows.Close()
	out := make(map[uuid.UUID]struct{}, 8)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan match partner: %w", err)
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}

// Distance buckets moved to geo.go (DistanceBucketFor) with the lane D7 codes.
