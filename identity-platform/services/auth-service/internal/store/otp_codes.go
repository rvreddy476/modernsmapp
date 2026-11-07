// auth.otp_codes: saving, reading, counting attempts on and deleting
// one-time codes. Pure move out of users.go; behaviour is unchanged.

package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

// --- auth.otp_codes ---

// SaveOTP stores the ONE live code for (phone, purpose), replacing any other.
//
// M2 (registration audit): this was a DELETE then an INSERT, two autocommit
// statements, over a non-unique index. Two concurrent saves (the registration
// fast path and the relay, or two resends) could leave two rows, and GetOTP
// returned whichever Postgres found first, so the code in the user's newest
// email was not necessarily the one that worked. It is now one upsert against
// UNIQUE (phone, purpose) (uq_otp_codes_phone_purpose): exactly one row, and
// the last writer's code is the live one.
//
// attempts restarts at 0 for the new code. What stops a resend from buying
// more guesses is the per-ACCOUNT budget in the service (allowAttempt), which
// lives in its own window and is not touched here.
func (s *Store) SaveOTP(ctx context.Context, phone, code, purpose string, ttl time.Duration) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(ctx, `
		INSERT INTO auth.otp_codes (id, phone, otp_hash, purpose, expires_at, attempts, created_at)
		VALUES ($1, $2, $3, $4, $5, 0, NOW())
		ON CONFLICT (phone, purpose) DO UPDATE
		SET id = EXCLUDED.id,
		    otp_hash = EXCLUDED.otp_hash,
		    expires_at = EXCLUDED.expires_at,
		    attempts = 0,
		    created_at = NOW()
	`, uuid.New(), phone, string(hash), purpose, time.Now().Add(ttl))
	return err
}

func (s *Store) GetOTP(ctx context.Context, phone, purpose string) (*OTP, error) {
	var o OTP
	err := s.db.QueryRow(ctx, `
		SELECT id, phone, otp_hash, purpose, expires_at, attempts
		FROM auth.otp_codes
		WHERE phone = $1 AND purpose = $2 AND expires_at > NOW()
		ORDER BY created_at DESC
		LIMIT 1
	`, phone, purpose).Scan(&o.ID, &o.Phone, &o.Hash, &o.Purpose, &o.ExpiresAt, &o.Attempts)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &o, nil
}

func (s *Store) IncrementOTPAttempts(ctx context.Context, id uuid.UUID) (int, error) {
	var attempts int
	err := s.db.QueryRow(ctx, `
		UPDATE auth.otp_codes
		SET attempts = attempts + 1
		WHERE id = $1
		RETURNING attempts
	`, id).Scan(&attempts)
	if err != nil {
		return 0, err
	}
	return attempts, nil
}

func (s *Store) DeleteOTP(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM auth.otp_codes WHERE id = $1
	`, id)
	return err
}
