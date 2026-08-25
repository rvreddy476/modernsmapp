package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Module 3 M3-P0-3 / LB-5 — pending activation, consent, atomic recovery.
//
// Three defects, all of them silent:
//
//  1. Registration created the account ACTIVE and returned access and refresh
//     tokens immediately, with no verification challenge sent. An address
//     could be registered without its owner ever being contacted — someone
//     else's email became a working account and the real owner never learned.
//  2. The accepted terms version was validated in memory and thrown away.
//     `auth.users` has consent booleans that registration never wrote, so for
//     every account the answer to "did they accept anything, and which text?"
//     was nothing. Under the DPDP Act that answer IS the lawful basis.
//  3. Password recovery deleted the code before validating the new password
//     and updated the password before revoking sessions, so a failure between
//     those steps left the user unable to retry with a consumed code, or with
//     a changed password and an attacker's session still live.

// AccountStatusPendingVerification is the state a new account starts in. No
// session is issued until one-time email verification promotes it.
const AccountStatusPendingVerification = "pending_verification"

// AccountStatusActive is a usable account.
const AccountStatusActive = "active"

// ErrAccountNotPending is returned when activation is attempted on an account
// that is not awaiting verification — already active, or deactivated.
var ErrAccountNotPending = errors.New("account is not awaiting verification")

// RegistrationConsent is the record written in the registration transaction.
type RegistrationConsent struct {
	TermsVersion    string
	AcceptedTerms   bool
	AcceptedPrivacy bool
	DeclaredDOB     *time.Time
	IP              string
	UserAgent       string
}

// SetAccountPendingTx marks a newly created account as awaiting verification.
//
// Called inside the registration transaction, so an account is never briefly
// active: either the whole registration commits with the account pending, or
// nothing exists.
func (s *Store) SetAccountPendingTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE auth.users
		SET account_status = $2, updated_at = NOW()
		WHERE user_id = $1`, userID, AccountStatusPendingVerification)
	return err
}

// RecordConsentTx writes the consent record and the matching boolean columns.
//
// Both, deliberately: the booleans are what existing code reads, and the row
// is what an audit needs — it carries the VERSION, which a boolean cannot.
func (s *Store) RecordConsentTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, c RegistrationConsent) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO auth.registration_consents
			(user_id, terms_version, accepted_terms, accepted_privacy, declared_dob, ip, user_agent)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		userID, c.TermsVersion, c.AcceptedTerms, c.AcceptedPrivacy,
		c.DeclaredDOB, nullIfEmpty(c.IP), nullIfEmpty(c.UserAgent)); err != nil {
		return fmt.Errorf("record consent: %w", err)
	}

	// consent_age reflects the 18+ self-declaration the age gate enforced.
	if _, err := tx.Exec(ctx, `
		UPDATE auth.users
		SET consent_terms = $2, consent_privacy = $3, consent_age = TRUE, updated_at = NOW()
		WHERE user_id = $1`,
		userID, c.AcceptedTerms, c.AcceptedPrivacy); err != nil {
		return fmt.Errorf("update consent flags: %w", err)
	}
	return nil
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ActivateVerifiedAccount promotes a pending account to active and marks the
// email verified, in ONE transaction.
//
// Returns ErrAccountNotPending when the account was not awaiting verification,
// which is what makes activation ONE-TIME: a replayed verification finds the
// account already active and changes nothing.
func (s *Store) ActivateVerifiedAccount(ctx context.Context, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("activate: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	ct, err := tx.Exec(ctx, `
		UPDATE auth.users
		SET account_status = $2, email_verified = TRUE, updated_at = NOW()
		WHERE user_id = $1 AND account_status = $3`,
		userID, AccountStatusActive, AccountStatusPendingVerification)
	if err != nil {
		return fmt.Errorf("activate: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrAccountNotPending
	}

	// The app-side projection must not stay pending either, or the account is
	// active for login and invisible to every other service.
	if _, err := tx.Exec(ctx, `
		UPDATE usr.users SET status = 'active', updated_at = NOW() WHERE id = $1`,
		userID); err != nil {
		return fmt.Errorf("activate projection: %w", err)
	}

	return tx.Commit(ctx)
}

// IsAccountPendingVerification reports whether login must be refused.
func (s *Store) IsAccountPendingVerification(ctx context.Context, userID uuid.UUID) (bool, error) {
	var status string
	err := s.db.QueryRow(ctx,
		`SELECT account_status FROM auth.users WHERE user_id = $1`, userID).Scan(&status)
	if err != nil {
		return false, err
	}
	return status == AccountStatusPendingVerification, nil
}

// ── Atomic password recovery ────────────────────────────────────────────────

// ErrRecoveryCodeInvalid means the code was wrong, expired, or already used.
var ErrRecoveryCodeInvalid = errors.New("invalid or expired code")

// ConsumeRecoveryAndSetPassword performs the whole recovery in ONE transaction:
// consume the one-time code, set the new password, and revoke every session.
//
// LB-5: these were three independent statements in the wrong order. The code
// was deleted BEFORE the new password was validated, so a weak-password
// rejection left the user with a consumed code and no way to retry. And the
// password was committed BEFORE sessions were revoked, so a crash in between
// left the account with a new password and the attacker's session still live —
// the exact opposite of what a password reset is for.
//
// verify is called with the stored hash while the row is locked, so a
// concurrent second attempt cannot consume the same code.
func (s *Store) ConsumeRecoveryAndSetPassword(
	ctx context.Context,
	userID uuid.UUID,
	otpKey, purpose string,
	verify func(storedHash string) bool,
	newPasswordHash string,
) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("recovery: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// FOR UPDATE locks the code row for the duration of the transaction, so
	// two concurrent resets cannot both consume it.
	var otpID uuid.UUID
	var otpHash string
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT id, otp_hash, expires_at
		FROM auth.otp_codes
		WHERE phone = $1 AND purpose = $2
		ORDER BY created_at DESC
		LIMIT 1
		FOR UPDATE`, otpKey, purpose).Scan(&otpID, &otpHash, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRecoveryCodeInvalid
	}
	if err != nil {
		return fmt.Errorf("recovery: read code: %w", err)
	}
	if time.Now().After(expiresAt) {
		return ErrRecoveryCodeInvalid
	}
	if !verify(otpHash) {
		// Wrong code: the row is NOT consumed, so the user can retry with the
		// real one. Attempt limiting is handled by the rate limiter in front.
		return ErrRecoveryCodeInvalid
	}

	// From here everything commits together or not at all.
	if _, err := tx.Exec(ctx, `DELETE FROM auth.otp_codes WHERE id = $1`, otpID); err != nil {
		return fmt.Errorf("recovery: consume code: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE auth.users SET password_hash = $2, updated_at = NOW()
		WHERE user_id = $1`, userID, newPasswordHash); err != nil {
		return fmt.Errorf("recovery: set password: %w", err)
	}
	// Revoking IN THE SAME transaction is the point: a password change that
	// commits without the revocation leaves an attacker's session alive.
	if _, err := tx.Exec(ctx, `
		UPDATE auth.sessions SET is_active = FALSE, revoked_at = NOW()
		WHERE user_id = $1 AND is_active = TRUE`, userID); err != nil {
		return fmt.Errorf("recovery: revoke sessions: %w", err)
	}

	return tx.Commit(ctx)
}
