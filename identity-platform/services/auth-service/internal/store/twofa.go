// Two-factor authentication: the second-factor method, enabling and
// disabling TOTP or email 2FA, reading the TOTP secret, and the
// auth.recovery_codes that back TOTP.

package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// --- 2FA ---

// Second-factor methods (auth.users.two_factor_method). App users get email
// (founder decision, 10 Nov 2026); TOTP is offered only to admin-console
// accounts, and legacy consumers already enrolled in it keep it. "sms" is
// reserved for when an SMS provider exists.
const (
	TwoFactorMethodTOTP  = "totp"
	TwoFactorMethodEmail = "email"
	TwoFactorMethodSMS   = "sms"
)

// ErrTwoFactorStateChanged: a conditional 2FA write matched no row because
// the account's 2FA state changed underneath it (a concurrent enable).
var ErrTwoFactorStateChanged = errors.New("two-factor state changed")

// SecondFactorMethod is the account's live second factor: "" when 2FA is
// off, otherwise the stored method, with a legacy enabled row that predates
// the method column read as "totp".
func (u *User) SecondFactorMethod() string {
	if u == nil || !u.TwoFactorEnabled {
		return ""
	}
	if u.TwoFactorMethod == "" {
		return TwoFactorMethodTOTP
	}
	return u.TwoFactorMethod
}

// HasTOTP reports whether the account's second factor is an authenticator
// app. Every admin MFA decision reads this, never TwoFactorEnabled alone: an
// email-2FA account has 2FA on but no TOTP secret.
func (u *User) HasTOTP() bool { return u.SecondFactorMethod() == TwoFactorMethodTOTP }

// EnableEmail2FA turns 2FA on with the email method. Conditional on 2FA being
// off, so it can never overwrite (and silently drop) an enrolled TOTP secret;
// ErrTwoFactorStateChanged when it was already on.
func (s *Store) EnableEmail2FA(ctx context.Context, userID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE auth.users
		SET two_factor_enabled = TRUE,
			two_factor_method = 'email',
			two_factor_secret = NULL,
			two_factor_secret_encrypted = NULL,
			updated_at = NOW()
		WHERE user_id = $1 AND two_factor_enabled = FALSE
	`, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrTwoFactorStateChanged
	}
	return nil
}

// Enable2FA sets two_factor_enabled=true and stores the TOTP secret.
// When the AES-256-GCM box is wired (WithTOTPEncryption), the secret
// is sealed into two_factor_secret_encrypted and the plaintext column
// is cleared. Without the box, falls back to plaintext storage and
// returns no error — main.go emits a startup warning when the env is
// missing so this isn't silent in operation.
func (s *Store) Enable2FA(ctx context.Context, userID uuid.UUID, secret string) error {
	if s.totpCrypt != nil {
		ct, err := s.totpCrypt.Seal([]byte(secret))
		if err != nil {
			return fmt.Errorf("seal totp: %w", err)
		}
		_, err = s.db.Exec(ctx, `
			UPDATE auth.users
			SET two_factor_enabled = TRUE,
				two_factor_method = 'totp',
				two_factor_secret_encrypted = $1,
				two_factor_secret = NULL,
				updated_at = NOW()
			WHERE user_id = $2
		`, ct, userID)
		return err
	}
	_, err := s.db.Exec(ctx, `
		UPDATE auth.users
		SET two_factor_enabled = TRUE,
			two_factor_method = 'totp',
			two_factor_secret = $1,
			two_factor_secret_encrypted = NULL,
			updated_at = NOW()
		WHERE user_id = $2
	`, secret, userID)
	return err
}

// Disable2FA turns 2FA off whatever the method, clearing the method and both
// the encrypted and plaintext TOTP columns.
func (s *Store) Disable2FA(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE auth.users
		SET two_factor_enabled = FALSE,
			two_factor_method = '',
			two_factor_secret = NULL,
			two_factor_secret_encrypted = NULL,
			updated_at = NOW()
		WHERE user_id = $1
	`, userID)
	return err
}

// Get2FASecret returns the TOTP secret for a user. Prefers the
// encrypted column when populated; falls back to the plaintext
// column for rows that predate the cutover. Returns "" + nil when
// neither column is set so callers can check "no 2FA configured"
// without an error.
func (s *Store) Get2FASecret(ctx context.Context, userID uuid.UUID) (string, error) {
	var enc []byte
	var plain *string
	err := s.db.QueryRow(ctx, `
		SELECT two_factor_secret_encrypted, two_factor_secret
		FROM auth.users WHERE user_id = $1
	`, userID).Scan(&enc, &plain)
	if err != nil {
		return "", err
	}
	if len(enc) > 0 {
		if s.totpCrypt == nil {
			return "", fmt.Errorf("totp: encryption key not configured but encrypted secret present")
		}
		pt, derr := s.totpCrypt.Open(enc)
		if derr != nil {
			return "", fmt.Errorf("open totp: %w", derr)
		}
		return string(pt), nil
	}
	if plain == nil {
		return "", nil
	}
	return *plain, nil
}

// --- auth.recovery_codes ---

// StoreRecoveryCodes inserts hashed recovery codes into Postgres for the given user,
// replacing any existing unused codes.
func (s *Store) StoreRecoveryCodes(ctx context.Context, userID uuid.UUID, codeHashes []string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Remove existing unused codes
	if _, err := tx.Exec(ctx,
		`DELETE FROM auth.recovery_codes WHERE user_id = $1 AND used_at IS NULL`,
		userID,
	); err != nil {
		return fmt.Errorf("delete old recovery codes: %w", err)
	}

	for _, h := range codeHashes {
		if _, err := tx.Exec(ctx,
			`INSERT INTO auth.recovery_codes (user_id, code_hash) VALUES ($1, $2)`,
			userID, h,
		); err != nil {
			return fmt.Errorf("insert recovery code: %w", err)
		}
	}

	return tx.Commit(ctx)
}

// GetUnusedRecoveryCodes returns all unused recovery code hashes for a user.
func (s *Store) GetUnusedRecoveryCodes(ctx context.Context, userID uuid.UUID) ([]RecoveryCode, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, code_hash FROM auth.recovery_codes WHERE user_id = $1 AND used_at IS NULL`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []RecoveryCode
	for rows.Next() {
		var r RecoveryCode
		if err := rows.Scan(&r.ID, &r.CodeHash); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// MarkRecoveryCodeUsed sets used_at = NOW() for a specific recovery code row.
func (s *Store) MarkRecoveryCodeUsed(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE auth.recovery_codes SET used_at = NOW() WHERE id = $1`,
		id,
	)
	return err
}
