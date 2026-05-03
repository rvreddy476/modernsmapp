// Package store — Verifications.
//
// DPDP Act compliant — see PULSE_DATING_SPEC.md §15.8
// We persist *no* Aadhaar number. Only:
//   - selfie_status / selfie_score / selfie_at  (face-match attempt result)
//   - aadhaar_status / aadhaar_at               (DigiLocker outcome flag)
//   - digilocker_ref                            (opaque assertion id from Setu/Signzy)
//   - doc_type_hash                             (SHA-256 of the doc-type label)
//
// trust_tier on dating_profiles is the user-visible signal. It can step up
// phone -> selfie -> aadhaar but never demote.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Verification is one row of dating_verifications.
//
// DPDP Act compliant — see PULSE_DATING_SPEC.md §15.8
// No Aadhaar number field exists on this struct, by design.
type Verification struct {
	UserID        uuid.UUID  `json:"user_id"`
	SelfieStatus  *string    `json:"selfie_status,omitempty"`
	SelfieScore   *float64   `json:"selfie_score,omitempty"`
	SelfieAt      *time.Time `json:"selfie_at,omitempty"`
	AadhaarStatus *string    `json:"aadhaar_status,omitempty"`
	AadhaarAt     *time.Time `json:"aadhaar_at,omitempty"`
	DigilockerRef *string    `json:"digilocker_ref,omitempty"`
	DocTypeHash   *string    `json:"doc_type_hash,omitempty"`
}

// ErrVerificationNotFound is returned when no verification row exists.
var ErrVerificationNotFound = errors.New("not_found: verification not found")

const verificationCols = `user_id, selfie_status, selfie_score, selfie_at,
    aadhaar_status, aadhaar_at, digilocker_ref, doc_type_hash`

func scanVerification(row pgx.Row) (*Verification, error) {
	v := &Verification{}
	if err := row.Scan(
		&v.UserID, &v.SelfieStatus, &v.SelfieScore, &v.SelfieAt,
		&v.AadhaarStatus, &v.AadhaarAt, &v.DigilockerRef, &v.DocTypeHash,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrVerificationNotFound
		}
		return nil, fmt.Errorf("scan verification: %w", err)
	}
	return v, nil
}

// RecordSelfieAttempt upserts the selfie verification outcome for userID.
// status must be one of: pending | passed | failed.
func (s *Store) RecordSelfieAttempt(ctx context.Context, userID uuid.UUID, score float64, status string) error {
	if userID == uuid.Nil {
		return fmt.Errorf("invalid: user_id required")
	}
	switch status {
	case "pending", "passed", "failed":
	default:
		return fmt.Errorf("invalid: selfie status must be pending|passed|failed")
	}
	_, err := s.db.Exec(ctx, `
        INSERT INTO dating_verifications (user_id, selfie_status, selfie_score, selfie_at)
        VALUES ($1, $2, $3, now())
        ON CONFLICT (user_id) DO UPDATE
            SET selfie_status = EXCLUDED.selfie_status,
                selfie_score  = EXCLUDED.selfie_score,
                selfie_at     = EXCLUDED.selfie_at`,
		userID, status, score)
	if err != nil {
		return fmt.Errorf("record selfie attempt: %w", err)
	}
	return nil
}

// RecordAadhaarVerification persists the DigiLocker outcome for userID.
//
// DPDP Act compliant — see PULSE_DATING_SPEC.md §15.8
// digilockerRef is the opaque partner assertion id; docTypeHash is the
// SHA-256 of the document-type label (e.g. "AADHAAR-XML"). The Aadhaar
// number itself is never accepted by this method.
func (s *Store) RecordAadhaarVerification(ctx context.Context, userID uuid.UUID, digilockerRef, docTypeHash string) error {
	if userID == uuid.Nil {
		return fmt.Errorf("invalid: user_id required")
	}
	if digilockerRef == "" {
		return fmt.Errorf("invalid: digilocker_ref required")
	}
	if docTypeHash == "" {
		return fmt.Errorf("invalid: doc_type_hash required")
	}
	_, err := s.db.Exec(ctx, `
        INSERT INTO dating_verifications (user_id, aadhaar_status, aadhaar_at, digilocker_ref, doc_type_hash)
        VALUES ($1, 'verified', now(), $2, $3)
        ON CONFLICT (user_id) DO UPDATE
            SET aadhaar_status = 'verified',
                aadhaar_at     = now(),
                digilocker_ref = EXCLUDED.digilocker_ref,
                doc_type_hash  = EXCLUDED.doc_type_hash`,
		userID, digilockerRef, docTypeHash)
	if err != nil {
		return fmt.Errorf("record aadhaar verification: %w", err)
	}
	return nil
}

// GetVerification fetches a row or returns ErrVerificationNotFound.
func (s *Store) GetVerification(ctx context.Context, userID uuid.UUID) (*Verification, error) {
	row := s.db.QueryRow(ctx, `SELECT `+verificationCols+` FROM dating_verifications WHERE user_id = $1`, userID)
	return scanVerification(row)
}

// UpdateTrustTier sets dating_profiles.trust_tier. Allowed values:
// phone | selfie | aadhaar. Never demotes — a higher tier wins.
func (s *Store) UpdateTrustTier(ctx context.Context, userID uuid.UUID, tier string) error {
	if userID == uuid.Nil {
		return fmt.Errorf("invalid: user_id required")
	}
	switch tier {
	case "phone", "selfie", "aadhaar":
	default:
		return fmt.Errorf("invalid: trust_tier must be phone|selfie|aadhaar")
	}
	rank := func(t string) int {
		switch t {
		case "phone":
			return 1
		case "selfie":
			return 2
		case "aadhaar":
			return 3
		}
		return 0
	}
	tag, err := s.db.Exec(ctx, `
        UPDATE dating_profiles
        SET trust_tier = $2, updated_at = now()
        WHERE user_id = $1
          AND CASE trust_tier
              WHEN 'phone'   THEN 1
              WHEN 'selfie'  THEN 2
              WHEN 'aadhaar' THEN 3
              ELSE 0
          END < $3`, userID, tier, rank(tier))
	if err != nil {
		return fmt.Errorf("update trust tier: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Either no profile or already at-or-above the requested tier.
		// We treat that as a non-error — the caller's invariant is "at
		// least this tier", which is already met.
		return nil
	}
	return nil
}
