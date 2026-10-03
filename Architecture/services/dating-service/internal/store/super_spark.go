// Super Spark store — mechanic M3.
//
// A Super Spark is an ordinary dating_sparks row with is_super set. What makes
// it scarce is the charge, taken inside the spark's own transaction
// (CreateSparkWithOptions): the daily allowance first, then one purchased
// Super Spark from dating_super_spark_balances.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SuperSparkQuotaWindow is the rolling window the daily allowance counts.
const SuperSparkQuotaWindow = 24 * time.Hour

// ErrSuperSparkLimited: the daily allowance is used and no purchased Super
// Spark is left.
var ErrSuperSparkLimited = errors.New("rate_limited: super spark limit reached")

// Super Spark ledger sources.
const (
	SuperSparkSourceDaily = "daily"
	SuperSparkSourcePack  = "pack"
)

// chargeSuperSparkTx charges one Super Spark from sender to recipient, unless
// the sender already holds a live Super Spark toward them (newer than the
// pair's last closed match), which is not charged twice. The caller holds the
// per-sender spark lock.
func chargeSuperSparkTx(ctx context.Context, tx pgx.Tx, fromUserID, toUserID uuid.UUID, dailyLimit int) error {
	var already bool
	if err := tx.QueryRow(ctx, `
        SELECT EXISTS (SELECT 1 FROM dating_sparks
            WHERE from_user_id = $1 AND to_user_id = $2 AND is_super
              AND created_at > `+pairLastClosedSQL("$1::uuid", "$2::uuid")+`)`,
		fromUserID, toUserID).Scan(&already); err != nil {
		return fmt.Errorf("check existing super spark: %w", err)
	}
	if already {
		return nil
	}
	var used int
	if err := tx.QueryRow(ctx, `
        SELECT COUNT(*)::int FROM dating_super_spark_ledger
        WHERE user_id = $1 AND source = $2 AND sent_at > now() - make_interval(secs => $3)`,
		fromUserID, SuperSparkSourceDaily, SuperSparkQuotaWindow.Seconds()).Scan(&used); err != nil {
		return fmt.Errorf("count super sparks: %w", err)
	}
	source := SuperSparkSourceDaily
	if used >= dailyLimit {
		tag, err := tx.Exec(ctx, `
            UPDATE dating_super_spark_balances SET balance = balance - 1, updated_at = now()
            WHERE user_id = $1 AND balance > 0`, fromUserID)
		if err != nil {
			return fmt.Errorf("spend purchased super spark: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrSuperSparkLimited
		}
		source = SuperSparkSourcePack
	}
	if _, err := tx.Exec(ctx, `
        INSERT INTO dating_super_spark_ledger (user_id, to_user_id, source) VALUES ($1, $2, $3)`,
		fromUserID, toUserID, source); err != nil {
		return fmt.Errorf("record super spark: %w", err)
	}
	return nil
}

// SuperSparkUsage returns the daily-allowance Super Sparks the user sent
// inside the window, when the oldest of them was (nil when none) and the
// purchased balance left.
func (s *Store) SuperSparkUsage(ctx context.Context, userID uuid.UUID) (used int, oldest *time.Time, balance int, err error) {
	err = s.db.QueryRow(ctx, `
        SELECT COUNT(*)::int, min(sent_at),
               COALESCE((SELECT b.balance FROM dating_super_spark_balances b WHERE b.user_id = $1), 0)
        FROM dating_super_spark_ledger
        WHERE user_id = $1 AND source = $2 AND sent_at > now() - make_interval(secs => $3)`,
		userID, SuperSparkSourceDaily, SuperSparkQuotaWindow.Seconds()).Scan(&used, &oldest, &balance)
	if err != nil {
		return 0, nil, 0, fmt.Errorf("super spark usage: %w", err)
	}
	return used, oldest, balance, nil
}
