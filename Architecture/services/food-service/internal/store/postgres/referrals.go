package postgres

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Referral mirrors food.referrals.
type Referral struct {
	ID         uuid.UUID `json:"id"`
	ReferrerID uuid.UUID `json:"referrer_id"`
	RefereeID  uuid.UUID `json:"referee_id"`
	CodeUsed   string    `json:"code_used"`
	Status     string    `json:"status"`
	RewardedAt *string   `json:"rewarded_at,omitempty"`
	CreatedAt  string    `json:"created_at"`
}

const referralAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789" // skip ambiguous chars
const referralCodeLen = 6

// generateReferralCode picks 6 random chars from a 31-char alphabet
// (excluding the visually-ambiguous 0/O/I/1/L). Collisions are
// retried at the insert site via ON CONFLICT.
func generateReferralCode() (string, error) {
	out := make([]byte, referralCodeLen)
	alphaLen := big.NewInt(int64(len(referralAlphabet)))
	for i := 0; i < referralCodeLen; i++ {
		n, err := rand.Int(rand.Reader, alphaLen)
		if err != nil {
			return "", err
		}
		out[i] = referralAlphabet[n.Int64()]
	}
	return string(out), nil
}

// EnsureReferralCode returns the user's persistent referral code,
// minting one if they don't have it yet. Idempotent.
func (s *Store) EnsureReferralCode(ctx context.Context, userID uuid.UUID) (string, error) {
	var existing string
	if err := s.db.QueryRow(ctx, `
		SELECT code FROM food.referral_codes WHERE user_id = $1
	`, userID).Scan(&existing); err == nil {
		return existing, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	// 5 attempts to find a unique code — collisions at 31^6 ≈ 887M
	// are vanishingly unlikely until the referral table is enormous.
	for attempt := 0; attempt < 5; attempt++ {
		code, err := generateReferralCode()
		if err != nil {
			return "", err
		}
		var inserted string
		err = s.db.QueryRow(ctx, `
			INSERT INTO food.referral_codes (user_id, code)
			VALUES ($1, $2)
			ON CONFLICT (user_id) DO NOTHING
			RETURNING code
		`, userID, code).Scan(&inserted)
		if err == nil {
			return inserted, nil
		}
		if errors.Is(err, pgx.ErrNoRows) {
			// Either the user already has a row (lost the race) or
			// the code collided. Re-read on the user-already path.
			if e := s.db.QueryRow(ctx, `
				SELECT code FROM food.referral_codes WHERE user_id = $1
			`, userID).Scan(&inserted); e == nil {
				return inserted, nil
			}
			continue // code collision — try a new one
		}
		return "", err
	}
	return "", fmt.Errorf("could not allocate unique referral code after retries")
}

// RecordReferral binds a referee to the referrer behind `code`. Refuses
// to bind a user to themselves and respects the UNIQUE (referee_id)
// constraint — one referrer per referee.
func (s *Store) RecordReferral(ctx context.Context, refereeID uuid.UUID, code string) (*Referral, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return nil, fmt.Errorf("invalid: empty code")
	}
	var referrerID uuid.UUID
	if err := s.db.QueryRow(ctx, `
		SELECT user_id FROM food.referral_codes WHERE code = $1
	`, code).Scan(&referrerID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("unknown referral code")
		}
		return nil, err
	}
	if referrerID == refereeID {
		return nil, fmt.Errorf("invalid: cannot self-refer")
	}
	var r Referral
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.referrals (referrer_id, referee_id, code_used)
		VALUES ($1, $2, $3)
		RETURNING id, referrer_id, referee_id, code_used, status, rewarded_at::text, created_at::text
	`, referrerID, refereeID, code).Scan(
		&r.ID, &r.ReferrerID, &r.RefereeID, &r.CodeUsed,
		&r.Status, &r.RewardedAt, &r.CreatedAt,
	); err != nil {
		// Unique violation on referee_id → already referred.
		return nil, fmt.Errorf("referee already referred")
	}
	return &r, nil
}

// MarkReferralRewarded promotes a pending referral once the referee
// places their first delivered order. Awards 200 loyalty points to
// each side. Idempotent at the row level (status != pending → skip).
func (s *Store) MarkReferralRewarded(ctx context.Context, refereeID uuid.UUID, rewardPoints int) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var refID uuid.UUID
	var referrerID uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT id, referrer_id FROM food.referrals
		WHERE referee_id = $1 AND status = 'pending'
		FOR UPDATE
	`, refereeID).Scan(&refID, &referrerID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // not referred or already rewarded
		}
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.referrals
		SET status = 'rewarded', rewarded_at = NOW()
		WHERE id = $1
	`, refID); err != nil {
		return err
	}
	for _, uid := range []uuid.UUID{referrerID, refereeID} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO food.loyalty_ledger (user_id, delta, reason)
			VALUES ($1, $2, 'referral_reward')
		`, uid, rewardPoints); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO food.loyalty_balances (user_id, points_balance, lifetime_earned, updated_at)
			VALUES ($1, $2, $2, NOW())
			ON CONFLICT (user_id) DO UPDATE
			SET points_balance  = food.loyalty_balances.points_balance + EXCLUDED.points_balance,
				lifetime_earned = food.loyalty_balances.lifetime_earned + EXCLUDED.lifetime_earned,
				updated_at      = NOW()
		`, uid, rewardPoints); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
