package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Module 3 CLB-3 — the credential that lets a PENDING account finish signing up.
//
// WHAT WAS BROKEN
//
// LB-5 made registration create a pending account and issue no session, which
// is correct. But /verify-email and /resend-verification were registered under
// the authenticated group and each derived the pending user from X-User-Id —
// a header only a verified bearer session causes the gateway to set. So the
// user received a code and had no route to submit it, and if the first send
// failed or the app restarted, the documented resend path was unreachable too.
// The signup loop terminated at "check your email" for every real user.
//
// WHY NOT JUST TRUST X-User-Id ON A PUBLIC ROUTE
//
// Because then anyone could activate anyone: POST the victim's user id and a
// guessed code, forever, with no session and no rate limit that means anything.
// The identity has to come from something the server issued and can revoke.
//
// WHAT THIS IS, AND WHAT IT IS NOT
//
// It is an opaque 256-bit random string, stored only as a SHA-256 digest,
// bound to ONE user and ONE purpose, short-lived, and single-use at the moment
// it succeeds. It authorises exactly two operations — submit a code, resend a
// code — and nothing else.
//
// It is NOT an access token: it carries no scopes, is not a JWT, cannot be
// presented to any other endpoint, and is not accepted by the auth middleware.
// A stolen one lets the thief request that an email be re-sent to an address
// they cannot read.
//
// WHY SHA-256 AND NOT BCRYPT
//
// bcrypt exists to make LOW-entropy secrets expensive to guess; the OTP codes
// in this service are six digits and are hashed with it for exactly that
// reason. This value has 256 bits of entropy from crypto/rand, so there is no
// guessing attack for a work factor to slow down, and a fast digest keeps the
// lookup an indexed equality match instead of a scan-and-compare over every
// outstanding row.

// Verification transaction purposes. The purpose is part of the lookup, so a
// credential minted for one flow cannot be replayed into another.
const (
	VerificationPurposeEmail = "email_verify"
)

// VerificationTransactionTTL is deliberately generous enough to survive
// switching to the mail app and back, and short enough that a credential left
// in a log or a screenshot stops working the same day.
const VerificationTransactionTTL = 30 * time.Minute

// ErrVerificationTransactionInvalid covers every failure the caller is allowed
// to distinguish: unknown, expired, wrong purpose, already consumed, forged.
// They are ONE error on purpose — telling a caller which of those it was hands
// an attacker a way to enumerate valid credentials and account states.
var ErrVerificationTransactionInvalid = errors.New("verification transaction is invalid or expired")

// VerificationTransactionDDL creates the table.
const VerificationTransactionDDL = `
CREATE TABLE IF NOT EXISTS auth.verification_transactions (
	id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	user_id     UUID NOT NULL,
	purpose     VARCHAR(32) NOT NULL,
	token_hash  TEXT NOT NULL UNIQUE,
	expires_at  TIMESTAMPTZ NOT NULL,
	consumed_at TIMESTAMPTZ,
	created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_verification_tx_user
	ON auth.verification_transactions (user_id, purpose);
CREATE INDEX IF NOT EXISTS idx_verification_tx_expiry
	ON auth.verification_transactions (expires_at);
`

// VerificationTransaction is what the client is handed.
type VerificationTransaction struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func hashVerificationToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// newVerificationToken mints 256 bits from crypto/rand.
func newVerificationToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate verification transaction: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// CreateVerificationTransaction issues a credential for one user and purpose.
//
// Issuing a new one invalidates every outstanding credential for the same
// user and purpose. That is what makes the pending-login recovery path safe:
// a user who returns after losing the app gets exactly one live credential,
// and an older one that leaked somewhere stops working the moment they do.
func (s *Store) CreateVerificationTransaction(ctx context.Context, userID uuid.UUID, purpose string, ttl time.Duration) (*VerificationTransaction, error) {
	if ttl <= 0 {
		ttl = VerificationTransactionTTL
	}
	token, err := newVerificationToken()
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().Add(ttl)

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin verification transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`DELETE FROM auth.verification_transactions WHERE user_id = $1 AND purpose = $2`,
		userID, purpose); err != nil {
		return nil, fmt.Errorf("clear prior verification transactions: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO auth.verification_transactions (user_id, purpose, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)`,
		userID, purpose, hashVerificationToken(token), expiresAt); err != nil {
		return nil, fmt.Errorf("insert verification transaction: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit verification transaction: %w", err)
	}

	return &VerificationTransaction{Token: token, ExpiresAt: expiresAt}, nil
}

// LookupVerificationTransaction resolves a credential to its user WITHOUT
// consuming it. Used by resend, which may legitimately happen several times
// while the user waits for an email.
func (s *Store) LookupVerificationTransaction(ctx context.Context, token, purpose string) (uuid.UUID, error) {
	if token == "" {
		return uuid.Nil, ErrVerificationTransactionInvalid
	}
	var userID uuid.UUID
	err := s.db.QueryRow(ctx, `
		SELECT user_id FROM auth.verification_transactions
		WHERE token_hash = $1
		  AND purpose = $2
		  AND consumed_at IS NULL
		  AND expires_at > NOW()`,
		hashVerificationToken(token), purpose).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrVerificationTransactionInvalid
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("lookup verification transaction: %w", err)
	}
	return userID, nil
}

// ConsumeVerificationTransaction spends the credential exactly once.
//
// The consumed_at IS NULL predicate lives in the UPDATE, so two concurrent
// requests carrying the same credential cannot both succeed: PostgreSQL
// serialises the row update and the loser matches nothing. That is what makes
// "activates exactly once" true under a replay rather than merely likely.
func (s *Store) ConsumeVerificationTransaction(ctx context.Context, token, purpose string) (uuid.UUID, error) {
	if token == "" {
		return uuid.Nil, ErrVerificationTransactionInvalid
	}
	var userID uuid.UUID
	err := s.db.QueryRow(ctx, `
		UPDATE auth.verification_transactions
		SET consumed_at = NOW()
		WHERE token_hash = $1
		  AND purpose = $2
		  AND consumed_at IS NULL
		  AND expires_at > NOW()
		RETURNING user_id`,
		hashVerificationToken(token), purpose).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrVerificationTransactionInvalid
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("consume verification transaction: %w", err)
	}
	return userID, nil
}

// PurgeExpiredVerificationTransactions is the housekeeping call. Expired rows
// are already unusable — the expiry predicate is in every lookup — so this
// only reclaims space.
func (s *Store) PurgeExpiredVerificationTransactions(ctx context.Context) (int64, error) {
	ct, err := s.db.Exec(ctx,
		`DELETE FROM auth.verification_transactions WHERE expires_at < NOW() - INTERVAL '1 day'`)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}
