package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// LoyaltyBalance mirrors food.loyalty_balances.
type LoyaltyBalance struct {
	UserID         uuid.UUID `json:"user_id"`
	PointsBalance  int       `json:"points_balance"`
	LifetimeEarned int       `json:"lifetime_earned"`
	Tier           string    `json:"tier"`
}

// LoyaltyLedgerRow mirrors food.loyalty_ledger.
type LoyaltyLedgerRow struct {
	ID        uuid.UUID  `json:"id"`
	UserID    uuid.UUID  `json:"user_id"`
	OrderID   *uuid.UUID `json:"order_id,omitempty"`
	Delta     int        `json:"delta"`
	Reason    string     `json:"reason"`
	CreatedAt string     `json:"created_at"`
}

// computeTier maps lifetime_earned → tier label. Thresholds are tuned
// for FiGo's order economics; revise via env later if needed.
func computeTier(lifetime int) string {
	switch {
	case lifetime >= 10000:
		return "platinum"
	case lifetime >= 5000:
		return "gold"
	case lifetime >= 1000:
		return "silver"
	default:
		return "bronze"
	}
}

// GetLoyaltyBalance returns the current balance + computed tier. New
// users default to (0, 0, bronze) without a row insertion.
func (s *Store) GetLoyaltyBalance(ctx context.Context, userID uuid.UUID) (*LoyaltyBalance, error) {
	b := LoyaltyBalance{UserID: userID}
	if err := s.db.QueryRow(ctx, `
		SELECT COALESCE(points_balance, 0), COALESCE(lifetime_earned, 0)
		FROM food.loyalty_balances WHERE user_id = $1
	`, userID).Scan(&b.PointsBalance, &b.LifetimeEarned); err != nil {
		if err != pgx.ErrNoRows {
			return nil, err
		}
	}
	b.Tier = computeTier(b.LifetimeEarned)
	return &b, nil
}

// EarnPoints records a +delta on order completion. Idempotent on
// (user_id, order_id, reason) — re-running the worker for the same
// order is a no-op rather than double-credit. Returns the post-write
// balance.
func (s *Store) EarnPoints(ctx context.Context, userID, orderID uuid.UUID, delta int, reason string) (*LoyaltyBalance, error) {
	if delta <= 0 {
		return nil, fmt.Errorf("invalid: delta must be > 0")
	}
	if strings.TrimSpace(reason) == "" {
		reason = "order_delivered"
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	// Idempotency check.
	var existing int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM food.loyalty_ledger
		WHERE user_id = $1 AND order_id = $2 AND reason = $3 AND delta > 0
	`, userID, orderID, reason).Scan(&existing); err != nil {
		return nil, err
	}
	if existing > 0 {
		// No-op replay; just return the current state.
		var b LoyaltyBalance
		b.UserID = userID
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(points_balance, 0), COALESCE(lifetime_earned, 0)
			FROM food.loyalty_balances WHERE user_id = $1
		`, userID).Scan(&b.PointsBalance, &b.LifetimeEarned); err != nil && err != pgx.ErrNoRows {
			return nil, err
		}
		b.Tier = computeTier(b.LifetimeEarned)
		return &b, tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO food.loyalty_ledger (user_id, order_id, delta, reason)
		VALUES ($1, $2, $3, $4)
	`, userID, orderID, delta, reason); err != nil {
		return nil, err
	}
	var b LoyaltyBalance
	if err := tx.QueryRow(ctx, `
		INSERT INTO food.loyalty_balances (user_id, points_balance, lifetime_earned, updated_at)
		VALUES ($1, $2, $2, NOW())
		ON CONFLICT (user_id) DO UPDATE
		SET points_balance  = food.loyalty_balances.points_balance + EXCLUDED.points_balance,
			lifetime_earned = food.loyalty_balances.lifetime_earned + EXCLUDED.lifetime_earned,
			updated_at      = NOW()
		RETURNING user_id, points_balance, lifetime_earned
	`, userID, delta).Scan(&b.UserID, &b.PointsBalance, &b.LifetimeEarned); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	b.Tier = computeTier(b.LifetimeEarned)
	return &b, nil
}

// RedeemPoints debits the balance. Refuses to go negative. Reason is
// usually "redeemed_at_checkout".
func (s *Store) RedeemPoints(ctx context.Context, userID uuid.UUID, orderID *uuid.UUID, delta int, reason string) (*LoyaltyBalance, error) {
	if delta <= 0 {
		return nil, fmt.Errorf("invalid: delta must be > 0")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var current int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(points_balance, 0)
		FROM food.loyalty_balances WHERE user_id = $1 FOR UPDATE
	`, userID).Scan(&current); err != nil && err != pgx.ErrNoRows {
		return nil, err
	}
	if current < delta {
		return nil, fmt.Errorf("insufficient points: balance %d, requested %d", current, delta)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO food.loyalty_ledger (user_id, order_id, delta, reason)
		VALUES ($1, $2, -$3, $4)
	`, userID, orderID, delta, reason); err != nil {
		return nil, err
	}
	var b LoyaltyBalance
	if err := tx.QueryRow(ctx, `
		UPDATE food.loyalty_balances
		SET points_balance = points_balance - $2, updated_at = NOW()
		WHERE user_id = $1
		RETURNING user_id, points_balance, lifetime_earned
	`, userID, delta).Scan(&b.UserID, &b.PointsBalance, &b.LifetimeEarned); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	b.Tier = computeTier(b.LifetimeEarned)
	return &b, nil
}

// ListLoyaltyLedger returns the most-recent N ledger rows for a user.
func (s *Store) ListLoyaltyLedger(ctx context.Context, userID uuid.UUID, limit int) ([]LoyaltyLedgerRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, order_id, delta, reason, created_at::text
		FROM food.loyalty_ledger
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LoyaltyLedgerRow
	for rows.Next() {
		var r LoyaltyLedgerRow
		if err := rows.Scan(&r.ID, &r.UserID, &r.OrderID, &r.Delta, &r.Reason, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
