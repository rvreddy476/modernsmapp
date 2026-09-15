package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Internal identity read (dating lane D2).
//
// dating-service needs a user's first name and date of birth from their
// REGISTRATION, not from its own client. This is the read behind
// GET /internal/v1/profiles/users/:userId/identity.
//
// WHICH DATE OF BIRTH
//
// profile.profiles.dob is written at registration, but it is not a
// registration record: PUT /v1/profiles/me overwrites it with whatever the
// client sends, with no validation and no age check. Serving it as "the
// registration DOB" would hand dating exactly the client-controlled value it
// is trying to stop trusting.
//
// auth.registration_consents.declared_dob is the value the 18+ gate actually
// checked, written in the registration transaction and never updated. So the
// earliest consent row that carries a DOB wins. Only an account with no such
// row (one that predates consent capture) falls back to profile.profiles.dob,
// and DoBSource says so, so the caller can refuse that source if it chooses.
//
// first_name has no registration-only copy; it is profile.profiles.first_name,
// which the user can also edit.
//
// WHO IS WITHHELD
//
// An account that is deactivated, scheduled for deletion or purged reads as
// not found, as does one carrying a profile.hidden_profiles marker. That is
// the same "reads as nonexistent" rule every profile surface applies, and it
// keeps a DOB from being served for an account its owner has shut. suspended
// and pending_verification accounts still exist and are served; the caller
// owns its own eligibility rules.

// DoB sources reported alongside the date.
const (
	DoBSourceRegistration = "registration"
	DoBSourceProfile      = "profile"
	DoBSourceNone         = "none"
)

// IdentityBasics is the registration identity another service may read.
// Deliberately narrow: no email, phone, last name, username or gender.
type IdentityBasics struct {
	UserID    uuid.UUID
	FirstName string
	DoB       *time.Time
	DoBSource string
}

const identityBasicsQuery = `
	SELECT p.user_id,
	       COALESCE(p.first_name, ''),
	       rc.declared_dob,
	       p.dob
	FROM profile.profiles p
	JOIN auth.users u ON u.user_id = p.user_id
	LEFT JOIN LATERAL (
	    SELECT declared_dob
	    FROM auth.registration_consents
	    WHERE user_id = p.user_id AND declared_dob IS NOT NULL
	    ORDER BY accepted_at ASC, id ASC
	    LIMIT 1
	) rc ON TRUE
	WHERE p.user_id = $1
	  AND COALESCE(u.account_status, 'active') NOT IN ('deactivated', 'pending_deletion', 'purged')
	  AND NOT EXISTS (SELECT 1 FROM profile.hidden_profiles h WHERE h.user_id = p.user_id)`

const registrationDOBQuery = `
	SELECT declared_dob
	FROM auth.registration_consents
	WHERE user_id = $1 AND declared_dob IS NOT NULL
	ORDER BY accepted_at ASC, id ASC
	LIMIT 1`

// GetRegistrationDOB returns the date of birth declared at registration, or
// nil when the account has no consent row carrying one (it predates consent
// capture, or signed up through OAuth, which records no DOB). Same row choice
// as identityBasicsQuery: the earliest declaration wins, so a later
// re-acceptance cannot move it. No account-status filter: this backs the
// owner's own profile write, not a read served to another service.
func (s *Store) GetRegistrationDOB(ctx context.Context, userID uuid.UUID) (*time.Time, error) {
	var dob time.Time
	err := s.db.QueryRow(ctx, registrationDOBQuery, userID).Scan(&dob)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &dob, nil
}

// GetIdentityBasics returns nil, nil for an unknown or withheld account.
func (s *Store) GetIdentityBasics(ctx context.Context, userID uuid.UUID) (*IdentityBasics, error) {
	var (
		b          IdentityBasics
		registered *time.Time
		profileDOB *time.Time
	)
	err := s.db.QueryRow(ctx, identityBasicsQuery, userID).
		Scan(&b.UserID, &b.FirstName, &registered, &profileDOB)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	switch {
	case registered != nil:
		b.DoB, b.DoBSource = registered, DoBSourceRegistration
	case profileDOB != nil:
		b.DoB, b.DoBSource = profileDOB, DoBSourceProfile
	default:
		b.DoBSource = DoBSourceNone
	}
	return &b, nil
}
